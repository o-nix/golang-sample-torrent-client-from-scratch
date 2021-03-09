package main

import (
	"encoding/binary"
	"net"
	"testing"
)

func Test_decodeIPs(t *testing.T) {
	ip := net.IPv4(172, 1, 2, 3)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 16378)
	testBytes := make([]byte, 0, 6)
	testBytes = append(testBytes, []byte(ip.To4())...)
	testBytes = append(testBytes, portBytes...)

	res := decodePeers(string(testBytes))

	if r := res[0]; len(res) != 1 || r.ip.String() != "172.1.2.3" || r.port != 16378 {
		t.Fail()
	}
}
