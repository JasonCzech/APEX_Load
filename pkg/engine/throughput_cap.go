package engine

import (
	"context"
	"math"
	"sync"
	"time"
)

// GroupThroughputCap holds a group's passed transactions — the population
// live TPS counts — to a target rate by spacing the start of its iterations.
// Goal-driven and traffic-profile runs set it from the group's current TPS
// target. The goal controller still scales VUsers so there is enough capacity
// to reach the target; the cap stops any surplus from overshooting it. A VUser
// count alone cannot do that: one VUser of a fast script can exceed a small
// per-group target on its own, and a minimum VUser floor can hold a run above
// its goal indefinitely.
//
// Each admitted iteration is given the next start slot. Slots are spaced by
// the group's observed transactions per iteration divided by the target, so
// the rate is held in transaction units whatever the script's shape. Any
// excess the estimate lets through (a first iteration that records more
// transactions than assumed, a branchy script) is measured against the
// group's run counters and pushes later slots back, so the cap converges on
// the target rather than on its estimate.
type GroupThroughputCap struct {
	mu         sync.Mutex
	attempts   func() int64
	iterations func() int64
	now        func() time.Time

	enabled bool
	tps     float64
	// changed is closed and replaced whenever the target, enablement or pause
	// state changes, so VUsers waiting on an old slot re-admit promptly.
	changed chan struct{}

	nextSlot time.Time
	// The accounting segment began at segmentStart with anchorAttempts
	// counted. expectedBase is what the target allowed before anchor, at the
	// rates then in force; tps × active time since anchor is added to it.
	// "Attempts" here are the counted transactions: passed ones, as live TPS
	// counts them.
	segmentStart   time.Time
	anchor         time.Time
	anchorAttempts int64
	expectedBase   float64
	pausedAt       time.Time
}

// newGroupThroughputCap creates a disabled cap reading a group's run counters.
func newGroupThroughputCap(attempts, iterations func() int64) *GroupThroughputCap {
	return &GroupThroughputCap{
		attempts:   attempts,
		iterations: iterations,
		now:        time.Now,
		changed:    make(chan struct{}),
	}
}

// validThroughputCapTPS reports whether tps can be used as a cap target. Zero
// is valid and suspends the group (a traffic profile at zero).
func validThroughputCapTPS(tps float64) bool {
	return tps >= 0 && !math.IsNaN(tps) && !math.IsInf(tps, 0)
}

