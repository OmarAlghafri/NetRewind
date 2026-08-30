package main

import (
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// The record contains strings this project did not choose.
//
// An interface name comes from whoever created the interface - and the kernel
// accepts an escape character in one. A DNS name is whatever a machine on the
// watched segment looked up. nftables rule text is arbitrary. All of it is read
// back by an engineer in a terminal, and a terminal treats some of those bytes
// as instructions rather than as text: ESC [ 2 J clears the screen, ESC [ 1;1 H
// moves the cursor to the top of it, and a carriage return overwrites the line
// that was just printed.
//
// So a machine that can put a name on this network can decide what the person
// investigating it sees. On a tool whose whole claim is that the record can be
// trusted, that is the worst shape a bug can take: the store is intact, the
// query is right, and the account on the screen is a lie written by the
// subject of the investigation.
//
// Everything the CLI prints therefore goes through here. Not the store - the
// store is evidence and keeps exactly what arrived - and not the JSON output's
// content, which encoding/json already escapes. Only the last step, where bytes
// become something a terminal acts on.

// safeOut wraps a command's output so no byte from the record can be read by a
// terminal as a command. Every place the CLI writes uses it rather than
// cmd.OutOrStdout, so a command added later cannot forget.
func safeOut(cmd *cobra.Command) io.Writer { return &safeWriter{w: cmd.OutOrStdout()} }

// safeWriter passes text through and renders anything a terminal would act on
// as \xNN, so the operator sees what was actually recorded.
//
// Tab and newline are the two control characters this tool writes itself, and
// they are the only ones let through. Everything else in C0, DEL, and the C1
// range that a terminal may treat as an escape introducer is shown rather than
// executed.
type safeWriter struct{ w io.Writer }

func (s *safeWriter) Write(p []byte) (int, error) {
	if !needsEscaping(p) {
		return s.w.Write(p)
	}
	// Written into a buffer first so one hostile row cannot turn into a
	// hundred small writes.
	buf := make([]byte, 0, len(p)+16)
	for i := 0; i < len(p); {
		b := p[i]
		if b < utf8.RuneSelf {
			if unsafeByte(b) {
				buf = append(buf, fmt.Sprintf("\\x%02x", b)...)
			} else {
				buf = append(buf, b)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRune(p[i:])
		if r == utf8.RuneError && size == 1 {
			// Not valid UTF-8. Show the byte rather than emitting it, because
			// what a terminal does with a lone high byte depends on its
			// encoding, and none of the answers are useful here.
			buf = append(buf, fmt.Sprintf("\\x%02x", b)...)
			i++
			continue
		}
		if r >= 0x80 && r <= 0x9f {
			// C1. Some terminals treat these as single-byte equivalents of the
			// two-byte ESC sequences.
			buf = append(buf, fmt.Sprintf("\\u%04x", r)...)
			i += size
			continue
		}
		buf = append(buf, p[i:i+size]...)
		i += size
	}
	if _, err := s.w.Write(buf); err != nil {
		return 0, err
	}
	// The caller is told its own bytes were taken. Reporting the expanded
	// length would break io.Writer's contract and every wrapper above it.
	return len(p), nil
}

// unsafeByte reports whether an ASCII byte is one a terminal acts on.
func unsafeByte(b byte) bool {
	switch b {
	case '\t', '\n':
		return false
	}
	return b < 0x20 || b == 0x7f
}

// needsEscaping is the fast path: almost everything the CLI prints is ordinary
// text, and rebuilding a buffer for it would be work done for nothing.
func needsEscaping(p []byte) bool {
	for _, b := range p {
		if b < utf8.RuneSelf {
			if unsafeByte(b) {
				return true
			}
			continue
		}
		if b == 0xc2 {
			return true // leads U+0080..U+00BF, which is where C1 lives
		}
	}
	return !utf8.Valid(p)
}
