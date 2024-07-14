package bittorrent

import (
	"net/url"
	"testing"

	"github.com/hojdars/bitflood/assert"
	"github.com/hojdars/bitflood/types"
)

func TestGetPeersUrl(t *testing.T) {
	t.Run("basic test", func(t *testing.T) {
		torrent := types.TorrentFile{}
		torrent.Announce = "http://test.org/announce"
		copy(torrent.InfoHash[:], "aabbccddeeffgghhiijj")
		torrent.Length = 666
		url, err := url.Parse(torrent.Announce)
		if err != nil {
			t.Errorf("got error, err=%s", err)
		}
		peerId := "SH01-ziYDZM5WilvkDy9"
		addGetPeersQuery(url, torrent, peerId, 1234, 0, 0, torrent.Length)
		got := url.String()

		want := "http://test.org/announce?compact=1&downloaded=0&event=started&info_hash=aabbccddeeffgghhiijj&left=666&peer_id=SH01-ziYDZM5WilvkDy9&port=1234&uploaded=0"
		assert.Equal(t, got, want)
	})

	t.Run("different left-to-download", func(t *testing.T) {
		torrent := types.TorrentFile{}
		torrent.Announce = "http://test.org/announce"
		copy(torrent.InfoHash[:], "aabbccddeeffgghhiijj")
		torrent.Length = 666
		url, err := url.Parse(torrent.Announce)
		if err != nil {
			t.Errorf("got error, err=%s", err)
		}
		peerId := "SH01-ziYDZM5WilvkDy9"
		addGetPeersQuery(url, torrent, peerId, 1234, 0, 0, 400)
		got := url.String()

		want := "http://test.org/announce?compact=1&downloaded=0&event=started&info_hash=aabbccddeeffgghhiijj&left=400&peer_id=SH01-ziYDZM5WilvkDy9&port=1234&uploaded=0"
		assert.Equal(t, got, want)

	})
}
