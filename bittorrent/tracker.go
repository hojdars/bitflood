package bittorrent

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hojdars/bitflood/decode"
	"github.com/hojdars/bitflood/types"
)

func GetPeers(torrent types.TorrentFile, port int) (types.PeerInformation, error) {
	trackerUrl, err := buildGetPeersUrl(torrent, port)
	if err != nil {
		return types.PeerInformation{}, err
	}

	log.Printf("requesting peer information from tracker")
	resp, err := http.Get(trackerUrl)
	if err != nil {
		return types.PeerInformation{}, fmt.Errorf("sending GET request failed, url=%s, error=%e", trackerUrl, err)
	}

	defer resp.Body.Close()

	body, err := decode.DecodePeerInformation(resp.Body)
	if err != nil {
		return types.PeerInformation{}, fmt.Errorf("IO read from HTTP response failed, error=%e", err)
	}
	return body, nil
}

func buildGetPeersUrl(torrent types.TorrentFile, port int) (string, error) {
	trackerUrl, err := url.Parse(torrent.Announce)
	if err != nil {
		return "", fmt.Errorf("cannot parse 'announce' to url, error=%e", err)
	}
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
	return trackerUrl.String(), nil
}
