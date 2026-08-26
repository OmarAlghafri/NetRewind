package otel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

const (
	// logsPath is where OTLP/HTTP receivers listen for logs.
	logsPath = "/v1/logs"
	// exportTimeout bounds one attempt. A collector that has stopped answering
	// must not be able to slow the recorder down: recording is the job, and
	// exporting is a courtesy to somebody else's dashboard.
	exportTimeout = 10 * time.Second
	// maxAttempts is how many times one batch is tried. Receivers ask for
	// retries on 429 and 503, and a collector restarting is the ordinary case.
	maxAttempts = 3
)

// Exporter ships the record to an OTLP receiver.
//
// It is deliberately fire-and-forget from the writer's point of view. The store
// is the record; this is a copy going somewhere else, and a copy that cannot be
// delivered must never be allowed to stop the original being written.
type Exporter struct {
	endpoint   string
	headers    map[string]string
	observerID string
	version    string
	client     *http.Client
	log        *slog.Logger

	// dropped counts records the collector never received. Exported as a
	// metric and, unlike the store's own losses, not recoverable - so it is
	// counted and reported rather than quietly forgotten.
	dropped atomic.Uint64
	failing atomic.Bool
}

// New returns an exporter for an OTLP/HTTP endpoint.
//
// The endpoint is the receiver's base address, e.g. http://localhost:4318; the
// signal path is appended. Passing a URL that already ends in /v1/logs is
// accepted too, because that is what half the documentation shows.
func New(endpoint, observerID, version string, headers map[string]string, log *slog.Logger) *Exporter {
	url := strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(url, logsPath) {
		url += logsPath
	}
	return &Exporter{
		endpoint:   url,
		headers:    headers,
		observerID: observerID,
		version:    version,
		client:     &http.Client{Timeout: exportTimeout},
		log:        log,
	}
}

// Endpoint is where this exporter posts. Useful for logging what was configured.
func (e *Exporter) Endpoint() string { return e.endpoint }

// Dropped is how many records the receiver never got.
func (e *Exporter) Dropped() uint64 { return e.dropped.Load() }

// Export sends one batch, retrying the failures worth retrying.
//
// Returns an error for the caller to log, having already accounted for the
// loss. The caller is not expected to do anything about it.
func (e *Exporter) Export(ctx context.Context, events []*event.Event, incidents []*incident.Incident) error {
	if len(events) == 0 && len(incidents) == 0 {
		return nil
	}
	body, err := Encode(e.observerID, e.version, events, incidents)
	if err != nil {
		e.note(len(events) + len(incidents))
		return fmt.Errorf("otel: encode: %w", err)
	}

	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, err := e.post(ctx, body)
		switch {
		case err == nil && status/100 == 2:
			if e.failing.Swap(false) {
				e.log.Info("the OTLP collector is accepting the record again",
					"endpoint", e.endpoint)
			}
			return nil

		case err == nil && !retryable(status):
			// A 400 will be a 400 next time too. Retrying a rejected payload
			// just delays finding out that it is malformed.
			e.note(len(events) + len(incidents))
			return fmt.Errorf("otel: %s rejected the batch with HTTP %d", e.endpoint, status)

		case err != nil:
			last = err
		default:
			last = fmt.Errorf("HTTP %d", status)
		}

		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				e.note(len(events) + len(incidents))
				return ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
	}

	e.note(len(events) + len(incidents))
	if !e.failing.Swap(true) {
		// Said once when it starts failing, not on every batch. A collector
		// that has been down for an hour should not have produced an hour of
		// identical log lines to scroll past.
		e.log.Warn("the OTLP collector is not accepting the record; the store still has it",
			"endpoint", e.endpoint, "err", last)
	}
	return fmt.Errorf("otel: export to %s: %w", e.endpoint, last)
}

func (e *Exporter) post(ctx context.Context, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range e.headers {
		req.Header.Set(k, v)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// The body has to be drained for the connection to be reused, and a
	// receiver that explains a rejection is worth not throwing away.
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

// note records that this many records will never reach the collector.
func (e *Exporter) note(n int) { e.dropped.Add(uint64(n)) }

// retryable reports whether a status is worth trying again.
func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable,
		http.StatusBadGateway, http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return true
	}
	return status/100 == 5
}

func backoff(attempt int) time.Duration {
	// 200ms, 400ms. Short: the batch behind this one is already accumulating,
	// and the store has the data regardless.
	return time.Duration(200*(1<<(attempt-1))) * time.Millisecond
}
