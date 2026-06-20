package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func openWorkloadScript(tps float64) *dsl.Script {
	script := &dsl.Script{}
	script.Config.RunLogic.WorkloadModel = "open"
	script.Config.Pacing.Type = "tps"
	script.Config.Pacing.TPS = tps
	return script
}

func TestOpenWorkloadInFlightLimit(t *testing.T) {
	tests := []struct {
		name        string
		script      *dsl.Script
		maxInFlight int
		want        int
	}{
		{name: "nil script unbounded", script: nil, want: 0},
		{name: "derived from tps", script: openWorkloadScript(10), want: 10 * defaultOpenWorkloadInFlightPerTPS},
		{name: "fractional tps floors to per-tps default", script: openWorkloadScript(0.5), want: defaultOpenWorkloadInFlightPerTPS},
		{name: "zero tps unbounded", script: openWorkloadScript(0), want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.maxInFlight > 0 && tc.script != nil {
				tc.script.Config.RunLogic.MaxInFlight = tc.maxInFlight
			}
			if got := openWorkloadInFlightLimit(tc.script); got != tc.want {
				t.Fatalf("openWorkloadInFlightLimit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestOpenWorkloadInFlightLimitExplicitWins(t *testing.T) {
	script := openWorkloadScript(1000) // derived would be 60000
	script.Config.RunLogic.MaxInFlight = 42
	if got := openWorkloadInFlightLimit(script); got != 42 {
		t.Fatalf("explicit MaxInFlight should win: got %d, want 42", got)
	}
}

func TestInFlightLimiterAcquireReleaseShed(t *testing.T) {
	l := newInFlightLimiter(2)
	if l == nil {
		t.Fatal("expected non-nil limiter for positive cap")
	}
	if !l.tryAcquire() || !l.tryAcquire() {
		t.Fatal("expected first two acquires to succeed")
	}
	// At capacity: the next acquire must fail and record a shed.
	if l.tryAcquire() {
		t.Fatal("expected acquire to fail at capacity")
	}
	if l.tryAcquire() {
		t.Fatal("expected acquire to fail at capacity (second attempt)")
	}
	if got := l.Shed(); got != 2 {
		t.Fatalf("expected 2 shed arrivals, got %d", got)
	}
	// Freeing a slot lets exactly one more in.
	l.release()
	if !l.tryAcquire() {
		t.Fatal("expected acquire to succeed after release")
	}
	if l.tryAcquire() {
		t.Fatal("expected acquire to fail again at capacity")
	}
}

func TestInFlightLimiterNilIsUnbounded(t *testing.T) {
	var l *inFlightLimiter // newInFlightLimiter(0) returns nil
	if got := newInFlightLimiter(0); got != nil {
		t.Fatal("expected nil limiter for non-positive cap")
	}
	for i := 0; i < 1000; i++ {
		if !l.tryAcquire() {
			t.Fatal("nil limiter must always grant capacity")
		}
	}
	l.release() // must not panic
	if l.Shed() != 0 {
		t.Fatal("nil limiter must report zero sheds")
	}
	if l.shouldWarn() {
		t.Fatal("nil limiter must never warn")
	}
}

func TestInFlightLimiterShouldWarnOnce(t *testing.T) {
	l := newInFlightLimiter(1)
	if !l.shouldWarn() {
		t.Fatal("first shouldWarn must return true")
	}
	if l.shouldWarn() {
		t.Fatal("subsequent shouldWarn must return false")
	}
}
