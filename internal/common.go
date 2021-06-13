package internal

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
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

func Min(i1 int, i2 int) int {
	if i1 < i2 {
		return i1
	} else {
		return i2
	}
}

func Max(i1 int, i2 int) int {
	if i1 > i2 {
		return i1
	} else {
		return i2
	}
}

func Pow(n int, m int) int {
	if m == 0 {
		return 1
	}

	result := n

	for i := 2; i <= m; i++ {
		result *= n
	}

	return result
}

func randomLetters(n int) string {
	var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

	buf := make([]rune, n)

	for i := range buf {
		buf[i] = letters[rand.Intn(len(letters))]
	}

	return string(buf)
}
