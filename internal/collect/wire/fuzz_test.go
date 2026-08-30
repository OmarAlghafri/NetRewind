package wire

import (
	"encoding/hex"
	"net"
	"testing"
	"time"

	"github.com/OmarAlghafri/netrewind/internal/event"
)

// These are the only parsers in the project that read bytes nobody here chose.
//
// Everything else works from netlink, from the kernel's own structures, or from
// a file an operator wrote. A frame arrives from whoever is on the segment, and
// the recorder is on that segment precisely because something there is
// suspected. A parser that can be made to panic by a crafted frame is a way to
// switch the witness off from the network it is watching - which is worse than
// the fault it was deployed to find, because the record then stops with no
// explanation in it.
//
// Run them for longer than a unit test does with:
//
//	go test ./internal/collect/wire -run xxx -fuzz FuzzParseFrame -fuzztime 2m

func FuzzParseFrame(f *testing.F) {
	for _, seed := range frameSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, frame []byte) {
		p, err := Parse(frame)
		if err != nil {
			return
		}
		// Whatever it returns has to be inside the frame it was given.
		if len(p.Payload) > len(frame) {
			t.Fatalf("payload of %d bytes from a %d byte frame", len(p.Payload), len(frame))
		}
		if len(p.SrcMAC) != 6 || len(p.DstMAC) != 6 {
			t.Fatalf("hardware addresses of %d and %d bytes", len(p.SrcMAC), len(p.DstMAC))
		}
		if len(p.SrcIP) != 4 || len(p.DstIP) != 4 {
			t.Fatalf("addresses of %d and %d bytes", len(p.SrcIP), len(p.DstIP))
		}
		// And the handlers downstream must survive it too.
		switch {
		case p.Proto == protoUDP && (p.SrcPort == dhcpServerPort || p.DstPort == dhcpServerPort):
			if m, err := ParseDHCP(p.Payload); err == nil {
				NewDHCPWatcher(event.NewBuilder("obs", nil), fixedNow).Observe(p, m)
			}
		case p.Proto == protoUDP && (p.SrcPort == dnsPort || p.DstPort == dnsPort):
			if m, err := ParseDNS(p.Payload); err == nil {
				NewDNSWatcher(event.NewBuilder("obs", nil), fixedNow, true).Observe(p, m)
			}
		case p.Proto == protoICMP:
			ParseICMP(p.Payload)
		}
	})
}

func FuzzParseDHCP(f *testing.F) {
	f.Add(dhcpOfferFrame()[42:])
	f.Add(make([]byte, dhcpMinLen))
	f.Fuzz(func(t *testing.T, payload []byte) {
		m, err := ParseDHCP(payload)
		if err != nil {
			return
		}
		// A malformed message must not be able to make up an address.
		addrs := []net.IP{m.YourIP, m.ServerID, m.Router, m.RequestedIP}
		addrs = append(addrs, m.DNS...)
		for _, ip := range addrs {
			if ip != nil && len(ip) != 4 {
				t.Fatalf("address of %d bytes from a %d byte payload", len(ip), len(payload))
			}
		}
		if m.ClientMAC != nil && len(m.ClientMAC) != 6 {
			t.Fatalf("hardware address of %d bytes", len(m.ClientMAC))
		}
	})
}

func FuzzParseDNS(f *testing.F) {
	f.Add(dnsQueryPayload())
	f.Add(make([]byte, 12))
	// A name that points at itself: the classic way to hang a resolver.
	f.Add(mustHex("00010000000100000000000" + "0c00c0000010001"))
	f.Fuzz(func(t *testing.T, payload []byte) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			m, err := ParseDNS(payload)
			if err == nil {
				// The question is a string built from attacker bytes; it must
				// stay bounded whatever the compression pointers say.
				if len(m.Question) > 4096 {
					panic("question of unreasonable length")
				}
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("ParseDNS did not return for a %d byte payload", len(payload))
		}
	})
}

func fixedNow() time.Time { return time.Unix(1700000000, 0) }

func mustHex(s string) []byte {
	if len(s)%2 == 1 {
		s = s[:len(s)-1]
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

// frameSeeds are shapes worth starting from: valid ones, and the awkward
// lengths a length field can claim.
func frameSeeds() [][]byte {
	seeds := [][]byte{
		nil,
		make([]byte, 13),  // one short of an Ethernet header
		make([]byte, 14),  // exactly an Ethernet header and nothing else
		make([]byte, 33),  // an IP header claiming more than it has
		dhcpOfferFrame(),  // a real one
		vlanTaggedFrame(), // and one under two tags
	}
	// An IPv4 header whose IHL claims sixty bytes in a frame that has fourteen.
	lying := make([]byte, 34)
	lying[12], lying[13] = 0x08, 0x00
	lying[14] = 0x4f // version 4, IHL 15 -> 60 bytes
	lying[16], lying[17] = 0xff, 0xff
	seeds = append(seeds, lying)
	return seeds
}
