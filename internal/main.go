package internal

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
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
			trackerURL:    *parsedUrl,
			localConnInfo: localConnInfo,
		}

		client.trackers = append(client.trackers, transport)
	}

	go client.run()

	select {}
}

type TrackerTransport struct {
	active        bool
	trackerURL    url.URL
	localConnInfo LocalConnectionInfo
	peerID        string
	trackerID     string
	interval      int
}

type Peer struct {
	ID   string
	IP   net.IP
	Port int
}

func (p *Peer) String() string {
	var fmtString string

	if p.ID != "" {
		fmtString = "{Peer id:%s, h:%s, p:%d}"
	} else {
		fmtString = "{Peer h:%s, p:%d}"
	}

	return fmt.Sprintf(fmtString, p.ID, p.IP.String(), p.Port)
}

func (p *Peer) ToBytes() []byte {
	result := make([]byte, 6)
	binary.BigEndian.PutUint16(result[4:], uint16(p.Port))
	copy(result, p.IP.To4())

	return result
}

type UpDownStats struct {
	downloaded int
	uploaded   int
	left       int
}

type TorrentMetadata struct {
	announceUrls []string
	infoHash     SHA1Hash
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
					if newPeer.IP.Equal(existingPeer.IP) && newPeer.Port == existingPeer.Port {
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
			ownID:     tc.peerID,
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
	infoHash  SHA1Hash
	ownID     string
	pieceSize int
	conn      *messageConnection

	readCh  chan WireMessage
	writeCh chan WireMessage

	iamChoked       bool // Default: true
	iamInteresting  bool // Default: false
	peerChoked      bool // Default: true
	peerInteresting bool // Default: false
}

const handshakeSizeBytes = 68

const (
	keepAliveKind = 0xFF
)

const (
	chokeKind         = iota
	unchokeKind       = iota
	interestedKind    = iota
	notInterestedKind = iota
	haveKind          = iota
	bitfieldKind      = iota
	requestKind       = iota
)

type WireMessage interface {
	Bytes() []byte
	String() string
}

type wireMessage struct {
	kind    byte
	payload []byte
	cached  []byte
}

func (w wireMessage) Bytes() []byte {
	if w.cached == nil {
		size := len(w.payload)

		if w.kind != keepAliveKind {
			size++
		}

		buf := bytes.NewBuffer(make([]byte, 0, size))
		_ = binary.Write(buf, binary.BigEndian, size)

		if w.kind != keepAliveKind {
			_ = binary.Write(buf, binary.BigEndian, w.kind)
		}

		buf.Write(w.payload)
		w.cached = buf.Bytes()
	}

	return w.cached
}

func (w wireMessage) String() string {
	return fmt.Sprintf("{Wire message t:%d, s:%d}", w.kind, len(w.payload))
}

type SHA1Hash []byte

func (s SHA1Hash) String() string {
	return hex.EncodeToString(s)
}

type handshakeMessage struct {
	peerID   string
	infoHash SHA1Hash
}

// <pstrlen><pstr><reserved><info_hash><peer_id>
func (h handshakeMessage) Bytes() []byte {
	const protocolVersionConstant = "BitTorrent protocol"
	var zeroes = [8]byte{}

	buf := bytes.NewBuffer(make([]byte, 0, handshakeSizeBytes))
	_ = binary.Write(buf, binary.BigEndian, int8(len(protocolVersionConstant)))
	buf.WriteString(protocolVersionConstant)
	buf.Write(zeroes[:])
	buf.Write(h.infoHash)
	buf.Write([]byte(h.peerID))

	return buf.Bytes()
}

func (h handshakeMessage) String() string {
	return fmt.Sprintf("{Handshake message id:%s, h:%s}", h.peerID, h.infoHash.String())
}

func parseHandshake(handshake []byte) handshakeMessage {
	return handshakeMessage{
		peerID:   string(handshake[48:]),
		infoHash: handshake[28:48],
	}
}

var keepAliveMsg = wireMessage{kind: keepAliveKind}
var unchokeMsg = wireMessage{kind: unchokeKind}
var interestedMsg = wireMessage{kind: interestedKind}

type messageConnection struct {
	net.Conn
}

func (c *messageConnection) writeMsg(msg WireMessage) (int, error) {
	return c.Write(msg.Bytes())
}

func (c *WireProtocolConnection) close() {
	_ = c.conn.Close()

	close(c.readCh)
	close(c.writeCh)

	c.conn = nil
	c.active = false
}

func (c *WireProtocolConnection) start() {
	host := net.JoinHostPort(c.peer.IP.String(), strconv.Itoa(c.peer.Port))
	conn, err := net.Dial("tcp", host)

	if err != nil {
		c.active = false
		log.Printf("Cannot initiate connect to %s\n", c.peer.String())

		return
	}

	c.conn = &messageConnection{conn}

	c.readCh = make(chan WireMessage)
	c.writeCh = make(chan WireMessage)

	go startReading(c)
	go startWriting(c)

	startHandshake := handshakeMessage{
		peerID:   c.ownID,
		infoHash: c.infoHash,
	}

	log.Printf("Sending handshake %s to %s", startHandshake.String(), c.peer.String())

	c.writeCh <- startHandshake

	select {
	case msg := <-c.readCh:
		handshakeMsg, ok := msg.(handshakeMessage)

		if !ok {
			log.Printf("Peer %s started messaging not with a handshake, disconnecting...\n", c.peer.String())
			return
		}

		if !bytes.Equal(handshakeMsg.infoHash, c.infoHash) {
			log.Printf("Peer returned wrong info_hash %s, should be %s, disconnecting...\n",
				handshakeMsg.infoHash.String(), c.infoHash.String())

			return
		}

		c.peer.ID = handshakeMsg.peerID

		log.Printf("Successful handshake %s with %s\n", handshakeMsg.String(), c.peer.String())
	case <-time.After(time.Second * 10):
		panic("Unable to get handshake")
	}

	keepAliveTicker := time.Tick(time.Second * 60)

	for {
		select {
		case msg, ok := <-c.readCh:
			if !ok {
				log.Printf("Peer %s disconnected\n", c.peer.String())
				return
			}

			log.Printf("New %s from %s\n", msg.String(), c.peer.String())

		case <-keepAliveTicker:
			c.writeCh <- keepAliveMsg
		}
	}

	// request := createMsg(request, 13)
	// zero := [4]byte{0}
	// tt := [4]byte{0, 0, 0, 255}
	// request = append(request, zero[:]...)
	// request = append(request, zero[:]...)
	// request = append(request, tt[:]...)
	//
	// _, err = conn.Write(request)
}

func startReading(c *WireProtocolConnection) {
	defer func() {
		c.close()
	}()

	const readBufSize = 1024
	var numWantMore uint32

	shaked := false

	readBuf := make([]byte, readBufSize, readBufSize)
	msgBuf := bytes.NewBuffer(nil)
	handshake := make([]byte, handshakeSizeBytes, handshakeSizeBytes)
	maxPossiblePayloadSize := c.pieceSize + 5

	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 10))
		numRead, err := c.conn.Read(readBuf)

		reader := bytes.NewReader(readBuf[:numRead])
		_, _ = msgBuf.ReadFrom(reader)

		if errors.Is(err, os.ErrDeadlineExceeded) {
			continue
		} else if err != nil { // TCP connection closed
			return
		}

		if !shaked {
			if msgBuf.Len() < handshakeSizeBytes { // Fewer than handshake
				continue
			}

			_, _ = msgBuf.Read(handshake)
			shaked = true

			handshakeMsg := parseHandshake(handshake)
			c.readCh <- handshakeMsg
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

			var kind byte = keepAliveKind

			if numWantMore > 0 { // Not just keep-alive message
				kind, _ = msgBuf.ReadByte() // 5th bytes is the msg type
				numWantMore--               // Type byte is included in the size
			}

			// Check that no more than N bytes requested, we don't want to crash on 4GB allocations
			if numWantMore > uint32(maxPossiblePayloadSize) {
				log.Fatalf("Payload size requested %d is too big (max %d)\n", numWantMore, maxPossiblePayloadSize)

				return
			}

			payload := make([]byte, int(numWantMore))

			_, _ = msgBuf.Read(payload)
			numWantMore = 0

			msg := wireMessage{
				kind:    kind,
				payload: payload,
			}

			c.readCh <- msg
		}
	}
}

func startWriting(c *WireProtocolConnection) {
	func(c *WireProtocolConnection) {
		defer func() {
			c.close()
		}()

		for msg := range c.writeCh {
			log.Printf("Sending %s to %s\n", msg.String(), c.peer.String())
			_, err := c.conn.writeMsg(msg)

			if err != nil {
				log.Printf("Unable to send message to %s, disconnecting...\n", c.peer.String())

				return
			}
		}
	}(c)
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
