package identity

import (
	"context"
	"testing"
	"time"
)

// memRepo is an in-memory Repo, enough to exercise the resolution rules
// without a database.
type memRepo struct{ bindings []Binding }

func (m *memRepo) LoadCurrent(context.Context) ([]Binding, error) {
	var out []Binding
	for _, b := range m.bindings {
		if b.Current() {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *memRepo) Open(_ context.Context, b Binding) error {
	_ = m.CloseBinding(nil, b.AttrType, b.AttrValue, b.ValidFrom)
	m.bindings = append(m.bindings, b)
	return nil
}

func (m *memRepo) CloseBinding(_ context.Context, attrType, attrValue string, at int64) error {
	for i := range m.bindings {
		b := &m.bindings[i]
		if b.Current() && b.AttrType == attrType && b.AttrValue == attrValue {
			b.ValidTo = at
		}
	}
	return nil
}

func (m *memRepo) ResolveAt(_ context.Context, attrType, value string, at int64) (string, bool, error) {
	for _, b := range m.bindings {
		if b.AttrType == attrType && b.AttrValue == value &&
			b.ValidFrom <= at && (b.ValidTo == 0 || b.ValidTo > at) {
			return b.HostID, true, nil
		}
	}
	return "", false, nil
}

func (m *memRepo) LabelsFor(_ context.Context, hostID string, from, to int64) ([]string, error) {
	var out []string
	for _, b := range m.bindings {
		if b.HostID == hostID && b.ValidFrom <= to && (b.ValidTo == 0 || b.ValidTo >= from) {
			out = append(out, b.AttrValue)
		}
	}
	return out, nil
}

func newResolver(t *testing.T) (*Resolver, *memRepo) {
	t.Helper()
	repo := &memRepo{}
	r, err := New(context.Background(), repo)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, repo
}

func observe(t *testing.T, r *Resolver, at time.Time, attrs ...Attr) Result {
	t.Helper()
	res, err := r.Observe(context.Background(), Observation{At: at, Attrs: attrs})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	return res
}

func TestFirstSightingCreatesAHost(t *testing.T) {
	r, _ := newResolver(t)
	res := observe(t, r, time.Now(),
		Attr{AttrMAC, "aa:bb:cc:00:00:01"}, Attr{AttrIPv4, "10.0.0.5"})

	if !res.New {
		t.Error("a machine never seen before should be a new host")
	}
	if res.HostID == "" {
		t.Error("no host id assigned")
	}
}

// A machine that gets a new address from DHCP is still the same machine.
func TestSameHardwareNewAddressStaysOneHost(t *testing.T) {
	r, _ := newResolver(t)
	now := time.Now()

	first := observe(t, r, now, Attr{AttrMAC, "aa:bb:cc:00:00:01"}, Attr{AttrIPv4, "10.0.0.5"})
	second := observe(t, r, now.Add(time.Hour),
		Attr{AttrMAC, "aa:bb:cc:00:00:01"}, Attr{AttrIPv4, "10.0.0.9"})

	if second.HostID != first.HostID {
		t.Errorf("the same machine on a new address became a different host: %s then %s",
			first.HostID, second.HostID)
	}
	if second.New {
		t.Error("a known machine was reported as new")
	}
}

// An address turning up behind different hardware is a different machine.
// Merging them is how an identity table quietly conflates an attacker with its
// victim, or two machines fighting over one address into one impossible host.
func TestAddressChangingHandsCreatesADifferentHost(t *testing.T) {
	r, _ := newResolver(t)
	now := time.Now()

	first := observe(t, r, now, Attr{AttrMAC, "aa:bb:cc:00:00:01"}, Attr{AttrIPv4, "10.0.0.5"})
	second := observe(t, r, now.Add(time.Minute),
		Attr{AttrMAC, "aa:bb:cc:00:00:99"}, Attr{AttrIPv4, "10.0.0.5"})

	if second.HostID == first.HostID {
		t.Error("two machines claiming one address were merged into a single host")
	}
	if !second.Displaced {
		t.Error("the address changing hands was not reported")
	}
	if len(second.Moved) == 0 {
		t.Error("the address was rebound without reporting the move")
	}
}

// Hardware address is the stronger identity: if we know the machine, an address
// we have seen elsewhere joins it rather than the other way round.
func TestKnownHardwareWinsOverKnownAddress(t *testing.T) {
	r, _ := newResolver(t)
	now := time.Now()

	a := observe(t, r, now, Attr{AttrMAC, "aa:bb:cc:00:00:01"}, Attr{AttrIPv4, "10.0.0.5"})
	b := observe(t, r, now.Add(time.Minute), Attr{AttrMAC, "aa:bb:cc:00:00:02"}, Attr{AttrIPv4, "10.0.0.6"})
	if a.HostID == b.HostID {
		t.Fatal("two distinct machines were merged")
	}

	// Machine A takes over the address machine B had.
	third := observe(t, r, now.Add(2*time.Minute),
		Attr{AttrMAC, "aa:bb:cc:00:00:01"}, Attr{AttrIPv4, "10.0.0.6"})
	if third.HostID != a.HostID {
		t.Errorf("the known machine did not win: got %s, want %s", third.HostID, a.HostID)
	}
	if len(third.Moved) == 0 {
		t.Error("taking an address from another host was not reported as a move")
	}
}

func TestResolveAtSeesThePast(t *testing.T) {
	r, _ := newResolver(t)
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)

	first := observe(t, r, t0, Attr{AttrMAC, "aa:bb:cc:00:00:01"}, Attr{AttrIPv4, "10.0.0.5"})
	t1 := t0.Add(30 * time.Minute)
	second := observe(t, r, t1, Attr{AttrMAC, "aa:bb:cc:00:00:99"}, Attr{AttrIPv4, "10.0.0.5"})

	// Asking about the address as it was before the handover must return the
	// machine that held it then, not the one holding it now. This is the whole
	// reason the table is temporal.
	got, ok, err := r.ResolveAt(ctx, Attr{AttrIPv4, "10.0.0.5"}, t0.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("ResolveAt: %v (found=%v)", err, ok)
	}
	if got != first.HostID {
		t.Errorf("resolved to the current holder, not the one at that time")
	}

	got, ok, err = r.ResolveAt(ctx, Attr{AttrIPv4, "10.0.0.5"}, t1.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("ResolveAt: %v (found=%v)", err, ok)
	}
	if got != second.HostID {
		t.Error("resolved to the previous holder after the handover")
	}
}

func TestLabelsForFollowsAMachineAcrossAddresses(t *testing.T) {
	r, _ := newResolver(t)
	ctx := context.Background()
	now := time.Now()

	res := observe(t, r, now, Attr{AttrMAC, "aa:bb:cc:00:00:01"}, Attr{AttrIPv4, "10.0.0.5"})
	observe(t, r, now.Add(time.Hour), Attr{AttrMAC, "aa:bb:cc:00:00:01"}, Attr{AttrIPv4, "10.0.0.9"})

	labels, err := r.LabelsFor(ctx, res.HostID, now.Add(-time.Minute), now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("LabelsFor: %v", err)
	}
	want := map[string]bool{"aa:bb:cc:00:00:01": true, "10.0.0.5": true, "10.0.0.9": true}
	for _, l := range labels {
		delete(want, l)
	}
	if len(want) > 0 {
		t.Errorf("labels missing from history: %v (got %v)", want, labels)
	}
}

func TestObserveRejectsNothing(t *testing.T) {
	r, _ := newResolver(t)
	if _, err := r.Observe(context.Background(), Observation{At: time.Now()}); err == nil {
		t.Error("an observation with no attributes was accepted")
	}
}
