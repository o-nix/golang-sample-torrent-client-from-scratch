package main

import (
	"fmt"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/internal"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/pkg/bencode"
	"log"
	"net"
	"net/http"
	"strconv"
)

func main() {
	type PeerInfo struct {
		details internal.Peer
		stats   internal.UpDownStats
	}

	peers := make(map[string]PeerInfo)

	http.HandleFunc("/announce", func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		event := query.Get("event")

		host, _, _ := net.SplitHostPort(request.RemoteAddr)
		ip := net.ParseIP(host)

		if ip.Equal(net.IPv6loopback) {
			ip = net.IPv4(127, 0, 0, 1)
		}

		port, _ := strconv.Atoi(query.Get("port"))
		key := fmt.Sprintf("%s:%d", ip, port)

		if _, present := peers[key]; !present {
			peers[key] = PeerInfo{
				details: internal.Peer{
					Ip:   ip,
					Port: port,
				},
				stats: internal.UpDownStats{},
			}
		}

		switch event {
		case internal.TrackerStoppedEvent:
			delete(peers, key)
		}

		out := make(map[string]interface{})
		out[internal.TrackerIntervalKey] = 30

		ips := make([]byte, 0, len(peers)*6)

		for _, peer := range peers {
			ips = append(ips, peer.details.ToBytes()...)
		}

		out["peers"] = string(ips)

		encoded := bencode.Encode(out)

		_, _ = writer.Write(encoded)
	})

	http.HandleFunc("/scrape", func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		print(query)

		// TODO: return proper seeders/leechers stats

		out := make(map[string]interface{})
		encoded := bencode.Encode(out)

		_, _ = writer.Write(encoded)
	})

	log.Fatal(http.ListenAndServe(":26880", nil))
}
