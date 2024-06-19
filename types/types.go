package types

import "net"

type TorrentFile struct {
	Announce     string
	AnnounceList [][]string
	Name         string
	Comment      string
	CreatedBy    string
	CreationDate int
	Length       int
	PieceLength  int
	Pieces       [][20]byte
	InfoHash     [20]byte
}

type PeerInformation struct {
	Interval int
	IPs      []net.IP
	Ports    []uint16
}
