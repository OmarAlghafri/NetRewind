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
// Access control is deliberately restricted to exactly the user who started
// the listener, matching a single desktop machine where the same person
// runs the agent and the UI. A deployment where the agent runs as a separate
// service account and a different interactive user needs to reach it (see
// deploy/systemd/netrewindd.service's AmbientCapabilities, which already
// implies the recorder can run unprivileged-but-capable rather than as the
// desktop user) needs a real group-ownership and installer decision that
// belongs in deploy/, not a default this package should guess at.
package ipc

import "net"

// Listen opens the local IPC endpoint at path, restricted to the current
// user, and returns a net.Listener ready for http.Serve or any other
// net.Listener consumer. path is a filesystem path on Linux (see
// DefaultPath) and a named pipe path (\\.\pipe\...) on Windows.
func Listen(path string) (net.Listener, error) {
	return listen(path)
}

// DefaultPath returns the platform-appropriate default endpoint location,
// alongside wherever this machine's event store already lives.
func DefaultPath() string {
	return defaultPath()
}
