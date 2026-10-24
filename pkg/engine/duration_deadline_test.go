package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func durationDeadlineTestEngine(t *testing.T, duration time.Duration, terminationMode string) *MultiGroupEngine {
	t.Helper()
	return durationDeadlineBaselineEngine(t, duration, 0, terminationMode)
}

func durationDeadlineBaselineEngine(t *testing.T, duration, preBaseline time.Duration, terminationMode string) *MultiGroupEngine {
	t.Helper()
	script := &dsl.Script{Name: "script", Protocol: "stub", Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}}}
	if terminationMode == dsl.TerminationIterations {
		script.Config.RunLogic.Iterations = 1
		duration = 0
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		TerminationMode: terminationMode,
		Groups:          []GroupConfig{{Name: "group", Script: script, VUsers: 1}},
		Duration:        duration,
		PreBaseline:     preBaseline,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	return mge
}

func runDurationDeadlineEngine(t *testing.T, mge *MultiGroupEngine, whileRunning func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := mge.Run(ctx)
		done <- err
	}()
	select {
	case <-mge.Ready():
	case <-ctx.Done():
		t.Fatal("engine never became ready")
	}
	if whileRunning != nil {
		whileRunning()
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// DurationDeadline is the generator's proof that a run ended because its
// duration ran out (the RUN_TERMINAL_CAUSE_DURATION_ELAPSED cause).
func TestDurationDeadlineReportsOnlyAnElapsedDuration(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		mge := durationDeadlineTestEngine(t, 150*time.Millisecond, "")
		runDurationDeadlineEngine(t, mge, nil)
		if target, reached := mge.DurationDeadline(); !reached || target != 150*time.Millisecond {
			t.Fatalf("DurationDeadline = (%s, %v), want (150ms, true)", target, reached)
		}
	})
	t.Run("adjusted duration", func(t *testing.T) {
		mge := durationDeadlineTestEngine(t, time.Minute, "")
		runDurationDeadlineEngine(t, mge, func() {
			if err := mge.AdjustDuration(200 * time.Millisecond); err != nil {
				t.Errorf("AdjustDuration: %v", err)
			}
		})
		if target, reached := mge.DurationDeadline(); !reached || target != 200*time.Millisecond {
			t.Fatalf("DurationDeadline = (%s, %v), want (200ms, true)", target, reached)
		}
	})
	t.Run("pause holds the deadline", func(t *testing.T) {
		// The duration leaves the pause well inside the active window even
		// on a loaded runner; the pause outlasts the whole duration.
		const duration, pause = 2 * time.Second, 2500 * time.Millisecond
		mge := durationDeadlineTestEngine(t, duration, "")
		started := time.Now()
		runDurationDeadlineEngine(t, mge, func() {
			if err := mge.Pause(); err != nil {
				t.Errorf("Pause: %v", err)
			}
			time.Sleep(pause)
			if _, reached := mge.DurationDeadline(); reached {
				t.Error("the deadline passed while the run was paused")
			}
			if err := mge.Resume(); err != nil {
				t.Errorf("Resume: %v", err)
			}
		})
		if target, reached := mge.DurationDeadline(); !reached || target != duration {
			t.Fatalf("DurationDeadline = (%s, %v), want (%s, true)", target, reached, duration)
		}
		if elapsed := time.Since(started); elapsed < duration+pause {
			t.Fatalf("run ended after %s, before its active duration plus the pause", elapsed)
		}
	})
	t.Run("hard stop", func(t *testing.T) {
		mge := durationDeadlineTestEngine(t, time.Minute, "")
		runDurationDeadlineEngine(t, mge, mge.Stop)
		if _, reached := mge.DurationDeadline(); reached {
			t.Fatal("a stopped run reports its duration deadline")
		}
	})
	t.Run("graceful stop", func(t *testing.T) {
		mge := durationDeadlineTestEngine(t, time.Minute, "")
		runDurationDeadlineEngine(t, mge, mge.GracefulStop)
		if _, reached := mge.DurationDeadline(); reached {
			t.Fatal("a gracefully stopped run reports its duration deadline")
		}
	})
	t.Run("iteration termination", func(t *testing.T) {
		mge := durationDeadlineTestEngine(t, 0, dsl.TerminationIterations)
		runDurationDeadlineEngine(t, mge, nil)
		if _, reached := mge.DurationDeadline(); reached {
			t.Fatal("an iteration run reports a duration deadline")
		}
	})
}

