package internal

import (
	"github.com/dchest/uniuri"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/pkg/bencode"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"time"
)

var httpClient http.Client

func init() {
	httpClient = http.Client{
		Timeout: time.Second * 30,
	}

	rand.Seed(time.Now().UnixNano())
}

func Start(filePath string) {
	content, err := os.ReadFile(filePath)

	if err != nil {
		panic("Error reading torrent file")
	}

	metadata := createTorrentInfo(bencode.Decode(content))
	peerID := generatePeerID()

	localConnInfo := LocalConnectionInfo{
		port:   getPort(),
		peerID: peerID,
	}

	client := TorrentClient{
		metadata: metadata,
		peerID:   peerID,
	}

	for _, announceUrl := range metadata.announceUrls {
		parsedUrl, err := url.Parse(announceUrl)

		if err != nil {
			log.Printf("Malformed tracker URL: %s", announceUrl)
			continue
		}

		transport := TrackerTransport{
			active:        true,
			trackerURL:    *parsedUrl,
			localConnInfo: localConnInfo,
		}

		client.trackers = append(client.trackers, transport)
	}

	go client.run()

	select {}
}

func getPort() int {
	return 25276 // 16000 + rand.Intn(32000)
}

// https://wiki.theory.org/BitTorrentSpecification#peer_id
func generatePeerID() string {
	return "-DF0001-" + uniuri.NewLen(12)
}
