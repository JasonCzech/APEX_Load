package engine

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestComputeThinkTime_Fixed(t *testing.T) {
	d := computeThinkTime(protocol.ThinkTimeConfig{Type: "fixed", MeanMs: 500})
	if d != 500*time.Millisecond {
		t.Errorf("got %v, want 500ms", d)
	}
}

func TestWaitForDelayOrCancellationFailsClosedWhenBothAreReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for iteration := 0; iteration < 1000; iteration++ {
		timer := make(chan time.Time, 1)
		timer <- time.Now()
		if _, completed := waitForDelayOrCancellation(ctx, time.Second, time.Now(), timer); completed {
			t.Fatalf("iteration %d reported a completed delay after cancellation", iteration)
		}
	}

	timer := make(chan time.Time, 1)
	timer <- time.Now()
	if elapsed, completed := waitForDelayOrCancellation(context.Background(), time.Second, time.Now(), timer); !completed || elapsed != time.Second {
		t.Fatalf("uncanceled ready timer = (%s, %v), want (1s, true)", elapsed, completed)
	}
}

func TestComputeThinkTimePreservesFractionalMilliseconds(t *testing.T) {
	for _, thinkType := range []string{"fixed", "recorded"} {
		t.Run(thinkType, func(t *testing.T) {
			got := computeThinkTime(protocol.ThinkTimeConfig{Type: thinkType, MeanMs: 1.5})
			if got != 1500*time.Microsecond {
				t.Fatalf("fractional %s think time = %s, want 1.5ms", thinkType, got)
			}
		})
	}
}

func TestTimingTypesMatchCaseInsensitiveValidation(t *testing.T) {
	if got := computeThinkTime(protocol.ThinkTimeConfig{Type: " Fixed ", MeanMs: 7}); got != 7*time.Millisecond {
		t.Fatalf("mixed-case fixed think time = %s, want 7ms", got)
	}
	var paced time.Duration
	applyIterationPacingCtx(context.Background(), PacingRuntimeConfig{
		Type:    " Fixed_Delay ",
		DelayMs: 9,
		activeSleep: func(_ context.Context, delay time.Duration) bool {
			paced = delay
			return true
		},
	}, time.Now())
	if paced != 9*time.Millisecond {
		t.Fatalf("mixed-case fixed delay = %s, want 9ms", paced)
	}
}

func TestComputeThinkTimeBoundsNonFiniteAndOversizedValues(t *testing.T) {
	if got := computeThinkTime(protocol.ThinkTimeConfig{Type: "fixed", MeanMs: math.Inf(1)}); got != 0 {
		t.Fatalf("non-finite think time = %s, want zero", got)
	}
	got := computeThinkTime(protocol.ThinkTimeConfig{Type: "fixed", MeanMs: float64(dsl.MaxConfiguredProtocolTimeoutMS), Multiplier: 100})
	if got != dsl.MaxConfiguredProtocolTimeout {
		t.Fatalf("oversized multiplied think time = %s, want %s", got, dsl.MaxConfiguredProtocolTimeout)
	}
}

func TestRandomPacingBoundsExtremeRangesWithoutPanic(t *testing.T) {
	for _, pacingType := range []string{"random_delay", "random_interval"} {
		t.Run(pacingType, func(t *testing.T) {
			var observed time.Duration
			cfg := PacingRuntimeConfig{Type: pacingType, activeSleep: func(_ context.Context, delay time.Duration) bool {
				observed = delay
				return true
			}}
			if pacingType == "random_delay" {
				cfg.MinDelayMs = math.MinInt
				cfg.MaxDelayMs = math.MaxInt
			} else {
				cfg.MinIntervalMs = math.MinInt
				cfg.MaxIntervalMs = math.MaxInt
			}
			applyIterationPacingCtx(context.Background(), cfg, time.Now())
			if observed < 0 || observed > dsl.MaxConfiguredProtocolTimeout {
				t.Fatalf("extreme pacing delay = %s, want bounded non-negative delay", observed)
			}
		})
	}
}

func TestComputeThinkTime_Ignore(t *testing.T) {
	d := computeThinkTime(protocol.ThinkTimeConfig{Type: "fixed", MeanMs: 500, Ignore: true})
	if d != 0 {
		t.Errorf("got %v, want 0 (ignored)", d)
	}
}

func TestComputeThinkTime_Multiplier(t *testing.T) {
	d := computeThinkTime(protocol.ThinkTimeConfig{Type: "fixed", MeanMs: 1000, Multiplier: 0.5})
	if d != 500*time.Millisecond {
		t.Errorf("got %v, want 500ms (0.5x multiplier)", d)
	}
}

