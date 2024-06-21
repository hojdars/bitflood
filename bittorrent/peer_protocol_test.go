package bittorrent

import (
	"bytes"
	"testing"

	"github.com/hojdars/bitflood/assert"
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

	assert.Equal(t, got[0], byte(ProtocolNumber))
	assert.SliceEqual(t, got[1:20], []byte(ProtocolString))
	assert.SliceEqual(t, got[20:28], make([]byte, 8))
	assert.SliceEqual(t, got[28:48], data.infoHash[:])
	assert.SliceEqual(t, got[48:68], data.peerId[:])
	assert.Equal(t, len(got), 68)

	reader := bytes.NewReader(got)
	decoded, err := DeserializeHandshake(reader)
	if err != nil {
		t.Errorf("got error=%s", err)
	}

	assert.Equal(t, data.extensions, decoded.extensions)
	assert.SliceEqual(t, data.infoHash[:], decoded.infoHash[:])
	assert.SliceEqual(t, decoded.peerId[:], decoded.peerId[:])
}
