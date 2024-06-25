package types

import (
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
	Downloaded uint32
	ChokedBy   bool
	Choking    bool
	Interested bool
	Bitfield   bitfield.Bitfield
	ID         string
	Addr       net.Addr
}