func TestComputeThinkTime_ZeroMultiplierUsesInheritedDefault(t *testing.T) {
	d := computeThinkTime(protocol.ThinkTimeConfig{Type: "recorded", MeanMs: 250})
	if d != 250*time.Millisecond {
		t.Errorf("got %v, want 250ms (omitted multiplier inherits 1x)", d)
	}
}

func TestComputeThinkTime_Limit(t *testing.T) {
	d := computeThinkTime(protocol.ThinkTimeConfig{Type: "fixed", MeanMs: 5000, LimitMs: 2000})
	if d != 2000*time.Millisecond {
		t.Errorf("got %v, want 2000ms (capped by limit)", d)
	}
}

func TestComputeThinkTime_Uniform(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := computeThinkTime(protocol.ThinkTimeConfig{Type: "uniform", MinMs: 100, MaxMs: 200})
		if d < 100*time.Millisecond || d > 200*time.Millisecond {
			t.Fatalf("uniform think time %v out of range [100ms, 200ms]", d)
		}
	}
}

func TestComputeThinkTime_Gaussian(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := computeThinkTime(protocol.ThinkTimeConfig{
			Type:   "gaussian",
			MeanMs: 500,
			StdMs:  100,
			MinMs:  200,
			MaxMs:  800,
		})
		if d < 200*time.Millisecond || d > 800*time.Millisecond {
			t.Fatalf("gaussian think time %v out of clamped range [200ms, 800ms]", d)
		}
	}
}

func TestComputeThinkTime_UnknownType(t *testing.T) {
	d := computeThinkTime(protocol.ThinkTimeConfig{Type: "unknown", MeanMs: 500})
	if d != 0 {
		t.Errorf("unknown type should return 0, got %v", d)
	}
}

func TestComputeThinkTime_UniformEqualBounds(t *testing.T) {
	d := computeThinkTime(protocol.ThinkTimeConfig{Type: "uniform", MinMs: 100, MaxMs: 100})
	if d != 100*time.Millisecond {
		t.Errorf("equal bounds should return MinMs, got %v", d)
	}
}

func TestTokenBucket(t *testing.T) {
	tb := NewTokenBucket(100)
	defer tb.Stop()

	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		tb.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Error("token bucket Wait() did not return in time")
	}
}

func TestTokenBucketAddsTokensForElapsedIntervals(t *testing.T) {
	tb := &TokenBucket{
		tokens:   make(chan struct{}, 10),
		interval: time.Millisecond,
	}
	tb.addTokensForElapsed(5*time.Millisecond + 500*time.Microsecond)

	drained := 0
	for {
		select {
		case <-tb.tokens:
			drained++
		default:
			if drained != 5 {
				t.Fatalf("drained tokens = %d, want 5", drained)
			}
			return
		}
	}
}

// TestTokenBucketAddsNoTokensWhilePaused covers the pause guard inside
// addTokensForElapsed directly, because no end-to-end pause test can. fill()
// drops its timer channel when it applies a pause, so a timer never fires during
// a paused window and the interleaving the guard exists for is never provoked:
// fill() can already have received from the timer and be blocked on tb.mu inside
// addTokensForElapsed when SetPaused(true) takes the lock, flips paused and
// drains. Without the guard that in-flight tick buffers a credit *after* the
// drain, where it sits for the whole paused window and is released the instant
// WaitContext stops seeing paused — the burst the pause exists to prevent.
// Calling addTokensForElapsed directly pins the guard without racing for that
// window.
func TestTokenBucketAddsNoTokensWhilePaused(t *testing.T) {
	tb := &TokenBucket{
		tokens:   make(chan struct{}, 10),
		interval: time.Millisecond,
	}
	tb.paused = true
	tb.addTokensForElapsed(5 * tb.interval)
	if got := len(tb.tokens); got != 0 {
		t.Fatalf("paused token bucket buffered %d credits from an in-flight tick, want 0", got)
	}

	// The same call on a running bucket has to issue credit, otherwise the
	// assertion above would hold for a bucket that simply never earns tokens for
	// this input and would stop meaning anything.
	tb.paused = false
	tb.addTokensForElapsed(5 * tb.interval)
	if got := len(tb.tokens); got != 5 {
		t.Fatalf("running token bucket issued %d credits for 5 elapsed intervals, want 5", got)
	}
}

