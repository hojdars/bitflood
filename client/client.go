package client

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"log"
	"net"

	"github.com/hojdars/bitflood/bitfield"
	"github.com/hojdars/bitflood/bittorrent"
	"github.com/hojdars/bitflood/types"
)

const PipelineLength int = 5
const ChunkSize int = 1 << 14

func Seed(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, comms types.Communication, results *types.Results) {
	log.Printf("INFO: started seeding to target=%s", conn.RemoteAddr().String())
	defer conn.Close()

	peerIdOptional := ctx.Value("peer-id")
	if peerIdOptional == nil {
		log.Fatalf("ERROR: context is missing value")
	}
	peerId, ok := peerIdOptional.(string)
	if !ok {
		log.Fatalf("ERROR: context does not contain a string")
	}

	peer, err := bittorrent.AcceptConnection(conn, *torrent, results, peerId)
	if err != nil {
		log.Printf("ERROR: error accepting bittorrent connection from target=%s", conn.RemoteAddr().String())
		comms.ConnectionEnded <- types.ConnectionEnd{Id: "unknown", Addr: conn.RemoteAddr()}
		return
	}

	log.Printf("INFO  [%s]: handshake complete", peer.ID)
	communicationLoop(ctx, conn, torrent, &peer, comms, results)
}

func Leech(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, comms types.Communication, results *types.Results) {
	log.Printf("INFO: started leeching from target=%s", conn.RemoteAddr().String())
	defer conn.Close()

	peerIdOptional := ctx.Value("peer-id")
	if peerIdOptional == nil {
		log.Fatalf("ERROR: context is missing value")
	}
	peerId, ok := peerIdOptional.(string)
	if !ok {
		log.Fatalf("ERROR: context does not contain a string")
	}

	peer, err := bittorrent.InitiateConnection(conn, *torrent, results, peerId)
	if err != nil {
		log.Printf("ERROR: error initiating bittorrent connection to target=%s, err=%s", conn.RemoteAddr().String(), err)
		comms.ConnectionEnded <- types.ConnectionEnd{Id: "unknown", Addr: conn.RemoteAddr()}
		return
	}

	log.Printf("INFO  [%s]: handshake complete", peer.ID)
	communicationLoop(ctx, conn, torrent, &peer, comms, results)
}

func communicationLoop(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, peer *types.Peer, comms types.Communication, results *types.Results) {
	msgChannel := make(chan bittorrent.PeerMessage)

	// goroutine to accept incoming messages from TCP
	go func() {
		for {
			msg, err := bittorrent.DeserializeMessage(conn)
			if err != nil {
				log.Printf("ERROR [%s]: error while receiving message from target=%s, err=%s", peer.ID, peer.Addr.String(), err)
				close(msgChannel)
				return
			}
			msgChannel <- msg
		}
	}()

	var progress pieceProgress
	var seedState seederState

	for {
		// TODO:
		// if should send keep alive, send keep alive

		// if should be uploading (= peer is interested AND i am unchoking), launch goroutine uploading the requested pieces
		if !peer.Choking && peer.Interested && len(seedState.requested) > 0 {
			log.Printf("INFO  [%s]: uploading to peer", peer.ID)
			uploadedIndex, err := uploadChunk(conn, *peer, results, &seedState)
			if err != nil {
				log.Printf("ERROR [%s]: error while uploading a chunk, err=%s", peer.ID, err)
			} else {
				seedState.requested[uploadedIndex] = seedState.requested[len(seedState.requested)-1]
				seedState.requested = seedState.requested[:len(seedState.requested)-1]
			}
		}

		// check if we have a complete piece -> verify hash, send 'have' message and send through results channel
		if progress.order != nil && progress.numDone == progress.order.Length {
			err := handlePieceComplete(conn, &progress, peer, comms)
			if err != nil {
				log.Printf("ERROR [%s]: error while handling a completed piece, err=%s", peer.ID, err)
			} else {
				// piece is done -> progress is reset
				progress = pieceProgress{}
			}
		}

		// check if we should request a piece (only request if we have the bitfield)
		if progress.order == nil && peer.Bitfield.Length == len(torrent.PieceHashes) {
			progress.order = getPiece(*peer, comms.Orders)
			if progress.order != nil {
				log.Printf("INFO  [%s]: downloading piece index=%d", peer.ID, progress.order.Index)
				progress.buf = make([]byte, progress.order.Length)
			}
		}

		// if we are interested but choked, send 'interested'
		if progress.order != nil && peer.ChokedBy && !peer.InterestedSent {
			sendInterested(peer, conn)
			log.Printf("INFO  [%s]: sent interested message", peer.ID)
		}

		// if should be requesting (= I am interested AND peer is unchoking me AND not enough requests are pipelined), send the requests
		if progress.order != nil && !peer.ChokedBy && len(progress.requests) < PipelineLength {
			fillRequests(*peer, conn, &progress)
		}

		// after this is done, block on context.Done or message incoming
		select {
		case <-ctx.Done():
			log.Printf("INFO  [%s]: seeder closed", peer.ID)
			if progress.order != nil {
				comms.Orders <- progress.order
			}
			comms.ConnectionEnded <- types.ConnectionEnd{Id: peer.ID, Addr: peer.Addr}
			return
		case msg, ok := <-msgChannel:
			if !ok {
				log.Printf("INFO  [%s]: connection to target=%s lost", peer.ID, peer.Addr.String())
				if progress.order != nil {
					comms.Orders <- progress.order
				}
				comms.ConnectionEnded <- types.ConnectionEnd{Id: peer.ID, Addr: peer.Addr}
				return
			}
			if msg.KeepAlive {
				continue
			}
			log.Printf("INFO  [%s]: received message=%s from target=%s", peer.ID, bittorrent.CodeToString(msg.Code), peer.Addr)

			err := handleMessage(msg, peer, &progress, *torrent, comms, &seedState)
			if err != nil {
				log.Printf("ERROR [%s]: error while handling message, err=%s", peer.ID, err)
			}
		case unchokedPeers, ok := <-comms.PeersToUnchoke:
			if !ok {
				log.Printf("ERROR [%s]: unchoke channel to main lost, seeder exiting", peer.ID)
				if progress.order != nil {
					comms.Orders <- progress.order
				}
				comms.ConnectionEnded <- types.ConnectionEnd{Id: peer.ID, Addr: peer.Addr}
				return
			}
			peerIncluded := false
			for _, id := range unchokedPeers {
				if id == peer.ID {
					peerIncluded = true
					break
				}
			}
			if peerIncluded {
				peer.Choking = false
				log.Printf("INFO  [%s]: unchoking", peer.ID)
			} else {
				if !peer.Choking { // only print the 'choking' message if we are currently not choking
					log.Printf("INFO  [%s]: choking", peer.ID)
				}
				peer.Choking = true
			}
		}
	}
}

