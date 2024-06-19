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
		return types.PeerInformation{}, fmt.Errorf("sending GET request failed, url=%s, err=%s", trackerUrl, err)
	}

	defer resp.Body.Close()

	body, err := decode.DecodePeerInformation(resp.Body)
	if err != nil {
		return types.PeerInformation{}, fmt.Errorf("IO read from HTTP response failed, err=%s", err)
	}
	return body, nil
}

func buildGetPeersUrl(torrent types.TorrentFile, port int) (string, error) {
	trackerUrl, err := getFirstSupportedUrl(torrent)
	log.Printf("tracker url=%s", trackerUrl.String())
	if err != nil {
		return "", fmt.Errorf("cannot parse 'announce' to url, err=%s", err)
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

func getFirstSupportedUrl(torrent types.TorrentFile) (*url.URL, error) {
	if len(torrent.AnnounceList) == 0 {
		ann, err := url.Parse(torrent.Announce)
		if err != nil {
			return &url.URL{}, err
		} else {
			return ann, nil
		}
	}

	// currently we only support 'http' protocol and we do not support 'uTP' (BEP:29)
	// find the first 'http' announce URL
	for i := 0; i < len(torrent.AnnounceList); i += 1 {
		for t := 0; t < len(torrent.AnnounceList[i]); t += 1 {
			announceUrl, err := url.Parse(torrent.AnnounceList[i][t])
			if err != nil {
				log.Printf("WARNING: could not parse announcer URL=%s", torrent.AnnounceList[i][t])
				continue
			}
			if announceUrl.Scheme == "http" {
				return announceUrl, nil
			}
		}
	}

	return &url.URL{}, nil
}
