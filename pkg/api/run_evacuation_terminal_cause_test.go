package api

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

// completeWithCause makes gen report a completed run with cause, ending at the
// logical duration completed.
func completeWithCause(gen *rollbackGeneratorClient, cause pb.RunTerminalCause, completed time.Duration) {
	gen.outcome, gen.cause, gen.completed = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED, cause, completed
}

func completeNaturally(gen *rollbackGeneratorClient, completed time.Duration) {
	completeWithCause(gen, pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, completed)
}

// journalSwapCaptureStore records the plain checkpoint that replaced the
// evacuation journal, before the adoption re-arms the run.
type journalSwapCaptureStore struct {
	*storage.SQLiteStore
	swapped atomic.Pointer[string]
}

func (s *journalSwapCaptureStore) CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner string) (storage.TestStatusTransitionResult, error) {
	result, err := s.SQLiteStore.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner)
	if _, plain, decodeErr := decodeRunContinuityCheckpoint(next); err == nil && decodeErr == nil && plain && interruptedRunEvacuation(expected) {
		s.swapped.Store(&next)
	}
	return result, err
}

// swappedCompletedTargets returns the completed targets of the checkpoint
// that replaced the journal.
func (s *journalSwapCaptureStore) swappedCompletedTargets(t *testing.T) []string {
	t.Helper()
	raw := s.swapped.Load()
	if raw == nil {
		t.Fatal("the journal was not replaced")
	}
	checkpoint, _, err := decodeRunContinuityCheckpoint(*raw)
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint.CompletedTargets
}