// The two deadline checks around the pre-load baseline record the deadline
// too, and a launch a stop closed first never does.
func TestDurationDeadlineAroundThePreBaseline(t *testing.T) {
	// A hard stop during the baseline ends RunAt with its cancellation;
	// every other case must end cleanly.
	run := func(t *testing.T, mge *MultiGroupEngine, executionStart time.Time, whileRunning func(), stopped ...bool) *MultiGroupRunResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		done := make(chan *MultiGroupRunResult, 1)
		go func() {
			result, err := mge.RunAt(ctx, executionStart)
			if err != nil && (len(stopped) == 0 || !errors.Is(err, context.Canceled)) {
				t.Errorf("RunAt: %v", err)
			}
			done <- result
		}()
		if whileRunning != nil {
			whileRunning()
		}
		return <-done
	}
	t.Run("stop during the baseline", func(t *testing.T) {
		mge := durationDeadlineBaselineEngine(t, time.Minute, 2*time.Second, "")
		run(t, mge, time.Time{}, func() {
			time.Sleep(100 * time.Millisecond)
			mge.Stop()
		}, true)
		if _, reached := mge.DurationDeadline(); reached {
			t.Fatal("a run stopped during its baseline reports its duration deadline")
		}
	})
	t.Run("deadline passes during the baseline", func(t *testing.T) {
		// The deadline falls a nanosecond after the baseline, so the check
		// that follows the baseline sees it before the deadline loop's first
		// 100 ms tick, and closes the run before any load.
		mge := durationDeadlineBaselineEngine(t, time.Nanosecond, 50*time.Millisecond, "")
		result := run(t, mge, time.Time{}, nil)
		if target, reached := mge.DurationDeadline(); !reached || target != time.Nanosecond {
			t.Fatalf("DurationDeadline = (%s, %v), want (1ns, true)", target, reached)
		}
		if result == nil || result.TotalTransactions != 0 {
			t.Fatalf("a run whose deadline passed in its baseline admitted load: %+v", result)
		}
	})
	t.Run("start past the whole window", func(t *testing.T) {
		mge := durationDeadlineBaselineEngine(t, 100*time.Millisecond, 100*time.Millisecond, "")
		run(t, mge, time.Now().Add(-time.Second), nil)
		if target, reached := mge.DurationDeadline(); !reached || target != 100*time.Millisecond {
			t.Fatalf("DurationDeadline = (%s, %v), want (100ms, true)", target, reached)
		}
	})
	t.Run("stop latched before a run past its window", func(t *testing.T) {
		mge := durationDeadlineBaselineEngine(t, 100*time.Millisecond, 100*time.Millisecond, "")
		mge.GracefulStop()
		run(t, mge, time.Now().Add(-time.Second), nil)
		if _, reached := mge.DurationDeadline(); reached {
			t.Fatal("a run stopped before it started reports its duration deadline")
		}
	})
	t.Run("stop latched before a run without a baseline", func(t *testing.T) {
		// The check after the (empty) baseline sees the closed launch.
		mge := durationDeadlineBaselineEngine(t, time.Minute, 0, "")
		mge.GracefulStop()
		run(t, mge, time.Time{}, nil)
		if _, reached := mge.DurationDeadline(); reached {
			t.Fatal("a run stopped before it started reports its duration deadline")
		}
	})
	t.Run("closed launch", func(t *testing.T) {
		launchCtx, cancel := context.WithCancel(context.Background())
		cancel()
		mge := &MultiGroupEngine{launchCtx: launchCtx, baseTargetDuration: time.Minute}
		mge.mu.Lock()
		mge.markDurationDeadlineLocked()
		mge.mu.Unlock()
		if _, reached := mge.DurationDeadline(); reached {
			t.Fatal("a deadline was recorded after the launch closed")
		}
	})
}
