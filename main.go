package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/hojdars/bitflood/bitfield"
	"github.com/hojdars/bitflood/bittorrent"
	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/types"
)

const Port int = 6881
const PipelineLength int = 5
const ChunkSize int = 1 << 14

type request struct {
	start  int
	length int
}

type pieceProgress struct {
	order         *pieceOrder
	buf           []byte
	numDone       int       // how many bytes are downloaded into 'buf'
	requests      []request // queue for requests
	nextToRequest int
}

func (p *pieceProgress) DeleteRequest(start, length int) error {
	for i, r := range p.requests {
		if r.start == start && r.length == length {
			// delete the element
			p.requests[i] = p.requests[len(p.requests)-1]
			p.requests = p.requests[:len(p.requests)-1]
			return nil
		}
	}
	return fmt.Errorf("could not find request with start=%d, length=%d", start, length)
}

func seed(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, peerId string, workQueue chan *pieceOrder) {
	log.Printf("INFO: started seeding to target=%s", conn.RemoteAddr().String())
	defer conn.Close()

	peer, err := bittorrent.AcceptConnection(conn, *torrent, peerId)
	if err != nil {
		log.Printf("ERROR: error accepting bittorrent connection from target=%s", conn.RemoteAddr().String())
		return
	}

	log.Printf("INFO  [%s]: handshake complete", peer.ID)
	communicationLoop(ctx, conn, torrent, &peer, workQueue)
}

func leech(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, peerId string, workQueue chan *pieceOrder) {
	log.Printf("INFO: started leeching from target=%s", conn.RemoteAddr().String())
	defer conn.Close()

	peer, err := bittorrent.InitiateConnection(conn, *torrent, peerId)
	if err != nil {
		log.Printf("ERROR: error initiating bittorrent connection to target=%s", conn.RemoteAddr().String())
		return
	}

	log.Printf("INFO [%s]: handshake complete", peer.ID)
	communicationLoop(ctx, conn, torrent, &peer, workQueue)
}

func communicationLoop(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, peer *types.Peer, workQueue chan *pieceOrder) {
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

	progress := pieceProgress{
		order:         nil,
		buf:           nil,
		numDone:       0,
		requests:      nil,
		nextToRequest: 0,
	}

	// TODO: So far only listens to messages and logs them, seeding needs to be implemented
	for {
		// TODO:
		// if should send keep alive, send keep alive
		// receive 'request' and 'interested' messages
		// if should be uploading (= peer is interested AND i am unchoking), launch goroutine uploading the requested pieces

		// if pieceProgress is 100%, we have a result -> verify hash, send 'have' message and send through results channel
		if progress.order != nil && progress.numDone == progress.order.length {
			err := handlePieceComplete(conn, &progress, peer, workQueue)
			if err != nil {
				log.Printf("ERROR [%s]: error while handling a completed piece, err=%s", peer.ID, err)
			} else {
				// piece is done -> progress is reset
				progress = pieceProgress{
					order:         nil,
					buf:           nil,
					numDone:       0,
					requests:      nil,
					nextToRequest: 0,
				}
			}
		}

		// check if we should request a piece (only request if we have the bitfield)
		if progress.order == nil && peer.Bitfield.Length == len(torrent.PieceHashes) {
			progress.order = getPiece(*peer, workQueue)
			if progress.order != nil {
				log.Printf("INFO  [%s]: downloading piece index=%d", peer.ID, progress.order.index)
				progress.buf = make([]byte, progress.order.length)
			}
		}

		// if we are interested but choked, send 'interested'
		if progress.order != nil && peer.ChokedBy && !peer.InterestedSent {
			sendInterested(peer, conn)
		}

		// if should be requesting (= I am interested AND peer is unchoking me AND not enough requests are pipelined), send the requests
		if progress.order != nil && !peer.ChokedBy && len(progress.requests) < PipelineLength {
			fillRequests(*peer, conn, &progress)
		}

		// after this is done, block on context.Done or message incoming
		select {
		case <-ctx.Done():
			log.Printf("INFO  [%s]: seeder closed", peer.ID)
			return
		case msg, ok := <-msgChannel:
			if !ok {
				log.Printf("INFO  [%s]: connection to target=%s lost", peer.ID, peer.Addr.String())
				return
			}
			if msg.KeepAlive {
				continue
			}
			log.Printf("INFO  [%s]: received msg, code=%d from target=%s", peer.ID, msg.Code, peer.Addr)

			err := handleMessage(msg, peer, &progress, *torrent)
			if err != nil {
				log.Printf("ERROR [%s]: error while handling message, err=%s", peer.ID, err)
			}
		}
	}
}

func getPiece(peer types.Peer, workQueue chan *pieceOrder) *pieceOrder {
	select {
	case order, ok := <-workQueue:
		if !ok {
			return nil
		}
		got, err := peer.Bitfield.Get(order.index)
		if err != nil {
			log.Printf("ERROR [%s]: error querying bitfield for piece, index=%d, err=%s", peer.ID, order.index, err)
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
		if progress.nextToRequest >= progress.order.length {
			break
		}

		len := min(ChunkSize, progress.order.length-progress.nextToRequest)
		nextRequest := request{start: progress.nextToRequest, length: len}

		// craft the message
		msg := bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgRequest}
		msg.SerializeRequestData(progress.order.index, nextRequest.start, nextRequest.length)

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
		log.Printf("INFO  [%s]: requested chunk index=%d, start=%d, len=%d", peer.ID, progress.order.index, nextRequest.start, nextRequest.length)
	}
}

