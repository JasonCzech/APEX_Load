package api

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// REC-A exit cases the roll-forward and restart records left unexercised.

// A replacement whose generator lacks a capability the recovery needs holds
// the run: one that predates run adoption cannot be adopted (nor shown to run
// nothing), one without the duration control cannot be shortened to the
// journaled commit target, and a spare without the evacuation assignment
// state is never given a restarted share.
func TestEvacuationExitCapabilityMismatch(t *testing.T) {
	t.Run("roll-forward replacement predates run adoption", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.spareGen.noAdoptionSupport = true
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, generation, "gen-b: generator predates run adoption", true, false)
		if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != 0 {
			t.Fatalf("a replacement that cannot be adopted was adjusted: %+v", adjustments)
		}
		if row := f.ledger(t)["gen-b"]; row.State != "pending" || row.Generation != f.oldGen {
			t.Fatalf("held replacement capacity changed: %+v", row)
		}
	})
	t.Run("roll-forward replacement predates run adoption with the restart on", func(t *testing.T) {
		// Its claim proves nothing about what it runs, so its share is not
		// restarted either.
		f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{phase: runEvacuationPhaseReplacementCommitting}})
		f.spareGen.noAdoptionSupport = true
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, generation, "the replacement gen-b could not be restarted: it is unreachable, which does not prove it stopped", true, false)
		if assignments, _ := spare.recorder.snapshot(); len(assignments) != 0 {
			t.Fatalf("a refused restart assigned the share: %+v", assignments)
		}
	})
	t.Run("roll-forward replacement without the duration control", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.spareGen.adjustErr = status.Error(codes.Unimplemented, "unknown method AdjustDuration")
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, generation, "unknown method AdjustDuration", true, true)
		if row := f.ledger(t)["gen-b"]; row.State != "pending" || row.Generation != f.oldGen {
			t.Fatalf("an unshortened replacement's capacity was committed: %+v", row)
		}
	})
	withoutEvacuationState := func(server *readyCapacityServer) {
		server.withoutFeatures = []string{generatorrpc.FeatureEvacuationAssignmentStateV1}
	}
	for name, phase := range map[string]string{"restart": runEvacuationPhaseSourceFenced, "roll-forward fallback": runEvacuationPhaseReplacementCommitting} {
		t.Run(name+" onto a spare without the evacuation assignment state", func(t *testing.T) {
			f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{phase: phase}, configureRestartTarget: withoutEvacuationState})
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertRestartIntent(t, generation)
			if ok, _ := f.restartPhaseB(t); ok {
				t.Fatal("a share was restarted onto a generator that lacks the evacuation assignment state")
			}
			f.assertRestartHeld(t, generation, "no healthy generator with sufficient capacity", runEvacuationPhaseReplacementRestarting, true, false)
			if assignments, _ := spare.recorder.snapshot(); len(assignments) != 0 {
				t.Fatalf("the incapable spare was assigned the share: %+v", assignments)
			}
		})
	}
}

// Rolling a committed replacement forward needs no spare capacity: its
// reservation is journaled. A replacement not adopted at the
// replacement_committing or rolling_forward phase falls back to a restart,
// which does, and without it the run is held with the reason.
func TestEvacuationExitNoSpareCapacity(t *testing.T) {
	t.Run("replacement_committing rolls forward without a spare", func(t *testing.T) {
		f, _ := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{phase: runEvacuationPhaseReplacementCommitting}, noRestartTarget: true})
		owner, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertRolledForward(t, owner, generation, rollForwardCommitTarget)
	})
	for _, phase := range []string{runEvacuationPhaseReplacementCommitting, runEvacuationPhaseRollingForward} {
		for name, opts := range map[string]restartOptions{"no spare generator": {noRestartTarget: true}, "a spare without room": {restartVUsers: 2}} {
			t.Run(phase+"/"+name, func(t *testing.T) {
				opts.rollForwardOptions = rollForwardOptions{phase: runEvacuationPhaseReplacementCommitting}
				f, spare := newEvacuationRestartFixture(t, opts)
				if phase == runEvacuationPhaseRollingForward {
					// An earlier successor recorded its roll-forward intent and
					// was lost; the next term finds the replacement gone.
					ctx := context.Background()
					firstOwner, firstGeneration := f.tr.leadership.executionLeaseTerm()
					guard, _, err := runEvacuationRollForwardPlan(f.guard, f.test.ID)
					if err != nil {
						t.Fatal(err)
					}
					planned, _, err := f.tr.planRunAdoptionIntent(ctx, f.test.ID, runEvacuationRollForwardCheckpoint(guard), firstGeneration, firstOwner)
					if err != nil {
						t.Fatal(err)
					}
					if _, _, err := f.tr.persistRunEvacuationRollForwardIntent(f.test, f.guard, guard, planned, firstGeneration, firstOwner); err != nil {
						t.Fatal(err)
					}
					f.takeOver(t, "no-spare-after-rolling-forward")
				}
				f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
				_, generation := f.tr.leadership.executionLeaseTerm()
				if err := f.tr.adoptOrphanedRuns(); err != nil {
					t.Fatal(err)
				}
				f.assertRestartIntent(t, generation)
				if ok, _ := f.restartPhaseB(t); ok {
					t.Fatal("a restart without spare capacity reported success")
				}
				f.assertRestartHeld(t, generation, "no healthy generator with sufficient capacity", runEvacuationPhaseReplacementRestarting, true, false)
				if spare != nil {
					if assignments, _ := spare.recorder.snapshot(); len(assignments) != 0 {
						t.Fatalf("a spare without room was assigned the share: %+v", assignments)
					}
				}
				// Nothing was reserved for a restart that could not run.
				if row, present := f.ledger(t)["gen-d"]; present && row.State != "released" {
					t.Fatalf("refused restart kept a reservation: %+v", row)
				}
			})
		}
	}
}

