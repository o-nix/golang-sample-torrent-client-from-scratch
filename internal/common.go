package internal

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
)

type SHA1Hash []byte

func (s SHA1Hash) String() string {
	return hex.EncodeToString(s)
}

type LocalConnectionInfo struct {
	port   int
	ip     net.IP
	peerID string
}

type UpDownStats struct {
	downloaded int
	uploaded   int
	left       int
}

type FileInfo struct {
	path string
	size int
}

type Peer struct {
	ID   string
	IP   net.IP
	Port int
}

func (p *Peer) String() string {
	if p.ID != "" {
		return fmt.Sprintf("{Peer id:%s, h:%s, p:%d}", p.ID, p.IP.String(), p.Port)
	} else {
		return fmt.Sprintf("{Peer h:%s, p:%d}", p.IP.String(), p.Port)
	}
}

func (p *Peer) ToBytes() []byte {
	result := make([]byte, 6)
	binary.BigEndian.PutUint16(result[4:], uint16(p.Port))
	copy(result, p.IP.To4())

	return result
}
