package wire

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

/* ------------------------------------------------------------------ */
/* Frame construction                                                 */
/* ------------------------------------------------------------------ */

type frameOpts struct {
	srcMAC, dstMAC net.HardwareAddr
	vlan           uint16
	srcIP, dstIP   net.IP
	proto          uint8
	srcPort        uint16
	dstPort        uint16
	payload        []byte
}

// buildFrame assembles a real Ethernet/IPv4/UDP frame so the parser is tested
// against bytes of the shape it will actually see, not a struct handed to it.
func buildFrame(o frameOpts) []byte {
	if o.srcMAC == nil {
		o.srcMAC = net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}
	}
	if o.dstMAC == nil {
		o.dstMAC = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	}
	if o.srcIP == nil {
		o.srcIP = net.IPv4(10, 0, 0, 1)
	}
	if o.dstIP == nil {
		o.dstIP = net.IPv4(255, 255, 255, 255)
	}
	if o.proto == 0 {
		o.proto = protoUDP
	}

	var eth []byte
	eth = append(eth, o.dstMAC...)
	eth = append(eth, o.srcMAC...)
	if o.vlan != 0 {
		eth = append(eth, 0x81, 0x00)
		eth = append(eth, byte(o.vlan>>8), byte(o.vlan))
	}
	eth = append(eth, 0x08, 0x00) // IPv4

	transport := o.payload
	if o.proto == protoUDP {
		udp := make([]byte, udpHeaderLen)
		binary.BigEndian.PutUint16(udp[0:2], o.srcPort)
		binary.BigEndian.PutUint16(udp[2:4], o.dstPort)
		binary.BigEndian.PutUint16(udp[4:6], uint16(udpHeaderLen+len(o.payload)))
		transport = append(udp, o.payload...)
	}

	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(20+len(transport)))
	ip[8] = 64
	ip[9] = o.proto
	copy(ip[12:16], o.srcIP.To4())
	copy(ip[16:20], o.dstIP.To4())

	frame := append(eth, ip...)
	return append(frame, transport...)
}

func buildDHCP(msgType byte, clientMAC net.HardwareAddr, yourIP, serverID, router net.IP, lease time.Duration) []byte {
	p := make([]byte, dhcpMinLen)
	p[0] = 2 // BOOTREPLY
	p[1] = 1 // ethernet
	p[2] = 6 // hlen
	if yourIP != nil {
		copy(p[16:20], yourIP.To4())
	}
	if clientMAC != nil {
		copy(p[28:34], clientMAC)
	}
	binary.BigEndian.PutUint32(p[236:240], dhcpMagic)

	p = append(p, optMessageType, 1, msgType)
	if serverID != nil {
		p = append(p, optServerID, 4)
		p = append(p, serverID.To4()...)
	}
	if router != nil {
		p = append(p, optRouter, 4)
		p = append(p, router.To4()...)
	}
	if lease > 0 {
		p = append(p, optLeaseTime, 4, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(p[len(p)-4:], uint32(lease.Seconds()))
	}
	return append(p, optEnd)
}

func buildDNS(id uint16, response bool, rcode uint8, name string) []byte {
	p := make([]byte, 12)
	binary.BigEndian.PutUint16(p[0:2], id)
	flags := uint16(rcode)
	if response {
		flags |= 0x8000
	}
	binary.BigEndian.PutUint16(p[2:4], flags)
	binary.BigEndian.PutUint16(p[4:6], 1) // one question

	for _, label := range splitName(name) {
		p = append(p, byte(len(label)))
		p = append(p, label...)
	}
	p = append(p, 0)
	p = append(p, 0, 1, 0, 1) // A, IN
	return p
}

func splitName(name string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if i > start {
				out = append(out, name[start:i])
			}
			start = i + 1
		}
	}
	return out
}

/* ------------------------------------------------------------------ */
/* Parsing                                                            */
/* ------------------------------------------------------------------ */

func TestParseUDPFrame(t *testing.T) {
	frame := buildFrame(frameOpts{
		srcIP: net.IPv4(10, 0, 0, 5), dstIP: net.IPv4(10, 0, 0, 1),
		srcPort: 45000, dstPort: 53, payload: []byte("hello"),
	})

	p, err := Parse(frame)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !p.SrcIP.Equal(net.IPv4(10, 0, 0, 5)) || !p.DstIP.Equal(net.IPv4(10, 0, 0, 1)) {
		t.Errorf("addresses = %v -> %v", p.SrcIP, p.DstIP)
	}
	if p.SrcPort != 45000 || p.DstPort != 53 {
		t.Errorf("ports = %d -> %d", p.SrcPort, p.DstPort)
	}
	if string(p.Payload) != "hello" {
		t.Errorf("payload = %q", p.Payload)
	}
}

