package ipc

import (
	"io"
	"path/filepath"
	"testing"
	"time"
)

// TestListenAcceptsAConnection is the basic positive case, run for real on
// whichever platform the test suite executes on: a Unix domain socket here,
// a named pipe under Windows's own ipc_windows.go.
func TestListenAcceptsAConnection(t *testing.T) {
	path := testPath(t)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			t.Errorf("Accept: %v", err)
			return
		}
		defer conn.Close()
		conn.Write([]byte("hello"))
	}()

	conn, err := dial(t, path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	buf := make([]byte, 5)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read from server: %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("got %q, want %q", buf, "hello")
	}
	<-done
}

// TestListenReplacesAStaleEndpoint covers the crash-recovery path: a prior
// run's endpoint (socket file or, conceptually, a leftover pipe instance)
// must not permanently wedge the next start.
func TestListenReplacesAStaleEndpoint(t *testing.T) {
	path := testPath(t)
	l1, err := Listen(path)
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	l1.Close()

	l2, err := Listen(path)
	if err != nil {
		t.Fatalf("second Listen after the first was closed: %v", err)
	}
	defer l2.Close()
}

func TestDefaultPathIsNonEmpty(t *testing.T) {
	if DefaultPath() == "" {
		t.Error("DefaultPath() is empty")
	}
}

func testPath(t *testing.T) string {
	t.Helper()
	if usesFilesystemPath {
		return filepath.Join(t.TempDir(), "api.sock")
	}
	// Named pipes live in their own namespace; a per-test unique name avoids
	// collisions between parallel test runs without needing a filesystem at
	// all.
	return `\\.\pipe\netrewind-api-test-` + t.Name()
}
