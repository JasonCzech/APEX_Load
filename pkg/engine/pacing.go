package engine

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// sleepCtx sleeps for d, but returns early if ctx is cancelled. This lets
// think-time and between-iteration pacing be interrupted promptly when a VUser
// is scaled/ramped down or the run is hard-stopped, instead of holding load for
// the full sleep duration.
func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	if ctx == nil {
		time.Sleep(d)
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
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

	var ms float64

	switch cfg.Type {
	case "fixed":
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

	// Apply multiplier (1.0 = as recorded, 0.5 = half, 2.0 = double)
	if cfg.Multiplier > 0 && cfg.Multiplier != 1.0 {
		ms *= cfg.Multiplier
	}

	// Apply limit cap
	if cfg.LimitMs > 0 && ms > cfg.LimitMs {
		ms = cfg.LimitMs
	}

	return time.Duration(ms) * time.Millisecond
}

// applyIterationPacing applies the configured pacing delay between iterations.
// iterStart is the time the iteration began (used for interval-based pacing).
func applyIterationPacing(cfg PacingRuntimeConfig, iterStart time.Time) {
	applyIterationPacingCtx(context.Background(), cfg, iterStart)
}

// applyIterationPacingCtx is the context-aware variant of applyIterationPacing;
// the pacing sleep is cut short if ctx is cancelled.
func applyIterationPacingCtx(ctx context.Context, cfg PacingRuntimeConfig, iterStart time.Time) {
	switch cfg.Type {
	case "none", "":
		// No pacing — start next iteration immediately
		return

	case "fixed_delay":
		if cfg.DelayMs > 0 {
			pacingSleep(ctx, cfg, time.Duration(cfg.DelayMs)*time.Millisecond)
		}

	case "random_delay":
		lo := cfg.MinDelayMs
		hi := cfg.MaxDelayMs
		if hi <= lo {
			pacingSleep(ctx, cfg, time.Duration(lo)*time.Millisecond)
			return
		}
		d := lo + rand.Intn(hi-lo+1)
		pacingSleep(ctx, cfg, time.Duration(d)*time.Millisecond)

	case "fixed_interval":
		if cfg.IntervalMs > 0 {
			elapsed := pacingElapsed(cfg, iterStart)
			target := time.Duration(cfg.IntervalMs) * time.Millisecond
			if elapsed < target {
				pacingSleep(ctx, cfg, target-elapsed)
			}
		}

	case "random_interval":
		lo := cfg.MinIntervalMs
		hi := cfg.MaxIntervalMs
		if hi <= lo {
			hi = lo + 1
		}
		target := time.Duration(lo+rand.Intn(hi-lo+1)) * time.Millisecond
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
