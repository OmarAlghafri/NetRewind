package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// The interface binds to loopback and has no authentication, which means its
// threat model is "somebody who can already reach this machine, plus every
// string the watched network chose". The escaping tests cover the second. These
// cover the first: what happens when the requests stop being reasonable.
//
// None of these may produce a 5xx, a hang, or a panic. A page that fails is an
// inconvenience; a reader that takes the recorder down with it is a way to stop
// the recording from a browser tab.

func TestNoQueryStringCanProduceAServerError(t *testing.T) {
	srv, st := newTestServer(t)
	seedEvent(t, st, event.KindLinkDown, event.SevWarn, "eth0", time.Now(), nil)

	hostile := []string{
		"", "?", "?q", "?q=", "?window=", "?window=&q=",
		"?q=" + strings.Repeat("A", 100000),
		"?window=" + strings.Repeat("9", 5000) + "h",
		"?window=-9223372036854775808ns",
		"?window=9223372036854775807ns",
		"?window=0.0000000001s",
		"?window=NaNh", "?window=%00", "?window=1h&window=2h&window=3h",
		"?q=%FF%FE%00", "?q=%C0%80", "?q=" + url.QueryEscape("' OR 1=1 --"),
		"?q=" + url.QueryEscape("%' UNION SELECT sqlite_version() --"),
		"?q=" + url.QueryEscape("../../../../etc/passwd"),
		"?q=" + url.QueryEscape("\x00\x01\x02"),
		"?" + strings.Repeat("a=1&", 5000),
	}

	for _, page := range []string{"/", "/incidents", "/timeline", "/host"} {
		for _, q := range hostile {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", page+q, nil)
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code >= 500 {
				t.Errorf("GET %s%.60s -> %d", page, q, rec.Code)
			}
		}
	}
}

// The store is evidence. Nothing reachable from a browser may change it, and
// nothing may reach it by a method the routes do not define.
func TestNothingReachableFromABrowserCanWriteToTheStore(t *testing.T) {
	srv, st := newTestServer(t)
	seedEvent(t, st, event.KindLinkDown, event.SevWarn, "eth0", time.Now(), nil)

	before, err := st.CountEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT"} {
		for _, path := range []string{"/", "/incidents", "/timeline", "/host", "/healthz", "/static/style.css"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(method, path, strings.NewReader("q=1&drop=events"))
			srv.Handler().ServeHTTP(rec, req)

			if rec.Code == http.StatusOK && method != "HEAD" {
				t.Errorf("%s %s was served with 200; only GET is defined", method, path)
			}
		}
	}

	after, err := st.CountEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("the store went from %d events to %d; the interface is not read-only", before, after)
	}
}

// A hundred readers at once is not a lot, and it is exactly what happens when
// somebody leaves the timeline open on a refresh and then an incident starts.
// The store is opened with a single connection, so this is worth pinning: a
// deadlock here stops the page rather than the recorder, but it stops it during
// the incident.
func TestConcurrentReadersAreAllServed(t *testing.T) {
	srv, st := newTestServer(t)
	now := time.Now()
	for i := 0; i < 200; i++ {
		seedEvent(t, st, event.KindARPBindingChanged, event.SevWarn,
			fmt.Sprintf("10.0.0.%d", i%256), now.Add(-time.Duration(i)*time.Second), nil)
	}

	const readers = 100
	var wg sync.WaitGroup
	codes := make([]int, readers)
	start := time.Now()
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			page := []string{"/", "/timeline?window=24h", "/incidents", "/host?q=10.0.0.1"}[i%4]
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", page, nil))
			codes[i] = rec.Code
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("100 concurrent readers did not all finish within a minute")
	}
	t.Logf("%d concurrent readers served in %s", readers, time.Since(start).Round(time.Millisecond))

	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("reader %d got %d", i, c)
		}
	}
}

// A window is bounded so a query string cannot turn into a scan of the whole
// store, and the bound has to hold however the number is written.
func TestTheWindowIsAlwaysBounded(t *testing.T) {
	for _, raw := range []string{
		"100000h", "876000h", "1000000000s", "9999999999999ns", "-1h", "0", "",
		"abc", "1h30m", "2562047h47m16.854775807s",
	} {
		got := parseWindow(raw)
		if got <= 0 {
			t.Errorf("parseWindow(%q) = %s, which would ask for an empty or reversed range", raw, got)
		}
		if got > maxWindow {
			t.Errorf("parseWindow(%q) = %s, past the %s limit", raw, got, maxWindow)
		}
	}
}

// A store that fails mid-request must produce a page saying so, not a panic and
// not a blank 200 that reads as "the network was quiet".
func TestAStoreThatCannotBeReadIsReportedOnThePage(t *testing.T) {
	srv, st := newTestServer(t)
	seedEvent(t, st, event.KindLinkDown, event.SevWarn, "eth0", time.Now(), nil)

	// Closing it underneath the server is the cheapest faithful stand-in for
	// the disk going away.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/", "/incidents", "/timeline", "/host?q=eth0"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		if rec.Code >= 500 {
			t.Errorf("%s returned %d rather than a page explaining the failure", path, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "could not read the store") {
			t.Errorf("%s does not say the store could not be read; "+
				"an empty page reads as a quiet network", path)
		}
	}
}
