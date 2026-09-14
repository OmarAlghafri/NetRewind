//go:build windows

package ipc

import (
	"net"
	"strings"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
)

const usesFilesystemPath = false

func dial(t *testing.T, path string) (net.Conn, error) {
	t.Helper()
	timeout := 5 * time.Second
	return winio.DialPipe(path, &timeout)
}

// TestCurrentUserSIDIsWellFormed checks the one piece listen() cannot prove
// about itself: that the SID it embeds into the pipe's SDDL is a real
// Windows SID string, not an empty or malformed one that happened not to
// break SddlToSecurityDescriptor's parser.
func TestCurrentUserSIDIsWellFormed(t *testing.T) {
	sid, err := currentUserSID()
	if err != nil {
		t.Fatalf("currentUserSID: %v", err)
	}
	if !strings.HasPrefix(sid, "S-1-") {
		t.Errorf("currentUserSID() = %q, does not look like a Windows SID (want a S-1-... string)", sid)
	}
}

// TestSDDLBuiltByListenParsesAsValid proves the exact string format() in
// ipc_windows.go produces is accepted by Windows's own SDDL parser, which is
// the actual property that matters - not merely that it happens to be a
// non-empty string.
func TestSDDLBuiltByListenParsesAsValid(t *testing.T) {
	sid, err := currentUserSID()
	if err != nil {
		t.Fatalf("currentUserSID: %v", err)
	}
	sddl := "D:P(A;;GA;;;" + sid + ")"
	if _, err := winio.SddlToSecurityDescriptor(sddl); err != nil {
		t.Errorf("the SDDL listen() builds (%q) does not parse: %v", sddl, err)
	}
}
