package storage

import (
	"sync"
	"testing"
	"time"
)

func TestContinuityCheckpointCompareAndSwap(t *testing.T) {
	checkContinuityCheckpointCompareAndSwap(t, newHAFencingStore(t))
}

func checkContinuityCheckpointCompareAndSwap(t *testing.T, s *SQLiteStore) {
	t.Helper()
	owner := "intent-owner"
	lease := "intent-execution"
	if held, err := s.TryAcquireSchedulerLease(lease, owner, time.Now(), time.Minute); err != nil || !held {
		t.Fatalf("lease=%v %v", held, err)
	}
	record := &TestRecord{ID: "intent-cas-run", Name: "intent", Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}
	if err := s.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	cas := func(old, next, who string) TestStatusTransitionResult {
		t.Helper()
		result, err := s.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(record.ID, old, next, lease, who)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := cas("", "first", owner); got != TestStatusTransitionUpdated {
		t.Fatalf("NULL initial checkpoint=%s", got)
	}
	if got := cas("", "lost", owner); got != TestStatusTransitionUnchanged {
		t.Fatalf("stale snapshot=%s", got)
	}
	if got := cas("first", "lost", "stale-owner"); got != TestStatusTransitionFenced {
		t.Fatalf("stale owner=%s", got)
	}
	var wg sync.WaitGroup
	results := make(chan TestStatusTransitionResult, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(record.ID, "first", "winner", lease, owner)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	winners := 0
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if result == TestStatusTransitionUpdated {
			winners++
		} else if result != TestStatusTransitionUnchanged {
			t.Fatalf("unexpected competing writer=%s", result)
		}
	}
	if winners != 1 {
		t.Fatalf("CAS winners=%d", winners)
	}
	got, err := s.GetTest(record.ID)
	if err != nil || got.ContinuityCheckpoint != "winner" {
		t.Fatal("checkpoint overwritten")
	}
	end := time.Now().UTC()
	if _, err := s.UpdateTestStatusIfActive(record.ID, "ABORTED", &end, ""); err != nil {
		t.Fatal(err)
	}
	if result := cas("winner", "late", owner); result != TestStatusTransitionUnchanged {
		t.Fatalf("terminal write=%s", result)
	}
	if result := cas("winner", "", owner); result != TestStatusTransitionUpdated {
		t.Fatalf("terminal clear=%s", result)
	}
	if result, err := s.CompareAndSwapTestContinuityCheckpointIfLeaseHeld("missing", "", "new", lease, owner); err != nil || result != TestStatusTransitionMissing {
		t.Fatalf("missing=%s %v", result, err)
	}
}
