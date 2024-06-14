package main

import (
	"log"
	"os"

	"github.com/hojdars/bitflood/internal/decode"
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

	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("cannot open file, error=%e", err)
	}

	torrent := decode.Decode(file)

	log.Printf("tracker url=%s", torrent.Announce)
	log.Printf("name=%s, comment=%s, created by=%s, creation date=%d", torrent.Name, torrent.Comment, torrent.CreatedBy, torrent.CreationDate)
	log.Printf("piece length=%d, length=%d, total length of pieces=%d", torrent.PieceLength, torrent.Length, len(torrent.Pieces))
}
