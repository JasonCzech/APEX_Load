package engine

import (
	"math"
	"math/rand"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func applyThinkTime(cfg protocol.ThinkTimeConfig) {
	d := computeThinkTime(cfg)
	if d > 0 {
		time.Sleep(d)
	}
}

func computeThinkTime(cfg protocol.ThinkTimeConfig) time.Duration {
	switch cfg.Type {
	case "fixed":
		return time.Duration(cfg.MeanMs) * time.Millisecond

	case "gaussian":
		v := rand.NormFloat64()*cfg.StdMs + cfg.MeanMs
		if cfg.MinMs > 0 && v < cfg.MinMs {
			v = cfg.MinMs
		}
		if cfg.MaxMs > 0 && v > cfg.MaxMs {
			v = cfg.MaxMs
		}
		return time.Duration(math.Max(0, v)) * time.Millisecond

	case "uniform":
		lo := cfg.MinMs
		hi := cfg.MaxMs
		if hi <= lo {
			return time.Duration(lo) * time.Millisecond
		}
		v := lo + rand.Float64()*(hi-lo)
		return time.Duration(v) * time.Millisecond

	default:
		return 0
	}
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
