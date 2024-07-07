package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/hojdars/bitflood/bitfield"
	"github.com/hojdars/bitflood/bittorrent"
	"github.com/hojdars/bitflood/types"
)

const BitFieldLength = 316
const TargetPort = 6881

func main() {

	peerId := bittorrent.MakePeerId()
	log.Printf("starting with peer-id=%s", peerId)

	torrent := types.TorrentFile{
		InfoHash: [20]byte([]byte("aabbccddeeffgghhiijj")),
	}

	conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", TargetPort))
	if err != nil {
		log.Fatalf("connection failed")
	}
	defer conn.Close()

	results := types.Results{Pieces: make([]*types.Piece, len(torrent.PieceHashes)), Bitfield: bitfield.New(len(torrent.PieceHashes)), Lock: sync.RWMutex{}}
	peer, err := bittorrent.InitiateConnection(conn, torrent, &results, peerId)
	if err != nil {
		log.Fatalf("handshake failed")
	}

	log.Printf("received correct handshake from target=%s, peer-id=%s", conn.RemoteAddr().String(), peer.ID)

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

	// wait for interested
	receiveMessage(conn, peer.ID)

	log.Println("sending unchoke")
	msg = bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgUnchoke, Data: []byte{}}
	msgData, err = bittorrent.SerializeMessage(msg)
	if err != nil {
		log.Printf("ERROR: failed to serialize bitfield")
		return
	}
	conn.Write(msgData)

	loopReceive(conn, peer.ID)
}

func loopReceive(conn net.Conn, remotePeerId string) {
	for {
		receiveMessage(conn, remotePeerId)
	}
}

func receiveMessage(conn net.Conn, remotePeerId string) {
	msg, err := bittorrent.DeserializeMessage(conn)
	if err != nil {
		log.Printf("ERROR [%s]: error while receiving message from target=%s, err=%s", remotePeerId, conn.RemoteAddr().String(), err)
	}

	log.Printf("received message, code=%d", msg.Code)
	if msg.Code == bittorrent.MsgRequest {
		index, begin, len, err := msg.DeserializeRequestMsg()
		if err != nil {
			log.Printf("ERROR [%s]: error while deserializing request message from target=%s, err=%s", remotePeerId, conn.RemoteAddr().String(), err)
		}
		log.Printf("received request, index=%d, begin=%d, len=%d", index, begin, len)
	}
	if msg.Code == bittorrent.MsgInterested {
		log.Printf("got 'interested' message")
	}
}

func loopSend(conn net.Conn) {
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
