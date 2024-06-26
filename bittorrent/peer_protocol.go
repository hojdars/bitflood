package bittorrent

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const ProtocolNumber int = 19
const ProtocolLength int = 48

var ProtocolString string = "BitTorrent protocol"

type HandshakeData struct {
	Extensions [8]byte
	InfoHash   [20]byte
	PeerId     [20]byte
}

func SerializeHandshake(data HandshakeData) ([]byte, error) {
	buf := make([]byte, 0, 68)
	buffer := bytes.NewBuffer(buf)

	err := buffer.WriteByte(byte(ProtocolNumber))
	if err != nil {
		return []byte{}, fmt.Errorf("error while writing into buffer, err=%s", err)
	}

	n, err := buffer.WriteString(ProtocolString)
	if err != nil || n != ProtocolNumber {
		return []byte{}, fmt.Errorf("error while writing into buffer, len=%d, err=%s", n, err)
	}

	n, err = buffer.Write(data.Extensions[:])
	if err != nil || n != 8 {
		return []byte{}, fmt.Errorf("error while writing into buffer, len=%d, err=%s", n, err)
	}

	n, err = buffer.Write(data.InfoHash[:])
	if err != nil || n != len(data.InfoHash) {
		return []byte{}, fmt.Errorf("error while writing into buffer, len=%d, err=%s", n, err)
	}

	n, err = buffer.Write(data.PeerId[:])
	if err != nil || n != len(data.PeerId) {
		return []byte{}, fmt.Errorf("error while writing into buffer, len=%d, err=%s", n, err)
	}

	return buffer.Bytes(), nil
}

func DeserializeHandshake(reader io.Reader) (HandshakeData, error) {
	lenBuf := make([]byte, 1)
	n, err := io.ReadFull(reader, lenBuf)
	if err != nil {
		return HandshakeData{}, fmt.Errorf("handshake first byte read failed, len=%d, err=%s", n, err)
	}

	len := int(lenBuf[0])
	if len != ProtocolNumber {
		return HandshakeData{}, fmt.Errorf("handshake is not the BitTorrent protocol, first byte: %d", len)
	}

	dataBuf := make([]byte, ProtocolNumber+ProtocolLength)
	n, err = io.ReadFull(reader, dataBuf)
	if err != nil {
		return HandshakeData{}, fmt.Errorf("hanshake data read failed, len=%d, err=%s", n, err)
	}

	if string(dataBuf[0:ProtocolNumber]) != ProtocolString {
		return HandshakeData{}, fmt.Errorf("hanshake does not contain the correct string, string=%s", string(dataBuf[0:20]))
	}

	return HandshakeData{
		Extensions: [8]byte(dataBuf[ProtocolNumber : ProtocolNumber+8]),
		InfoHash:   [20]byte(dataBuf[ProtocolNumber+8 : ProtocolNumber+28]),
		PeerId:     [20]byte(dataBuf[ProtocolNumber+28 : ProtocolNumber+48]),
	}, nil
}

const (
	MsgChoke         byte = 0
	MsgUnchoke       byte = 1
	MsgInterested    byte = 2
	MsgNotInterested byte = 3
	MsgHave          byte = 4
	MsgBitfield      byte = 5
	MsgRequest       byte = 6
	MsgPiece         byte = 7
	MsgCancel        byte = 8
)

type PeerMessage struct {
	KeepAlive bool
	Code      byte
	Data      []byte
}

func SerializeMessage(msg PeerMessage) ([]byte, error) {
	payloadLength := uint32(1 + len(msg.Data)) // payload = without the 4B length at the start
	result := make([]byte, 4+payloadLength)
	binary.BigEndian.PutUint32(result[0:4], payloadLength)
	result[4] = msg.Code
	copied := copy(result[5:], msg.Data)
	if copied != len(msg.Data) {
		return []byte{}, fmt.Errorf("did not copy the whole message, msg len=%d, copied=%d", len(msg.Data), copied)
	}
	return result, nil
}

func DeserializeMessage(r io.Reader) (PeerMessage, error) {
	lengthBuf := make([]byte, 4)
	_, err := io.ReadFull(r, lengthBuf)
	if err != nil {
		return PeerMessage{}, fmt.Errorf("error while reading message length, err=%s", err)
	}

	length := binary.BigEndian.Uint32(lengthBuf)
	if length == 0 {
		return PeerMessage{KeepAlive: true}, nil
	}

	codeBuf := make([]byte, 1)
	_, err = io.ReadFull(r, codeBuf)
	if err != nil {
		return PeerMessage{}, fmt.Errorf("error while reading message code, err=%s", err)
	}

	dataBuf := make([]byte, length-1)
	if length-1 > 0 {
		_, err = io.ReadFull(r, dataBuf)
		if err != nil {
			return PeerMessage{}, fmt.Errorf("error while reading message data, err=%s", err)
		}
	}

	return PeerMessage{KeepAlive: false, Code: codeBuf[0], Data: dataBuf}, nil
}

func (msg PeerMessage) DeserializePiece() (index, begin int, piece []byte, err error) {
	if msg.Code != MsgPiece {
		err = fmt.Errorf("cannot deserialize piece message on a non-piece message, msg-code=%d", msg.Code)
		return
	}
	if len(msg.Data) <= 8 {
		err = fmt.Errorf("malformed piece message, too short, len=%d", len(msg.Data))
		return
	}

	index = int(binary.BigEndian.Uint32(msg.Data[0:4]))
	begin = int(binary.BigEndian.Uint32(msg.Data[4:8]))
	piece = msg.Data[8:]
	return
}
