package event

import (
	"sync"
	"time"
)

// DefaultStepThreshold is how far the wall clock may drift away from the
// monotonic clock between two readings before we call it a step rather than
// ordinary jitter.
const DefaultStepThreshold = 500 * time.Millisecond

// Clock hands out the two timestamps every event carries.
//
// ts_wall answers "when did this happen" and is what a human queries by. It can
// jump: NTP corrects it, a VM is resumed, someone sets the date by hand.
// ts_mono answers "in what order did this happen" and never jumps, but is
// meaningless across a reboot. Recording both is what lets the timeline stay
// correctly ordered through a clock correction instead of quietly reordering
// itself - and lets us emit system.clock_step when it happens.
type Clock struct {
	mu       sync.Mutex
	base     time.Time // carries a monotonic reading; mono is measured from here
	lastWall int64
	lastMono int64
	step     int64 // pending, un-drained discontinuity in nanoseconds
	stepped  bool
	thresh   time.Duration
}

// NewClock starts a clock whose monotonic origin is now.
func NewClock() *Clock {
	now := time.Now()
	return &Clock{base: now, lastWall: now.UnixNano(), thresh: DefaultStepThreshold}
}

// Now returns the wall time in nanoseconds since the Unix epoch (UTC) and the
// monotonic time in nanoseconds since this clock was created.
func (c *Clock) Now() (wall int64, mono int64) {
	now := time.Now()
	wall = now.UnixNano()
	mono = int64(now.Sub(c.base))

	c.mu.Lock()
	defer c.mu.Unlock()
	// A step is a wall-clock movement that the monotonic clock did not see.
	if dw, dm := wall-c.lastWall, mono-c.lastMono; abs64(dw-dm) > int64(c.thresh) {
		c.step = dw - dm
		c.stepped = true
	}
	c.lastWall, c.lastMono = wall, mono
	return wall, mono
}

// TakeStep reports any clock discontinuity observed since it was last called,
// and clears it. The health collector drains this and turns it into a
// system.clock_step event.
func (c *Clock) TakeStep() (ns int64, stepped bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, stepped = c.step, c.stepped
	c.step, c.stepped = 0, false
	return ns, stepped
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
