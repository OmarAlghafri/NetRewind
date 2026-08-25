// Package listen decides whether a listening address is reachable from the
// network, and says so once when it is.
//
// The recorder has two HTTP surfaces - the web interface and the metrics
// endpoint - and neither has any authentication. Both show what the watched
// network has been doing. Whether an address exposes them is one rule, and it
// lived in two places until the two disagreed: one of them treated ":8464" as
// loopback, so binding to every interface produced no warning at all. Exactly
// the case that most needed one.
package listen

import (
	"log/slog"
	"net"
)

// IsLocal reports whether an address reaches only this machine.
//
// An empty host is not local. ":8464" binds every interface, and it is the form
// people type when they mean "the default port" without thinking about which
// addresses that includes.
func IsLocal(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Unparseable addresses are handled by the listener, which will refuse
		// them with a better message than this package could give. Treating an
		// address we do not understand as safe is the wrong default.
		return false
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// WarnIfExposed says once, loudly, that an unauthenticated surface is reachable
// from the network. Nobody should discover this from a port scan.
func WarnIfExposed(log *slog.Logger, addr, what string) {
	if log == nil || IsLocal(addr) {
		return
	}
	log.Warn("bound beyond loopback with no authentication",
		"surface", what, "addr", addr,
		"consequence", "anyone who can reach this address can read what the recorder has seen")
}
