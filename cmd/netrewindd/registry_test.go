package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/OmarAlghafri/netrewind/internal/collect/flow"
	"github.com/OmarAlghafri/netrewind/internal/collect/netlink"
	"github.com/OmarAlghafri/netrewind/internal/collect/policy"
	"github.com/OmarAlghafri/netrewind/internal/collect/probe"
	"github.com/OmarAlghafri/netrewind/internal/collect/wire"
	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/identity"
	"github.com/OmarAlghafri/netrewind/internal/registry"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/OmarAlghafri/netrewind/internal/update"
)

// A descriptor whose Name has quietly drifted from the collector it describes
// is worse than no descriptor: it reports a capability under the wrong name,
// so a UI would show the wrong collector as down. This test constructs the
// real collectors run() builds and checks every name against the table by
// hand, so a rename on either side breaks the build instead of the record.
func TestDescriptorNamesMatchTheRealCollectors(t *testing.T) {
	log := slog.New(slog.NewTextHandler(noopWriter{}, nil))
	b := event.NewBuilder("test", nil)
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer st.Close()
	ids, err := identity.New(context.Background(), st)
	if err != nil {
		t.Fatalf("identity.New: %v", err)
	}

	realNames := map[string]bool{
		netlink.NewLinkCollector(b, log).Name():                      true,
		netlink.NewNeighCollector(b, log, ids).Name():                true,
		netlink.NewRouteCollector(b, log).Name():                     true,
		netlink.NewAddrCollector(b, log).Name():                      true,
		flow.NewCollector(b, log).Name():                             true,
		policy.NewCollector(b, log).Name():                           true,
		wire.NewCollector(b, log, "", false).Name():                  true,
		probe.NewCollector(b, log, "").Name():                        true,
		update.New(update.Config{}, "dev", b, log, func() {}).Name(): true,
	}

	if len(realNames) != len(collectorDescriptors) {
		t.Errorf("%d real collectors but %d descriptors - one side has an entry the other does not",
			len(realNames), len(collectorDescriptors))
	}

	seen := map[string]bool{}
	for _, d := range collectorDescriptors {
		if seen[d.Name] {
			t.Errorf("descriptor %q registered twice", d.Name)
		}
		seen[d.Name] = true
		if !realNames[d.Name] {
			t.Errorf("descriptor %q does not match any real collector's Name()", d.Name)
		}
		if d.Platform == "" {
			t.Errorf("descriptor %q has no platform - a capability page cannot explain it", d.Name)
		}
		if d.Privilege == "" {
			t.Errorf("descriptor %q has no privilege - an operator cannot act on it", d.Name)
		}
		if len(d.Coverage) == 0 {
			t.Errorf("descriptor %q covers no event kinds - an absence of its family cannot be explained", d.Name)
		}
	}
	for name := range realNames {
		if !seen[name] {
			t.Errorf("collector %q has no descriptor - it would run with no declared capability", name)
		}
	}
}

// The registry is only worth having if run() actually reports through it. A
// descriptor table nobody feeds status into would look complete and mean
// nothing.
func TestRegistrationCoversEveryDescriptor(t *testing.T) {
	reg := registry.New(nil)
	for _, d := range collectorDescriptors {
		reg.Register(d)
	}
	if got := len(reg.Snapshot()); got != len(collectorDescriptors) {
		t.Fatalf("registered %d, want %d", got, len(collectorDescriptors))
	}
	for _, s := range reg.Snapshot() {
		if s.Status != registry.StatusUnknown {
			t.Errorf("%s: status = %s before anything ran, want unknown", s.Name, s.Status)
		}
	}
}

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
