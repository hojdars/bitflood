package bittorrent

import (
	"bytes"
	"encoding/binary"
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

type MockReader struct {
	position int
	content  []byte
}

func New(content []byte) MockReader {
	return MockReader{0, content}
}

func (mock *MockReader) Read(p []byte) (n int, err error) {
	n = min(len(p), len(mock.content)-mock.position)
	copy(p, mock.content[mock.position:mock.position+n])
	mock.position += n
	return
}

func TestReadMessage(t *testing.T) {
	t.Run("test message deserialization without payload", func(t *testing.T) {
		data := make([]byte, 5)
		binary.BigEndian.PutUint32(data[0:4], 1)
		data[4] = MsgUnchoke
		mock := New(data)

		got, err := DeserializeMessage(&mock)
		assert.IsError(t, err)

		assert.Equal(t, got.keepAlive, false)
		assert.Equal(t, got.code, MsgUnchoke)
		assert.Equal(t, len(got.data), 0)
	})

	t.Run("test message deserialization with payload", func(t *testing.T) {
		data := make([]byte, 10)
		messageData := []byte{1, 2, 3, 4, 5}
		binary.BigEndian.PutUint32(data[0:4], uint32(1+len(messageData)))
		data[4] = MsgBitfield
		copy(data[5:10], messageData)
		mock := New(data)

		got, err := DeserializeMessage(&mock)
		assert.IsError(t, err)

		assert.Equal(t, got.keepAlive, false)
		assert.Equal(t, got.code, MsgBitfield)
		assert.Equal(t, len(got.data), 5)
		assert.SliceEqual(t, got.data, messageData)
	})
}
