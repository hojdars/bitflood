package types

type TorrentFile struct {
	Announce     string
	Name         string
	Comment      string
	CreatedBy    string
	CreationDate int
	Length       int
	PieceLength  int
	Pieces       [][20]byte
	InfoHash     [20]byte
}
