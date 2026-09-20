package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// parse runs the real three-layer load against a temporary rules directory, so
// the rules check does not fail on every case for an unrelated reason.
func parse(t *testing.T, file string, args ...string) (config, error) {
	t.Helper()
	dir := t.TempDir()
	rules := filepath.Join(dir, "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	args = append([]string{"--rules", rules}, args...)
	if file != "" {
		path := filepath.Join(dir, "netrewindd.yaml")
		if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append([]string{"--config", path}, args...)
	}
	fs := flag.NewFlagSet("netrewindd", flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{})
	return loadConfig(fs, args)
}

func TestDefaultsAreAWorkingRecorder(t *testing.T) {
	cfg, err := parse(t, "")
	if err != nil {
		t.Fatalf("the defaults are not usable: %v", err)
	}
	if cfg.Retention != duration(7*24*time.Hour) {
		t.Errorf("retention = %s, want a week", cfg.Retention)
	}
	if cfg.RecordDNSNames {
		t.Error("names are recorded by default; they must be opt-in")
	}
}

func TestFileOverridesDefaultsAndFlagsOverrideFile(t *testing.T) {
	const file = `
observer_id: from-file
retention: 48h
log_level: warn
`
	cfg, err := parse(t, file, "--retention", "72h")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ObserverID != "from-file" {
		t.Errorf("observer_id = %q, want the file's value", cfg.ObserverID)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("log_level = %q, want the file's value", cfg.LogLevel)
	}
	// The flag was typed, so it wins over the file.
	if cfg.Retention != duration(72*time.Hour) {
		t.Errorf("retention = %s, want the flag's 72h", cfg.Retention)
	}
}

// A flag left at its default must not silently overwrite the file: that is the
// bug that makes config files feel like they do nothing.
func TestAnUntypedFlagDoesNotOverrideTheFile(t *testing.T) {
	cfg, err := parse(t, "retention: 48h\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retention != duration(48*time.Hour) {
		t.Errorf("retention = %s; the unset flag's default overwrote the file", cfg.Retention)
	}
}

func TestAMisspelledKeyIsRefused(t *testing.T) {
	_, err := parse(t, "record_dns_name: true\n") // singular: the real key is plural
	if err == nil {
		t.Fatal("a misspelled key was accepted, so the setting would silently do nothing")
	}
	if !strings.Contains(err.Error(), "record_dns_name") {
		t.Errorf("the error does not name the offending key: %v", err)
	}
}

func TestAnEmptyFileMeansAllDefaults(t *testing.T) {
	cfg, err := parse(t, "# nothing but a comment\n")
	if err != nil {
		t.Fatalf("an empty config file was rejected: %v", err)
	}
	if cfg.Retention != duration(7*24*time.Hour) {
		t.Errorf("retention = %s, want the default", cfg.Retention)
	}
}

func TestAMissingExplicitConfigIsAnError(t *testing.T) {
	fs := flag.NewFlagSet("netrewindd", flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{})
	_, err := loadConfig(fs, []string{"--config", filepath.Join(t.TempDir(), "absent.yaml")})
	if err == nil {
		t.Fatal("a config file that was asked for by name and does not exist was ignored")
	}
}

func TestDurationsMustBeWrittenAsDurations(t *testing.T) {
	// "7" would decode to seven nanoseconds if the field were a plain int64.
	_, err := parse(t, "retention: 7\n")
	if err == nil {
		t.Fatal("a bare number was accepted as a duration")
	}
	if !strings.Contains(err.Error(), "168h") {
		t.Errorf("the error does not show the operator what to write: %v", err)
	}
}

func TestConfigurationThatWouldDestroyTheRecordIsRefused(t *testing.T) {
	cases := []struct {
		name string
		file string
		want string
	}{
		{
			name: "retention of zero prunes everything",
			file: "retention: 0s\n",
			want: "delete the whole record",
		},
		{
			name: "negative retention prunes from the future",
			file: "retention: -1h\n",
			want: "delete the whole record",
		},
		{
			name: "retention below the prune interval",
			file: "retention: 5m\n",
			want: "shorter than the hour between prunes",
		},
		{
			name: "gap threshold below the heartbeat",
			file: "gap_threshold: 1s\n",
			want: "below the 10s heartbeat",
		},
		{
			name: "unparseable metrics address",
			file: "metrics_addr: not-an-address\n",
			want: "is not host:port",
		},
		{
			name: "a probe target that is a name, not an address",
			file: "probe: example.com\n",
			want: "does not resolve names",
		},
		{
			name: "an unknown log level",
			file: "log_level: verbose\n",
			want: "not one of debug, info, warn, error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse(t, tc.file)
			if err == nil {
				t.Fatal("accepted a configuration that would break the recorder")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not explain the problem.\n got: %v\nwant it to mention: %s", err, tc.want)
			}
		})
	}
}

