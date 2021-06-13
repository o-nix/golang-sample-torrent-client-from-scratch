package internal

import (
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/pkg/bencode"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var httpClient http.Client

func init() {
	httpClient = http.Client{
		// Default timeout will prevent HTTP requests from waiting the response forever
		Timeout: time.Second * 30,
	}
}

func Start(filePath string) {
	content, err := os.ReadFile(filePath)

	if err != nil {
		panic("Error reading torrent file")
	}

	metadata := createTorrentInfo(bencode.Decode(content).(map[string]interface{}))
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

	appSignals := make(chan bool)

	go client.run(localConnInfo, appSignals)

	systemSignals := make(chan os.Signal)
	signal.Notify(systemSignals, syscall.SIGINT, syscall.SIGTERM)

	stop := func() {
		client.Stop()
		os.Exit(0)
	}

	select {
	case <-appSignals:
		stop()
	case <-systemSignals:
		stop()
	}
}

func getPort() int {
	return testLocalPort // 16000 + rand.Intn(32000)
}

// https://wiki.theory.org/BitTorrentSpecification#peer_id
func generatePeerID() string {
	return "-DF0001-" + randomLetters(12)
}
