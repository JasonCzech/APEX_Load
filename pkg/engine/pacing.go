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
	tokens          chan struct{}
	interval        time.Duration
	done            chan struct{}
	mu              sync.Mutex
	paused          bool
	resumeNotBefore time.Time
	stopOnce        sync.Once
}

// maxTokenBucketCapacity bounds the buffered token channel so a pathologically
// large pacing.tps cannot trigger an enormous channel allocation.
const maxTokenBucketCapacity = 1_000_000

func NewTokenBucket(tps float64) *TokenBucket {
	if tps <= 0 || math.IsNaN(tps) || math.IsInf(tps, 0) {
		return nil
	}
	// For very large tps the fill interval truncates to 0, which makes
	// time.NewTicker panic ("non-positive interval for NewTicker") inside the
	// recover-less fill goroutine, crashing the worker. Clamp to 1ns, mirroring
	// runArrivalScheduler.
	interval := time.Duration(float64(time.Second) / tps)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	capacity := int(math.Min(tps*2, maxTokenBucketCapacity)) + 1
	tb := &TokenBucket{
		tokens:   make(chan struct{}, capacity),
		interval: interval,
		done:     make(chan struct{}),
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
	tb.paused = paused
	if paused {
		for {
			select {
			case <-tb.tokens:
			default:
				tb.mu.Unlock()
				return
			}
		}
	}
	tb.resumeNotBefore = time.Now().Add(tb.interval)
	tb.mu.Unlock()
}

func (tb *TokenBucket) fill() {
	ticker := time.NewTicker(tb.interval)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-tb.done:
			return
		case now := <-ticker.C:
			elapsed := now.Sub(last)
			last = now
			tb.addTokensForElapsedAt(elapsed, now)
		}
	}
}

func (tb *TokenBucket) addTokensForElapsed(elapsed time.Duration) {
	tb.addTokensForElapsedAt(elapsed, time.Time{})
}

func (tb *TokenBucket) addTokensForElapsedAt(elapsed time.Duration, now time.Time) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if tb.paused {
		return
	}
	if !now.IsZero() && !tb.resumeNotBefore.IsZero() {
		if now.Before(tb.resumeNotBefore) {
			return
		}
		tb.resumeNotBefore = time.Time{}
	}
	due := tokenBucketTokensForElapsed(elapsed, tb.interval)
	for i := 0; i < due; i++ {
		select {
		case tb.tokens <- struct{}{}:
		default:
			return
		}
	}
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