func uploadChunk(conn net.Conn, peer types.Peer, results *types.Results, seedState *seederState) (int, error) {
	if len(seedState.requested) == 0 {
		return -1, fmt.Errorf("cannot upload chunk, no requests pending")
	}

	request := 0

	msg := bittorrent.PeerMessage{
		KeepAlive: false,
		Code:      bittorrent.MsgPiece,
	}

	pieceIndex := seedState.requested[request].index
	pieceStart := seedState.requested[request].start
	pieceLength := seedState.requested[request].length

	results.Lock.RLock()
	data := results.Pieces[pieceIndex].Data[pieceStart : pieceStart+pieceLength]
	msg.SerializePieceMsg(pieceIndex, pieceStart, data)
	results.Lock.RUnlock()

	msgData, err := bittorrent.SerializeMessage(msg)
	if err != nil {
		return -1, fmt.Errorf("ERROR [%s]: error while serializing 'piece' message, err=%s", peer.ID, err)
	}
	_, err = conn.Write(msgData)
	if err != nil {
		return -1, fmt.Errorf("ERROR [%s]: error while sending 'piece' message, err=%s", peer.ID, err)
	}

	return request, nil
}

func getPiece(peer types.Peer, workQueue chan *types.PieceOrder) *types.PieceOrder {
	select {
	case order, ok := <-workQueue:
		if !ok {
			return nil
		}
		got, err := peer.Bitfield.Get(order.Index)
		if err != nil {
			log.Printf("ERROR [%s]: error querying bitfield for piece, index=%d, err=%s", peer.ID, order.Index, err)
			workQueue <- order
			return nil
		}
		if !got {
			workQueue <- order
			return nil
		}
		return order
	default:
		return nil
	}
}

func sendInterested(peer *types.Peer, conn net.Conn) {
	msg := bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgInterested, Data: []byte{}}
	msgData, err := bittorrent.SerializeMessage(msg)
	if err != nil {
		log.Printf("ERROR [%s]: error while serializing 'interested' message, err=%s", peer.ID, err)
		return
	}
	_, err = conn.Write(msgData)
	if err != nil {
		log.Printf("ERROR [%s]: error while sending 'interested' message, err=%s", peer.ID, err)
		return
	}
	peer.InterestedSent = true
}

func fillRequests(peer types.Peer, conn net.Conn, progress *pieceProgress) {
	numberToSend := PipelineLength - len(progress.requests)
	for i := 0; i < numberToSend; i++ {
		if progress.nextToRequest >= progress.order.Length {
			break
		}

		len := min(ChunkSize, progress.order.Length-progress.nextToRequest)
		nextRequest := request{index: progress.order.Index, start: progress.nextToRequest, length: len}

		// craft the message
		msg := bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgRequest}
		msg.SerializeRequestMsg(nextRequest.index, nextRequest.start, nextRequest.length)

		// send over TCP
		// TODO: technically, we can create the requests in this thread and then launch goroutine to send them
		reqByte, err := bittorrent.SerializeMessage(msg)
		if err != nil {
			log.Printf("ERROR [%s]: error while serializing request message, err=%s", peer.ID, err)
			break
		}
		_, err = conn.Write(reqByte)
		if err != nil {
			log.Printf("ERROR [%s]: error while sending request message, err=%s", peer.ID, err)
			break
		}

		// then add to the request list
		progress.requests = append(progress.requests, nextRequest)
		progress.nextToRequest = nextRequest.start + ChunkSize
	}
}

