package bittorrent

import (
	"testing"

	"github.com/hojdars/bitflood/types"
)

func TestGetPeersUrl(t *testing.T) {
	torrent := types.TorrentFile{}
	torrent.Announce = "http://test.org/announce"
	copy(torrent.InfoHash[:], "aabbccddeeffgghhiijj")
	torrent.Length = 666
	url, err := buildGetPeersUrl(torrent, 1234)
	if err != nil {
		t.Errorf("got error, err=%e", err)
	}
	want := "http://test.org/announce?compact=1&downloaded=0&event=started&info_hash=aabbccddeeffgghhiijj&left=666&peer_id=SH01-ziYDZM5WilvkDy9&port=1234&uploaded=0"
	// check before random peer id
	if url[:115] != want[:115] {
		t.Errorf("got '%v', wanted '%v'", url, want)
	}
	// check after random peer id
	if url[130:] != want[130:] {
		t.Errorf("got '%v', wanted '%v'", url, want)
	}
}
