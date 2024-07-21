package client

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hojdars/bitflood/types"
)

type request struct {
	index  int
	start  int
	length int
}

type pieceProgress struct {
	order         *types.PieceOrder
	buf           []byte
	numDone       int       // how many bytes are downloaded into 'buf'
	requests      []request // queue for requests
	nextToRequest int       // which offset is the next to be requested
}

func (p *pieceProgress) DeleteRequest(start, length int) error {
	for i, r := range p.requests {
		if r.start == start && r.length == length {
			// delete the element
			p.requests[i] = p.requests[len(p.requests)-1]
			p.requests = p.requests[:len(p.requests)-1]
			return nil
		}
	}
	return fmt.Errorf("could not find request with start=%d, length=%d", start, length)
}

type seederState struct {
	requested []request
}

type connection struct {
	ip               net.Addr
	peersToUnchokeCh chan []string // main -> seed, sends array of 'peer-id' of peers that should be unchoked this tick
}

type connections struct {
	peers     []connection
	blacklist map[string]time.Time // IP as string -> timestamp of blacklisting
	lock      sync.Mutex
}

func (conns *connections) IsOpen(ip string) (bool, error) {
	getIpFromAddr := func(inAddr net.Addr) (string, error) {
		if addr, ok := inAddr.(*net.TCPAddr); ok {
			return addr.IP.String(), nil
		} else {
			return "", fmt.Errorf("cannot get IP from addr=%s", inAddr)
		}
	}

	conns.lock.Lock()
	defer conns.lock.Unlock()

	for _, peer := range conns.peers {
		peerIp, err := getIpFromAddr(peer.ip)
		if err != nil {
			return false, fmt.Errorf("cannot check IsOpen, invalid address present in connections, addr=%s, err=%s", peer.ip.String(), err)
		}

		if peerIp == ip {
			return true, nil
		}
	}
	return false, nil
}

func (conns *connections) Add(inputAddr net.Addr) (*connection, error) {
	getIpFromAddr := func(inAddr net.Addr) (string, error) {
		if addr, ok := inAddr.(*net.TCPAddr); ok {
			return addr.IP.String(), nil
		} else {
			return "", fmt.Errorf("cannot get IP from addr=%s", inAddr)
		}
	}

	inputIp, err := getIpFromAddr(inputAddr)
	if err != nil {
		return nil, fmt.Errorf("cannot check IsOpen, invalid input address, addr=%s, err=%s", inputAddr.String(), err)
	}

	isOpen, err := conns.IsOpen(inputIp)
	if isOpen {
		return nil, fmt.Errorf("error adding connection, connection already open")
	}
	if err != nil {
		return nil, fmt.Errorf("error adding connection, error while checking connection open, err=%s", err)
	}

	conns.lock.Lock()
	defer conns.lock.Unlock()

	newConnection := connection{
		ip:               inputAddr,
		peersToUnchokeCh: make(chan []string, 1),
	}
	conns.peers = append(conns.peers, newConnection)
	return &conns.peers[len(conns.peers)-1], nil
}

func (conns *connections) Remove(inputAddr net.Addr) error {
	conns.lock.Lock()
	defer conns.lock.Unlock()

	indexToDrop := -1
	for i, peer := range conns.peers {
		if peer.ip == inputAddr {
			indexToDrop = i
		}
	}
	if indexToDrop == -1 {
		return fmt.Errorf("error removing connection, connection not found")
	}

	conns.peers[indexToDrop] = conns.peers[len(conns.peers)-1]
	conns.peers = conns.peers[:len(conns.peers)-1]

	return nil
}

func (conns *connections) IsBlacklisted(ip string) (bool, error) {
	conns.lock.Lock()
	defer conns.lock.Unlock()

	timeBlacklisted, present := conns.blacklist[ip]
	if !present {
		return false, nil
	}

	if time.Now().Sub(timeBlacklisted) > connectionBlacklistDuration {
		delete(conns.blacklist, ip)
		return false, nil
	}

	return true, nil
}

func (conns *connections) BlacklistAddr(inputAddr net.Addr) error {
	getIpFromAddr := func(inAddr net.Addr) (string, error) {
		if addr, ok := inAddr.(*net.TCPAddr); ok {
			return addr.IP.String(), nil
		} else {
			return "", fmt.Errorf("cannot get IP from addr=%s", inAddr)
		}
	}

	conns.lock.Lock()
	defer conns.lock.Unlock()

	inputIp, err := getIpFromAddr(inputAddr)
	if err != nil {
		return fmt.Errorf("cannot check IsOpen, invalid input address, addr=%s, err=%s", inputAddr.String(), err)
	}

	conns.blacklist[inputIp] = time.Now()
	return nil
}

func (conns *connections) BlacklistIp(ip string) error {
	conns.lock.Lock()
	defer conns.lock.Unlock()
	conns.blacklist[ip] = time.Now()
	return nil
}

func (conns *connections) GetConnectedNumber() int {
	conns.lock.Lock()
	defer conns.lock.Unlock()
	return len(conns.peers)
}
