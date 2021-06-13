package internal

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

var keepAliveMsg = wireMessage{kind: keepAliveKind}
var unchokeMsg = wireMessage{kind: unchokeKind}
var chokeMsg = wireMessage{kind: chokeKind}
var interestedMsg = wireMessage{kind: interestedKind}
var notInterestedMsg = wireMessage{kind: notInterestedKind}
var connectionClosedMsg = wireMessage{kind: connectionClosedKind}

type WireMessage interface {
	Bytes() []byte
	String() string
	Kind() byte
	// TODO: add separate Payload() instead of Bytes()
}

type wireMessage struct {
	kind    byte
	payload []byte
	cached  []byte
}

func (w wireMessage) Bytes() []byte {
	if w.kind == pieceKind {
		return w.payload
	}

	if w.cached == nil {
		var size = uint32(len(w.payload))

		if w.kind != keepAliveKind {
			size++
		}

		buf := bytes.NewBuffer(make([]byte, 0))
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
	if w.kind == keepAliveKind {
		return "{Wire message keep-alive}"
	} else {
		return fmt.Sprintf("{Wire message t:%d, s:%d}", w.kind, len(w.payload))
	}
}

func (w wireMessage) Kind() byte {
	return w.kind
}

type handshakeMessage struct {
	peerID   string
	infoHash SHA1Hash
}

// <pstrlen><pstr><reserved><info_hash><peer_id>
func (h handshakeMessage) Bytes() []byte {
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

func (h handshakeMessage) Kind() byte {
	return handshakeKind
}

func parseHandshakeBytes(handshake []byte) handshakeMessage {
	return handshakeMessage{
		peerID:   string(handshake[48:]),
		infoHash: handshake[28:48],
	}
}
