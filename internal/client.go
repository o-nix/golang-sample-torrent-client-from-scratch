package internal

import "time"

type TorrentClient struct {
	metadata  TorrentMetadata
	trackers  []TrackerTransport
	peerID    string
	trackerId string
	peers     []Peer
	stats     UpDownStats
	conns     []wireProtocolConnection
}

func (tc *TorrentClient) run() {
	tc.recalculateStats()

	infoHash := tc.metadata.infoHash
	stats := tc.stats

	for _, tracker := range tc.trackers {
		peers, err := tracker.announce(TrackerStartedEvent, infoHash, stats)

		if err != nil {
			tracker.active = false
		} else {
		outer:
			for _, newPeer := range peers {
				for _, existingPeer := range tc.peers {
					if newPeer.IP.Equal(existingPeer.IP) && newPeer.Port == existingPeer.Port {
						continue outer
					}
				}

				tc.peers = append(tc.peers, peers...)
			}
		}
	}

	for _, peer := range tc.peers {
		conn := wireProtocolConnection{
			peer:      peer,
			active:    true,
			infoHash:  tc.metadata.infoHash,
			ownID:     tc.peerID,
			pieceSize: tc.metadata.pieceLen,

			iamChoked:       true,
			iamInteresting:  false,
			peerChoked:      true,
			peerInteresting: false,
		}

		tc.conns = append(tc.conns, conn)
		go conn.Start()
	}

	time.Sleep(time.Hour * 2)

	for _, tracker := range tc.trackers {
		if tracker.active {
			_, _ = tracker.announce(TrackerStoppedEvent, infoHash, stats)
		}
	}
}

func (tc *TorrentClient) recalculateStats() {
	left := 0

	for _, file := range tc.metadata.files {
		left += file.size
	}

	tc.stats = UpDownStats{
		downloaded: 0,
		uploaded:   0,
		left:       left,
	}
}
