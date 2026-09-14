//go:build windows

package ipc

import (
	"context"
	"strings"
	"testing"

	winio "github.com/Microsoft/go-winio"
)

const usesFilesystemPath = false

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

// TestSecurityDescriptorWithAllowedSIDs proves the widened DACL is still a
// valid, protected descriptor with exactly one ACE per allowed SID, and
// that a malformed SID is refused rather than embedded.
func TestSecurityDescriptorWithAllowedSIDs(t *testing.T) {
	owner, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sddl, err := securityDescriptor(owner, []string{"S-1-5-32-544", " S-1-5-18 ", ""})
	if err != nil {
		t.Fatalf("securityDescriptor: %v", err)
	}
	if want := "D:P(A;;GA;;;" + owner + ")(A;;GA;;;S-1-5-32-544)(A;;GA;;;S-1-5-18)"; sddl != want {
		t.Errorf("sddl = %q, want %q", sddl, want)
	}
	if _, err := winio.SddlToSecurityDescriptor(sddl); err != nil {
		t.Errorf("widened SDDL does not parse: %v", err)
	}
	if _, err := securityDescriptor(owner, []string{"not-a-sid"}); err == nil {
		t.Errorf("a malformed allowed SID must be refused, got a descriptor")
	}
}

// TestListenWithAllowedSIDIsStillConnectable exercises the real pipe with a
// widened ACL: the listening user must still be able to dial it.
func TestListenWithAllowedSIDIsStillConnectable(t *testing.T) {
	path := `\\.\pipe\netrewind-ipc-test-` + strings.ReplaceAll(t.Name(), "/", "-")
	l, err := ListenWith(path, Options{AllowSIDs: []string{"S-1-5-32-544"}})
	if err != nil {
		t.Fatalf("ListenWith: %v", err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			c.Close()
		}
	}()
	c, err := Dial(context.Background(), path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c.Close()
}
