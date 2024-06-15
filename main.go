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

	torrent, err := decode.Decode(file)
	if err != nil {
		log.Fatalf("error during .torrent file decoding, err=%e", err)
	}

	if torrent.Length == 0 {
		// TODO: specification requires either 'length' or 'key files', implement 'key files'
		log.Fatalf("key 'length' is missing, unsupported .torrent file")
	}

	log.Printf("tracker url=%s", torrent.Announce)
	log.Printf("torrent file=%s, size=%s", torrent.Name, humanize.Bytes(uint64(torrent.Length)))
}
