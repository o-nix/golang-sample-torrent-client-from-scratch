package internal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

type wireProtocolConnection struct {
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

type messageConnection struct {
	net.Conn
}

func (c *messageConnection) WriteMsg(msg WireMessage) (int, error) {
	return c.Write(msg.Bytes())
}

func (c *wireProtocolConnection) Close() {
	_ = c.conn.Close()

	close(c.readCh)

	c.conn = nil
	c.active = false
}

func (c *wireProtocolConnection) Message(message WireMessage) {
	if c.active {
		select {
		case c.writeCh <- message:
			// Successfully read/queued
		default:
			// We avoided blocking
		}
	}
}

func (c *wireProtocolConnection) Start() {
	host := net.JoinHostPort(c.peer.IP.String(), strconv.Itoa(c.peer.Port))
	conn, err := net.Dial("tcp", host)

	if err != nil {
		c.active = false
		log.Printf("Cannot initiate connect to %s\n", c.peer.String())

		return
	}

	c.conn = &messageConnection{conn}

	c.readCh = make(chan WireMessage, rwChanSize)
	c.writeCh = make(chan WireMessage, rwChanSize)

	go c.startReading()
	go c.startWriting()

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
		c.active = true

		log.Printf("Successful handshake %s with %s\n", handshakeMsg.String(), c.peer.String())

	case <-time.After(time.Second * 10):
		log.Printf("Unable to get handshake after 10 seconds with %s\n", c.peer.String())
		return
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
}

func (c *wireProtocolConnection) startReading() {
	defer func() {
		c.Close()
	}()

	var numWantMore uint32

	shaked := false

	readBuf := make([]byte, readBufSize, readBufSize)
	msgBuf := bytes.NewBuffer(nil)
	handshake := make([]byte, handshakeSizeBytes, handshakeSizeBytes)
	maxPossiblePayloadSize := c.pieceSize + 5

	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 50))
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

			handshakeMsg := parseHandshakeBytes(handshake)
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

func (c *wireProtocolConnection) startWriting() {
	func(c *wireProtocolConnection) {
		defer func() {
			c.Close()
		}()

		for msg := range c.writeCh {
			log.Printf("Sending %s to %s\n", msg.String(), c.peer.String())
			_, err := c.conn.WriteMsg(msg)

			if err != nil {
				log.Printf("Unable to send message to %s, disconnecting...\n", c.peer.String())

				return
			}
		}
	}(c)
}
