package bittorrent

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/hojdars/bitflood/assert"
)

func TestHandshake(t *testing.T) {
	data := HandshakeData{
		Extensions: [8]byte{},
		InfoHash:   [20]byte([]byte("aabbccddeeffgghhiijj")),
		PeerId:     [20]byte([]byte("SH01-1234567890abcde")),
	}
	got, err := serializeHandshake(data)

	if err != nil {
		t.Errorf("got error=%s", err)
	}

	assert.Equal(t, got[0], byte(ProtocolNumber))
	assert.SliceEqual(t, got[1:20], []byte(ProtocolString))
	assert.SliceEqual(t, got[20:28], make([]byte, 8))
	assert.SliceEqual(t, got[28:48], data.InfoHash[:])
	assert.SliceEqual(t, got[48:68], data.PeerId[:])
	assert.Equal(t, len(got), 68)

	reader := bytes.NewReader(got)
	decoded, err := deserializeHandshake(reader)
	if err != nil {
		t.Errorf("got error=%s", err)
	}

	assert.Equal(t, data.Extensions, decoded.Extensions)
	assert.SliceEqual(t, data.InfoHash[:], decoded.InfoHash[:])
	assert.SliceEqual(t, decoded.PeerId[:], decoded.PeerId[:])
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

func TestMessage(t *testing.T) {
	t.Run("test message deserialization without payload", func(t *testing.T) {
		data := make([]byte, 5)
		binary.BigEndian.PutUint32(data[0:4], 1)
		data[4] = MsgUnchoke
		mock := New(data)

		got, err := DeserializeMessage(&mock)
		assert.IsError(t, err)

		assert.Equal(t, got.KeepAlive, false)
		assert.Equal(t, got.Code, MsgUnchoke)
		assert.Equal(t, len(got.Data), 0)
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

		assert.Equal(t, got.KeepAlive, false)
		assert.Equal(t, got.Code, MsgBitfield)
		assert.Equal(t, len(got.Data), 5)
		assert.SliceEqual(t, got.Data, messageData)
	})

	t.Run("serialize message", func(t *testing.T) {
		msgData := []byte{1, 2, 3, 4, 5}
		msg := PeerMessage{KeepAlive: false, Code: 5, Data: msgData}
		got, err := SerializeMessage(msg)
		assert.IsError(t, err)
		assert.Equal(t, len(got), 4+1+len(msgData))
		assert.Equal(t, binary.BigEndian.Uint32(got[0:4]), uint32(1+len(msgData)))
		assert.Equal(t, got[4], 5)
		assert.SliceEqual(t, got[5:], msgData)
	})

	t.Run("serialize and deserialize message", func(t *testing.T) {
		msgData := []byte{1, 2, 3, 4, 5}
		msg := PeerMessage{KeepAlive: false, Code: 5, Data: msgData}
		serialized, err := SerializeMessage(msg)
		assert.IsError(t, err)
		reader := bytes.NewReader(serialized)
		fmt.Println(serialized)
		got, err := DeserializeMessage(reader)
		assert.IsError(t, err)
		assert.Equal(t, got.KeepAlive, msg.KeepAlive)
		assert.Equal(t, got.Code, msg.Code)
		assert.SliceEqual(t, got.Data, msg.Data)
	})
}
