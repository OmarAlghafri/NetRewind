//go:build !windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/OmarAlghafri/netrewind/internal/store"
)

func listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("ipc: create socket directory: %w", err)
	}
	// A stale socket file left by an unclean shutdown must not block a fresh
	// bind: net.Listen("unix", ...) fails with "address already in use"
	// against a path nothing is actually listening on any more, which would
	// otherwise need a human to notice and delete it by hand.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("ipc: remove stale socket %s: %w", path, err)
	}

	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on %s: %w", path, err)
	}
	// The actual enforcement, not a convenience default: a Unix socket
	// inherits the umask at creation, which on some systems is permissive
	// enough to let any local user read the record through it. 0600 is the
	// named-pipe ACL's exact equivalent - the owner, and no one else.
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, fmt.Errorf("ipc: restrict permissions on %s: %w", path, err)
	}
	return l, nil
}

func defaultPath() string {
	return filepath.Join(filepath.Dir(store.DefaultPath()), "api.sock")
}
