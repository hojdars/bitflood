package main

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/hojdars/bitflood/bitfield"
	"github.com/hojdars/bitflood/bittorrent"
	"github.com/hojdars/bitflood/client"
	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/file"
	"github.com/hojdars/bitflood/types"
)

const ConnectionBlacklistDuration = time.Second * 60
const ChokeAlgorithmTick int = 10
const MaximumDownloaders = 20
const LogFile = "testdata/log/all.log"
const Port uint16 = 6881

func listeningServer(ctx context.Context, torrent *types.TorrentFile, comms types.Communication, results *types.Results, conns *Connections) {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", Port))
	if err != nil {
		log.Fatalf("encountered error listening on port=%d, error=%s", Port, err)
	}

	for {
		conn, err := listener.Accept()
		log.Printf("incoming connection, addr=%s", conn.RemoteAddr().String())
		if err != nil {
			log.Printf("ERROR: accept failed, err=%s", err)
			continue
		}

		connection, err := conns.Add(conn.RemoteAddr())
		if err != nil {
			log.Printf("ERROR: adding a connection to pool failed, error=%s", err)
			continue
		}

		newComms := types.Communication{
			Orders:          comms.Orders,
			Results:         comms.Results,
			PeerInterested:  comms.PeerInterested,
			ConnectionEnded: comms.ConnectionEnded,
			PeersToUnchoke:  connection.peersToUnchokeCh,
		}

		go client.Seed(ctx, conn, torrent, newComms, results)
	}
}

func connectToPeer(ctx context.Context, torrent *types.TorrentFile, comms types.Communication, results *types.Results, ip string, port uint16, conns *Connections) error {
	log.Printf("connecting to %s", ip)

	isOpen, err := conns.IsOpen(ip)
	if err != nil {
		return fmt.Errorf("error while checking if already connected to IP=%s, err=%s", ip, err)
	}
	if isOpen {
		return fmt.Errorf("already connected to IP=%s", ip)
	}

	var d net.Dialer
	d.Timeout = time.Millisecond * 250

	address := fmt.Sprintf("%s:%d", ip, port)
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		conns.BlacklistIp(ip)
		return fmt.Errorf("connection to peer=%s failed, err=%s", address, err)
	}

	connection, err := conns.Add(conn.RemoteAddr())
	if err != nil {
		return fmt.Errorf("adding a connection to pool failed, error=%s", err)
	}

	newComms := types.Communication{
		Orders:          comms.Orders,
		Results:         comms.Results,
		PeerInterested:  comms.PeerInterested,
		ConnectionEnded: comms.ConnectionEnded,
		PeersToUnchoke:  connection.peersToUnchokeCh,
	}

	go client.Leech(ctx, conn, torrent, newComms, results)

	return nil
}

func savePartialFiles(torrent types.TorrentFile, results *types.Results, savedPieces *bitfield.Bitfield) error {
	fileNumber := (len(torrent.PieceHashes) / 1000) + 1
	writers := make([]io.Writer, fileNumber)
	for i := 0; i < fileNumber; i += 1 {
		filename := fmt.Sprintf("%s.%d.part", torrent.Name[0:20], i)
		fp, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if err != nil {
			return fmt.Errorf("error while opening file=%s, err=%s", filename, err)
		}
		writers[i] = fp
	}

	err := file.WritePartialFiles(writers, results.Pieces, savedPieces)
	if err != nil {
		return fmt.Errorf("error while writing partial files, err=%s", err)
	}

	return nil
}

func loadFromFile(torrent types.TorrentFile, results *types.Results) (bool, error) {
	fullFile, fileErr := os.Open(torrent.Name)
	if fileErr == nil {
		err := loadPiecesFromCompleteFile(fullFile, torrent, results)
		if err == nil {
			log.Printf("loaded %d pieces from file %s", results.PiecesDone, torrent.Name)
			return true, nil
		}
	}

	if errors.Is(fileErr, os.ErrNotExist) {
		return false, loadPiecesFromPartialFiles(torrent, results)
	} else {
		return false, fmt.Errorf("error while reading file %s, err=%s", torrent.Name, fileErr)
	}
}

