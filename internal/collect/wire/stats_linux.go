//go:build linux

package wire

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// rcvBufBytes is how much the kernel is asked to hold for this socket.
//
// The default for a raw socket is around 200 KB, which a broadcast storm can
// fill between two reads. The filter already keeps all but four kinds of frame
// out, so a larger buffer costs almost nothing and removes the most likely
// reason for this collector to miss the moment it exists to catch.
const rcvBufBytes = 4 << 20

// drainStats reads and resets the kernel's drop counters for the socket.
//
// PACKET_STATISTICS is destructive: reading it zeroes the counters, so each
// call returns what happened since the last one. That is exactly the shape
// wanted here - the question is never "how many in total" but "did we miss
// anything in the window I am looking at".
func drainStats(fd int) (received, dropped uint32, err error) {
	st, err := unix.GetsockoptTpacketStats(fd, unix.SOL_PACKET, unix.PACKET_STATISTICS)
	if err != nil {
		return 0, 0, fmt.Errorf("wire: read packet statistics: %w", err)
	}
	return st.Packets, st.Drops, nil
}

// setRcvBuf enlarges the socket buffer, best effort.
//
// SO_RCVBUF is capped by net.core.rmem_max, so the kernel may grant less than
// asked. That is not a failure worth refusing to start over - a smaller buffer
// still records, and anything it then misses is reported by drainStats rather
// than hidden.
func setRcvBuf(fd int) error {
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, rcvBufBytes)
}
