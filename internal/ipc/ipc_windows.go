//go:build windows

package ipc

import (
	"fmt"
	"net"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listen(path string) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, fmt.Errorf("ipc: determine current user's SID: %w", err)
	}
	// A protected DACL with exactly one ACE, granting full control to this
	// user's SID and no one else - the named-pipe equivalent of a Unix
	// socket at mode 0600. "P" (protected) stops the pipe from inheriting a
	// broader ACE from its container, which would otherwise silently widen
	// access past what this line says.
	sddl := fmt.Sprintf("D:P(A;;GA;;;%s)", sid)
	l, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on pipe %s: %w", path, err)
	}
	return l, nil
}

func currentUserSID() (string, error) {
	tok := windows.GetCurrentProcessToken()
	user, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("get token user: %w", err)
	}
	return user.User.Sid.String(), nil
}

// defaultPath is a fixed pipe name rather than derived from the store
// location (as ipc_unix.go's socket path is): a named pipe lives in its own
// kernel namespace (\\.\pipe\...), not on any filesystem path, so there is
// no directory to colocate it with in the first place.
func defaultPath() string {
	return `\\.\pipe\netrewind-api`
}