func handlePieceComplete(conn net.Conn, progress *pieceProgress, peer *types.Peer, comms types.Communication) error {
	log.Printf("INFO  [%s]: piece %d download complete", peer.ID, progress.order.Index)
	hash := sha1.Sum(progress.buf)
	if !bytes.Equal(hash[:], progress.order.Hash[:]) {
		log.Printf("ERROR [%s]: hash mismatch for piece %d", peer.ID, progress.order.Index)
		comms.Orders <- progress.order
		return nil
	}

	// send 'have' message
	msgBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(msgBuf, uint32(progress.order.Index))
	msg := bittorrent.PeerMessage{
		KeepAlive: false,
		Code:      bittorrent.MsgHave,
		Data:      msgBuf,
	}

	msgData, err := bittorrent.SerializeMessage(msg)
	if err != nil {
		return fmt.Errorf("ERROR [%s]: error while serializing 'have' message, err=%s", peer.ID, err)
	}
	_, err = conn.Write(msgData)
	if err != nil {
		return fmt.Errorf("ERROR [%s]: error while sending 'have' message, err=%s", peer.ID, err)
	}

	log.Printf("INFO  [%s]: piece %d hash check OK, piece complete", peer.ID, progress.order.Index)
	comms.Results <- &types.Piece{
		Index:            progress.order.Index,
		Data:             progress.buf,
		Length:           progress.order.Length,
		DownloadedFromId: peer.ID,
	}
	peer.Downloaded += uint32(progress.numDone)

	return nil
}

func handleMessage(msg bittorrent.PeerMessage, peer *types.Peer, progress *pieceProgress, torrent types.TorrentFile, comms types.Communication, seedState *seederState) error {
	switch msg.Code {
	case bittorrent.MsgChoke:
		log.Printf("INFO  [%s]: choked", peer.ID)
		peer.ChokedBy = true
		peer.InterestedSent = false
	case bittorrent.MsgUnchoke:
		log.Printf("INFO  [%s]: unchoked", peer.ID)
		peer.ChokedBy = false
	case bittorrent.MsgInterested:
		peer.Interested = true
		comms.PeerInterested <- types.PeerInterest{Id: peer.ID, IsInterested: true}
	case bittorrent.MsgNotInterested:
		peer.Interested = false
		comms.PeerInterested <- types.PeerInterest{Id: peer.ID, IsInterested: false}
	case bittorrent.MsgHave:
		log.Printf("INFO  [%s]: peer confirmed upload of piece index=%d", peer.ID, binary.BigEndian.Uint32(msg.Data))
		return nil
	case bittorrent.MsgBitfield:
		expectedLength := len(torrent.PieceHashes) / 8
		if len(torrent.PieceHashes)%8 > 0 {
			expectedLength += 1
		}
		if len(msg.Data) != expectedLength {
			return fmt.Errorf("invalid bitfield length, received %d bytes, required %d bytes", len(msg.Data), expectedLength)
		}
		peer.Bitfield = bitfield.FromBytes(msg.Data, len(torrent.PieceHashes))
		log.Printf("INFO  [%s]: received bitfield", peer.ID)
	case bittorrent.MsgRequest:
		index, start, length, err := msg.DeserializeRequestMsg()
		if err != nil {
			return fmt.Errorf("error while parsing request message, err=%s", err)
		}
		seedState.requested = append(seedState.requested, request{index: index, start: start, length: length})
		return nil
	case bittorrent.MsgPiece:
		index, begin, data, err := msg.DeserializePieceMsg()
		if err != nil {
			return fmt.Errorf("error while parsing piece message, err=%s", err)
		}
		if index != progress.order.Index {
			return fmt.Errorf("received a chunk of a different piece, want piece index=%d, got piece index=%d", progress.order.Index, index)
		}
		err = progress.DeleteRequest(begin, len(data))
		if err != nil {
			return fmt.Errorf("error while deleting request, err=%s", err)
		}
		progress.numDone += len(data)
		if begin+len(data) > len(progress.buf) {
			return fmt.Errorf("data is too long, buf length=%d, got begin=%d and len=%d", len(progress.buf), begin, len(data))
		}
		copy(progress.buf[begin:], data)
	case bittorrent.MsgCancel:
		// TODO: Seeding-only, endgame only
		return nil
	}
	return nil
}
