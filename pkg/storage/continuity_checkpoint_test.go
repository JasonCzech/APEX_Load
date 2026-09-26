package storage

import (
	"testing"
	"time"
)

func TestContinuityCheckpointIsLeaseFencedAndSurvivesFullSave(t *testing.T) {
	store := newHAFencingStore(t)
	oldToken := "controller-a/epoch-1"
	newToken := "controller-b/epoch-2"
	if acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, oldToken, time.Now(), time.Minute); err != nil || !acquired {
		t.Fatalf("acquire lease = (%v, %v)", acquired, err)
	}
	record := &TestRecord{ID: "continuity-run", Name: "continuity", Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}
	if saved, err := store.SaveTestIfLeaseHeld(record, testExecutionLeaseName, oldToken); err != nil || !saved {
		t.Fatalf("save run = (%v, %v)", saved, err)
	}

	const checkpoint = `{"version":1,"execution_generation":7}`
	result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(record.ID, checkpoint, testExecutionLeaseName, oldToken)
	if err != nil || result != TestStatusTransitionUpdated {
		t.Fatalf("arm checkpoint = (%q, %v), want updated", result, err)
	}
	got, err := store.GetTest(record.ID)
	if err != nil || got.ContinuityCheckpoint != checkpoint {
		t.Fatalf("stored checkpoint = %q (%v), want %q", got.ContinuityCheckpoint, err, checkpoint)
	}

	// The full-row save the runner performs on every lifecycle change must
	// never erase the checkpoint: it is written by its own fenced path only.
	record.Status = "PAUSED"
	if saved, err := store.SaveTestIfLeaseHeld(record, testExecutionLeaseName, oldToken); err != nil || !saved {
		t.Fatalf("resave run = (%v, %v)", saved, err)
	}
	if got, _ := store.GetTest(record.ID); got.ContinuityCheckpoint != checkpoint {
		t.Fatalf("full save clobbered checkpoint: %q", got.ContinuityCheckpoint)
	}

	if err := store.ReleaseSchedulerLease(testExecutionLeaseName, oldToken); err != nil {
		t.Fatalf("release: %v", err)
	}
	if acquired, err := store.TryAcquireSchedulerLease(testExecutionLeaseName, newToken, time.Now(), time.Minute); err != nil || !acquired {
		t.Fatalf("successor acquire = (%v, %v)", acquired, err)
	}
	if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(record.ID, `{"stale":true}`, testExecutionLeaseName, oldToken); err != nil || result != TestStatusTransitionFenced {
		t.Fatalf("stale leader write = (%q, %v), want fenced", result, err)
	}
	if got, _ := store.GetTest(record.ID); got.ContinuityCheckpoint != checkpoint {
		t.Fatalf("stale leader changed checkpoint: %q", got.ContinuityCheckpoint)
	}

	endedAt := time.Now().UTC()
	if result, err := store.UpdateTestStatusIfActiveAndLeaseHeld(record.ID, "COMPLETE", &endedAt, "", testExecutionLeaseName, newToken); err != nil || result != TestStatusTransitionUpdated {
		t.Fatalf("complete run = (%q, %v)", result, err)
	}
	if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(record.ID, `{"late":true}`, testExecutionLeaseName, newToken); err != nil || result != TestStatusTransitionUnchanged {
		t.Fatalf("arming a finished run = (%q, %v), want unchanged", result, err)
	}
	if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(record.ID, "", testExecutionLeaseName, newToken); err != nil || result != TestStatusTransitionUpdated {
		t.Fatalf("clearing a finished run's checkpoint = (%q, %v), want updated", result, err)
	}
	if got, _ := store.GetTest(record.ID); got.ContinuityCheckpoint != "" {
		t.Fatalf("checkpoint not cleared: %q", got.ContinuityCheckpoint)
	}
	if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld("missing", checkpoint, testExecutionLeaseName, newToken); err != nil || result != TestStatusTransitionMissing {
		t.Fatalf("missing run = (%q, %v), want missing", result, err)
	}
}
