package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

func render(t *testing.T, r *Registry) string {
	t.Helper()
	var b strings.Builder
	if _, err := r.WriteTo(&b); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	return b.String()
}

func TestDeclaredMetricsExistAtZero(t *testing.T) {
	r := New()
	r.Declare("netrewind_test_total", Counter, "A test counter.")

	out := render(t, r)
	if !strings.Contains(out, "# HELP netrewind_test_total A test counter.") {
		t.Error("help line missing")
	}
	if !strings.Contains(out, "# TYPE netrewind_test_total counter") {
		t.Error("type line missing")
	}
}

// A series that springs into existence on the first failure cannot be alerted
// on before it, which is exactly backwards.
//
// A HELP and TYPE line with no sample under it is invisible to a scraper, so
// this checks for the sample, not the declaration.
func TestTheHonestyMetricsExistBeforeAnythingGoesWrong(t *testing.T) {
	rec := NewRecorder("test", "obs")
	out := render(t, rec.Registry())

	for _, sample := range []string{
		BlindSeconds + " 0\n",
		ClockSteps + " 0\n",
		DroppedTotal + `{source="ringbuf"} 0` + "\n",
	} {
		if !strings.Contains(out, sample) {
			t.Errorf("no series for %q until something goes wrong:\n%s", strings.TrimSpace(sample), out)
		}
	}
}

func TestCountersAccumulate(t *testing.T) {
	r := New()
	r.Declare("hits_total", Counter, "")
	r.Inc("hits_total", Labels{"kind": "a"})
	r.Inc("hits_total", Labels{"kind": "a"})
	r.Inc("hits_total", Labels{"kind": "b"})

	if v, _ := r.Value("hits_total", Labels{"kind": "a"}); v != 2 {
		t.Errorf("a = %v, want 2", v)
	}
	if v, _ := r.Value("hits_total", Labels{"kind": "b"}); v != 1 {
		t.Errorf("b = %v, want 1", v)
	}
}

func TestUndeclaredMetricIsIgnoredRatherThanPanicking(t *testing.T) {
	r := New()
	r.Inc("never_declared", nil) // a bug in the caller, not a reason to crash
	if out := render(t, r); out != "" {
		t.Errorf("an undeclared metric was rendered: %q", out)
	}
}

// Two scrapes of an unchanged registry must be identical, or diffs and tests
// mean nothing.
func TestOutputIsStable(t *testing.T) {
	r := New()
	r.Declare("m_total", Counter, "")
	for _, k := range []string{"z", "a", "m", "b"} {
		r.Inc("m_total", Labels{"kind": k})
	}
	if render(t, r) != render(t, r) {
		t.Error("two renders of the same registry differed")
	}
}

// A subject label comes off the wire and must never be able to forge a series.
func TestLabelValuesAreEscaped(t *testing.T) {
	r := New()
	r.Declare("m_total", Counter, "")
	r.Inc("m_total", Labels{"subject": `he said "hi"` + "\n" + `and\then`})

	out := render(t, r)
	if strings.Count(out, "\n") != 2 { // help is absent, so: the TYPE line and the series
		t.Errorf("a newline in a label value broke the line structure:\n%s", out)
	}
	if !strings.Contains(out, `\"hi\"`) {
		t.Errorf("quotes not escaped: %s", out)
	}
	if !strings.Contains(out, `\\then`) {
		t.Errorf("backslash not escaped: %s", out)
	}
	if strings.Contains(out, `\\"`) {
		t.Errorf("label value was escaped twice: %s", out)
	}
}

// The exposition format is UTF-8. A Go quoted string would turn a hostname in
// any non-Latin script into a row of escape sequences that no dashboard can
// display, so the quoting is done by hand.
func TestNonLatinLabelValuesSurviveIntact(t *testing.T) {
	r := New()
	r.Declare("m_total", Counter, "")
	const host = "خادم-الرياض"
	r.Inc("m_total", Labels{"subject": host})

	if out := render(t, r); !strings.Contains(out, host) {
		t.Errorf("a non-Latin label value was mangled:\n%s", out)
	}
}

