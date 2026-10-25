package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	rollForwardStagedElapsed   = 20 * time.Second
	rollForwardStagedRemaining = 40 * time.Second
	rollForwardCommitTarget    = 55 * time.Second
)

type rollForwardOptions struct {
	// version is the journal version the predecessor writes (default 5).
	version int
	// phase is the last phase the predecessor recorded (default
	// replacement_committing).
	phase   string
	standby bool
	paused  bool
	// committedCapacity marks the replacement reservation committed, as the
	// predecessor does right after CommitWork.
	committedCapacity bool
	// noCapacity journals no reservations at all.
	noCapacity bool
	// priorCompletedSurvivor records the survivor gen-c as completed in the
	// prior lineage, before the evacuation began.
	priorCompletedSurvivor bool
	commitTarget           time.Duration
	// noSurvivor leaves the source as the run's only participant.
	noSurvivor bool
	// secondMovedGroup puts a second group, gb (3 VUsers), on the source.
	secondMovedGroup bool
}

// newEvacuationRollForwardFixture journals an evacuation of gen-a to gen-b that
// left the survivor gen-c unchanged, up to the given phase, then hands the
// execution lease to a successor that has not yet reconciled.
func newEvacuationRollForwardFixture(t *testing.T, opts rollForwardOptions, beforeHandOver ...func(*evacuationRollbackFixture)) *evacuationRollbackFixture {
	t.Helper()
	if opts.version == 0 {
		opts.version = runEvacuationRollForwardCheckpointVersion
	}
	if opts.phase == "" {
		opts.phase = runEvacuationPhaseReplacementCommitting
	}
	if opts.commitTarget == 0 {
		opts.commitTarget = rollForwardCommitTarget
	}
	ctx := context.Background()
	tr, store, test, generation, owner, token, _, _ := evacuationCheckpointFixture(t)
	tr.evacuationRollbackRecovery = opts.version >= runEvacuationRollbackCheckpointVersion
	tr.evacuationRollForwardRecovery = opts.version == runEvacuationRollForwardCheckpointVersion
	started := time.Now().Add(-rollForwardStagedElapsed).UTC()
	test.StartedAt = &started
	test.Duration = rollForwardStagedElapsed + rollForwardStagedRemaining
	test.Groups = []TestGroup{{Name: "ga", Protocol: "http", VUsers: 5}, {Name: "gc", Protocol: "http", VUsers: 3}}
	if opts.paused {
		test.Status = "PAUSED"
		pausedAt := time.Now().UTC()
		test.PausedAt = &pausedAt
	}
	previous := map[string][]GeneratorShardSummary{
		"ga": {{Group: "ga", Target: "gen-a", VUsers: 5, ShardCount: 1}},
		"gc": {{Group: "gc", Target: "gen-c", VUsers: 3, ShardCount: 1}},
	}
	if opts.noSurvivor {
		test.Groups = test.Groups[:1]
		delete(previous, "gc")
	}
	if opts.secondMovedGroup {
		test.Groups = append(test.Groups, TestGroup{Name: "gb", Protocol: "http", VUsers: 3})
		previous["gb"] = []GeneratorShardSummary{{Group: "gb", Target: "gen-a", VUsers: 3, ShardCount: 1}}
	}
	next := cloneGroupShardMap(previous)
	next["ga"][0].Target = "gen-b"
	if opts.secondMovedGroup {
		next["gb"][0].Target = "gen-b"
	}
	if opts.priorCompletedSurvivor {
		test.remoteTargetStates = map[string]string{"gen-c": "complete"}
	}
	tr.armRunContinuity(ctx, test, generation, owner, token, previous)
	f := &evacuationRollbackFixture{tr: tr, store: store, test: test, oldOwner: owner, oldGen: generation, token: token}
	var journal runEvacuationCapacity
	if !opts.noCapacity {
		f.source = storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "gen-a", TestID: test.ID, Incarnation: "pod-a", Generation: generation, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: rollbackBudget, Demand: rollbackDemand, GroupPeaks: map[string]int32{"ga": 5}, GroupDemands: map[string]protocol.CapacityVector{"ga": rollbackDemand}}
		reserveCommitted(t, store, f.source, owner, generation)
		if !opts.noSurvivor {
			survivor := storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "gen-c", TestID: test.ID, Incarnation: "pod-c", Generation: generation, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: rollbackBudget, Demand: rollbackDemand, GroupPeaks: map[string]int32{"gc": 3}, GroupDemands: map[string]protocol.CapacityVector{"gc": rollbackDemand}}
			reserveCommitted(t, store, survivor, owner, generation)
		}
		if opts.standby {
			idle := storage.PodCapacityReservation{ReservationID: "spare", Revision: 3, Target: "gen-b", TestID: test.ID, Incarnation: "pod-b", Generation: generation, State: "committed", ModelVersion: protocol.CapacityReservationModel, Budget: rollbackBudget, Demand: rollbackBudget, Dedicated: true}
			reserveCommitted(t, store, idle, owner, generation)
			idle = rollForwardLedgerRow(t, store, test.ID, "gen-b")
			activated := idle
			activated.State = "pending"
			activated.Revision++
			activated.GroupPeaks = map[string]int32{"ga": 5}
			activated.GroupDemands = map[string]protocol.CapacityVector{"ga": rollbackDemand}
			if err := store.ReservePodCapacityIfUnchanged([]storage.PodCapacityReservation{activated}, []storage.PodCapacityReservation{idle}, executionLeaseName, owner, generation); err != nil {
				t.Fatal(err)
			}
			f.claim, f.standby = activated, &idle
			journal = runEvacuationCapacity{claims: []storage.PodCapacityReservation{activated}, preImages: []storage.PodCapacityReservation{idle}}
		} else {
			f.claim = storage.PodCapacityReservation{ReservationID: "attempt", Revision: 1, Target: "gen-b", TestID: test.ID, Incarnation: "pod-b", Generation: generation, State: "pending", ModelVersion: protocol.CapacityReservationModel, Budget: rollbackBudget, Demand: rollbackDemand, GroupPeaks: map[string]int32{"ga": 5}, GroupDemands: map[string]protocol.CapacityVector{"ga": rollbackDemand}}
			if err := store.ReservePodCapacity([]storage.PodCapacityReservation{f.claim}, executionLeaseName, owner, generation); err != nil {
				t.Fatal(err)
			}
			journal = runEvacuationCapacity{claims: []storage.PodCapacityReservation{f.claim}}
		}
	}
	guard, err := tr.beginRunEvacuationCheckpoint(ctx, test, generation, owner, "gen-a", token, previous, next, journal)
	if err != nil {
		t.Fatal(err)
	}
	if opts.phase != runEvacuationPhasePrepared {
		if guard, err = tr.markRunEvacuationStaged(ctx, test, guard, generation, owner, []string{"gen-b"}, runEvacuationStagedTiming{steadyElapsed: rollForwardStagedElapsed, remaining: rollForwardStagedRemaining, paused: opts.paused}); err != nil {
			t.Fatal(err)
		}
	}
	fenced := opts.phase == runEvacuationPhaseSourceFenced || opts.phase == runEvacuationPhaseReplacementCommitting
	if fenced {
		if guard, err = tr.markRunEvacuationSourceFenced(ctx, test, guard, generation, owner); err != nil {
			t.Fatal(err)
		}
	}
	if fenced && !opts.noCapacity {
		// As the leader does once its source is fenced.
		if err := store.TransitionPodCapacity("gen-a", test.ID, "pod-a", "attempt", "releasing", executionLeaseName, owner, generation); err != nil {
			t.Fatal(err)
		}
	}
	if opts.phase == runEvacuationPhaseReplacementCommitting {
		if guard, err = tr.markRunEvacuationReplacementCommitting(ctx, test, guard, generation, owner, opts.commitTarget); err != nil {
			t.Fatal(err)
		}
		if opts.committedCapacity {
			if err := store.TransitionPodCapacity("gen-b", test.ID, "pod-b", f.claim.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.guard = guard
	f.sourceGen = newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "pod-a")
	f.spareGen = newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "pod-b")
	f.survivorGen = newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "pod-c")
	for _, prepare := range beforeHandOver {
		prepare(f)
	}
	f.handOver(t, "roll-forward-successor")
	return f
}

func rollForwardLedgerRow(t *testing.T, store *storage.SQLiteStore, testID, target string) storage.PodCapacityReservation {
	t.Helper()
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.TestID == testID && row.Target == target {
			return row
		}
	}
	t.Fatalf("no reservation for %s", target)
	return storage.PodCapacityReservation{}
}

