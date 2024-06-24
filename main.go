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

	remotePeerId := string(inHandshake.PeerId[:])
	log.Printf("SEED  [%s]: received correct handshake from target=%s, peer-id=%s", remotePeerId, conn.RemoteAddr().String(), remotePeerId)

	outHandshake := bittorrent.HandshakeData{Extensions: [8]byte{}, InfoHash: torrent.InfoHash, PeerId: [20]byte([]byte(peerId))}
	outHandshakeBytes, err := bittorrent.SerializeHandshake(outHandshake)
	if err != nil {
		log.Printf("ERROR [%s]: error serializing handshake, err=%s", remotePeerId, err)
		return
	}

	_, err = conn.Write(outHandshakeBytes)
	if err != nil {
		log.Printf("ERROR [%s]: error sending handshake over TCP, err=%s", remotePeerId, err)
		return
	}
	log.Printf("SEED  [%s]: sent handshake to target=%s, starting listening loop", remotePeerId, conn.RemoteAddr().String())

	msgChannel := make(chan bittorrent.PeerMessage)

	// goroutine to accept incoming messages from TCP
	go func() {
		for {
			msg, err := bittorrent.DeserializeMessage(conn)
			if err != nil {
				log.Printf("ERROR [%s]: error while receiving message from target=%s, err=%s", remotePeerId, conn.RemoteAddr().String(), err)
				close(msgChannel)
				return
			}
			msgChannel <- msg
		}
	}()

	// TODO: So far only listens to messages and logs them, seeding needs to be implemented
	for {
		select {
		case <-ctx.Done():
			log.Printf("SEED  [%s]: seeder closed", remotePeerId)
			return
		case msg, ok := <-msgChannel:
			if !ok {
				log.Printf("SEED  [%s]: connection to target=%s lost", remotePeerId, conn.RemoteAddr().String())
				return
			}
			if msg.KeepAlive {
				continue
			}
			log.Printf("SEED  [%s]: received msg, code=%d from target=%s", remotePeerId, msg.Code, conn.RemoteAddr().String())
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
		case <-signalChannel:
			log.Printf("SIGINT caught, terminating")
			cancel()
			time.Sleep(time.Second)
			exit = true
		}

		if exit {
			break
		}
	}

	os.Exit(0)
}
