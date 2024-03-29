package internal

import (
	"bytes"
	"net"
	"testing"
)

func TestPeer_ToBytes(t *testing.T) {
	peer := Peer{
		IP:   net.IPv4(121, 12, 23, 17),
		Port: 6354,
	}

	if !bytes.Equal(peer.ToBytes(), []byte{121, 12, 23, 17, 24, 210}) {
		t.Fail()
	}
}
