package bittorrent

import (
	"bytes"
	"slices"
	"testing"
)

func TestHandshake(t *testing.T) {
	data := HandshakeData{
		extensions: [8]byte{},
		infoHash:   [20]byte([]byte("aabbccddeeffgghhiijj")),
		peerId:     [20]byte([]byte("SH01-1234567890abcde")),
	}
	got, err := SerializeHandshake(data)

	if err != nil {
		t.Errorf("got error=%s", err)
	}

	assertEqual(t, got[0], byte(ProtocolNumber))
	assertSliceEqual(t, got[1:20], []byte(ProtocolString))
	assertSliceEqual(t, got[20:28], make([]byte, 8))
	assertSliceEqual(t, got[28:48], data.infoHash[:])
	assertSliceEqual(t, got[48:68], data.peerId[:])
	assertEqual(t, len(got), 68)

	reader := bytes.NewReader(got)
	decoded, err := DeserializeHandshake(reader)
	if err != nil {
		t.Errorf("got error=%s", err)
	}

	assertEqual(t, data.extensions, decoded.extensions)
	assertSliceEqual(t, data.infoHash[:], decoded.infoHash[:])
	assertSliceEqual(t, decoded.peerId[:], decoded.peerId[:])
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got '%v', wanted '%v'", got, want)
	}
}

func assertSliceEqual(t *testing.T, got, want []byte) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("got '%v' (len=%v), wanted '%v'(len=%v)", got, len(got), want, len(want))
	}
}
