package internal

import (
	"net"
	"testing"
)

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
