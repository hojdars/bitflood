package decode

import (
	"encoding/hex"
	"testing"

	"github.com/hojdars/bitflood/assert"
)

type MockFile struct {
	position int
	content  []byte
}

func New(content string) MockFile {
	return MockFile{0, []byte(content)}
}

func (mock MockFile) Read(p []byte) (n int, err error) {
	n = min(len(p), len(mock.content)-mock.position)
	copy(p, mock.content[mock.position:mock.position+n])
	return
}

func TestMock(t *testing.T) {
	tests := []struct {
		text       string
		bufferSize int
		correct    string
	}{
		{
			"testing the mocking class",
			len("testing the mocking class"),
			"testing the mocking class",
		},
		{
			"testing the mocking class",
			4,
			"test",
		},
		{
			"testing the mocking class",
			60,
			"testing the mocking class",
		},
	}

	for _, test := range tests {
		mock := New(test.text)
		buf := make([]byte, test.bufferSize)
		n, err := mock.Read(buf)
		if err != nil {
			t.Errorf("got error, err=%s", err)
		}
		assert.Equal(t, n, len(test.correct))
		assert.Equal(t, string(buf[:n]), test.correct)
	}
}

func TestDecodeTorrentFile(t *testing.T) {
	mock := New("d8:announce37:http://tracker.test.org:6969/announce7:comment34:Testing data for bitflood purposes10:created by12:bitflood 0.113:creation datei1718445002e4:infod6:lengthi12345e4:name8:test.iso12:piece lengthi262144e6:pieces20:aabbccddeeffgghhiijjee")
	torrent, err := DecodeTorrentFile(mock)
	if err != nil {
		t.Errorf("got err=%s", err)
	}
	assert.Equal(t, torrent.Announce, "http://tracker.test.org:6969/announce")
	assert.Equal(t, torrent.Comment, "Testing data for bitflood purposes")
	assert.Equal(t, torrent.CreatedBy, "bitflood 0.1")
	assert.Equal(t, torrent.CreationDate, 1718445002)
	assert.Equal(t, torrent.Length, 12345)
	assert.Equal(t, torrent.Name, "test.iso")
	assert.Equal(t, torrent.PieceLength, 262144)
	assert.Equal(t, len(torrent.Pieces), 1)
	assert.SliceEqual(t, torrent.Pieces[0][:], []byte("aabbccddeeffgghhiijj"))

	infoHashSlice, err := hex.DecodeString("96911ea49cf0e5066f85e755f92cb66017b412d9")
	if err != nil {
		t.Errorf("could not decode hex string to bytes")
	}
	assert.SliceEqual(t, torrent.InfoHash[:], infoHashSlice)
}

func TestDecodePeerInformation(t *testing.T) {
	mock := New("d8:intervali900e5:peers6:1122126:peers60:e")
	peers, err := DecodePeerInformation(mock)
	if err != nil {
		t.Errorf("got err=%s", err)
	}
	assert.Equal(t, peers.Interval, 900)
	assert.Equal(t, len(peers.IPs), 1)
	assert.Equal(t, len(peers.Ports), 1)
	assert.SliceEqual(t, []byte(peers.IPs[0]), []byte{49, 49, 50, 50})
	assert.Equal(t, peers.Ports[0], 12594)
}