func TestParseVLANTaggedFrame(t *testing.T) {
	frame := buildFrame(frameOpts{vlan: 20, srcPort: 67, dstPort: 68, payload: []byte("x")})

	p, err := Parse(frame)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.VLAN != 20 {
		t.Errorf("VLAN = %d, want 20", p.VLAN)
	}
	if p.SrcPort != 67 {
		t.Errorf("ports were misread past the tag: %d", p.SrcPort)
	}
}

// Ethernet pads frames shorter than 60 bytes. The padding is not payload, and
// a parser that treats it as payload hands garbage to every decoder above it.
func TestPaddingIsNotPayload(t *testing.T) {
	frame := buildFrame(frameOpts{srcPort: 53, dstPort: 45000, payload: []byte("ab")})
	padded := append(frame, make([]byte, 40)...)

	p, err := Parse(padded)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if string(p.Payload) != "ab" {
		t.Errorf("payload = %q, want the two real bytes", p.Payload)
	}
}

// Everything here arrives from the network and can be crafted by anyone on the
// segment. A truncated or lying header must produce an error, never a panic.
func TestMalformedFramesAreRejectedNotFatal(t *testing.T) {
	good := buildFrame(frameOpts{srcPort: 67, dstPort: 68, payload: []byte("payload")})

	cases := map[string][]byte{
		"empty":              {},
		"ethernet only":      good[:14],
		"truncated ip":       good[:20],
		"truncated udp":      good[:36],
		"nothing but zeroes": make([]byte, 64),
	}
	for name, frame := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: parser panicked: %v", name, r)
				}
			}()
			_, _ = Parse(frame)
		}()
	}

	// A header claiming an IHL longer than the frame must not slice out of range.
	lying := append([]byte(nil), good...)
	lying[14] = 0x4f // IHL = 15 words = 60 bytes
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("a lying IHL panicked the parser: %v", r)
			}
		}()
		if _, err := Parse(lying); err == nil {
			t.Error("a frame claiming a 60-byte IP header inside a short frame was accepted")
		}
	}()
}

func TestNonIPv4IsIgnored(t *testing.T) {
	frame := buildFrame(frameOpts{srcPort: 53, dstPort: 53, payload: []byte("x")})
	frame[12], frame[13] = 0x86, 0xdd // IPv6

	if _, err := Parse(frame); err == nil {
		t.Error("an IPv6 frame was accepted by an IPv4-only parser")
	}
}

/* ------------------------------------------------------------------ */
/* DHCP                                                               */
/* ------------------------------------------------------------------ */

func dhcpPacket(t *testing.T, serverIP net.IP, serverMAC net.HardwareAddr, payload []byte) (Packet, DHCPMessage) {
	t.Helper()
	frame := buildFrame(frameOpts{
		srcMAC: serverMAC, srcIP: serverIP,
		srcPort: dhcpServerPort, dstPort: dhcpClientPort, payload: payload,
	})
	p, err := Parse(frame)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	m, err := ParseDHCP(p.Payload)
	if err != nil {
		t.Fatalf("ParseDHCP: %v", err)
	}
	return p, m
}

func TestParseDHCPOptions(t *testing.T) {
	client := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x55}
	payload := buildDHCP(dhcpOffer, client,
		net.IPv4(10, 0, 0, 50), net.IPv4(10, 0, 0, 1), net.IPv4(10, 0, 0, 254), 12*time.Hour)

	m, err := ParseDHCP(payload)
	if err != nil {
		t.Fatalf("ParseDHCP: %v", err)
	}
	if m.Type != dhcpOffer {
		t.Errorf("type = %d", m.Type)
	}
	if m.ClientMAC.String() != client.String() {
		t.Errorf("client mac = %v", m.ClientMAC)
	}
	if !m.YourIP.Equal(net.IPv4(10, 0, 0, 50)) {
		t.Errorf("offered address = %v", m.YourIP)
	}
	if !m.ServerID.Equal(net.IPv4(10, 0, 0, 1)) {
		t.Errorf("server id = %v", m.ServerID)
	}
	if !m.Router.Equal(net.IPv4(10, 0, 0, 254)) {
		t.Errorf("router = %v", m.Router)
	}
	if m.LeaseTime != 12*time.Hour {
		t.Errorf("lease = %v", m.LeaseTime)
	}
}

