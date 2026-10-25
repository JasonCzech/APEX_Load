package duckdb

import (
	"errors"
	"testing"
	"time"
)

func TestWorkerAwaitDrainedWaitsOutAccountingLag(t *testing.T) {
	calls := 0
	slow := 0
	err := workerAwaitDrained(func() (uint32, error) {
		calls++
		if calls < 4 {
			return 1, nil
		}
		return 0, nil
	}, time.Second, func(uint32, time.Duration) { slow++ })
	if err != nil || calls != 4 || slow != 0 {
		t.Fatalf("drained: %v after %d queries, %d slow reports; want nil after 4, none slow", err, calls, slow)
	}
}

func TestWorkerAwaitDrainedReportsALongLagOnceAndKeepsWaiting(t *testing.T) {
	began := time.Now()
	var reports []uint32
	err := workerAwaitDrained(func() (uint32, error) {
		if time.Since(began) < 120*time.Millisecond {
			return 1, nil
		}
		return 0, nil
	}, 20*time.Millisecond, func(count uint32, waited time.Duration) {
		if waited < 20*time.Millisecond {
			t.Errorf("reported slow after %s, before the 20ms threshold", waited)
		}
		reports = append(reports, count)
	})
	if err != nil || len(reports) != 1 || reports[0] != 1 {
		t.Fatalf("drained: %v with slow reports %v; want nil and one report of 1 active", err, reports)
	}
	if elapsed := time.Since(began); elapsed < 120*time.Millisecond {
		t.Fatalf("returned after %s, before the job emptied", elapsed)
	}
}

func TestWorkerAwaitDrainedStopsAtAQueryError(t *testing.T) {
	queryErr := errors.New("query failed")
	calls := 0
	err := workerAwaitDrained(func() (uint32, error) {
		calls++
		return 1, queryErr
	}, time.Second, nil)
	if !errors.Is(err, queryErr) || calls != 1 {
		t.Fatalf("drained: %v after %d queries, want the query error after 1", err, calls)
	}
}