// A stop cannot land inside a roll-forward: Phase A runs during acquisition
// reconcile, before admission opens, so the stop is refused as not admitted
// and the roll-forward completes. Retried after admission, the stop owns the
// installed run and reaches every adopted participant at the new term.
func TestStopRequestedWhileTheRollForwardRuns(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	owner, generation := f.tr.leadership.executionLeaseTerm()
	var duringRollForward []error
	f.spareGen.onClaim = func() {
		duringRollForward = append(duringRollForward, f.tr.StopTest(f.test.ID))
	}
	// The test constructs the admission state acquisition reconcile has while
	// Phase A runs; it calls Phase A directly rather than through the lease's
	// acquisition callback.
	f.tr.leadership.reconciling.Store(true)
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.tr.leadership.reconciling.Store(false)
	f.spareGen.onClaim = nil
	if len(duringRollForward) != 1 || !errors.Is(duringRollForward[0], errNotExecutionLeader) {
		t.Fatalf("stops during the roll-forward = %v, want one refused as not admitted", duringRollForward)
	}
	f.assertRolledForward(t, owner, generation, rollForwardCommitTarget)

	for _, gen := range []*rollbackGeneratorClient{f.spareGen, f.survivorGen} {
		gen.allowGraceful = true
	}
	// Phase B needs metrics streams the fakes do not serve; the stop lands on
	// the run Phase A installed, as one landing before Phase B does.
	stopErr := f.tr.StopTest(f.test.ID)
	if errors.Is(stopErr, errNotExecutionLeader) {
		t.Fatalf("the admitted stop was refused: %v", stopErr)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || stopErr != nil || record.Status != "STOPPING" {
		t.Fatalf("the stop did not take the run: %+v, %v (stop: %v)", record, err, stopErr)
	}
	for name, gen := range map[string]*rollbackGeneratorClient{"replacement": f.spareGen, "survivor": f.survivorGen} {
		gen.mu.Lock()
		stops := append(append([]uint64(nil), gen.gracefulStops...), gen.stops...)
		gen.mu.Unlock()
		if len(stops) == 0 || slices.ContainsFunc(stops, func(stopped uint64) bool { return stopped != generation }) || !gen.stoppedWithClaimedToken() {
			t.Fatalf("%s stops = %v, want stops at %d with the rotated token", name, stops, generation)
		}
	}
	if stops := f.sourceGen.stopGenerations(); len(stops) != 0 {
		t.Fatalf("the fenced source was stopped: %v", stops)
	}
}

// A paused run whose roll-forward a successor began, then lost, stays paused
// through the next leader change: the next term rolls it forward from the
// recorded intent, and a pause state that changed since staging is held.
func TestPausedRollForwardAcrossASecondLeaderChange(t *testing.T) {
	for _, interrupted := range []string{"after intent", "after claim", "resumed since staging"} {
		t.Run(interrupted, func(t *testing.T) {
			ctx := context.Background()
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{paused: true}, func(f *evacuationRollbackFixture) {
				// The run's pause is durable, as a successor reads it.
				record, err := f.store.GetTest(f.test.ID)
				if err != nil {
					t.Fatal(err)
				}
				record.Status, record.PausedAt = "PAUSED", f.test.PausedAt
				if err := f.store.SaveTest(record); err != nil {
					t.Fatal(err)
				}
			})
			firstOwner, firstGeneration := f.tr.leadership.executionLeaseTerm()
			// The survivor and the replacement check proof chains as
			// generators do: after the first successor's claim they hold the
			// run under its generation and token, and adopt it again only for
			// a chain that names them.
			for _, gen := range []*rollbackGeneratorClient{f.spareGen, f.survivorGen} {
				gen.trackLineage(f.oldGen, f.token)
			}
			guard, _, err := runEvacuationRollForwardPlan(f.guard, f.test.ID)
			if err != nil {
				t.Fatal(err)
			}
			planned, adoption, err := f.tr.planRunAdoptionIntent(ctx, f.test.ID, runEvacuationRollForwardCheckpoint(guard), firstGeneration, firstOwner)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.tr.persistRunEvacuationRollForwardIntent(f.test, f.guard, guard, planned, firstGeneration, firstOwner); err != nil {
				t.Fatal(err)
			}
			if interrupted == "after claim" {
				for _, gen := range []*rollbackGeneratorClient{f.spareGen, f.survivorGen} {
					claim, err := gen.ClaimExecutionEpochWithAdoptions(ctx, firstGeneration, firstOwner, time.Second, []generatorrpc.RunAdoption{adoption})
					if err != nil || len(claim.Adoptions) != 1 || claim.Adoptions[0].Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
						t.Fatalf("the first successor's claim = (%+v, %v)", claim, err)
					}
				}
				_, _ = f.spareGen.AdjustDuration(ctx, generatorrpc.DurationAdjustment{TestID: f.test.ID, ExecutionGeneration: firstGeneration, TargetDuration: rollForwardCommitTarget, RunSupervisionToken: adoption.NewToken})
			}
			_, raw := f.durableGuard(t)
			secondOwner, secondGeneration := f.handOver(t, "paused-roll-forward-second-successor")
			record, err := f.store.GetTest(f.test.ID)
			if err != nil {
				t.Fatal(err)
			}
			restored := &ManagedTest{ID: f.test.ID, Name: f.test.ID, Status: "PAUSED", PausedAt: f.test.PausedAt, Assignment: f.test.Assignment, Groups: f.test.Groups, StartedAt: f.test.StartedAt, Duration: f.test.Duration}
			if interrupted == "resumed since staging" {
				// The durable pause state no longer matches the staged one.
				record.Status, record.PausedAt = "RUNNING", nil
				if err := f.store.SaveTest(record); err != nil {
					t.Fatal(err)
				}
				restored.Status, restored.PausedAt = "RUNNING", nil
			}
			setContinuityCheckpointLocked(restored, raw)
			f.tr.mu.Lock()
			f.tr.tests[f.test.ID] = restored
			f.tr.mu.Unlock()
			f.test = restored
			if interrupted == "resumed since staging" {
				if f.tr.isRunAdoptionCandidate(restored) {
					t.Fatal("a rolling_forward journal whose pause state changed is a candidate")
				}
				if err := f.tr.reconcileStoredActiveTest(restored); err != nil {
					t.Fatal(err)
				}
				held, err := f.store.GetTest(f.test.ID)
				if err != nil || held.Status != "STOPPING" || held.ContinuityCheckpoint != raw {
					t.Fatalf("changed pause state = %+v, %v", held, err)
				}
				return
			}
			if record.Status != "PAUSED" {
				t.Fatalf("a lost roll-forward left the run %s", record.Status)
			}
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertRolledForwardWithStatus(t, secondOwner, secondGeneration, rollForwardCommitTarget, "PAUSED")
			if f.test.Status != "PAUSED" || f.test.PausedAt == nil {
				t.Fatalf("the rolled-forward run is %s, paused at %v", f.test.Status, f.test.PausedAt)
			}
			claims, _ := f.spareGen.snapshot()
			last := claims[len(claims)-1][0]
			if len(last.PriorProofs) != 1 || last.PriorProofs[0].Generation != firstGeneration {
				t.Fatalf("second adoption lacked the first successor's token proof: %+v", last)
			}
		})
	}
}