func TestARulesDirectoryThatIsNotThereIsRefused(t *testing.T) {
	fs := flag.NewFlagSet("netrewindd", flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{})
	_, err := loadConfig(fs, []string{"--rules", filepath.Join(t.TempDir(), "absent")})
	if err == nil {
		t.Fatal("a missing rules directory was accepted; correlation would silently do nothing")
	}
	if !strings.Contains(err.Error(), "empty value to run without correlation") {
		t.Errorf("the error does not say how to run without rules: %v", err)
	}
}

func TestCorrelationCanBeTurnedOffDeliberately(t *testing.T) {
	fs := flag.NewFlagSet("netrewindd", flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{})
	cfg, err := loadConfig(fs, []string{"--rules", ""})
	if err != nil {
		t.Fatalf("an empty rules directory should mean no correlation, not an error: %v", err)
	}
	if cfg.RulesDir != "" {
		t.Errorf("rules = %q, want empty", cfg.RulesDir)
	}
}

// Every problem at once must be reported at once. An operator fixing a config
// file one restart at a time is an operator not recording.
func TestAllProblemsAreReportedTogether(t *testing.T) {
	_, err := parse(t, "retention: 0s\nlog_level: verbose\nmetrics_addr: nope\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"retention", "log_level", "metrics_addr"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error stops before mentioning %s: %v", want, err)
		}
	}
}

// --version has to be answerable by the binary alone, whatever state the host
// it landed on is in.
//
// The updater runs it on a downloaded build to decide whether that build works
// before replacing anything, inheriting the running daemon's working
// directory. While the configuration was validated first, the same binary
// printed its version from a directory that happened to contain a rules/ and
// exited 2 from one that did not - so whether an update could be installed
// depended on where the daemon happened to be running from, and the refusal
// said only that running it failed, which points at the new build rather than
// at a rules path.
func TestVersionIsAnsweredWithoutJudgingTheConfiguration(t *testing.T) {
	fs := flag.NewFlagSet("netrewindd", flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{})

	// A rules directory that does not exist is enough to fail validation, and
	// is exactly what a half-installed or freshly unpacked host looks like.
	cfg, err := loadConfig(fs, []string{"--version", "--rules", "/nonexistent/rules"})
	if err != nil {
		t.Fatalf("--version must not depend on the configuration being usable: %v", err)
	}
	if !cfg.ShowVersion {
		t.Error("--version was accepted but not recorded, so nothing would print it")
	}
}

// ...and --check-config still judges it, because that is the whole job.
func TestCheckConfigStillRefusesAConfigurationThatWouldNotWork(t *testing.T) {
	fs := flag.NewFlagSet("netrewindd", flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{})
	if _, err := loadConfig(fs, []string{"--check-config", "--rules", "/nonexistent/rules"}); err == nil {
		t.Fatal("--check-config accepted a configuration the recorder cannot use")
	}
}

func TestNotesDefaultsToEnabledWithThreadsAllowed(t *testing.T) {
	cfg, err := parse(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Notes.enabled() {
		t.Error("notes should be enabled by default")
	}
	if !cfg.Notes.threads() {
		t.Error("notes threads should be allowed by default")
	}
}

func TestNotesCanBeDisabledWithoutDisablingTheApi(t *testing.T) {
	cfg, err := parse(t, "notes:\n  enabled: false\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notes.enabled() {
		t.Error("notes.enabled: false was not honored")
	}
	if !cfg.API.enabled() {
		t.Error("disabling notes must not disable the read-only API")
	}
}

func TestNotesThreadsCanBeDisabledWithoutDisablingNotes(t *testing.T) {
	cfg, err := parse(t, "notes:\n  threads: false\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Notes.enabled() {
		t.Error("notes should stay enabled")
	}
	if cfg.Notes.threads() {
		t.Error("notes.threads: false was not honored")
	}
}

func TestNotesThreadsOnWithNotesOffIsRefused(t *testing.T) {
	_, err := parse(t, "notes:\n  enabled: false\n  threads: true\n")
	if err == nil {
		t.Fatal("accepted threads:true with enabled:false, which can never persist anything")
	}
	if !strings.Contains(err.Error(), "no notes store") {
		t.Errorf("the error does not explain the problem: %v", err)
	}
}
