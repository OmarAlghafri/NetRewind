//go:build linux

// Command nrinject sends the DHCP and DNS frames the lab needs, from inside a
// network namespace.
//
// Two commands in one binary. There is no netcat trick that sends a DHCP OFFER
// from a chosen server address with a chosen gateway option, and running a real
// DHCP daemon to produce two conflicting servers on a test segment is far more
// machinery than the fault deserves. This writes the frames directly.
//
// It exists only to exercise the recorder. It is not shipped in a release and
// it must never be pointed at a network anyone is using: a DHCP OFFER from an
// unexpected server is precisely the fault NetRewind is built to catch.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

func main() {
	log := func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

	if len(os.Args) < 2 {
		log("usage: nrinject <dhcp|dns> [flags]")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "dhcp":
		if err := sendDHCP(os.Args[2:]); err != nil {
			log("dhcp: %v", err)
			os.Exit(1)
		}
	case "dns":
		if err := sendDNS(os.Args[2:]); err != nil {
			log("dns: %v", err)
			os.Exit(1)
		}
	default:
		log("unknown command %q", os.Args[1])
		os.Exit(2)
	}
}

/* ------------------------------------------------------------------ */
/* DHCP                                                               */
/* ------------------------------------------------------------------ */

func sendDHCP(args []string) error {
	fs := flag.NewFlagSet("dhcp", flag.ExitOnError)
	iface := fs.String("iface", "", "interface to send from")
	serverIP := fs.String("server", "10.99.0.1", "the server's address")
	serverMAC := fs.String("mac", "02:00:00:00:00:01", "the server's hardware address")
	gateway := fs.String("gw", "", "the gateway option to hand out")
	clientMAC := fs.String("client", "02:00:00:00:00:55", "the client being answered")
	offered := fs.String("offer", "10.99.0.50", "the address being offered")
	kind := fs.String("type", "offer", "offer or ack")
	if err := fs.Parse(args); err != nil {
		return err
	}

	smac, err := net.ParseMAC(*serverMAC)
	if err != nil {
		return fmt.Errorf("bad -mac: %w", err)
	}
	cmac, err := net.ParseMAC(*clientMAC)
	if err != nil {
		return fmt.Errorf("bad -client: %w", err)
	}
	sip, cip := net.ParseIP(*serverIP), net.ParseIP(*offered)
	if sip == nil || cip == nil {
		return fmt.Errorf("bad address")
	}

	msgType := byte(2) // OFFER
	if *kind == "ack" {
		msgType = 5
	}

	payload := buildDHCP(msgType, cmac, cip, sip, net.ParseIP(*gateway))
	frame := buildFrame(smac, broadcastMAC, sip, net.IPv4(255, 255, 255, 255), 67, 68, payload)
	return send(*iface, frame)
}

func buildDHCP(msgType byte, clientMAC net.HardwareAddr, yourIP, serverID, router net.IP) []byte {
	p := make([]byte, 240)
	p[0] = 2 // BOOTREPLY
	p[1] = 1 // ethernet
	p[2] = 6 // hardware address length
	binary.BigEndian.PutUint32(p[4:8], 0x0badcafe)
	copy(p[16:20], yourIP.To4())
	copy(p[28:34], clientMAC)
	binary.BigEndian.PutUint32(p[236:240], 0x63825363)

	p = append(p, 53, 1, msgType)
	p = append(p, 54, 4)
	p = append(p, serverID.To4()...)
	p = append(p, 51, 4, 0, 0, 0x0e, 0x10) // one hour
	if router != nil {
		p = append(p, 3, 4)
		p = append(p, router.To4()...)
	}
	return append(p, 255)
}

/* ------------------------------------------------------------------ */
/* DNS                                                                */
/* ------------------------------------------------------------------ */

func sendDNS(args []string) error {
	fs := flag.NewFlagSet("dns", flag.ExitOnError)
	iface := fs.String("iface", "", "interface to send from")
	client := fs.String("client", "10.99.0.11", "the client asking")
	resolver := fs.String("resolver", "10.99.0.1", "the resolver being asked")
	name := fs.String("name", "example.lab", "the name being looked up")
	id := fs.Int("id", 0x2a2a, "transaction id")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cip, rip := net.ParseIP(*client), net.ParseIP(*resolver)
	if cip == nil || rip == nil {
		return fmt.Errorf("bad address")
	}

	payload := buildDNSQuery(uint16(*id), *name)
	frame := buildFrame(
		net.HardwareAddr{0x02, 0, 0, 0, 0, 0x11},
		net.HardwareAddr{0x02, 0, 0, 0, 0, 0x01},
		cip, rip, 45000, 53, payload)
	return send(*iface, frame)
}

func buildDNSQuery(id uint16, name string) []byte {
	p := make([]byte, 12)
	binary.BigEndian.PutUint16(p[0:2], id)
	binary.BigEndian.PutUint16(p[2:4], 0x0100) // standard query, recursion desired
	binary.BigEndian.PutUint16(p[4:6], 1)

	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if i > start {
				p = append(p, byte(i-start))
				p = append(p, name[start:i]...)
			}
			start = i + 1
		}
	}
	p = append(p, 0)
	return append(p, 0, 1, 0, 1) // A, IN
}

/* ------------------------------------------------------------------ */
/* Frames                                                             */
/* ------------------------------------------------------------------ */

var broadcastMAC = net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

func buildFrame(srcMAC, dstMAC net.HardwareAddr, srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	udp := make([]byte, 8)
	binary.BigEndian.PutUint16(udp[0:2], srcPort)
	binary.BigEndian.PutUint16(udp[2:4], dstPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(8+len(payload)))
	udp = append(udp, payload...)

	ip := make([]byte, 20)
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(20+len(udp)))
	ip[8] = 64
	ip[9] = 17 // UDP
	copy(ip[12:16], srcIP.To4())
	copy(ip[16:20], dstIP.To4())
	binary.BigEndian.PutUint16(ip[10:12], checksum(ip))

	frame := make([]byte, 0, 14+len(ip)+len(udp))
	frame = append(frame, dstMAC...)
	frame = append(frame, srcMAC...)
	frame = append(frame, 0x08, 0x00)
	frame = append(frame, ip...)
	return append(frame, udp...)
}

func checksum(header []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(header); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(header[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// send writes one frame to an interface.
//
// The caller is expected to have entered the target network namespace already
// (the lab uses `ip netns exec`), so this only has to find the interface and
// write. Locking the thread matters even so: Go can move a goroutine between
// OS threads, and the namespace is a property of the thread.
func send(iface string, frame []byte) error {
	if iface == "" {
		return fmt.Errorf("-iface is required")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return fmt.Errorf("no interface %q: %w", iface, err)
	}

	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return fmt.Errorf("open packet socket (needs root): %w", err)
	}
	defer unix.Close(fd)

	addr := unix.SockaddrLinklayer{
		Protocol: htons(unix.ETH_P_ALL),
		Ifindex:  ifi.Index,
		Halen:    6,
	}
	copy(addr.Addr[:6], frame[0:6])

	if err := unix.Sendto(fd, frame, 0, &addr); err != nil {
		return fmt.Errorf("send on %s: %w", iface, err)
	}
	return nil
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }
