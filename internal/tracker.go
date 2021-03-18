package internal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/pkg/bencode"
	"io"
	"net/url"
	"strconv"
)

type TrackerTransport struct {
	active        bool
	trackerURL    url.URL
	localConnInfo LocalConnectionInfo
	peerID        string
	trackerID     string
	interval      int
}

func (tt *TrackerTransport) announce(event string, infoHash []byte, stats UpDownStats) (peers []Peer, err error) {
	trackerUrl := tt.trackerURL
	localConnInfo := tt.localConnInfo
	query := trackerUrl.Query()

	query.Set("peer_id", tt.peerID)
	query.Set("info_hash", string(infoHash))
	query.Set("port", strconv.Itoa(localConnInfo.port))

	if localConnInfo.ip != nil {
		query.Set("ip", localConnInfo.ip.String())
	}

	if event != "" {
		query.Set("event", event)
	}

	query.Set("downloaded", strconv.Itoa(stats.downloaded))
	query.Set("uploaded", strconv.Itoa(stats.uploaded))
	query.Set("left", strconv.Itoa(stats.left))

	query.Set("compact", "1")

	if tt.trackerID != "" {
		query.Set("trackerid", tt.trackerID)
	}

	trackerUrl.RawQuery = query.Encode()

	resp, err := httpClient.Get(trackerUrl.String())

	if err != nil {
		return nil, fmt.Errorf("can't reach %v: %v", trackerUrl, err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	bodyBytes, err := io.ReadAll(resp.Body)

	if err != nil {
		panic(err)
	}

	trackerResponse := bencode.Decode(bodyBytes).(map[string]interface{})

	trackerID := trackerResponse["tracker id"]
	tt.interval = trackerResponse[TrackerIntervalKey].(int)

	if trackerID != nil {
		tt.trackerID = trackerID.(string)
	}

	peers = decodePeers(trackerResponse["peers"].(string))

	return peers, nil
}

func decodePeers(ipsString string) (peers []Peer) {
	reader := bytes.NewReader([]byte(ipsString))
	chunk := make([]byte, 6)
	peers = make([]Peer, 0, reader.Size()/6)

	for {
		numRead, err := reader.Read(chunk)

		if err == io.EOF || numRead < 6 {
			break
		}

		peer := Peer{}
		peer.IP = chunk[:4]
		peer.Port = int(binary.BigEndian.Uint16(chunk[4:]))

		peers = append(peers, peer)
	}

	return peers
}
