package client

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/file"
	"github.com/hojdars/bitflood/types"
)

const connectionBlacklistDuration = time.Second * 60
const chokeAlgorithmTick int = 10
const maximumDownloaders = 20

func Main(filename string, port uint16) {
	if _, err := os.Stat(filename); err != nil {
		slog.Error("file does not exist", slog.String("file", filename))
		os.Exit(1)
	}

	slog.Info("started", "file", filename)

	torrentFile, err := os.Open(filename)
	if err != nil {
		slog.Error("error while opening a file", slog.String("file", filename), slog.String("err", err.Error()))
		os.Exit(1)
	}

	torrent, err := decode.DecodeTorrentFile(torrentFile)
	if err != nil {
		slog.Error("error while decoding the .torrent file", slog.String("err", err.Error()))
		os.Exit(1)
	}

	if torrent.Length == 0 {
		// TODO: specification requires either 'length' or 'key files', implement 'key files'
		slog.Error("unsupported .torrent file, key 'length' is missing")
		os.Exit(1)
	}

	slog.Info("torrent file decoded", slog.String("file", torrent.Name), slog.String("size", humanize.Bytes(uint64(torrent.Length))), slog.Int("pieces", len(torrent.PieceHashes)))

	// load the file or PartialFiles, verify all pieces hashes, create BitField
	results := types.Results{Pieces: make([]*types.Piece, len(torrent.PieceHashes)), Bitfield: bitfield.New(len(torrent.PieceHashes)), Lock: sync.RWMutex{}}

	alreadySavedFinalFile, err := loadFromFile(torrent, &results)
	if err != nil {
		slog.Error("error while loading data from downloaded files", slog.String("err", err.Error()))
		os.Exit(1)
	}
	alreadySavedNumber := results.PiecesDone
	alreadySavedPieces := bitfield.Copy(&results.Bitfield)

	peerId := bittorrent.MakePeerId()
	slog.Debug("peer-id generated", slog.String("peer-id", peerId))

	leftToDownload := torrent.Length - results.PiecesDone*torrent.PieceLength
	// TODO: Critical - report the correct information to the tracker
	trackerInfo, err := bittorrent.GetTrackerFromFile(torrent, peerId, port, 0, 0, leftToDownload)
	if err != nil {
		slog.Error("error while retrieving peers from tracker", slog.String("err", err.Error()))
		os.Exit(1)
	}

	slog.Info("received response from tracker", slog.Int("peers", len(trackerInfo.IPs)), slog.Int("interval", trackerInfo.Interval))

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
			slog.Error("error while checking bitfield", slog.String("err", err.Error()))
			os.Exit(1)
		}
		if have {
			continue
		}
		p := &types.PieceOrder{Index: r, Length: torrent.PieceLength, Hash: torrent.PieceHashes[r]}
		sharedComms.Orders <- p
	}

	mainCtx, cancel := context.WithCancel(context.WithValue(context.Background(), "peer-id", peerId))

	connections := connections{peers: make([]connection, 0), blacklist: make(map[string]time.Time), lock: sync.Mutex{}}

	go listeningServer(mainCtx, &torrent, sharedComms, &results, &connections, port)

	if results.PiecesDone != len(torrent.PieceHashes) {
		launchClients(maximumDownloaders, mainCtx, &torrent, sharedComms, &results, trackerInfo, &connections)
	}

	signalCh := make(chan os.Signal, maximumDownloaders)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGINT)

	chokeAlgorithmCh, trackerUpdateCh, loggingCh := launchTimers(chokeAlgorithmTick, trackerInfo.Interval, 1)

	uploadedPieces := 0
	generosityMap := make(map[string]int)
	interestedPeers := make(map[string]bool)
	for {
		exit := false

		select {
		case <-signalCh:
			exit = true
			slog.Info("SIGINT caught, terminating")
		case <-loggingCh:
			results.Lock.Lock()
			piecesDone := results.PiecesDone
			results.Lock.Unlock()
			if piecesDone != len(torrent.PieceHashes) {
				piecePercent := 100.0 * float32(piecesDone) / float32(len(torrent.PieceHashes))
				fmt.Printf("downloaded %d/%d pieces, %f%%, uploaded %d pieces\n", piecesDone, len(torrent.PieceHashes), piecePercent, uploadedPieces)
			}
		case piece := <-sharedComms.Results:
			results.Lock.Lock()
			results.Pieces[piece.Index] = piece
			results.PiecesDone += 1
			err := results.Bitfield.Set(piece.Index, true)
			results.Lock.Unlock()
			if err != nil {
				slog.Error("encountered an error while setting a bit in bitfield to true", slog.Int("index", piece.Index), slog.String("err", err.Error()))
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
					slog.Warn("peer is not reading their choke-algorithm channel", slog.String("peer", peer.ip.String()))
				}
			}
			slog.Info("choke algorithm tick complete", "unchoking", unchokedPeers, "generosity", generosityMap, "interest", interestedPeers)
		case <-trackerUpdateCh:
			results.Lock.Lock()
			uploaded := uploadedPieces * torrent.PieceLength
			downloaded := (results.PiecesDone - alreadySavedNumber) * torrent.PieceLength
			leftToDownload := torrent.Length - results.PiecesDone*torrent.PieceLength
			results.Lock.Unlock()
			trackerInfo, err = bittorrent.UpdateTracker(trackerInfo.Tracker, torrent, peerId, port, uploaded, downloaded, leftToDownload)
			if err != nil {
				trackerInfo, err = bittorrent.GetTrackerFromFile(torrent, peerId, port, uploaded, downloaded, leftToDownload)
				if err != nil {
					exit = true // cannot get a tracker -> unrecoverable error -> shutdown
					slog.Error("encountered a fatal error while updating tracker, exiting", slog.String("err", err.Error()))
				}
			}
			slog.Info("tracker updated", slog.Int("uploaded", uploaded), slog.Int("downloaded", downloaded), slog.Int("left to download", leftToDownload))
		case interestedPeer := <-sharedComms.PeerInterested:
			interestedPeers[interestedPeer.Id] = interestedPeer.IsInterested
			if interestedPeer.IsInterested {
				slog.Info("interested peer detected", slog.String("peer-id", interestedPeer.Id))
			}
		case <-sharedComms.Uploaded:
			uploadedPieces += 1
			slog.Debug("piece uploaded", slog.Int("total uploaded pieces", uploadedPieces))
		}

		updateOnlineConnections(&connections, &sharedComms, &generosityMap, &interestedPeers)

		if exit {
			cancel()
			time.Sleep(time.Second)
			break
		}

		if results.PiecesDone != len(torrent.PieceHashes) && len(connections.peers) < maximumDownloaders {
			launchClients(maximumDownloaders-len(connections.peers), mainCtx, &torrent, sharedComms, &results, trackerInfo, &connections)
		}

		if results.PiecesDone == len(torrent.PieceHashes) && !alreadySavedFinalFile {
			alreadySavedFinalFile = true
			resultFile, err := os.Create(torrent.Name)
			if err != nil {
				slog.Error("encountered error while writing result file", slog.String("file", torrent.Name), slog.String("err", err.Error()))
			}
			results.Lock.RLock()
			file.Save(resultFile, results.Pieces)
			results.Lock.RUnlock()
			slog.Info("file saved", slog.String("filename", torrent.Name))
		}
	}

	// if the partial files were incomplete at start, save them
	// TODO: Critical - do we really want the partial files even if the whole file is saved? Maybe add results.PiecesDone != len(torrent.PieceHashes)?
	if alreadySavedNumber < torrent.GetNumberOfPieces() {
		slog.Info("saving pieces to partial files", slog.Int("pieces", results.PiecesDone))
		err = savePartialFiles(torrent, &results, &alreadySavedPieces)
		if err != nil {
			slog.Error("encountered error while saving partial files", slog.String("err", err.Error()))
		}
	}
}

