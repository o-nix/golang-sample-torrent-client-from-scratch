package internal

const handshakeSizeBytes = 68

const (
	keepAliveKind = 0xFF
)

const (
	chokeKind         = iota
	unchokeKind       = iota
	interestedKind    = iota
	notInterestedKind = iota
	haveKind          = iota
	bitfieldKind      = iota
	requestKind       = iota
)

const (
	TrackerStartedEvent = "started"
	TrackerStoppedEvent = "stopped"
	TrackerIntervalKey  = "interval"
)

const protocolVersionConstant = "BitTorrent protocol"
const rwChanSize = 256
const readBufSize = 1024
