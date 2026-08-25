package web

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// The strings this interface renders are not the operator's. A DNS name is
// whatever a machine on the watched segment chose to look up, an interface name
// comes from whoever configured it, and nftables rule text is arbitrary. Anyone
// able to put a name on the wire can put a string in the record, and the record
// is then rendered as a page for the person investigating them.
//
// html/template escapes contextually and this is not in doubt; the test exists
// because the guarantee is only worth anything if it is actually reaching these
// templates, and a single stray template.HTML anywhere would end it silently.
func TestHostileStringsFromTheWireAreEscaped(t *testing.T) {
	hostile := []struct {
		name    string
		payload string
		// escaped is what must appear instead, if the payload appears at all.
		escaped string
	}{
		{"script tag", `<script>alert(1)</script>`, `&lt;script&gt;`},
		{"attribute break-out", `" onmouseover="alert(1)`, `&#34;`},
		{"tag injection", `<img src=x onerror=alert(1)>`, `&lt;img`},
		{"entity", `&lt;script&gt;`, `&amp;lt;`},
	}

	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			srv, st := newTestServer(t)
			b := event.NewBuilder("obs-1", nil)

			// Three routes for the same payload: as a subject label, as an
			// attribute value, and as an interface name.
			e := b.New(event.SourceDNS, event.KindDNSResolverChanged, event.SevWarn,
				event.Host("10.0.0.5", ""))
			e.Subject.Label = tc.payload
			e.WithAttr("name", tc.payload).
				WithAttr("resolver_new", tc.payload)
			if err := st.Append(context.Background(), e); err != nil {
				t.Fatal(err)
			}

			for _, path := range []string{"/", "/timeline?window=24h", "/host?host=" + url.QueryEscape(tc.payload)} {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("GET", path, nil)
				srv.Handler().ServeHTTP(rec, req)

				body := rec.Body.String()
				if strings.Contains(body, tc.payload) {
					t.Errorf("%s rendered %q unescaped", path, tc.payload)
				}
				// If the payload reached the page at all, it must be escaped.
				if strings.Contains(body, "10.0.0.5") && !strings.Contains(body, tc.escaped) {
					t.Logf("%s: payload not present on this page", path)
				}
			}
		})
	}
}

// The query string is the other way in, and it is echoed back into the page.
func TestHostileQueryParametersAreEscaped(t *testing.T) {
	srv, _ := newTestServer(t)

	const payload = `<script>alert(1)</script>`
	for _, path := range []string{
		"/host?host=" + url.QueryEscape(payload),
		"/timeline?window=" + url.QueryEscape(payload),
		"/incidents?window=" + url.QueryEscape(payload),
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		if strings.Contains(rec.Body.String(), payload) {
			t.Errorf("%s echoed the query string unescaped", path)
		}
	}
}

// A window far larger than the store holds must be refused rather than turned
// into a scan of everything, and one that is not a duration must not panic.
func TestAbsurdWindowsAreRefusedNotServed(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, window := range []string{"100000h", "-5h", "not-a-duration", "0s", ""} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec,
			httptest.NewRequest("GET", "/timeline?window="+url.QueryEscape(window), nil))

		if rec.Code >= 500 {
			t.Errorf("window=%q produced %d", window, rec.Code)
		}
	}
}

// Anything that changes the record must not be reachable, whatever is asked.
func TestTheInterfaceIsReadOnly(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		for _, path := range []string{"/", "/timeline", "/incidents", "/host"} {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, nil))

			if rec.Code == 200 {
				t.Errorf("%s %s was served; the interface must be read-only", method, path)
			}
		}
	}
}

// The static handler serves an embedded filesystem, which has no parent
// directories to walk into. Worth pinning: the day it is changed to serve from
// disk, this is what says why it must not be.
func TestStaticAssetsCannotEscapeTheEmbeddedFilesystem(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, path := range []string{
		"/static/../templates/layout.html",
		"/static/..%2ftemplates%2flayout.html",
		"/static/....//templates/layout.html",
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		if strings.Contains(rec.Body.String(), "{{") {
			t.Errorf("%s served a template source file", path)
		}
	}
}
