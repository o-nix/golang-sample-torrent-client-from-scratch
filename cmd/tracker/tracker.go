package main

import (
	"fmt"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/internal"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/pkg/bencode"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
)

type PeerInfo struct {
	details internal.Peer
	stats   internal.UpDownStats
}

func main() {
	hashes := make(map[string]map[string]PeerInfo)

	http.HandleFunc("/announce", func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		event := query.Get("event")

		hash, _ := url.QueryUnescape(query.Get("info_hash"))

		host, _, _ := net.SplitHostPort(request.RemoteAddr)
		ip := net.ParseIP(host)

		if ip.Equal(net.IPv6loopback) {
			ip = net.IPv4(127, 0, 0, 1)
		}

		port, _ := strconv.Atoi(query.Get("port"))
		key := fmt.Sprintf("%s:%d", ip, port)

		if _, present := hashes[hash]; !present {
			hashes[hash] = make(map[string]PeerInfo)
		}

		if _, present := hashes[hash][key]; !present {
			hashes[hash][key] = PeerInfo{
				details: internal.Peer{
					Ip:   ip,
					Port: port,
				},
				stats: internal.UpDownStats{},
			}
		}

		if event == internal.TrackerStoppedEvent {
			delete(hashes[hash], key)
		}

		out := make(map[string]interface{})

		ips := make([]byte, 0, len(hashes)*6)

		for _, peer := range hashes[hash] {
			ips = append(ips, peer.details.ToBytes()...)
		}

		out[internal.TrackerIntervalKey] = 30
		out["peers"] = string(ips)

		encoded := bencode.Encode(out)

		_, _ = writer.Write(encoded)
	})

	http.HandleFunc("/scrape", func(writer http.ResponseWriter, request *http.Request) {
		out := make(map[string]interface{})
		files := make(map[string]interface{})
		out["files"] = files

		query := request.URL.Query()

		hash, _ := url.QueryUnescape(query.Get("info_hash"))

		if peers, present := hashes[hash]; present {
			files[hash] = map[string]interface{}{
				"complete":   len(peers),
				"downloaded": 0,
				"incomplete": 0,
			}
		}

		encoded := bencode.Encode(out)

		_, _ = writer.Write(encoded)
	})

	log.Fatal(http.ListenAndServe(":26880", nil))
}
