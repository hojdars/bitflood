package main

import (
	"log"
	"os"

	"github.com/dustin/go-humanize"
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

	log.Printf("detected tracker url=%s", torrent.Announce)
	log.Printf("read torrent file=%s, size=%s", torrent.Name, humanize.Bytes(uint64(torrent.Length)))
}