// durableCompletedTargets returns the plain checkpoint's completed targets,
// failing if the journal was not replaced.
func (f *evacuationRollbackFixture) durableCompletedTargets(t *testing.T, owner string, generation uint64) []string {
	t.Helper()
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "RUNNING" {
		t.Fatalf("durable run = %+v, %v", record, err)
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
	if err != nil || !present || checkpoint.ExecutionGeneration != generation || checkpoint.ExecutionLeaseOwnerID != owner {
		t.Fatalf("plain checkpoint = %+v, present %v, %v", checkpoint, present, err)
	}
	return checkpoint.CompletedTargets
}

// A participant whose generator reports that its duration ran out, at a
// logical duration no shorter than the durable expectation, is done: the
// recovery goes on without it and records it completed.
func TestEvacuationRecoveryTrustsANaturalCompletion(t *testing.T) {
	for _, mode := range []struct {
		name     string
		opts     rollForwardOptions
		reported time.Duration
	}{
		{name: "pending reservation", reported: rollForwardCommitTarget},
		{name: "committed reservation", opts: rollForwardOptions{committedCapacity: true}, reported: rollForwardCommitTarget},
		{name: "activated standby", opts: rollForwardOptions{standby: true}, reported: rollForwardCommitTarget},
		{name: "longer than the commit target", reported: rollForwardCommitTarget + time.Second},
	} {
		t.Run("roll-forward replacement/"+mode.name, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, mode.opts)
			completeNaturally(f.spareGen, mode.reported)
			capture := &journalSwapCaptureStore{SQLiteStore: f.store}
			f.tr.testStore = capture
			owner, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if swapped := capture.swappedCompletedTargets(t); !slices.Equal(swapped, []string{"gen-b"}) {
				t.Fatalf("the swapped checkpoint's completed targets = %v, want gen-b", swapped)
			}
			resume := f.test.adoptedResume
			if resume == nil || resume.finishStatus != "" || resume.adopted["gen-c"] == nil || resume.completed["gen-b"] == nil || resume.adopted["gen-b"] != nil {
				t.Fatalf("naturally completed replacement not rolled forward: %+v", resume)
			}
			if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != 0 {
				t.Fatalf("a completed replacement was adjusted: %+v", adjustments)
			}
			if completed := f.durableCompletedTargets(t, owner, generation); !slices.Contains(completed, "gen-b") {
				t.Fatalf("completed targets = %v, want gen-b", completed)
			}
			_, capacity := f.spareGen.snapshot()
			row, kept := f.ledger(t)["gen-b"]
			if mode.opts.committedCapacity {
				// As for any completed participant, the committed row stays.
				if !kept || row.State != "committed" || row.Generation != f.oldGen || len(capacity) != 0 {
					t.Fatalf("committed reservation = %+v (kept %v), requests %+v", row, kept, capacity)
				}
				return
			}
			if kept || len(capacity) != 1 || !capacity[0].Release || capacity[0].ExecutionGeneration != generation {
				t.Fatalf("pending reservation not released: %+v (kept %v), requests %+v", row, kept, capacity)
			}
			if survivor := f.ledger(t)["gen-c"]; survivor.Generation != generation || survivor.State != "committed" {
				t.Fatalf("survivor capacity not transferred: %+v", survivor)
			}
		})
	}
	t.Run("roll-forward replacement/release once its transition finished", func(t *testing.T) {
		// The generator reported the completion before its terminal
		// transition finished, and refuses the release until it has.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.spareGen, rollForwardCommitTarget)
		f.spareGen.drainingReleases = 2
		restore := runEvacuationReleaseDrainRetryDelay
		runEvacuationReleaseDrainRetryDelay = time.Millisecond
		t.Cleanup(func() { runEvacuationReleaseDrainRetryDelay = restore })
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		if resume := f.test.adoptedResume; resume == nil || resume.finishStatus != "" || resume.completed["gen-b"] == nil {
			t.Fatalf("naturally completed replacement not rolled forward: %+v", resume)
		}
		if _, capacity := f.spareGen.snapshot(); len(capacity) != 3 {
			t.Fatalf("release requests = %d, want two refused and one accepted", len(capacity))
		}
		if _, kept := f.ledger(t)["gen-b"]; kept {
			t.Fatal("the completed replacement's reservation was not released")
		}
	})
	t.Run("roll-forward/every participant", func(t *testing.T) {
		// Both the survivor and the replacement ran out: the installed run has
		// no adopted participant, so resumeAdoptedRun completes it from the
		// retained archives.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.spareGen, rollForwardCommitTarget)
		completeNaturally(f.survivorGen, f.test.Duration)
		owner, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		resume := f.test.adoptedResume
		if resume == nil || resume.finishStatus != "" || len(resume.adopted) != 0 || resume.completed["gen-b"] == nil || resume.completed["gen-c"] == nil {
			t.Fatalf("all-completed run = %+v", resume)
		}
		if completed := f.durableCompletedTargets(t, owner, generation); !slices.Contains(completed, "gen-b") || !slices.Contains(completed, "gen-c") {
			t.Fatalf("completed targets = %v", completed)
		}
	})
	for _, phase := range []string{runEvacuationPhaseReplacementCommitting, runEvacuationPhaseStaged} {
		rollForward := phase == runEvacuationPhaseReplacementCommitting
		name := map[bool]string{true: "roll-forward", false: "rollback"}[rollForward]
		t.Run(name+"/survivor", func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: phase})
			completeNaturally(f.survivorGen, f.test.Duration)
			capture := &journalSwapCaptureStore{SQLiteStore: f.store}
			f.tr.testStore = capture
			owner, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if swapped := capture.swappedCompletedTargets(t); !slices.Equal(swapped, []string{"gen-c"}) {
				t.Fatalf("the swapped checkpoint's completed targets = %v, want gen-c", swapped)
			}
			running := map[bool]string{true: "gen-b", false: "gen-a"}[rollForward]
			resume := f.test.adoptedResume
			if resume == nil || resume.finishStatus != "" || resume.adopted[running] == nil || resume.completed["gen-c"] == nil {
				t.Fatalf("naturally completed survivor blocked the %s: %+v", name, resume)
			}
			if completed := f.durableCompletedTargets(t, owner, generation); !slices.Contains(completed, "gen-c") {
				t.Fatalf("completed targets = %v, want gen-c", completed)
			}
		})
	}
	t.Run("rollback/source", func(t *testing.T) {
		// The source's duration ran out before its evacuation was fenced: the
		// rollback returns to the prior topology with the source completed.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: runEvacuationPhaseStaged})
		completeNaturally(f.sourceGen, f.test.Duration)
		capture := &journalSwapCaptureStore{SQLiteStore: f.store}
		f.tr.testStore = capture
		owner, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		if swapped := capture.swappedCompletedTargets(t); !slices.Equal(swapped, []string{"gen-a"}) {
			t.Fatalf("the swapped checkpoint's completed targets = %v, want gen-a", swapped)
		}
		resume := f.test.adoptedResume
		if resume == nil || resume.finishStatus != "" || resume.completed["gen-a"] == nil || resume.adopted["gen-c"] == nil || f.test.remoteClientsByTarget["gen-b"] != nil {
			t.Fatalf("naturally completed source not rolled back: %+v", resume)
		}
		completed := f.durableCompletedTargets(t, owner, generation)
		if !slices.Contains(completed, "gen-a") || !slices.Equal(f.inMemoryCompletedTargets(t), completed) {
			t.Fatalf("completed targets = %v", completed)
		}
		if claims, capacity := f.spareGen.snapshot(); len(claims) != 1 || len(claims[0]) != 0 || len(capacity) != 1 || !capacity[0].Release {
			t.Fatalf("staged replacement not fenced and released: claims %+v requests %+v", claims, capacity)
		}
		if _, kept := f.ledger(t)["gen-b"]; kept {
			t.Fatal("the replacement reservation survived the rollback")
		}
	})
}

