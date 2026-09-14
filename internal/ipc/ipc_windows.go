package ipc

import (
	"context"
	"fmt"
	"net"
	"strings"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listen(path string, opts Options) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, fmt.Errorf("ipc: determine current user's SID: %w", err)
	}
	sddl, err := securityDescriptor(sid, opts.AllowSIDs)
	if err != nil {
		return nil, err
	}
	l, err := winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on pipe %s: %w", path, err)
	}
	return l, nil
}

// securityDescriptor builds a protected DACL granting full control to the
// listening user's SID and to each explicitly allowed SID, and to no one
// else - the named-pipe equivalent of a Unix socket at mode 0600 (or 0660
// with a group). "P" (protected) stops the pipe from inheriting a broader
// ACE from its container, which would otherwise silently widen access past
// what these ACEs say. Every allowed SID is parsed before use so a typo in
// a config file fails the listener rather than producing a pipe nobody can
// open, or one that a mangled SDDL string opens too widely.
func securityDescriptor(owner string, allow []string) (string, error) {
	var b strings.Builder
	b.WriteString("D:P(A;;GA;;;")
	b.WriteString(owner)
	b.WriteString(")")
	for _, s := range allow {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, err := windows.StringToSid(s); err != nil {
			return "", fmt.Errorf("ipc: allowed SID %q is not a valid SID: %w", s, err)
		}
		b.WriteString("(A;;GA;;;")
		b.WriteString(s)
		b.WriteString(")")
	}
	return b.String(), nil
}

func currentUserSID() (string, error) {
	tok := windows.GetCurrentProcessToken()
	user, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("get token user: %w", err)
	}
	return user.User.Sid.String(), nil
}

func defaultPath() string {
	return `\\.\pipe\netrewind-api`
}

func dial(ctx context.Context, path string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, path)
}
