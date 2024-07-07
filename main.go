package main

import (
	"context"
	"crypto/sha1"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
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

const ChokeAlgorithmTick int = 10
const Port int = 6881

func listeningServer(ctx context.Context, torrent *types.TorrentFile, comms types.Communication, results *types.Results) {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", Port))
	if err != nil {
		log.Fatalf("encountered error listening on port=%d, error=%s", Port, err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("ERROR: accept failed")
		}

		go client.Seed(ctx, conn, torrent, comms, results)
	}
}

func connectToPeer(ctx context.Context, torrent *types.TorrentFile, comms types.Communication, results *types.Results, peerInfo types.PeerInformation, peerIndex int) error {
	peerAddr := fmt.Sprintf("%s:%d", peerInfo.IPs[peerIndex].String(), peerInfo.Ports[peerIndex])
	log.Printf("connecting to %s", peerAddr)

	var d net.Dialer
	d.Timeout = time.Second * 2

	conn, err := d.DialContext(ctx, "tcp", peerAddr)
	if err != nil {
		return fmt.Errorf("connection to peer=%s failed, err=%s", peerAddr, err)
	}

	go client.Leech(ctx, conn, torrent, comms, results)

	return nil
}

func savePartialFiles(torrent types.TorrentFile, results *types.Results, savedPieces *bitfield.Bitfield) error {
	fileNumber := (len(torrent.PieceHashes) / 1000) + 1
	files := make([]*os.File, fileNumber)
	for i := 0; i < fileNumber; i += 1 {
		filename := fmt.Sprintf("%s.%d.part", torrent.Name[0:20], i)
		fp, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if err != nil {
			return fmt.Errorf("error while opening file=%s, err=%s", filename, err)
		}
		files[i] = fp
	}

	for _, piece := range results.Pieces {
		if piece == nil {
			continue
		}
		alreadySaved, err := savedPieces.Get(piece.Index)
		if err != nil {
			return fmt.Errorf("error while verifying piece index in bitfield, err=%s", err)
		}
		if alreadySaved {
			continue
		}

		fileIndex := piece.Index / 1000
		bytes := piece.Serialize()
		_, err = files[fileIndex].Write(bytes)
		if err != nil {
			return fmt.Errorf("error while writing piece id=%d, err=%s", piece.Index, err)
		}
	}

	return nil
}

func loadPiecesFromPartialFiles(torrent types.TorrentFile, results *types.Results) error {
	results.Lock.Lock()
	defer results.Lock.Unlock()

	results.PiecesDone = 0
	fileNumber := (len(torrent.PieceHashes) / 1000) + 1
	for i := 0; i < fileNumber; i += 1 {
		filename := fmt.Sprintf("%s.%d.part", torrent.Name[0:20], i)
		numberOfPiecesInFile := 0

		if _, err := os.Stat(filename); err != nil {
			continue
		}

		pfile, err := os.Open(filename)
		if err != nil {
			return fmt.Errorf("cannot open file=%s, err=%s", filename, err)
		}

		res, err := file.ReadPartialFile(pfile, &results.Bitfield)
		if err != nil {
			return fmt.Errorf("error while reading partial file, name=%s, err=%s", filename, err)
		}

		for _, p := range res {
			hash := sha1.Sum(p.Data)
			if hash != torrent.PieceHashes[p.Index] {
				return fmt.Errorf("hash mismatch for piece=%d, want=%s, got=%s", p.Index, string(torrent.PieceHashes[p.Index][:]), string(hash[:]))
			}
			loadedPiece := p
			results.Pieces[loadedPiece.Index] = &loadedPiece
			results.PiecesDone += 1
			numberOfPiecesInFile += 1
			err := results.Bitfield.Set(loadedPiece.Index, true)
			if err != nil {
				return fmt.Errorf("error setting true for bit %d, err=%s", p.Index, err)
			}
		}

		log.Printf("loaded %d pieces from file %s", numberOfPiecesInFile, filename)
	}
	return nil
}

func launchClients(numberOfClients int, ctx context.Context, torrent *types.TorrentFile, comms types.Communication, results *types.Results, peerInfo types.PeerInformation) {
	numberOfConnections := 0
	peerCons := make(map[int]struct{})
	for numberOfConnections < numberOfClients {
		var err error = nil
		i := 0
		for {
			_, ok := peerCons[i]
			if !ok {
				err = connectToPeer(ctx, torrent, comms, results, peerInfo, i)
			}
			if err != nil {
				log.Printf("ERROR: encountered an error connecting to target=%s, err=%s", peerInfo.IPs[i], err)
				i += 1
			} else {
				break
			}
		}
		peerCons[i] = struct{}{}
		log.Printf("connected to peer number %d", i)
		numberOfConnections += 1
	}
}

func launchTimers(chokeInterval, trackerInterval int) (chan struct{}, chan struct{}) {
	chokeAlgCh := make(chan struct{})
	go func() {
		for {
			time.Sleep(time.Second * time.Duration(chokeInterval))
			chokeAlgCh <- struct{}{}
		}
	}()

	trackerUpdateCh := make(chan struct{})
	go func() {
		for {
			time.Sleep(time.Second * time.Duration(trackerInterval))
			trackerUpdateCh <- struct{}{}
		}
	}()

	return chokeAlgCh, trackerUpdateCh
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
	results := types.Results{Pieces: make([]*types.Piece, len(torrent.PieceHashes)), Bitfield: bitfield.New(len(torrent.PieceHashes)), Lock: sync.RWMutex{}}
	err = loadPiecesFromPartialFiles(torrent, &results)
	if err != nil {
		log.Fatalf("encountered an error while reading partial files, err=%s", err)
	}
	savedPieces := bitfield.Copy(&results.Bitfield)

	peerInfo, peerId, err := bittorrent.GetPeers(torrent, &results, Port)
	if err != nil {
		log.Fatalf("encountered an error while retrieving peers from tracker, err=%s", err)
	}
	log.Printf("set peer-id to=%s, received %d peers, interval=%d", peerId, len(peerInfo.IPs), peerInfo.Interval)

	// TODO [MVP]: fill 'workQueue' with each piece
	comms := types.Communication{Orders: make(chan *types.PieceOrder, len(torrent.PieceHashes)), Results: make(chan *types.Piece, len(torrent.PieceHashes))}
	requests := []int{0, 1, 1001, 1003, 2005, 2024}
	for _, r := range requests {
		have, err := results.Bitfield.Get(r)
		if err != nil {
			log.Fatalf("encountered error while checking bitfield, err=%s", err)
		}
		if have {
			continue
		}
		p := &types.PieceOrder{Index: r, Length: torrent.PieceLength, Hash: torrent.PieceHashes[r]}
		comms.Orders <- p
	}

	mainCtx, cancel := context.WithCancel(context.WithValue(context.Background(), "peer-id", peerId))

	go listeningServer(mainCtx, &torrent, comms, &results)

	launchClients(2, mainCtx, &torrent, comms, &results, peerInfo)

	signalCh := make(chan os.Signal, 2)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGINT)

	chokeAlgorithmCh, trackerUpdateCh := launchTimers(ChokeAlgorithmTick, peerInfo.Interval)

	for {
		exit := false
		select {
		case <-signalCh:
			log.Printf("SIGINT caught, terminating")
			cancel()
			time.Sleep(time.Second)
			exit = true
		case piece := <-comms.Results:
			results.Lock.Lock()
			results.Pieces[piece.Index] = piece
			results.PiecesDone += 1
			err := results.Bitfield.Set(piece.Index, true)
			results.Lock.Unlock()
			if err != nil {
				log.Fatalf("ERROR: encountered an error while setting a bit in bitfield to true, index=%d, err=%s", piece.Index, err)
			}
			log.Printf("downloaded %d/%d pieces, %f%%", results.PiecesDone, len(torrent.PieceHashes), float32(results.PiecesDone)/float32(len(torrent.PieceHashes)))
		case <-chokeAlgorithmCh:
			// TODO: Implement choke algorithm
			log.Printf("choke algorithm tick")
		case <-trackerUpdateCh:
			// TODO: Implement tracker update
			log.Printf("tracker update tick")
		}

		if exit {
			break
		}
	}

	log.Printf("saving %d pieces", results.PiecesDone)
	if results.PiecesDone != len(torrent.PieceHashes) {
		err := savePartialFiles(torrent, &results, &savedPieces)
		if err != nil {
			log.Printf("ERROR: encountered error while saving partial files, err=%s", err)
		}
	} else {
		// TODO [MVP]: If file is complete, write the complete file and not PartialFile
	}

	os.Exit(0)
}
