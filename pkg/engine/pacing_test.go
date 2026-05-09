package engine

import (
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
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