func loadPiecesFromCompleteFile(fullFile io.Reader, torrent types.TorrentFile, results *types.Results) error {
	pieces, err := file.Read(fullFile, len(torrent.PieceHashes), torrent.PieceLength)
	if err != nil {
		return fmt.Errorf("error while reading pieces from full file, err=%s", err)
	}
	results.Pieces = pieces
	results.PiecesDone = len(torrent.PieceHashes)
	results.Bitfield = bitfield.NewFull(len(torrent.PieceHashes))

	for _, p := range results.Pieces {
		hash := sha1.Sum(p.Data)
		if hash != torrent.PieceHashes[p.Index] {
			return fmt.Errorf("hash mismatch for piece=%d, want=%s, got=%s", p.Index, string(torrent.PieceHashes[p.Index][:]), string(hash[:]))
		}
	}

	return nil
}

func loadPiecesFromPartialFiles(torrent types.TorrentFile, results *types.Results) error {
	results.Lock.Lock()
	defer results.Lock.Unlock()

	results.PiecesDone = 0
	fileNumber := (len(torrent.PieceHashes) / 1000) + 1
	for i := 0; i < fileNumber; i += 1 {
		filename := fmt.Sprintf("%s.%d.part", torrent.Name[0:20], i)
		numberOfPiecesInFile := 0

		if _, err := os.Stat(filename); err != nil {
			continue
		}

		pfile, err := os.Open(filename)
		if err != nil {
			return fmt.Errorf("cannot open file=%s, err=%s", filename, err)
		}

		res, err := file.ReadPartialFile(pfile, &results.Bitfield)
		if err != nil {
			return fmt.Errorf("error while reading partial file, name=%s, err=%s", filename, err)
		}

		for _, p := range res {
			hash := sha1.Sum(p.Data)
			if hash != torrent.PieceHashes[p.Index] {
				return fmt.Errorf("hash mismatch for piece=%d, want=%s, got=%s", p.Index, string(torrent.PieceHashes[p.Index][:]), string(hash[:]))
			}
			loadedPiece := p
			results.Pieces[loadedPiece.Index] = &loadedPiece
			results.PiecesDone += 1
			numberOfPiecesInFile += 1
			err := results.Bitfield.Set(loadedPiece.Index, true)
			if err != nil {
				return fmt.Errorf("error setting true for bit %d, err=%s", p.Index, err)
			}
		}

		log.Printf("loaded %d pieces from file %s", numberOfPiecesInFile, filename)
	}
	return nil
}

func launchClients(requestedConnections int, ctx context.Context, torrent *types.TorrentFile, comms types.Communication, results *types.Results, peerInfo types.TrackerInformation, conns *Connections) {
	numberOfConnections := 0
	for i := 0; i < len(peerInfo.IPs); i += 1 {
		ip := peerInfo.IPs[i].String()

		// if already open -> skip
		isOpen, e := conns.IsOpen(ip)
		if e != nil {
			log.Printf("ERROR: error while checking if already connected to IP=%s, err=%s, skipping", ip, e)
			continue
		}
		if isOpen {
			continue
		}

		// if blacklisted -> skip
		isBlacklisted, eb := conns.IsBlacklisted(ip)
		if eb != nil {
			log.Printf("ERROR: error while checking if already connected to IP=%s, err=%s, skipping", ip, eb)
			continue
		}
		if isBlacklisted {
			continue
		}

		err := connectToPeer(ctx, torrent, comms, results, ip, peerInfo.Ports[i], conns)
		if err != nil {
			log.Printf("ERROR: encountered an error connecting to target=%s, err=%s", peerInfo.IPs[i], err)
		} else {
			numberOfConnections += 1
			log.Printf("connected to peer number %d, connected to %d peers", i, numberOfConnections)
		}

		if numberOfConnections == requestedConnections {
			break
		}
		if numberOfConnections > requestedConnections {
			panic(fmt.Sprintf("connected to more peers than wanted, wanted to connect to %d, connected to %d", requestedConnections, numberOfConnections))
		}
	}

	if numberOfConnections > 0 {
		log.Printf("launched %d connections, connected to %d peers", numberOfConnections, conns.GetConnectedNumber())
	}
}

func launchTimers(chokeInterval, trackerInterval, loggingInterval int) (chan struct{}, chan struct{}, chan struct{}) {
	chokeAlgCh := make(chan struct{})
	go func() {
		for {
			time.Sleep(time.Second * time.Duration(chokeInterval))
			chokeAlgCh <- struct{}{}
		}
	}()

	trackerUpdateCh := make(chan struct{})
	go func() {
		for {
			time.Sleep(time.Second * time.Duration(trackerInterval))
			trackerUpdateCh <- struct{}{}
		}
	}()

	loggingCh := make(chan struct{})
	go func() {
		for {
			time.Sleep(time.Second * time.Duration(loggingInterval))
			loggingCh <- struct{}{}
		}
	}()

	return chokeAlgCh, trackerUpdateCh, loggingCh
}

