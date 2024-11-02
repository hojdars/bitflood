package bittorrent

import (
	"context"
	"fmt"
	"log/slog"
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

func GetTrackerFromFile(torrent types.TorrentFile, peerId string, port uint16, uploaded, downloaded, leftToDownload int) (types.TrackerInformation, error) {
	tier := 0     // start at the first tier
	tierPos := -1 // start at the first tracker of the tier
	for {
		tracker, err := getFirstSupportedTracker(torrent, tier, tierPos)
		if err != nil {
			return types.TrackerInformation{}, fmt.Errorf("encoutered error while getting supported tracker, err=%s", err)
		}

		addGetPeersQuery(tracker.url, torrent, peerId, port, uploaded, downloaded, leftToDownload)
		result, err := getPeers(tracker)

		if err != nil {
			slog.Error("error while contacting tracker", slog.String("tracker", tracker.url.String()), slog.String("err", err.Error()))
			tier = tracker.tier
			tierPos += tracker.tierIndex + 1
			continue
		} else {
			// TODO: According to BEP:12, we should re-order the 'announce-list'
			result.Tracker = tracker.url
			result.Tracker.RawQuery = ""
			slog.Debug("choosen tracker", slog.String("url", tracker.url.String()))
			return result, nil
		}
	}
}

func UpdateTracker(trackerUrl *url.URL, torrent types.TorrentFile, peerId string, port uint16, uploaded, downloaded, leftToDownload int) (types.TrackerInformation, error) {
	tracker := Tracker{url: trackerUrl}
	addGetPeersQuery(tracker.url, torrent, peerId, port, uploaded, downloaded, leftToDownload)
	result, err := getPeers(tracker)
	if err != nil {
		slog.Error("error while contacting tracker", slog.String("tracker", tracker.url.String()), slog.String("err", err.Error()))
		return types.TrackerInformation{}, fmt.Errorf("encountered error while updating tracker, err=%s", err)
	}

	result.Tracker = tracker.url
	result.Tracker.RawQuery = ""
	return result, nil
}

type response struct {
	response *http.Response
	err      error
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
				slog.Warn("could not parse announcer URL", slog.String("url", torrent.AnnounceList[i][t]))
				continue
			}
			if announceUrl.Scheme == "http" {
				return Tracker{announceUrl, i, t}, nil
			}
		}
	}

	return Tracker{}, fmt.Errorf("no supported tracker found")
}

func addGetPeersQuery(trackerUrl *url.URL, torrent types.TorrentFile, peerId string, port uint16, uploaded, downloaded, leftToDownload int) {
	slog.Debug("creating GET request for tracker", slog.Int("port", int(port)), slog.Int("uploaded", uploaded), slog.Int("downloaded", downloaded), slog.Int("left-to-download", leftToDownload))
	query := url.Values{}
	query.Add("info_hash", string(torrent.InfoHash[:]))
	query.Add("peer_id", peerId)
	query.Add("port", strconv.Itoa(int(port)))
	query.Add("uploaded", strconv.Itoa(uploaded))
	query.Add("downloaded", strconv.Itoa(downloaded))
	query.Add("left", strconv.Itoa(leftToDownload))
	query.Add("event", "started")
	query.Add("compact", strconv.Itoa(1))
	trackerUrl.RawQuery = query.Encode()
}

func getPeers(tracker Tracker) (types.TrackerInformation, error) {
	timeout := time.Millisecond * 500
	slog.Debug("posting GET request to tracker", slog.Int("timeout", int(timeout.Milliseconds())))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", tracker.url.String(), nil)
	if err != nil {
		return types.TrackerInformation{}, fmt.Errorf("could not create context, err=%s", err)
	}

	resultChan := make(chan response)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resultChan <- response{
				response: nil,
				err:      fmt.Errorf("sending GET request failed, err=%s, url=%s", err, tracker.url),
			}
		} else {
			resultChan <- response{
				response: resp,
				err:      nil,
			}
		}
	}()

	select {
	case <-ctx.Done():
		return types.TrackerInformation{}, fmt.Errorf("GET request timed out, tracker url=%s", tracker.url)
	case resp := <-resultChan:
		return handleGetPeersResponse(resp)
	}
}

func handleGetPeersResponse(response response) (types.TrackerInformation, error) {
	if response.err != nil {
		return types.TrackerInformation{}, fmt.Errorf("GET request ended with an error, err=%s", response.err)
	}
	if response.response == nil {
		panic(fmt.Errorf("GET error: both 'err' and 'response' are nil, should never happen"))
	}

	defer response.response.Body.Close()

	if response.response.StatusCode != 200 {
		return types.TrackerInformation{}, fmt.Errorf("server answered GET request with an error, code=%d", response.response.StatusCode)
	}

	body, err := decode.DecodePeerInformation(response.response.Body)
	if err != nil {
		return types.TrackerInformation{}, fmt.Errorf("IO read from HTTP response failed, err=%s", err)
	}
	return body, nil
}
