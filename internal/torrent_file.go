package internal

import (
	"bytes"
	"crypto/sha1"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/pkg/bencode"
	"path/filepath"
	"strings"
)

type TorrentMetadata struct {
	announceUrls []string
	infoHash     SHA1Hash
	files        []FileInfo
	folder       string
	pieceLen     int
	hashes       []SHA1Hash
}

func createTorrentInfo(dict map[string]interface{}) *TorrentMetadata {
	var annListValue []interface{}

	if annListUntyped := dict["announce-list"]; annListUntyped != nil {
		annListValue = annListUntyped.([]interface{})
	}
	announces := []string{dict["announce"].(string)}

	for _, listWithElemsValue := range annListValue {
	uniqueCheck:
		for _, elemValue := range listWithElemsValue.([]interface{}) {
			for _, alreadyAddedAnn := range announces {
				if elemValue == alreadyAddedAnn {
					break uniqueCheck
				}
			}

			announces = append(announces, elemValue.(string))
		}
	}

	info := dict["info"].(map[string]interface{})
	encoded := bencode.Encode(info)
	infoHash := sha1.Sum(encoded)

	var files []FileInfo
	topLevelName := info["name"].(string)

	if fileEntries := info["files"]; fileEntries != nil {
		for _, fileEntryUntyped := range fileEntries.([]interface{}) {
			var builder strings.Builder
			fileEntry := fileEntryUntyped.(map[string]interface{})

			for _, pathComponent := range fileEntry["path"].([]interface{}) {
				if builder.Len() > 0 {
					builder.WriteRune(filepath.Separator)
				}

				builder.WriteString(pathComponent.(string))
			}

			files = append(files, FileInfo{
				size: fileEntry["length"].(int),
				path: builder.String(),
			})
		}
	} else {
		files = []FileInfo{
			{
				path: topLevelName,
				size: info["length"].(int),
			},
		}

		topLevelName = ""
	}

	pieceLen := info["piece length"].(int)
	hashes := readPieceHashes(info)

	return &TorrentMetadata{
		announceUrls: announces,
		infoHash:     infoHash[:],
		folder:       topLevelName,
		files:        files,
		pieceLen:     pieceLen,
		hashes:       hashes,
	}
}

func readPieceHashes(info map[string]interface{}) []SHA1Hash {
	r := bytes.NewReader([]byte(info["pieces"].(string)))
	pieceHashes := make([]SHA1Hash, 0, r.Len()/20)

	for {
		var hash [hashLen]byte
		read, err := r.Read(hash[:])

		if err != nil || read < hashLen {
			break
		}

		pieceHashes = append(pieceHashes, hash[:])
	}
	return pieceHashes
}
