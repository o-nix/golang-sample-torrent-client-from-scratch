package internal

import (
	"crypto/sha1"
	"reflect"
	"testing"
)

func Test_wireMessage_Bytes(t *testing.T) {
	actual := interestedMsg.Bytes()
	expected := [...]byte{0, 0, 0, 1, interestedKind}

	if !reflect.DeepEqual(actual, expected[:]) {
		t.Fail()
	}

	hash := sha1.Sum([]byte("test"))

	handshake := handshakeMessage{
		peerID:   generatePeerID(),
		infoHash: SHA1Hash(hash[:]),
	}

	parsed := parseHandshakeBytes(handshake.Bytes())

	if !reflect.DeepEqual(handshake, parsed) {
		t.Fail()
	}
}
