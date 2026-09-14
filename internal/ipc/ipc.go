// Package ipc provides the local-only transport the versioned API listens
// on: a Unix domain socket restricted to the owning user on Linux, a named
// pipe restricted to the owning user's SID on Windows.
//
// PRODUCT_RELEASE_PLAN_AR.md §4.1: "أنشئ API محلياً versioned (v1) فوق IPC
// محلي فقط: named pipe مع ACL على Windows وUnix-domain socket مع permissions
// على Linux... لا يُفتح HTTP على LAN في الإصدار الأول."
//
// Nothing in this package is ever reachable from the network, on either
// platform: a Unix socket and a Windows named pipe are both local-machine
// primitives with no network stack underneath them at all, which is a
// stronger guarantee than "not listening on 0.0.0.0" - there is no port to
// scan and no firewall rule that would matter either way.
//
// Access control defaults to exactly the user who started the listener,
// matching a single desktop machine where the same person runs the agent
// and the UI. When the agent runs as a service account (root under systemd,
// LocalSystem as a Windows service) and a different interactive user needs
// the desktop to reach it, Options widens that deliberately and visibly:
// a group on Linux, extra SIDs on Windows. Nothing widens by accident.
package ipc

import "net"

// Options controls who, beyond the listening user, may connect.
type Options struct {
	// Group (Linux) makes the socket group-owned by this group name and
	// readable/writable by it (mode 0660 instead of 0600). Empty keeps the
	// owner-only default.
	Group string
	// AllowSIDs (Windows) grants each listed SID (S-1-5-21-...) access to
	// the pipe in addition to the listening user. Empty keeps the
	// owner-only default.
	AllowSIDs []string
}

// Listen opens the local IPC endpoint at path, restricted to the current
// user, and returns a net.Listener ready for http.Serve or any other
// net.Listener consumer. path is a filesystem path on Linux (see
// DefaultPath) and a named pipe path (\\.\pipe\...) on Windows.
func Listen(path string) (net.Listener, error) {
	return listen(path, Options{})
}

// ListenWith is Listen with explicit access options.
func ListenWith(path string, opts Options) (net.Listener, error) {
	return listen(path, opts)
}

// DefaultPath returns the platform-appropriate default endpoint location,
// alongside wherever this machine's event store already lives.
func DefaultPath() string {
	return defaultPath()
}
