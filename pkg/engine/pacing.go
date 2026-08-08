package engine

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// sleepCtx sleeps for d, but returns early if ctx is cancelled. This lets
// think-time and between-iteration pacing be interrupted promptly when a VUser
// is scaled/ramped down or the run is hard-stopped, instead of holding load for
// the full sleep duration.
func sleepCtx(ctx context.Context, d time.Duration) {
	_, _ = sleepCtxElapsed(ctx, d)
}

// sleepCtxElapsed waits for d or context cancellation and reports both the
// time actually spent waiting and whether the full delay completed. Callers
// that exclude think time from response measurements must account the elapsed
// value, not the requested delay, because cancellation can interrupt the wait.
func sleepCtxElapsed(ctx context.Context, d time.Duration) (time.Duration, bool) {
	if d <= 0 {
		return 0, true
	}
	if ctx == nil {
		time.Sleep(d)
		return d, true
	}
	started := time.Now()
	timer := time.NewTimer(d)
	defer timer.Stop()
	return waitForDelayOrCancellation(ctx, d, started, timer.C)
}

func waitForDelayOrCancellation(ctx context.Context, d time.Duration, started time.Time, timer <-chan time.Time) (time.Duration, bool) {
	select {
	case <-ctx.Done():
		elapsed := time.Since(started)
		if elapsed > d {
			elapsed = d
		}
		return elapsed, false
	case <-timer:
		// A suspended or heavily loaded process can resume after both the delay
		// timer and an earlier context deadline became ready. select chooses
		// randomly in that state, so accepting the timer unconditionally can
		// report a canceled wait as complete and execute the next source action.
		// Recheck cancellation after receiving the timer: once the caller's
		// boundary is closed, failing closed is the only disposition that cannot
		// run work after cancellation.
		if ctx.Err() != nil {
			elapsed := time.Since(started)
			if elapsed > d {
				elapsed = d
			}
			return elapsed, false
		}
		return d, true
	}
}

// applyThinkTime sleeps for a computed think time duration, respecting
// multiplier, limit, and ignore settings.
func applyThinkTime(cfg protocol.ThinkTimeConfig) {
	applyThinkTimeCtx(context.Background(), cfg)
}

// applyThinkTimeCtx is the context-aware variant of applyThinkTime; the sleep is
// cut short if ctx is cancelled.
func applyThinkTimeCtx(ctx context.Context, cfg protocol.ThinkTimeConfig) {
	sleepCtx(ctx, computeThinkTime(cfg))
}

func computeThinkTime(cfg protocol.ThinkTimeConfig) time.Duration {
	if cfg.Ignore {
		return 0
	}
	for _, value := range []float64{cfg.MeanMs, cfg.StdMs, cfg.MinMs, cfg.MaxMs, cfg.Multiplier, cfg.LimitMs} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0
		}
	}

	var ms float64

	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "fixed", "recorded":
		ms = cfg.MeanMs

	case "gaussian", "normal":
		ms = rand.NormFloat64()*cfg.StdMs + cfg.MeanMs
		if cfg.MinMs > 0 && ms < cfg.MinMs {
			ms = cfg.MinMs
		}
		if cfg.MaxMs > 0 && ms > cfg.MaxMs {
			ms = cfg.MaxMs
		}

	case "uniform":
		lo := cfg.MinMs
		hi := cfg.MaxMs
		if hi <= lo {
			ms = lo
		} else {
			ms = lo + rand.Float64()*(hi-lo)
		}

	default:
		return 0
	}

	ms = math.Max(0, ms)

	// Zero is the wire-compatible omitted/inherit sentinel. Treat it exactly as
	// the documented default of 1; Ignore is the explicit zero-delay control.
	multiplier := cfg.Multiplier
	if multiplier <= 0 {
		multiplier = 1
	}
	// Apply multiplier (1.0 = as recorded, 0.5 = half, 2.0 = double).
	if multiplier != 1 {
		ms *= multiplier
	}

	// Apply limit cap
	if cfg.LimitMs > 0 && ms > cfg.LimitMs {
		ms = cfg.LimitMs
	}
	ms = min(ms, float64(dsl.MaxConfiguredProtocolTimeoutMS))

	return time.Duration(ms * float64(time.Millisecond))
}

func boundedRuntimeDelayMilliseconds(value int) int {
	if value <= 0 {
		return 0
	}
	return min(value, dsl.MaxConfiguredProtocolTimeoutMS)
}

func randomRuntimeDelayMilliseconds(low, high int) int {
	low = boundedRuntimeDelayMilliseconds(low)
	high = boundedRuntimeDelayMilliseconds(high)
	if high <= low {
		return low
	}
	// Both endpoints are capped to a small positive range, so the inclusive
	// width cannot overflow int or reach rand.Intn with a non-positive bound.
	return low + rand.Intn(high-low+1)
}

// applyIterationPacing applies the configured pacing delay between iterations.
// iterStart is the time the iteration began (used for interval-based pacing).
func applyIterationPacing(cfg PacingRuntimeConfig, iterStart time.Time) {
	applyIterationPacingCtx(context.Background(), cfg, iterStart)
}