func listeningServer(ctx context.Context, torrent *types.TorrentFile, comms types.Communication, results *types.Results, conns *connections, port uint16) {
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		slog.Error("error while listening on port", slog.Int("port", int(port)), slog.String("err", err.Error()))
		os.Exit(1)
	}

	for {
		conn, err := listener.Accept()
		slog.Info("incoming connection", slog.String("addr", conn.RemoteAddr().String()))
		if err != nil {
			slog.Error("error while accepting connection", slog.String("addr", conn.RemoteAddr().String()), slog.String("err", err.Error()))
			continue
		}

		connection, err := conns.Add(conn.RemoteAddr())
		if err != nil {
			slog.Error("adding a connection to pool failed", slog.String("addr", conn.RemoteAddr().String()), slog.String("err", err.Error()))
			continue
		}

		newComms := types.Communication{
			Orders:          comms.Orders,
			Results:         comms.Results,
			PeerInterested:  comms.PeerInterested,
			ConnectionEnded: comms.ConnectionEnded,
			PeersToUnchoke:  connection.peersToUnchokeCh,
		}

		go seed(ctx, conn, torrent, newComms, results)
	}
}

func connectToPeer(ctx context.Context, torrent *types.TorrentFile, comms types.Communication, results *types.Results, ip string, port uint16, conns *connections) error {
	slog.Debug("connecting to peer", slog.String("ip", ip))

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

	go leech(ctx, conn, torrent, newComms, results)

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
			slog.Debug("loaded data from file", slog.Int("pieces", results.PiecesDone), slog.String("file", torrent.Name))
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

		slog.Debug("loaded data from file", slog.Int("pieces", numberOfPiecesInFile), slog.String("file", filename))
	}
	return nil
}

