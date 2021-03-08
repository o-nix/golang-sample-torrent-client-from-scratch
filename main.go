package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"github.com/dchest/uniuri"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/pkg/bencode"
	"io"
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

	client := TorrentClient{
		metadata: metadata,
		peerID:   peerID,
		trackerTransport: TrackerTransport{
			peerID: peerID,
		},
	}

	localConnInfo := LocalConnectionInfo{
		port: 16000 + rand.Intn(32000),
	}

	go client.run(localConnInfo)

	select {} // Wait forever
}

type TrackerTransport struct {
	peerID     string
	trackerID  string
	requestNum int
	interval   int
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

func (tc *TorrentClient) run(localConnInfo LocalConnectionInfo) {
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

	parsedUrl, err := url.Parse(trackerUrl)

	if err != nil {
		panic(fmt.Errorf("cannot parse URL: %v", err))
	}

	ips, err := tc.trackerTransport.announce(*parsedUrl, "started", localConnInfo, infoHash, stats)

	if err != nil {
		panic(err)
	}

	print(ips)

	_, _ = tc.trackerTransport.announce(*parsedUrl, "stopped", localConnInfo, infoHash, stats)
}

type LocalConnectionInfo struct {
	port int
	ip   net.IP
}

func (tt *TrackerTransport) announce(trackerUrl url.URL, event string, localConnInfo LocalConnectionInfo, infoHash []byte, stats UpDownStats) (ips []string, err error) {
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

	if tt.requestNum == 0 {
		query.Set("event", "started")
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
	} else {
		tt.requestNum++
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

	return allIps, nil
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
