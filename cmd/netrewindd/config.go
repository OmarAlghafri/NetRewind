package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/collect/probe"
	"github.com/OmarAlghafri/netrewind/internal/store"
	"github.com/OmarAlghafri/netrewind/internal/update"
	"gopkg.in/yaml.v3"
)

// DefaultConfigPath is read when it exists and no other path was given.
//
// A missing file there is not an error: the defaults are a working recorder,
// and an appliance that refuses to start because nobody wrote a config file
// would be a recorder that is not recording.
const DefaultConfigPath = "/etc/netrewind/netrewindd.yaml"

// config is everything the recorder needs to be told.
//
// The same struct is the YAML document and the parsed flags, so the two can
// never describe different sets of options. Precedence is defaults, then the
// file, then flags - the order of increasing specificity, which is what an
// operator debugging a running service expects when they add one flag to
// override one line.
type config struct {
	DBPath       string `yaml:"db"`
	ObserverID   string `yaml:"observer_id"`
	RulesDir     string `yaml:"rules"`
	MetricsAddr  string `yaml:"metrics_addr"`
	WireIface    string `yaml:"wire_iface"`
	ProbeTargets string `yaml:"probe"`
	// OTLPEndpoint is an OpenTelemetry collector to copy the record to, e.g.
	// http://localhost:4318. Empty means no export, which is the default: an
	// appliance should not talk to anything nobody asked it to talk to.
	OTLPEndpoint   string            `yaml:"otlp_endpoint"`
	OTLPHeaders    map[string]string `yaml:"otlp_headers"`
	LogLevel       string            `yaml:"log_level"`
	RecordDNSNames bool              `yaml:"record_dns_names"`

	// Update controls whether the recorder looks for, and installs, new
	// releases of itself.
	Update updateConfig `yaml:"update"`

	// CheckOnly comes from --check-config and never from the file. It is what
	// the systemd unit runs before starting, so a configuration the recorder
	// cannot use stops the service instead of starting one that records the
	// wrong thing.
	CheckOnly bool `yaml:"-"`
	// ShowVersion prints the version and exits. The updater runs a downloaded
	// binary with this before trusting it, so it is the check that a new build
	// is the right architecture and not truncated.
	ShowVersion bool     `yaml:"-"`
	Retention   duration `yaml:"retention"`
	GapAfter    duration `yaml:"gap_threshold"`
}

// updateConfig is what the operator decides about the recorder maintaining
// itself.
//
// Checking and applying are separate because they are separate decisions.
// Knowing a version exists is information; letting a machine on your network
// rewrite its own binary is a change of trust, and an operator is entitled to
// want the first without the second.
type updateConfig struct {
	Check bool     `yaml:"check"`
	Apply bool     `yaml:"apply"`
	Every duration `yaml:"every"`
	Repo  string   `yaml:"repo"`
	// Token reaches a private repository. Not needed once it is public.
	Token string `yaml:"token"`
	// PublicKey is an ed25519 key, base64. When set, a release whose
	// SHA256SUMS is not signed by it is refused rather than installed.
	PublicKey string `yaml:"public_key"`
}

// duration is a time.Duration the config file can write the way the flag does.
//
// YAML has no duration type. Decoding straight into a time.Duration would
// silently accept a bare number and read it as nanoseconds, so an operator
// writing "retention: 7" would get seven nanoseconds of history and no
// complaint - the recorder would start, look healthy, and keep nothing.
type duration time.Duration

func (d duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("%q is not a duration; write it like 168h, 30m or 45s", node.Value)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%q is not a duration; write it like 168h, 30m or 45s", s)
	}
	*d = duration(v)
	return nil
}

func defaultConfig() config {
	return config{
		DBPath:     store.DefaultPath(),
		ObserverID: defaultObserverID(),
		RulesDir:   "rules",
		LogLevel:   "info",
		Retention:  duration(7 * 24 * time.Hour),
		GapAfter:   duration(defaultGapThreshold),
		// Checking is on and installing is off by default.
		//
		// Knowing a fix exists costs one HTTPS request a day and is nearly
		// always wanted. Replacing the binary of a recorder whose output is
		// meant to be evidence is a decision its operator should make
		// deliberately, so it is opted into rather than out of.
		Update: updateConfig{Check: true, Apply: false, Every: duration(24 * time.Hour)},
	}
}

