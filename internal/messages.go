package internal

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

var keepAliveMsg = wireMessage{kind: keepAliveKind}
var unchokeMsg = wireMessage{kind: unchokeKind}
var interestedMsg = wireMessage{kind: interestedKind}

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

func parseHandshakeBytes(handshake []byte) handshakeMessage {
	return handshakeMessage{
		peerID:   string(handshake[48:]),
		infoHash: handshake[28:48],
	}
}
