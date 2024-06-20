package bittorrent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/types"
)

type Tracker struct {
	url       *url.URL
	tier      int
	tierIndex int
}

type Response struct {
	response *http.Response
	err      error
}

func GetPeers(torrent types.TorrentFile, port int) (types.PeerInformation, error) {
	tier := 0     // start at the first tier
	tierPos := -1 // start at the first tracker of the tier
	for {
		tracker, err := getFirstSupportedTracker(torrent, tier, tierPos)
		if err != nil {
			return types.PeerInformation{}, fmt.Errorf("encoutered error while getting supported tracker, err=%s", err)
		}

		log.Printf("choosing tracker url=%s", tracker.url.String())
		addGetPeersQuery(tracker.url, torrent, port)
		peers, err := getPeers(tracker)

		if err != nil {
			log.Printf("encoutered error, err=%s", err)
			tier = tracker.tier
			tierPos += tracker.tierIndex + 1
			continue
		} else {
			// TODO: According to BEP:12, we should re-order the 'announce-list'
			return peers, nil
		}
	}
}

func getFirstSupportedTracker(torrent types.TorrentFile, startTier, startTierPosition int) (Tracker, error) {
	if len(torrent.AnnounceList) == 0 {
		ann, err := url.Parse(torrent.Announce)
		if err != nil {
			return Tracker{}, err
		} else {
			return Tracker{ann, -1, -1}, nil
		}
	}

	// currently we only support 'http' protocol and we do not support 'uTP' (BEP:29)
	// find the first 'http' announce URL
	for i := startTier; i < len(torrent.AnnounceList); i += 1 {
		start := 0
		if i == startTier {
			start = startTierPosition + 1 // only start at 'startTierPosition+1' if we are still in 'startTier'
		}
		for t := start; t < len(torrent.AnnounceList[i]); t += 1 {
			announceUrl, err := url.Parse(torrent.AnnounceList[i][t])
			if err != nil {
				log.Printf("WARNING: could not parse announcer URL=%s", torrent.AnnounceList[i][t])
				continue
			}
			if announceUrl.Scheme == "http" {
				return Tracker{announceUrl, i, t}, nil
			}
		}
	}

	return Tracker{}, fmt.Errorf("no supported tracker found")
}

func addGetPeersQuery(trackerUrl *url.URL, torrent types.TorrentFile, port int) {
	query := url.Values{}
	query.Add("info_hash", string(torrent.InfoHash[:]))
	query.Add("peer_id", MakePeerId())
	query.Add("port", strconv.Itoa(port))
	query.Add("uploaded", "0")
	query.Add("downloaded", "0")
	query.Add("left", strconv.Itoa(torrent.Length))
	query.Add("event", "started")
	query.Add("compact", strconv.Itoa(1))
	trackerUrl.RawQuery = query.Encode()
}

func getPeers(tracker Tracker) (types.PeerInformation, error) {
	timeout := time.Millisecond * 500
	log.Printf("requesting peer information from tracker, timeout=%d", timeout.Milliseconds())
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", tracker.url.String(), nil)
	if err != nil {
		return types.PeerInformation{}, fmt.Errorf("could not create context, err=%s", err)
	}

	resultChan := make(chan Response)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resultChan <- Response{
				response: nil,
				err:      fmt.Errorf("sending GET request failed, err=%s, url=%s", err, tracker.url),
			}
		} else {
			resultChan <- Response{
				response: resp,
				err:      nil,
			}
		}
	}()

	select {
	case <-ctx.Done():
		return types.PeerInformation{}, fmt.Errorf("GET request timed out, tracker url=%s", tracker.url)
	case resp := <-resultChan:
		return handleGetPeersResponse(resp)
	}
}

func handleGetPeersResponse(response Response) (types.PeerInformation, error) {
	if response.err != nil {
		return types.PeerInformation{}, fmt.Errorf("GET request ended with an error, err=%s", response.err)
	}
	if response.response == nil {
		panic(fmt.Errorf("GET error: both 'err' and 'response' are nil, should never happen"))
	}

	defer response.response.Body.Close()

	if response.response.StatusCode != 200 {
		return types.PeerInformation{}, fmt.Errorf("server answered GET request with an error, code=%d", response.response.StatusCode)
	}

	body, err := decode.DecodePeerInformation(response.response.Body)
	if err != nil {
		return types.PeerInformation{}, fmt.Errorf("IO read from HTTP response failed, err=%s", err)
	}
	return body, nil
}
