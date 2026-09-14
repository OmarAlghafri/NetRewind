//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/OmarAlghafri/netrewind/internal/store"
)

func listen(path string, opts Options) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
	// named-pipe ACL's exact equivalent - the owner, and no one else - and
	// 0660 plus a group is the one deliberate widening Options allows.
	mode := os.FileMode(0o600)
	if opts.Group != "" {
		g, err := user.LookupGroup(opts.Group)
		if err != nil {
			l.Close()
			return nil, fmt.Errorf("ipc: group %q: %w", opts.Group, err)
		}
		gid, err := strconv.Atoi(g.Gid)
		if err != nil {
			l.Close()
			return nil, fmt.Errorf("ipc: group %q has a non-numeric gid %q", opts.Group, g.Gid)
		}
		if err := os.Chown(path, -1, gid); err != nil {
			l.Close()
			return nil, fmt.Errorf("ipc: set group of %s to %s: %w", path, opts.Group, err)
		}
		mode = 0o660
	}
	if err := os.Chmod(path, mode); err != nil {
		l.Close()
		return nil, fmt.Errorf("ipc: restrict permissions on %s: %w", path, err)
	}
	return l, nil
}

func defaultPath() string {
	return filepath.Join(filepath.Dir(store.DefaultPath()), "api.sock")
}

func dial(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
