# Sample torrent client written in Go

## Overview

This is a console app for downloading torrent-hosted content via ***.torrent** files.

### Good parts

* [x] Very tiny and quick
* [x] No external libraries at all
* [x] Torrent-tracker implementation for local testing
* [x] `Bencode` package is ready for reuse

### Missing parts

* [ ] Big beautiful progress bar instead of the messages log!
* [ ] Seeding / Bitfield / `HAVE`
* [ ] Game / randomization mode, it downloads pieces in order
* [ ] No memory cache flushing
* [ ] DHT

## Usage

```
./golang-sample-torrent-client-from-scratch path/to/file.torrent
```

```
Sending started event to tracker http://localhost:26880/announce

Sending handshake {Handshake message id:-DF0001-ylipvsb5vE0g, h:23759d44a5d3c789555554ec916cc3269a23dffc} to {Peer h:127.0.0.1, p:50862}
Sending {Handshake message id:-DF0001-ylipvsb5vE0g, h:23759d44a5d3c789555554ec916cc3269a23dffc} to {Peer h:127.0.0.1, p:50862}
Successful handshake {Handshake message id:-DE13F0-DEDk-U0mNz7f, h:23759d44a5d3c789555554ec916cc3269a23dffc} with {Peer id:-DE13F0-DEDk-U0mNz7f, h:127.0.0.1, p:50862}

New {Wire message t:5, s:3} from {Peer id:-DE13F0-DEDk-U0mNz7f, h:127.0.0.1, p:50862}
Sending {Wire message t:2, s:0} to {Peer id:-DE13F0-DEDk-U0mNz7f, h:127.0.0.1, p:50862}
New {Wire message t:1, s:0} from {Peer id:-DE13F0-DEDk-U0mNz7f, h:127.0.0.1, p:50862}

I am UNCHOKED by {Peer id:-DE13F0-DEDk-U0mNz7f, h:127.0.0.1, p:50862}

Sending {Wire message t:6, s:12} to {Peer id:-DE13F0-DEDk-U0mNz7f, h:127.0.0.1, p:50862}
New {Wire message t:7, s:16392} from {Peer id:-DE13F0-DEDk-U0mNz7f, h:127.0.0.1, p:50862}
Sending {Wire message t:6, s:12} to {Peer id:-DE13F0-DEDk-U0mNz7f, h:127.0.0.1, p:50862}
New {Wire message t:7, s:16392} from {Peer id:-DE13F0-DEDk-U0mNz7f, h:127.0.0.1, p:50862}

Download complete! Writing files...
Sending stopped event to tracker http://localhost:26880/announce

Exiting.

```