// An older controller cannot read a newer journal version. It holds the run
// STOPPING through its interrupted distributed stop path, leaves the journal
// unchanged and claims no generator. The reader's newest version is lowered to
// read each journal as every older release would.
func TestOlderControllersHoldNewerJournals(t *testing.T) {
	journals := map[int]func(t *testing.T) (*evacuationRollbackFixture, *ManagedTest){
		runEvacuationRollbackCheckpointVersion: func(t *testing.T) (*evacuationRollbackFixture, *ManagedTest) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: runEvacuationRollbackCheckpointVersion, phase: runEvacuationPhaseStaged})
			return f, f.test
		},
		runEvacuationRollForwardCheckpointVersion: func(t *testing.T) (*evacuationRollbackFixture, *ManagedTest) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			return f, f.test
		},
		runEvacuationRestartCheckpointVersion: func(t *testing.T) (*evacuationRollbackFixture, *ManagedTest) {
			// A successor wrote a restart intent and was lost.
			f, _ := newEvacuationRestartFixture(t, restartOptions{})
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertRestartIntent(t, generation)
			f.handOver(t, "older-reader")
			record, err := f.store.GetTest(f.test.ID)
			if err != nil {
				t.Fatal(err)
			}
			restored := managedTestFromRecord(record)
			restored.Assignment, restored.Groups = f.test.Assignment, f.test.Groups
			f.tr.mu.Lock()
			f.tr.tests[f.test.ID] = restored
			f.tr.mu.Unlock()
			f.test = restored
			return f, restored
		},
	}
	for _, version := range []int{runEvacuationRollbackCheckpointVersion, runEvacuationRollForwardCheckpointVersion, runEvacuationRestartCheckpointVersion} {
		for reader := runEvacuationCheckpointVersion; reader <= version; reader++ {
			name := map[bool]string{true: "read by its own release", false: "read by an older release"}[reader == version]
			t.Run(strings.Join([]string{"version", string(rune('0' + version)), "reader", string(rune('0' + reader)), name}, " "), func(t *testing.T) {
				f, test := journals[version](t)
				_, raw := f.durableGuard(t)
				claimsBefore := map[string]int{}
				for target, gen := range map[string]*rollbackGeneratorClient{"gen-a": f.sourceGen, "gen-b": f.spareGen, "gen-c": f.survivorGen} {
					claimsBefore[target] = gen.claimCount()
				}
				readAs(t, reader)
				f.tr.mu.RLock()
				candidate := f.tr.isRunAdoptionCandidate(test)
				f.tr.mu.RUnlock()
				if reader == version {
					// Control: the journal's own release reads it.
					if !candidate && !interruptedRunEvacuation(raw) {
						t.Fatalf("version %d is unreadable by its own release", version)
					}
					return
				}
				if candidate || interruptedRunEvacuation(raw) {
					t.Fatalf("a version %d reader recognized a version %d journal (candidate %v)", reader, version, candidate)
				}
				if _, committed := committedRunEvacuationCheckpoint(raw, test.ID); committed {
					t.Fatal("an older reader promoted a newer journal")
				}
				if err := f.tr.adoptOrphanedRuns(); err != nil {
					t.Fatal(err)
				}
				if err := f.tr.reconcileStoredActiveTest(test); err != nil {
					t.Fatal(err)
				}
				record, err := f.store.GetTest(f.test.ID)
				if err != nil || record.Status != "STOPPING" || !strings.Contains(record.Error, interruptedDistributedStopReason) || record.ContinuityCheckpoint != raw {
					t.Fatalf("version %d journal read by a version %d controller = %+v, %v", version, reader, record, err)
				}
				for target, gen := range map[string]*rollbackGeneratorClient{"gen-a": f.sourceGen, "gen-b": f.spareGen, "gen-c": f.survivorGen} {
					if claims := gen.claimCount(); claims != claimsBefore[target] {
						t.Fatalf("an older reader claimed %s (%d claims, %d before)", target, claims, claimsBefore[target])
					}
				}
			})
		}
	}
}

