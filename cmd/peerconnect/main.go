package main

import (
	"bytes"
	"crypto/sha1"
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
const ChunkSize int = 1 << 14

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
	actAsLeech(conn, &peer, torrent)
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

func actAsLeech(conn net.Conn, peer *types.Peer, torrent types.TorrentFile) {
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

	pieceIndex := 0
	pieceData := make([]byte, torrent.PieceLength)
	pieceNextRequest := 0
	numberOfRequests := 0

	for {
		for numberOfRequests < 5 && pieceNextRequest < torrent.PieceLength {
			length := min(ChunkSize, torrent.PieceLength-pieceNextRequest)
			msg := bittorrent.PeerMessage{KeepAlive: false, Code: bittorrent.MsgRequest}
			msg.SerializeRequestMsg(pieceIndex, pieceNextRequest, length)

			msgData, err := bittorrent.SerializeMessage(msg)
			if err != nil {
				log.Printf("ERROR: failed to serialize bitfield")
				return
			}
			conn.Write(msgData)
			log.Printf("INFO  [%s]: sent request %d,%d,%d", peer.ID, pieceIndex, pieceNextRequest, length)
			pieceNextRequest = pieceNextRequest + length
			numberOfRequests += 1
		}

		msg, err := bittorrent.DeserializeMessage(conn)
		if err != nil {
			log.Printf("ERROR [%s]: error while receiving message from target=%s, err=%s", peer.ID, conn.RemoteAddr().String(), err)
		}
		log.Printf("INFO  [%s]: received message, code=%d", peer.ID, msg.Code)

		if msg.Code == bittorrent.MsgPiece {
			index, begin, data, err := msg.DeserializePieceMsg()
			if err != nil {
				log.Printf("ERROR [%s]: error while receiving piece message from target=%s, err=%s", peer.ID, conn.RemoteAddr().String(), err)
			}
			log.Printf("INFO  [%s]: got 'piece' message, index=%d, begin=%d, data-len=%d", peer.ID, index, begin, len(data))
			copy(pieceData[begin:], data)
			numberOfRequests -= 1
		}

		if pieceNextRequest >= torrent.PieceLength && numberOfRequests == 0 {
			break
		}
	}

	log.Printf("INFO  [%s]: piece complete, verifying hash", peer.ID)
	hash := sha1.Sum(pieceData)
	if !bytes.Equal(hash[:], torrent.PieceHashes[pieceIndex][:]) {
		log.Printf("ERROR [%s]: hash mismatch for piece %d", peer.ID, pieceIndex)
	} else {
		log.Printf("INFO  [%s]: hash verification OK", peer.ID)
	}
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
		log.Printf("INFO  [%s]: received request, index=%d, begin=%d, len=%d", remotePeerId, index, begin, len)
	}
	if msg.Code == bittorrent.MsgInterested {
		log.Printf("INFO  [%s]: got 'interested' message", remotePeerId)
	}
	if msg.Code == bittorrent.MsgPiece {
		log.Printf("INFO  [%s]: got 'piece' message", remotePeerId)
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
