package types

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

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
	Index            int
	Length           int
	Data             []byte
	DownloadedFromId string
}

type PeerInterest struct {
	Id           string
	IsInterested bool
}

type ConnectionEnd struct {
	Id   string
	Addr net.Addr
}

type Communication struct {
	Orders          chan *PieceOrder   // 1 main -> N leeches
	Results         chan *Piece        // N leeches -> 1 main
	PeerInterested  chan PeerInterest  // N seeds -> 1 main
	ConnectionEnded chan ConnectionEnd // N seeds -> 1 main
	Uploaded        chan int           // N seeds -> 1 main

	PeersToUnchoke chan []string // 1 main -> 1 seed
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
	_, err := io.ReadFull(reader, buf)
	if err == io.EOF {
		return err
	} else if err != nil {
		return fmt.Errorf("error deserializing piece, err=%s", err)
	}

	p.Index = int(binary.BigEndian.Uint32(buf[0:4]))
	p.Length = int(binary.BigEndian.Uint32(buf[4:8]))
	p.Data = make([]byte, p.Length)

	_, err = reader.Read(p.Data)
	if err != nil {
		return fmt.Errorf("error deserializing piece's data, err=%s", err)
	}

	return nil
}

type Results struct {
	Pieces     []*Piece
	PiecesDone int
	Bitfield   bitfield.Bitfield
	Lock       sync.RWMutex
}
