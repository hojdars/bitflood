package client

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/hojdars/bitflood/bitfield"
	"github.com/hojdars/bitflood/bittorrent"
	"github.com/hojdars/bitflood/types"
)

const pipelineLength int = 5
const chunkSize int = 1 << 14

func seed(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, comms types.Communication, results *types.Results) {
	slog.Info("started seeding to target", slog.String("target", conn.RemoteAddr().String()))
	defer conn.Close()

	peerIdOptional := ctx.Value("peer-id")
	if peerIdOptional == nil {
		slog.Error("conext is missing the 'peer-id' value")
		os.Exit(0)
	}
	peerId, ok := peerIdOptional.(string)
	if !ok {
		slog.Error("'peer-id' value in conext is not a string")
		os.Exit(0)
	}

	peer, err := bittorrent.AcceptConnection(conn, *torrent, results, peerId)
	if err != nil {
		slog.Error("error accepting bittorrent connection", slog.String("target", conn.RemoteAddr().String()), slog.String("err", err.Error()))
		comms.ConnectionEnded <- types.ConnectionEnd{Id: "unknown", Addr: conn.RemoteAddr()}
		return
	}

	communicationLoop(ctx, conn, torrent, &peer, comms, results)
}

func leech(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, comms types.Communication, results *types.Results) {
	slog.Info("started leeching from target", slog.String("target", conn.RemoteAddr().String()))
	defer conn.Close()

	peerIdOptional := ctx.Value("peer-id")
	if peerIdOptional == nil {
		slog.Error("conext is missing the 'peer-id' value")
		os.Exit(0)
	}
	peerId, ok := peerIdOptional.(string)
	if !ok {
		slog.Error("'peer-id' value in conext is not a string")
		os.Exit(0)
	}

	peer, err := bittorrent.InitiateConnection(conn, *torrent, results, peerId)
	if err != nil {
		slog.Error("error initiating bittorrent connection", slog.String("target", conn.RemoteAddr().String()), slog.String("err", err.Error()))
		comms.ConnectionEnded <- types.ConnectionEnd{Id: "unknown", Addr: conn.RemoteAddr()}
		return
	}

	communicationLoop(ctx, conn, torrent, &peer, comms, results)
}