// applyIterationPacingCtx is the context-aware variant of applyIterationPacing;
// the pacing sleep is cut short if ctx is cancelled.
func applyIterationPacingCtx(ctx context.Context, cfg PacingRuntimeConfig, iterStart time.Time) {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "none", "":
		// No pacing — start next iteration immediately
		return

	case "fixed_delay":
		if delay := boundedRuntimeDelayMilliseconds(cfg.DelayMs); delay > 0 {
			pacingSleep(ctx, cfg, time.Duration(delay)*time.Millisecond)
		}

	case "random_delay":
		if delay := randomRuntimeDelayMilliseconds(cfg.MinDelayMs, cfg.MaxDelayMs); delay > 0 {
			pacingSleep(ctx, cfg, time.Duration(delay)*time.Millisecond)
		}

	case "fixed_interval":
		if interval := boundedRuntimeDelayMilliseconds(cfg.IntervalMs); interval > 0 {
			elapsed := pacingElapsed(cfg, iterStart)
			target := time.Duration(interval) * time.Millisecond
			if elapsed < target {
				pacingSleep(ctx, cfg, target-elapsed)
			}
		}

	case "random_interval":
		target := time.Duration(randomRuntimeDelayMilliseconds(cfg.MinIntervalMs, cfg.MaxIntervalMs)) * time.Millisecond
		elapsed := pacingElapsed(cfg, iterStart)
		if elapsed < target {
			pacingSleep(ctx, cfg, target-elapsed)
		}
	}
}

func pacingSleep(ctx context.Context, cfg PacingRuntimeConfig, delay time.Duration) {
	if cfg.activeSleep != nil {
		cfg.activeSleep(ctx, delay)
		return
	}
	sleepCtx(ctx, delay)
}

func pacingElapsed(cfg PacingRuntimeConfig, iterStart time.Time) time.Duration {
	if cfg.activeClock != nil {
		elapsed := cfg.activeClock() - cfg.iterStartActive
		if elapsed < 0 {
			return 0
		}
		return elapsed
	}
	return time.Since(iterStart)
}

// PacingRuntimeConfig is the engine-internal pacing config, mirroring dsl.PacingConfig.
type PacingRuntimeConfig struct {
	Type            string
	DelayMs         int
	MinDelayMs      int
	MaxDelayMs      int
	IntervalMs      int
	MinIntervalMs   int
	MaxIntervalMs   int
	TPS             float64
	RateLimiter     *TokenBucket
	activeSleep     func(context.Context, time.Duration) bool
	activeClock     func() time.Duration
	iterStartActive time.Duration
}

type TokenBucket struct {
	tokens   chan struct{}
	interval time.Duration
	done     chan struct{}
	reset    chan struct{}
	mu       sync.Mutex
	paused   bool
	stopOnce sync.Once
}

// maxTokenBucketCapacity bounds the buffered token channel so a pathologically
// large pacing.tps cannot trigger an enormous channel allocation.
const maxTokenBucketCapacity = 1_000_000

func NewTokenBucket(tps float64) *TokenBucket {
	if tps <= 0 || math.IsNaN(tps) || math.IsInf(tps, 0) {
		return nil
	}
	// For very large tps the fill interval truncates to 0. The clamp is load
	// bearing: tokenBucketTokensForElapsed returns 0 for every elapsed value once
	// interval <= 0, so an unclamped bucket would never issue a single credit and
	// every VUser gated on WaitContext (vuser.go, the "tps" pacing branch) would
	// block forever instead of the run failing loudly. fill() drives a
	// time.Timer, which tolerates a non-positive duration by firing immediately
	// — only time.NewTicker panics on one — so the hazard here is silent token
	// starvation, not a crashed fill goroutine.
	interval := time.Duration(float64(time.Second) / tps)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	capacity := int(math.Min(tps*2, maxTokenBucketCapacity)) + 1
	tb := &TokenBucket{
		tokens:   make(chan struct{}, capacity),
		interval: interval,
		done:     make(chan struct{}),
		reset:    make(chan struct{}, 1),
	}
	go tb.fill()
	return tb
}

func (tb *TokenBucket) Wait() {
	<-tb.tokens
}

func (tb *TokenBucket) WaitContext(ctx context.Context) bool {
	if tb == nil {
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-tb.done:
			return false
		case <-tb.tokens:
			tb.mu.Lock()
			paused := tb.paused
			tb.mu.Unlock()
			if !paused {
				return true
			}
		}
	}
}

func (tb *TokenBucket) Stop() {
	if tb == nil {
		return
	}
	tb.stopOnce.Do(func() { close(tb.done) })
}

// SetPaused prevents token production while paused and discards buffered
// credits so resuming cannot release a wall-time-accumulated burst.
func (tb *TokenBucket) SetPaused(paused bool) {
	if tb == nil {
		return
	}
	tb.mu.Lock()
	changed := tb.paused != paused
	tb.paused = paused
	if paused {
		for {
			select {
			case <-tb.tokens:
			default:
				tb.mu.Unlock()
				if changed {
					tb.signalTimerReset()
				}
				return
			}
		}
	}
	tb.mu.Unlock()
	if changed {
		tb.signalTimerReset()
	}
}

func (tb *TokenBucket) signalTimerReset() {
	select {
	case tb.reset <- struct{}{}:
	default:
	}
}

func (tb *TokenBucket) fill() {
	last := time.Now()
	timer := time.NewTimer(tb.interval)
	defer timer.Stop()
	timerC := timer.C
	// nextFire is the absolute deadline the timer is currently armed for. Every
	// re-arm is measured from this deadline, never from the instant the goroutine
	// happened to wake, so the fill cadence stays exactly one interval wide.
	// Re-arming with a fresh tb.interval instead makes each cycle
	// interval+wake_latency long while the credit accounting below floors
	// elapsed/interval, discarding the sub-interval remainder every cycle. That
	// shortfall is one-directional and grows with tps (deficit ≈
	// latency/(interval+latency)), so a bucket configured for 1000 tps delivered
	// closer to 790.
	nextFire := last.Add(tb.interval)
	// pausedProgress carries the portion of the in-flight fill interval that had
	// already elapsed when a pause began. Paused wall time still earns nothing,
	// but resuming charges only the remainder instead of restarting the interval
	// from scratch — otherwise every resume costs a full pacing interval of dead
	// time before the first credit, which is a visible stall at low tps.
	var pausedProgress time.Duration
	// fillPaused mirrors the pause state this goroutine has already applied to
	// the timer. Reset signals are coalesced through a buffered-by-1 channel, so
	// a pause that is immediately undone — the generator service rolls back a
	// failed pause by resuming the same engines microseconds later — can reach
	// this loop as a single signal, or as two signals that both observe the
	// settled running state. Stopping and restarting the timer for a signal that
	// carries no net transition would discard the in-flight interval progress
	// this bookkeeping exists to preserve, costing a full pacing interval of
	// throughput per rolled-back pause.
	fillPaused := false
	for {
		select {
		case <-tb.done:
			return
		case <-tb.reset:
			tb.mu.Lock()
			paused := tb.paused
			tb.mu.Unlock()
			if paused == fillPaused {
				continue
			}
			fillPaused = paused
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			now := time.Now()
			if paused {
				pausedProgress = now.Sub(last)
				if pausedProgress < 0 {
					pausedProgress = 0
				}
				if pausedProgress > tb.interval {
					pausedProgress = tb.interval
				}
				last = now
				timerC = nil
				continue
			}
			remaining := tb.interval - pausedProgress
			if remaining <= 0 {
				remaining = time.Nanosecond
			}
			// Backdate last by the preserved progress so the credit accounting in
			// addTokensForElapsed still sees exactly one whole interval elapse.
			last = now.Add(-pausedProgress)
			pausedProgress = 0
			nextFire = now.Add(remaining)
			timer.Reset(remaining)
			timerC = timer.C
		case now := <-timerC:
			// Advance last by the whole intervals actually credited rather than to
			// the wake time. Anything short of a whole interval stays on the clock
			// and is paid out by a later cycle, so wake latency can never be
			// silently rounded away.
			if issued := tb.addTokensForElapsed(now.Sub(last)); issued > 0 {
				last = last.Add(time.Duration(issued) * tb.interval)
			}
			nextFire = nextFire.Add(tb.interval)
			wait := nextFire.Sub(now)
			if wait <= 0 {
				// A stall swallowed one or more deadlines. Skip forward to the next
				// future deadline — keeping the original phase — instead of arming a
				// run of non-positive timers that would spin the loop. Credit is not
				// lost by the skip: last still trails, so the missed intervals are
				// issued by the accounting above. NewTokenBucket clamps interval
				// positive; the guard keeps a zero interval a (creditless) spin
				// rather than a divide-by-zero panic in this recover-less goroutine.
				if tb.interval > 0 {
					missed := int64(now.Sub(nextFire)/tb.interval) + 1
					nextFire = nextFire.Add(time.Duration(missed) * tb.interval)
				} else {
					nextFire = now
				}
				wait = nextFire.Sub(now)
			}
			timer.Reset(wait)
		}
	}
}

// addTokensForElapsed issues one credit per whole fill interval contained in
// elapsed and returns how many intervals it credited, so fill() can advance its
// accounting clock by exactly that much. Credits that do not fit in the
// buffered channel still count toward the return value: the channel capacity is
// the deliberate burst bound, and holding unbuffered credit on the clock would
// turn a momentarily full buffer into an unbounded backlog that discharges as a
// spike once consumers catch up.
func (tb *TokenBucket) addTokensForElapsed(elapsed time.Duration) int {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if tb.paused {
		return 0
	}
	due := tokenBucketTokensForElapsed(elapsed, tb.interval)
	for i := 0; i < due; i++ {
		select {
		case tb.tokens <- struct{}{}:
		default:
			return due
		}
	}
	return due
}

func tokenBucketTokensForElapsed(elapsed time.Duration, interval time.Duration) int {
	if elapsed <= 0 || interval <= 0 {
		return 0
	}
	due := int(elapsed / interval)
	if due < 1 {
		return 1
	}
	return due
}
