package engine

import (
	"context"
	"fmt"
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
	const interval = 100 * time.Millisecond
	tb := NewTokenBucket(10) // one token every 100ms
	defer tb.Stop()

	// Phase-lock onto the fill cadence rather than racing a fixed sleep against
	// it. Resuming charges only the unspent remainder of the in-flight fill
	// interval, so the delay before the first post-resume credit is
	// interval-minus-progress. With a fixed pre-pause sleep the immediate budget
	// below therefore has a margin that depends on where that sleep happened to
	// land inside the fill cadence — a sleep returning late on a loaded runner
	// pushes the first credit inside the budget and reports a bogus
	// "released an accumulated credit" failure. Waiting for the first credit to
	// be issued *without consuming it* pins the pause to just after a fill tick,
	// and leaves SetPaused a buffered credit to drain, which is what the length
	// assertion below exists to pin.
	pollDeadline := time.Now().Add(10 * interval)
	for len(tb.tokens) == 0 {
		if time.Now().After(pollDeadline) {
			t.Fatal("token bucket issued no credit within ten fill intervals")
		}
		time.Sleep(time.Millisecond)
	}
	tb.SetPaused(true)
	time.Sleep(2*interval + 20*time.Millisecond)
	// This reads 0 for any bucket whose SetPaused still discards buffered
	// credits: fill() stops ticking once the pause is applied, so nothing can be
	// added back afterwards. What it pins is therefore the drain in SetPaused,
	// not the pause guard in addTokensForElapsed — that guard is covered by
	// TestTokenBucketAddsNoTokensWhilePaused.
	if got := len(tb.tokens); got != 0 {
		t.Fatalf("paused token bucket buffered %d credits, want 0", got)
	}
	tb.SetPaused(false)

	// Essentially the whole fill interval was still unspent when the pause began,
	// so any credit arriving this soon after resume is one accumulated during the
	// paused window.
	immediateCtx, immediateCancel := context.WithTimeout(context.Background(), interval/4)
	defer immediateCancel()
	if tb.WaitContext(immediateCtx) {
		t.Fatal("token bucket released an accumulated credit immediately after resume")
	}
	eventualCtx, eventualCancel := context.WithTimeout(context.Background(), 3*interval)
	defer eventualCancel()
	if !tb.WaitContext(eventualCtx) {
		t.Fatal("token bucket did not issue a newly earned credit after resume")
	}
}

// tokenBucketRateTolerance bounds how far the delivered credit count may sit
// from the configured rate over the measurement window in
// TestTokenBucketDeliversConfiguredRate. It has to be tight enough to catch the
// cadence regression this test exists for — re-arming the fill timer from the
// wake instant instead of the deadline truncated ~16-21% of the configured rate
// away at these rates — and loose enough that a busy runner does not flake.
// Carrying the sub-interval remainder makes a late wake self-correcting rather
// than lossy, so the residual error is a fraction of one interval per window;
// 10% leaves a wide margin above that while still failing every rate the
// regression produced (79-85% delivered).
const tokenBucketRateTolerance = 0.10

