package internal

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type TorrentClient struct {
	metadata    *TorrentMetadata
	trackers    []TrackerTransport
	peerID      string
	trackerId   string
	peers       []Peer
	stats       UpDownStats
	connections []*wireProtocolConnection
}

func (c *TorrentClient) run() {
	c.recalculateStats()

	for _, tracker := range c.trackers {
		peers, err := tracker.announce(TrackerStartedEvent, c.metadata.infoHash, c.stats)

		if err != nil {
			tracker.active = false
		} else {
		outer:
			for _, newPeer := range peers {
				for _, existingPeer := range c.peers {
					if newPeer.IP.Equal(existingPeer.IP) && newPeer.Port == existingPeer.Port {
						continue outer
					}
				}

				c.peers = append(c.peers, peers...)
			}
		}
	}

	for _, peer := range c.peers {
		if peer.Port != testLocalPort {
			conn := &wireProtocolConnection{
				peer:      peer,
				infoHash:  c.metadata.infoHash,
				ownID:     c.peerID,
				pieceSize: c.metadata.pieceLen,
			}

			c.connections = append(c.connections, conn)
			go conn.Start()
		}
	}

	time.Sleep(time.Second * 2)

	pm := newPiecesManager(c.metadata)

	for _, conn := range c.connections {
		pm.onNewConnection(conn)
	}

	pm.DownloadAll()
}

func (m *piecesManager) onNewConnection(conn *wireProtocolConnection) {
	go func() {
		select {
		case <-conn.Subscribe(handshakeKind, true):
		default:
			return // Closed?
		}

		unchokeCh := conn.Subscribe(unchokeKind, true)
		conn.Message(interestedMsg)

		select {
		case <-unchokeCh:
			log.Printf("I am UNCHOKED by %s\n", conn.peer.String())
			m.onUnchoke(conn)

		case <-time.After(time.Second * 10):
			conn.Message(notInterestedMsg)
			return // Just ignore this peer?
		}
	}()
}

type connectionDownloadDetails struct {
	conn           *wireProtocolConnection
	lastActiveTime time.Time
	downloading    bool
	piece          *piece
}

type piecesManager struct {
	metadata        *TorrentMetadata
	done            chan bool
	overallSize     int
	numPieces       int
	pieces          []*piece
	downloadDetails []*connectionDownloadDetails
	eventsMutex     sync.Mutex
}

type piece struct {
	start int
	end   int
	index int
	buf   *bytes.Buffer
	hash  SHA1Hash
}

func (p *piece) calculateRealHash() SHA1Hash {
	hash := sha1.New()
	hash.Write(p.buf.Bytes())
	return hash.Sum(nil)
}

func (p *piece) isFullyDownloaded() bool {
	return p.buf.Len() >= p.end-p.start
}

func newPiecesManager(m *TorrentMetadata) *piecesManager {
	overallSize := 0

	for _, file := range m.files {
		overallSize += file.size
	}

	var pieces []*piece

	for i, j := 0, 0; i < overallSize; {
		currentPieceLen := m.pieceLen

		if i+currentPieceLen > overallSize {
			currentPieceLen = overallSize - i
		}

		pieces = append(pieces, &piece{
			start: i,
			end:   i + currentPieceLen,
			index: j,
			buf:   new(bytes.Buffer),
			hash:  m.hashes[len(pieces)],
		})

		i += currentPieceLen
		j++
	}

	pm := piecesManager{
		metadata:    m,
		done:        make(chan bool),
		overallSize: overallSize,
		pieces:      pieces,
	}

	return &pm
}

func (m *piecesManager) onUnchoke(c *wireProtocolConnection) {
	m.eventsMutex.Lock()
	m.downloadDetails = append(m.downloadDetails, &connectionDownloadDetails{
		conn:           c,
		lastActiveTime: time.Now(),
	})
	m.eventsMutex.Unlock()

	// TODO: timeout and Choke back after 10s when not used
}