// assertRolledForward checks that the run now runs the next topology under the
// successor's term: the survivor and the replacement adopted, the replacement
// shortened to the journaled target with the rotated token, and the journal
// replaced by the plain checkpoint.
func (f *evacuationRollbackFixture) assertRolledForward(t *testing.T, owner string, generation uint64, target time.Duration) {
	t.Helper()
	resume := f.test.adoptedResume
	if resume == nil || resume.finishStatus != "" || resume.adopted["gen-b"] == nil || resume.adopted["gen-c"] == nil || resume.adopted["gen-a"] != nil {
		t.Fatalf("next topology not adopted: %+v", resume)
	}
	if f.test.remoteClientsByTarget["gen-a"] != nil || f.test.remoteArchiveClientsByTarget["gen-a"] == nil {
		t.Fatal("the fenced source became a participant or lost its archive transport")
	}
	for name, gen := range map[string]*rollbackGeneratorClient{"replacement": f.spareGen, "survivor": f.survivorGen} {
		claims, _ := gen.snapshot()
		if len(claims) == 0 || len(claims[len(claims)-1]) != 1 || claims[len(claims)-1][0].PriorGeneration != f.oldGen {
			t.Fatalf("%s adoption = %+v", name, claims)
		}
	}
	if claims, _ := f.sourceGen.snapshot(); len(claims) != 0 {
		t.Fatalf("the fenced source was claimed: %+v", claims)
	}
	adjustments := f.spareGen.durationAdjustments()
	if len(adjustments) == 0 {
		t.Fatal("the replacement duration was not adjusted")
	}
	last := adjustments[len(adjustments)-1]
	if last.ExecutionGeneration != generation || last.TargetDuration != target || last.TargetDurationSeconds != int64(target/time.Second) || string(last.RunSupervisionToken) != string(f.test.remoteRunSupervisionToken[:]) {
		t.Fatalf("replacement duration adjustment = %+v", last)
	}
	if survivor := f.survivorGen.durationAdjustments(); len(survivor) != 0 {
		t.Fatalf("survivor duration changed: %+v", survivor)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "RUNNING" || strings.HasPrefix(record.Error, uncertainEvacuationReasonPrefix) {
		t.Fatalf("durable run = %+v, %v", record, err)
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
	if err != nil || !present || checkpoint.Shards["ga"][0].Target != "gen-b" || checkpoint.Shards["gc"][0].Target != "gen-c" ||
		checkpoint.ExecutionGeneration != generation || checkpoint.ExecutionLeaseOwnerID != owner || !slices.Contains(checkpoint.ArchiveTargets, "gen-a") {
		t.Fatalf("rolled-forward checkpoint = %+v, %v", checkpoint, err)
	}
}

// assertFailedRollForwardHeld checks that a failed roll-forward kept the
// journal, force-stopped what this term adopted with the rotated token and left
// the run STOPPING with no runtime, so attestation and a newer term's fences
// can resolve it.
func (f *evacuationRollbackFixture) assertFailedRollForwardHeld(t *testing.T, generation uint64, problem string, survivorAdopted, replacementAdopted bool) {
	t.Helper()
	if f.test.adoptedResume != nil || f.test.executionCtx != nil || f.test.cancel != nil {
		t.Fatalf("failed roll-forward installed a runtime: %+v", f.test.adoptedResume)
	}
	guard, _ := f.durableGuard(t)
	if guard.Phase != runEvacuationPhaseRollingForward || guard.RollForwardGeneration != generation || guard.Version != runEvacuationRollForwardCheckpointVersion {
		t.Fatalf("journal = phase %q roll-forward generation %d", guard.Phase, guard.RollForwardGeneration)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.HasPrefix(record.Error, uncertainEvacuationReasonPrefix) || !strings.Contains(record.Error, problem) {
		t.Fatalf("durable run = %+v, %v", record, err)
	}
	f.tr.mu.RLock()
	eligible := interruptedEvacuationAwaitingProofLocked(f.test)
	f.tr.mu.RUnlock()
	if !eligible {
		t.Fatal("failed roll-forward is not eligible for administrator attestation")
	}
	for name, c := range map[string]struct {
		gen     *rollbackGeneratorClient
		adopted bool
	}{"survivor": {f.survivorGen, survivorAdopted}, "replacement": {f.spareGen, replacementAdopted}} {
		stops := 0
		for _, stopped := range c.gen.stopGenerations() {
			if stopped == generation {
				stops++
			}
		}
		if c.adopted && (stops != 1 || !c.gen.stoppedWithClaimedToken()) {
			t.Fatalf("adopted %s stops = %v, want one forced stop at %d with the rotated token", name, c.gen.stopGenerations(), generation)
		}
		if !c.adopted && stops != 0 {
			t.Fatalf("unadopted %s was stopped at %d: %v", name, generation, c.gen.stopGenerations())
		}
	}
	if stops := f.sourceGen.stopGenerations(); len(stops) != 0 {
		t.Fatalf("fenced source was stopped again: %v", stops)
	}
	// The same term adopted the next topology; it cannot fence it.
	if err := f.tr.recoverInterruptedRunEvacuation(f.test); err == nil || !strings.Contains(err.Error(), "recovery generation") {
		t.Fatalf("same-term fail-closed recovery = %v", err)
	}
}

func TestEvacuationJournalVersionFiveFollowsRollForwardRecovery(t *testing.T) {
	for _, mode := range []struct {
		name                  string
		rollback, rollForward bool
		want                  int
	}{
		{"both off", false, false, runEvacuationCheckpointVersion},
		{"roll-forward alone", false, true, runEvacuationCheckpointVersion},
		{"rollback alone", true, false, runEvacuationRollbackCheckpointVersion},
		{"both on", true, true, runEvacuationRollForwardCheckpointVersion},
	} {
		t.Run(mode.name, func(t *testing.T) {
			ctx := context.Background()
			tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			tr.evacuationRollbackRecovery, tr.evacuationRollForwardRecovery = mode.rollback, mode.rollForward
			tr.armRunContinuity(ctx, test, generation, owner, token, previous)
			raw, err := tr.beginRunEvacuationCheckpoint(ctx, test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
			if err != nil {
				t.Fatal(err)
			}
			timing := runEvacuationStagedTiming{steadyElapsed: rollForwardStagedElapsed, remaining: rollForwardStagedRemaining, paused: true}
			if raw, err = tr.markRunEvacuationStaged(ctx, test, raw, generation, owner, []string{"gen-b"}, timing); err != nil {
				t.Fatal(err)
			}
			var guard runEvacuationCheckpoint
			_ = json.Unmarshal([]byte(raw), &guard)
			if guard.Version != mode.want {
				t.Fatalf("journal version = %d, want %d", guard.Version, mode.want)
			}
			v5 := mode.want == runEvacuationRollForwardCheckpointVersion
			if v5 != (guard.StagedSteadyElapsed == timing.steadyElapsed && guard.StagedRemaining == timing.remaining && guard.StagedPaused) {
				t.Fatalf("staged timing = %v %v %v", guard.StagedSteadyElapsed, guard.StagedRemaining, guard.StagedPaused)
			}
			if !v5 {
				for _, field := range []string{"staged_steady_elapsed", "staged_remaining", "staged_paused", "committing_at", "commit_target_duration", "roll_forward"} {
					if strings.Contains(raw, field) {
						t.Fatalf("version %d journal carries %s: %s", guard.Version, field, raw)
					}
				}
			}
			if raw, err = tr.markRunEvacuationSourceFenced(ctx, test, raw, generation, owner); err != nil {
				t.Fatal(err)
			}
			fenced := raw
			if v5 {
				for _, refused := range []time.Duration{0, rollForwardStagedElapsed + rollForwardStagedRemaining + time.Nanosecond} {
					if _, err := tr.markRunEvacuationReplacementCommitting(ctx, test, raw, generation, owner, refused); err == nil {
						t.Fatalf("commit intent accepted target %s", refused)
					}
				}
				// The committed mark needs the commit intent first.
				if _, err := tr.markRunEvacuationReplacementCommitted(ctx, test, raw, generation, owner); err == nil {
					t.Fatal("a version 5 journal was committed without a commit intent")
				}
			}
			if raw, err = tr.markRunEvacuationReplacementCommitting(ctx, test, raw, generation, owner, rollForwardCommitTarget); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal([]byte(raw), &guard)
			if v5 && (guard.Phase != runEvacuationPhaseReplacementCommitting || guard.CommittingAt.IsZero() || guard.CommitTargetDuration != rollForwardCommitTarget) {
				t.Fatalf("commit intent = %+v", guard)
			}
			if !v5 && raw != fenced {
				t.Fatal("an older journal recorded a commit intent")
			}
			if raw, err = tr.markRunEvacuationReplacementCommitted(ctx, test, raw, generation, owner); err != nil {
				t.Fatal(err)
			}
			if _, committed := committedRunEvacuationCheckpoint(raw, test.ID); !committed {
				t.Fatal("committed journal lacks promotion proof")
			}
			// The adoption checkpoint reader rejects the journal.
			if _, _, err := decodeRunContinuityCheckpoint(raw); err == nil {
				t.Fatal("an adoption reader accepted the evacuation journal")
			}
		})
	}
}

// A version 5 journal is a superset of version 4: before the source fence it
// is rolled back, and its leader marks or restores it exactly as before.
func TestVersionFiveJournalKeepsRollbackBehavior(t *testing.T) {
	for _, phase := range []string{runEvacuationPhasePrepared, runEvacuationPhaseStaged} {
		t.Run("successor/"+phase, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: phase})
			if guard, _ := f.durableGuard(t); guard.Version != runEvacuationRollForwardCheckpointVersion {
				t.Fatalf("journal version = %d", guard.Version)
			}
			if _, _, rollForward := f.tr.evacuationRollForwardGuard(f.test, f.guard); rollForward {
				t.Fatal("an unfenced evacuation was accepted for roll-forward")
			}
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			resume := f.test.adoptedResume
			if resume == nil || resume.finishStatus != "" || resume.adopted["gen-a"] == nil || resume.adopted["gen-c"] == nil || resume.adopted["gen-b"] != nil {
				t.Fatalf("prior topology not adopted: %+v", resume)
			}
			if claims, _ := f.spareGen.snapshot(); len(claims) != 1 || len(claims[0]) != 0 {
				t.Fatalf("replacement epoch claims = %+v", claims)
			}
			if _, kept := f.ledger(t)["gen-b"]; kept {
				t.Fatal("replacement reservation was not released")
			}
		})
	}
	t.Run("same leader", func(t *testing.T) {
		ctx := context.Background()
		tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
		tr.evacuationRollbackRecovery, tr.evacuationRollForwardRecovery = true, true
		tr.armRunContinuity(ctx, test, generation, owner, token, previous)
		before := test.continuityCheckpoint
		guard, err := tr.beginRunEvacuationCheckpoint(ctx, test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
		if err != nil {
			t.Fatal(err)
		}
		if guard, err = tr.markRunEvacuationStaged(ctx, test, guard, generation, owner, []string{"gen-b"}, runEvacuationStagedTiming{remaining: time.Minute}); err != nil {
			t.Fatal(err)
		}
		marked := tr.markRunEvacuationCheckpointAbandoned(test, guard, generation, owner)
		var decoded runEvacuationCheckpoint
		if err := json.Unmarshal([]byte(marked), &decoded); err != nil || decoded.Version != runEvacuationRollForwardCheckpointVersion || decoded.AbandonedAt.IsZero() {
			t.Fatalf("abandoned version 5 journal = %+v, %v", decoded, err)
		}
		tr.restoreRunEvacuationPriorCheckpoint(test, marked, generation, owner)
		record, err := store.GetTest(test.ID)
		if err != nil || record.ContinuityCheckpoint != before {
			t.Fatalf("version 5 journal was not restored to the prior checkpoint: %v", err)
		}
	})
}

func TestEvacuationRollForwardAdoptsCommittedReplacement(t *testing.T) {
	for _, standby := range []bool{false, true} {
		for _, committed := range []bool{false, true} {
			for _, paused := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/%s", map[bool]string{false: "replacement", true: "standby"}[standby], map[bool]string{false: "pending", true: "committed"}[committed], map[bool]string{false: "running", true: "paused"}[paused])
				t.Run(name, func(t *testing.T) {
					f := newEvacuationRollForwardFixture(t, rollForwardOptions{standby: standby, committedCapacity: committed, paused: paused})
					owner, generation := f.tr.leadership.executionLeaseTerm()
					status := f.test.Status
					if err := f.tr.reconcileStoredActiveTest(f.test); err != nil || f.test.Status != status {
						t.Fatalf("roll-forward candidate reconciled to %s: %v", f.test.Status, err)
					}
					if err := f.tr.adoptOrphanedRuns(); err != nil {
						t.Fatal(err)
					}
					if f.test.Status != status {
						t.Fatalf("status after roll-forward = %s, want %s", f.test.Status, status)
					}
					f.assertRolledForward(t, owner, generation, rollForwardCommitTarget)
					ledger := f.ledger(t)
					for _, target := range []string{"gen-b", "gen-c"} {
						if row := ledger[target]; row.State != "committed" || row.Generation != generation {
							t.Fatalf("%s capacity not transferred: %+v", target, row)
						}
					}
					if row := ledger["gen-a"]; row.Generation != f.oldGen || row.State != "releasing" {
						t.Fatalf("fenced source capacity changed: %+v", row)
					}
					if row := ledger["gen-b"]; standby && (row.ReservationID != f.standby.ReservationID || row.Revision != f.claim.Revision || len(row.GroupPeaks) == 0) {
						t.Fatalf("activated standby = %+v", row)
					}
					_, capacity := f.spareGen.snapshot()
					if want := map[bool]int{false: 2, true: 1}[committed]; len(capacity) != want {
						t.Fatalf("replacement capacity requests = %d, want %d", len(capacity), want)
					}
					for _, request := range capacity {
						if request.Release || request.ExecutionGeneration != generation || request.ReservationId != f.claim.ReservationID || len(request.GroupPeaks) == 0 {
							t.Fatalf("replacement capacity request = %+v", request)
						}
					}
				})
			}
		}
	}
}

func TestEvacuationRollForwardHoldsUncommittedReplacement(t *testing.T) {
	cases := map[string]struct {
		outcome pb.RunAdoptionOutcome
		down    bool
		want    string
	}{
		"rejected":        {outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, want: "gen-b: REJECTED"},
		"unknown test":    {outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST, want: "gen-b: UNKNOWN_TEST"},
		"already stopped": {outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, want: "gen-b: ALREADY_STOPPED"},
		"unreachable":     {down: true, want: "replacement unreachable"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			f.spareGen.outcome, f.spareDown = c.outcome, c.down
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertFailedRollForwardHeld(t, generation, c.want, true, false)
			if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != 0 {
				t.Fatalf("an uncommitted replacement was adjusted: %+v", adjustments)
			}
			if _, capacity := f.spareGen.snapshot(); len(capacity) != 0 {
				t.Fatalf("an uncommitted replacement received capacity requests: %+v", capacity)
			}
			if row := f.ledger(t)["gen-b"]; row.State != "pending" || row.Generation != f.oldGen {
				t.Fatalf("replacement capacity changed without a commit: %+v", row)
			}
			if !c.down {
				// The claim that carried the refused adoption drains the staged
				// assignment; no claim omitted the replacement.
				if claims, _ := f.spareGen.snapshot(); len(claims) != 1 || len(claims[0]) != 1 {
					t.Fatalf("replacement epoch claims = %+v", claims)
				}
			}
		})
	}
}

func TestEvacuationRollForwardKeepsFailClosedPaths(t *testing.T) {
	cases := map[string]struct {
		opts   rollForwardOptions
		before func(*testing.T, *evacuationRollbackFixture)
		mutate func(*testing.T, *evacuationRollbackFixture)
		want   string
	}{
		"version 3 source fenced": {opts: rollForwardOptions{version: runEvacuationCheckpointVersion, phase: runEvacuationPhaseSourceFenced}},
		"version 4 source fenced": {opts: rollForwardOptions{version: runEvacuationRollbackCheckpointVersion, phase: runEvacuationPhaseSourceFenced}},
		"version 5 source fenced": {opts: rollForwardOptions{phase: runEvacuationPhaseSourceFenced}, want: "the replacement never committed"},
		"abandoned": {before: func(t *testing.T, f *evacuationRollbackFixture) {
			var guard runEvacuationCheckpoint
			_ = json.Unmarshal([]byte(f.guard), &guard)
			guard.AbandonedAt = time.Now().UTC()
			raw := encodeEvacuationGuard(t, guard)
			if err := f.tr.swapRunContinuityCheckpoint(f.test, f.guard, raw, f.oldOwner); err != nil {
				t.Fatal(err)
			}
			f.guard = raw
		}},
		"stopping": {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.test.Status = "STOPPING" }},
		"fail policy": {mutate: func(_ *testing.T, f *evacuationRollbackFixture) {
			f.test.Assignment = &GeneratorAssignmentSettings{FailoverPolicy: "fail"}
		}},
		"disabled":       {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.tr.evacuationRollForwardRecovery = false }},
		"rollback off":   {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.tr.evacuationRollbackRecovery = false }},
		"pause mismatch": {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.test.Status = "PAUSED" }},
		"resumed since staging": {opts: rollForwardOptions{paused: true}, mutate: func(_ *testing.T, f *evacuationRollbackFixture) {
			f.test.Status, f.test.PausedAt = "RUNNING", nil
		}},
		"stateful group": {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.test.Groups[0].Protocol = "browser" }},
		"unknown group":  {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.test.Groups = f.test.Groups[1:] }},
		"rendezvous group": {mutate: func(t *testing.T, f *evacuationRollbackFixture) {
			script := &dsl.Script{Name: "meet", Protocol: "http", Actions: []dsl.Action{{Name: "meet", Type: "rendezvous", Params: map[string]any{
				"scope": "distributed_group", "key": "meet", "timeout_ms": 1000, "on_timeout": "fail",
			}}}}
			if specs, err := dsl.ScriptDistributedRendezvous(script); err != nil || len(specs) != 1 {
				t.Fatalf("rendezvous fixture = %v, %v", specs, err)
			}
			if ok, why := groupAllowsGeneratorLossReshard(TestGroup{Name: "ga", Protocol: "http", script: script}); !ok {
				t.Fatalf("rendezvous fixture refused for another reason: %s", why)
			}
			f.test.Groups[0].script = script
		}},
		"stateful data group": {mutate: func(t *testing.T, f *evacuationRollbackFixture) {
			script := &dsl.Script{Name: "accounts", Protocol: "http", DataSources: []dsl.DataSource{{Name: "accounts", Type: "csv", InlineData: "id\n1\n", Strategy: "sequential"}}}
			if ok, why := groupAllowsGeneratorLossReshard(TestGroup{Name: "ga", Protocol: "http", script: script}); !ok {
				t.Fatalf("data fixture refused for another reason: %s", why)
			}
			f.test.Groups[0].script = script
		}},
		"iterations": {mutate: func(_ *testing.T, f *evacuationRollbackFixture) { f.test.TerminationMode = "iterations" }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var hooks []func(*evacuationRollbackFixture)
			if c.before != nil {
				hooks = append(hooks, func(f *evacuationRollbackFixture) { c.before(t, f) })
			}
			f := newEvacuationRollForwardFixture(t, c.opts, hooks...)
			if c.mutate != nil {
				c.mutate(t, f)
			}
			if _, _, rollForward := f.tr.evacuationRollForwardGuard(f.test, f.test.continuityCheckpoint); rollForward {
				t.Fatal("journal was accepted for roll-forward")
			}
			if f.tr.isRunAdoptionCandidate(f.test) {
				t.Fatal("journal became an adoption candidate")
			}
			if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
				t.Fatal(err)
			}
			record, err := f.store.GetTest(f.test.ID)
			if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.HasPrefix(record.Error, uncertainEvacuationReasonPrefix) || record.ContinuityCheckpoint != f.guard {
				t.Fatalf("journal reconciled to %+v, %v", record, err)
			}
			if c.want != "" && !strings.Contains(record.Error, c.want) {
				t.Fatalf("held reason = %q, want %q", record.Error, c.want)
			}
			if claims, _ := f.spareGen.snapshot(); len(claims) != 0 {
				t.Fatalf("a fail-closed journal reached the replacement: %+v", claims)
			}
		})
	}
}

