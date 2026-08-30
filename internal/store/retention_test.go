package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/incident"
)

// Retention is documented as how much history the recorder keeps, and for a
// long time it bounded one of the three tables that hold it.
//
// The events went; the conclusions and the identity bindings did not.
// PruneIncidents existed, was tested, and was never called by anything. Nothing
// pruned identity at all. So a recorder on a segment with any churn grew until
// the disk was full, however retention was set - and the deployment that
// suffers most is the appliance, whose whole premise is being plugged in and
// forgotten. Measured before the fix: after a prune that removed all twenty
// thousand events, twenty thousand bindings and a thousand incidents remained.
//
// This holds all three to the same promise.
func TestRetentionBoundsEveryTableAndNotJustTheEvents(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs", nil)

	// Eight days of history, against a week of retention.
	old := time.Now().Add(-8 * 24 * time.Hour)
	const n = 500

	for i := 0; i < n; i++ {
		ip := fmt.Sprintf("10.9.%d.%d", i/256%256, i%256)
		host := fmt.Sprintf("host-%d", i)
		e := b.New(event.SourceNetlink, event.KindARPBindingNew, event.SevInfo, event.Host(ip, ""))
		e.Subject.ID = host
		e.TSWall = old.Add(time.Duration(i) * time.Millisecond).UnixNano()
		if err := st.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
		if err := st.Open(ctx, identity.Binding{
			HostID: host, AttrType: identity.AttrIPv4, AttrValue: ip,
			ValidFrom: e.TSWall, Confidence: 90,
		}); err != nil {
			t.Fatal(err)
		}
		if i%10 == 0 {
			if err := st.AppendIncidents(ctx, &incident.Incident{
				ID: fmt.Sprintf("inc-%d", i), OpenedAt: e.TSWall, ClosedAt: e.TSWall,
				Status: incident.StatusClosed, Title: "an old conclusion",
				Severity: event.SevWarn, Confidence: 80, RuleID: "port-flapping",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	events, err := st.Prune(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := st.PruneIdentity(ctx, cutoff.UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	incidents, err := st.PruneIncidents(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pruned %d events, %d identity bindings, %d incidents", events, bindings, incidents)

	if got := rows(t, st, "events"); got != 0 {
		t.Errorf("%d events survived retention", got)
	}
	if got := rows(t, st, "identity_binding"); got != 0 {
		t.Errorf("%d identity bindings survived retention; the table grows without limit", got)
	}
	if got := rows(t, st, "incidents"); got != 0 {
		t.Errorf("%d incidents survived retention; the table grows without limit", got)
	}
}

// The half of the identity table that must survive, and would be the easy
// thing to get wrong: a binding still in force carries the time it was made,
// not the time it was last seen, so pruning by age alone would delete exactly
// the machines that have been on the network longest.
func TestTheMachinesStillOnTheNetworkKeepTheirIdentities(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	b := event.NewBuilder("obs", nil)

	longAgo := time.Now().Add(-90 * 24 * time.Hour)
	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	// A server that has held one address for three months and is still talking.
	if err := st.Open(ctx, identity.Binding{
		HostID: "the-server", AttrType: identity.AttrIPv4, AttrValue: "10.9.0.10",
		ValidFrom: longAgo.UnixNano(), Confidence: 95,
	}); err != nil {
		t.Fatal(err)
	}
	recent := b.New(event.SourceNetlink, event.KindARPBindingNew, event.SevInfo,
		event.Host("10.9.0.10", ""))
	recent.Subject.ID = "the-server"
	recent.TSWall = time.Now().Add(-time.Minute).UnixNano()
	if err := st.Append(ctx, recent); err != nil {
		t.Fatal(err)
	}

	// A machine seen once during a scan three months ago and never again.
	if err := st.Open(ctx, identity.Binding{
		HostID: "a-stranger", AttrType: identity.AttrIPv4, AttrValue: "10.9.0.200",
		ValidFrom: longAgo.UnixNano(), Confidence: 60,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.PruneIdentity(ctx, cutoff.UnixNano()); err != nil {
		t.Fatal(err)
	}

	host, ok, err := st.ResolveAt(ctx, identity.AttrIPv4, "10.9.0.10", time.Now().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || host != "the-server" {
		t.Error("a machine still on the network lost its identity to the prune; " +
			"a binding in force carries when it was made, not when it was last seen")
	}
	if _, ok, err := st.ResolveAt(ctx, identity.AttrIPv4, "10.9.0.200", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("a binding whose host appears in no retained event was kept")
	}
}

// A binding that was superseded inside the retention window is history somebody
// may still ask about: it is what makes "who held this address last Tuesday" a
// question with an answer.
func TestASupersededBindingInsideTheWindowIsKept(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()

	yesterday := time.Now().Add(-24 * time.Hour)
	today := time.Now().Add(-time.Hour)
	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	if err := st.Open(ctx, identity.Binding{
		HostID: "first", AttrType: identity.AttrIPv4, AttrValue: "10.9.1.5",
		ValidFrom: yesterday.UnixNano(), Confidence: 90,
	}); err != nil {
		t.Fatal(err)
	}
	// The address changes hands, which closes the first binding.
	if err := st.Open(ctx, identity.Binding{
		HostID: "second", AttrType: identity.AttrIPv4, AttrValue: "10.9.1.5",
		ValidFrom: today.UnixNano(), Confidence: 90,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.PruneIdentity(ctx, cutoff.UnixNano()); err != nil {
		t.Fatal(err)
	}

	host, ok, err := st.ResolveAt(ctx, identity.AttrIPv4, "10.9.1.5", yesterday.Add(time.Hour).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || host != "first" {
		t.Errorf("asking who held the address yesterday returned %q (found=%v), want first; "+
			"the prune took history that is still inside the window", host, ok)
	}
}

func openStore(t *testing.T) *SQLite {
	t.Helper()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func rows(t *testing.T, s *SQLite, table string) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
