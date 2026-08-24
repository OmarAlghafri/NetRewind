// Package wire observes the few protocols whose *metadata* answers questions
// nothing else can: DHCP, DNS and ICMP.
//
// A rogue DHCP server, a client whose resolver silently changed, a path that
// started returning "fragmentation needed" - none of these are visible in the
// kernel's own state, because they happen to other machines. They have to be
// seen on the wire.
//
// No payloads are kept. A DHCP message becomes "this server offered this
// address to this hardware address"; a DNS exchange becomes "this name failed,
// through this resolver". The bytes are read and discarded.
package wire

import (
	"encoding/binary"
	"errors"
	"net"
)

// Ethernet and IP constants, spelled out rather than pulled from a dependency:
// there are six of them and they have not changed since 1982.
const (
	ethTypeIPv4 = 0x0800
	ethTypeVLAN = 0x8100
	ethTypeQinQ = 0x88a8

	protoICMP = 1
	protoUDP  = 17

	ethHeaderLen = 14
	vlanTagLen   = 4
	udpHeaderLen = 8
)

// ErrNotForUs is returned for a frame the parser has no interest in. It is a
// normal outcome on a busy segment, not a failure, and callers should not log
// it.
var ErrNotForUs = errors.New("wire: frame is not one we parse")

// Packet is the decoded envelope of a frame, with the transport payload left
// as a slice into the original buffer.
type Packet struct {
	SrcMAC  net.HardwareAddr
	DstMAC  net.HardwareAddr
	VLAN    uint16 // 0 when untagged
	SrcIP   net.IP
	DstIP   net.IP
	Proto   uint8
	SrcPort uint16
	DstPort uint16
	// Payload is the UDP payload, or the ICMP message including its header.
	Payload []byte
}

// Parse decodes an Ethernet frame far enough to route it to a handler.
//
// It is deliberately strict about lengths at every step. This reads bytes that
// arrived from the network and that anyone on the segment can craft, so a
// truncated or lying header must produce an error rather than a slice out of
// range.
func Parse(frame []byte) (Packet, error) {
	var p Packet
	if len(frame) < ethHeaderLen {
		return p, ErrNotForUs
	}

	p.DstMAC = net.HardwareAddr(frame[0:6])
	p.SrcMAC = net.HardwareAddr(frame[6:12])

	offset := 12
	ethType := binary.BigEndian.Uint16(frame[offset : offset+2])
	offset += 2

	// One level of VLAN tagging, and one of QinQ inside it. Deeper stacks
	// exist and are not worth the loop: nothing this parser cares about is
	// carried under three tags.
	for i := 0; i < 2 && (ethType == ethTypeVLAN || ethType == ethTypeQinQ); i++ {
		if len(frame) < offset+vlanTagLen {
			return p, ErrNotForUs
		}
		if p.VLAN == 0 {
			p.VLAN = binary.BigEndian.Uint16(frame[offset:offset+2]) & 0x0fff
		}
		ethType = binary.BigEndian.Uint16(frame[offset+2 : offset+4])
		offset += vlanTagLen
	}

	if ethType != ethTypeIPv4 {
		return p, ErrNotForUs
	}
	if len(frame) < offset+20 {
		return p, ErrNotForUs
	}

	ihl := int(frame[offset]&0x0f) * 4
	if ihl < 20 || len(frame) < offset+ihl {
		return p, ErrNotForUs
	}
	totalLen := int(binary.BigEndian.Uint16(frame[offset+2 : offset+4]))

	p.Proto = frame[offset+9]
	p.SrcIP = net.IP(frame[offset+12 : offset+16])
	p.DstIP = net.IP(frame[offset+16 : offset+20])

	// A fragment other than the first has no transport header to read.
	fragOff := binary.BigEndian.Uint16(frame[offset+6:offset+8]) & 0x1fff
	if fragOff != 0 {
		return p, ErrNotForUs
	}

	// Trust the IP total length over the captured length where it is smaller:
	// Ethernet pads short frames, and the padding is not payload.
	end := len(frame)
	if totalLen >= ihl && offset+totalLen < end {
		end = offset + totalLen
	}

	transport := offset + ihl
	if transport > end {
		return p, ErrNotForUs
	}

	switch p.Proto {
	case protoUDP:
		if end-transport < udpHeaderLen {
			return p, ErrNotForUs
		}
		p.SrcPort = binary.BigEndian.Uint16(frame[transport : transport+2])
		p.DstPort = binary.BigEndian.Uint16(frame[transport+2 : transport+4])
		udpLen := int(binary.BigEndian.Uint16(frame[transport+4 : transport+6]))
		payloadEnd := end
		if udpLen >= udpHeaderLen && transport+udpLen < payloadEnd {
			payloadEnd = transport + udpLen
		}
		p.Payload = frame[transport+udpHeaderLen : payloadEnd]
		return p, nil

	case protoICMP:
		p.Payload = frame[transport:end]
		return p, nil
	}
	return p, ErrNotForUs
}

// IsBroadcastMAC reports whether an address is the all-ones broadcast, which is
// how a DHCP client that has no address yet is reached.
func IsBroadcastMAC(mac net.HardwareAddr) bool {
	if len(mac) != 6 {
		return false
	}
	for _, b := range mac {
		if b != 0xff {
			return false
		}
	}
	return true
}
