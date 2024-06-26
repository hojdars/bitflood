package main

import (
	"fmt"
	"log"
	"net"
	"time"

	"github.com/hojdars/bitflood/bittorrent"
)

const BitFieldLength = 315
const TargetPort = 6881

func main() {

	peerId := bittorrent.MakePeerId()
	log.Printf("starting with peer-id=%s", peerId)

	data := bittorrent.HandshakeData{
		Extensions: [8]byte{},
		InfoHash:   [20]byte([]byte("aabbccddeeffgghhiijj")),
		PeerId:     [20]byte([]byte(peerId)),
	}

	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", TargetPort))
	if err != nil {
		log.Fatalf("connection failed")
	}
	defer conn.Close()

	outHandshake, err := bittorrent.SerializeHandshake(data)
	if err != nil {
		log.Fatalf("handshake serialization failed")
	}
	_, err = conn.Write(outHandshake)
	if err != nil {
		log.Fatalf("network write failed")
	}

	inHandshake, err := bittorrent.DeserializeHandshake(conn)
	if err != nil {
		log.Printf("ERROR: failed handshake from target=%s", conn.RemoteAddr().String())
		return
	}

	remotePeerId := string(inHandshake.PeerId[:])
	log.Printf("received correct handshake from target=%s, peer-id=%s", conn.RemoteAddr().String(), remotePeerId)

	log.Println("sending full bitfield")

	bitfield := make([]byte, BitFieldLength)
	for i := range bitfield {
		bitfield[i] = 255
	}
	msg := bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgBitfield, Data: bitfield}
	msgData, err := bittorrent.SerializeMessage(msg)
	if err != nil {
		log.Printf("ERROR: failed to serialize bitfield")
		return
	}
	conn.Write(msgData)

	for {
		time.Sleep(time.Second * 3)
		log.Printf("sending message")
		msg := bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgUnchoke, Data: []byte{}}
		msgData, err := bittorrent.SerializeMessage(msg)
		if err != nil {
			log.Printf("ERROR: failed to serialize msg")
			return
		}
		conn.Write(msgData)
	}
}