func TestWholeNumbersRenderWithoutExponent(t *testing.T) {
	r := New()
	r.Declare("big_total", Counter, "")
	r.Add("big_total", nil, 1787512752577630230)

	if out := render(t, r); !strings.Contains(out, "1787512752577630208") && !strings.Contains(out, "e+") == false {
		// float64 cannot hold that exactly; what matters is that it is not
		// rendered in exponent form, which a scraper reads but a human cannot.
		if strings.Contains(out, "e+") {
			t.Errorf("large value rendered as an exponent: %s", out)
		}
	}
	r.Set("big_total", nil, 3)
	if out := render(t, r); !strings.Contains(out, "big_total 3\n") {
		t.Errorf("whole number not rendered plainly: %s", out)
	}
}

// A folded event stands for several occurrences. Counting rows instead would
// under-report exactly when things are worst.
func TestFoldedEventsCountTheirOccurrences(t *testing.T) {
	rec := NewRecorder("test", "obs")
	b := event.NewBuilder("obs", nil)

	e := b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface("eth1", 3))
	e.Count = 7
	rec.Observe(e)

	v, ok := rec.Registry().Value(EventsTotal,
		Labels{"kind": string(event.KindLinkDown), "severity": string(event.SevWarn)})
	if !ok || v != 7 {
		t.Errorf("events_total = %v (found=%v), want 7", v, ok)
	}
}

func TestGapsBecomeBlindSeconds(t *testing.T) {
	rec := NewRecorder("test", "obs")
	b := event.NewBuilder("obs", nil)

	rec.Observe(b.New(event.SourceInternal, event.KindSystemGap, event.SevWarn, event.Observer("obs")).
		WithAttr("gap_duration_ms", 7002))
	rec.Observe(b.New(event.SourceInternal, event.KindSystemGap, event.SevWarn, event.Observer("obs")).
		WithAttr("gap_duration_ms", 3000))

	v, ok := rec.Registry().Value(BlindSeconds, nil)
	if !ok {
		t.Fatal("blind seconds not recorded")
	}
	if want := 10.002; v < want-0.001 || v > want+0.001 {
		t.Errorf("blind seconds = %v, want %v", v, want)
	}
}

func TestDropsAreAttributedToTheirSource(t *testing.T) {
	rec := NewRecorder("test", "obs")
	b := event.NewBuilder("obs", nil)

	rec.Observe(b.New(event.SourceEBPF, event.KindSystemDrop, event.SevWarn, event.Observer("obs")).
		WithAttr("dropped", 42).
		WithAttr("source", "ringbuf"))

	if v, ok := rec.Registry().Value(DroppedTotal, Labels{"source": "ringbuf"}); !ok || v != 42 {
		t.Errorf("dropped = %v (found=%v), want 42", v, ok)
	}
}

func TestIncidentsAreCountedByRule(t *testing.T) {
	rec := NewRecorder("test", "obs")
	rec.ObserveIncident(&incident.Incident{RuleID: "gateway-hijack", Severity: event.SevError})
	rec.ObserveIncident(&incident.Incident{RuleID: "gateway-hijack", Severity: event.SevError})
	rec.ObserveIncident(&incident.Incident{RuleID: "port-flapping", Severity: event.SevWarn})

	if v, _ := rec.Registry().Value(IncidentsTotal,
		Labels{"rule": "gateway-hijack", "severity": "error"}); v != 2 {
		t.Errorf("gateway-hijack = %v, want 2", v)
	}
}

func TestCollectorUpFlipsToZero(t *testing.T) {
	rec := NewRecorder("test", "obs")
	rec.SetCollector("netlink.link", true)
	if v, _ := rec.Registry().Value(CollectorUp, Labels{"collector": "netlink.link"}); v != 1 {
		t.Fatal("collector not marked up")
	}
	rec.SetCollector("netlink.link", false)
	if v, _ := rec.Registry().Value(CollectorUp, Labels{"collector": "netlink.link"}); v != 0 {
		t.Error("a stopped collector is still reported as running")
	}
}

func TestHandlerServesTheTextFormat(t *testing.T) {
	rec := NewRecorder("0.6.0", "obs")
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	rec.Handler().ServeHTTP(w, req)

	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type = %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `netrewind_build_info{observer="obs",version="0.6.0"} 1`) {
		t.Errorf("build info missing or malformed:\n%s", body)
	}
}
