package decode

import (
	"encoding/hex"
	"reflect"
	"testing"
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
			t.Errorf("got error, error=%e", err)
		}
		assertEqual(t, n, len(test.correct))
		assertEqual(t, string(buf[:n]), test.correct)
	}
}

func TestDecode(t *testing.T) {
	t.Run("test basic decode", func(t *testing.T) {
		mock := New("d8:announce37:http://tracker.test.org:6969/announce7:comment34:Testing data for bitflood purposes10:created by12:bitflood 0.113:creation datei1718445002e4:infod6:lengthi12345e4:name8:test.iso12:piece lengthi262144e6:pieces20:aabbccddeeffgghhiijjee")
		torrent, err := Decode(mock)
		if err != nil {
			t.Errorf("got error=%e", err)
		}
		assertEqual(t, torrent.Announce, "http://tracker.test.org:6969/announce")
		assertEqual(t, torrent.Comment, "Testing data for bitflood purposes")
		assertEqual(t, torrent.CreatedBy, "bitflood 0.1")
		assertEqual(t, torrent.CreationDate, 1718445002)
		assertEqual(t, torrent.Length, 12345)
		assertEqual(t, torrent.Name, "test.iso")
		assertEqual(t, torrent.PieceLength, 262144)
		assertEqual(t, len(torrent.Pieces), 1)
		assertArrayEqual(t, torrent.Pieces[0], []byte("aabbccddeeffgghhiijj"))

		infoHashSlice, err := hex.DecodeString("96911ea49cf0e5066f85e755f92cb66017b412d9")
		if err != nil {
			t.Errorf("could not decode hex string to bytes")
		}
		assertArrayEqual(t, torrent.InfoHash, infoHashSlice)
	})
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got '%v', wanted '%v'", got, want)
	}
}

func assertArrayEqual(t *testing.T, got [20]byte, want []byte) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		t.Errorf("got '%v', wanted '%v'", got, want)
	}
}