// Set enables the cap at tps transactions per second, or disables it.
func (c *GroupThroughputCap) Set(tps float64, enabled bool) {
	if c == nil {
		return
	}
	if !validThroughputCapTPS(tps) {
		enabled, tps = false, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enabled == enabled && (!enabled || c.tps == tps) {
		return
	}
	now := c.now()
	if c.enabled && enabled && !c.anchor.IsZero() {
		// The segment keeps running across a target change: what the old rate
		// allowed up to now is banked, so an excess (or a shortfall) carries.
		c.bankExpectedLocked(now)
	} else if enabled && !c.anchor.IsZero() {
		c.resetAccountingLocked(now)
	}
	c.enabled, c.tps = enabled, tps
	if enabled {
		// Every VUser waiting for a slot is woken below and takes a fresh one at
		// the new rate, so the slots handed out at the old rate are released.
		// Keeping them as a backlog queued the woken VUsers behind their own
		// abandoned slots: a traffic profile that moves every few seconds then
		// starved its groups. The next slot stays where it was if it falls
		// within one slot at the new rate, so a change does not release an
		// iteration early either.
		limit := now.Add(throughputCapSpacing(c.costLocked(), tps))
		if c.nextSlot.Before(now) {
			c.nextSlot = now
		} else if c.nextSlot.After(limit) {
			c.nextSlot = limit
		}
	}
	c.signalLocked()
}

// Target returns the cap's rate and whether it is enabled.
func (c *GroupThroughputCap) Target() (float64, bool) {
	if c == nil {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tps, c.enabled
}

// SetPaused stops paused time from earning credit: on resume the accounting
// segment moves forward by the paused span and the waiting VUsers take fresh
// slots from the resume, spaced exactly as before the pause.
func (c *GroupThroughputCap) SetPaused(paused bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if paused {
		if c.pausedAt.IsZero() {
			c.pausedAt = now
			// Wake VUsers holding a slot so none starts an iteration while paused.
			c.signalLocked()
		}
		return
	}
	if c.pausedAt.IsZero() {
		return
	}
	span := now.Sub(c.pausedAt)
	c.pausedAt = time.Time{}
	if span > 0 && !c.anchor.IsZero() {
		c.anchor = c.anchor.Add(span)
		c.segmentStart = c.segmentStart.Add(span)
	}
	// The VUsers that waited out the pause re-admit at once, in turn, from
	// now; their pre-pause slots are released as on a target change.
	c.nextSlot = now
	c.signalLocked()
}

// Admit waits for the next start slot. It returns false when ctx ends first.
// A disabled cap admits at once.
func (c *GroupThroughputCap) Admit(ctx context.Context) bool {
	if c == nil {
		return true
	}
	for {
		c.mu.Lock()
		if !c.enabled {
			c.mu.Unlock()
			return true
		}
		changed := c.changed
		if c.tps <= 0 || !c.pausedAt.IsZero() {
			// Suspended (zero target) or paused: wait for a change.
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return false
			case <-changed:
				continue
			}
		}
		now := c.now()
		if c.anchor.IsZero() {
			// Account from the first admission, not from when the target was
			// set: startup time before any VUser runs must not earn credit.
			c.resetAccountingLocked(now)
		}
		c.correctLocked(now)
		slot := c.nextSlot
		if slot.Before(now) {
			slot = now
		}
		c.nextSlot = slot.Add(throughputCapSpacing(c.costLocked(), c.tps))
		c.mu.Unlock()

		wait := slot.Sub(now)
		if wait <= 0 {
			return ctx.Err() == nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-changed:
			// The target or pause state changed while waiting. Slots handed out
			// under the old state were released; take a fresh one under the new.
			timer.Stop()
			continue
		case <-timer.C:
			if ctx.Err() != nil {
				return false
			}
			// The run may have paused (or the group been suspended) as the slot
			// came due — select picks between ready cases at random — and a slot
			// reserved before the group's cost per iteration was known (or before
			// a burst of transactions landed) may now be early. In any of these
			// cases queue again instead of starting the iteration.
			c.mu.Lock()
			retry := c.enabled && (c.tps <= 0 || !c.pausedAt.IsZero() || c.excessLocked(c.now()) > 0)
			c.mu.Unlock()
			if retry {
				continue
			}
			return true
		}
	}
}

// maxThroughputCapSpacing bounds one slot's spacing so a vanishing target
// cannot overflow the duration conversion; such a target is a suspension in
// all but name.
const maxThroughputCapSpacing = time.Hour

func throughputCapSpacing(attempts, tps float64) time.Duration {
	if tps <= 0 || attempts <= 0 {
		return 0
	}
	seconds := attempts / tps
	if math.IsNaN(seconds) || seconds >= maxThroughputCapSpacing.Seconds() {
		return maxThroughputCapSpacing
	}
	return time.Duration(seconds * float64(time.Second))
}

func (c *GroupThroughputCap) resetAccountingLocked(now time.Time) {
	c.segmentStart = now
	c.anchor = now
	c.anchorAttempts = c.readAttempts()
	c.expectedBase = 0
}

// bankExpectedLocked adds what the current rate allowed since anchor to the
// segment's expected total and moves anchor to now (to the pause, while
// paused: paused time allows nothing, and SetPaused moves anchor past it).
func (c *GroupThroughputCap) bankExpectedLocked(now time.Time) {
	end := now
	if !c.pausedAt.IsZero() {
		end = c.pausedAt
	}
	if elapsed := end.Sub(c.anchor); elapsed > 0 && c.tps > 0 {
		c.expectedBase += c.tps * elapsed.Seconds()
	}
	if end.After(c.anchor) {
		c.anchor = end
	}
}

// correctLocked pushes the next slot back when the group has recorded more
// attempts than the target allows since the accounting segment began. An
// admitted iteration records its transactions in a burst while the allowance
// grows smoothly; when the next slot comes due the earlier iterations have
// landed exactly on it, so slack of one iteration (and a tenth of a second's
// worth, for timing noise at high rates) keeps the correction to real excess.
// That bounds the overshoot of slots handed out before the first iteration
// showed the script's cost to about one iteration. Transactions still in
// flight are not counted yet, so they can only make the correction late,
// never early.
func (c *GroupThroughputCap) correctLocked(now time.Time) {
	if c.anchor.IsZero() || c.tps <= 0 {
		return
	}
	if excess := c.excessLocked(now); excess > 0 {
		earliest := now.Add(throughputCapSpacing(excess, c.tps))
		if c.nextSlot.Before(earliest) {
			c.nextSlot = earliest
		}
	}
	// Restart the segment every minute so an early burst is not held against
	// the rest of a long run, and a long under-delivery (too few VUsers) does
	// not bank credit that would hide a later excess from this check.
	if now.Sub(c.segmentStart) > time.Minute {
		c.resetAccountingLocked(now)
	}
}

// excessLocked is how many attempts the group is ahead of its target in the
// current accounting segment, beyond the slack described at correctLocked.
func (c *GroupThroughputCap) excessLocked(now time.Time) float64 {
	if c.anchor.IsZero() || c.tps <= 0 {
		return 0
	}
	elapsed := now.Sub(c.anchor)
	if elapsed < 0 {
		elapsed = 0
	}
	actual := float64(c.readAttempts() - c.anchorAttempts)
	expected := c.expectedBase + c.tps*elapsed.Seconds()
	slack := math.Max(math.Max(1, c.costLocked()), c.tps/10)
	return actual - expected - slack
}

// costLocked is the expected transaction attempts per iteration: the group's
// observed ratio once an iteration has completed, else one. Transactions of
// iterations still running are already counted, so the early ratio leans
// high, which errs toward under-delivery that the controller's VUser scaling
// makes up, never toward overshoot.
func (c *GroupThroughputCap) costLocked() float64 {
	iterations := int64(0)
	if c.iterations != nil {
		iterations = c.iterations()
	}
	attempts := c.readAttempts()
	if iterations < 1 || attempts <= 0 {
		return 1
	}
	cost := float64(attempts) / float64(iterations)
	return math.Min(math.Max(cost, 0.05), 10000)
}

func (c *GroupThroughputCap) readAttempts() int64 {
	if c.attempts == nil {
		return 0
	}
	return c.attempts()
}

func (c *GroupThroughputCap) signalLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}
