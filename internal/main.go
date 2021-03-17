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
	return fmt.Sprintf("[%s %s:%d]", p.ID, p.IP.String(), p.Port)
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
	infoHash  []byte
	ownID     string
	pieceSize int
	conn      *messageConnection

	iamChoked       bool // Default: true
	iamInteresting  bool // Default: false
	peerChoked      bool // Default: true
	peerInteresting bool // Default: false
}

const (
	keepAliveKind = 0xFF
	handshakeKind = keepAliveKind - iota
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
	return fmt.Sprintf("[Message type %d]", w.kind)
}

type handshakeMessage struct {
	peerID   string
	infoHash []byte
}

// <pstrlen><pstr><reserved><info_hash><peer_id>
func (h handshakeMessage) Bytes() []byte {
	const protocolVersionConstant = "BitTorrent protocol"
	var zeroes = [8]byte{}

	buf := bytes.NewBuffer(make([]byte, 0, 68))
	_ = binary.Write(buf, binary.BigEndian, int8(len(protocolVersionConstant)))
	buf.WriteString(protocolVersionConstant)
	buf.Write(zeroes[:])
	buf.Write(h.infoHash)
	buf.Write([]byte(h.peerID))

	return buf.Bytes()
}

func (h handshakeMessage) String() string {
	return fmt.Sprintf("[Message type handshake]")
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

func (c *WireProtocolConnection) start() {
	peer := c.peer
	host := net.JoinHostPort(peer.IP.String(), strconv.Itoa(peer.Port))
	conn, err := net.Dial("tcp", host)

	if err != nil {
		c.active = false
		log.Println("Cannot initiate connect to", peer)

		return
	}

	c.conn = &messageConnection{conn}

	readCh := make(chan WireMessage)
	writeCh := make(chan WireMessage)

	go func() {
		const readBufSize = 1024
		readBuf := make([]byte, readBufSize, readBufSize)
		msgBuf := bytes.NewBuffer(nil)
		var numWantMore uint32
		shaked := false
		handshake := make([]byte, 68, 68)

		for {
			_ = conn.SetReadDeadline(time.Now().Add(time.Millisecond * 10))
			numRead, err := conn.Read(readBuf)

			reader := bytes.NewReader(readBuf[:numRead])
			_, _ = msgBuf.ReadFrom(reader)

			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			} else if err != nil {
				log.Println("Disconnected from", peer)
				return
			}

			if !shaked {
				if msgBuf.Len() < 68 { // Fewer than handshake
					continue
				}

				_, _ = msgBuf.Read(handshake)
				shaked = true

				handshakeMsg := parseHandshake(handshake)
				peer.ID = handshakeMsg.peerID

				if !bytes.Equal(handshakeMsg.infoHash, c.infoHash) {
					log.Printf("Peer returned wrong info_hash %s, disconnecting...\n", hex.EncodeToString(handshakeMsg.infoHash))

					_ = conn.Close()

					return
				}

				log.Println("Got handshake from", peer)

				readCh <- handshakeMsg
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

				kind, _ := msgBuf.ReadByte() // 5th bytes is the msg type
				numWantMore--                // Type byte is included in the size
				payload := make([]byte, numWantMore)

				_, _ = msgBuf.Read(payload)
				numWantMore = 0

				msg := wireMessage{
					kind:    kind,
					payload: payload,
				}

				readCh <- msg

				log.Printf("New message %d received from %s\n", kind, peer.String())
			}
		}
	}()

	go func() {
		for msg := range writeCh {
			log.Printf("Sending message %s to %s\n", msg.String(), peer.String())
			_, err = c.conn.writeMsg(msg)

			if err != nil {
				log.Printf("Unable to send message to %s, disconnecting...\n", peer.String())

				_ = c.conn.Close()
				c.conn = nil

				return
			}
		}
	}()

	log.Println("Sending handshake to", peer)

	startHandshake := handshakeMessage{
		peerID:   c.ownID,
		infoHash: c.infoHash,
	}

	writeCh <- startHandshake

	select {
	case handshakeMsg := <-readCh:
		log.Println("Successful handshake", handshakeMsg)
	case <-time.After(time.Second * 10):
		panic("Unable to get handshake")
	}

	writeCh <- keepAliveMsg

	keepAliveTicker := time.Tick(time.Second * 10)

	for {
		select {
		case msg, ok := <-readCh:
			if !ok {
				panic("Disconnected")
			}

			log.Println(msg)
		case <-keepAliveTicker:
			writeCh <- keepAliveMsg
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