func handlePieceComplete(conn net.Conn, progress *pieceProgress, peer *types.Peer, workQueue chan *pieceOrder) error {
	log.Printf("INFO  [%s]: piece %d download complete", peer.ID, progress.order.index)
	hash := sha1.Sum(progress.buf)
	if !bytes.Equal(hash[:], progress.order.hash[:]) {
		log.Printf("ERROR [%s]: hash mismatch for piece %d", peer.ID, progress.order.index)
		workQueue <- progress.order
		return nil
	}

	// send 'have' message
	msgBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(msgBuf, uint32(progress.order.index))
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

	log.Printf("INFO  [%s]: piece %d hash check verified, piece complete", peer.ID, progress.order.index)
	// TODO: resultQueue <- progres
	peer.Downloaded += uint32(progress.numDone)

	return nil
}

func handleMessage(msg bittorrent.PeerMessage, peer *types.Peer, progress *pieceProgress, torrent types.TorrentFile) error {
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
	case bittorrent.MsgNotInterested:
		peer.Interested = false
	case bittorrent.MsgHave:
		// TODO: Seeding-only, peer confirmed to have received the piece
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
		// TODO: Seeding-only, peer is requesting a piece
		return nil
	case bittorrent.MsgPiece:
		index, begin, data, err := msg.DeserializePiece()
		if err != nil {
			return fmt.Errorf("error while parsing piece message, err=%s", err)
		}
		if index != progress.order.index {
			return fmt.Errorf("received a chunk of a different piece, want piece index=%d, got piece index=%d", progress.order.index, index)
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

func listeningServer(ctx context.Context, torrent *types.TorrentFile, peerId string, workQueue chan *pieceOrder) {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", Port))
	if err != nil {
		log.Fatalf("encountered error listening on port=%d, error=%s", Port, err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("ERROR: accept failed")
		}

		go seed(ctx, conn, torrent, peerId, workQueue)
	}
}

type pieceOrder struct {
	index  int
	length int
	hash   [20]byte
}

func connectToPeer(ctx context.Context, torrent *types.TorrentFile, peerId string, workQueue chan *pieceOrder, peerInfo types.PeerInformation, peerIndex int) error {
	peerAddr := fmt.Sprintf("%s:%d", peerInfo.IPs[peerIndex].String(), peerInfo.Ports[peerIndex])
	log.Printf("connecting to %s", peerAddr)

	var d net.Dialer
	d.Timeout = time.Second * 2

	conn, err := d.DialContext(ctx, "tcp", peerAddr)
	if err != nil {
		return fmt.Errorf("connection to peer=%s failed, err=%s", peerAddr, err)
	}

	go leech(ctx, conn, torrent, peerId, workQueue)

	return nil
}

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("invalid number of arguments, expected 2, got %v", len(os.Args))
	}

	filename := os.Args[1]

	if _, err := os.Stat(filename); err != nil {
		log.Fatalf("file does not exist, file=%s", filename)
	}

	log.Printf("started on file=%s\n", filename)

	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("cannot open file, err=%s", err)
	}

	torrent, err := decode.DecodeTorrentFile(file)
	if err != nil {
		log.Fatalf("encountered an error during .torrent file decoding, err=%s", err)
	}

	if torrent.Length == 0 {
		// TODO: specification requires either 'length' or 'key files', implement 'key files'
		log.Fatalf("key 'length' is missing, unsupported .torrent file")
	}

	log.Printf("torrent file=%s, size=%s, pieces=%d", torrent.Name, humanize.Bytes(uint64(torrent.Length)), len(torrent.PieceHashes))

	peerInfo, peerId, err := bittorrent.GetPeers(torrent, Port)
	if err != nil {
		log.Fatalf("encountered an error while retrieving peers from tracker, err=%s", err)
	}
	log.Printf("set peer-id to=%s, received %d peers, interval=%d", peerId, len(peerInfo.IPs), peerInfo.Interval)

	workQueue := make(chan *pieceOrder, len(torrent.PieceHashes))
	// [MVP] TODO: fill 'workQueue' with each piece
	p := &pieceOrder{index: 0, length: torrent.PieceLength, hash: torrent.PieceHashes[0]}
	workQueue <- p
	p = &pieceOrder{index: 1, length: torrent.PieceLength, hash: torrent.PieceHashes[1]}
	workQueue <- p

	mainCtx, cancel := context.WithCancel(context.Background())
	go listeningServer(mainCtx, &torrent, peerId, workQueue)

	// WIP: start one thread
	err = connectToPeer(mainCtx, &torrent, peerId, workQueue, peerInfo, 0)
	for i := 0; err != nil; i += 1 {
		err = connectToPeer(mainCtx, &torrent, peerId, workQueue, peerInfo, i)
	}

	signalChannel := make(chan os.Signal, 2)
	signal.Notify(signalChannel, os.Interrupt, syscall.SIGINT)
	for {
		exit := false
		select {
		// TODO: case: updating tracker every torrent.Interval
		// TODO: case: choke algorithm tick every 10 seconds
		case <-signalChannel:
			log.Printf("SIGINT caught, terminating")
			cancel()
			time.Sleep(time.Second)
			// [MVP] TODO: save everything we have to disk by implementing 'pieceResult' which the downloaders send back
			exit = true
		}

		if exit {
			break
		}
	}

	os.Exit(0)
}