// An option length that runs past the end of the packet is the obvious way to
// attack a DHCP parser.
func TestDHCPOptionOverrunIsBounded(t *testing.T) {
	payload := make([]byte, dhcpMinLen)
	binary.BigEndian.PutUint32(payload[236:240], dhcpMagic)
	payload = append(payload, optServerID, 200, 1, 2, 3) // claims 200 bytes, has 3

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("an overlong option panicked the parser: %v", r)
		}
	}()
	m, err := ParseDHCP(payload)
	if err != nil {
		t.Fatalf("ParseDHCP: %v", err)
	}
	if m.ServerID != nil {
		t.Error("a truncated option was read anyway")
	}
}

func TestDHCPRejectsNonDHCP(t *testing.T) {
	if _, err := ParseDHCP(make([]byte, 100)); err == nil {
		t.Error("a short payload was accepted as DHCP")
	}
	if _, err := ParseDHCP(make([]byte, 400)); err == nil {
		t.Error("a payload without the magic cookie was accepted as DHCP")
	}
}

// The event this whole collector exists for.
func TestASecondDHCPServerIsAnError(t *testing.T) {
	clk := &clock{t: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
	w := NewDHCPWatcher(event.NewBuilder("obs", nil), clk.now)
	client := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x55}

	// The legitimate server. First sighting: worth noting, not an alarm.
	p, m := dhcpPacket(t, net.IPv4(10, 0, 0, 1), net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01},
		buildDHCP(dhcpAck, client, net.IPv4(10, 0, 0, 50), net.IPv4(10, 0, 0, 1), nil, time.Hour))
	first := findKind(w.Observe(p, m), event.KindDHCPServerSeen)
	if first == nil {
		t.Fatal("the first server was not reported at all")
	}
	if first.Severity != event.SevNotice {
		t.Errorf("the only server on a segment is severity %s, want notice", first.Severity)
	}

	// A second server answering on the same segment. This is the fault.
	clk.advance(time.Minute)
	p, m = dhcpPacket(t, net.IPv4(10, 0, 0, 99), net.HardwareAddr{0x02, 0, 0, 0, 0, 0x99},
		buildDHCP(dhcpOffer, client, net.IPv4(10, 0, 0, 77), net.IPv4(10, 0, 0, 99),
			net.IPv4(10, 0, 0, 99), time.Hour))
	second := findKind(w.Observe(p, m), event.KindDHCPServerSeen)
	if second == nil {
		t.Fatal("a second DHCP server was not reported")
	}
	if second.Severity != event.SevError {
		t.Errorf("a second server is severity %s, want error", second.Severity)
	}
	if n, _ := event.Int(second, "other_servers"); n != 1 {
		t.Errorf("other_servers = %d, want 1", n)
	}
	if got := event.Str(second, "offers_gateway"); got != "10.0.0.99" {
		t.Errorf("the gateway it was handing out is not recorded: %q", got)
	}
}

func TestAKnownServerIsNotReportedRepeatedly(t *testing.T) {
	clk := &clock{t: time.Now()}
	w := NewDHCPWatcher(event.NewBuilder("obs", nil), clk.now)
	client := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x55}

	for i := 0; i < 5; i++ {
		clk.advance(time.Minute)
		p, m := dhcpPacket(t, net.IPv4(10, 0, 0, 1), net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01},
			buildDHCP(dhcpAck, client, net.IPv4(10, 0, 0, 50), net.IPv4(10, 0, 0, 1), nil, time.Hour))
		got := findKind(w.Observe(p, m), event.KindDHCPServerSeen)
		if i > 0 && got != nil {
			t.Fatalf("the same server was reported again on sighting %d", i+1)
		}
	}
}