func TestTokenBucketDoesNotAccumulatePausedCredits(t *testing.T) {
	tb := NewTokenBucket(20) // one token every 50ms
	defer tb.Stop()
	time.Sleep(70 * time.Millisecond)
	tb.SetPaused(true)
	time.Sleep(120 * time.Millisecond)
	// This reads 0 for any bucket whose SetPaused still discards buffered
	// credits: fill() stops ticking once the pause is applied, so nothing can be
	// added back afterwards. What it pins is therefore the drain in SetPaused,
	// not the pause guard in addTokensForElapsed — that guard is covered by
	// TestTokenBucketAddsNoTokensWhilePaused.
	if got := len(tb.tokens); got != 0 {
		t.Fatalf("paused token bucket buffered %d credits, want 0", got)
	}
	tb.SetPaused(false)

	immediateCtx, immediateCancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer immediateCancel()
	if tb.WaitContext(immediateCtx) {
		t.Fatal("token bucket released an accumulated credit immediately after resume")
	}
	eventualCtx, eventualCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer eventualCancel()
	if !tb.WaitContext(eventualCtx) {
		t.Fatal("token bucket did not issue a newly earned credit after resume")
	}
}

// TestTokenBucketResumePreservesPrePauseIntervalProgress pins the soft-pause
// contract from the other side of TestTokenBucketDoesNotAccumulatePausedCredits:
// pausing must not grant credit for wall time spent paused, but it also must not
// throw away the portion of the current fill interval that had already elapsed.
// Resuming a slow bucket used to cost a full extra interval of dead time before
// the first paced iteration could run.
func TestTokenBucketResumePreservesPrePauseIntervalProgress(t *testing.T) {
	tb := NewTokenBucket(2.5) // one token every 400ms
	defer tb.Stop()

	// Burn most of the first fill interval, then pause before it elapses. The
	// credit guard has to run before SetPaused(true): pausing drains tb.tokens,
	// so a check placed after the pause reads 0 no matter how early the bucket
	// started issuing credit and can never observe the regression it names.
	time.Sleep(300 * time.Millisecond)
	if got := len(tb.tokens); got != 0 {
		t.Fatalf("token bucket issued %d credits before the first interval elapsed", got)
	}
	tb.SetPaused(true)
	time.Sleep(150 * time.Millisecond)

	tb.SetPaused(false)
	start := time.Now()
	// Roughly 100ms of the interval remained when the pause began, so the first
	// post-resume credit must arrive well inside another full 400ms interval.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if !tb.WaitContext(ctx) {
		t.Fatalf("no credit within %v of resume: pausing restarted the fill interval from scratch", time.Since(start))
	}
}

// TestTokenBucketIgnoresResetWithoutAPauseTransition covers the coalescing
// window in the reset signal. SetPaused writes the new state and then pokes a
// buffered-by-1 channel, so a pause that is immediately undone — the generator
// service rolls back a failed pause by resuming the same engines microseconds
// later, see rollbackPauseParticipants — reaches the fill goroutine either as a
// single signal or as two signals that both observe the settled running state.
// A reset carrying no net transition must leave the in-flight fill interval
// alone; restarting it there costs a full pacing interval of throughput for a
// pause that never actually took effect.
func TestTokenBucketIgnoresResetWithoutAPauseTransition(t *testing.T) {
	tb := NewTokenBucket(2.5) // one token every 400ms
	defer tb.Stop()

	// Burn most of the first fill interval, then deliver reset signals that carry
	// no pause transition, exactly as a pause/rollback pair does.
	time.Sleep(300 * time.Millisecond)
	if got := len(tb.tokens); got != 0 {
		t.Fatalf("token bucket issued %d credits before the first interval elapsed", got)
	}
	tb.signalTimerReset()
	tb.signalTimerReset()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if !tb.WaitContext(ctx) {
		t.Fatalf("no credit within %v of a no-op reset: the fill interval was restarted from scratch", time.Since(start))
	}
}

// TestNewTokenBucket_LargeTPS ensures a very large pacing.tps (which truncates
// the fill interval to 0) does not panic the fill goroutine via NewTicker(0).
func TestNewTokenBucket_LargeTPS(t *testing.T) {
	for _, tps := range []float64{1e9, 2e9, 1e10, 1e15} {
		tb := NewTokenBucket(tps)
		if tb == nil {
			t.Fatalf("tps=%g: NewTokenBucket returned nil", tps)
		}
		if cap(tb.tokens) > maxTokenBucketCapacity+1 {
			t.Errorf("tps=%g: token channel cap %d exceeds bound %d", tps, cap(tb.tokens), maxTokenBucketCapacity+1)
		}
		if tb.interval <= 0 {
			t.Errorf("tps=%g: interval %v not clamped to a positive value", tps, tb.interval)
		}
		// Give fill() a moment to start; it must not panic on NewTicker(0).
		done := make(chan struct{})
		go func() {
			tb.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(200 * time.Millisecond):
			t.Errorf("tps=%g: Wait() did not return; fill goroutine likely failed", tps)
		}
		tb.Stop()
	}
}
