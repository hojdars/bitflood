package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/hojdars/bitflood/bitfield"
	"github.com/hojdars/bitflood/bittorrent"
	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/types"
)

const BitFieldLength = 315
const TargetPort = 6881

func main() {

	peerId := bittorrent.MakePeerId()
	log.Printf("starting with peer-id=%s", peerId)

	filename := "/home/ashen/go/projects/bitflood/testdata/debian-12.5.0-amd64-netinst.iso.torrent"
	torrentFile, err := os.Open(filename)
	if err != nil {
		log.Fatalf("error opening torrent file")
	}
	torrent, err := decode.DecodeTorrentFile(torrentFile)
	if err != nil {
		log.Fatalf("error decoding torrent file")
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

	log.Println("sending empty bitfield")
	bitfield := make([]byte, BitFieldLength)
	for i := range bitfield {
		bitfield[i] = 0
	}
	msg := bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgBitfield, Data: bitfield}
	msgData, err := bittorrent.SerializeMessage(msg)
	if err != nil {
		log.Printf("ERROR: failed to serialize bitfield")
		return
	}
	conn.Write(msgData)

	// act as seeder
	// actAsSeed(conn, &peer)

	// act as leech
	actAsLeech(conn, &peer)
}

func actAsSeed(conn net.Conn, peer *types.Peer) {
	// wait for interested
	receiveMessage(conn, peer.ID)

	log.Println("sending unchoke")
	msg := bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgUnchoke, Data: []byte{}}
	msgData, err := bittorrent.SerializeMessage(msg)
	if err != nil {
		log.Printf("ERROR: failed to serialize bitfield")
		return
	}
	conn.Write(msgData)

	loopReceive(conn, peer.ID)
}

func actAsLeech(conn net.Conn, peer *types.Peer) {
	log.Printf("acting as leech")
	sendMessage(conn, bittorrent.MsgInterested, []byte{})

	for {
		msg, err := bittorrent.DeserializeMessage(conn)
		if err != nil {
			log.Printf("ERROR [%s]: error while receiving message from target=%s, err=%s", peer.ID, conn.RemoteAddr().String(), err)
		}
		log.Printf("INFO  [%s]: received message, code=%d", peer.ID, msg.Code)

		if msg.Code == bittorrent.MsgUnchoke {
			log.Printf("INFO  [%s]: got unchoke message", peer.ID)
			break
		}
	}

	log.Printf("INFO  [%s]: starting requests", peer.ID)
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

func sendMessage(conn net.Conn, code byte, data []byte) {
	msg := bittorrent.PeerMessage{KeepAlive: false, Code: code, Data: data}
	msgData, err := bittorrent.SerializeMessage(msg)
	if err != nil {
		log.Printf("ERROR: failed to serialize bitfield")
		return
	}
	conn.Write(msgData)
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
