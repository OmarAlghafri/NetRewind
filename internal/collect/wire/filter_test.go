package wire

import (
	"net"
	"testing"

	"golang.org/x/net/bpf"
)

// runFilter executes the capture filter against a frame in a userspace VM.
//
// The kernel's only feedback on a bad filter is "invalid argument" at attach
// time, and its only feedback on a *wrong* filter is a collector that quietly
// sees nothing. Running the same program here gives both a real answer.
func runFilter(t *testing.T, frame []byte) int {
	t.Helper()
	vm, err := bpf.NewVM(captureFilter())
	if err != nil {
		t.Fatalf("the filter does not assemble: %v", err)
	}
	n, err := vm.Run(frame)
	if err != nil {
		t.Fatalf("filter run: %v", err)
	}
	return n
}

func TestFilterAssembles(t *testing.T) {
	// The same check the kernel makes at attach time. The first version of this
	// filter had hand-counted jumps and was rejected with "invalid argument",
	// which the collector only discovered at startup.
	if _, err := bpf.Assemble(captureFilter()); err != nil {
		t.Fatalf("the filter would be rejected by the kernel: %v", err)
	}
}

func TestFilterAcceptsWhatTheCollectorNeeds(t *testing.T) {
	cases := map[string][]byte{
		"DHCP server to client": buildFrame(frameOpts{
			srcPort: dhcpServerPort, dstPort: dhcpClientPort, payload: make([]byte, 300)}),
		"DHCP client to server": buildFrame(frameOpts{
			srcPort: dhcpClientPort, dstPort: dhcpServerPort, payload: make([]byte, 300)}),
		"DNS query": buildFrame(frameOpts{
			srcPort: 45000, dstPort: dnsPort, payload: make([]byte, 40)}),
		"DNS response": buildFrame(frameOpts{
			srcPort: dnsPort, dstPort: 45000, payload: make([]byte, 40)}),
		"ICMP": buildFrame(frameOpts{
			proto: protoICMP, payload: make([]byte, 40)}),
		"VLAN-tagged DHCP": buildFrame(frameOpts{
			vlan: 20, srcPort: dhcpServerPort, dstPort: dhcpClientPort, payload: make([]byte, 300)}),
		"VLAN-tagged ICMP": buildFrame(frameOpts{
			vlan: 20, proto: protoICMP, payload: make([]byte, 40)}),
	}
	for name, frame := range cases {
		if n := runFilter(t, frame); n == 0 {
			t.Errorf("%s was filtered out", name)
		}
	}
}

// Everything the collector does not need must be dropped in the kernel. A
// filter that lets the rest through turns the recorder into a process that
// spends its day discarding video traffic.
func TestFilterRejectsEverythingElse(t *testing.T) {
	tcp := buildFrame(frameOpts{proto: 6, payload: make([]byte, 40)})

	ipv6 := buildFrame(frameOpts{srcPort: dnsPort, dstPort: 45000, payload: make([]byte, 40)})
	ipv6[12], ipv6[13] = 0x86, 0xdd

	arp := buildFrame(frameOpts{srcPort: dnsPort, dstPort: 45000, payload: make([]byte, 40)})
	arp[12], arp[13] = 0x08, 0x06

	cases := map[string][]byte{
		"unrelated UDP": buildFrame(frameOpts{
			srcPort: 40000, dstPort: 12345, payload: make([]byte, 200)}),
		"TCP":  tcp,
		"IPv6": ipv6,
		"ARP":  arp,
	}
	for name, frame := range cases {
		if n := runFilter(t, frame); n != 0 {
			t.Errorf("%s was accepted (returned %d)", name, n)
		}
	}
}