func launchClients(requestedConnections int, ctx context.Context, torrent *types.TorrentFile, comms types.Communication, results *types.Results, peerInfo types.TrackerInformation, conns *connections) {
	numberOfConnections := 0
	for i := 0; i < len(peerInfo.IPs); i += 1 {
		ip := peerInfo.IPs[i].String()

		// if already open -> skip
		isOpen, e := conns.IsOpen(ip)
		if e != nil {
			slog.Error("error while checking if already connected to IP, skipping", slog.String("ip", ip), slog.String("err", e.Error()))
			continue
		}
		if isOpen {
			continue
		}

		// if blacklisted -> skip
		isBlacklisted, eb := conns.IsBlacklisted(ip)
		if eb != nil {
			slog.Error("error while checking if IP is blacklisted, skipping", slog.String("ip", ip), slog.String("err", eb.Error()))
			continue
		}
		if isBlacklisted {
			continue
		}

		err := connectToPeer(ctx, torrent, comms, results, ip, peerInfo.Ports[i], conns)
		if err != nil {
			slog.Error("connecting to the target failed", slog.String("ip", peerInfo.IPs[i].String()), slog.String("err", err.Error()))
		} else {
			numberOfConnections += 1
			slog.Debug("connection to peer successful", slog.Int("peer number", i), slog.Int("total connections", numberOfConnections))
		}

		if numberOfConnections == requestedConnections {
			break
		}
		if numberOfConnections > requestedConnections {
			panic(fmt.Sprintf("connected to more peers than wanted, wanted to connect to %d, connected to %d", requestedConnections, numberOfConnections))
		}
	}

	if numberOfConnections > 0 {
		slog.Debug("connections launched", slog.Int("launched", numberOfConnections), slog.Int("total", conns.GetConnectedNumber()))
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

func updateOnlineConnections(connections *connections, comms *types.Communication, generosity *map[string]int, interestedPeers *map[string]bool) {
	toRemove := make([]net.Addr, 0)
	for {
		done := false
		select {
		case ended, ok := <-comms.ConnectionEnded:
			if ok {
				slog.Info("detected a closed connection", slog.String("peer-id", ended.Id), slog.String("addr", ended.Addr.String()))
				toRemove = append(toRemove, ended.Addr)
				delete(*generosity, ended.Id)
				delete(*interestedPeers, ended.Id)
				connections.BlacklistAddr(ended.Addr)
			} else {
				slog.Error("peer ended channel was closed")
				os.Exit(1)
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
			slog.Error("error while removing connection", slog.String("connection", r.String()), slog.String("err", err.Error()))
		}
	}

	if toRemoveLen > 0 {
		slog.Debug("all offline connections removed", slog.Int("removed", toRemoveLen))
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
