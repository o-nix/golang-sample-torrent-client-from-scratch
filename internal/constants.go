package internal

import "time"

const testLocalPort = 25276

const handshakeSizeBytes = 68

const lenHeaderSizeBytes = 4

const (
	allMessagesKind      = 0xFF
	keepAliveKind        = allMessagesKind - iota
	handshakeKind        = allMessagesKind - iota
	connectionClosedKind = allMessagesKind - iota

	keepAliveTick = time.Second * 60
)

const handshakeTimeout = time.Second * 10

const (
	chokeKind         = iota
	unchokeKind       = iota
	interestedKind    = iota
	notInterestedKind = iota
	haveKind          = iota
	bitfieldKind      = iota
	requestKind       = iota
	pieceKind         = iota
)

const (
	TrackerStartedEvent = "started"
	TrackerStoppedEvent = "stopped"
	TrackerIntervalKey  = "interval"
)

const protocolVersionConstant = "BitTorrent protocol"
const rwChanSize = 256
const readBufSize = 1024

const hashLen = 20

var pieceDownloadSize = Pow(2, 14)
