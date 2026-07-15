package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestComputeThinkTime_Fixed(t *testing.T) {
	d := computeThinkTime(protocol.ThinkTimeConfig{Type: "fixed", MeanMs: 500})
	if d != 500*time.Millisecond {
		t.Errorf("got %v, want 500ms", d)
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

func TestTokenBucketDoesNotAccumulatePausedCredits(t *testing.T) {
	tb := NewTokenBucket(20) // one token every 50ms
	defer tb.Stop()
	time.Sleep(70 * time.Millisecond)
	tb.SetPaused(true)
	time.Sleep(120 * time.Millisecond)
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
