package internal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
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

	subscribers    []subscriberOptions
	subscribeMutex sync.Mutex

	iamUnchoked     bool
	iamInteresting  bool
	peerUnchoked    bool
	peerInteresting bool
}

type subscriberOptions struct {
	once bool
	kind byte
	ch   chan WireMessage
}

type messageConnection struct {
	net.Conn
}

func (c *messageConnection) writeMsg(msg WireMessage) (int, error) {
	return c.Write(msg.Bytes())
}

func (c *wireProtocolConnection) close() {
	c.signalSubscribers(connectionClosedMsg)

	if c.conn != nil {
		_ = c.conn.Close()
	}

	if c.readCh != nil {
		c.readCh = nil
	}

	if c.writeCh != nil {
		c.writeCh = nil
	}

	c.conn = nil
	c.active = false
}

func (c *wireProtocolConnection) message(message WireMessage) {
	if c.active {
		select {
		case c.writeCh <- message:
		}
	}
}

func (c *wireProtocolConnection) subscribe(kind byte, once bool) chan WireMessage {
	ch := make(chan WireMessage, 1)

	if kind == handshakeKind && c.active {
		ch <- handshakeMessage{peerID: c.peer.ID}

		if once {
			return ch
		}
	}

	c.subscribeMutex.Lock()

	c.subscribers = append(c.subscribers, subscriberOptions{
		once: once,
		kind: kind,
		ch:   ch,
	})

	c.subscribeMutex.Unlock()

	return ch
}

func (c *wireProtocolConnection) unsubscribe(ch chan WireMessage) {
	c.subscribeMutex.Lock()

	for i, subscriber := range c.subscribers {
		if subscriber.ch == ch {
			c.subscribers = append(c.subscribers[:i], c.subscribers[i+1:]...)
		}
	}

	close(ch)

	c.subscribeMutex.Unlock()
}

func (c *wireProtocolConnection) start() {
	c.close()

	defer func() {
		_ = c.close
	}()

	host := net.JoinHostPort(c.peer.IP.String(), strconv.Itoa(c.peer.Port))

	fmt.Printf("Trying to connect to %s\n", host)

	conn, err := net.DialTimeout("tcp", host, 30*time.Second)

	if err != nil {
		c.active = false
		log.Printf("Cannot initiate connect to %s\n", c.peer.String())

		return
	}

	fmt.Printf("Successfully connected to %s\n", host)

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
			c.close()

			return
		}

		if !bytes.Equal(handshakeMsg.infoHash, c.infoHash) {
			log.Printf("Peer returned wrong info_hash %s, should be %s, disconnecting...\n",
				handshakeMsg.infoHash.String(), c.infoHash.String())
			c.close()

			return
		}

		c.peer.ID = handshakeMsg.peerID
		c.active = true

		log.Printf("Successful handshake %s with %s\n", handshakeMsg.String(), c.peer.String())

		c.signalSubscribers(handshakeMsg)

	case <-time.After(handshakeTimeout):
		log.Printf("Unable to get handshake after %d seconds with %s, disconnecting...\n", handshakeTimeout, c.peer.String())
		return
	}

	keepAliveTicker := time.Tick(keepAliveTick)

	for {
		select {
		case msg, ok := <-c.readCh:
			if !ok {
				log.Printf("Peer %s disconnected\n", c.peer.String())
				return
			}

			log.Printf("New %s from %s\n", msg.String(), c.peer.String())

			switch msg.Kind() {
			case unchokeKind:
				c.iamUnchoked = true
			case chokeKind:
				c.iamUnchoked = false
			}

			c.signalSubscribers(msg)

		case <-keepAliveTicker:
			c.message(keepAliveMsg)
		}
	}
}

func (c *wireProtocolConnection) signalSubscribers(msg WireMessage) {
	for _, subscriber := range c.subscribers {
		if kind := subscriber.kind; kind == allMessagesKind || kind == msg.Kind() {
			select {
			case subscriber.ch <- msg:
			}
		}

		if subscriber.once {
			c.unsubscribe(subscriber.ch)
		}
	}
}

func (c *wireProtocolConnection) startReading() {
	defer func() {
		c.close()
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
			if numWantMore == 0 { // Read first bytes containing msg size
				if msgBuf.Len() < lenHeaderSizeBytes {
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

			// Check that no more than N bytes requested, we don't want to crash on 4 GB allocations
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
}