// loadConfig resolves the three layers into one configuration and checks it.
func loadConfig(fs *flag.FlagSet, args []string) (config, error) {
	cfg := defaultConfig()

	var (
		configPath     = fs.String("config", "", "YAML configuration file; defaults to "+DefaultConfigPath+" when that exists")
		checkOnly      = fs.Bool("check-config", false, "check the configuration and exit without recording")
		showVersion    = fs.Bool("version", false, "print the version and exit")
		dbPath         = fs.String("db", cfg.DBPath, "path to the event store")
		observerID     = fs.String("observer-id", cfg.ObserverID, "identity of this recorder")
		logLevel       = fs.String("log-level", cfg.LogLevel, "debug, info, warn or error")
		retention      = fs.Duration("retention", time.Duration(cfg.Retention), "how much history to keep")
		gapAfter       = fs.Duration("gap-threshold", time.Duration(cfg.GapAfter), "absence longer than this is recorded as a gap in the record")
		rulesDir       = fs.String("rules", cfg.RulesDir, "directory of correlation rules; empty disables correlation")
		metricsAddr    = fs.String("metrics-addr", "", "serve Prometheus metrics on this address, e.g. 127.0.0.1:9464; empty disables it")
		wireIface      = fs.String("wire-iface", "", "capture DHCP, DNS and ICMP on this interface; empty means all")
		recordDNSNames = fs.Bool("record-dns-names", false, "store the names looked up. Off by default: there are networks where recording them is not permitted")
		probeTargets   = fs.String("probe", "", "addresses to measure reachability to; empty follows the default gateway")
		otlpEndpoint   = fs.String("otlp-endpoint", "", "copy the record to this OpenTelemetry collector, e.g. http://localhost:4318; empty disables it")
		updateCheck    = fs.Bool("update-check", true, "look for newer releases")
		updateApply    = fs.Bool("update-apply", false, "install newer releases automatically")
	)
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	path, required := *configPath, true
	if path == "" {
		path, required = DefaultConfigPath, false
	}
	if err := cfg.mergeFile(path, required); err != nil {
		return config{}, err
	}

	// Only flags the operator actually typed override the file. Visit reports
	// exactly those, which is why the flags are registered with the defaults
	// rather than with sentinels.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "db":
			cfg.DBPath = *dbPath
		case "observer-id":
			cfg.ObserverID = *observerID
		case "log-level":
			cfg.LogLevel = *logLevel
		case "retention":
			cfg.Retention = duration(*retention)
		case "gap-threshold":
			cfg.GapAfter = duration(*gapAfter)
		case "rules":
			cfg.RulesDir = *rulesDir
		case "metrics-addr":
			cfg.MetricsAddr = *metricsAddr
		case "wire-iface":
			cfg.WireIface = *wireIface
		case "record-dns-names":
			cfg.RecordDNSNames = *recordDNSNames
		case "probe":
			cfg.ProbeTargets = *probeTargets
		case "otlp-endpoint":
			cfg.OTLPEndpoint = *otlpEndpoint
		case "update-check":
			cfg.Update.Check = *updateCheck
		case "update-apply":
			cfg.Update.Apply = *updateApply
		}
	})

	cfg.CheckOnly = *checkOnly
	cfg.ShowVersion = *showVersion

	// --version is answered before the configuration is judged, because it is
	// a property of the binary and nothing else.
	//
	// The updater runs exactly this on a downloaded build to decide whether it
	// works before replacing anything, and it inherits the running daemon's
	// working directory when it does. With validation first, the answer
	// depended on things that have nothing to do with the binary: the same
	// file printed its version from a directory that happened to contain a
	// rules/ and exited 2 from one that did not. An update refused that way
	// reports only that running it failed, which points an operator at the new
	// build when the cause is a rules path - and the check exists to be the
	// last thing standing between an update and a recorder that has silently
	// stopped, so it must not fail for reasons of its own.
	//
	// --check-config still validates. Judging the configuration is its entire
	// purpose.
	if cfg.ShowVersion {
		return cfg, nil
	}

	if err := cfg.validate(); err != nil {
		return config{}, err
	}
	return cfg, nil
}

// mergeFile overlays a YAML document onto the defaults.
//
// Unknown keys are refused rather than ignored. A misspelled key in a config
// file is the quietest possible failure: the operator believes they turned
// something on, the recorder never saw it, and nobody finds out until the
// record is needed and the setting was never in effect.
func (c *config) mergeFile(path string, required bool) error {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist) && !required:
		return nil
	case err != nil:
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := c.merge(data); err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	return nil
}

