package engine

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRunArrivalSchedulerUsesAbsoluteActiveDeadlines(t *testing.T) {
	var active time.Duration
	dispatchedAt := make([]time.Duration, 0, 5)
	runArrivalScheduler(
		context.Background(),
		10,
		5,
		nil,
		func(_ context.Context, delay time.Duration) bool {
			active += delay
			return true
		},
		func() time.Duration { return active },
		func(_ int) {
			dispatchedAt = append(dispatchedAt, active)
			// Simulate material dispatcher overhead. Relative sleeps would add
			// this 25ms to every 100ms arrival interval.
			active += 25 * time.Millisecond
		},
		nil,
	)
	want := []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond, 400 * time.Millisecond}
	if !reflect.DeepEqual(dispatchedAt, want) {
		t.Fatalf("dispatch active times = %v, want %v", dispatchedAt, want)
	}
}

func TestRunArrivalSchedulerCanPreserveSharedActiveOrigin(t *testing.T) {
	active := 150 * time.Millisecond
	dispatched := make([]int, 0, 2)
	var slept time.Duration
	runArrivalSchedulerFromOrigin(
		context.Background(),
		10,
		2,
		nil,
		func(_ context.Context, delay time.Duration) bool {
			slept += delay
			active += delay
			return true
		},
		func() time.Duration { return active },
		true,
		func(index int) { dispatched = append(dispatched, index) },
		nil,
	)
	if !reflect.DeepEqual(dispatched, []int{0, 1}) {
		t.Fatalf("shared-origin dispatches = %v, want overdue arrivals 0 and 1", dispatched)
	}
	if slept != 0 {
		t.Fatalf("shared-origin scheduler slept %v before overdue arrivals", slept)
	}
}

func TestRunArrivalSchedulerBoundsAndAccountsCatchUp(t *testing.T) {
	activeReads := 0
	dispatched := 0
	shedStart := -1
	shedCount := 0
	runArrivalScheduler(
		context.Background(), 1000, 2000, nil,
		func(context.Context, time.Duration) bool { return true },
		func() time.Duration {
			activeReads++
			if activeReads == 1 {
				return 0 // scheduler origin
			}
			return 5 * time.Second // simulated stall after scheduler start
		},
		func(int) { dispatched++ },
		func(first, count int) {
			shedStart = first
			shedCount += count
		},
	)
	if dispatched != maxOpenWorkloadCatchUpBurst {
		t.Fatalf("dispatched catch-up arrivals = %d, want bounded %d", dispatched, maxOpenWorkloadCatchUpBurst)
	}
	if shedStart != maxOpenWorkloadCatchUpBurst || shedCount != 2000-maxOpenWorkloadCatchUpBurst {
		t.Fatalf("shed range = start %d count %d, want start %d count %d", shedStart, shedCount, maxOpenWorkloadCatchUpBurst, 2000-maxOpenWorkloadCatchUpBurst)
	}
}

func TestRunArrivalSchedulerPauseDoesNotCreateBacklog(t *testing.T) {
	var active time.Duration
	wall := time.Duration(0)
	paused := false
	dispatchedAt := make([]time.Duration, 0, 2)
	runArrivalScheduler(
		context.Background(), 10, 2,
		func(context.Context) error {
			if paused {
				wall += 10 * time.Second
				paused = false
			}
			return nil
		},
		func(_ context.Context, delay time.Duration) bool {
			active += delay
			wall += delay
			return true
		},
		func() time.Duration { return active },
		func(index int) {
			dispatchedAt = append(dispatchedAt, active)
			if index == 0 {
				paused = true
			}
		},
		nil,
	)
	if !reflect.DeepEqual(dispatchedAt, []time.Duration{0, 100 * time.Millisecond}) {
		t.Fatalf("dispatch active times across pause = %v", dispatchedAt)
	}
	if wall != 10*time.Second+100*time.Millisecond {
		t.Fatalf("wall time = %v, want pause plus one active interval", wall)
	}
}

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
	for range 2 {
		if !l.tryAcquire() {
			t.Fatal("expected first two acquires to succeed")
		}
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