// inMemoryCompletedTargets reads back the run's in-memory plain checkpoint.
func (f *evacuationRollbackFixture) inMemoryCompletedTargets(t *testing.T) []string {
	t.Helper()
	f.tr.mu.RLock()
	raw := f.test.continuityCheckpoint
	f.tr.mu.RUnlock()
	checkpoint, present, err := decodeRunContinuityCheckpoint(raw)
	if err != nil || !present {
		t.Fatalf("in-memory checkpoint = present %v, %v", present, err)
	}
	return checkpoint.CompletedTargets
}

// Any other completion holds the run, with a reason that names its cause.
func TestEvacuationRecoveryHoldsACompletionWithoutItsDuration(t *testing.T) {
	short := 30 * time.Second
	for _, c := range []struct {
		name     string
		cause    pb.RunTerminalCause
		reported time.Duration
		want     string
	}{
		{name: "controller stop", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP, want: "it was stopped by a controller"},
		{name: "older generator", want: "its generator reports no terminal cause"},
		{name: "executions ended early", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_EXECUTIONS_ENDED_EARLY, want: "its executions ended before the run's duration ran out"},
		// A late old owner shortened the participant before it was adopted.
		{name: "shorter duration", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, reported: short, want: "its duration ran out at 30s, short of the expected"},
		{name: "unknown cause", cause: pb.RunTerminalCause(99), reported: time.Hour, want: "its terminal cause 99 is unknown"},
	} {
		t.Run("roll-forward replacement/"+c.name, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			completeWithCause(f.spareGen, c.cause, c.reported)
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertFailedRollForwardHeld(t, generation, "replacement gen-b reports a completed run the journal never recorded: "+c.want, true, false)
			if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != 0 {
				t.Fatalf("a held replacement was adjusted: %+v", adjustments)
			}
			if _, capacity := f.spareGen.snapshot(); len(capacity) != 0 {
				t.Fatalf("a held replacement received capacity requests: %+v", capacity)
			}
			if row := f.ledger(t)["gen-b"]; row.State != "pending" || row.Generation != f.oldGen {
				t.Fatalf("held replacement capacity changed: %+v", row)
			}
		})
		t.Run("roll-forward survivor/"+c.name, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			completeWithCause(f.survivorGen, c.cause, c.reported)
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertFailedRollForwardHeld(t, generation, "participant gen-c reports a completed run the journal never recorded: "+c.want, false, true)
		})
		t.Run("rollback source/"+c.name, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: runEvacuationPhaseStaged})
			completeWithCause(f.sourceGen, c.cause, c.reported)
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			guard, _ := f.durableGuard(t)
			record, err := f.store.GetTest(f.test.ID)
			if err != nil || guard.Phase != runEvacuationPhaseRollingBack || guard.RollbackGeneration != generation || record.Status != "STOPPING" ||
				!strings.Contains(record.Error, "evacuation source gen-a no longer runs its prior assignment: "+c.want) || f.test.adoptedResume != nil {
				t.Fatalf("rollback with a completed source = phase %q, %+v, %v", guard.Phase, record, err)
			}
		})
	}
	t.Run("roll-forward replacement/a short replacement duration", func(t *testing.T) {
		// The replacement's own expectation is the journaled commit target.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.spareGen, rollForwardCommitTarget-time.Millisecond)
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, generation, "short of the expected 55s", true, false)
	})
	t.Run("roll-forward survivor/short of the run's duration", func(t *testing.T) {
		// A survivor's expectation is the run's duration, not the
		// replacement's shorter commit target.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.survivorGen, rollForwardCommitTarget)
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, generation, "participant gen-c reports a completed run the journal never recorded: its duration ran out at 55s, short of the expected 1m0s", false, true)
	})
	t.Run("roll-forward replacement/release refused while draining", func(t *testing.T) {
		// The retries are bounded; a generator that never finishes holds the run.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.spareGen, rollForwardCommitTarget)
		f.spareGen.drainingReleases = 1000
		attempts, delay := runEvacuationReleaseDrainAttempts, runEvacuationReleaseDrainRetryDelay
		runEvacuationReleaseDrainAttempts, runEvacuationReleaseDrainRetryDelay = 3, time.Millisecond
		t.Cleanup(func() { runEvacuationReleaseDrainAttempts, runEvacuationReleaseDrainRetryDelay = attempts, delay })
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, generation, generatorrpc.PodCapacityStillDrainingMessage, true, false)
		if _, capacity := f.spareGen.snapshot(); len(capacity) != 3 {
			t.Fatalf("release requests = %d, want the bounded three", len(capacity))
		}
		if row := f.ledger(t)["gen-b"]; row.State != "releasing" || row.Generation != f.oldGen {
			t.Fatalf("refused release = %+v, want releasing under the old term", row)
		}
	})
	t.Run("roll-forward replacement/release unconfirmed", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.spareGen, rollForwardCommitTarget)
		f.spareGen.rejectCap = true
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, generation, "release the completed replacement's reservation", true, false)
		// Only a refusal while draining is retried.
		if _, capacity := f.spareGen.snapshot(); len(capacity) != 1 {
			t.Fatalf("release requests = %d, want one", len(capacity))
		}
		if row := f.ledger(t)["gen-b"]; row.State != "releasing" || row.Generation != f.oldGen {
			t.Fatalf("unconfirmed release = %+v, want releasing under the old term", row)
		}
	})
}

