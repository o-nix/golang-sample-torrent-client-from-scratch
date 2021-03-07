package main

import (
	"encoding/binary"
	"net"
	"testing"
)

func Test_decodeIPs(t *testing.T) {
	ip := net.IPv4(172, 1, 2, 0)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 16378)
	var testBytes []byte
	testBytes = append(testBytes, []byte(ip.To4())...)
	testBytes = append(testBytes, portBytes...)

	res := decodeIPs(string(testBytes))

	if len(res) != 1 && res[0] != "172.1.2.0:16378" {
		t.Fail()
	}
}
