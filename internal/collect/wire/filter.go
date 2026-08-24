package wire

import "golang.org/x/net/bpf"

// captureFilter is the kernel-side filter: IPv4, and then ICMP, or UDP on one
// of the three ports this collector understands.
//
// Written as instructions rather than a tcpdump expression compiled at runtime,
// because a filter is a security boundary. It decides what this process is ever
// handed, and something that decides that should be readable and reviewable in
// the repository rather than assembled from a string.
//
// The jump distances are *computed*, not counted by hand. The first version of
// this was hand-counted, the kernel rejected it with "invalid argument", and
// the collector failed at startup on every run - which is exactly how a
// hand-counted jump table fails: silently at review, loudly and late. There is
// a test that runs this program against real frames in a userspace VM.
func captureFilter() []bpf.Instruction {
	const (
		ethTypeOff  = 12
		ipStart     = ethHeaderLen              // 14
		protoOff    = ipStart + 9               // 23
		fragOff     = ipStart + 6               // 20
		vlanIPStart = ethHeaderLen + vlanTagLen // 18
		vlanTypeOff = ethTypeOff + vlanTagLen   // 16
		vlanProto   = vlanIPStart + 9           // 27

		accept = 0xffff
		reject = 0
	)

	b := newProgram()

	// ---- ethertype ------------------------------------------------------
	b.add(bpf.LoadAbsolute{Off: ethTypeOff, Size: 2})
	b.jumpTo(bpf.JumpEqual, ethTypeIPv4, "ipv4", "")
	b.jumpTo(bpf.JumpEqual, ethTypeVLAN, "vlan", "drop")

	// ---- untagged IPv4 --------------------------------------------------
	b.label("ipv4")
	b.add(bpf.LoadAbsolute{Off: protoOff, Size: 1})
	b.jumpTo(bpf.JumpEqual, protoICMP, "accept", "")
	b.jumpTo(bpf.JumpEqual, protoUDP, "", "drop")

	// A fragment after the first carries no ports to inspect.
	b.add(bpf.LoadAbsolute{Off: fragOff, Size: 2})
	b.jumpTo(bpf.JumpBitsSet, 0x1fff, "drop", "")

	// X = IP header length, so options do not shift the ports out from under
	// the offsets below.
	b.add(bpf.LoadMemShift{Off: ipStart})
	b.add(bpf.LoadIndirect{Off: ipStart, Size: 2}) // source port
	b.jumpTo(bpf.JumpEqual, dhcpServerPort, "accept", "")
	b.jumpTo(bpf.JumpEqual, dhcpClientPort, "accept", "")
	b.jumpTo(bpf.JumpEqual, dnsPort, "accept", "")
	b.add(bpf.LoadIndirect{Off: ipStart + 2, Size: 2}) // destination port
	b.jumpTo(bpf.JumpEqual, dhcpServerPort, "accept", "")
	b.jumpTo(bpf.JumpEqual, dhcpClientPort, "accept", "")
	b.jumpTo(bpf.JumpEqual, dnsPort, "accept", "drop")

	// ---- one VLAN tag ---------------------------------------------------
	//
	// Tagged UDP is accepted without a port check and filtered in Go.
	// Repeating the whole port ladder against shifted offsets would double the
	// program for a small fraction of any segment's traffic, and every extra
	// hand-maintained offset is another chance to be wrong.
	b.label("vlan")
	b.add(bpf.LoadAbsolute{Off: vlanTypeOff, Size: 2})
	b.jumpTo(bpf.JumpEqual, ethTypeIPv4, "", "drop")
	b.add(bpf.LoadAbsolute{Off: vlanProto, Size: 1})
	b.jumpTo(bpf.JumpEqual, protoICMP, "accept", "")
	b.jumpTo(bpf.JumpEqual, protoUDP, "accept", "drop")

	b.label("accept")
	b.add(bpf.RetConstant{Val: accept})
	b.label("drop")
	b.add(bpf.RetConstant{Val: reject})

	return b.resolve()
}

/* ------------------------------------------------------------------ */
/* A tiny assembler                                                    */
/* ------------------------------------------------------------------ */

// program collects instructions and patches jump distances once every label's
// position is known. Classic BPF jumps forward by a count of instructions, and
// a count is the one thing a human should never be asked to maintain by hand.
type program struct {
	ins    []bpf.Instruction
	labels map[string]int
	fixups []fixup
}

type fixup struct {
	at         int
	trueLabel  string
	falseLabel string
}

func newProgram() *program {
	return &program{labels: make(map[string]int)}
}

func (p *program) add(i bpf.Instruction) { p.ins = append(p.ins, i) }

// label marks the position the next instruction will occupy.
func (p *program) label(name string) { p.labels[name] = len(p.ins) }

// jumpTo adds a conditional jump. An empty label means "fall through", which is
// a skip of zero.
func (p *program) jumpTo(cond bpf.JumpTest, val uint32, whenTrue, whenFalse string) {
	p.fixups = append(p.fixups, fixup{at: len(p.ins), trueLabel: whenTrue, falseLabel: whenFalse})
	p.add(bpf.JumpIf{Cond: cond, Val: val})
}

// resolve patches every jump and returns the finished program.
func (p *program) resolve() []bpf.Instruction {
	for _, f := range p.fixups {
		j := p.ins[f.at].(bpf.JumpIf)
		j.SkipTrue = p.distance(f.at, f.trueLabel)
		j.SkipFalse = p.distance(f.at, f.falseLabel)
		p.ins[f.at] = j
	}
	return p.ins
}

func (p *program) distance(from int, label string) uint8 {
	if label == "" {
		return 0 // fall through to the next instruction
	}
	target, ok := p.labels[label]
	if !ok {
		// A label that does not exist is a programming error, and a filter
		// that silently jumps to zero would accept or drop everything. Panic
		// here rather than at 3am on somebody's network: this runs at startup
		// and is covered by a test.
		panic("wire: filter jumps to unknown label " + label)
	}
	skip := target - from - 1
	if skip < 0 || skip > 255 {
		panic("wire: filter jump out of range to " + label)
	}
	return uint8(skip)
}
