package main

import (
	"crypto/sha1"
	"fmt"
	"log"
	"os"

	"github.com/dustin/go-humanize"
	"github.com/hojdars/bitflood/bitfield"
	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/file"
	"github.com/hojdars/bitflood/types"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("invalid number of arguments, expected 2, got %v", len(os.Args))
	}

	filename := os.Args[1]

	if _, err := os.Stat(filename); err != nil {
		log.Fatalf("file does not exist, file=%s", filename)
	}

	log.Printf("started on file=%s\n", filename)

	torrentFile, err := os.Open(filename)
	if err != nil {
		log.Fatalf("cannot open file, err=%s", err)
	}

	torrent, err := decode.DecodeTorrentFile(torrentFile)
	if err != nil {
		log.Fatalf("encountered an error during .torrent file decoding, err=%s", err)
	}

	if torrent.Length == 0 {
		log.Fatalf("key 'length' is missing, unsupported .torrent file")
	}

	log.Printf("torrent file=%s, size=%s, pieces=%d", torrent.Name, humanize.Bytes(uint64(torrent.Length)), len(torrent.PieceHashes))

	numberOfPieces := 0
	results := make([]*types.Piece, len(torrent.PieceHashes))
	resultBitfield := bitfield.New(len(torrent.PieceHashes))

	fileNumber := (len(torrent.PieceHashes) / 1000) + 1
	for i := 0; i < fileNumber; i += 1 {
		filename := fmt.Sprintf("%s.%d.part", torrent.Name[0:20], i)
		numberOfPiecesInFile := 0

		if _, err := os.Stat(filename); err != nil {
			continue
		}

		log.Printf("found partial file, name=%s", filename)

		pfile, err := os.Open(filename)
		if err != nil {
			log.Fatalf("cannot open file=%s, err=%s", filename, err)
		}

		res, err := file.ReadPartialFile(pfile, &resultBitfield)
		if err != nil {
			log.Fatalf("error while reading partial file, name=%s, err=%s", filename, err)
		}

		for _, p := range res {
			hash := sha1.Sum(p.Data)
			if hash != torrent.PieceHashes[p.Index] {
				log.Fatalf("hash mismatch for piece=%d, want=%s, got=%s", p.Index, string(torrent.PieceHashes[p.Index][:]), string(hash[:]))
				continue
			}

			results[p.Index] = &p
			numberOfPieces += 1
			numberOfPiecesInFile += 1
			err := resultBitfield.Set(p.Index, true)
			if err != nil {
				log.Fatalf("error setting true for bit %d, err=%s", p.Index, err)
			}
		}

		log.Printf("loaded %d pieces from file %s", numberOfPiecesInFile, filename)
	}

	log.Printf("loaded %d pieces", numberOfPieces)
	log.Printf("writing 'bitfield.txt'")

	bf, err := os.Create("bitfield.txt")
	if err != nil {
		log.Fatalf("cannot open 'bitfield.txt' for writing, err=%s", err)
	}

	for i := 0; i < len(torrent.PieceHashes); i += 1 {
		r, err := resultBitfield.Get(i)
		if err != nil {
			log.Fatalf("error getting bitfield result")
		}
		if r {
			bf.WriteString("1\n")
		} else {
			bf.WriteString("0\n")
		}
	}
}
