package update

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// Config is what the operator decides about updates.
type Config struct {
	// Check looks for newer releases. Off means the recorder makes no outbound
	// connection for this at all.
	Check bool
	// Apply installs them. Separate from Check on purpose: knowing a version
	// exists and letting a machine on your network rewrite itself are
	// different decisions, and plenty of people want the first without the
	// second.
	Apply bool
	// Interval is how often to look.
	Interval time.Duration
	// Repo is owner/name. Empty means this project's own.
	Repo string
	// Token authenticates to a private repository. Not needed once public.
	Token string
	// PublicKey, if set, is an ed25519 key that must have signed the release's
	// SHA256SUMS. A release without a valid signature is then refused.
	PublicKey string
	// RulesDir receives rules that are new in the release. Existing files are
	// never touched.
	RulesDir string
}

// Updater watches for releases and, if allowed, installs them.
type Updater struct {
	cfg     Config
	client  *Client
	version string
	log     *slog.Logger
	b       *event.Builder

	// restart is called once an update is installed. The process has to be
	// replaced by the new binary, and the supervisor is what does that.
	restart func()

	// announced remembers the last version reported, so a release that sits
	// there for a month does not produce an event every interval.
	announced string
}

// New returns an updater. b and log are required; restart may be nil, in which
// case an installed update takes effect at the next restart.
func New(cfg Config, version string, b *event.Builder, log *slog.Logger, restart func()) *Updater {
	if cfg.Interval <= 0 {
		cfg.Interval = 24 * time.Hour
	}
	return &Updater{
		cfg:     cfg,
		client:  NewClient(cfg.Repo, cfg.Token),
		version: version,
		b:       b,
		log:     log,
		restart: restart,
	}
}

// Name implements collect.Collector, so the updater is supervised, counted and
// reported like every other source. If it dies, the record says so.
func (u *Updater) Name() string { return "update" }

// Run checks on a timer until ctx is done.
func (u *Updater) Run(ctx context.Context, out chan<- *event.Event) error {
	if !u.cfg.Check {
		u.log.Debug("update checks are disabled")
		<-ctx.Done()
		return nil
	}

	u.log.Info("watching for releases",
		"repo", firstNonEmpty(u.cfg.Repo, DefaultRepo),
		"every", u.cfg.Interval,
		"install", u.cfg.Apply,
		"signature_required", u.cfg.PublicKey != "")

	// A first check shortly after start rather than immediately: the recorder
	// has a network to start watching, and an appliance that has just been
	// plugged in may not have an address yet.
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			u.once(ctx, out)
			timer.Reset(u.cfg.Interval)
		}
	}
}

func (u *Updater) once(ctx context.Context, out chan<- *event.Event) {
	rel, err := u.client.Latest(ctx)
	if err != nil {
		// Not being able to reach GitHub is ordinary on a segment that does not
		// route outbound, and it is not a fault in the recorder. Said at debug
		// so a locked-down appliance does not fill its log with it.
		u.log.Debug("could not check for updates", "err", err)
		return
	}
	if rel.Draft || rel.Prerelease {
		return
	}

	latest, err := ParseVersion(rel.Tag)
	if err != nil {
		u.log.Debug("the latest release is not a version this understands", "tag", rel.Tag)
		return
	}
	current, err := ParseVersion(u.version)
	if err != nil {
		// A development build. Report what exists, never replace it: whatever
		// this binary was built to test would vanish without explanation.
		u.announce(ctx, out, rel, latest, "this is a development build and will not be replaced automatically")
		return
	}
	if !current.Newer(latest) {
		return
	}

	if !u.cfg.Apply {
		u.announce(ctx, out, rel, latest, "automatic installation is turned off")
		return
	}

	u.announce(ctx, out, rel, latest, "installing it")
	if err := u.install(ctx, out, rel, latest); err != nil {
		u.log.Error("the update was not installed", "version", latest.String(), "err", err)
		u.emit(ctx, out, u.b.New(event.SourceInternal, event.KindUpdateFailed, event.SevWarn,
			event.Observer(u.b.ObserverID)).
			WithAttr("version", latest.String()).
			WithAttr("from_version", u.version).
			WithAttr("reason", err.Error()).
			WithDedup("system.update_failed|"+latest.String()))
	}
}

func (u *Updater) install(ctx context.Context, out chan<- *event.Event, rel *Release, latest Version) error {
	tarball := TarballName(strings.TrimPrefix(rel.Tag, "v"))
	payload, signed, err := u.client.Download(ctx, rel, tarball, u.cfg.PublicKey)
	if err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	binDir := filepath.Dir(self)

	applied, err := Install(payload, binDir, u.cfg.RulesDir, u.version)
	if err != nil {
		return err
	}
	applied.Signed = signed
	applied.SourceURL = rel.URL
	if applied.To == "" {
		applied.To = latest.String()
	}

	// Recorded before the process goes away, and recorded at all because a gap
	// in the timeline with no explanation is the thing this project exists to
	// prevent. Somebody reading the record months later has to be able to see
	// that the recorder replaced itself, when, and with what.
	e := u.b.New(event.SourceInternal, event.KindUpdated, event.SevNotice,
		event.Observer(u.b.ObserverID)).
		WithAttr("from_version", applied.From).
		WithAttr("to_version", applied.To).
		WithAttr("signature_verified", applied.Signed).
		WithAttr("binaries", applied.Binaries).
		WithAttr("release_url", applied.SourceURL).
		WithEvidence("new_rules", applied.NewRules)
	if len(applied.NewRules) > 0 {
		e.WithAttr("new_rules_count", len(applied.NewRules))
	}
	u.emit(ctx, out, e)

	u.log.Warn("updated; restarting into the new binary",
		"from", applied.From, "to", applied.To,
		"signature_verified", applied.Signed, "new_rules", len(applied.NewRules))

	// Give the writer a moment to make that event durable. Restarting before it
	// is written would produce precisely the unexplained gap this is meant to
	// avoid.
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
	}

	if u.restart != nil {
		u.restart()
	}
	return nil
}

func (u *Updater) announce(ctx context.Context, out chan<- *event.Event, rel *Release, latest Version, note string) {
	if u.announced == latest.String() {
		return
	}
	u.announced = latest.String()

	u.log.Info("a newer release is available",
		"version", latest.String(), "running", u.version, "note", note, "url", rel.URL)

	u.emit(ctx, out, u.b.New(event.SourceInternal, event.KindUpdateAvailable, event.SevInfo,
		event.Observer(u.b.ObserverID)).
		WithAttr("version", latest.String()).
		WithAttr("running_version", u.version).
		WithAttr("release_url", rel.URL).
		WithAttr("note", note).
		WithDedup("system.update_available|"+latest.String()))
}

func (u *Updater) emit(ctx context.Context, out chan<- *event.Event, e *event.Event) {
	select {
	case out <- e:
	case <-ctx.Done():
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
