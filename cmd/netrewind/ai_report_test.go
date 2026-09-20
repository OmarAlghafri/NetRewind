package main

import (
	"strings"
	"testing"
)

// The command's whole job is a single shared internal/redact.Redactor over
// the entire stdin document - proven here by a value appearing twice
// getting the SAME placeholder both times, which a naive per-line or
// per-call redaction (a new Redactor each time) would not guarantee.
func TestAIReportCommandRedactsPrivateAddressesConsistently(t *testing.T) {
	// Two distinct addresses, the second repeated on its own line: a
	// per-section (here, per-line) Redactor would reset its numbering on
	// that second line and mis-assign 10.99.0.201 <HOST_1> there instead
	// of reusing the <HOST_2> it was already given on the first line.
	stdin := "Engine facts: gateway 10.99.0.1 is now answered by 10.99.0.201.\n" +
		"Model text: 10.99.0.201 took over the gateway."
	out, err := runWithStdin(t, stdin, "ai", "report")
	if err != nil {
		t.Fatalf("unexpected error: %v (stdout: %s)", err, out)
	}
	if strings.Contains(out, "10.99.0.1") || strings.Contains(out, "10.99.0.201") {
		t.Fatalf("output = %q, still contains a real address", out)
	}
	lines := strings.SplitN(out, "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("output = %q, want two lines", out)
	}
	if !strings.Contains(lines[0], "<HOST_2>") {
		t.Fatalf("first line = %q, want the second address to be <HOST_2>", lines[0])
	}
	if !strings.Contains(lines[1], "<HOST_2>") || strings.Contains(lines[1], "<HOST_1>") {
		t.Fatalf("second line = %q, want it to reuse <HOST_2> (the same placeholder as line one), not mint a new one", lines[1])
	}
}

func TestAIReportCommandRedactsMACAddresses(t *testing.T) {
	out, err := runWithStdin(t, "moved to AA:BB:CC:DD:EE:FF", "ai", "report")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "AA:BB:CC:DD:EE:FF") {
		t.Fatalf("output = %q, still contains the real MAC", out)
	}
	if !strings.Contains(out, "<MAC_1>") {
		t.Fatalf("output = %q, want it to contain <MAC_1>", out)
	}
}

func TestAIReportCommandPassesThroughTextWithNoAddresses(t *testing.T) {
	out, err := runWithStdin(t, "nothing sensitive here", "ai", "report")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "nothing sensitive here" {
		t.Fatalf("out = %q, want the text unchanged", out)
	}
}