// A client cannot make itself a server by claiming to be one: only the server
// port counts.
func TestAClientCannotRegisterItselfAsAServer(t *testing.T) {
	w := NewDHCPWatcher(event.NewBuilder("obs", nil), nil)
	payload := buildDHCP(dhcpOffer, net.HardwareAddr{0x02, 0, 0, 0, 0, 0x55},
		net.IPv4(10, 0, 0, 50), net.IPv4(10, 0, 0, 66), nil, time.Hour)

	frame := buildFrame(frameOpts{
		srcIP: net.IPv4(10, 0, 0, 66), srcPort: dhcpClientPort, dstPort: dhcpServerPort,
		payload: payload,
	})
	p, err := Parse(frame)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseDHCP(p.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if findKind(w.Observe(p, m), event.KindDHCPServerSeen) != nil {
		t.Error("a message from the client port registered a server")
	}
}

func TestDHCPLeaseChangeIsReported(t *testing.T) {
	clk := &clock{t: time.Now()}
	w := NewDHCPWatcher(event.NewBuilder("obs", nil), clk.now)
	client := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x55}
	server := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01}

	p, m := dhcpPacket(t, net.IPv4(10, 0, 0, 1), server,
		buildDHCP(dhcpAck, client, net.IPv4(10, 0, 0, 50), net.IPv4(10, 0, 0, 1), nil, time.Hour))
	if findKind(w.Observe(p, m), event.KindDHCPLeaseChanged) != nil {
		t.Fatal("a first lease was reported as a change")
	}

	clk.advance(time.Hour)
	p, m = dhcpPacket(t, net.IPv4(10, 0, 0, 1), server,
		buildDHCP(dhcpAck, client, net.IPv4(10, 0, 0, 77), net.IPv4(10, 0, 0, 1), nil, time.Hour))
	changed := findKind(w.Observe(p, m), event.KindDHCPLeaseChanged)
	if changed == nil {
		t.Fatal("a client taking a different address was not reported")
	}
	if event.Str(changed, "address_old") != "10.0.0.50" || event.Str(changed, "address_new") != "10.0.0.77" {
		t.Errorf("the change does not say what moved: %v", changed.Attrs)
	}
}

/* ------------------------------------------------------------------ */
/* DNS                                                                */
/* ------------------------------------------------------------------ */

func dnsPacket(t *testing.T, src, dst net.IP, srcPort, dstPort uint16, payload []byte) (Packet, DNSMessage) {
	t.Helper()
	frame := buildFrame(frameOpts{srcIP: src, dstIP: dst, srcPort: srcPort, dstPort: dstPort, payload: payload})
	p, err := Parse(frame)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	m, err := ParseDNS(p.Payload)
	if err != nil {
		t.Fatalf("ParseDNS: %v", err)
	}
	return p, m
}

func TestParseDNSQuestion(t *testing.T) {
	m, err := ParseDNS(buildDNS(0x1234, false, 0, "www.example.com"))
	if err != nil {
		t.Fatalf("ParseDNS: %v", err)
	}
	if m.ID != 0x1234 || m.Response {
		t.Errorf("header misread: %+v", m)
	}
	if m.Question != "www.example.com" {
		t.Errorf("question = %q", m.Question)
	}
}

// A label that points at itself loops forever in a parser that follows
// compression pointers without counting - and anyone on the segment can send
// one.
func TestDNSCompressionLoopTerminates(t *testing.T) {
	p := make([]byte, 12)
	binary.BigEndian.PutUint16(p[4:6], 1)
	p = append(p, 0xc0, 0x0c) // a pointer to offset 12, which is itself

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = ParseDNS(p)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a self-referential compression pointer did not terminate")
	}
}

func TestDNSMalformedIsSurvivable(t *testing.T) {
	for name, payload := range map[string][]byte{
		"empty":     {},
		"short":     make([]byte, 5),
		"truncated": append(make([]byte, 12), 0x05, 'a'),
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panicked: %v", name, r)
				}
			}()
			_, _ = ParseDNS(payload)
		}()
	}
}

// The event that matters: a client that was asking one resolver is now asking
// another.
func TestResolverChangeIsReported(t *testing.T) {
	clk := &clock{t: time.Now()}
	w := NewDNSWatcher(event.NewBuilder("obs", nil), clk.now, true)
	client := net.IPv4(10, 0, 0, 5)

	p, m := dnsPacket(t, client, net.IPv4(10, 0, 0, 1), 45000, dnsPort, buildDNS(1, false, 0, "example.com"))
	if len(w.Observe(p, m)) != 0 {
		t.Fatal("a first query reported a resolver change")
	}

	clk.advance(time.Minute)
	p, m = dnsPacket(t, client, net.IPv4(10, 0, 0, 99), 45001, dnsPort, buildDNS(2, false, 0, "example.com"))
	changed := findKind(w.Observe(p, m), event.KindDNSResolverChanged)
	if changed == nil {
		t.Fatal("a client switching resolver was not reported")
	}
	if event.Str(changed, "resolver_old") != "10.0.0.1" || event.Str(changed, "resolver_new") != "10.0.0.99" {
		t.Errorf("the change does not say what moved: %v", changed.Attrs)
	}
}

