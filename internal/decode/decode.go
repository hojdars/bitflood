package decode

import (
	"io"
	"log"

	"github.com/jackpal/bencode-go"
)

type TorrentFile struct {
	Announce     string
	Name         string
	Comment      string
	CreatedBy    string
	CreationDate int
	Length       int
	PieceLength  int
	Pieces       [][20]byte
}

type bitTorrentFile struct {
	Announce     string
	Comment      string
	CreatedBy    string `bencode:"created by"`
	CreationDate int    `bencode:"creation date"`
	Info         bitTorrentInfo
}

type bitTorrentInfo struct {
	Name        string
	Length      int
	PieceLength int    `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
}

func Decode(file io.Reader) TorrentFile {
	torrent := bitTorrentFile{}
	err := bencode.Unmarshal(file, &torrent)
	if err != nil {
		log.Fatalf("decoding the bencode failed, error=%e", err)
	}

	if len(torrent.Info.Pieces)%20 != 0 {
		log.Fatalf("length of pieces is not divisable by 20, malformed torrent file, len of pieces=%d", len(torrent.Info.Pieces))
	}

	numberOfHashes := len(torrent.Info.Pieces) / 20

	result := TorrentFile{
		torrent.Announce,
		torrent.Info.Name,
		torrent.Comment,
		torrent.CreatedBy,
		torrent.CreationDate,
		torrent.Info.Length,
		torrent.Info.PieceLength,
		make([][20]byte, numberOfHashes),
	}

	for i := 0; i < numberOfHashes; i += 1 {
		copy(result.Pieces[i][:], torrent.Info.Pieces[i*20:(i+1)*20])
	}

	return result
}