func communicationLoop(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, peer *types.Peer, comms types.Communication, results *types.Results) {
	logger := slog.With(slog.String("peer-id", peer.ID))
	logger.Info("handshake complete", slog.String("addr", peer.Addr.String()))

	msgChannel := make(chan bittorrent.PeerMessage)

	// goroutine to accept incoming messages from TCP
	go func() {
		for {
			msg, err := bittorrent.DeserializeMessage(conn)
			if err != nil {
				logger.Error("error while receiving message", slog.String("target", peer.Addr.String()), slog.String("err", err.Error()))
				close(msgChannel)
				return
			}
			msgChannel <- msg
		}
	}()

	var progress pieceProgress
	var seedState seederState

	for {
		// TODO: send keep alive

		// if should be uploading (= peer is interested AND i am unchoking), launch goroutine uploading the requested pieces
		if !peer.Choking && peer.Interested && len(seedState.requested) > 0 {
			logger.Info("uploading to peer")
			uploadedIndex, err := uploadChunk(conn, *peer, results, &seedState)
			if err != nil {
				logger.Error("error while uploading a chunk", slog.String("err", err.Error()))
			} else {
				seedState.requested[uploadedIndex] = seedState.requested[len(seedState.requested)-1]
				seedState.requested = seedState.requested[:len(seedState.requested)-1]
			}
		}

		// check if we have a complete piece -> verify hash, send 'have' message and send through results channel
		if progress.order != nil && progress.numDone == progress.order.Length {
			err := handlePieceComplete(conn, &progress, peer, comms)
			if err != nil {
				logger.Error("error while handling a completed piece", slog.String("err", err.Error()))
			} else {
				// piece is done -> progress is reset
				progress = pieceProgress{}
			}
		}

		// check if we should request a piece (only request if we have the bitfield)
		if progress.order == nil && peer.Bitfield.Length == len(torrent.PieceHashes) {
			progress.order = getPiece(*peer, comms.Orders)
			if progress.order != nil {
				progress.buf = make([]byte, progress.order.Length)
			}
		}

		// if we are interested but choked, send 'interested'
		if progress.order != nil && peer.ChokedBy && !peer.InterestedSent {
			sendInterested(peer, conn)
			logger.Info("sent interested message")
		}

		// if should be requesting (= I am interested AND peer is unchoking me AND not enough requests are pipelined), send the requests
		if progress.order != nil && !peer.ChokedBy && len(progress.requests) < pipelineLength {
			fillRequests(*peer, conn, &progress)
		}

		// after this is done, block on context.Done or message incoming
		select {
		case <-ctx.Done():
			logger.Info("seeder closed", slog.String("addr", peer.Addr.String()))
			if progress.order != nil {
				comms.Orders <- progress.order
			}
			comms.ConnectionEnded <- types.ConnectionEnd{Id: peer.ID, Addr: peer.Addr}
			return
		case msg, ok := <-msgChannel:
			if !ok {
				logger.Info("connection lost", slog.String("target", peer.Addr.String()))
				if progress.order != nil {
					comms.Orders <- progress.order
				}
				comms.ConnectionEnded <- types.ConnectionEnd{Id: peer.ID, Addr: peer.Addr}
				return
			}
			if msg.KeepAlive {
				continue
			}

			err := handleMessage(msg, peer, &progress, *torrent, comms, &seedState)
			if err != nil {
				logger.Error("error while handling message", slog.String("err", err.Error()))
			}
		case unchokedPeers, ok := <-comms.PeersToUnchoke:
			if !ok {
				logger.Error("unchoke channel to main lost, seeder exiting")
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
				if peer.Choking { // only send 'unchoke' message if we are currently choking
					logger.Info("unchoking")
					sendChokeChangeMessage(false, peer, conn)
				}
				peer.Choking = false
			} else {
				if !peer.Choking { // only send 'choke' message if we are currently not choking
					logger.Info("choking")
					sendChokeChangeMessage(true, peer, conn)
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

	msg := bittorrent.PeerMessage{
		KeepAlive: false,
		Code:      bittorrent.MsgPiece,
	}

	// index '0' means 'take the first request made (FIFO)
	pieceIndex := seedState.requested[0].index
	pieceStart := seedState.requested[0].start
	pieceLength := seedState.requested[0].length

	if pieceIndex >= len(results.Pieces) {
		return -1, fmt.Errorf("ERROR [%s]: requested piece number %d does not exist", peer.ID, pieceIndex)
	}
	if pieceStart+pieceLength > len(results.Pieces[pieceIndex].Data) {
		return -1, fmt.Errorf("ERROR [%s]: requested data out of bounds, requested data until %dB, piece is only %dB", peer.ID, pieceStart+pieceLength, len(results.Pieces[pieceIndex].Data))
	}

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

	return 0, nil
}

func getPiece(peer types.Peer, workQueue chan *types.PieceOrder) *types.PieceOrder {
	select {
	case order, ok := <-workQueue:
		if !ok {
			return nil
		}
		got, err := peer.Bitfield.Get(order.Index)
		if err != nil {
			slog.Error("error while handling a completed piece", slog.String("peer-id", peer.ID), slog.Int("index", order.Index), slog.String("err", err.Error()))
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
		slog.Error("error while serializing 'interested' message", slog.String("peer-id", peer.ID), slog.String("err", err.Error()))
		return
	}
	_, err = conn.Write(msgData)
	if err != nil {
		slog.Error("error while sending 'interested' message", slog.String("peer-id", peer.ID), slog.String("err", err.Error()))
		return
	}
	peer.InterestedSent = true
}

func fillRequests(peer types.Peer, conn net.Conn, progress *pieceProgress) {
	numberToSend := pipelineLength - len(progress.requests)
	for i := 0; i < numberToSend; i++ {
		if progress.nextToRequest >= progress.order.Length {
			break
		}

		len := min(chunkSize, progress.order.Length-progress.nextToRequest)
		nextRequest := request{index: progress.order.Index, start: progress.nextToRequest, length: len}

		// craft the message
		msg := bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgRequest}
		msg.SerializeRequestMsg(nextRequest.index, nextRequest.start, nextRequest.length)

		// send over TCP
		// TODO: technically, we can create the requests in this thread and then launch goroutine to send them
		reqByte, err := bittorrent.SerializeMessage(msg)
		if err != nil {
			slog.Error("error while serializing request message", slog.String("peer-id", peer.ID), slog.String("err", err.Error()))
			break
		}
		_, err = conn.Write(reqByte)
		if err != nil {
			slog.Error("error while sending request message", slog.String("peer-id", peer.ID), slog.String("err", err.Error()))
			break
		}

		// then add to the request list
		progress.requests = append(progress.requests, nextRequest)
		progress.nextToRequest = nextRequest.start + chunkSize
	}
}

func handlePieceComplete(conn net.Conn, progress *pieceProgress, peer *types.Peer, comms types.Communication) error {
	hash := sha1.Sum(progress.buf)
	if !bytes.Equal(hash[:], progress.order.Hash[:]) {
		slog.Error("hash mismatch for piece", slog.String("peer-id", peer.ID), slog.Int("piece", progress.order.Index))
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
	logger := slog.With(slog.String("peer-id", peer.ID))

	switch msg.Code {
	case bittorrent.MsgChoke:
		logger.Info("choked")
		peer.ChokedBy = true
		peer.InterestedSent = false
	case bittorrent.MsgUnchoke:
		logger.Info("unchoked")
		peer.ChokedBy = false
	case bittorrent.MsgInterested:
		peer.Interested = true
		comms.PeerInterested <- types.PeerInterest{Id: peer.ID, IsInterested: true}
		logger.Info("peer is interested")
	case bittorrent.MsgNotInterested:
		peer.Interested = false
		comms.PeerInterested <- types.PeerInterest{Id: peer.ID, IsInterested: false}
		logger.Info("peer is not interested")
	case bittorrent.MsgHave:
		pieceIndex := int(binary.BigEndian.Uint32(msg.Data))
		logger.Info("peer confirmed upload of piece", slog.Int("index", pieceIndex))
		comms.Uploaded <- pieceIndex
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
		logger.Info("received bitfield")
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

func sendChokeChangeMessage(choking bool, peer *types.Peer, conn net.Conn) {
	var code byte
	if choking {
		code = bittorrent.MsgChoke
	} else {
		code = bittorrent.MsgUnchoke
	}
	msg := bittorrent.PeerMessage{KeepAlive: false, Code: code, Data: []byte{}}
	msgData, err := bittorrent.SerializeMessage(msg)
	if err != nil {
		slog.Error("error while serializing 'choke/unchoke' message", slog.String("peer-id", peer.ID), slog.String("err", err.Error()))
		return
	}
	_, err = conn.Write(msgData)
	if err != nil {
		slog.Error("error while sending 'choke/unchoke' message", slog.String("peer-id", peer.ID), slog.String("err", err.Error()))
		return
	}
}