// TestTokenBucketDeliversConfiguredRate is the end-to-end rate assertion the
// package was missing: the other bucket tests only pin single-credit timing, so
// a bucket that delivered a fifth fewer credits than configured passed all of
// them. The bucket is the sole producer of iteration credit for "tps" pacing
// (vuser.go gates every iteration on WaitContext), so a shortfall here is a
// silent, unreported shortfall in offered load for the whole run.
func TestTokenBucketDeliversConfiguredRate(t *testing.T) {
	const window = time.Second
	for _, tps := range []float64{100, 500, 1000} {
		t.Run(fmt.Sprintf("tps_%g", tps), func(t *testing.T) {
			tb := NewTokenBucket(tps)
			if tb == nil {
				t.Fatalf("tps=%g: NewTokenBucket returned nil", tps)
			}
			defer tb.Stop()
			start := time.Now()

			// Drain continuously so the consumer is never the bottleneck. A
			// consumer that lets the buffered channel fill would measure the
			// channel capacity instead of the producer's rate.
			delivered := 0
			stop := make(chan struct{})
			consumed := make(chan struct{})
			go func() {
				defer close(consumed)
				for {
					select {
					case <-stop:
						return
					case <-tb.tokens:
						delivered++
					}
				}
			}()

			time.Sleep(window)
			elapsed := time.Since(start)
			close(stop)
			<-consumed

			expected := tps * elapsed.Seconds()
			ratio := float64(delivered) / expected
			t.Logf("tps=%g window=%s delivered=%d expected=%.1f (%.1f%% of configured)",
				tps, elapsed.Round(time.Millisecond), delivered, expected, ratio*100)
			if ratio < 1-tokenBucketRateTolerance || ratio > 1+tokenBucketRateTolerance {
				t.Fatalf("tps=%g: delivered %d credits in %s, want %.1f ±%.0f%% (got %.1f%% of the configured rate)",
					tps, delivered, elapsed.Round(time.Millisecond), expected, tokenBucketRateTolerance*100, ratio*100)
			}
		})
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
// the fill interval to 0) still leaves a positive fill interval. fill() is
// timer-driven and would not crash on a zero interval, but
// tokenBucketTokensForElapsed returns 0 for every elapsed value once the
// interval is non-positive, so an unclamped bucket issues no credit at all and
// every WaitContext caller blocks forever.
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
		// Give fill() a moment to start; with the interval clamped it must issue
		// credit rather than starve.
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

// TestNewTokenBucket_SmallTPS covers the opposite end of the conversion from
// TestNewTokenBucket_LargeTPS: a tps small enough that float64(time.Second)/tps
// exceeds MaxInt64 (anything below ~1.0842e-10). Go leaves an out-of-range
// float-to-int conversion implementation defined — arm64's FCVTZSD saturates to
// MaxInt64, amd64's CVTTSD2SQ returns the integer-indefinite value MinInt64 — so
// truncating first and clamping non-positive results afterwards mistakes the
// amd64 overflow for the large-tps truncation case and hands back 1ns. That
// inverts the configured rate from "one iteration per thousands of years" into
// "one credit per nanosecond", failing the closed-model rate limiter wide open
// on amd64 generators while arm64 behaves. Range-checking before the conversion
// makes both architectures agree.
func TestNewTokenBucket_SmallTPS(t *testing.T) {
	saturated := time.Duration(math.MaxInt64)
	for _, tc := range []struct {
		tps  float64
		want time.Duration
	}{
		// Overflowing quotients: the interval must saturate, never invert.
		{tps: 1e-11, want: saturated},
		{tps: 1e-300, want: saturated},
		{tps: math.SmallestNonzeroFloat64, want: saturated},
		{tps: 1.0842021724855044e-10, want: saturated},
		// Just inside the representable range: exact, not saturated or clamped.
		{tps: 1e-9, want: time.Duration(1e18)},
		{tps: 1, want: time.Second},
		{tps: 1000, want: time.Millisecond},
	} {
		if got := tokenBucketFillInterval(tc.tps); got != tc.want {
			t.Errorf("tokenBucketFillInterval(%g) = %v (%d ns), want %v (%d ns)",
				tc.tps, got, int64(got), tc.want, int64(tc.want))
		}
	}

	// End to end through the constructor: a bucket configured for one iteration
	// per ~3170 years must not hand out credit during the test.
	const tinyTPS = 1e-11
	tb := NewTokenBucket(tinyTPS)
	if tb == nil {
		t.Fatalf("tps=%g: NewTokenBucket returned nil", tinyTPS)
	}
	defer tb.Stop()
	if tb.interval < 24*time.Hour {
		t.Fatalf("tps=%g: fill interval %v collapsed; the rate limiter is failing open", tinyTPS, tb.interval)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if tb.WaitContext(ctx) {
		t.Fatalf("tps=%g: bucket issued a credit within 150ms; interval=%v", tinyTPS, tb.interval)
	}
	if got := len(tb.tokens); got != 0 {
		t.Fatalf("tps=%g: bucket buffered %d credits; interval=%v", tinyTPS, got, tb.interval)
	}
}

func TestRecordedPercentageThinkTime(t *testing.T) {
	global := protocol.ThinkTimeConfig{Type: "random_percentage", MinPercent: 50, MaxPercent: 150}
	for _, recorded := range []float64{400, 4000} {
		cfg := resolveActionThinkTime(global, dsl.ThinkTimeConfig{Type: "fixed", MeanMs: recorded})
		for i := 0; i < 100; i++ {
			got := computeThinkTime(cfg)
			if got < time.Duration(recorded/2*float64(time.Millisecond)) || got > time.Duration(recorded*1.5*float64(time.Millisecond)) {
				t.Fatalf("%g ms base produced %v", recorded, got)
			}
		}
	}
	for _, tc := range []struct {
		name                      string
		min, max, multiplier, cap float64
		ignore                    bool
		want                      time.Duration
	}{
		{name: "equal", min: 50, max: 50, want: 2 * time.Second},
		{name: "zero", want: 0},
		{name: "multiply", min: 50, max: 50, multiplier: 2, want: 4 * time.Second},
		{name: "cap", min: 150, max: 150, cap: 1000, want: time.Second},
		{name: "ignore", min: 150, max: 150, ignore: true},
		{name: "reversed", min: 150, max: 50},
		{name: "negative", min: -1, max: 100},
		{name: "oversize", min: 0, max: 10001},
		{name: "nan", min: math.NaN(), max: 100},
		{name: "inf", min: 0, max: math.Inf(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			global.MinPercent, global.MaxPercent, global.Multiplier, global.LimitMs, global.Ignore = tc.min, tc.max, tc.multiplier, tc.cap, tc.ignore
			got := computeThinkTime(resolveActionThinkTime(global, dsl.ThinkTimeConfig{Type: "recorded", MeanMs: 4000}))
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	if got := computeThinkTime(resolveActionThinkTime(protocol.ThinkTimeConfig{Type: "random_percentage", MinPercent: 50, MaxPercent: 50}, dsl.ThinkTimeConfig{Type: "uniform", MinMs: 100, MaxMs: 100})); got != 100*time.Millisecond {
		t.Fatalf("explicit distribution changed: %v", got)
	}
}

func TestRandomPercentageLoadAndReplayRespectRecordedAction(t *testing.T) {
	// A huge recorded delay scaled to zero must complete even with a canceled
	// context: neither load nor replay may accidentally use the unscaled delay.
	action := dsl.Action{ThinkTimeAfter: true, ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 3600000}}
	vu := &VUser{Script: &dsl.Script{Config: dsl.ScriptConfig{ThinkTime: dsl.ThinkTimeConfig{Type: "random_percentage"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !vu.applyActionThinkTimeConfig(ctx, action.ThinkTimeAfter, &action.ThinkTime) {
		t.Fatal("load ignored zero percentage")
	}
	if !applyReplayActionThinkTime(ctx, vu, action, false, protocol.ThinkTimeConfig{Type: "random_percentage"}) {
		t.Fatal("replay ignored zero percentage")
	}
	// Nonzero percentages really wait and remain interruptible.
	vu.Script.Config.ThinkTime.MinPercent, vu.Script.Config.ThinkTime.MaxPercent = 50, 150
	if vu.applyActionThinkTimeConfig(ctx, action.ThinkTimeAfter, &action.ThinkTime) {
		t.Fatal("load lost nonzero percentage wait")
	}
	if applyReplayActionThinkTime(ctx, vu, action, false, protocol.ThinkTimeConfig{Type: "random_percentage", MinPercent: 50, MaxPercent: 150}) {
		t.Fatal("replay lost nonzero percentage wait")
	}
}

func TestEditedPercentagePolicyCapsRecordedCallsIndependently(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy protocol.ThinkTimeConfig
		want   time.Duration
	}{
		{"percentage with cap", protocol.ThinkTimeConfig{Type: "random_percentage", MinPercent: 100, MaxPercent: 100, LimitMs: 3000}, 5 * time.Second},
		{"percentage with smaller cap", protocol.ThinkTimeConfig{Type: "random_percentage", MinPercent: 100, MaxPercent: 100, LimitMs: 2000}, 4 * time.Second},
		{"percentage with multiplier", protocol.ThinkTimeConfig{Type: "random_percentage", MinPercent: 50, MaxPercent: 50, Multiplier: 2, LimitMs: 3000}, 5 * time.Second},
		{"ignored", protocol.ThinkTimeConfig{Type: "random_percentage", MinPercent: 100, MaxPercent: 100, Ignore: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var total time.Duration
			for _, recorded := range []float64{2000, 3000} {
				total += computeThinkTime(resolveActionThinkTime(tc.policy, dsl.ThinkTimeConfig{Type: "fixed", MeanMs: recorded}))
			}
			if total != tc.want {
				t.Fatalf("total=%v, want %v", total, tc.want)
			}
		})
	}
}

func TestResolveActionThinkTimeForceIgnoresScriptPolicy(t *testing.T) {
	global := protocol.ThinkTimeConfig{Type: "random_percentage", MinPercent: 10, MaxPercent: 20, Multiplier: 0.5, LimitMs: 100, Ignore: true}
	cfg := resolveActionThinkTime(global, dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 2000, Force: true})
	if got := computeThinkTime(cfg); got != 2*time.Second {
		t.Fatalf("forced delay = %s, want exactly 2s regardless of ignore, multiplier, limit and percentage", got)
	}
	if got := computeThinkTime(resolveActionThinkTime(global, dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 2000})); got != 0 {
		t.Fatalf("unforced delay = %s, want the script's ignore setting to apply", got)
	}
}

func TestReplayIgnoreThinkTimeStillWaitsForForcedDelay(t *testing.T) {
	// A canceled context makes an attempted wait report interruption, which
	// shows whether replay tried to wait at all.
	vu := &VUser{Script: &dsl.Script{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	forced := dsl.Action{ThinkTimeAfter: true, ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1000, Force: true}}
	if applyReplayActionThinkTime(ctx, vu, forced, true, protocol.ThinkTimeConfig{}) {
		t.Fatal("replay skipped a forced delay when ignoring think time")
	}
	forced.ThinkTime.Force = false
	if !applyReplayActionThinkTime(ctx, vu, forced, true, protocol.ThinkTimeConfig{}) {
		t.Fatal("replay waited for an unforced delay while ignoring think time")
	}
}
