package main

import (
	"context"
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

type request struct {
	start  int
	end    int
	length int
}

type pieceProgress struct {
	order    *pieceOrder
	buf      []byte
	numDone  int       // how many bytes are downloaded into 'buf'
	requests []request // queue for requests
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
	log.Printf("SEED: started a seeding to target=%s", conn.RemoteAddr().String())
	defer conn.Close()

	inHandshake, err := bittorrent.DeserializeHandshake(conn)
	if err != nil {
		log.Printf("ERROR: failed handshake from target=%s", conn.RemoteAddr().String())
		return
	}

	peer := types.Peer{
		Downloaded: 0,
		ChokedBy:   true,
		Choking:    true,
		Interested: false,
		ID:         string(inHandshake.PeerId[:]),
		Addr:       conn.RemoteAddr(),
	}

	log.Printf("SEED  [%s]: received correct handshake from target=%s, peer-id=%s", peer.ID, peer.Addr.String(), peer.Addr)

	outHandshake := bittorrent.HandshakeData{Extensions: [8]byte{}, InfoHash: torrent.InfoHash, PeerId: [20]byte([]byte(peerId))}
	outHandshakeBytes, err := bittorrent.SerializeHandshake(outHandshake)
	if err != nil {
		log.Printf("ERROR [%s]: error serializing handshake, err=%s", peer.ID, err)
		return
	}

	_, err = conn.Write(outHandshakeBytes)
	if err != nil {
		log.Printf("ERROR [%s]: error sending handshake over TCP, err=%s", peer.ID, err)
		return
	}
	log.Printf("SEED  [%s]: sent handshake to target=%s, starting listening loop", peer.ID, peer.Addr.String())

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
		order:    nil,
		buf:      nil,
		numDone:  0,
		requests: nil,
	}

	// TODO: So far only listens to messages and logs them, seeding needs to be implemented
	for {
		// TODO:
		// if should send keep alive, send keep alive
		// if should be uploading (= peer is interested AND i am unchoking), launch goroutine uploading the requested pieces

		// Check if we should request a piece (only request if we have the bitfield)
		if progress.order == nil && peer.Bitfield.Length == len(torrent.PieceHashes) {
			progress.order = getPiece(peer, workQueue)
			if progress.order != nil {
				log.Printf("SEED  [%s]: downloading piece index=%d", peer.ID, progress.order.index)
				progress.buf = make([]byte, 0, progress.order.length)
			}
		}

		// [MVP] if should be requesting (= I am interested AND peer is unchoking me AND not enough requests are pipelined), launch goroutine sending the requests
		// [MVP] if pieceProgress is 100%, we have a result

		// after this is done, block on context.Done or message incoming
		select {
		case <-ctx.Done():
			log.Printf("SEED  [%s]: seeder closed", peer.ID)
			return
		case msg, ok := <-msgChannel:
			if !ok {
				log.Printf("SEED  [%s]: connection to target=%s lost", peer.ID, peer.Addr.String())
				return
			}
			if msg.KeepAlive {
				continue
			}
			log.Printf("SEED  [%s]: received msg, code=%d from target=%s", peer.ID, msg.Code, peer.Addr)

			err := handleMessage(msg, &peer, &progress, *torrent)
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

func handleMessage(msg bittorrent.PeerMessage, peer *types.Peer, progress *pieceProgress, torrent types.TorrentFile) error {
	switch msg.Code {
	case bittorrent.MsgChoke:
		peer.ChokedBy = true
	case bittorrent.MsgUnchoke:
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
		log.Printf("SEED  [%s]: received bitfield", peer.ID)
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

	mainCtx, cancel := context.WithCancel(context.Background())
	go listeningServer(mainCtx, &torrent, peerId, workQueue)

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
