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
	"github.com/hojdars/bitflood/client"
	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/types"
)

const Port int = 6881

func listeningServer(ctx context.Context, torrent *types.TorrentFile, peerId string, workQueue chan *types.PieceOrder, results chan *types.PieceResult) {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", Port))
	if err != nil {
		log.Fatalf("encountered error listening on port=%d, error=%s", Port, err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("ERROR: accept failed")
		}

		go client.Seed(ctx, conn, torrent, peerId, workQueue, results)
	}
}

func connectToPeer(ctx context.Context, torrent *types.TorrentFile, peerId string, workQueue chan *types.PieceOrder, peerInfo types.PeerInformation, peerIndex int, results chan *types.PieceResult) error {
	peerAddr := fmt.Sprintf("%s:%d", peerInfo.IPs[peerIndex].String(), peerInfo.Ports[peerIndex])
	log.Printf("connecting to %s", peerAddr)

	var d net.Dialer
	d.Timeout = time.Second * 2

	conn, err := d.DialContext(ctx, "tcp", peerAddr)
	if err != nil {
		return fmt.Errorf("connection to peer=%s failed, err=%s", peerAddr, err)
	}

	go client.Leech(ctx, conn, torrent, peerId, workQueue, results)

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

	// TODO [MVP]: Load the files, verify all pieces hashes

	peerInfo, peerId, err := bittorrent.GetPeers(torrent, Port)
	if err != nil {
		log.Fatalf("encountered an error while retrieving peers from tracker, err=%s", err)
	}
	log.Printf("set peer-id to=%s, received %d peers, interval=%d", peerId, len(peerInfo.IPs), peerInfo.Interval)

	workQueue := make(chan *types.PieceOrder, len(torrent.PieceHashes))
	// TODO [MVP]: fill 'workQueue' with each piece
	p := &types.PieceOrder{Index: 0, Length: torrent.PieceLength, Hash: torrent.PieceHashes[0]}
	workQueue <- p
	p = &types.PieceOrder{Index: 1, Length: torrent.PieceLength, Hash: torrent.PieceHashes[1]}
	workQueue <- p

	mainCtx, cancel := context.WithCancel(context.Background())

	resultChannel := make(chan *types.PieceResult, len(torrent.PieceHashes))
	results := make([]*types.PieceResult, len(torrent.PieceHashes))
	piecesDone := 0

	go listeningServer(mainCtx, &torrent, peerId, workQueue, resultChannel)

	// WIP: start one thread
	err = connectToPeer(mainCtx, &torrent, peerId, workQueue, peerInfo, 0, resultChannel)
	for i := 1; err != nil; i += 1 {
		log.Printf("ERROR: encountered an error connecting to target=%s, err=%s", peerInfo.IPs[i-1], err)
		err = connectToPeer(mainCtx, &torrent, peerId, workQueue, peerInfo, i, resultChannel)
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
			exit = true
		case piece := <-resultChannel:
			results[piece.Index] = piece
			piecesDone += 1
			log.Printf("downloaded %d/%d pieces, %f%%", piecesDone, len(torrent.PieceHashes), float32(piecesDone)/float32(len(torrent.PieceHashes)))
		}

		if exit {
			break
		}
	}

	log.Printf("saving %d pieces", piecesDone)
	// TODO [MVP]: Save the file

	os.Exit(0)
}
