package api

import (
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func forgetSequentialPlanState(runner *TestRunner, runID, testID string) {
	runner.seqMu.Lock()
	delete(runner.seqPlanRuns, runID)
	delete(runner.seqPlanByTest, testID)
	runner.seqMu.Unlock()
}

func TestStopUnrestoredSequentialPlanSurvivesRestart(t *testing.T) {
	for _, scenario := range []struct {
		policy   string
		complete bool
	}{{"skip", false}, {"skip", true}, {"halt", false}, {"halt", true}} {
		name := scenario.policy + "/interrupted_child"
		if scenario.complete {
			name = scenario.policy + "/completed_child"
		}
		t.Run(name, func(t *testing.T) {
			store, runner, _, plan, firstID := newSequentialRecoveryFixture(t, scenario.policy)
			t.Cleanup(runner.beginShutdown)
			const runID = "exec-plan-run-recovery"
			// A post-admission recovery retry has discarded its provisional
			// state. The Stop request still has to survive another restart.
			forgetSequentialPlanState(runner, runID, firstID)
			if _, err := runner.StopExecutionPlanRun(plan.ID, runID, testCreationActor{DisplayName: "Alice"}); err != nil {
				t.Fatalf("stop unrestored plan: %v", err)
			}
			stored, err := store.GetExecutionPlanRun(runID)
			if err != nil {
				t.Fatal(err)
			}
			if !stored.Entries[0].Stopped || !stored.Entries[1].Stopped || stored.Entries[1].Status != "skipped" {
				t.Fatalf("accepted stop was not durable: %+v", stored.Entries)
			}
			if scenario.complete {
				// The normal graceful-stop completion path persists COMPLETE.
				completeManagedTest(t, runner, firstID, "COMPLETE")
			}
			runner.beginShutdown()
			recovered, backend := acquireRecoveredSequentialRunner(t, store)
			t.Cleanup(recovered.beginShutdown)
			if id, started := waitForStart(t, backend, 150*time.Millisecond); started {
				t.Fatalf("recovery launched %s after the plan was stopped", id)
			}
			stored, err = store.GetExecutionPlanRun(runID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != "halted" || stored.Entries[1].Status != "skipped" || !stored.Entries[1].Stopped {
				t.Fatalf("recovered stopped plan = %+v", stored)
			}
		})
	}
}

type rejectedPlanStopStore struct{ *storage.SQLiteStore }

func (s *rejectedPlanStopStore) CompareAndSwapExecutionPlanRun(*storage.ExecutionPlanRun, int64) (bool, error) {
	return false, errors.New("stop storage unavailable")
}

func TestStopUnrestoredSequentialPlanRequiresDurableAcceptance(t *testing.T) {
	for _, lostResponse := range []bool{false, true} {
		name := "write_failed"
		if lostResponse {
			name = "commit_response_lost"
		}
		t.Run(name, func(t *testing.T) {
			store, runner, _, plan, firstID := newSequentialRecoveryFixture(t, "skip")
			t.Cleanup(runner.beginShutdown)
			const runID = "exec-plan-run-recovery"
			forgetSequentialPlanState(runner, runID, firstID)
			if lostResponse {
				runner.execPlanRunStore = &stopCommitUnknownStore{base: store, armed: true}
			} else {
				runner.execPlanRunStore = &rejectedPlanStopStore{SQLiteStore: store}
			}
			_, err := runner.StopExecutionPlanRun(plan.ID, runID, testCreationActor{DisplayName: "Alice"})
			if lostResponse && err != nil {
				t.Fatalf("durably committed stop was rejected: %v", err)
			}
			if !lostResponse && err == nil {
				t.Fatal("stop accepted without durable persistence")
			}
			stored, loadErr := store.GetExecutionPlanRun(runID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if stored.Entries[0].Stopped != lostResponse || stored.Entries[1].Stopped != lostResponse {
				t.Fatalf("durable entries after stop: %+v", stored.Entries)
			}
			runner.seqMu.Lock()
			_, pending := runner.pendingPlanStops[runID]
			runner.seqMu.Unlock()
			if pending != lostResponse {
				t.Fatalf("pending stop = %v; committed = %v", pending, lostResponse)
			}
		})
	}
}

type conflictedPlanStopStore struct {
	*storage.SQLiteStore
	conflicted bool
}

func (s *conflictedPlanStopStore) CompareAndSwapExecutionPlanRun(run *storage.ExecutionPlanRun, revision int64) (bool, error) {
	if !s.conflicted {
		s.conflicted = true
		current, err := s.GetExecutionPlanRun(run.ID)
		if err != nil {
			return false, err
		}
		current.Entries[0].Status = "complete"
		current.Entries[0].Detail = "completed before stop"
		current.TransitionToken = "concurrent-completion"
		if _, err := s.SQLiteStore.CompareAndSwapExecutionPlanRun(current, revision); err != nil {
			return false, err
		}
		return false, nil
	}
	return s.SQLiteStore.CompareAndSwapExecutionPlanRun(run, revision)
}

func TestStopUnrestoredSequentialPlanPreservesConcurrentTransition(t *testing.T) {
	store, runner, _, _, _ := newSequentialRecoveryFixture(t, "skip")
	t.Cleanup(runner.beginShutdown)
	run, err := store.GetExecutionPlanRun("exec-plan-run-recovery")
	if err != nil {
		t.Fatal(err)
	}
	runner.execPlanRunStore = &conflictedPlanStopStore{SQLiteStore: store}
	runner.seqMu.Lock()
	updated, marked, err := runner.stopUnrestoredSequentialPlanRunLocked(run, "plan stopped by Alice")
	runner.seqMu.Unlock()
	if err != nil || !marked {
		t.Fatalf("stop after conflict = %v, %v", marked, err)
	}
	if updated.Entries[0].Status != "complete" || updated.Entries[0].Detail != "completed before stop" || updated.Entries[0].Stopped || !updated.Entries[1].Stopped {
		t.Fatalf("stop overwrote concurrent transition: %+v", updated.Entries)
	}
}