// NXDOMAIN means the resolver worked and the name does not exist. Recording it
// as a failure would bury the two rcodes that are network faults.
func TestOnlyResolverFailuresAreRecorded(t *testing.T) {
	w := NewDNSWatcher(event.NewBuilder("obs", nil), nil, true)
	resolver, client := net.IPv4(10, 0, 0, 1), net.IPv4(10, 0, 0, 5)

	p, m := dnsPacket(t, resolver, client, dnsPort, 45000, buildDNS(1, true, rcodeNXDomain, "nope.example"))
	if findKind(w.Observe(p, m), event.KindDNSQueryFail) != nil {
		t.Error("NXDOMAIN was recorded as a network failure")
	}

	p, m = dnsPacket(t, resolver, client, dnsPort, 45000, buildDNS(2, true, rcodeServFail, "x.example"))
	if findKind(w.Observe(p, m), event.KindDNSQueryFail) == nil {
		t.Error("SERVFAIL was not recorded")
	}
}

// There are networks where recording query names is not permitted, so the
// switch has to turn them off entirely rather than merely hide them.
func TestQueryNamesCanBeTurnedOffEntirely(t *testing.T) {
	w := NewDNSWatcher(event.NewBuilder("obs", nil), nil, false)
	p, m := dnsPacket(t, net.IPv4(10, 0, 0, 1), net.IPv4(10, 0, 0, 5), dnsPort, 45000,
		buildDNS(1, true, rcodeServFail, "secret.internal.example"))

	e := findKind(w.Observe(p, m), event.KindDNSQueryFail)
	if e == nil {
		t.Fatal("the failure was not recorded")
	}
	if name := event.Str(e, "name"); name != "" {
		t.Errorf("a query name was stored with recording disabled: %q", name)
	}
	for _, v := range e.Attrs {
		if s, ok := v.(string); ok && s == "secret.internal.example" {
			t.Error("the query name leaked into another attribute")
		}
	}
}

func TestSlowResolverIsReported(t *testing.T) {
	clk := &clock{t: time.Now()}
	w := NewDNSWatcher(event.NewBuilder("obs", nil), clk.now, true)
	resolver, client := net.IPv4(10, 0, 0, 1), net.IPv4(10, 0, 0, 5)

	p, m := dnsPacket(t, client, resolver, 45000, dnsPort, buildDNS(7, false, 0, "slow.example"))
	w.Observe(p, m)

	clk.advance(2 * time.Second)
	p, m = dnsPacket(t, resolver, client, dnsPort, 45000, buildDNS(7, true, 0, "slow.example"))
	spike := findKind(w.Observe(p, m), event.KindDNSLatencySpike)
	if spike == nil {
		t.Fatal("a two second lookup was not reported")
	}
	if ms, _ := event.Int(spike, "took_ms"); ms != 2000 {
		t.Errorf("took_ms = %d, want 2000", ms)
	}
}

// Transaction ids are sixteen bits and every client picks its own, so a table
// keyed on the id alone would attribute one client's answer to another.
func TestPendingQueriesAreKeyedPerClient(t *testing.T) {
	clk := &clock{t: time.Now()}
	w := NewDNSWatcher(event.NewBuilder("obs", nil), clk.now, true)
	resolver := net.IPv4(10, 0, 0, 1)

	// Two clients, same transaction id.
	for _, c := range []net.IP{net.IPv4(10, 0, 0, 5), net.IPv4(10, 0, 0, 6)} {
		p, m := dnsPacket(t, c, resolver, 45000, dnsPort, buildDNS(42, false, 0, "x.example"))
		w.Observe(p, m)
	}
	if len(w.pending) != 2 {
		t.Errorf("two clients with the same transaction id collapsed to %d entries", len(w.pending))
	}
}

