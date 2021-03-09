package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"github.com/dchest/uniuri"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/pkg/bencode"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var httpClient http.Client

func init() {
	httpClient = http.Client{
		Timeout: time.Second * 30,
	}

	rand.Seed(time.Now().UnixNano())
}

func main() {
	var filePath = "golang-for-prof.torrent"

	content, err := os.ReadFile(filePath)

	if err != nil {
		panic("Error reading torrent file")
	}

	metadata := createTorrentInfo(bencode.Decode(content))

	// Generate new ID for the whole app: https://wiki.theory.org/BitTorrentSpecification#peer_id
	peerID := "-DF0001-" + uniuri.NewLen(12)

	localConnInfo := LocalConnectionInfo{
		port:   16000 + rand.Intn(32000),
		peerID: peerID,
	}

	client := TorrentClient{
		metadata: metadata,
	}

	for _, announceUrl := range metadata.announceUrls {
		parsedUrl, err := url.Parse(announceUrl)

		if err != nil {
			log.Printf("Malformed tracker URL: %s", announceUrl)
			continue
		}

		transport := TrackerTransport{
			active:        true,
			trackerUrl:    *parsedUrl,
			localConnInfo: localConnInfo,
		}

		client.trackers = append(client.trackers, transport)
	}

	go client.run()

	select {} // Wait forever
}

type TrackerTransport struct {
	active        bool
	trackerUrl    url.URL
	localConnInfo LocalConnectionInfo
	peerID        string
	trackerID     string
	interval      int
}

type Peer struct {
	id   string
	ip   net.IP
	port int
}

type UpDownStats struct {
	downloaded int
	uploaded   int
	left       int
}

type TorrentMetadata struct {
	announceUrls []string
	infoHash     []byte
	files        []FileInfo
	raw          map[string]interface{}
	folder       string
}

type TorrentClient struct {
	metadata  TorrentMetadata
	trackers  []TrackerTransport
	peerID    string
	trackerId string
	peers     []Peer
	stats     UpDownStats
}

func (tc *TorrentClient) run() {
	tc.recalculateStats()

	infoHash := tc.metadata.infoHash
	stats := tc.stats

	for _, tracker := range tc.trackers {
		peers, err := tracker.announce("started", infoHash, stats)

		if err != nil {
			tracker.active = false
		} else {
		outer:
			for _, newPeer := range peers {
				for _, existingPeer := range tc.peers {
					if newPeer.ip.Equal(existingPeer.ip) && newPeer.port == existingPeer.port {
						continue outer
					}
				}

				tc.peers = append(tc.peers, peers...)
			}
		}
	}

	time.Sleep(time.Second * 2)

	for _, tracker := range tc.trackers {
		if tracker.active {
			_, _ = tracker.announce("stopped", infoHash, stats)
		}
	}
}

func (tc *TorrentClient) recalculateStats() {
	left := 0

	for _, file := range tc.metadata.files {
		left += file.size
	}

	tc.stats = UpDownStats{
		downloaded: 0,
		uploaded:   0,
		left:       left,
	}
}

type LocalConnectionInfo struct {
	port   int
	ip     net.IP
	peerID string
}

func (tt *TrackerTransport) announce(event string, infoHash []byte, stats UpDownStats) (peers []Peer, err error) {
	trackerUrl := tt.trackerUrl
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
	tt.interval = trackerResponse["interval"].(int)

	if trackerID != nil {
		tt.trackerID = trackerID.(string)
	}

	peers = decodePeers(trackerResponse["peers"].(string))

	return peers, nil
}

func decodePeers(IPsString string) (peers []Peer) {
	reader := bytes.NewReader([]byte(IPsString))
	chunk := make([]byte, 6, 6)
	peers = make([]Peer, 0, reader.Size()/6)

	for {
		numRead, err := reader.Read(chunk)

		if err == io.EOF || numRead < 6 {
			break
		}

		peer := Peer{}
		peer.ip = chunk[:4]
		peer.port = int(binary.BigEndian.Uint16(chunk[4:]))

		peers = append(peers, peer)
	}

	return peers
}

type FileInfo struct {
	path string
	size int
}

func createTorrentInfo(untyped interface{}) TorrentMetadata {
	dict := untyped.(map[string]interface{})

	annListValue := dict["announce-list"].([]interface{})
	announces := []string{dict["announce"].(string)}

	for _, listWithElemsValue := range annListValue {
	uniqueCheck:
		for _, elemValue := range listWithElemsValue.([]interface{}) {
			for _, alreadyAddedAnn := range announces {
				if elemValue == alreadyAddedAnn {
					break uniqueCheck
				}
			}

			announces = append(announces, elemValue.(string))
		}
	}

	info := dict["info"].(map[string]interface{})
	infoHash := sha1.Sum(bencode.Encode(info))

	var files []FileInfo
	topLevelName := info["name"].(string)

	if fileEntries := info["files"]; fileEntries != nil {
		for _, fileEntry := range fileEntries.([]map[string]interface{}) {
			var builder strings.Builder

			for _, pathComponent := range fileEntry["path"].([]string) {
				if builder.Len() > 0 {
					builder.WriteRune(filepath.Separator)
				}

				builder.WriteString(pathComponent)
			}

			files = append(files, FileInfo{
				size: fileEntry["length"].(int),
				path: builder.String(),
			})
		}
	} else {
		files = []FileInfo{
			{
				path: topLevelName,
				size: info["length"].(int),
			},
		}

		topLevelName = ""
	}

	return TorrentMetadata{
		announceUrls: announces,
		infoHash:     infoHash[:],
		folder:       topLevelName,
		files:        files,
		raw:          dict,
	}
}