func TestEvacuationRollForwardPlanRejectsUnsafeJournals(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	if _, replacements, err := runEvacuationRollForwardPlan(f.guard, f.test.ID); err != nil || len(replacements) != 1 || replacements[0] != "gen-b" {
		t.Fatalf("valid journal rejected: %v %v", replacements, err)
	}
	mutations := map[string]struct {
		mutate func(*runEvacuationCheckpoint)
		want   string
	}{
		"version 4": {func(g *runEvacuationCheckpoint) { g.Version = runEvacuationRollbackCheckpointVersion }, "contradictory progress"},
		"source fenced only": {func(g *runEvacuationCheckpoint) {
			g.Phase, g.CommittingAt, g.CommitTargetDuration = runEvacuationPhaseSourceFenced, time.Time{}, 0
		}, "never received CommitWork"},
		"abandoned":        {func(g *runEvacuationCheckpoint) { g.AbandonedAt = time.Now().UTC() }, "abandoned by its leader"},
		"unarmed lineage":  {func(g *runEvacuationCheckpoint) { g.Prior.ArmedAt = time.Time{} }, "never armed"},
		"source completed": {func(g *runEvacuationCheckpoint) { g.Prior.CompletedTargets = []string{"gen-a"} }, "source already completed"},
		"survivor changed": {func(g *runEvacuationCheckpoint) { g.NextShards["gc"][0].VUsers = 4 }, "changed surviving participant"},
		"claim committed":  {func(g *runEvacuationCheckpoint) { g.CapacityClaims[0].State = "committed" }, "invalid replacement claim"},
		"no target":        {func(g *runEvacuationCheckpoint) { g.CommitTargetDuration = 0 }, "no usable replacement duration"},
		"longer target": {func(g *runEvacuationCheckpoint) {
			g.CommitTargetDuration = g.StagedSteadyElapsed + g.StagedRemaining + time.Second
		}, "no usable replacement duration"},
		"no staged remainder": {func(g *runEvacuationCheckpoint) { g.StagedRemaining = 0 }, "no usable replacement duration"},
		"negative offset":     {func(g *runEvacuationCheckpoint) { g.StagedSteadyElapsed = -time.Second }, "no usable replacement duration"},
	}
	for name, c := range mutations {
		t.Run(name, func(t *testing.T) {
			var guard runEvacuationCheckpoint
			_ = json.Unmarshal([]byte(f.guard), &guard)
			c.mutate(&guard)
			if _, _, err := runEvacuationRollForwardPlan(encodeEvacuationGuard(t, guard), f.test.ID); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("plan error = %v, want %q", err, c.want)
			}
		})
	}
}

func TestRollingForwardJournalRejectsContradictions(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	owner, generation := f.tr.leadership.executionLeaseTerm()
	guard, _, err := runEvacuationRollForwardPlan(f.guard, f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The source of a committing journal is durably fenced.
	if _, targets, err := interruptedEvacuationTargets(f.guard, f.test.ID); err != nil || !slices.Equal(targets, []string{"gen-b", "gen-c"}) {
		t.Fatalf("committing journal participants = %v, %v", targets, err)
	}
	committing := guard
	for name, mutate := range map[string]func(*runEvacuationCheckpoint){
		"version 4":          func(g *runEvacuationCheckpoint) { g.Version = runEvacuationRollbackCheckpointVersion },
		"no commit intent":   func(g *runEvacuationCheckpoint) { g.CommittingAt = time.Time{} },
		"source unfenced":    func(g *runEvacuationCheckpoint) { g.SourceFencedAt = time.Time{} },
		"replacement commit": func(g *runEvacuationCheckpoint) { g.ReplacementCommittedAt = time.Now().UTC() },
		"unstaged":           func(g *runEvacuationCheckpoint) { g.StagedTargets = nil },
		"successor term":     func(g *runEvacuationCheckpoint) { g.RollForwardGeneration = generation },
	} {
		t.Run("committing/"+name, func(t *testing.T) {
			g := committing
			mutate(&g)
			encoded := encodeEvacuationGuard(t, g)
			if _, _, err := interruptedEvacuationTargets(encoded, f.test.ID); err == nil || !strings.Contains(err.Error(), "committing evacuation has contradictory progress") {
				t.Fatalf("fail-closed recovery accepted it: %v", err)
			}
			if _, _, err := runEvacuationRollForwardPlan(encoded, f.test.ID); err == nil {
				t.Fatal("roll-forward accepted it")
			}
		})
	}
	planned, _, err := f.tr.planRunAdoptionIntent(context.Background(), f.test.ID, runEvacuationRollForwardCheckpoint(guard), generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := f.tr.persistRunEvacuationRollForwardIntent(f.test, f.guard, guard, planned, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, targets, err := interruptedEvacuationTargets(raw, f.test.ID); err != nil || !slices.Equal(targets, []string{"gen-b", "gen-c"}) {
		t.Fatalf("valid rolling_forward journal participants = %v, %v", targets, err)
	}
	if _, _, err := runEvacuationRollForwardPlan(raw, f.test.ID); err != nil {
		t.Fatalf("valid rolling_forward journal rejected for roll-forward: %v", err)
	}
	if _, _, err := runEvacuationRollbackPlan(raw, f.test.ID); err == nil {
		t.Fatal("a rolling_forward journal was accepted for rollback")
	}
	for name, mutate := range map[string]func(*runEvacuationCheckpoint){
		"version 4":          func(g *runEvacuationCheckpoint) { g.Version = runEvacuationRollbackCheckpointVersion },
		"roll-forward term":  func(g *runEvacuationCheckpoint) { g.RollForwardGeneration = g.Generation },
		"roll-forward owner": func(g *runEvacuationCheckpoint) { g.RollForwardOwnerID = "" },
		"roll-forward start": func(g *runEvacuationCheckpoint) { g.RollForwardStartedAt = time.Time{} },
		"rollback term":      func(g *runEvacuationCheckpoint) { g.RollbackGeneration = generation },
		"staged targets":     func(g *runEvacuationCheckpoint) { g.StagedTargets = []string{"gen-z"} },
		"source unfenced":    func(g *runEvacuationCheckpoint) { g.SourceFencedAt = time.Time{} },
		"no commit intent":   func(g *runEvacuationCheckpoint) { g.CommittingAt = time.Time{} },
		"replacement commit": func(g *runEvacuationCheckpoint) { g.ReplacementCommittedAt = time.Now().UTC() },
		"no checkpoint":      func(g *runEvacuationCheckpoint) { g.RollForwardCheckpoint = nil },
		"prior topology":     func(g *runEvacuationCheckpoint) { g.RollForwardCheckpoint.Shards = cloneGroupShardMap(g.BeforeShards) },
		"source not archived": func(g *runEvacuationCheckpoint) {
			g.RollForwardCheckpoint.ArchiveTargets = nil
		},
		"other lineage owner": func(g *runEvacuationCheckpoint) { g.RollForwardCheckpoint.ExecutionLeaseOwnerID = "other" },
		"other lineage term":  func(g *runEvacuationCheckpoint) { g.RollForwardCheckpoint.ExecutionGeneration++ },
		"other token": func(g *runEvacuationCheckpoint) {
			g.RollForwardCheckpoint.RunSupervisionToken.Ciphertext = "other"
		},
		"no intent": func(g *runEvacuationCheckpoint) { g.RollForwardCheckpoint.PendingAdoptions = nil },
		"intent of another owner": func(g *runEvacuationCheckpoint) {
			g.RollForwardCheckpoint.PendingAdoptions[len(g.RollForwardCheckpoint.PendingAdoptions)-1].OwnerID = "other"
		},
	} {
		t.Run("rolling_forward/"+name, func(t *testing.T) {
			var g runEvacuationCheckpoint
			if err := json.Unmarshal([]byte(raw), &g); err != nil {
				t.Fatal(err)
			}
			mutate(&g)
			encoded := encodeEvacuationGuard(t, g)
			if _, _, err := interruptedEvacuationTargets(encoded, f.test.ID); err == nil || !strings.Contains(err.Error(), "rolling-forward evacuation has contradictory progress") {
				t.Fatalf("fail-closed recovery accepted it: %v", err)
			}
			if _, _, err := runEvacuationRollForwardPlan(encoded, f.test.ID); err == nil {
				t.Fatal("roll-forward accepted it")
			}
		})
	}
}

// rollForwardFaultStore injects journal and capacity failures at the
// roll-forward's boundaries.
type rollForwardFaultStore struct {
	*storage.SQLiteStore
	failIntent    int
	failPlainSwap bool
	panicRearm    bool
}

func (s *rollForwardFaultStore) CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner string) (storage.TestStatusTransitionResult, error) {
	var guard runEvacuationCheckpoint
	if s.failIntent > 0 && json.Unmarshal([]byte(next), &guard) == nil && guard.Phase == runEvacuationPhaseRollingForward {
		s.failIntent--
		return "", errors.New("injected roll-forward intent failure")
	}
	_, nextPlain, nextErr := decodeRunContinuityCheckpoint(next)
	if s.failPlainSwap && nextErr == nil && nextPlain && interruptedRunEvacuation(expected) {
		return "", errors.New("injected journal swap failure")
	}
	if _, expectedPlain, err := decodeRunContinuityCheckpoint(expected); s.panicRearm && err == nil && expectedPlain {
		panic("re-arm failure")
	}
	return s.SQLiteStore.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner)
}

func TestEvacuationRollForwardInterruptionBoundaries(t *testing.T) {
	// Each case is a controller lost at one boundary of evacuateGenerator; the
	// journal and the generators show what had happened.
	t.Run("before CommitWork", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, generation, "gen-b: REJECTED", true, false)
	})
	t.Run("after CommitWork before capacity", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		owner, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertRolledForward(t, owner, generation, rollForwardCommitTarget)
	})
	for _, adjusted := range []bool{false, true} {
		name := map[bool]string{false: "after capacity before AdjustDuration", true: "after AdjustDuration before the committed mark"}[adjusted]
		t.Run(name, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{committedCapacity: true}, func(f *evacuationRollbackFixture) {
				if adjusted {
					_, _ = f.spareGen.AdjustDuration(context.Background(), generatorrpc.DurationAdjustment{TestID: f.test.ID, ExecutionGeneration: f.oldGen, TargetDuration: rollForwardCommitTarget, RunSupervisionToken: f.token})
				}
			})
			owner, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertRolledForward(t, owner, generation, rollForwardCommitTarget)
			// The absolute target is sent again; repeating it changes nothing.
			if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != map[bool]int{false: 1, true: 2}[adjusted] {
				t.Fatalf("duration adjustments = %+v", adjustments)
			}
		})
	}
}

func TestEvacuationRollForwardFailuresKeepJournal(t *testing.T) {
	cases := map[string]struct {
		prepare            func(*evacuationRollbackFixture)
		want               string
		replacementAdopted bool
		capacityReturned   bool
	}{
		"duration rejected":    {prepare: func(f *evacuationRollbackFixture) { f.spareGen.rejectAdjust = true }, want: "was not shortened to its journaled duration", replacementAdopted: true},
		"capacity unconfirmed": {prepare: func(f *evacuationRollbackFixture) { f.spareGen.rejectCap = true }, want: "evacuation capacity commit", replacementAdopted: true},
		"replacement row changed": {prepare: func(f *evacuationRollbackFixture) {
			f.tr.testStore = &rollbackLedgerStore{SQLiteStore: f.store, rewrite: func(r *storage.PodCapacityReservation) {
				if r.Target == "gen-b" {
					r.Demand.VUsers++
				}
			}}
		}, want: "replacement reservation changed since the evacuation journal", replacementAdopted: true},
		"replacement row from another term": {prepare: func(f *evacuationRollbackFixture) {
			f.tr.testStore = &rollbackLedgerStore{SQLiteStore: f.store, rewrite: func(r *storage.PodCapacityReservation) {
				if r.Target == "gen-b" {
					r.Generation += 100
				}
			}}
		}, want: "replacement reservation is pending under generation", replacementAdopted: true},
		"committed row from an older term": {prepare: func(f *evacuationRollbackFixture) {
			older := f.oldGen - 1
			f.tr.testStore = &rollbackLedgerStore{SQLiteStore: f.store, rewrite: func(r *storage.PodCapacityReservation) {
				if r.Target == "gen-b" {
					r.State, r.Generation = "committed", older
				}
			}}
		}, want: "replacement reservation is committed under generation", replacementAdopted: true},
		"journal swap fails": {prepare: func(f *evacuationRollbackFixture) {
			f.tr.testStore = &rollForwardFaultStore{SQLiteStore: f.store, failPlainSwap: true}
		}, want: "record evacuation roll-forward", replacementAdopted: true, capacityReturned: true},
		"panic before swap": {prepare: func(f *evacuationRollbackFixture) { f.spareGen.panicCap = true }, want: "internal error while rolling the evacuation forward", replacementAdopted: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			c.prepare(f)
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertFailedRollForwardHeld(t, generation, c.want, true, c.replacementAdopted)
			row := f.ledger(t)["gen-b"]
			if c.capacityReturned {
				// The capacity was transferred before the swap failed; a newer
				// term's fences release it.
				if row.State != "committed" || row.Generation != generation {
					t.Fatalf("replacement capacity before the failed swap = %+v", row)
				}
				return
			}
			if row.State != "pending" || row.Generation != f.oldGen {
				t.Fatalf("replacement capacity changed by a failed roll-forward: %+v", row)
			}
		})
	}
}

// statusWriteFaultStore refuses the run's status writes while fail is set.
type statusWriteFaultStore struct {
	*storage.SQLiteStore
	fail atomic.Bool
	// beforeWrite runs before each write that is not refused. Set it before
	// any write can run.
	beforeWrite func()
}

func newStatusWriteFaultStore(store *storage.SQLiteStore) *statusWriteFaultStore {
	faults := &statusWriteFaultStore{SQLiteStore: store}
	faults.fail.Store(true)
	return faults
}

func (s *statusWriteFaultStore) UpdateTestStatusIfActiveAndLeaseHeld(id, status string, endTime *time.Time, errStr, lease, owner string) (storage.TestStatusTransitionResult, error) {
	if s.fail.Load() {
		return "", errors.New("injected status write failure")
	}
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
	return s.SQLiteStore.UpdateTestStatusIfActiveAndLeaseHeld(id, status, endTime, errStr, lease, owner)
}

// term is the fixture's current controller term.
func (f *evacuationRollbackFixture) term() executionLeaseTerm {
	owner, generation := f.tr.leadership.executionLeaseTerm()
	return executionLeaseTerm{ownerID: owner, generation: generation}
}

