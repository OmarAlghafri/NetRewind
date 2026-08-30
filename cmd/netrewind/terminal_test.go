package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
	"github.com/OmarAlghafri/netrewind/internal/store"
)

// The record holds strings the watched network chose, and the terminal reading
// them treats some bytes as instructions rather than as text.
//
// The Linux kernel accepts an escape character in an interface name; a DNS name
// is whatever somebody looked up; nftables rule text is arbitrary. Printed
// unchanged, ESC [ 2 J clears the operator's screen, ESC [ 1;1 H puts the
// cursor at the top of it, and a carriage return overwrites the line before -
// so the machine being investigated gets to decide what the investigator reads.
// That is a worse failure than the fault the recorder was deployed to find,
// because everything else about the record is correct and the account on the
// screen is not.
func TestTheTerminalNeverExecutesWhatTheNetworkWrote(t *testing.T) {
	hostile := map[string]string{
		"clear the screen":  "\x1b[2Jgone",
		"set the title":     "\x1b]0;owned\x07",
		"overwrite by hand": "real\rfake",
		"backspace":         "safe\b\b\b\bevil",
		"C1 escape":         "xJy",
		"delete":            "a\x7fb",
	}

	for name, payload := range hostile {
		t.Run(name, func(t *testing.T) {
			db := seedHostile(t, payload)

			for _, args := range [][]string{
				{"events", "--db", db, "--last", "1h"},
				{"timeline", "--db", db, "--last", "1h"},
				{"what-happened", "--db", db, "--host", payload, "--window", "1h"},
			} {
				var out bytes.Buffer
				root := newRootCmd()
				root.SetOut(&out)
				root.SetErr(&out)
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatalf("%v: %v", args, err)
				}
				if body := out.String(); containsControl(body) {
					t.Errorf("%v wrote a control character the terminal would act on: %q",
						args[0], firstControl(body))
				}
			}
		})
	}
}

// Ordinary text has to survive untouched, including text that is not ASCII.
// A recorder that mangles a hostname in Arabic to protect against an escape
// sequence has traded one wrong answer for another.
func TestOrdinaryTextIsPrintedAsItIs(t *testing.T) {
	for _, s := range []string{
		"eth0", "10.99.0.11", "02:00:00:00:00:11",
		"مسجل-الشبكة", "router-café", "日本語のホスト", "a\tb\nc",
	} {
		var out bytes.Buffer
		w := &safeWriter{w: &out}
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != s {
			t.Errorf("wrote %q, want %q", got, s)
		}
	}
}

// io.Writer's contract: the count returned is of the bytes it was given, not of
// the bytes it chose to emit. tabwriter and fmt both check it.
func TestTheSafeWriterReportsTheLengthItWasGiven(t *testing.T) {
	var out bytes.Buffer
	w := &safeWriter{w: &out}
	in := []byte("a\x1b[2Jb")
	n, err := w.Write(in)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(in) {
		t.Errorf("reported %d bytes written for an input of %d", n, len(in))
	}
	if out.Len() <= len(in) {
		t.Errorf("the escape was not expanded: %q", out.String())
	}
}

func seedHostile(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	b := event.NewBuilder("obs", nil)
	// Three routes into the output: the subject an event is about, an
	// attribute rendered in the detail column, and the interface name the
	// narrative puts in a sentence.
	link := b.New(event.SourceNetlink, event.KindLinkDown, event.SevWarn, event.Iface(payload, 3))
	link.TSWall = time.Now().Add(-time.Minute).UnixNano()
	link.WithAttr("cause", "administrative").WithAttr("ifname", payload)

	dns := b.New(event.SourceDNS, event.KindDNSResolverChanged, event.SevWarn,
		event.Host(payload, ""))
	dns.TSWall = time.Now().Add(-30 * time.Second).UnixNano()
	dns.WithAttr("client", payload).
		WithAttr("resolver_old", "10.0.0.1").
		WithAttr("resolver_new", payload)

	if err := st.Append(context.Background(), link, dns); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsControl(s string) bool { return firstControl(s) != "" }

// firstControl returns the offending run, for a message that says what went
// wrong rather than only that something did.
func firstControl(s string) string {
	for i, r := range s {
		switch {
		case r == '\t' || r == '\n':
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			end := i + 12
			if end > len(s) {
				end = len(s)
			}
			return strings.TrimSpace(s[i:end])
		}
	}
	return ""
}