type Connection struct {
	ip               net.Addr
	peersToUnchokeCh chan []string // main -> seed, sends array of 'peer-id' of peers that should be unchoked this tick
}

type Connections struct {
	peers     []Connection
	blacklist map[string]time.Time // IP as string -> timestamp of blacklisting
	lock      sync.Mutex
}

func (conns *Connections) IsOpen(ip string) (bool, error) {
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

func (conns *Connections) Add(inputAddr net.Addr) (*Connection, error) {
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

	newConnection := Connection{
		ip:               inputAddr,
		peersToUnchokeCh: make(chan []string, 1),
	}
	conns.peers = append(conns.peers, newConnection)
	return &conns.peers[len(conns.peers)-1], nil
}

func (conns *Connections) Remove(inputAddr net.Addr) error {
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

func (conns *Connections) IsBlacklisted(ip string) (bool, error) {
	conns.lock.Lock()
	defer conns.lock.Unlock()

	timeBlacklisted, present := conns.blacklist[ip]
	if !present {
		return false, nil
	}

	if time.Now().Sub(timeBlacklisted) > ConnectionBlacklistDuration {
		delete(conns.blacklist, ip)
		return false, nil
	}

	return true, nil
}

func (conns *Connections) BlacklistAddr(inputAddr net.Addr) error {
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

func (conns *Connections) BlacklistIp(ip string) error {
	conns.lock.Lock()
	defer conns.lock.Unlock()
	conns.blacklist[ip] = time.Now()
	return nil
}

func (conns *Connections) GetConnectedNumber() int {
	conns.lock.Lock()
	defer conns.lock.Unlock()
	return len(conns.peers)
}

func updateOnlineConnections(connections *Connections, comms *types.Communication, generosity *map[string]int, interestedPeers *map[string]bool) {
	toRemove := make([]net.Addr, 0)
	for {
		done := false
		select {
		case ended, ok := <-comms.ConnectionEnded:
			if ok {
				log.Printf("detected connection closed, peer-id=%s, address=%s", ended.Id, ended.Addr.String())
				toRemove = append(toRemove, ended.Addr)
				delete(*generosity, ended.Id)
				delete(*interestedPeers, ended.Id)
				connections.BlacklistAddr(ended.Addr)
			} else {
				log.Fatalf("ERROR: peer ended channel was closed")
			}
		default:
			done = true
		}
		if done {
			break
		}
	}

	toRemoveLen := len(toRemove)
	for _, r := range toRemove {
		err := connections.Remove(r)
		if err != nil {
			log.Printf("ERROR: error while removing connection=%s, err=%s", r.String(), err)
		}
	}

	if toRemoveLen > 0 {
		log.Printf("finished removing %d offline connections", toRemoveLen)
	}
}

func chokeAlgorithm(generosity map[string]int, interest map[string]bool) []string {
	interestedPeers := make([]string, 0)
	for id, isInterested := range interest {
		if isInterested {
			interestedPeers = append(interestedPeers, id)
		}
	}

	if len(interestedPeers) < 5 {
		return interestedPeers
	}

	sortedGenerosity := make([]struct {
		id         string
		downloaded int
	}, 0)

	for id, downloaded := range generosity {
		if isInterested, isPresent := interest[id]; !isPresent || !isInterested {
			continue
		}

		sortedGenerosity = append(sortedGenerosity, struct {
			id         string
			downloaded int
		}{id, downloaded})
	}

	sort.Slice(sortedGenerosity, func(i, j int) bool {
		return sortedGenerosity[i].downloaded > sortedGenerosity[j].downloaded
	})

	pickedPeers := make([]string, 5)
	for i := 0; i < min(4, len(sortedGenerosity)); i += 1 {
		pickedPeers[i] = sortedGenerosity[i].id
	}

	if len(sortedGenerosity) < 5 {
		return pickedPeers
	}

	fifthRandom := rand.Intn(len(sortedGenerosity) - 4)
	pickedPeers[4] = sortedGenerosity[4+fifthRandom].id

	if len(pickedPeers) != 5 {
		panic(fmt.Sprintf("incorrect number peers picked by the choke algorithm, got %d, expected 5", len(pickedPeers)))
	}
	return pickedPeers
}

func main() {
	if len(os.Args) != 2 {
		fmt.Printf("invalid number of arguments, expected 2, got %v", len(os.Args))
		os.Exit(1)
	}

	f, err := os.OpenFile(LogFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		fmt.Printf("error opening '%s', err=%s", LogFile, err)
		os.Exit(1)
	}

	logWriter := io.MultiWriter(f, os.Stderr)
	log.SetOutput(logWriter)

	filename := os.Args[1]

	if _, err := os.Stat(filename); err != nil {
		log.Fatalf("ERROR: file does not exist, file=%s", filename)
	}

	log.Printf("started on file=%s\n", filename)

	torrentFile, err := os.Open(filename)
	if err != nil {
		log.Fatalf("ERROR: cannot open file, err=%s", err)
	}

	torrent, err := decode.DecodeTorrentFile(torrentFile)
	if err != nil {
		log.Fatalf("ERROR: encountered an error during .torrent file decoding, err=%s", err)
	}

	if torrent.Length == 0 {
		// TODO: specification requires either 'length' or 'key files', implement 'key files'
		log.Fatalf("ERROR: key 'length' is missing, unsupported .torrent file")
	}

	log.Printf("torrent file=%s, size=%s, pieces=%d", torrent.Name, humanize.Bytes(uint64(torrent.Length)), len(torrent.PieceHashes))

	// load the file or PartialFiles, verify all pieces hashes, create BitField
	results := types.Results{Pieces: make([]*types.Piece, len(torrent.PieceHashes)), Bitfield: bitfield.New(len(torrent.PieceHashes)), Lock: sync.RWMutex{}}

	alreadySavedFinalFile, err := loadFromFile(torrent, &results)
	if err != nil {
		log.Fatalf("ERROR: encountered an error while loading data from files, err=%s", err)
	}
	alreadySavedNumber := results.PiecesDone
	alreadySavedPieces := bitfield.Copy(&results.Bitfield)

	peerId := bittorrent.MakePeerId()
	leftToDownload := torrent.Length - results.PiecesDone*torrent.PieceLength
	trackerInfo, err := bittorrent.GetTrackerFromFile(torrent, peerId, Port, 0, 0, leftToDownload)
	if err != nil {
		log.Fatalf("ERROR: encountered an error while retrieving peers from tracker, err=%s", err)
	}
	log.Printf("set peer-id to=%s, received %d peers, interval=%d", peerId, len(trackerInfo.IPs), trackerInfo.Interval)

	sharedComms := types.Communication{
		Orders:          make(chan *types.PieceOrder, len(torrent.PieceHashes)),
		Results:         make(chan *types.Piece, len(torrent.PieceHashes)),
		PeerInterested:  make(chan types.PeerInterest, len(trackerInfo.IPs)+50),
		ConnectionEnded: make(chan types.ConnectionEnd, len(trackerInfo.IPs)+50),
		Uploaded:        make(chan int, len(trackerInfo.IPs)+50),
	}

	for _, r := range rand.Perm(torrent.GetNumberOfPieces()) {
		have, err := results.Bitfield.Get(r)
		if err != nil {
			log.Fatalf("ERROR: encountered error while checking bitfield, err=%s", err)
		}
		if have {
			continue
		}
		p := &types.PieceOrder{Index: r, Length: torrent.PieceLength, Hash: torrent.PieceHashes[r]}
		sharedComms.Orders <- p
	}

	mainCtx, cancel := context.WithCancel(context.WithValue(context.Background(), "peer-id", peerId))

	connections := Connections{peers: make([]Connection, 0), blacklist: make(map[string]time.Time), lock: sync.Mutex{}}

	go listeningServer(mainCtx, &torrent, sharedComms, &results, &connections)

	if results.PiecesDone != len(torrent.PieceHashes) {
		launchClients(MaximumDownloaders, mainCtx, &torrent, sharedComms, &results, trackerInfo, &connections)
	}

	signalCh := make(chan os.Signal, MaximumDownloaders)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGINT)

	chokeAlgorithmCh, trackerUpdateCh, loggingCh := launchTimers(ChokeAlgorithmTick, trackerInfo.Interval, 1)

	uploadedPieces := 0
	generosityMap := make(map[string]int)
	interestedPeers := make(map[string]bool)
	for {
		exit := false

		select {
		case <-signalCh:
			log.Printf("SIGINT caught, terminating")
			cancel()
			time.Sleep(time.Second)
			exit = true
		case <-loggingCh:
			results.Lock.Lock()
			piecesDone := results.PiecesDone
			results.Lock.Unlock()
			if piecesDone != len(torrent.PieceHashes) {
				piecePercent := 100.0 * float32(piecesDone) / float32(len(torrent.PieceHashes))
				log.Printf("downloaded %d/%d pieces, %f%%, uploaded %d pieces", piecesDone, len(torrent.PieceHashes), piecePercent, uploadedPieces)
			}
		case piece := <-sharedComms.Results:
			results.Lock.Lock()
			results.Pieces[piece.Index] = piece
			results.PiecesDone += 1
			err := results.Bitfield.Set(piece.Index, true)
			results.Lock.Unlock()
			if err != nil {
				log.Fatalf("ERROR: encountered an error while setting a bit in bitfield to true, index=%d, err=%s", piece.Index, err)
			}
			_, found := generosityMap[piece.DownloadedFromId]
			if found {
				generosityMap[piece.DownloadedFromId] += 1
			} else {
				generosityMap[piece.DownloadedFromId] = 1
			}
		case <-chokeAlgorithmCh:
			unchokedPeers := chokeAlgorithm(generosityMap, interestedPeers)
			for _, peer := range connections.peers {
				select {
				case peer.peersToUnchokeCh <- unchokedPeers:
					continue
				default:
					log.Printf("WARN: peer=%s is not reading their choke-algorithm channel", peer.ip)
				}
			}
			log.Printf("choke algorithm tick complete, unchoking=%v, generosity=%v, interest=%v", unchokedPeers, generosityMap, interestedPeers)
		case <-trackerUpdateCh:
			results.Lock.Lock()
			uploaded := uploadedPieces * torrent.PieceLength
			downloaded := (results.PiecesDone - alreadySavedNumber) * torrent.PieceLength
			leftToDownload := torrent.Length - results.PiecesDone*torrent.PieceLength
			results.Lock.Unlock()
			trackerInfo, err = bittorrent.UpdateTracker(trackerInfo.Tracker, torrent, peerId, Port, uploaded, downloaded, leftToDownload)
			if err != nil {
				trackerInfo, err = bittorrent.GetTrackerFromFile(torrent, peerId, Port, uploaded, downloaded, leftToDownload)
				if err != nil {
					log.Printf("ERROR: encountered a fatal error while updating tracker, exiting, err=%s", err)
					exit = true // cannot get a tracker -> unrecoverable error -> shutdown
				}
			}
			log.Printf("tracker updated, uploaded=%d, downloaded=%d, left-to-download=%d", uploaded, downloaded, leftToDownload)
		case interestedPeer := <-sharedComms.PeerInterested:
			interestedPeers[interestedPeer.Id] = interestedPeer.IsInterested
			log.Printf("interested peer registered, id=%s, isInterested=%t", interestedPeer.Id, interestedPeer.IsInterested)
		case <-sharedComms.Uploaded:
			uploadedPieces += 1
			log.Printf("piece uploaded, total uploaded pieces=%d", uploadedPieces)
		}

		updateOnlineConnections(&connections, &sharedComms, &generosityMap, &interestedPeers)

		if exit {
			break
		}

		if results.PiecesDone != len(torrent.PieceHashes) && len(connections.peers) < MaximumDownloaders {
			launchClients(MaximumDownloaders-len(connections.peers), mainCtx, &torrent, sharedComms, &results, trackerInfo, &connections)
		}

		if results.PiecesDone == len(torrent.PieceHashes) && !alreadySavedFinalFile {
			alreadySavedFinalFile = true
			resultFile, err := os.Create(torrent.Name)
			if err != nil {
				log.Printf("ERROR: encountered error while writing result file, err=%s", err)
			}
			results.Lock.RLock()
			file.Save(resultFile, results.Pieces)
			results.Lock.RUnlock()
			log.Printf("file saved, name=%s", torrent.Name)
		}
	}

	// if the partial files were incomplete at start, save them
	if alreadySavedNumber < torrent.GetNumberOfPieces() {
		log.Printf("saving %d pieces", results.PiecesDone)
		err = savePartialFiles(torrent, &results, &alreadySavedPieces)
		if err != nil {
			log.Printf("ERROR: encountered error while saving partial files, err=%s", err)
		}
	}

	os.Exit(0)
}