func (m *piecesManager) DownloadAll() {
	tick := time.Tick(100 * time.Millisecond)

	for {
		select {
		case <-tick:
			allDownloaded := true

			for _, piece := range m.pieces {
				if !piece.isFullyDownloaded() {
					allDownloaded = false
					break
				}
			}

			if allDownloaded {
				fmt.Println("Download complete! Writing files...")

				// TODO: flush during downloading!

				folderName := m.metadata.folder
				_ = os.RemoveAll(folderName)      // TODO: insecure, should check relativeness
				os.Mkdir(folderName, os.ModePerm) // TODO: check error

				start := 0
				for _, fileInfo := range m.metadata.files {
					file, _ := os.Create(folderName + string(os.PathSeparator) + fileInfo.path)

					end := start + fileInfo.size

					for _, piece := range m.pieces {
						if piece.start >= start && piece.start <= end || piece.end <= end && piece.end >= start {
							upper := Max(piece.start, start) - piece.start
							lower := Min(piece.end, end) - piece.start
							buf := piece.buf.Bytes()[upper:lower]
							file.Write(buf) // TODO: check error
						}
					}

					start += fileInfo.size
				}

				m.done <- true
			}

			for i, downloadDetails := range m.downloadDetails {
				conn := downloadDetails.conn

				if conn.iamUnchoked {
					if downloadDetails.downloading {
						if time.Now().After(downloadDetails.lastActiveTime.Add(10 * time.Second)) {
							conn.Message(notInterestedMsg)
							conn.Message(chokeMsg)

							// TODO: disconnect the peer?

							m.eventsMutex.Lock()
							m.downloadDetails = append(m.downloadDetails[:i], m.downloadDetails[i:]...)
							m.eventsMutex.Unlock()

							break
						}
					} else {
						for _, piece := range m.pieces {
							if !piece.isFullyDownloaded() {
								downloadDetails.piece = piece
								downloadDetails.downloading = true

								go func(details *connectionDownloadDetails) {
									// This worker will receive all PIECE events for this connection
									connection := details.conn
									pieceCh := connection.Subscribe(pieceKind, false)

									for {
										buf := new(bytes.Buffer)
										pieceBuffer := piece.buf
										downloaded := pieceBuffer.Len()
										pieceLen := piece.end - piece.start

										if downloaded >= pieceLen {
											details.downloading = false
											connection.Unsubscribe(pieceCh)

											break
										}

										// <index><begin><length>
										_ = binary.Write(buf, binary.BigEndian, int32(piece.index))
										_ = binary.Write(buf, binary.BigEndian, int32(downloaded))
										_ = binary.Write(buf, binary.BigEndian, int32(Min(pieceDownloadSize, pieceLen-downloaded)))

										connection.Message(wireMessage{kind: requestKind, payload: buf.Bytes()})

									outerLoop:
										for {
											select {
											case msg := <-pieceCh:
												details.lastActiveTime = time.Now()

												// <index><begin><block>
												var index, begin int32
												payloadBuf := bytes.NewBuffer(msg.Bytes())
												_ = binary.Read(payloadBuf, binary.BigEndian, &index)
												_ = binary.Read(payloadBuf, binary.BigEndian, &begin)

												if int(index) != piece.index { // We don't expect this piece in this worker
													continue
												}

												if int(begin) > pieceBuffer.Len()+1 { // When peer sends data in not requested range
													continue // Not a real case?
												}

												// Make existing buffer end right where new starts
												piece.buf = bytes.NewBuffer(pieceBuffer.Bytes()[0:begin])
												piece.buf.Write(payloadBuf.Bytes())

												break outerLoop
											}
										}
									}
								}(downloadDetails)

								break
							} else {
								realHash := piece.calculateRealHash()

								if !bytes.Equal(realHash, piece.hash) {
									fmt.Printf("Incorrect piece data hash %s, should be %s, resetting...\n", realHash, piece.hash)
									piece.buf.Reset()
								}
							}
						}
					}
				}
			}
		case <-m.done:
			return
		}
	}
}

func (c TorrentClient) Stop() {
	for _, tracker := range c.trackers {
		if tracker.active {
			_, _ = tracker.announce(TrackerStoppedEvent, c.metadata.infoHash, c.stats)
		}
	}
}

func (c *TorrentClient) recalculateStats() {
	left := 0

	for _, file := range c.metadata.files {
		left += file.size
	}

	c.stats = UpDownStats{
		downloaded: 0,
		uploaded:   0,
		left:       left,
	}
}