// awaitHoldRetry waits until no background hold retry runs for the run.
func (f *evacuationRollbackFixture) awaitHoldRetry(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		f.tr.evacuationHoldsMu.Lock()
		_, running := f.tr.evacuationHoldRetrying[f.test.ID]
		f.tr.evacuationHoldsMu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the hold retry did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// restartAfterFailedHold models a successor lost after a failed recovery whose
// hold write failed: the durable row is reloaded into a fresh managed run, so
// the lost term's retry ends, and the lease passes to a newer term.
func (f *evacuationRollbackFixture) restartAfterFailedHold(t *testing.T, faults *statusWriteFaultStore, name string) (*storage.TestRecord, uint64) {
	t.Helper()
	record, err := f.store.GetTest(f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := &ManagedTest{ID: f.test.ID, Name: f.test.ID, Status: record.Status, Assignment: f.test.Assignment, Groups: f.test.Groups, StartedAt: f.test.StartedAt, Duration: f.test.Duration}
	setContinuityCheckpointLocked(restored, record.ContinuityCheckpoint)
	f.tr.mu.Lock()
	f.tr.tests[f.test.ID] = restored
	f.tr.mu.Unlock()
	f.test = restored
	f.awaitHoldRetry(t)
	faults.fail.Store(false)
	_, generation := f.handOver(t, name)
	return record, generation
}

// A generator reports a completed run for a stopped staged assignment too,
// so a completed replacement proves its commit only when its duration ran out
// (TestEvacuationRecoveryTrustsANaturalCompletion). A stop, or a generator that
// reports no cause, holds the run.
func TestEvacuationRollForwardHoldsCompletedReplacement(t *testing.T) {
	for cause, want := range map[pb.RunTerminalCause]string{
		pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP: "replacement gen-b reports a completed run the journal never recorded: it was stopped by a controller",
		pb.RunTerminalCause_RUN_TERMINAL_CAUSE_UNSPECIFIED:     "replacement gen-b reports a completed run the journal never recorded: its generator reports no terminal cause",
	} {
		t.Run("stopped by its own leader/"+cause.String(), func(t *testing.T) {
			// The predecessor's uncertain path stopped the still-staged
			// replacement after writing its commit intent.
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			completeWithCause(f.spareGen, cause, 0)
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertFailedRollForwardHeld(t, generation, want, true, false)
			if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != 0 {
				t.Fatalf("a completed replacement was adjusted: %+v", adjustments)
			}
			if _, capacity := f.spareGen.snapshot(); len(capacity) != 0 {
				t.Fatalf("a completed replacement received capacity requests: %+v", capacity)
			}
			if row := f.ledger(t)["gen-b"]; row.State != "pending" || row.Generation != f.oldGen {
				t.Fatalf("completed replacement capacity changed: %+v", row)
			}
		})
	}
	t.Run("stopped by a failed successor", func(t *testing.T) {
		// The first successor fails after adopting, cannot write its hold, and
		// still stops everything it adopted.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.spareGen.rejectAdjust = true
		faults := newStatusWriteFaultStore(f.store)
		f.tr.testStore = faults
		_, first := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		record, err := f.store.GetTest(f.test.ID)
		if err != nil || record.Status != "RUNNING" {
			t.Fatalf("a failed hold write left the run %+v, %v", record, err)
		}
		for name, gen := range map[string]*rollbackGeneratorClient{"survivor": f.survivorGen, "replacement": f.spareGen} {
			if stops := gen.stopGenerations(); len(stops) != 1 || stops[0] != first {
				t.Fatalf("%s stops after a failed hold = %v, want one at %d", name, stops, first)
			}
		}
		// Every stopped assignment now reports a run a controller stopped.
		completeWithCause(f.spareGen, pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP, 0)
		completeWithCause(f.survivorGen, pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP, 0)
		_, second := f.restartAfterFailedHold(t, faults, "roll-forward-after-a-failed-successor")
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		f.assertFailedRollForwardHeld(t, second, "reports a completed run the journal never recorded: it was stopped by a controller", false, false)
		if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != 1 || adjustments[0].ExecutionGeneration != first {
			t.Fatalf("duration adjustments = %+v, want only the first successor's", adjustments)
		}
	})
}

// A survivor that reports a completed run the prior lineage never recorded may
// have been stopped by a lost leader or by a failed earlier successor; the run
// is held rather than continued without that survivor's shard.
func TestEvacuationRecoveryHoldsUnrecordedCompletion(t *testing.T) {
	const want = "participant gen-c reports a completed run the journal never recorded"
	for _, phase := range []string{runEvacuationPhaseReplacementCommitting, runEvacuationPhaseStaged} {
		rollForward := phase == runEvacuationPhaseReplacementCommitting
		name := map[bool]string{true: "roll-forward", false: "rollback"}[rollForward]
		// assertHeld checks the journal, the hold and the adopted participant's
		// forced stop at generation.
		assertHeld := func(t *testing.T, f *evacuationRollbackFixture, generation uint64) {
			t.Helper()
			if rollForward {
				f.assertFailedRollForwardHeld(t, generation, want, false, true)
				return
			}
			guard, _ := f.durableGuard(t)
			record, err := f.store.GetTest(f.test.ID)
			if err != nil || guard.Phase != runEvacuationPhaseRollingBack || guard.RollbackGeneration != generation || record.Status != "STOPPING" || !strings.Contains(record.Error, want) || f.test.adoptedResume != nil {
				t.Fatalf("rollback with a stopped survivor = phase %q, %+v, %v", guard.Phase, record, err)
			}
			atGeneration := func(gen *rollbackGeneratorClient) int {
				count := 0
				for _, stopped := range gen.stopGenerations() {
					if stopped == generation {
						count++
					}
				}
				return count
			}
			if atGeneration(f.sourceGen) != 1 || !f.sourceGen.stoppedWithClaimedToken() {
				t.Fatalf("adopted source stops = %v, want one forced stop at %d with the rotated token", f.sourceGen.stopGenerations(), generation)
			}
			if atGeneration(f.survivorGen) != 0 {
				t.Fatalf("the completed survivor was stopped at %d: %v", generation, f.survivorGen.stopGenerations())
			}
			f.tr.mu.RLock()
			eligible := interruptedEvacuationAwaitingProofLocked(f.test)
			f.tr.mu.RUnlock()
			if !eligible {
				t.Fatal("held rollback is not eligible for administrator attestation")
			}
			if err := f.tr.recoverInterruptedRunEvacuation(f.test); err == nil || !strings.Contains(err.Error(), "recovery generation") {
				t.Fatalf("same-term fail-closed recovery = %v", err)
			}
			if row := f.ledger(t)["gen-b"]; row.State != "pending" || row.Generation != f.oldGen {
				t.Fatalf("replacement capacity changed: %+v", row)
			}
		}
		t.Run(name+"/stopped by the lost leader", func(t *testing.T) {
			// The lost leader's uncertain path stopped the survivor, and was cut
			// off before it stopped the rest.
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: phase})
			completeWithCause(f.survivorGen, pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP, 0)
			_, generation := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			assertHeld(t, f, generation)
		})
		t.Run(name+"/stopped by a failed successor", func(t *testing.T) {
			// The first successor fails, cannot write its hold, stops the
			// survivor, and the other participant's stop is lost.
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: phase})
			stillRunning := f.sourceGen
			if rollForward {
				f.spareGen.rejectAdjust = true
				stillRunning = f.spareGen
			} else {
				f.spareDown = true
			}
			stillRunning.failStop = true
			faults := newStatusWriteFaultStore(f.store)
			f.tr.testStore = faults
			_, first := f.tr.leadership.executionLeaseTerm()
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if stops := f.survivorGen.stopGenerations(); len(stops) != 1 || stops[0] != first {
				t.Fatalf("survivor stops = %v, want one at %d", stops, first)
			}
			completeWithCause(f.survivorGen, pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP, 0)
			stillRunning.failStop, f.spareGen.rejectAdjust, f.spareDown = false, false, false
			record, second := f.restartAfterFailedHold(t, faults, name+"-after-a-failed-successor")
			if record.Status != "RUNNING" {
				t.Fatalf("a failed hold write left the run %s", record.Status)
			}
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			assertHeld(t, f, second)
		})
		t.Run(name+"/completed before the evacuation", func(t *testing.T) {
			// A completion the prior lineage recorded is trusted as before.
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: phase, priorCompletedSurvivor: true})
			f.survivorGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			running := map[bool]string{true: "gen-b", false: "gen-a"}[rollForward]
			resume := f.test.adoptedResume
			if resume == nil || resume.finishStatus != "" || resume.adopted[running] == nil || resume.completed["gen-c"] == nil || resume.adopted["gen-c"] != nil {
				t.Fatalf("prior-completed survivor blocked the %s: %+v", name, resume)
			}
			if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "RUNNING" {
				t.Fatalf("durable run = %+v, %v", record, err)
			}
		})
	}
}

// A hold keeps the latest reason across a queued retry, and a retry ends once
// the run is terminal or its journal is gone.
func TestInterruptedRunEvacuationHoldRetry(t *testing.T) {
	t.Run("latest reason", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.spareGen.rejectAdjust = true
		f.spareGen.failStop = true
		faults := newStatusWriteFaultStore(f.store)
		f.tr.testStore = faults
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		// Both writes failed; the queued retry now succeeds with the second.
		faults.fail.Store(false)
		f.awaitHoldRetry(t)
		record, err := f.store.GetTest(f.test.ID)
		if err != nil || record.Status != "STOPPING" || !strings.Contains(record.Error, "gen-b: force stop of the adopted assignment was not acknowledged") {
			t.Fatalf("held run after the retry = %+v, %v", record, err)
		}
	})
	t.Run("during acquisition reconcile", func(t *testing.T) {
		// Phase A runs before admission. The hold retry must not wait for it:
		// the run would stay RUNNING with its participants force-stopped.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.spareGen.rejectAdjust = true
		faults := newStatusWriteFaultStore(f.store)
		f.tr.testStore = faults
		f.tr.leadership.reconciling.Store(true)
		defer f.tr.leadership.reconciling.Store(false)
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		if f.tr.isExecutionLeader() {
			t.Fatal("the fixture was admitted during reconcile")
		}
		faults.fail.Store(false)
		f.awaitHoldRetry(t)
		record, err := f.store.GetTest(f.test.ID)
		if err != nil || record.Status != "STOPPING" || !strings.HasPrefix(record.Error, uncertainEvacuationReasonPrefix) {
			t.Fatalf("held run during reconcile = %+v, %v", record, err)
		}
		f.tr.evacuationHoldsMu.Lock()
		_, registered := f.tr.evacuationHolds[f.test.ID]
		f.tr.evacuationHoldsMu.Unlock()
		if registered {
			t.Fatal("a written hold stayed registered")
		}
	})
	t.Run("reason changed during a retry write", func(t *testing.T) {
		// While the retry writes the first reason, the hold gains a later one
		// whose own write failed. The retry goes on until it wrote that one.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		faults := newStatusWriteFaultStore(f.store)
		h := f.tr.newInterruptedRunEvacuationHold(f.test, f.term())
		var once sync.Once
		faults.beforeWrite = func() {
			once.Do(func() {
				h.mu.Lock()
				h.reason = uncertainEvacuationReasonPrefix + "later reason"
				h.mu.Unlock()
			})
		}
		f.tr.testStore = faults
		h.hold("first reason")
		faults.fail.Store(false)
		f.awaitHoldRetry(t)
		record, err := f.store.GetTest(f.test.ID)
		if err != nil || record.Status != "STOPPING" || !strings.Contains(record.Error, "later reason") {
			t.Fatalf("held run after the retry = %+v, %v", record, err)
		}
	})
	t.Run("forgotten hold", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		faults := newStatusWriteFaultStore(f.store)
		f.tr.testStore = faults
		f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "injected")
		f.tr.forgetEvacuationHold(f.test.ID)
		// The writes still fail; the retry ends with the hold gone.
		f.awaitHoldRetry(t)
	})
	t.Run("shutdown", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		faults := newStatusWriteFaultStore(f.store)
		f.tr.testStore = faults
		f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "injected")
		f.tr.mu.Lock()
		f.tr.shuttingDown = true
		f.tr.mu.Unlock()
		// The writes still fail; the retry ends and frees the entry.
		f.awaitHoldRetry(t)
		f.tr.evacuationHoldsMu.Lock()
		_, registered := f.tr.evacuationHolds[f.test.ID]
		f.tr.evacuationHoldsMu.Unlock()
		if registered {
			t.Fatal("the hold stayed registered after shutdown")
		}
	})
	t.Run("claim refused in a current term", func(t *testing.T) {
		// A fence of another owner refuses the claim, but the term is still
		// current: the hold is kept and written once the fence is released.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.tr.mu.Lock()
		f.test.executionFence = "another-owner"
		f.tr.mu.Unlock()
		f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "refused claim")
		time.Sleep(200 * time.Millisecond)
		f.tr.evacuationHoldsMu.Lock()
		_, registered := f.tr.evacuationHolds[f.test.ID]
		f.tr.evacuationHoldsMu.Unlock()
		if record, _ := f.store.GetTest(f.test.ID); !registered || record.Status != "RUNNING" {
			t.Fatalf("refused hold: registered=%v durable=%s", registered, record.Status)
		}
		f.tr.mu.Lock()
		f.test.executionFence = ""
		f.tr.mu.Unlock()
		f.awaitHoldRetry(t)
		if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "STOPPING" || !strings.Contains(record.Error, "refused claim") {
			t.Fatalf("held run after the fence was released = %+v, %v", record, err)
		}
	})
	t.Run("latest hold of the run", func(t *testing.T) {
		// A hold whose term has since ended, as after a leadership change in
		// this process, still occupies the run's retry slot; the retry writes
		// the newer term's hold instead.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		faults := newStatusWriteFaultStore(f.store)
		f.tr.testStore = faults
		earlier := f.tr.newInterruptedRunEvacuationHold(f.test, f.term())
		earlier.hold("earlier term")
		lock := f.tr.evacuationHoldWriteLock(f.test.ID)
		lock.Lock()
		earlier.term.generation--
		lock.Unlock()
		f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "current term")
		faults.fail.Store(false)
		f.awaitHoldRetry(t)
		record, err := f.store.GetTest(f.test.ID)
		if err != nil || record.Status != "STOPPING" || !strings.Contains(record.Error, "current term") {
			t.Fatalf("held run after the retry = %+v, %v", record, err)
		}
	})
	t.Run("later hold during an earlier hold's retry write", func(t *testing.T) {
		// While the retry writes an earlier hold, a later hold of the run is
		// made, as by isolateRunAdoption after a panic. The later hold waits
		// for the run's write lock and its reason ends durable and in memory.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		faults := newStatusWriteFaultStore(f.store)
		later := make(chan struct{})
		var fired atomic.Bool
		faults.beforeWrite = func() {
			// Only the earlier hold's write starts the later hold; the later
			// hold's own write passes straight through.
			if !fired.CompareAndSwap(false, true) {
				return
			}
			go func() {
				defer close(later)
				f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "later hold")
			}()
			// Give the later hold the chance to write first; it must not.
			select {
			case <-later:
			case <-time.After(500 * time.Millisecond):
			}
		}
		f.tr.testStore = faults
		f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "earlier hold")
		faults.fail.Store(false)
		f.awaitHoldRetry(t)
		select {
		case <-later:
		case <-time.After(10 * time.Second):
			t.Fatal("the later hold did not finish")
		}
		f.awaitHoldRetry(t)
		record, err := f.store.GetTest(f.test.ID)
		f.tr.mu.RLock()
		memory := f.test.Error
		f.tr.mu.RUnlock()
		if err != nil || record.Status != "STOPPING" || !strings.Contains(record.Error, "later hold") || !strings.Contains(memory, "later hold") {
			t.Fatalf("held run = %+v (memory %q), %v", record, memory, err)
		}
	})
	for _, stale := range []string{"ended term and old object", "ended term", "old object"} {
		t.Run("stale hold after the current hold/"+stale, func(t *testing.T) {
			// The current term's hold failed to write; a goroutine of a lost
			// term then holds the run too, with the ended term, the run's old
			// object or both. It does not displace the current hold, whose
			// reason ends durable.
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			faults := newStatusWriteFaultStore(f.store)
			f.tr.testStore = faults
			f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "current hold")
			object, term := f.test, f.term()
			if stale != "ended term" {
				object = &ManagedTest{ID: f.test.ID, Status: "RUNNING", executionFence: f.oldOwner}
			}
			if stale != "old object" {
				term = executionLeaseTerm{ownerID: f.oldOwner, generation: f.oldGen}
			}
			f.tr.holdInterruptedRunEvacuation(object, term, "lost-term hold")
			faults.fail.Store(false)
			f.awaitHoldRetry(t)
			record, err := f.store.GetTest(f.test.ID)
			f.tr.mu.RLock()
			memory := f.test.Error
			f.tr.mu.RUnlock()
			if err != nil || record.Status != "STOPPING" || !strings.Contains(record.Error, "current hold") || !strings.Contains(memory, "current hold") {
				t.Fatalf("held run = %+v (memory %q), %v", record, memory, err)
			}
		})
	}
	t.Run("superseded hold writes nothing", func(t *testing.T) {
		// A write of an earlier hold that runs after a later hold of the run
		// was written, as an in-flight persist would, writes nothing.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		faults := newStatusWriteFaultStore(f.store)
		f.tr.testStore = faults
		earlier := f.tr.newInterruptedRunEvacuationHold(f.test, f.term())
		earlier.hold("earlier hold")
		faults.fail.Store(false)
		f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "later hold")
		f.awaitHoldRetry(t)
		if err := earlier.persist(); err != nil {
			t.Fatal(err)
		}
		if record, err := f.store.GetTest(f.test.ID); err != nil || !strings.Contains(record.Error, "later hold") {
			t.Fatalf("a superseded hold overwrote the later one: %+v, %v", record, err)
		}
	})
	t.Run("hold of an ended term", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		faults := newStatusWriteFaultStore(f.store)
		f.tr.testStore = faults
		earlier := f.tr.newInterruptedRunEvacuationHold(f.test, f.term())
		earlier.hold("earlier term")
		lock := f.tr.evacuationHoldWriteLock(f.test.ID)
		lock.Lock()
		earlier.term.generation--
		lock.Unlock()
		faults.fail.Store(false)
		// The retry drops it instead of writing under the newer lease.
		f.awaitHoldRetry(t)
		if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "RUNNING" {
			t.Fatalf("an ended term's hold was written: %+v, %v", record, err)
		}
	})
	for _, end := range []string{"terminal", "journal replaced"} {
		t.Run(end, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			faults := newStatusWriteFaultStore(f.store)
			f.tr.testStore = faults
			f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "injected")
			f.tr.mu.Lock()
			if end == "terminal" {
				// An administrator attestation finished the run meanwhile.
				f.test.Status = "FAILED"
			} else {
				f.test.continuityCheckpoint = ""
			}
			f.tr.mu.Unlock()
			// The writes still fail; the retry ends instead of looping.
			f.awaitHoldRetry(t)
			if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "RUNNING" {
				t.Fatalf("durable run = %+v, %v", record, err)
			}
		})
	}
}