// An IP header with options shifts the ports. A filter with a fixed offset
// reads two bytes of the options as a port number and silently stops matching.
func TestFilterHandlesIPOptions(t *testing.T) {
	frame := buildFrame(frameOpts{srcPort: dnsPort, dstPort: 45000, payload: make([]byte, 40)})

	// Rebuild with a 24-byte IP header: IHL 6, four bytes of options.
	withOptions := make([]byte, 0, len(frame)+4)
	withOptions = append(withOptions, frame[:14]...) // ethernet
	ip := append([]byte(nil), frame[14:34]...)
	ip[0] = 0x46 // IHL = 6 words
	ip[2], ip[3] = 0, byte(24+len(frame)-34)
	withOptions = append(withOptions, ip...)
	withOptions = append(withOptions, 0x01, 0x01, 0x01, 0x00) // four NOP/EOL option bytes
	withOptions = append(withOptions, frame[34:]...)          // UDP and payload

	if n := runFilter(t, withOptions); n == 0 {
		t.Error("a DNS packet with IP options was filtered out")
	}
}

// A fragment after the first has no transport header, so its "ports" are
// whatever the payload happens to contain.
func TestFilterRejectsLaterFragments(t *testing.T) {
	frame := buildFrame(frameOpts{srcPort: dnsPort, dstPort: 45000, payload: make([]byte, 40)})
	frame[20], frame[21] = 0x00, 0x20 // fragment offset 32

	if n := runFilter(t, frame); n != 0 {
		t.Error("a later fragment was accepted; its ports are payload bytes")
	}
}

// The filter is the first thing that touches attacker-controlled bytes.
func TestFilterSurvivesShortFrames(t *testing.T) {
	full := buildFrame(frameOpts{srcPort: dnsPort, dstPort: 45000, payload: make([]byte, 40)})
	vm, err := bpf.NewVM(captureFilter())
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	for n := 0; n < len(full); n++ {
		// A frame too short for an offset the program reads makes the VM
		// return an error; what must not happen is a panic.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("length %d panicked the filter: %v", n, r)
				}
			}()
			_, _ = vm.Run(full[:n])
		}()
	}
}

// An unknown label would silently become a jump of zero, which accepts or drops
// everything depending on where it lands.
func TestFilterAssemblerRefusesAnUnknownLabel(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a jump to a label that does not exist was allowed")
		}
	}()
	p := newProgram()
	p.jumpTo(bpf.JumpEqual, 1, "nowhere", "")
	p.resolve()
}

func TestFilterMatchesTheParser(t *testing.T) {
	// Anything the filter accepts, the parser must be able to decode - or the
	// collector wakes for frames it then throws away.
	frames := [][]byte{
		buildFrame(frameOpts{srcPort: dhcpServerPort, dstPort: dhcpClientPort, payload: make([]byte, 300)}),
		buildFrame(frameOpts{srcPort: 45000, dstPort: dnsPort, payload: make([]byte, 40)}),
		buildFrame(frameOpts{proto: protoICMP, payload: make([]byte, 40)}),
		buildFrame(frameOpts{vlan: 20, srcPort: dnsPort, dstPort: 45000, payload: make([]byte, 40)}),
	}
	for i, f := range frames {
		if runFilter(t, f) == 0 {
			continue // the filter dropped it, so the parser never sees it
		}
		if _, err := Parse(f); err != nil {
			t.Errorf("frame %d passed the filter but the parser rejected it: %v", i, err)
		}
	}
}

func TestFilterAcceptsARealisticDHCPFrame(t *testing.T) {
	// End to end: the exact bytes the lab injector sends.
	payload := buildDHCP(dhcpOffer, net.HardwareAddr{0x02, 0, 0, 0, 0, 0x55},
		net.IPv4(10, 99, 0, 77), net.IPv4(10, 99, 0, 99), net.IPv4(10, 99, 0, 99), 0)
	frame := buildFrame(frameOpts{
		srcMAC:  net.HardwareAddr{0x02, 0, 0, 0, 0, 0x99},
		srcIP:   net.IPv4(10, 99, 0, 99),
		srcPort: dhcpServerPort, dstPort: dhcpClientPort,
		payload: payload,
	})

	if runFilter(t, frame) == 0 {
		t.Fatal("a real DHCP offer was filtered out")
	}
	p, err := Parse(frame)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := ParseDHCP(p.Payload); err != nil {
		t.Fatalf("ParseDHCP: %v", err)
	}
}
