package decode

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io"

	"github.com/hojdars/bitflood/types"
	"github.com/jackpal/bencode-go"
)

type bitTorrentFile struct {
	Announce     string
	Comment      string
	CreatedBy    string `bencode:"created by"`
	CreationDate int    `bencode:"creation date"`
	Info         bitTorrentInfo
}

type bitTorrentInfo struct {
	Name        string `bencode:"name"`
	Length      int    `bencode:"length"`
	PieceLength int    `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
}

func Decode(file io.Reader) (types.TorrentFile, error) {
	torrent := bitTorrentFile{}
	err := bencode.Unmarshal(file, &torrent)
	if err != nil {
		return types.TorrentFile{}, fmt.Errorf("decoding the bencode failed, error=%e", err)
	}

	if len(torrent.Info.Pieces)%20 != 0 {
		return types.TorrentFile{}, fmt.Errorf("length of pieces is not divisable by 20, malformed torrent file, len of pieces=%d", len(torrent.Info.Pieces))
	}

	numberOfHashes := len(torrent.Info.Pieces) / 20

	infoHash, err := computeInfoHash(&torrent.Info)
	if err != nil {
		return types.TorrentFile{}, fmt.Errorf("cannot compute 'info_hash', error=%e", err)
	}

	result := types.TorrentFile{
		Announce:     torrent.Announce,
		Name:         torrent.Info.Name,
		Comment:      torrent.Comment,
		CreatedBy:    torrent.CreatedBy,
		CreationDate: torrent.CreationDate,
		Length:       torrent.Info.Length,
		PieceLength:  torrent.Info.PieceLength,
		Pieces:       make([][20]byte, numberOfHashes),
		InfoHash:     infoHash,
	}

	for i := 0; i < numberOfHashes; i += 1 {
		copy(result.Pieces[i][:], torrent.Info.Pieces[i*20:(i+1)*20])
	}

	return result, nil
}

// TODO: This function is not completely correct.
// BitTorrent specification requires 'info_hash' to be computed from the .torrent file's 'info'
// as-is. Decoding and Encoding again will potentially change the key order, since bencode
// dictionaries have to be in lexicographical order. If the original 'info' is not in
// lexicographical order, the 'info_hash' will not match.
func computeInfoHash(info *bitTorrentInfo) ([20]byte, error) {
	var buffer bytes.Buffer
	err := bencode.Marshal(&buffer, *info)
	if err != nil {
		return [20]byte{}, fmt.Errorf("could not bencode bitTorrentInfo, error=%e", err)
	}
	return sha1.Sum(buffer.Bytes()), nil
}
