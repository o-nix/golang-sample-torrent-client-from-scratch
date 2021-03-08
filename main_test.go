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

	res := decodeIPs(string(testBytes))

	if len(res) != 1 && res[0] != "172.1.2.3:16378" {
		t.Fail()
	}
}
