//go:build linux

package policy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/collect"
	"github.com/OmarAlghafri/netrewind/internal/event"
)

const (
	// pollInterval is how often the ruleset is read.
	//
	// This collector polls rather than subscribing, so a change made and
	// reverted inside one interval is invisible. That is a real limitation and
	// it is stated rather than hidden; the alternative, parsing `nft monitor`
	// output, breaks whenever nft changes how it prints things - and a
	// recorder that stops recording without saying so is the failure this
	// project exists to prevent.
	pollInterval = 5 * time.Second
	// readTimeout bounds one read of the ruleset.
	readTimeout = 5 * time.Second
	// maxSampleLines is how many changed rules are carried as evidence. Enough
	// to recognise the change, not enough to copy a firewall's whole policy
	// into the event store.
	maxSampleLines = 8
)

// Collector watches the nftables ruleset for changes.
type Collector struct {
	b       *event.Builder
	log     *slog.Logger
	current Ruleset
	// read is the ruleset source, replaceable in tests.
	read func(context.Context) (string, error)
}

// NewCollector returns a collector for filtering-rule changes.
func NewCollector(b *event.Builder, log *slog.Logger) *Collector {
	return &Collector{b: b, log: log, read: readNftables}
}

// Name implements collect.Collector.
func (c *Collector) Name() string { return "policy.nftables" }

// Run takes a baseline, then reports every change to it until ctx is done.
func (c *Collector) Run(ctx context.Context, out chan<- *event.Event) error {
	raw, err := c.read(ctx)
	if err != nil {
		// Not every machine runs nftables, and that is not a failure of the
		// recorder. Say so once and stop, rather than logging every interval.
		return fmt.Errorf("policy: cannot read the nftables ruleset: %w", err)
	}
	c.current = Snapshot(raw)
	c.log.Info("watching filtering rules", "collector", c.Name(),
		"rules", len(c.current.Lines), "digest", c.current.Digest)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			e, err := c.poll(ctx)
			if err != nil {
				c.log.Warn("could not read the ruleset", "err", err)
				continue
			}
			if e != nil && !collect.Emit(ctx, out, e) {
				return nil
			}
		}
	}
}

// poll reads the ruleset and returns an event if it has changed.
func (c *Collector) poll(ctx context.Context) (*event.Event, error) {
	raw, err := c.read(ctx)
	if err != nil {
		return nil, err
	}
	next := Snapshot(raw)
	if next.Digest == c.current.Digest {
		return nil, nil
	}

	diff := Compare(c.current, next)
	previous := c.current
	c.current = next
	if diff.Empty() {
		return nil, nil
	}

	// Adding a rule that drops or rejects traffic can break connectivity;
	// adding one that logs or counts cannot. They should not read the same.
	severity := event.SevNotice
	for _, line := range diff.Added {
		if interesting(line) {
			severity = event.SevWarn
			break
		}
	}

	e := c.b.New(event.SourceNftables, event.KindPolicyRuleChanged, severity,
		event.Observer(c.b.ObserverID)).
		WithAttr("added", len(diff.Added)).
		WithAttr("removed", len(diff.Removed)).
		WithAttr("rules_now", len(next.Lines)).
		WithAttr("digest", next.Digest).
		WithEvidence("digest_before", previous.Digest)
	if s := sample(diff.Added); s != "" {
		e.WithEvidence("added_rules", s)
	}
	if s := sample(diff.Removed); s != "" {
		e.WithEvidence("removed_rules", s)
	}
	return e, nil
}

func sample(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	shown := lines
	suffix := ""
	if len(shown) > maxSampleLines {
		suffix = fmt.Sprintf("\n... and %d more", len(shown)-maxSampleLines)
		shown = shown[:maxSampleLines]
	}
	return strings.Join(shown, "\n") + suffix
}

// readNftables shells out to nft.
//
// The kernel's nftables netlink interface is considerably more work to speak
// than this is worth for a collector that only needs to know whether the
// ruleset changed, and nft is present on any machine that has a ruleset to
// read in the first place.
func readNftables(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nft", "list", "ruleset")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("nft: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

var _ collect.Collector = (*Collector)(nil)
