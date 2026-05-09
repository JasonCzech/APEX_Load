package engine

import (
	"math"
	"math/rand"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

// applyThinkTime sleeps for a computed think time duration, respecting
// multiplier, limit, and ignore settings.
func applyThinkTime(cfg protocol.ThinkTimeConfig) {
	d := computeThinkTime(cfg)
	if d > 0 {
		time.Sleep(d)
	}
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
	switch cfg.Type {
	case "none", "":
		// No pacing — start next iteration immediately
		return

	case "fixed_delay":
		if cfg.DelayMs > 0 {
			time.Sleep(time.Duration(cfg.DelayMs) * time.Millisecond)
		}

	case "random_delay":
		lo := cfg.MinDelayMs
		hi := cfg.MaxDelayMs
		if hi <= lo {
			time.Sleep(time.Duration(lo) * time.Millisecond)
			return
		}
		d := lo + rand.Intn(hi-lo+1)
		time.Sleep(time.Duration(d) * time.Millisecond)

	case "fixed_interval":
		if cfg.IntervalMs > 0 {
			elapsed := time.Since(iterStart)
			target := time.Duration(cfg.IntervalMs) * time.Millisecond
			if elapsed < target {
				time.Sleep(target - elapsed)
			}
		}

	case "random_interval":
		lo := cfg.MinIntervalMs
		hi := cfg.MaxIntervalMs
		if hi <= lo {
			hi = lo + 1
		}
		target := time.Duration(lo+rand.Intn(hi-lo+1)) * time.Millisecond
		elapsed := time.Since(iterStart)
		if elapsed < target {
			time.Sleep(target - elapsed)
		}
	}
}

// PacingRuntimeConfig is the engine-internal pacing config, mirroring dsl.PacingConfig.
type PacingRuntimeConfig struct {
	Type          string
	DelayMs       int
	MinDelayMs    int
	MaxDelayMs    int
	IntervalMs    int
	MinIntervalMs int
	MaxIntervalMs int
}

type TokenBucket struct {
	tokens   chan struct{}
	interval time.Duration
	done     chan struct{}
}

func NewTokenBucket(tps float64) *TokenBucket {
	tb := &TokenBucket{
		tokens:   make(chan struct{}, int(tps*2)+1),
		interval: time.Duration(float64(time.Second) / tps),
		done:     make(chan struct{}),
	}
	go tb.fill()
	return tb
}

func (tb *TokenBucket) Wait() {
	<-tb.tokens
}

func (tb *TokenBucket) Stop() {
	close(tb.done)
}

func (tb *TokenBucket) fill() {
	ticker := time.NewTicker(tb.interval)
	defer ticker.Stop()
	for {
		select {
		case <-tb.done:
			return
		case <-ticker.C:
			select {
			case tb.tokens <- struct{}{}:
			default:
			}
		}
	}
}
