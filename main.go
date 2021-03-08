package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"github.com/dchest/uniuri"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/pkg/bencode"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	var filePath = "golang-for-prof.torrent"

	content, err := os.ReadFile(filePath)

	if err != nil {
		panic("Error reading torrent file")
	}

	metadata := createTorrentInfo(bencode.Decode(content))

	// Generate new ID for the whole app: https://wiki.theory.org/BitTorrentSpecification#peer_id
	peerID := "-DF0001-" + uniuri.NewLen(12)

	client := TorrentClient{
		metadata: metadata,
		peerID:   peerID,
		trackerTransport: TrackerTransport{
			peerID: peerID,
		},
	}

	go client.run()

	select {} // Wait forever
}

type TrackerTransport struct {
	peerID    string
	trackerID string
	interval  int
}

type Peer struct {
	id   string
	ip   string
	port int
}

type UpDownStats struct {
	downloaded int
	uploaded   int
	left       int
}

type TorrentMetadata struct {
	announces []string
	infoHash  []byte
	files     []FileInfo
	raw       map[string]interface{}
	folder    string
}

type TorrentClient struct {
	metadata         TorrentMetadata
	trackerTransport TrackerTransport
	peerID           string
	trackerId        string
	peers            []Peer
	stats            UpDownStats
}

func (tc *TorrentClient) run() {
	trackerUrl := tc.metadata.announces[0]
	infoHash := tc.metadata.infoHash
	left := 0

	for _, file := range tc.metadata.files {
		left += file.size
	}

	stats := UpDownStats{
		downloaded: 0,
		uploaded:   0,
		left:       left,
	}

	tc.trackerTransport.announceStart(trackerUrl, infoHash, stats)
}

func (tt *TrackerTransport) announceStart(trackerUrl string, infoHash []byte, stats UpDownStats) {
	parsedUrl, err := url.Parse(trackerUrl)

	if err != nil {
		panic(fmt.Errorf("cannot parse URL: %v", err))
	}

	query := parsedUrl.Query()

	query.Set("peer_id", tt.peerID)
	query.Set("info_hash", string(infoHash))
	query.Set("port", "6881")
	query.Set("event", "started")

	query.Set("downloaded", strconv.Itoa(stats.downloaded))
	query.Set("uploaded", strconv.Itoa(stats.uploaded))
	query.Set("left", strconv.Itoa(stats.left))

	query.Set("compact", "1")

	if tt.trackerID != "" {
		query.Set("trackerid", tt.trackerID)
	}

	parsedUrl.RawQuery = query.Encode()

	resp, err := http.Get(parsedUrl.String())

	if err != nil {
		panic(err)
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

	allIps := decodeIPs(trackerResponse["peers"].(string))
	print(allIps)
}

func decodeIPs(IPsString string) []string {
	reader := bytes.NewReader([]byte(IPsString))
	chunk := make([]byte, 6, 6)
	addrs := make([]string, 0, reader.Size()/6)

	for {
		numRead, err := reader.Read(chunk)

		if err == io.EOF || numRead < 6 {
			break
		}

		ip := net.IP(chunk[:4])
		portAsNumber := binary.BigEndian.Uint16(chunk[4:])
		addr := net.JoinHostPort(ip.String(), strconv.Itoa(int(portAsNumber)))

		addrs = append(addrs, addr)
	}

	return addrs
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
		announces: announces,
		infoHash:  infoHash[:],
		folder:    topLevelName,
		files:     files,
		raw:       dict,
	}
}
