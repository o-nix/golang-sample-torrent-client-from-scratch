package main

import (
	"fmt"
	"github.com/o-nix/golang-sample-torrent-client-from-scratch/internal"
	"math/rand"
	"os"
	"time"
)

func init() {
	// Proper random() in the production code
	rand.Seed(time.Now().UnixNano())
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage:\n%s path/to/file.torrent\n", os.Args[0])
	} else {
		internal.Start(os.Args[1])
	}
}
