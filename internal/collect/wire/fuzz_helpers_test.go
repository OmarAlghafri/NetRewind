package wire

import (
	"net"
	"time"
)

// The fuzz seeds are built with the same frame builder the unit tests use, so
// a change to how a frame is assembled cannot leave the fuzzer starting from
// bytes that stopped being representative.

func dhcpOfferFrame() []byte {
	return buildFrame(frameOpts{
		srcMAC:  net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01},
		srcIP:   net.IPv4(10, 99, 0, 1),
		dstIP:   net.IPv4(255, 255, 255, 255),
		srcPort: dhcpServerPort,
		dstPort: dhcpClientPort,
		payload: buildDHCP(dhcpOffer,
			net.HardwareAddr{0x02, 0, 0, 0, 0, 0x11},
			net.IPv4(10, 99, 0, 50), net.IPv4(10, 99, 0, 1), net.IPv4(10, 99, 0, 1),
			time.Hour),
	})
}

func vlanTaggedFrame() []byte {
	return buildFrame(frameOpts{
		vlan:    42,
		srcIP:   net.IPv4(10, 99, 0, 11),
		dstIP:   net.IPv4(10, 99, 0, 1),
		srcPort: 40000,
		dstPort: dnsPort,
		payload: buildDNS(0x1234, false, 0, "example.internal"),
	})
}

func dnsQueryPayload() []byte {
	return buildDNS(0x1234, false, 0, "a-name.that.is.long.enough.to.have.several.labels")
}