// merge applies one YAML document. Separate from reading it so the parsing
// rules can be tested without a filesystem.
func (c *config) merge(data []byte) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		// An empty file is a legitimate way to say "all defaults".
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

// validate refuses a configuration that would make the recorder useless.
//
// Every check here is a setting that would otherwise fail silently: the
// recorder would start, report itself healthy, and produce a record that is
// empty, wrong, or deleted. Refusing to start is louder and therefore safer.
func (c *config) validate() error {
	var problems []string

	if c.DBPath == "" {
		problems = append(problems, "db: the event store needs a path")
	}
	if c.ObserverID == "" {
		problems = append(problems, "observer_id: events must say which recorder produced them")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		problems = append(problems, fmt.Sprintf("log_level: %q is not one of debug, info, warn, error", c.LogLevel))
	}

	// Retention of zero or less would have the hourly prune delete everything
	// ever recorded, including the incident being investigated.
	if c.Retention <= 0 {
		problems = append(problems, fmt.Sprintf(
			"retention: %s would delete the whole record on the next prune; it must be positive", c.Retention))
	} else if time.Duration(c.Retention) < time.Hour {
		problems = append(problems, fmt.Sprintf(
			"retention: %s is shorter than the hour between prunes, so history would vanish before anyone could ask about it", c.Retention))
	}

	// A gap threshold below the heartbeat means every ordinary restart is
	// reported as a hole in the record, and real holes stop standing out.
	if c.GapAfter <= 0 {
		problems = append(problems, fmt.Sprintf("gap_threshold: %s must be positive", c.GapAfter))
	} else if time.Duration(c.GapAfter) < heartbeatInterval {
		problems = append(problems, fmt.Sprintf(
			"gap_threshold: %s is below the %s heartbeat, so every restart would be recorded as a gap",
			c.GapAfter, heartbeatInterval))
	}

	if c.Update.Apply && !c.Update.Check {
		problems = append(problems,
			"update: apply is on but check is off, so nothing would ever be installed")
	}
	if c.Update.Check && time.Duration(c.Update.Every) < time.Hour {
		problems = append(problems, fmt.Sprintf(
			"update.every: %s is too often; github rate limits, and a release does not appear more than once an hour", c.Update.Every))
	}
	if c.Update.Repo != "" && !strings.Contains(c.Update.Repo, "/") {
		problems = append(problems, fmt.Sprintf(
			"update.repo: %q is not owner/name", c.Update.Repo))
	}
	if c.Update.PublicKey != "" {
		// Checked here rather than at the first update, which might be months
		// away and would then silently refuse every release.
		if err := update.VerifySignature(c.Update.PublicKey, nil, make([]byte, 64)); err != nil &&
			strings.Contains(err.Error(), "public_key") {
			problems = append(problems, "update.public_key: "+err.Error())
		}
	}
	if c.OTLPEndpoint != "" {
		// A malformed endpoint means the export silently never happens, and the
		// operator believes the record is reaching their pipeline.
		u, err := url.Parse(c.OTLPEndpoint)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("otlp_endpoint: %q is not a URL (%v)", c.OTLPEndpoint, err))
		case u.Scheme != "http" && u.Scheme != "https":
			problems = append(problems, fmt.Sprintf(
				"otlp_endpoint: %q must start with http:// or https://. This is OTLP over HTTP; the gRPC port (4317) will not answer it - use 4318", c.OTLPEndpoint))
		case u.Host == "":
			problems = append(problems, fmt.Sprintf("otlp_endpoint: %q names no host", c.OTLPEndpoint))
		}
	}
	if c.MetricsAddr != "" {
		if _, _, err := net.SplitHostPort(c.MetricsAddr); err != nil {
			problems = append(problems, fmt.Sprintf(
				"metrics_addr: %q is not host:port (%v)", c.MetricsAddr, err))
		}
	}
	for _, t := range probe.ParseTargets(c.ProbeTargets) {
		if net.ParseIP(t) == nil {
			problems = append(problems, fmt.Sprintf(
				"probe: %q is not an IP address; this collector sends ICMP and does not resolve names", t))
		}
	}
	if c.RulesDir != "" {
		if info, err := os.Stat(c.RulesDir); err != nil {
			problems = append(problems, fmt.Sprintf(
				"rules: %s cannot be read (%v); pass an empty value to run without correlation", c.RulesDir, err))
		} else if !info.IsDir() {
			problems = append(problems, fmt.Sprintf("rules: %s is not a directory", c.RulesDir))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("configuration is not usable:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}
