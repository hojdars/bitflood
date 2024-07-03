package main

import (
	"context"
	"crypto/sha1"
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
	"github.com/hojdars/bitflood/client"
	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/file"
	"github.com/hojdars/bitflood/types"
)

const Port int = 6881

func listeningServer(ctx context.Context, torrent *types.TorrentFile, peerId string, workQueue chan *types.PieceOrder, results chan *types.Piece) {
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

func connectToPeer(ctx context.Context, torrent *types.TorrentFile, peerId string, workQueue chan *types.PieceOrder, peerInfo types.PeerInformation, peerIndex int, results chan *types.Piece) error {
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

func savePartialFiles(torrent types.TorrentFile, results []*types.Piece) error {
	fileNumber := (len(torrent.PieceHashes) / 1000) + 1
	files := make([]*os.File, fileNumber)
	for i := 0; i < fileNumber; i += 1 {
		filename := fmt.Sprintf("%s.%d.part", torrent.Name[0:20], i)
		fp, err := os.Create(filename)
		if err != nil {
			return fmt.Errorf("error while opening file=%s, err=%s", filename, err)
		}
		files[i] = fp
	}

	for _, piece := range results {
		if piece == nil {
			continue
		}

		fileIndex := piece.Index / 1000
		bytes := piece.Serialize()
		_, err := files[fileIndex].Write(bytes)
		if err != nil {
			return fmt.Errorf("error while writing piece id=%d, err=%s", piece.Index, err)
		}
	}

	return nil
}

func loadPiecesFromPartialFiles(torrent types.TorrentFile, pieces []*types.Piece, resultBitfield *bitfield.Bitfield) (int, error) {
	numberOfPieces := 0
	fileNumber := (len(torrent.PieceHashes) / 1000) + 1
	for i := 0; i < fileNumber; i += 1 {
		filename := fmt.Sprintf("%s.%d.part", torrent.Name[0:20], i)
		numberOfPiecesInFile := 0

		if _, err := os.Stat(filename); err != nil {
			continue
		}

		pfile, err := os.Open(filename)
		if err != nil {
			return numberOfPieces, fmt.Errorf("cannot open file=%s, err=%s", filename, err)
		}

		res, err := file.ReadPartialFile(pfile, resultBitfield)
		if err != nil {
			return numberOfPieces, fmt.Errorf("error while reading partial file, name=%s, err=%s", filename, err)
		}

		for _, p := range res {
			hash := sha1.Sum(p.Data)
			if hash != torrent.PieceHashes[p.Index] {
				return numberOfPieces, fmt.Errorf("hash mismatch for piece=%d, want=%s, got=%s", p.Index, string(torrent.PieceHashes[p.Index][:]), string(hash[:]))
			}
			loadedPiece := p
			pieces[loadedPiece.Index] = &loadedPiece
			numberOfPieces += 1
			numberOfPiecesInFile += 1
			err := resultBitfield.Set(loadedPiece.Index, true)
			if err != nil {
				return numberOfPieces, fmt.Errorf("error setting true for bit %d, err=%s", p.Index, err)
			}
			log.Printf("loaded piece index=%d", loadedPiece.Index)
		}

		log.Printf("loaded %d pieces from file %s", numberOfPiecesInFile, filename)
	}
	return numberOfPieces, nil
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

	// load the file or PartialFiles, verify all pieces hashes, create BitField
	results := make([]*types.Piece, len(torrent.PieceHashes))
	resultBitfield := bitfield.New(len(torrent.PieceHashes))
	piecesDone, err := loadPiecesFromPartialFiles(torrent, results, &resultBitfield)
	if err != nil {
		log.Fatalf("encountered an error while reading partial files, err=%s", err)
	}

	peerInfo, peerId, err := bittorrent.GetPeers(torrent, Port)
	if err != nil {
		log.Fatalf("encountered an error while retrieving peers from tracker, err=%s", err)
	}
	log.Printf("set peer-id to=%s, received %d peers, interval=%d", peerId, len(peerInfo.IPs), peerInfo.Interval)

	// TODO [MVP]: fill 'workQueue' with each piece
	workQueue := make(chan *types.PieceOrder, len(torrent.PieceHashes))
	requests := []int{0, 1, 1001, 1003, 2005, 2024}
	for _, r := range requests {
		have, err := resultBitfield.Get(r)
		if err != nil {
			log.Fatalf("encountered error while checking bitfield, err=%s", err)
		}
		if have {
			continue
		}
		p := &types.PieceOrder{Index: r, Length: torrent.PieceLength, Hash: torrent.PieceHashes[r]}
		workQueue <- p
	}

	mainCtx, cancel := context.WithCancel(context.Background())

	resultChannel := make(chan *types.Piece, len(torrent.PieceHashes))

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
	if piecesDone != len(torrent.PieceHashes) {
		err := savePartialFiles(torrent, results)
		if err != nil {
			log.Printf("ERROR: encountered error while saving partial files, err=%s", err)
		}
	} else {
		// TODO [MVP]: If file is complete, write the complete file and not PartialFile
	}

	os.Exit(0)
}
