package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// /v1/rules and /v1/capabilities rarely change between polls, unlike
// /v1/events - a client re-fetching them on a timer should get a real
// bandwidth saving when nothing moved, not merely a hint. Standard HTTP
// conditional GET (ETag / If-None-Match / 304), not a same-body hash
// field the client would still have to download every time to read.
func TestRulesSupportsConditionalGet(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	first := get(t, h, "/v1/rules")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response carries no ETag")
	}
	if first.Body.Len() == 0 {
		t.Fatal("first response has an empty body")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/rules", nil)
	req.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 when If-None-Match matches the current ETag", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 response has a %d-byte body, want none - the whole point is not re-sending it", rec.Body.Len())
	}

	stale := httptest.NewRecorder()
	staleReq := httptest.NewRequest("GET", "/v1/rules", nil)
	staleReq.Header.Set("If-None-Match", `"not-the-real-etag"`)
	h.ServeHTTP(stale, staleReq)
	if stale.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a stale/wrong If-None-Match", stale.Code)
	}
	if stale.Body.Len() == 0 {
		t.Error("a stale If-None-Match must still get the real body")
	}
}

func TestCapabilitiesSupportsConditionalGet(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	first := get(t, h, "/v1/capabilities")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response carries no ETag")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/capabilities", nil)
	req.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec.Code)
	}
}
