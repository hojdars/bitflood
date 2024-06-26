package types

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/hojdars/bitflood/bitfield"
)

type TorrentFile struct {
	Announce     string
	Name         string
	Comment      string
	CreatedBy    string
	CreationDate int
	Length       int
	PieceLength  int
	PieceHashes  [][20]byte
	InfoHash     [20]byte

	// BEP:12 extension (Multitracker Metadata Extension)
	AnnounceList [][]string
}

type PeerInformation struct {
	Interval int
	IPs      []net.IP
	Ports    []uint16
}

type Peer struct {
	Downloaded     uint32
	ChokedBy       bool
	Choking        bool
	Interested     bool
	InterestedSent bool
	Bitfield       bitfield.Bitfield
	ID             string
	Addr           net.Addr
}

type PieceOrder struct {
	Index  int
	Length int
	Hash   [20]byte
}

type Piece struct {
	Index  int
	Length int
	Data   []byte
}

func (p Piece) Serialize() []byte {
	result := make([]byte, 8, 4+4+p.Length)
	binary.BigEndian.PutUint32(result[0:4], uint32(p.Index))
	binary.BigEndian.PutUint32(result[4:8], uint32(p.Length))
	result = append(result, p.Data...)
	return result
}

func (p *Piece) Deserialize(reader io.Reader) error {
	buf := make([]byte, 8)
	_, err := reader.Read(buf)
	if err != nil {
		return fmt.Errorf("error deserializing piece, err=%s", err)
	}

	p.Index = int(binary.BigEndian.Uint32(buf[0:4]))
	p.Length = int(binary.BigEndian.Uint32(buf[4:8]))
	p.Data = make([]byte, p.Length)

	_, err = reader.Read(p.Data)
	if err != nil {
		return fmt.Errorf("error deserializing piece, err=%s", err)
	}

	return nil
}
