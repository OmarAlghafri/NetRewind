package v1

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/ipc"
)

// TestServedOverRealLocalIPC proves the whole stack together: a real
// internal/ipc listener (a Unix socket on Linux, a named pipe on Windows -
// whichever this test runs on), an http.Server bound to it, and a real HTTP
// client dialing that exact transport rather than TCP. httptest.NewRecorder,
// used everywhere else in this package's tests, checks the handler's logic
// but never actually exercises internal/ipc at all; this is the test that
// would fail if the two packages did not actually fit together.
func TestServedOverRealLocalIPC(t *testing.T) {
	path := integrationTestPath(t)
	l, err := ipc.Listen(path)
	if err != nil {
		t.Fatalf("ipc.Listen: %v", err)
	}
	defer l.Close()

	srv, st := newTestServer(t)
	b := event.NewBuilder("obs-1", nil)
	if err := st.Append(context.Background(), b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth0", 2))); err != nil {
		t.Fatalf("seed: %v", err)
	}

	httpSrv := &http.Server{Handler: srv.Handler()}
	go httpSrv.Serve(l)
	defer httpSrv.Close()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialForTest(path)
			},
		},
		Timeout: 5 * time.Second,
	}

	// The base URL's host is meaningless for a Unix-socket/named-pipe
	// transport - only the DialContext override above decides where the
	// connection actually goes - but net/http requires a well-formed one.
	resp, err := client.Get("http://local-ipc/v1/events")
	if err != nil {
		t.Fatalf("GET over real IPC: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body eventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Events) != 1 || body.Events[0].Kind != event.KindLinkDown {
		t.Fatalf("events = %+v, want the one seeded event, read back over the real transport", body.Events)
	}
}

func integrationTestPath(t *testing.T) string {
	t.Helper()
	if usesFilesystemIPCPath {
		return filepath.Join(t.TempDir(), "api.sock")
	}
	return `\\.\pipe\netrewind-api-v1-integration-` + t.Name()
}
