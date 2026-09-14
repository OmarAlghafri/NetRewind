package ipc

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Dial connects to the local API endpoint at path (a socket path on Linux,
// a pipe name on Windows). The listener's access control decides whether
// the connection is accepted; a caller that is not allowed sees a
// permission error, not a hang.
func Dial(ctx context.Context, path string) (net.Conn, error) {
	return dial(ctx, path)
}

// HTTPClient returns an http.Client whose every request goes to the API at
// path, whatever host the request URL names. Use "http://netrewind/v1/..."
// as the URL; the host is a placeholder the transport ignores.
func HTTPClient(path string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dial(ctx, path)
			},
			// One local process talking to one local daemon: keep a
			// connection open between polls rather than reconnecting.
			MaxIdleConns:    2,
			IdleConnTimeout: 30 * time.Second,
		},
	}
}
