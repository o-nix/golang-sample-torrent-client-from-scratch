package internal

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
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
	// peerID := "-DF0001-" + uniuri.NewLen(12)
	peerID := "-DE13F0-8ioIzjScYpP7"

	localConnInfo := LocalConnectionInfo{
		port:   25276, // 16000 + rand.Intn(32000),
		peerID: peerID,
	}

	client := TorrentClient{
		metadata: metadata,
		peerID:   peerID,
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
	pieceLen     int
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
			infoHash:  tc.metadata.infoHash,
			ownId:     tc.peerID,
			pieceSize: tc.metadata.pieceLen,

			iamChoked:       true,
			iamInteresting:  false,
			peerChoked:      true,
			peerInteresting: false,
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
	infoHash  []byte
	ownId     string
	pieceSize int
	conn      net.Conn

	iamChoked       bool // Default: true
	iamInteresting  bool // Default: false
	peerChoked      bool // Default: true
	peerInteresting bool // Default: false
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

const protocolVersionConstant = "BitTorrent protocol"

var keepAliveMsg = [4]byte{}

var unchokeMsg = createMsg(unchoke, 1)
var interestedMsg = createMsg(interested, 1)

func createMsg(msg byte, len uint32) []byte {
	var buf = bytes.NewBuffer(nil)

	_ = binary.Write(buf, binary.BigEndian, len)
	_ = binary.Write(buf, binary.BigEndian, msg)

	return buf.Bytes()
}

func (c *WireProtocolConnection) start() {
	peer := c.peer
	host := net.JoinHostPort(peer.Ip.String(), strconv.Itoa(peer.Port))
	conn, err := net.Dial("tcp", host)

	if err != nil {
		c.active = false
		log.Println("Cannot initiate connect to", peer)

		return
	}

	c.conn = conn

	log.Println("Sending handshake to", peer)
	_, err = conn.Write(c.generateHandshakeMsg())

	if err != nil {
		log.Println("Unable to connect and send handshake to", peer)
		c.conn = nil

		return
	}

	go func() {
		readBuf := make([]byte, 1, 1)
		msgBuf := bytes.NewBuffer(nil)
		var numWantMore uint32
		shaked := false
		handshake := make([]byte, 68, 68)

		for {
			numRead, err := conn.Read(readBuf)

			if err != nil {
				log.Println("Disconnected from", peer)
				return
			}

			reader := bytes.NewReader(readBuf[:numRead])
			_, _ = msgBuf.ReadFrom(reader)

			if !shaked && msgBuf.Len() < 68 { // Fewer than "message size" bytes
				continue
			}

			if !shaked {
				_, _ = msgBuf.Read(handshake)
				shaked = true

				peerInfoHash := handshake[28:48]

				if !bytes.Equal(peerInfoHash, c.infoHash) {
					log.Printf("Peer returned info_hash %s we are not interested in, disconnecting...\n", hex.EncodeToString(peerInfoHash))

					_ = conn.Close()

					return
				}

				peer.Id = string(handshake[48:])

				log.Println("Got handshake from", peer)
			}

			for {
				if numWantMore == 0 { // Read first 4 bytes containing msg size
					if msgBuf.Len() < 4 {
						break
					}

					_ = binary.Read(msgBuf, binary.BigEndian, &numWantMore)
				}

				if uint32(msgBuf.Len()) < numWantMore { // Message is still incomplete, wait for new network bytes
					if msgBuf.Cap() > 32*1024 {
						msgBuf.Truncate(1024) // Do we need this?
					}

					break
				}

				msgType, _ := msgBuf.ReadByte() // 5th bytes is the msg type
				numWantMore--                   // Type byte is included in the size
				payload := make([]byte, numWantMore)

				_, _ = msgBuf.Read(payload)
				numWantMore = 0

				log.Printf("New message %d received from %s\n", msgType, peer.String())
			}
		}
	}()

	time.Sleep(time.Second * 2)

	log.Printf("Sending initial keep-alive to %s\n", peer.String())
	_, err = conn.Write(keepAliveMsg[:])

	// time.Sleep(time.Second * 2)

	// log.Println("Sending initial INTERESTED to", peer)
	// _, err = conn.Write(interestedMsg[:])

	// request := createMsg(request, 13)
	// zero := [4]byte{0}
	// tt := [4]byte{0, 0, 0, 255}
	// request = append(request, zero[:]...)
	// request = append(request, zero[:]...)
	// request = append(request, tt[:]...)
	//
	// _, err = conn.Write(request)

	for {
		time.Sleep(time.Second * 60)

		log.Printf("Writing keep-alive to %s\n", peer.String())
		_, err := conn.Write(keepAliveMsg[:])

		if err != nil {
			log.Printf("Cannot write keep-alive to %s, disconnecting...\n", peer.String())
			_ = conn.Close()

			return
		}
	}
}

func (c *WireProtocolConnection) generateHandshakeMsg() []byte {
	// <pstrlen><pstr><reserved><info_hash><peer_id>

	var zeroes = [8]byte{}

	buf := bytes.NewBuffer(make([]byte, 0, 68))
	_ = binary.Write(buf, binary.BigEndian, int8(len(protocolVersionConstant)))
	buf.WriteString(protocolVersionConstant)
	buf.Write(zeroes[:])
	buf.Write(c.infoHash)
	buf.Write([]byte(c.ownId))

	return buf.Bytes()
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

	pieceLen := info["piece length"].(int)

	return TorrentMetadata{
		announceUrls: announces,
		infoHash:     infoHash[:],
		folder:       topLevelName,
		files:        files,
		raw:          dict,
		pieceLen:     pieceLen,
	}
}
