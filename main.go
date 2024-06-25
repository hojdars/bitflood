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
	"github.com/hojdars/bitflood/bittorrent"
	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/types"
)

const Port int = 6881

func seed(ctx context.Context, conn net.Conn, torrent *types.TorrentFile, peerId string) {
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

	log.Printf("SEED  [%s]: received correct handshake from target=%s, peer-id=%s", peer.Addr, conn.RemoteAddr().String(), peer.Addr)

	outHandshake := bittorrent.HandshakeData{Extensions: [8]byte{}, InfoHash: torrent.InfoHash, PeerId: [20]byte([]byte(peerId))}
	outHandshakeBytes, err := bittorrent.SerializeHandshake(outHandshake)
	if err != nil {
		log.Printf("ERROR [%s]: error serializing handshake, err=%s", peer.Addr, err)
		return
	}

	_, err = conn.Write(outHandshakeBytes)
	if err != nil {
		log.Printf("ERROR [%s]: error sending handshake over TCP, err=%s", peer.Addr, err)
		return
	}
	log.Printf("SEED  [%s]: sent handshake to target=%s, starting listening loop", peer.Addr, conn.RemoteAddr().String())

	msgChannel := make(chan bittorrent.PeerMessage)

	// goroutine to accept incoming messages from TCP
	go func() {
		for {
			msg, err := bittorrent.DeserializeMessage(conn)
			if err != nil {
				log.Printf("ERROR [%s]: error while receiving message from target=%s, err=%s", peer.Addr, conn.RemoteAddr().String(), err)
				close(msgChannel)
				return
			}
			msgChannel <- msg
		}
	}()

	// TODO: So far only listens to messages and logs them, seeding needs to be implemented
	for {
		// if should be uploading (= peer is interested AND i am unchoking), launch goroutine uploading the requested pieces
		// if should be requesting (= I am interested AND peer is unchoking me AND not enough requests are pipelined), launch goroutine sending the requests
		// if should send keep alive, send keep alive

		// after this is done, block on context.Done or message incoming
		select {
		case <-ctx.Done():
			log.Printf("SEED  [%s]: seeder closed", peer.Addr)
			return
		case msg, ok := <-msgChannel:
			if !ok {
				log.Printf("SEED  [%s]: connection to target=%s lost", peer.Addr, conn.RemoteAddr().String())
				return
			}
			if msg.KeepAlive {
				continue
			}
			log.Printf("SEED  [%s]: received msg, code=%d from target=%s", peer.Addr, msg.Code, conn.RemoteAddr().String())
		}
	}
}

func listeningServer(ctx context.Context, torrent *types.TorrentFile, peerId string) {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", Port))
	if err != nil {
		log.Fatalf("encountered error listening on port=%d, error=%s", Port, err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("ERROR: accept failed")
		}

		go seed(ctx, conn, torrent, peerId)
	}
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

	log.Printf("torrent file=%s, size=%s", torrent.Name, humanize.Bytes(uint64(torrent.Length)))

	peerInfo, peerId, err := bittorrent.GetPeers(torrent, Port)
	if err != nil {
		log.Fatalf("encountered an error while retrieving peers from tracker, err=%s", err)
	}
	log.Printf("set peer-id to=%s, received %d peers, interval=%d", peerId, len(peerInfo.IPs), peerInfo.Interval)

	mainCtx, cancel := context.WithCancel(context.Background())

	go listeningServer(mainCtx, &torrent, peerId)

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
			// TODO: save everything we have to disk
			exit = true
		}

		if exit {
			break
		}
	}

	os.Exit(0)
}