// releaseThenLostStore fails the journal swap and the run's status writes, as
// a successor lost between releasing a completed replacement's capacity and
// replacing the journal leaves the run: RUNNING under its rolling_forward
// journal.
type releaseThenLostStore struct {
	*statusWriteFaultStore
	failPlainSwap atomic.Bool
}

func (s *releaseThenLostStore) CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner string) (storage.TestStatusTransitionResult, error) {
	faults := rollForwardFaultStore{SQLiteStore: s.SQLiteStore, failPlainSwap: s.failPlainSwap.Load()}
	return faults.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner)
}

// A successor lost after it released a naturally completed replacement's
// reservation and before it replaced the journal leaves the next term a
// rolling_forward journal and no reservation. The next term asks the
// generators again.
func TestNaturalCompletionAcrossAnotherLeaderChange(t *testing.T) {
	lose := func(t *testing.T) (*evacuationRollbackFixture, *releaseThenLostStore, uint64) {
		t.Helper()
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.spareGen, rollForwardCommitTarget)
		lost := &releaseThenLostStore{statusWriteFaultStore: newStatusWriteFaultStore(f.store)}
		lost.failPlainSwap.Store(true)
		f.tr.testStore = lost
		// The lost successor's stops never reached the survivor.
		f.survivorGen.failStop = true
		_, first := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		if _, kept := f.ledger(t)["gen-b"]; kept {
			t.Fatal("the first successor did not release the completed replacement's reservation")
		}
		guard, _ := f.durableGuard(t)
		if guard.Phase != runEvacuationPhaseRollingForward || guard.RollForwardGeneration != first {
			t.Fatalf("journal after the lost successor = phase %q generation %d", guard.Phase, guard.RollForwardGeneration)
		}
		f.survivorGen.failStop = false
		lost.failPlainSwap.Store(false)
		record, second := f.restartAfterFailedHold(t, lost.statusWriteFaultStore, "natural-after-a-lost-successor")
		if record.Status != "RUNNING" {
			t.Fatalf("the lost successor left the run %s", record.Status)
		}
		return f, lost, second
	}
	t.Run("rolls forward", func(t *testing.T) {
		f, _, second := lose(t)
		owner, _ := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		resume := f.test.adoptedResume
		if resume == nil || resume.finishStatus != "" || resume.adopted["gen-c"] == nil || resume.completed["gen-b"] == nil {
			t.Fatalf("second successor did not roll forward: %+v", resume)
		}
		if completed := f.durableCompletedTargets(t, owner, second); !slices.Contains(completed, "gen-b") {
			t.Fatalf("completed targets = %v, want gen-b", completed)
		}
		if _, capacity := f.spareGen.snapshot(); len(capacity) != 1 {
			t.Fatalf("the released reservation was released again: %+v", capacity)
		}
	})
	t.Run("pruned outcome is held", func(t *testing.T) {
		// The generator pruned the completed run's outcome before the second
		// successor asked; the journal never recorded the completion.
		f, _, second := lose(t)
		f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, second, "gen-b: UNKNOWN_TEST", true, false)
	})
}