// staleStatusListStore lists every run with status and no error, as a
// ListTests that read the row before a hold wrote STOPPING; rewrite, when set,
// changes each listed record further.
type staleStatusListStore struct {
	*storage.SQLiteStore
	status  string
	rewrite func(*storage.TestRecord)
}

func (s staleStatusListStore) ListTests(projectID string, folderID *string) ([]*storage.TestRecord, error) {
	records, err := s.SQLiteStore.ListTests(projectID, folderID)
	for _, record := range records {
		record.Status, record.Error = s.status, ""
		if s.rewrite != nil {
			s.rewrite(record)
		}
	}
	return records, err
}

// storeReloadableRun stores the run's groups, with compiled scripts so a
// replacement restores its runtime, and its assignment, so a reloaded RUNNING
// snapshot is itself a roll-forward candidate as in production, and matches
// the in-memory run's identity.
func storeReloadableRun(t *testing.T, f *evacuationRollbackFixture) {
	t.Helper()
	f.tr.mu.Lock()
	for index := range f.test.Groups {
		group := &f.test.Groups[index]
		group.ScriptID = "script-" + group.Name
		group.script = &dsl.Script{ID: group.ScriptID, Name: group.Name, Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1:9"}, Actions: []dsl.Action{{Name: "home", Type: "GET", Target: "/"}}}
	}
	f.tr.mu.Unlock()
	record, err := f.store.GetTest(f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Groups = encodeStoredGroups(f.test.Groups)
	record.Assignment = f.test.Assignment
	if err := f.store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	f.test.CreatedAt = record.StartTime
}

// reloadedRun reloads the runner from its store and returns the run's
// in-memory object afterwards.
func (f *evacuationRollbackFixture) reloadedRun(t *testing.T) *ManagedTest {
	t.Helper()
	if err := f.tr.reloadStoredTests(); err != nil {
		t.Fatal(err)
	}
	f.tr.mu.RLock()
	defer f.tr.mu.RUnlock()
	return f.tr.tests[f.test.ID]
}

// attest records an administrator attestation for the run and checks that
// the run became FAILED with the attestation proof.
func (f *evacuationRollbackFixture) attest(t *testing.T, test *ManagedTest) {
	t.Helper()
	if err := f.tr.finalizeAttestedInterruptedEvacuation(context.Background(), test, interruptedEvacuationAttestedProof); err != nil {
		t.Fatalf("attestation = %v", err)
	}
	if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "FAILED" || !strings.HasPrefix(record.Error, interruptedEvacuationAttestedProof) {
		t.Fatalf("attested run = %+v, %v", record, err)
	}
}

// A reload that read the row before an evacuation hold wrote STOPPING keeps
// the newer in-memory hold of this term, so the run stays attestable. A
// STOPPING never written in this term, or a row under another journal, wins.
func TestReloadKeepsANewerEvacuationHold(t *testing.T) {
	for _, c := range []struct{ name, status, phase string }{
		{"roll-forward", "RUNNING", runEvacuationPhaseReplacementCommitting},
		{"roll-forward", "PAUSED", runEvacuationPhaseReplacementCommitting},
		{"rollback", "RUNNING", runEvacuationPhaseStaged},
	} {
		status := c.status
		t.Run("hold written in this term/"+c.name+"/listed "+status, func(t *testing.T) {
			// A paused run's listing is a candidate only when staged paused.
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{paused: status == "PAUSED", phase: c.phase})
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
			f.spareDown = c.phase == runEvacuationPhaseStaged
			storeReloadableRun(t, f)
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "STOPPING" || f.test.Status != "STOPPING" {
				t.Fatalf("hold before the reload = %+v, %v", record, err)
			}
			stale := staleStatusListStore{SQLiteStore: f.store, status: status}
			f.tr.testStore = stale
			// The stale snapshot is itself a candidate, so reconcile leaves it
			// as listed: only the reload's rule keeps the hold.
			records, err := stale.ListTests("", nil)
			if err != nil || len(records) != 1 || !f.tr.isRunAdoptionCandidate(managedTestFromRecord(records[0])) {
				t.Fatalf("the stale snapshot is no candidate: %v", err)
			}
			current := f.reloadedRun(t)
			f.tr.mu.RLock()
			held, eligible := current.Status == "STOPPING" && strings.HasPrefix(current.Error, uncertainEvacuationReasonPrefix), interruptedEvacuationAwaitingProofLocked(current)
			f.tr.mu.RUnlock()
			if current != f.test || !held || !eligible {
				t.Fatalf("after the reload: same object=%v held=%v eligible=%v", current == f.test, held, eligible)
			}
			f.tr.testStore = f.store
			f.attest(t, current)
		})
	}
	t.Run("row under another journal", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
		storeReloadableRun(t, f)
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		committing := f.guard
		f.tr.testStore = staleStatusListStore{SQLiteStore: f.store, status: "RUNNING", rewrite: func(record *storage.TestRecord) {
			record.ContinuityCheckpoint = committing
		}}
		if current := f.reloadedRun(t); current.Status != "RUNNING" {
			t.Fatalf("a row under another journal did not win: %s", current.Status)
		}
	})
	t.Run("STOPPING never written in this term", func(t *testing.T) {
		// STOPPING in memory only, as an uncertain evacuation whose save failed
		// leaves it, follows the row. In its own term that path keeps its
		// runtime handles, which the pending-stop-proof rule covers, so a run
		// object like this one is left after a lease change: the next term's
		// Phase A decides it.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		storeReloadableRun(t, f)
		f.tr.mu.Lock()
		if err := f.tr.claimTestExecutionFence(f.test); err != nil {
			f.tr.mu.Unlock()
			t.Fatal(err)
		}
		f.test.Status, f.test.Error = "STOPPING", uncertainEvacuationReasonPrefix+"save failed"
		f.tr.mu.Unlock()
		if current := f.reloadedRun(t); current != f.test || current.Status != "RUNNING" {
			t.Fatalf("an unwritten STOPPING survived the reload: same object=%v status=%s", current == f.test, current.Status)
		}
	})
	t.Run("hold write failed", func(t *testing.T) {
		// The hold stamps the run only after its durable write: STOPPING in
		// memory from an uncertain evacuation, plus a hold whose write failed,
		// still follows the row.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		storeReloadableRun(t, f)
		f.tr.mu.Lock()
		f.test.Status, f.test.Error = "STOPPING", uncertainEvacuationReasonPrefix+"save failed"
		f.tr.mu.Unlock()
		faults := newStatusWriteFaultStore(f.store)
		f.tr.testStore = faults
		f.tr.holdInterruptedRunEvacuation(f.test, f.term(), "write fails")
		f.tr.forgetEvacuationHold(f.test.ID)
		f.awaitHoldRetry(t)
		f.tr.testStore = f.store
		f.tr.mu.RLock()
		owner := f.test.evacuationHoldOwner
		f.tr.mu.RUnlock()
		if owner != "" {
			t.Fatalf("a failed hold write stamped the run for %q", owner)
		}
		if current := f.reloadedRun(t); current != f.test || current.Status != "RUNNING" {
			t.Fatalf("after the reload: same object=%v status=%s", current == f.test, current.Status)
		}
	})
	t.Run("terminal run", func(t *testing.T) {
		// A terminal run needs no fence; a lease change does not re-hydrate it.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		storeReloadableRun(t, f)
		ended := time.Now().UTC()
		if err := f.store.UpdateTestStatus(f.test.ID, "FAILED", &ended, "finished"); err != nil {
			t.Fatal(err)
		}
		f.tr.mu.Lock()
		f.test.Status, f.test.Error, f.test.EndedAt = "FAILED", "finished", &ended
		f.test.executionFence = f.oldOwner
		f.tr.mu.Unlock()
		if current := f.reloadedRun(t); current != f.test {
			t.Fatal("a terminal run object fenced to a lost term was replaced")
		}
	})
	t.Run("lost term", func(t *testing.T) {
		// The same, left by a term this process lost: the object, still fenced
		// to that term, is replaced, and Phase A holds the run because the
		// lost term stopped its participants.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		storeReloadableRun(t, f)
		f.tr.mu.Lock()
		f.test.executionFence = f.oldOwner
		f.test.Status, f.test.Error = "STOPPING", uncertainEvacuationReasonPrefix+"save failed"
		f.tr.mu.Unlock()
		lost := f.test
		current := f.reloadedRun(t)
		if current == lost || current.Status != "RUNNING" || current.executionFence != "" {
			t.Fatalf("the lost term's object was kept: same=%v status=%s fence=%q", current == lost, current.Status, current.executionFence)
		}
		// The replacement went through the runtime restore.
		if len(current.groupSpecs) != len(current.Groups) || current.runtimeHydrationError != "" {
			t.Fatalf("replacement runtime: %d specs for %d groups, hydration error %q", len(current.groupSpecs), len(current.Groups), current.runtimeHydrationError)
		}
		f.test = current
		f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
		f.survivorGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "STOPPING" {
			t.Fatalf("Phase A did not hold the run: %+v, %v", record, err)
		}
		f.attest(t, current)
	})
}

// A run object left fenced to a lost term is replaced by the acquisition
// reconcile, in production order, and the run then rolls forward normally.
func TestLostTermRunRollsForwardAfterReloadAndReconcile(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	storeReloadableRun(t, f)
	f.tr.mu.Lock()
	f.test.executionFence = f.oldOwner
	f.tr.mu.Unlock()
	lost := f.test
	owner, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.reloadAndReconcile(); err != nil {
		t.Fatal(err)
	}
	f.tr.mu.RLock()
	current := f.tr.tests[f.test.ID]
	f.tr.mu.RUnlock()
	if current == lost {
		t.Fatal("the lost term's object was reused")
	}
	if len(current.groupSpecs) != len(current.Groups) || current.runtimeHydrationError != "" {
		t.Fatalf("replacement runtime: %d specs for %d groups, hydration error %q", len(current.groupSpecs), len(current.Groups), current.runtimeHydrationError)
	}
	f.test = current
	f.assertRolledForward(t, owner, generation, rollForwardCommitTarget)
}

