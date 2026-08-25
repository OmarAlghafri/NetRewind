package listen

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestWhatCountsAsLocal(t *testing.T) {
	cases := []struct {
		addr  string
		local bool
		why   string
	}{
		{"127.0.0.1:8464", true, "the default, and the only address that reaches nothing else"},
		{"127.0.0.1:0", true, "any port, still loopback"},
		{"localhost:8464", true, "resolves to loopback on every sane host"},
		{"[::1]:8464", true, "loopback over IPv6"},
		{"127.0.0.53:8464", true, "the whole 127/8 range is loopback"},

		// This is the case the two former copies of this rule disagreed about.
		// One of them called it loopback, so binding the web interface to every
		// interface produced no warning at all.
		{":8464", false, "an empty host binds every interface, including the watched network"},
		{"0.0.0.0:8464", false, "every IPv4 interface, said explicitly"},
		{"[::]:8464", false, "every interface over IPv6"},
		{"192.168.1.10:8464", false, "an address on the network being watched"},
		{"10.0.0.5:8464", false, "likewise"},

		// An address that cannot be parsed is not evidence of safety.
		{"not-an-address", false, "unparseable, so not known to be local"},
		{"", false, "empty, so not known to be local"},
	}

	for _, tc := range cases {
		if got := IsLocal(tc.addr); got != tc.local {
			t.Errorf("IsLocal(%q) = %v, want %v — %s", tc.addr, got, tc.local, tc.why)
		}
	}
}

func TestExposedSurfacesAreAnnounced(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	WarnIfExposed(log, "0.0.0.0:9464", "metrics endpoint")

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("an exposed surface was not warned about: %q", out)
	}
	if !strings.Contains(out, "metrics endpoint") {
		t.Error("the warning does not say which surface")
	}
	if !strings.Contains(out, "0.0.0.0:9464") {
		t.Error("the warning does not say which address")
	}
}

func TestALocalSurfaceIsNotWarnedAbout(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	WarnIfExposed(log, "127.0.0.1:9464", "metrics endpoint")

	if buf.Len() != 0 {
		t.Errorf("the default binding produced a warning: %q", buf.String())
	}
}

// The warning path must not be the thing that takes the recorder down.
func TestANilLoggerIsNotAPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked with no logger: %v", r)
		}
	}()
	WarnIfExposed(nil, "0.0.0.0:9464", "metrics endpoint")
}
