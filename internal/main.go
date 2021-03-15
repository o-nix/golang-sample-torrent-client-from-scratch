package internal

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

const (
	TrackerStartedEvent = "started"
	TrackerStoppedEvent = "stopped"
	TrackerIntervalKey  = "interval"
)

func init() {
	httpClient = http.Client{
		Timeout: time.Second * 30,
	}

	rand.Seed(time.Now().UnixNano())
}

func Start(filePath string) {
	content, err := os.ReadFile(filePath)

	if err != nil {
		panic("Error reading torrent file")
	}

	metadata := createTorrentInfo(bencode.Decode(content))

	// Generate new peer ID: https://wiki.theory.org/BitTorrentSpecification#peer_id
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

	select {}
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
	Id   string
	Ip   net.IP
	Port int
}

func (p *Peer) String() string {
	return fmt.Sprintf("[%s %s:%d]", p.Id, p.Ip.String(), p.Port)
}

func (p *Peer) ToBytes() []byte {
	result := make([]byte, 6)
	binary.BigEndian.PutUint16(result[4:], uint16(p.Port))
	copy(result, p.Ip.To4())

	return result
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
	conns     []WireProtocolConnection
}

func (tc *TorrentClient) run() {
	tc.recalculateStats()

	infoHash := tc.metadata.infoHash
	stats := tc.stats

	for _, tracker := range tc.trackers {
		peers, err := tracker.announce(TrackerStartedEvent, infoHash, stats)

		if err != nil {
			tracker.active = false
		} else {
		outer:
			for _, newPeer := range peers {
				for _, existingPeer := range tc.peers {
					if newPeer.Ip.Equal(existingPeer.Ip) && newPeer.Port == existingPeer.Port {
						continue outer
					}
				}

				tc.peers = append(tc.peers, peers...)
			}
		}
	}

	for _, peer := range tc.peers {
		conn := WireProtocolConnection{
			peer:      peer,
			active:    true,
			hash:      tc.metadata.infoHash,
			ownId:     tc.peerID,
			pieceSize: tc.metadata.raw["info"].(map[string]interface{})["piece length"].(int),
		}

		tc.conns = append(tc.conns, conn)
		go conn.start()
	}

	time.Sleep(time.Hour * 2)

	for _, tracker := range tc.trackers {
		if tracker.active {
			_, _ = tracker.announce(TrackerStoppedEvent, infoHash, stats)
		}
	}
}

type WireProtocolConnection struct {
	peer      Peer
	active    bool
	hash      []byte
	ownId     string
	pieceSize int
	conn      net.Conn
}

const (
	choke         = iota
	unchoke       = iota
	interested    = iota
	notInterested = iota
	have          = iota
	bitfield      = iota
	request       = iota
)

func (c *WireProtocolConnection) start() {
	host := net.JoinHostPort(c.peer.Ip.String(), strconv.Itoa(c.peer.Port))
	conn, err := net.Dial("tcp", host)

	if err != nil {
		c.active = false

		log.Printf("Cannot initiate connect to %s\n", host)

		// time.AfterFunc(time.Second * 30, func() {
		//	c.start()
		// })

		return
	}

	c.conn = conn

	// <pstrlen><pstr><reserved><info_hash><peer_id>

	const ptsr = "BitTorrent protocol"
	zeroes := make([]byte, 8)

	buf := bytes.NewBuffer(make([]byte, 0, 68))
	_ = binary.Write(buf, binary.BigEndian, int8(len(ptsr)))
	buf.WriteString(ptsr)
	buf.Write(zeroes)
	buf.Write(c.hash)
	buf.Write([]byte(c.ownId))

	log.Printf("Sending handshake to %s...", host)

	_, err = conn.Write(buf.Bytes())

	if err != nil {
		log.Println("Unable to connect and send handshake")
		c.conn = nil

		return
	}

	go func() {
		readBuf := make([]byte, 1, 1)
		msgBuf := bytes.NewBuffer([]byte{})
		var numWantMore uint32
		shaked := false
		handshake := make([]byte, 68, 68)

		for {
			numRead, err := conn.Read(readBuf)

			if err != nil {
				log.Printf("Disconnected from %s:%d\n", c.peer.Ip.String(), c.peer.Port)
				return
			}

			reader := bytes.NewReader(readBuf[:numRead])
			_, _ = msgBuf.ReadFrom(reader)

			if msgBuf.Len() < 4 || !shaked && msgBuf.Len() < 68 { // Fewer than "message size" bytes
				continue
			}

			if !shaked {
				_, _ = msgBuf.Read(handshake)
				shaked = true
			}

			c.peer.Id = string(handshake[48:])

			for {
				if l := msgBuf.Len(); l < 4 { // Data with at least the msg size not yet arrived
					if l > 1024 {
						msgBuf.Truncate(1024)
					}

					break
				}

				if numWantMore == 0 { // Read first 4 bytes containing msg size
					_ = binary.Read(msgBuf, binary.BigEndian, &numWantMore)
				}

				if numWantMore < uint32(msgBuf.Len()) { // Message is still incomplete, wait for new network bytes
					break
				}

				msgType, _ := msgBuf.ReadByte() // 5th bytes is the msg type
				numWantMore--                   // Type byte is included in the size
				payload := make([]byte, numWantMore)

				_, _ = msgBuf.Read(payload)
				numWantMore = 0

				log.Printf("New message %d received from %s\n", msgType, c.peer.String())
			}
		}
	}()

	keepAliveMsg := [4]byte{}

	for {
		log.Printf("Writing keep-alive to %s", c.peer.String())
		_, err := conn.Write(keepAliveMsg[:])

		if err != nil {
			log.Println("Cannot write keep-alive, disconnecting...")
			_ = conn.Close()

			return
		}

		time.Sleep(time.Second * 1)
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
	tt.interval = trackerResponse[TrackerIntervalKey].(int)

	if trackerID != nil {
		tt.trackerID = trackerID.(string)
	}

	peers = decodePeers(trackerResponse["peers"].(string))

	return peers, nil
}

func decodePeers(IPsString string) (peers []Peer) {
	reader := bytes.NewReader([]byte(IPsString))
	chunk := make([]byte, 6)
	peers = make([]Peer, 0, reader.Size()/6)

	for {
		numRead, err := reader.Read(chunk)

		if err == io.EOF || numRead < 6 {
			break
		}

		peer := Peer{}
		peer.Ip = chunk[:4]
		peer.Port = int(binary.BigEndian.Uint16(chunk[4:]))

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
	var annListValue []interface{}

	if annListUntyped := dict["announce-list"]; annListUntyped != nil {
		annListValue = annListUntyped.([]interface{})
	}
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
	encoded := bencode.Encode(info)
	infoHash := sha1.Sum(encoded)

	var files []FileInfo
	topLevelName := info["name"].(string)

	if fileEntries := info["files"]; fileEntries != nil {
		for _, fileEntryUntyped := range fileEntries.([]interface{}) {
			var builder strings.Builder
			fileEntry := fileEntryUntyped.(map[string]interface{})

			for _, pathComponent := range fileEntry["path"].([]interface{}) {
				if builder.Len() > 0 {
					builder.WriteRune(filepath.Separator)
				}

				builder.WriteString(pathComponent.(string))
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