func TestSweepDropsUnansweredQueries(t *testing.T) {
	clk := &clock{t: time.Now()}
	w := NewDNSWatcher(event.NewBuilder("obs", nil), clk.now, true)

	p, m := dnsPacket(t, net.IPv4(10, 0, 0, 5), net.IPv4(10, 0, 0, 1), 45000, dnsPort,
		buildDNS(1, false, 0, "never.answered"))
	w.Observe(p, m)
	if len(w.pending) != 1 {
		t.Fatal("the query was not recorded as pending")
	}

	clk.advance(time.Minute)
	w.Sweep()
	if len(w.pending) != 0 {
		t.Error("a resolver that stopped answering would grow the pending table without bound")
	}
}

/* ------------------------------------------------------------------ */
/* ICMP                                                               */
/* ------------------------------------------------------------------ */

func buildICMPUnreachable(code uint8, mtu uint16, origSrc, origDst net.IP, origPort uint16) []byte {
	p := make([]byte, 8)
	p[0] = icmpDestUnreachable
	p[1] = code
	binary.BigEndian.PutUint16(p[6:8], mtu)

	quoted := make([]byte, 28)
	quoted[0] = 0x45
	quoted[9] = protoUDP
	copy(quoted[12:16], origSrc.To4())
	copy(quoted[16:20], origDst.To4())
	binary.BigEndian.PutUint16(quoted[22:24], origPort)
	return append(p, quoted...)
}

// The fault this watcher is worth writing for: ping works, large transfers do
// not, and the report naming the smaller MTU is addressed to somebody else.
func TestFragmentationNeededBecomesAnMTUBlackhole(t *testing.T) {
	w := NewICMPWatcher(event.NewBuilder("obs", nil), nil)
	payload := buildICMPUnreachable(codeFragNeeded, 1400, net.IPv4(10, 0, 0, 5), net.IPv4(10, 0, 0, 9), 443)

	frame := buildFrame(frameOpts{proto: protoICMP, srcIP: net.IPv4(10, 0, 0, 254), payload: payload})
	p, err := Parse(frame)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	m, err := ParseICMP(p.Payload)
	if err != nil {
		t.Fatalf("ParseICMP: %v", err)
	}

	e := findKind(w.Observe(p, m), event.KindMTUBlackhole)
	if e == nil {
		t.Fatal("fragmentation-needed was not reported as an MTU blackhole")
	}
	if mtu, _ := event.Int(e, "next_hop_mtu"); mtu != 1400 {
		t.Errorf("next_hop_mtu = %d, want 1400", mtu)
	}
	if event.Str(e, "destination") != "10.0.0.9" {
		t.Errorf("the destination that cannot be reached is not named: %v", e.Attrs)
	}
}

func TestAdministrativelyProhibitedIsAWarning(t *testing.T) {
	w := NewICMPWatcher(event.NewBuilder("obs", nil), nil)

	for code, wantSev := range map[uint8]event.Severity{
		codeAdminFiltered:   event.SevWarn,
		codeHostUnreachable: event.SevNotice,
	} {
		payload := buildICMPUnreachable(code, 0, net.IPv4(10, 0, 0, 5), net.IPv4(10, 0, 0, 9), 443)
		frame := buildFrame(frameOpts{proto: protoICMP, srcIP: net.IPv4(10, 0, 0, 254), payload: payload})
		p, _ := Parse(frame)
		m, err := ParseICMP(p.Payload)
		if err != nil {
			t.Fatalf("ParseICMP: %v", err)
		}
		e := findKind(w.Observe(p, m), event.KindICMPUnreachable)
		if e == nil {
			t.Fatalf("code %d was not reported", code)
		}
		if e.Severity != wantSev {
			t.Errorf("code %d severity = %s, want %s", code, e.Severity, wantSev)
		}
	}
}

func TestICMPTruncatedQuoteIsSurvivable(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a truncated quoted packet panicked: %v", r)
		}
	}()
	short := make([]byte, 10)
	short[0] = icmpDestUnreachable
	short[1] = codeFragNeeded
	if _, err := ParseICMP(short); err != nil {
		t.Errorf("a short but valid ICMP header was rejected: %v", err)
	}
}

/* ------------------------------------------------------------------ */
/* Helpers                                                            */
/* ------------------------------------------------------------------ */

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func findKind(events []*event.Event, kind event.Kind) *event.Event {
	for _, e := range events {
		if e.Kind == kind {
			return e
		}
	}
	return nil
}
