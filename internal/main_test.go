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

func Test_decodeIPs(t *testing.T) {
	peer := Peer{
		IP:   net.IPv4(172, 1, 2, 3),
		Port: 16378,
	}

	res := decodePeers(string(peer.ToBytes()))

	if r := res[0]; len(res) != 1 || r.IP.String() != "172.1.2.3" || r.Port != 16378 {
		t.Fail()
	}
}
