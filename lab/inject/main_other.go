//go:build !linux

// The frame injector needs a raw packet socket, which exists only on Linux.
// This stub keeps `go build ./...` and `go vet ./...` working on a developer's
// machine rather than failing on a directory with no buildable files.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "nrinject: the lab frame injector requires Linux")
	os.Exit(1)
}
