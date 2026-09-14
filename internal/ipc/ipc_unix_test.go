//go:build !windows

package ipc

import (
	"net"
	"os"
	"testing"
)

const usesFilesystemPath = true

func dial(t *testing.T, path string) (net.Conn, error) {
	t.Helper()
	return net.Dial("unix", path)
}

// TestSocketIsRestrictedToTheOwner is the actual security property this
// package exists to provide, checked directly rather than assumed from the
// code reading correctly: a socket any local user could read would defeat
// the whole point of a local-only IPC channel.
func TestSocketIsRestrictedToTheOwner(t *testing.T) {
	path := testPath(t)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("socket permissions = %o, want 0600 (owner read/write, nothing for group or other)", got)
	}
}
