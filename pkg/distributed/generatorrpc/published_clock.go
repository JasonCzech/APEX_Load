package generatorrpc

import (
	"sync"
	"time"
)

// publishedClockTolerance is how far the published clock may drift from the
// wall clock (slewing, or a step) before an idle heartbeat re-anchors it.
const publishedClockTolerance = time.Second

// publishedClock is the clock a generator stamps on what the controller
// times across hosts: heartbeat pongs (which the controller's offset probes
// measure) and telemetry collection and publication times. It advances on the
// monotonic clock from an anchor, so a wall-clock step on this host moves
// none of those stamps; it is re-anchored only while answering a heartbeat
// with no test assigned, so a run's stamps stay on one anchor from start to
// finish and the controller sees any jump at a probe outside a run, holding
// the mapping unqualified until a consistent probe confirms it. Offsets the
// controller measures are therefore against this clock: during a run they do
// not follow a step of the wall clock that stamps the run's transactions.
type publishedClock struct {
	mu   sync.Mutex
	wall time.Time // anchor's wall time, without a monotonic reading
	mono time.Time // anchor's reading, with its monotonic clock
}

var generatorPublishedClock = newPublishedClock(time.Now())

func newPublishedClock(now time.Time) *publishedClock {
	return &publishedClock{wall: now.Round(0), mono: now}
}

// at is t on the published clock. An instant without a monotonic reading
// falls back to its wall time.
func (c *publishedClock) at(t time.Time) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall.Add(t.Sub(c.mono))
}

// reanchor moves the anchor to now when the published clock has drifted from
// the wall clock by more than the tolerance; it reports whether it moved.
func (c *publishedClock) reanchor(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if (c.wall.Add(now.Sub(c.mono)).Sub(now.Round(0))).Abs() <= publishedClockTolerance {
		return false
	}
	c.wall, c.mono = now.Round(0), now
	return true
}