// readAs reads journals, for the rest of the test, with the version checks of
// the release whose newest journal version is reader.
func readAs(t *testing.T, reader int) {
	t.Helper()
	previous := runEvacuationReadableOverride.Swap(int32(reader))
	t.Cleanup(func() { runEvacuationReadableOverride.Store(previous) })
}

// Each of the three journal version checks refuses a version newer than the
// reader's release, on its own.
func TestEachJournalVersionCheckRefusesANewerVersion(t *testing.T) {
	for _, c := range []struct {
		name    string
		check   func(int) bool
		version int
	}{
		{"supported", supportedRunEvacuationCheckpointVersion, runEvacuationRollbackCheckpointVersion},
		{"supported", supportedRunEvacuationCheckpointVersion, runEvacuationRollForwardCheckpointVersion},
		{"supported", supportedRunEvacuationCheckpointVersion, runEvacuationRestartCheckpointVersion},
		{"rollback", rollbackRunEvacuationCheckpointVersion, runEvacuationRollbackCheckpointVersion},
		{"rollback", rollbackRunEvacuationCheckpointVersion, runEvacuationRollForwardCheckpointVersion},
		{"roll-forward", rollForwardRunEvacuationCheckpointVersion, runEvacuationRollForwardCheckpointVersion},
		{"roll-forward", rollForwardRunEvacuationCheckpointVersion, runEvacuationRestartCheckpointVersion},
	} {
		t.Run(fmt.Sprintf("%s/version %d", c.name, c.version), func(t *testing.T) {
			if !c.check(c.version) {
				t.Fatalf("this release refuses version %d", c.version)
			}
			readAs(t, c.version)
			if !c.check(c.version) {
				t.Fatalf("a version %d reader refuses its own version", c.version)
			}
			readAs(t, c.version-1)
			if c.check(c.version) {
				t.Fatalf("a version %d reader accepts version %d", c.version-1, c.version)
			}
		})
	}
}