// runEvacuationCompletionDoubt trusts only a COMPLETE run whose duration ran
// out at no less than a positive expectation.
func TestRunEvacuationCompletionDoubt(t *testing.T) {
	natural := generatorrpc.RunAdoptionResult{Outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED, TerminalState: "COMPLETE", TerminalCause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, CompletedDuration: time.Minute}
	with := func(change func(*generatorrpc.RunAdoptionResult)) generatorrpc.RunAdoptionResult {
		result := natural
		change(&result)
		return result
	}
	for name, c := range map[string]struct {
		completion generatorrpc.RunAdoptionResult
		expected   time.Duration
		want       string
	}{
		"exact":          {natural, time.Minute, ""},
		"longer":         {natural, 59 * time.Second, ""},
		"short":          {natural, time.Minute + time.Nanosecond, "short of the expected"},
		"no expectation": {natural, 0, "no duration to compare"},
		"failed state":   {with(func(r *generatorrpc.RunAdoptionResult) { r.TerminalState = "FAILED" }), time.Minute, `terminal state is "FAILED"`},
		"stopped outcome": {with(func(r *generatorrpc.RunAdoptionResult) {
			r.Outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED
		}), time.Minute, "no completed outcome"},
		"controller stop": {with(func(r *generatorrpc.RunAdoptionResult) {
			r.TerminalCause = pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP
		}), time.Minute, "stopped by a controller"},
		"ended early": {with(func(r *generatorrpc.RunAdoptionResult) {
			r.TerminalCause = pb.RunTerminalCause_RUN_TERMINAL_CAUSE_EXECUTIONS_ENDED_EARLY
		}), time.Minute, "ended before"},
		"no cause": {with(func(r *generatorrpc.RunAdoptionResult) {
			r.TerminalCause = pb.RunTerminalCause_RUN_TERMINAL_CAUSE_UNSPECIFIED
		}), time.Minute, "no terminal cause"},
		"lowercase status": {with(func(r *generatorrpc.RunAdoptionResult) { r.TerminalState = "complete" }), time.Minute, ""},
	} {
		got := runEvacuationCompletionDoubt(c.completion, c.expected)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: doubt = %q, want %s", name, got, fmt.Sprintf("%q", c.want))
		}
	}
}

// A rollback successor lost after it released the staged replacement's
// reservation and before it replaced its rolling_back journal leaves the next
// term to roll back again, with the naturally completed source asked again.
func TestNaturalSourceAcrossAnotherRollbackLeaderChange(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: runEvacuationPhaseStaged})
	completeNaturally(f.sourceGen, f.test.Duration)
	lost := &releaseThenLostStore{statusWriteFaultStore: newStatusWriteFaultStore(f.store)}
	lost.failPlainSwap.Store(true)
	f.tr.testStore = lost
	f.survivorGen.failStop = true
	_, first := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if _, kept := f.ledger(t)["gen-b"]; kept {
		t.Fatal("the first successor did not release the replacement's reservation")
	}
	guard, _ := f.durableGuard(t)
	if guard.Phase != runEvacuationPhaseRollingBack || guard.RollbackGeneration != first {
		t.Fatalf("journal after the lost successor = phase %q generation %d", guard.Phase, guard.RollbackGeneration)
	}
	f.survivorGen.failStop = false
	lost.failPlainSwap.Store(false)
	record, second := f.restartAfterFailedHold(t, lost.statusWriteFaultStore, "natural-source-after-a-lost-successor")
	if record.Status != "RUNNING" {
		t.Fatalf("the lost successor left the run %s", record.Status)
	}
	owner, _ := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	resume := f.test.adoptedResume
	if resume == nil || resume.finishStatus != "" || resume.completed["gen-a"] == nil || resume.adopted["gen-c"] == nil {
		t.Fatalf("second successor did not roll back: %+v", resume)
	}
	if completed := f.durableCompletedTargets(t, owner, second); !slices.Contains(completed, "gen-a") {
		t.Fatalf("completed targets = %v, want gen-a", completed)
	}
}