// registerLiveTables registers live tables for the run under term.
func (f *evacuationRollbackFixture) registerLiveTables(term executionLeaseTerm) *runLiveTables {
	live := &runLiveTables{ready: true, term: term, testID: f.test.ID}
	f.tr.liveTablesMu.Lock()
	if f.tr.liveTables == nil {
		f.tr.liveTables = make(map[string]*runLiveTables)
	}
	f.tr.liveTables[f.test.ID] = live
	f.tr.liveTablesMu.Unlock()
	return live
}

// awaitLiveTableRetries waits until no live-table retry runs for the run.
func (f *evacuationRollbackFixture) awaitLiveTableRetries(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for f.liveTableRetrying() {
		if time.Now().After(deadline) {
			t.Fatal("a live-table retry did not end")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// liveTableRetrying reports whether any live-table retry runs for the run.
func (f *evacuationRollbackFixture) liveTableRetrying() bool {
	f.tr.terminalRetryMu.Lock()
	defer f.tr.terminalRetryMu.Unlock()
	for key := range f.tr.terminalRetrying {
		if strings.HasPrefix(key, "live-tables:"+f.test.ID) {
			return true
		}
	}
	return false
}

func liveTablesClosed(live *runLiveTables) bool {
	live.mu.RLock()
	defer live.mu.RUnlock()
	return live.closed
}

// Live tables are keyed by run ID; only a terminal writer of the term holding
// them finalizes them.
func TestTerminalWriterFinalizesOnlyItsTermsLiveTables(t *testing.T) {
	t.Run("object replaced in the same term", func(t *testing.T) {
		// A same-term reload replaced the finishing run's object; nobody else
		// will finalize its tables, so its own terminal write does.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.tr.mu.Lock()
		if err := f.tr.claimTestExecutionFence(f.test); err != nil {
			f.tr.mu.Unlock()
			t.Fatal(err)
		}
		f.tr.tests[f.test.ID] = &ManagedTest{ID: f.test.ID, Status: "COMPLETE"}
		f.tr.mu.Unlock()
		live := f.registerLiveTables(f.term())
		if err := f.tr.completeTestRun(f.test, "COMPLETE", time.Now().UTC(), "", nil); err != nil {
			t.Fatal(err)
		}
		if !liveTablesClosed(live) {
			t.Fatal("the finishing run's live tables stayed open")
		}
	})
	t.Run("tables of an ended term", func(t *testing.T) {
		// A lost term's writer can never finalize its own tables again, so it
		// neither tries nor leaves a retry behind; tables a newer term restores
		// for the run stay untouched.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.tr.mu.Lock()
		f.test.executionFence = f.oldOwner
		f.tr.mu.Unlock()
		lost := f.registerLiveTables(executionLeaseTerm{ownerID: f.oldOwner, generation: f.oldGen})
		f.tr.finishLiveTables(f.test, "FAILED")
		if liveTablesClosed(lost) || f.liveTableRetrying() {
			t.Fatalf("an ended term's tables: closed=%v retrying=%v", liveTablesClosed(lost), f.liveTableRetrying())
		}
		restored := f.registerLiveTables(f.term())
		f.tr.finishLiveTables(f.test, "FAILED")
		if liveTablesClosed(restored) {
			t.Fatal("a lost term's writer finalized the tables a newer term restored")
		}
	})
	t.Run("tables restored during finalization", func(t *testing.T) {
		// Between the ownership check and the finalization, the lease passes to
		// a newer term, which restores the run's tables. The checked tables of
		// the ended term are refused at its term check; a lookup by run ID
		// would have finalized the newer term's tables instead.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.tr.mu.Lock()
		if err := f.tr.claimTestExecutionFence(f.test); err != nil {
			f.tr.mu.Unlock()
			t.Fatal(err)
		}
		f.tr.mu.Unlock()
		checked := f.registerLiveTables(f.term())
		var restored *runLiveTables
		var once sync.Once
		f.tr.afterLiveTableFinalizeCheck = func() {
			once.Do(func() {
				f.handOver(t, "live-tables-newer-term")
				restored = f.registerLiveTables(f.term())
			})
		}
		defer func() { f.tr.afterLiveTableFinalizeCheck = nil }()
		f.tr.finishLiveTables(f.test, "COMPLETE")
		if restored == nil || restored.term == checked.term || liveTablesClosed(checked) || liveTablesClosed(restored) {
			t.Fatalf("checked closed=%v, restored present=%v closed=%v", liveTablesClosed(checked), restored != nil, restored != nil && liveTablesClosed(restored))
		}
		// The ended term's retry gives up instead of looping.
		f.awaitLiveTableRetries(t)
	})
	t.Run("retry of an earlier term", func(t *testing.T) {
		// A retry left by an earlier term holds its own slot; the current
		// term's failed finalization still gets a retry.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.tr.mu.Lock()
		if err := f.tr.claimTestExecutionFence(f.test); err != nil {
			f.tr.mu.Unlock()
			t.Fatal(err)
		}
		f.tr.mu.Unlock()
		earlier := liveTableRetryKey(f.test.ID, &runLiveTables{term: executionLeaseTerm{ownerID: f.oldOwner, generation: f.oldGen}})
		current := f.registerLiveTables(f.term())
		if earlier == liveTableRetryKey(f.test.ID, current) {
			t.Fatalf("live-table retries of two terms share the key %q", earlier)
		}
		if !f.tr.beginTerminalRetry(earlier) {
			t.Fatal("could not occupy the earlier term's slot")
		}
		defer f.tr.endTerminalRetry(earlier)
		// A table without a snapshot fails finalization, which is retried.
		current.tables = map[string]*runLiveTable{"t": {}}
		f.tr.finishLiveTables(f.test, "COMPLETE")
		f.tr.terminalRetryMu.Lock()
		_, retrying := f.tr.terminalRetrying[liveTableRetryKey(f.test.ID, current)]
		f.tr.terminalRetryMu.Unlock()
		if !retrying {
			t.Fatal("the current term's live-table retry was dropped")
		}
		// Replacing the tables ends that retry.
		f.tr.liveTablesMu.Lock()
		delete(f.tr.liveTables, f.test.ID)
		f.tr.liveTablesMu.Unlock()
		deadline := time.Now().Add(10 * time.Second)
		for {
			f.tr.terminalRetryMu.Lock()
			_, retrying = f.tr.terminalRetrying[liveTableRetryKey(f.test.ID, current)]
			f.tr.terminalRetryMu.Unlock()
			if !retrying {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the retry of replaced tables did not end")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// Tables a restore found already closing are finalized like any other: a
// failure is retried under the run's term key, and the retry ends once the
// tables were replaced or their term ended, never touching other tables.
func TestRecoveredLiveTablesFinalization(t *testing.T) {
	failing := func(f *evacuationRollbackFixture, term executionLeaseTerm) *runLiveTables {
		live := f.registerLiveTables(term)
		// A table without a snapshot fails finalization.
		live.tables = map[string]*runLiveTable{"t": {}}
		return live
	}
	t.Run("retried until replaced", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		recovered := failing(f, f.term())
		if err := f.tr.finalizeRecoveredLiveTables(context.Background(), f.test, recovered); err == nil {
			t.Fatal("the failing finalization succeeded")
		}
		f.tr.terminalRetryMu.Lock()
		_, retrying := f.tr.terminalRetrying[liveTableRetryKey(f.test.ID, recovered)]
		f.tr.terminalRetryMu.Unlock()
		if !retrying {
			t.Fatal("the recovery finalization was not retried under its term key")
		}
		replacement := f.registerLiveTables(f.term())
		f.awaitLiveTableRetries(t)
		if liveTablesClosed(replacement) {
			t.Fatal("the recovery retry finalized the tables that replaced its own")
		}
	})
	t.Run("tables of an ended term", func(t *testing.T) {
		// Tables restored under a term that has since ended: the retry ends
		// instead of failing on the expired owner every second.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		recovered := failing(f, executionLeaseTerm{ownerID: f.oldOwner, generation: f.oldGen})
		if err := f.tr.finalizeRecoveredLiveTables(context.Background(), f.test, recovered); err == nil || !strings.Contains(err.Error(), "owner expired") || liveTablesClosed(recovered) {
			t.Fatalf("an ended term's tables: %v, closed=%v", err, liveTablesClosed(recovered))
		}
		f.awaitLiveTableRetries(t)
	})
}

// A hold written in one term stays attestable, and recoverable by a newer
// term's fences, after the same process loses and wins the lease again. The
// reload replaces the object still fenced to the lost term, and a goroutine
// of that term holding the old object cannot write the run.
func TestHeldRunAfterTheLeaseIsWonAgain(t *testing.T) {
	for _, resolve := range []string{"attestation", "fence recovery"} {
		t.Run(resolve, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
			storeReloadableRun(t, f)
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "STOPPING" {
				t.Fatalf("hold in the first term = %+v, %v", record, err)
			}
			lost := f.test
			f.handOver(t, "roll-forward-lease-won-again")
			current := f.reloadedRun(t)
			if current == lost || current.Status != "STOPPING" {
				t.Fatalf("after the reload: same=%v status=%s", current == lost, current.Status)
			}
			f.test = current
			// A lost-term writer still holding the old object writes nothing,
			// neither the run's row nor its live tables, keyed by run ID.
			live := &runLiveTables{ready: true, term: f.term(), testID: f.test.ID}
			f.tr.liveTablesMu.Lock()
			if f.tr.liveTables == nil {
				f.tr.liveTables = make(map[string]*runLiveTables)
			}
			f.tr.liveTables[f.test.ID] = live
			f.tr.liveTablesMu.Unlock()
			if err := f.tr.completeTestRun(lost, "COMPLETE", time.Now().UTC(), "", nil); err != nil {
				t.Fatal(err)
			}
			if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "STOPPING" {
				t.Fatalf("a lost-term writer changed the run: %+v, %v", record, err)
			}
			live.mu.RLock()
			closed := live.closed
			live.mu.RUnlock()
			if retrying := f.liveTableRetrying(); closed || retrying {
				t.Fatalf("a lost-term writer finalized the run's live tables: closed=%v retrying=%v", closed, retrying)
			}
			if resolve == "attestation" {
				// The registered run's own terminal write does finalize them.
				f.attest(t, current)
				live.mu.RLock()
				closed = live.closed
				live.mu.RUnlock()
				if !closed {
					t.Fatal("the attested run did not finalize its live tables")
				}
				return
			}
			f.tr.liveTablesMu.Lock()
			delete(f.tr.liveTables, f.test.ID)
			f.tr.liveTablesMu.Unlock()
			markEvacuationRecoveryRunStarted(t, f.store, current)
			if err := f.tr.recoverInterruptedRunEvacuation(current); err != nil {
				t.Fatal(err)
			}
			if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "FAILED" || !strings.HasPrefix(record.Error, interruptedEvacuationFenceProof) {
				t.Fatalf("fence recovery = %+v, %v", record, err)
			}
		})
	}
}

// A failed rollback or roll-forward holds the run before it sends any stop, so
// a RUNNING row never outlives a force stop.
func TestFailedEvacuationRecoveryHoldsBeforeStopping(t *testing.T) {
	observe := func(f *evacuationRollbackFixture, gen *rollbackGeneratorClient) *[]string {
		var observed []string
		gen.beforeStop = func() {
			record, err := f.store.GetTest(f.test.ID)
			if err != nil {
				observed = append(observed, err.Error())
				return
			}
			observed = append(observed, record.Status)
		}
		return &observed
	}
	for _, staleFence := range []bool{false, true} {
		// A process that lost and won the lease again keeps the run's object,
		// still fenced to the journal's old owner, until adoption installs it.
		suffix := map[bool]string{false: "", true: "/stale fence"}[staleFence]
		fence := func(f *evacuationRollbackFixture) {
			if staleFence {
				f.tr.mu.Lock()
				f.test.executionFence = f.oldOwner
				f.tr.mu.Unlock()
			}
		}
		t.Run("roll-forward"+suffix, func(t *testing.T) {
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
			f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
			fence(f)
			observed := observe(f, f.survivorGen)
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if len(*observed) != 1 || (*observed)[0] != "STOPPING" {
				t.Fatalf("durable status at the force stop = %v, want STOPPING", *observed)
			}
		})
		t.Run("rollback"+suffix, func(t *testing.T) {
			f := newEvacuationRollbackFixture(t, runEvacuationPhaseStaged, false)
			f.spareDown = true
			fence(f)
			observed := observe(f, f.sourceGen)
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if len(*observed) != 1 || (*observed)[0] != "STOPPING" {
				t.Fatalf("durable status at the force stop = %v, want STOPPING", *observed)
			}
		})
	}
}

// completeRunEvacuationRollForward itself refuses a replacement this term did
// not adopt, even though Phase A already reports one as a problem.
func TestCompleteRunEvacuationRollForwardRequiresTheReplacement(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	owner, generation := f.tr.leadership.executionLeaseTerm()
	guard, replacements, err := runEvacuationRollForwardPlan(f.guard, f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidate := &runAdoptionCandidate{test: f.test, checkpoint: runEvacuationRollForwardCheckpoint(guard), rollForward: &runEvacuationRollForward{guard: guard, raw: f.guard, replacements: replacements}}
	survivor := map[string]generatorClient{"gen-c": f.survivorGen}
	var problems []string
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("completion without the replacement panicked: %v", recovered)
			}
		}()
		problems = f.tr.completeRunEvacuationRollForward(context.Background(), f.tr.execution.(*distributedExecutionBackend), candidate, survivor, nil, nil, survivor, generation, owner)
	}()
	if len(problems) != 1 || !strings.Contains(problems[0], "replacement gen-b was not adopted") {
		t.Fatalf("problems = %v", problems)
	}
	if _, raw := f.durableGuard(t); raw != f.guard {
		t.Fatal("the journal changed without the replacement")
	}
	if row := f.ledger(t)["gen-c"]; row.Generation != f.oldGen {
		t.Fatalf("capacity moved without the replacement: %+v", row)
	}
}

// noLedgerTestStore has no pod-capacity ledger.
type noLedgerTestStore struct {
	testPersistence
}

func TestCommitRunEvacuationRollForwardCapacityRefusesUnprovableRows(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	owner, generation := f.tr.leadership.executionLeaseTerm()
	guard, replacements, err := runEvacuationRollForwardPlan(f.guard, f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adopted := map[string]generatorClient{"gen-b": f.spareGen}
	unrecorded := guard
	unrecorded.CapacityClaims = nil
	if err := f.tr.commitRunEvacuationRollForwardCapacity(ctx, unrecorded, replacements, adopted, nil, nil, generation, owner); err == nil || !strings.Contains(err.Error(), "did not record") {
		t.Fatalf("unjournaled reservation = %v", err)
	}
	if err := f.tr.commitRunEvacuationRollForwardCapacity(ctx, guard, replacements, map[string]generatorClient{"gen-b": &adoptionFakeClient{}}, nil, nil, generation, owner); err == nil || !strings.Contains(err.Error(), "cannot confirm capacity changes") {
		t.Fatalf("client without capacity RPC = %v", err)
	}
	// A naturally completed replacement's row is released only at the
	// journal's own term.
	f.tr.testStore = &rollbackLedgerStore{SQLiteStore: f.store, rewrite: func(r *storage.PodCapacityReservation) {
		if r.Target == "gen-b" {
			r.Generation += 100
		}
	}}
	natural := map[string]bool{"gen-b": true}
	if err := f.tr.commitRunEvacuationRollForwardCapacity(ctx, guard, replacements, nil, adopted, natural, generation, owner); err == nil || !strings.Contains(err.Error(), "replacement reservation is pending under generation") {
		t.Fatalf("natural replacement row from another term = %v", err)
	}
	f.tr.testStore = noLedgerTestStore{f.store}
	if err := f.tr.commitRunEvacuationRollForwardCapacity(ctx, guard, replacements, adopted, nil, nil, generation, owner); err == nil || !strings.Contains(err.Error(), "capacity ledger unavailable") {
		t.Fatalf("no capacity ledger = %v", err)
	}
	f.tr.testStore = f.store
	if err := f.store.TransitionPodCapacity("gen-b", f.test.ID, "pod-b", f.claim.ReservationID, "released", executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if err := f.tr.commitRunEvacuationRollForwardCapacity(ctx, guard, replacements, adopted, nil, nil, generation, owner); err == nil || !strings.Contains(err.Error(), "lost its replacement reservation") {
		t.Fatalf("lost reservation = %v", err)
	}
	if _, capacity := f.spareGen.snapshot(); len(capacity) != 0 {
		t.Fatalf("unprovable rows reached the generator: %+v", capacity)
	}
}

// A next checkpoint that cannot be planned before any claim holds the run
// STOPPING under its unchanged journal.
func TestEvacuationRollForwardUnusableNextCheckpointHoldsRun(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{}, func(f *evacuationRollbackFixture) {
		var guard runEvacuationCheckpoint
		if err := json.Unmarshal([]byte(f.guard), &guard); err != nil {
			t.Fatal(err)
		}
		guard.Prior.PendingAdoptions = []runAdoptionIntent{{Generation: guard.Generation, OwnerID: "earlier", PreparedAt: time.Now().UTC()}}
		raw := encodeEvacuationGuard(t, guard)
		if err := f.tr.swapRunContinuityCheckpoint(f.test, f.guard, raw, f.oldOwner); err != nil {
			t.Fatal(err)
		}
		f.guard = raw
	})
	if !f.tr.isRunAdoptionCandidate(f.test) {
		t.Fatal("journal is no roll-forward candidate")
	}
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.Contains(record.Error, "could not use the evacuation journal's next checkpoint") || record.ContinuityCheckpoint != f.guard {
		t.Fatalf("unusable next checkpoint = %+v, %v", record, err)
	}
	if f.test.adoptedResume != nil || f.test.executionCtx != nil {
		t.Fatal("an unusable roll-forward bound an adoption outcome")
	}
	for name, gen := range map[string]*rollbackGeneratorClient{"replacement": f.spareGen, "survivor": f.survivorGen} {
		if claims, _ := gen.snapshot(); len(claims) != 0 {
			t.Fatalf("%s was claimed without a usable checkpoint: %+v", name, claims)
		}
	}
}

// A rolling_forward journal reaching a term with the setting off is held and
// fenced like any other interrupted evacuation.
func TestRollingForwardJournalWithTheSettingOffIsHeld(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	firstOwner, firstGeneration := f.tr.leadership.executionLeaseTerm()
	guard, _, err := runEvacuationRollForwardPlan(f.guard, f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	planned, _, err := f.tr.planRunAdoptionIntent(context.Background(), f.test.ID, runEvacuationRollForwardCheckpoint(guard), firstGeneration, firstOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.tr.persistRunEvacuationRollForwardIntent(f.test, f.guard, guard, planned, firstGeneration, firstOwner); err != nil {
		t.Fatal(err)
	}
	_, raw := f.durableGuard(t)
	f.handOver(t, "roll-forward-disabled-successor")
	f.tr.evacuationRollForwardRecovery = false
	record, err := f.store.GetTest(f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := &ManagedTest{ID: f.test.ID, Name: f.test.ID, Status: record.Status, Assignment: f.test.Assignment, Groups: f.test.Groups, StartedAt: f.test.StartedAt, Duration: f.test.Duration}
	setContinuityCheckpointLocked(restored, raw)
	f.tr.mu.Lock()
	f.tr.tests[f.test.ID] = restored
	f.tr.mu.Unlock()
	f.test = restored
	if f.tr.isRunAdoptionCandidate(restored) {
		t.Fatal("a rolling_forward journal was rolled forward with the setting off")
	}
	if err := f.tr.reconcileStoredActiveTest(restored); err != nil {
		t.Fatal(err)
	}
	if record, err = f.store.GetTest(f.test.ID); err != nil || record.Status != "STOPPING" || record.ContinuityCheckpoint != raw {
		t.Fatalf("rolling_forward journal with the setting off = %+v, %v", record, err)
	}
	markEvacuationRecoveryRunStarted(t, f.store, restored)
	if err := f.tr.recoverInterruptedRunEvacuation(restored); err != nil {
		t.Fatal(err)
	}
	if record, err = f.store.GetTest(f.test.ID); err != nil || record.Status != "FAILED" || !strings.HasPrefix(record.Error, interruptedEvacuationFenceProof) {
		t.Fatalf("newer term did not fence the held journal: %+v %v", record, err)
	}
	for name, gen := range map[string]*rollbackGeneratorClient{"replacement": f.spareGen, "survivor": f.survivorGen} {
		if claims, _ := gen.snapshot(); len(claims) != 1 || len(claims[0]) != 0 {
			t.Fatalf("%s fences = %+v", name, claims)
		}
	}
	if claims, _ := f.sourceGen.snapshot(); len(claims) != 0 {
		t.Fatalf("the durably fenced source was claimed: %+v", claims)
	}
}

// A version 5 replacement_committed journal is promoted end to end, as a
// version 3 or 4 one is; the successor sends no further duration change.
func TestVersionFiveCommittedEvacuationIsPromoted(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{committedCapacity: true}, func(f *evacuationRollbackFixture) {
		raw, err := f.tr.markRunEvacuationReplacementCommitted(context.Background(), f.test, f.guard, f.oldGen, f.oldOwner)
		if err != nil {
			t.Fatal(err)
		}
		f.guard = raw
	})
	if _, committed := committedRunEvacuationCheckpoint(f.guard, f.test.ID); !committed {
		t.Fatal("version 5 committed journal lacks promotion proof")
	}
	if _, _, rollForward := f.tr.evacuationRollForwardGuard(f.test, f.guard); rollForward {
		t.Fatal("a committed journal was taken for roll-forward")
	}
	owner, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	resume := f.test.adoptedResume
	if resume == nil || resume.finishStatus != "" || resume.adopted["gen-b"] == nil || resume.adopted["gen-c"] == nil {
		t.Fatalf("version 5 committed replacement not adopted: %+v", resume)
	}
	if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != 0 {
		t.Fatalf("promotion changed the replacement duration: %+v", adjustments)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint); err != nil || !present || checkpoint.Shards["ga"][0].Target != "gen-b" ||
		checkpoint.ExecutionGeneration != generation || checkpoint.ExecutionLeaseOwnerID != owner || !slices.Contains(checkpoint.ArchiveTargets, "gen-a") {
		t.Fatalf("promoted checkpoint = %+v, %v", checkpoint, err)
	}
	if row := f.ledger(t)["gen-b"]; row.State != "committed" || row.Generation != generation {
		t.Fatalf("replacement capacity after promotion = %+v", row)
	}
}

func TestEvacuationRollForwardIntentWriteFailureRetries(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	f.tr.testStore = &rollForwardFaultStore{SQLiteStore: f.store, failIntent: 1}
	owner, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err == nil || !strings.Contains(err.Error(), "persist evacuation roll-forward intent") {
		t.Fatalf("Phase A with a failed intent write = %v", err)
	}
	if _, raw := f.durableGuard(t); raw != f.guard {
		t.Fatal("a failed intent write changed the journal")
	}
	for _, gen := range []*rollbackGeneratorClient{f.spareGen, f.survivorGen} {
		if claims, _ := gen.snapshot(); len(claims) != 0 {
			t.Fatalf("a generator was claimed before the intent was durable: %+v", claims)
		}
	}
	if record, _ := f.store.GetTest(f.test.ID); record.Status != "RUNNING" {
		t.Fatalf("a failed intent write left the run %s", record.Status)
	}
	// Reconcile retries Phase A in the same term.
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.assertRolledForward(t, owner, generation, rollForwardCommitTarget)
}

func TestEvacuationRollForwardFailurePanicHoldsRun(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
	f.survivorGen.panicStop = true
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.Contains(record.Error, "internal error while rolling the evacuation forward") {
		t.Fatalf("panicking roll-forward failure = %+v, %v", record, err)
	}
	guard, _ := f.durableGuard(t)
	if guard.Phase != runEvacuationPhaseRollingForward || f.test.adoptedResume != nil || f.test.executionCtx != nil {
		t.Fatalf("panicking roll-forward failure lost its hold: phase %q resume %+v", guard.Phase, f.test.adoptedResume)
	}
}

func TestEvacuationRollForwardPanicAfterSwapIsOrdinaryFailure(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	f.tr.testStore = &rollForwardFaultStore{SQLiteStore: f.store, panicRearm: true}
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if f.test.adoptedResume == nil || f.test.adoptedResume.finishStatus != "FAILED" || !strings.Contains(f.test.adoptedResume.finishMessage, "internal error while adopting the run") {
		t.Fatalf("a panic after the swap did not take the ordinary path: %+v", f.test.adoptedResume)
	}
	if stops := f.survivorGen.stopGenerations(); len(stops) != 0 {
		t.Fatalf("the roll-forward failure path force-stopped the survivor: %v", stops)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "RUNNING" || strings.HasPrefix(record.Error, uncertainEvacuationReasonPrefix) {
		t.Fatalf("a panic after the swap held the run as an uncertain evacuation: %+v, %v", record, err)
	}
	if _, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint); err != nil || !present {
		t.Fatalf("journal was not replaced before the panic: %v", err)
	}
}

func TestEvacuationRollForwardResumesAfterAnotherLeaderChange(t *testing.T) {
	for _, interrupted := range []string{"after intent", "after claim"} {
		t.Run(interrupted, func(t *testing.T) {
			ctx := context.Background()
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{standby: true})
			firstOwner, firstGeneration := f.tr.leadership.executionLeaseTerm()
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
				// The first successor's claim reached the generators, it shortened
				// the replacement and transferred the capacity, then it was lost
				// before replacing the journal.
				for _, gen := range []*rollbackGeneratorClient{f.spareGen, f.survivorGen} {
					if _, err := gen.ClaimExecutionEpochWithAdoptions(ctx, firstGeneration, firstOwner, time.Second, []generatorrpc.RunAdoption{adoption}); err != nil {
						t.Fatal(err)
					}
				}
				_, _ = f.spareGen.AdjustDuration(ctx, generatorrpc.DurationAdjustment{TestID: f.test.ID, ExecutionGeneration: firstGeneration, TargetDuration: rollForwardCommitTarget, RunSupervisionToken: adoption.NewToken})
				if err := f.store.TransitionPodCapacity("gen-b", f.test.ID, "pod-b", f.claim.ReservationID, "committed", executionLeaseName, firstOwner, firstGeneration); err != nil {
					t.Fatal(err)
				}
				ledger := f.ledger(t)
				if err := f.store.AdoptPodCapacity([]storage.PodCapacityReservation{ledger["gen-b"], ledger["gen-c"]}, executionLeaseName, firstOwner, firstGeneration); err != nil {
					t.Fatal(err)
				}
			}
			_, raw := f.durableGuard(t)
			secondOwner, secondGeneration := f.handOver(t, "roll-forward-second-successor")
			record, err := f.store.GetTest(f.test.ID)
			if err != nil || record.Status != "RUNNING" {
				t.Fatalf("a lost roll-forward left the run %+v, %v", record, err)
			}
			restored := &ManagedTest{ID: f.test.ID, Name: f.test.ID, Status: record.Status, Assignment: f.test.Assignment, Groups: f.test.Groups, StartedAt: f.test.StartedAt, Duration: f.test.Duration}
			setContinuityCheckpointLocked(restored, raw)
			f.tr.mu.Lock()
			f.tr.tests[f.test.ID] = restored
			f.tr.mu.Unlock()
			f.test = restored
			if !f.tr.isRunAdoptionCandidate(restored) {
				t.Fatal("rolling_forward journal of a running run is no roll-forward candidate")
			}
			if err := f.tr.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			f.assertRolledForward(t, secondOwner, secondGeneration, rollForwardCommitTarget)
			claims, _ := f.spareGen.snapshot()
			last := claims[len(claims)-1][0]
			if len(last.PriorProofs) != 1 || last.PriorProofs[0].Generation != firstGeneration {
				t.Fatalf("second adoption lacked the first successor's token proof: %+v", last)
			}
			if got := f.ledger(t)["gen-b"]; got.Generation != secondGeneration || got.State != "committed" || len(got.GroupPeaks) == 0 {
				t.Fatalf("replacement capacity after the second successor = %+v", got)
			}
		})
	}
}

func TestFailedRollForwardIsNotFencedInItsTerm(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED
	_, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.assertFailedRollForwardHeld(t, generation, "gen-b: REJECTED", true, false)
	f.tr.resumeInterruptedRunEvacuations()
	f.tr.terminalRetryMu.Lock()
	_, queued := f.tr.terminalRetrying["evacuation-guard:"+f.test.ID]
	f.tr.terminalRetryMu.Unlock()
	if queued {
		t.Fatal("same-term recovery was queued for a failed roll-forward")
	}
	// A newer term fences the survivor and the replacement, never the fenced
	// source, and records the failure.
	f.handOver(t, "roll-forward-fencing-successor")
	record, err := f.store.GetTest(f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := &ManagedTest{ID: f.test.ID, Name: f.test.ID, Status: record.Status, Error: record.Error, Assignment: f.test.Assignment}
	setContinuityCheckpointLocked(restored, record.ContinuityCheckpoint)
	f.tr.mu.Lock()
	f.tr.tests[f.test.ID] = restored
	f.tr.mu.Unlock()
	f.test = restored
	markEvacuationRecoveryRunStarted(t, f.store, restored)
	if err := f.tr.recoverInterruptedRunEvacuation(f.test); err != nil {
		t.Fatal(err)
	}
	record, err = f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "FAILED" || !strings.HasPrefix(record.Error, interruptedEvacuationFenceProof) {
		t.Fatalf("newer term did not fence the failed roll-forward: %+v %v", record, err)
	}
	for name, gen := range map[string]*rollbackGeneratorClient{"replacement": f.spareGen, "survivor": f.survivorGen} {
		claims, _ := gen.snapshot()
		if len(claims) != 2 || len(claims[1]) != 0 {
			t.Fatalf("%s fences = %+v", name, claims)
		}
	}
	if claims, _ := f.sourceGen.snapshot(); len(claims) != 0 {
		t.Fatalf("the durably fenced source was claimed: %+v", claims)
	}
}

func TestEvacuationRollForwardRefusesLateOldOwner(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	owner, generation := f.tr.leadership.executionLeaseTerm()
	// The predecessor returns between the successor's rolling_forward intent
	// and its journal swap, while the survivor is being claimed.
	var window []string
	var once sync.Once
	f.survivorGen.onClaim = func() {
		once.Do(func() {
			// Off the test goroutine: report problems, never t.Fatal.
			record, err := f.store.GetTest(f.test.ID)
			if err != nil {
				window = append(window, err.Error())
				return
			}
			current := record.ContinuityCheckpoint
			var guard runEvacuationCheckpoint
			if json.Unmarshal([]byte(current), &guard) != nil || guard.Phase != runEvacuationPhaseRollingForward {
				window = append(window, "the claim did not run inside the roll-forward window")
				return
			}
			if _, err := f.tr.markRunEvacuationReplacementCommitted(context.Background(), f.test, f.guard, f.oldGen, f.oldOwner); err == nil {
				window = append(window, "committed mark applied")
			}
			if result, err := f.store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(f.test.ID, current, f.guard, executionLeaseName, f.oldOwner); err == nil && result == storage.TestStatusTransitionUpdated {
				window = append(window, "journal write applied")
			}
			if err := f.store.TransitionPodCapacity("gen-b", f.test.ID, "pod-b", f.claim.ReservationID, "committed", executionLeaseName, f.oldOwner, f.oldGen); err == nil {
				window = append(window, "capacity commit applied")
			}
		})
	}
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if len(window) != 0 {
		t.Fatalf("old owner inside the roll-forward window: %v", window)
	}
	f.assertRolledForward(t, owner, generation, rollForwardCommitTarget)
	_, current := f.durableGuard(t)
	// The predecessor returns and tries to finish its own evacuation: its
	// committed mark, a raw journal write and its capacity commit all fail
	// against the successor's lease.
	if _, err := f.tr.markRunEvacuationReplacementCommitted(context.Background(), f.test, f.guard, f.oldGen, f.oldOwner); err == nil {
		t.Fatal("the old owner recorded its replacement commit")
	}
	if result, err := f.store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(f.test.ID, current, f.guard, executionLeaseName, f.oldOwner); err == nil && result == storage.TestStatusTransitionUpdated {
		t.Fatal("the old owner replaced the successor's checkpoint")
	}
	if err := f.store.TransitionPodCapacity("gen-b", f.test.ID, "pod-b", f.claim.ReservationID, "committed", executionLeaseName, f.oldOwner, f.oldGen); err == nil {
		t.Fatal("the old owner changed the replacement reservation")
	}
	if _, after := f.durableGuard(t); after != current {
		t.Fatal("a late old-owner write changed the checkpoint")
	}
	if row := f.ledger(t)["gen-b"]; row.Generation != generation {
		t.Fatalf("replacement capacity after the late write = %+v", row)
	}
}

// TestEvacuationRollForwardAgainstRealGenerator drives the successor against a
// real generator for the replacement: a staged replacement is refused and
// drained by the epoch claim, and a committed one is adopted and accepts the
// journaled duration with the rotated token, while the old token is refused.
func TestEvacuationRollForwardAgainstRealGenerator(t *testing.T) {
	t.Run("staged replacement is drained", func(t *testing.T) { runRealGeneratorRollForward(t, "staged") })
	// The predecessor's uncertain path stopped the staged replacement: the
	// generator then reports a completed run, which proves no commit.
	t.Run("stopped staged replacement is held", func(t *testing.T) { runRealGeneratorRollForward(t, "stopped staged") })
	t.Run("committed replacement is adopted", func(t *testing.T) { runRealGeneratorRollForward(t, "committed") })
	// The orphaned loss-tolerant replacement's per-run lease was cleared when
	// its epoch lapsed; the duration adjustment does not need it renewed.
	t.Run("loss-tolerant replacement is adopted", func(t *testing.T) { runRealGeneratorRollForward(t, "loss tolerant") })
	// The committed replacement's staged duration ran out before the
	// successor: its generator reports why, so the run rolls forward with the
	// replacement completed.
	t.Run("naturally completed replacement rolls forward", func(t *testing.T) { runRealGeneratorRollForward(t, "naturally completed") })
	// The predecessor stopped the committed replacement: a controller stop.
	t.Run("stopped committed replacement is held", func(t *testing.T) { runRealGeneratorRollForward(t, "stopped committed") })
}

// TestEvacuationRollForwardExpiredDeadlineCompletes rolls forward a
// replacement whose journaled duration has already elapsed: the adjustment
// ends its engines at once instead of failing the run.
func TestEvacuationRollForwardExpiredDeadlineCompletes(t *testing.T) {
	runRealGeneratorRollForward(t, "expired")
}

func runRealGeneratorRollForward(t *testing.T, mode string) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(target.Close)
	client, service := newTestGeneratorRPCClient(t, "gen-b", 10, generatorrpc.WithExecution(), generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	commitTarget := rollForwardCommitTarget
	stagedRemaining := rollForwardStagedRemaining
	if mode == "expired" {
		commitTarget = rollForwardStagedElapsed + time.Millisecond
	}
	if mode == "naturally completed" {
		// The predecessor staged exactly the journaled target, so it sent no
		// adjustment after CommitWork.
		stagedRemaining = 300 * time.Millisecond
		commitTarget = rollForwardStagedElapsed + stagedRemaining
	}
	ctx := context.Background()
	lossTolerant := mode == "loss tolerant"
	epochTTL := time.Minute
	if lossTolerant {
		// Long enough that setup cannot race the lapse; the lapse is then
		// awaited by polling, not by a fixed sleep.
		epochTTL = 2 * time.Second
	}
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{noCapacity: true, commitTarget: commitTarget}, func(f *evacuationRollbackFixture) {
		// The lost leader staged the replacement and, unless it was lost
		// before CommitWork, committed it.
		f.test.Assignment.TolerateGeneratorLoss = lossTolerant
		if _, err := client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, epochTTL); err != nil {
			t.Fatalf("lost leader epoch claim: %v", err)
		}
		content := []byte(fmt.Sprintf(`{"name":"roll-forward","protocol":"http","config":{"base_url":%q,"think_time":{"type":"fixed","mean_ms":50}},"actions":[{"name":"request","type":"GET","target":"/","think_time_after":true}]}`, target.URL))
		digest := sha256.Sum256(content)
		hash := hex.EncodeToString(digest[:])
		if _, err := client.DistributeScript(ctx, generatorrpc.Script{ID: "script", ExecutionGeneration: f.oldGen, Content: content, ContentHash: hash}); err != nil {
			t.Fatalf("distribute script: %v", err)
		}
		assign, err := client.AssignWork(ctx, generatorrpc.Assignment{
			TestID: f.test.ID, GroupName: "ga", ScriptID: "script", ScriptContent: content, ExpectedScriptHash: hash,
			VUserCount: 1, ExecutionGeneration: f.oldGen, TargetDuration: stagedRemaining, DurationOffset: rollForwardStagedElapsed,
			RunSupervisionToken: f.token, AdoptionGrace: 30 * time.Second, RequireTestExecutionLease: lossTolerant,
		})
		if err != nil || !assign.Accepted {
			t.Fatalf("assign work = (%+v, %v)", assign, err)
		}
		if lossTolerant {
			if _, err := client.ClaimTestExecutionEpoch(ctx, f.test.ID, f.oldGen, f.oldOwner, epochTTL); err != nil {
				t.Fatalf("lost leader run lease: %v", err)
			}
		}
		if mode == "stopped staged" {
			stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			stop, err := stopGeneratorTestWithToken(stopCtx, client, f.test.ID, false, f.oldGen, f.token)
			cancel()
			if err != nil || !stop.Acknowledged {
				t.Fatalf("predecessor stop of the staged replacement = (%+v, %v)", stop, err)
			}
		}
		if mode != "staged" && mode != "stopped staged" {
			if claim, err := client.ClaimRunSupervision(ctx, generatorrpc.RunSupervisionClaim{TestID: f.test.ID, ExecutionGeneration: f.oldGen, Token: f.token, RequestedTTL: time.Minute, ExecutionLeaseOwnerID: f.oldOwner}); err != nil || !claim.Accepted {
				t.Fatalf("supervision claim = (%+v, %v)", claim, err)
			}
			if commit, err := client.CommitWorkSupervisedAt(ctx, f.test.ID, f.oldGen, time.Now().Add(500*time.Millisecond), f.token); err != nil || !commit.Accepted {
				t.Fatalf("commit = (%+v, %v)", commit, err)
			}
			waitForActiveVUsers(t, service, 1)
		}
		switch mode {
		case "naturally completed":
			awaitRetainedTerminal(t, client, f.test.ID, f.oldGen)
		case "stopped committed":
			stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			stop, err := stopGeneratorTestWithToken(stopCtx, client, f.test.ID, false, f.oldGen, f.token)
			cancel()
			if err != nil || !stop.Acknowledged {
				t.Fatalf("predecessor stop of the committed replacement = (%+v, %v)", stop, err)
			}
		}
		if lossTolerant {
			// Wait until the lost term's epoch has lapsed and the generator
			// refuses its mutations; the replacement is then orphaned. The
			// probe re-sends the staged duration, which changes nothing.
			deadline := time.Now().Add(10 * time.Second)
			for {
				_, err := client.AdjustDuration(ctx, generatorrpc.DurationAdjustment{TestID: f.test.ID, ExecutionGeneration: f.oldGen, TargetDuration: rollForwardStagedElapsed + rollForwardStagedRemaining, RunSupervisionToken: f.token})
				if err != nil && strings.Contains(err.Error(), generatorrpc.ExecutionGenerationSessionExpiredMessage) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the lost term's epoch did not lapse: %v", err)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		f.replacement = client
	})
	_, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if mode == "stopped staged" || mode == "stopped committed" {
		f.assertFailedRollForwardHeld(t, generation, "replacement gen-b reports a completed run the journal never recorded: it was stopped by a controller", true, false)
		return
	}
	if mode == "naturally completed" {
		resume := f.test.adoptedResume
		if resume == nil || resume.finishStatus != "" || resume.completed["gen-b"] == nil || resume.adopted["gen-b"] != nil || resume.adopted["gen-c"] == nil {
			t.Fatalf("naturally completed replacement not rolled forward: %+v", resume)
		}
		owner, _ := f.tr.leadership.executionLeaseTerm()
		if completed := f.durableCompletedTargets(t, owner, generation); !slices.Contains(completed, "gen-b") {
			t.Fatalf("completed targets = %v, want gen-b", completed)
		}
		return
	}
	if mode == "staged" {
		f.assertFailedRollForwardHeld(t, generation, "gen-b: REJECTED", true, false)
		// The claim that refused the adoption drained the staged assignment.
		waitForActiveVUsers(t, service, 0)
		if _, err := client.AdjustDuration(ctx, generatorrpc.DurationAdjustment{TestID: f.test.ID, ExecutionGeneration: f.oldGen, TargetDuration: commitTarget, RunSupervisionToken: f.token}); err == nil {
			t.Fatal("the drained staged assignment still accepted a duration change")
		}
		return
	}
	resume := f.test.adoptedResume
	if resume == nil || resume.finishStatus != "" || resume.adopted["gen-b"] == nil {
		t.Fatalf("committed replacement not rolled forward: %+v", resume)
	}
	if _, present, err := decodeRunContinuityCheckpoint(f.test.continuityCheckpoint); err != nil || !present {
		t.Fatalf("journal not replaced: %v", err)
	}
	// The predecessor's generation and token no longer control the replacement.
	if result, err := client.AdjustDuration(ctx, generatorrpc.DurationAdjustment{TestID: f.test.ID, ExecutionGeneration: f.oldGen, TargetDuration: time.Hour, RunSupervisionToken: f.token}); err == nil && result.Accepted {
		t.Fatal("the generator accepted a duration change from the predecessor")
	}
	token := f.test.remoteRunSupervisionToken
	if mode == "expired" {
		// The journaled target has already elapsed for the replacement, so the
		// adjustment ended its engines and it completes on its own.
		waitForActiveVUsers(t, service, 0)
		return
	}
	if got := service.ActiveVUsers(); got != 1 {
		t.Fatalf("rolled-forward replacement active VUsers = %d, want 1", got)
	}
	if result, err := client.AdjustDuration(ctx, generatorrpc.DurationAdjustment{TestID: f.test.ID, ExecutionGeneration: generation, TargetDuration: commitTarget, RunSupervisionToken: token[:]}); err != nil || !result.Accepted {
		t.Fatalf("repeat of the journaled adjustment with the rotated token = (%+v, %v)", result, err)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := stopGeneratorTestWithToken(stopCtx, client, f.test.ID, false, generation, token[:]); err != nil {
		t.Logf("stop after test: %v", err)
	}
}

// awaitRetainedTerminal waits until the generator retained the run's terminal
// outcome for generation, which a subscription replays.
func awaitRetainedTerminal(t *testing.T, client *generatorrpc.Client, testID string, generation uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := client.OpenMetricsStreamForTestGeneration(ctx, testID, generation)
	if err != nil {
		t.Fatalf("open metrics stream: %v", err)
	}
	defer stream.CloseSend()
	for {
		batch, err := stream.Receive()
		if err != nil {
			t.Fatalf("no retained terminal outcome: %v", err)
		}
		if batch.TestID == testID && strings.EqualFold(batch.LifecycleState, "COMPLETE") {
			return
		}
	}
}

func waitForActiveVUsers(t *testing.T, service *generatorrpc.Service, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for int(service.ActiveVUsers()) != want {
		if time.Now().After(deadline) {
			t.Fatalf("generator active VUsers = %d, want %d", service.ActiveVUsers(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
