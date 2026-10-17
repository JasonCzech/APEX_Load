package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func evacuationCheckpointFixture(t *testing.T) (*TestRunner, *storage.SQLiteStore, *ManagedTest, uint64, string, []byte, map[string][]GeneratorShardSummary, map[string][]GeneratorShardSummary) {
	t.Helper()
	tr, store, leader := newAdoptionTestRunner(t)
	owner, generation := leader.executionLeaseTerm()
	test := &ManagedTest{ID: "guarded-evacuation", Status: "RUNNING", Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.ID, Status: test.Status, StartTime: time.Now().UTC(), Groups: []any{}}); err != nil {
		t.Fatal(err)
	}
	tr.tests[test.ID] = test
	token := bytes.Repeat([]byte{9}, generatorrpc.RunSupervisionTokenSize)
	previous := map[string][]GeneratorShardSummary{"ga": {{Group: "ga", Target: "gen-a", VUsers: 5, ShardCount: 1}}}
	next := cloneGroupShardMap(previous)
	next["ga"][0].Target = "gen-b"
	return tr, store, test, generation, owner, token, previous, next
}

func TestRunEvacuationCheckpointInterruptionAndCompletion(t *testing.T) {
	for _, initial := range []bool{false, true} {
		t.Run(map[bool]string{false: "unarmed", true: "armed"}[initial], func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			if initial {
				tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			}
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
			if err != nil || guard == "" {
				t.Fatalf("begin = %q, %v", guard, err)
			}
			record, err := store.GetTest(test.ID)
			if err != nil || record.ContinuityCheckpoint != guard {
				t.Fatalf("guard not durable: %v", err)
			}
			if tr.isRunAdoptionCandidate(test) || runContinuityStatusLocked(test).Armed {
				t.Fatal("interrupted evacuation remains adoptable")
			}
			var decoded runEvacuationCheckpoint
			if err := json.Unmarshal([]byte(guard), &decoded); err != nil {
				t.Fatal(err)
			}
			opened, err := openRunSupervisionToken(context.Background(), tr.continuityKeyWrapper, test.ID, decoded.Prior)
			if err != nil || !bytes.Equal(opened, token) {
				t.Fatalf("prior lineage lost: %v", err)
			}
			if _, _, err := decodeRunContinuityCheckpoint(guard); err == nil {
				t.Fatal("legacy reader accepted an in-flight topology")
			}
			tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			record, _ = store.GetTest(test.ID)
			if record.ContinuityCheckpoint != guard {
				t.Fatal("ordinary re-arm erased guard")
			}
			if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next); err == nil {
				t.Fatal("second evacuation bypassed unresolved guard")
			}
			if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, previous, guard); err == nil {
				t.Fatal("wrong replacement topology completed guard")
			}
			if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err == nil {
				t.Fatal("prepared guard completed before a durable source fence")
			}
			if _, err := tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner); err == nil {
				t.Fatal("source fence accepted a guard without staging proof")
			}
			guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
			if err != nil {
				t.Fatal(err)
			}
			if tr.isRunAdoptionCandidate(test) {
				t.Fatal("staged replacement was prematurely adoptable")
			}
			if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err == nil {
				t.Fatal("staged guard completed before a durable source fence")
			}
			guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
			if err != nil {
				t.Fatal(err)
			}
			record, err = store.GetTest(test.ID)
			if err != nil || record.ContinuityCheckpoint != guard {
				t.Fatalf("source fence not durable: %v", err)
			}
			if tr.isRunAdoptionCandidate(test) {
				t.Fatal("source-fenced replacement was prematurely adoptable")
			}
			if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err == nil {
				t.Fatal("source-fenced guard completed before replacement commit was recorded")
			}
			guard, err = tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner)
			if err != nil {
				t.Fatal(err)
			}
			if tr.isRunAdoptionCandidate(test) != initial {
				t.Fatalf("committed candidate = %v, want %v", tr.isRunAdoptionCandidate(test), initial)
			}
			if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err != nil {
				t.Fatal(err)
			}
			record, _ = store.GetTest(test.ID)
			cp, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
			if err != nil || !present || cp.Shards["ga"][0].Target != "gen-b" || !runContinuityStatusLocked(test).Armed {
				t.Fatalf("completion not armed: %+v %v", cp, err)
			}
			if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err == nil {
				t.Fatal("replayed completion overwrote newer checkpoint")
			}
		})
	}
}

func TestInterruptedEvacuationPhasesRemainStoppingAfterTakeover(t *testing.T) {
	for _, phase := range []string{runEvacuationPhasePrepared, runEvacuationPhaseStaged, runEvacuationPhaseSourceFenced} {
		t.Run(phase, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
			if err != nil {
				t.Fatal(err)
			}
			if phase != runEvacuationPhasePrepared {
				guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == runEvacuationPhaseSourceFenced {
				guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
				t.Fatal(err)
			}
			successor := newExecutionLeadership(store, "evacuation-successor")
			successor.tick()
			tr.leadership = successor
			record, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			restored := &ManagedTest{ID: test.ID, Status: record.Status, Assignment: test.Assignment, CreatedAt: record.StartTime, ProjectID: record.ProjectID}
			setContinuityCheckpointLocked(restored, guard)
			if err := tr.reconcileStoredActiveTest(restored); err != nil {
				t.Fatal(err)
			}
			durable, err := store.GetTest(test.ID)
			if err != nil || durable.Status != "STOPPING" || durable.EndTime != nil || restored.Status != "STOPPING" {
				t.Fatalf("phase %s was terminal before cleanup proof: memory=%s durable=%+v err=%v", phase, restored.Status, durable, err)
			}
			if !strings.Contains(durable.Error, "generator evacuation state is uncertain") {
				t.Fatalf("phase %s lost uncertainty reason: %q", phase, durable.Error)
			}
		})
	}
}

func TestRunEvacuationCommittedSuccessorAdoptsExistingReplacement(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "evacuation-successor")
	successor.tick()
	tr.leadership = successor
	tr.execution = newDistributedExecutionBackend([]string{"gen-b"}, time.Second, func(context.Context, string) (generatorClient, error) {
		return &adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}, nil
	})
	if err := tr.reconcileStoredActiveTest(test); err != nil || test.Status != "RUNNING" {
		t.Fatalf("committed guard reconciled to %s: %v", test.Status, err)
	}
	if err := tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if test.Status != "RUNNING" || test.adoptedResume == nil || len(test.adoptedResume.adopted) != 1 || test.adoptedResume.adopted["gen-b"] == nil {
		t.Fatalf("replacement not adopted: status=%s resume=%+v", test.Status, test.adoptedResume)
	}
	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
	if err != nil || !present || checkpoint.Shards["ga"][0].Target != "gen-b" || !slices.Contains(checkpoint.ArchiveTargets, "gen-a") || checkpoint.ExecutionGeneration <= generation {
		t.Fatalf("successor checkpoint omitted replacement or retired source: %+v, %v", checkpoint, err)
	}
	if _, committed := committedRunEvacuationCheckpoint(guard, test.ID); !committed {
		t.Fatal("original committed guard lost its proof")
	}
}

func TestLegacyCommittedEvacuationWaitsForCleanupProof(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	var legacy runEvacuationCheckpoint
	if err := json.Unmarshal([]byte(guard), &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Version = 2 // v2 committed before replacement duration adjustment was acknowledged.
	legacy.Prior.Failovers = []RunFailover{{PriorGeneration: 1, Generation: 2, AdoptedAt: time.Now().UTC()}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, string(raw), executionLeaseName, owner); err != nil || result != storage.TestStatusTransitionUpdated {
		t.Fatalf("persist legacy journal: %v, %v", result, err)
	}
	setContinuityCheckpointLocked(test, string(raw))
	if len(test.continuityFailovers) != 1 {
		t.Fatal("legacy guard lost prior failover history")
	}
	if _, accepted := committedRunEvacuationCheckpoint(string(raw), test.ID); accepted || !interruptedRunEvacuation(string(raw)) {
		t.Fatal("v2 committed guard was promoted despite uncertain duration adjustment")
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "legacy-guard-successor")
	successor.tick()
	tr.leadership = successor
	if tr.isRunAdoptionCandidate(test) {
		t.Fatal("legacy guard became adoptable")
	}
	if err := tr.reconcileStoredActiveTest(test); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetTest(test.ID)
	if err != nil || test.Status != "STOPPING" || record.Status != "STOPPING" || record.EndTime != nil {
		t.Fatalf("legacy guard was terminal before cleanup proof: memory=%s durable=%+v err=%v", test.Status, record, err)
	}
}

func TestRunEvacuationCommitWaitsForReplacementCapacity(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	budget := protocol.CapacityVector{CPUCores: 1, MemoryMB: 100, NetworkMbps: 100, VUsers: 10}
	claims := []storage.PodCapacityReservation{
		{ReservationID: "attempt", Revision: 1, Target: "gen-a", TestID: test.ID, Incarnation: "source-pod", Generation: generation, ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget, GroupPeaks: map[string]int32{"ga": 5}},
		{ReservationID: "attempt", Revision: 1, Target: "gen-b", TestID: test.ID, Incarnation: "replacement-pod", Generation: generation, ModelVersion: protocol.CapacityReservationModel, Budget: budget, Demand: budget, GroupPeaks: map[string]int32{"ga": 5}},
	}
	if err := store.ReservePodCapacity(claims, executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if err := store.TransitionPodCapacity("gen-a", test.ID, "source-pod", "attempt", "releasing", executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner); err == nil || !strings.Contains(err.Error(), "no committed capacity reservation") {
		t.Fatalf("pending replacement became adoptable: %v", err)
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.ContinuityCheckpoint != guard {
		t.Fatalf("rejected commit changed checkpoint: %v", err)
	}
	if err := store.TransitionPodCapacity("gen-b", test.ID, "replacement-pod", "attempt", "committed", executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "capacity-evacuation-successor")
	successor.tick()
	newOwner, newGeneration := successor.executionLeaseTerm()
	tr.leadership = successor
	client := &capacityAdoptionClient{adoptionFakeClient: &adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}}
	client.confirm = func(*pb.PodCapacityClaim) (*pb.PodCapacityAck, error) {
		return &pb.PodCapacityAck{Accepted: true, Snapshot: &pb.PodCapacitySnapshot{Incarnation: "replacement-pod"}}, nil
	}
	tr.execution = newDistributedExecutionBackend([]string{"gen-b"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	if err := tr.reconcileStoredActiveTest(test); err != nil {
		t.Fatal(err)
	}
	if err := tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if test.adoptedResume == nil || test.adoptedResume.finishStatus != "" {
		t.Fatalf("committed replacement capacity was not adopted: %+v", test.adoptedResume)
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Target == "gen-b" && row.Generation == newGeneration && row.State == "committed" {
			return
		}
	}
	t.Fatalf("replacement capacity was not transferred to %s: %+v", newOwner, rows)
}

func TestRunEvacuationCommittedPromotionRejectsStaleLeaseAndReplay(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.promoteCommittedRunEvacuationCheckpoint(context.Background(), test, guard, generation, owner); err == nil {
		t.Fatal("original generation promoted its own guard")
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "promotion-successor")
	successor.tick()
	newOwner, newGeneration := successor.executionLeaseTerm()
	if _, err := tr.promoteCommittedRunEvacuationCheckpoint(context.Background(), test, guard, newGeneration, "wrong-owner"); err == nil {
		t.Fatal("wrong owner promoted checkpoint")
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.ContinuityCheckpoint != guard {
		t.Fatalf("rejected promotion changed durable guard: %v", err)
	}
	promoted, err := tr.promoteCommittedRunEvacuationCheckpoint(context.Background(), test, guard, newGeneration, newOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.promoteCommittedRunEvacuationCheckpoint(context.Background(), test, guard, newGeneration, newOwner); err == nil {
		t.Fatal("replayed promotion overwrote completed topology")
	}
	record, err = store.GetTest(test.ID)
	if err != nil || record.ContinuityCheckpoint != promoted {
		t.Fatalf("replayed promotion changed durable topology: %v", err)
	}
	if !tr.isRunAdoptionCandidate(test) {
		t.Fatal("promoted checkpoint is not adoptable by a second successor")
	}
}

func TestRunEvacuationCommittedProofRejectsCorruption(t *testing.T) {
	tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"prior-topology", "prior-term", "unarmed", "source-completed", "stage-mismatch", "not-committed"} {
		t.Run(fault, func(t *testing.T) {
			var changed runEvacuationCheckpoint
			if err := json.Unmarshal([]byte(guard), &changed); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "prior-topology":
				changed.Prior.Shards["ga"][0].Target = "gen-c"
			case "prior-term":
				changed.Prior.ExecutionGeneration++
			case "unarmed":
				changed.Prior.ArmedAt = time.Time{}
			case "source-completed":
				changed.Prior.CompletedTargets = []string{"gen-a"}
			case "stage-mismatch":
				changed.StagedTargets = []string{"gen-c"}
			case "not-committed":
				changed.Phase = runEvacuationPhaseSourceFenced
			}
			raw, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, accepted := committedRunEvacuationCheckpoint(string(raw), test.ID); accepted {
				t.Fatal("corrupt committed evacuation became adoptable")
			}
		})
	}
}

func TestRunEvacuationStagingRejectsInvalidProgress(t *testing.T) {
	for _, tc := range []struct {
		name    string
		targets []string
	}{
		{"empty", nil},
		{"duplicate", []string{"gen-b", "gen-b"}},
		{"source", []string{"gen-a"}},
		{"unknown", []string{"gen-c"}},
		{"mixed", []string{"gen-b", "gen-c"}},
		{"whitespace", []string{" gen-b "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, tc.targets); err == nil {
				t.Fatal("invalid staged target set accepted")
			}
			record, err := store.GetTest(test.ID)
			if err != nil || record.ContinuityCheckpoint != guard {
				t.Fatalf("invalid staging changed durable guard: %v", err)
			}
		})
	}
	for _, fault := range []string{"wrong-owner", "wrong-generation", "stale-store", "lost-lease", "write-failure", "canceled", "replay"} {
		t.Run(fault, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			switch fault {
			case "wrong-owner":
				owner = "other-owner"
			case "wrong-generation":
				generation++
			case "stale-store":
				if _, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, `{"version":99}`, executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "lost-lease":
				if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "write-failure":
				tr.testStore = sqliteOverlay(&evacuationCheckpointFaultStore{SQLiteStore: store, reject: true})
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "replay":
				if _, err := tr.markRunEvacuationStaged(ctx, test, guard, generation, owner, []string{"gen-b"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tr.markRunEvacuationStaged(ctx, test, guard, generation, owner, []string{"gen-b"}); err == nil {
				t.Fatal("stale staged progress accepted")
			}
			record, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fault != "stale-store" && fault != "replay" && record.ContinuityCheckpoint != guard {
				t.Fatal("rejected staging changed durable checkpoint")
			}
		})
	}
}

func TestRunEvacuationReplacementTargets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before string
		next   []string
		want   []string
	}{
		{"one replacement", "gen-a", []string{"gen-b"}, []string{"gen-b"}},
		{"deduplicated replacements", "gen-a", []string{"gen-c", "gen-b", "gen-b"}, []string{"gen-b", "gen-c"}},
		{"source absent", "gen-x", []string{"gen-b"}, nil},
		{"source retained", "gen-a", []string{"gen-a", "gen-b"}, nil},
		{"no replacement", "gen-a", nil, nil},
		{"invalid target", "gen-a", []string{" gen-b "}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard := runEvacuationCheckpoint{Source: "gen-a", BeforeShards: map[string][]GeneratorShardSummary{
				"a": {{Target: tc.before}},
			}, NextShards: map[string][]GeneratorShardSummary{"a": {}}}
			for _, target := range tc.next {
				guard.NextShards["a"] = append(guard.NextShards["a"], GeneratorShardSummary{Target: target})
			}
			got, err := runEvacuationReplacementTargets(guard)
			if tc.want == nil {
				if err == nil {
					t.Fatalf("invalid topology returned targets %v", got)
				}
			} else if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("targets = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

type evacuationCheckpointFaultStore struct {
	SQLiteStore *storage.SQLiteStore
	before      func()
	reject      bool
	rejectWrite func(string) bool
}

func (s *evacuationCheckpointFaultStore) CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner string) (storage.TestStatusTransitionResult, error) {
	if s.before != nil {
		s.before()
	}
	if s.reject || (s.rejectWrite != nil && s.rejectWrite(next)) {
		return "", errors.New("injected checkpoint write failure")
	}
	return s.SQLiteStore.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner)
}

func TestRunEvacuationCheckpointRejectsUnavailableOwnershipAndStorage(t *testing.T) {
	for _, fault := range []string{"store", "terminal", "stale-snapshot", "wrong-owner", "wrong-generation", "canceled", "unsupported", "lost-lease"} {
		t.Run(fault, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			expected := test.continuityCheckpoint
			ctx := context.Background()
			switch fault {
			case "store":
				tr.testStore = sqliteOverlay(&evacuationCheckpointFaultStore{SQLiteStore: store, reject: true})
			case "terminal":
				now := time.Now()
				if err := store.UpdateTestStatus(test.ID, "FAILED", &now, "stopped"); err != nil {
					t.Fatal(err)
				}
			case "stale-snapshot":
				if _, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, "changed", executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "wrong-owner":
				owner = "other-owner"
			case "wrong-generation":
				generation++
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "lost-lease":
				if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "unsupported":
				setContinuityCheckpointLocked(test, `{"version":99}`)
				expected = test.continuityCheckpoint
			}
			guard, err := tr.beginRunEvacuationCheckpoint(ctx, test, generation, owner, "gen-a", token, previous, next)
			if err == nil || guard != "" {
				t.Fatalf("unsafe begin accepted: %q %v", guard, err)
			}
			if test.continuityCheckpoint != expected {
				t.Fatal("failed persistence changed in-memory checkpoint")
			}
		})
	}
}

func TestRunEvacuationCheckpointRejectedCompletionRemainsUnarmed(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	test.Assignment.FailoverPolicy = "fail"
	if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err == nil {
		t.Fatal("changed policy falsely acknowledged completion")
	}
	test.Assignment.FailoverPolicy = "continue"
	tr.testStore = sqliteOverlay(&evacuationCheckpointFaultStore{SQLiteStore: store, reject: true})
	if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err == nil {
		t.Fatal("failed completion accepted")
	}
	record, _ := store.GetTest(test.ID)
	if record.ContinuityCheckpoint != guard || runContinuityStatusLocked(test).Armed {
		t.Fatal("failed completion cleared durable guard")
	}
	tr.testStore = store
	restored := &ManagedTest{ID: test.ID, Status: "RUNNING", Assignment: test.Assignment,
		CreatedAt: record.StartTime, ProjectID: record.ProjectID}
	setContinuityCheckpointLocked(restored, record.ContinuityCheckpoint)
	if err := tr.reconcileStoredActiveTest(restored); err != nil {
		t.Fatal(err)
	}
	if restored.Status != "STOPPING" || !strings.Contains(restored.Error, "generator evacuation state is uncertain") {
		t.Fatalf("interrupted outcome: %s %s", restored.Status, restored.Error)
	}
	durable, err := store.GetTest(test.ID)
	if err != nil || durable.Status != "STOPPING" || durable.EndTime != nil {
		t.Fatalf("interrupted durable outcome = %+v, %v", durable, err)
	}
	if failure, err := store.GetRunArchiveFailure(test.ID); failure != nil || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("archive failure was published before cleanup proof: %+v, %v", failure, err)
	}
}

func TestRunEvacuationSourceFenceRejectsStaleOrUnownedProgress(t *testing.T) {
	if _, err := (*TestRunner)(nil).markRunEvacuationSourceFenced(context.Background(), nil, "", 0, ""); err == nil {
		t.Fatal("nil runner and test accepted")
	}
	for _, fault := range []string{"wrong-owner", "wrong-generation", "stage-mismatch", "stale-memory", "stale-store", "lost-lease", "write-failure", "canceled", "replay"} {
		t.Run(fault, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
			if err != nil {
				t.Fatal(err)
			}
			guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			switch fault {
			case "wrong-owner":
				owner = "other-owner"
			case "wrong-generation":
				generation++
			case "stage-mismatch":
				var changed runEvacuationCheckpoint
				if err := json.Unmarshal([]byte(guard), &changed); err != nil {
					t.Fatal(err)
				}
				changed.StagedTargets = []string{"gen-c"}
				raw, err := json.Marshal(changed)
				if err != nil {
					t.Fatal(err)
				}
				guard = string(raw)
				if _, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, guard, executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
				setContinuityCheckpointLocked(test, guard)
			case "stale-memory":
				setContinuityCheckpointLocked(test, `{"version":99}`)
			case "stale-store":
				if _, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, `{"version":99}`, executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "lost-lease":
				if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "write-failure":
				tr.testStore = sqliteOverlay(&evacuationCheckpointFaultStore{SQLiteStore: store, reject: true})
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "replay":
				if _, err := tr.markRunEvacuationSourceFenced(ctx, test, guard, generation, owner); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tr.markRunEvacuationSourceFenced(ctx, test, guard, generation, owner); err == nil {
				t.Fatal("invalid source-fence progress accepted")
			}
			record, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fault != "stale-store" && fault != "replay" && record.ContinuityCheckpoint != guard {
				t.Fatal("failed progress changed durable checkpoint")
			}
		})
	}
}

func TestRunEvacuationReplacementCommitRejectsInvalidProgress(t *testing.T) {
	for _, fault := range []string{"unfenced", "wrong-owner", "wrong-generation", "stage-mismatch", "stale-store", "lost-lease", "write-failure", "canceled", "replay"} {
		t.Run(fault, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
			if err != nil {
				t.Fatal(err)
			}
			guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
			if err != nil {
				t.Fatal(err)
			}
			if fault != "unfenced" {
				guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			switch fault {
			case "wrong-owner":
				owner = "other-owner"
			case "wrong-generation":
				generation++
			case "stage-mismatch":
				var changed runEvacuationCheckpoint
				if err := json.Unmarshal([]byte(guard), &changed); err != nil {
					t.Fatal(err)
				}
				changed.StagedTargets = []string{"gen-c"}
				raw, err := json.Marshal(changed)
				if err != nil {
					t.Fatal(err)
				}
				guard = string(raw)
				if _, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, guard, executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
				setContinuityCheckpointLocked(test, guard)
			case "stale-store":
				if _, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, `{"version":99}`, executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "lost-lease":
				if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "write-failure":
				tr.testStore = sqliteOverlay(&evacuationCheckpointFaultStore{SQLiteStore: store, reject: true})
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "replay":
				if _, err := tr.markRunEvacuationReplacementCommitted(ctx, test, guard, generation, owner); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tr.markRunEvacuationReplacementCommitted(ctx, test, guard, generation, owner); err == nil {
				t.Fatal("invalid replacement commit progress accepted")
			}
		})
	}
}

type unavailableArchiveFailureStore struct{ SQLiteStore *storage.SQLiteStore }

func (*unavailableArchiveFailureStore) SaveRunArchiveFailure(*storage.RunArchiveFailure, string, string) (bool, error) {
	return false, errors.New("injected archive failure store outage")
}

func TestRunEvacuationCheckpointRecoveryDefersArchiveMarkerWhenCleanupIsUnproven(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.ContinuityCheckpoint != guard {
		t.Fatalf("guard = %+v, %v", record, err)
	}
	tr.testStore = sqliteOverlay(&unavailableArchiveFailureStore{store})
	restored := &ManagedTest{ID: test.ID, Status: "RUNNING", Assignment: test.Assignment,
		CreatedAt: record.StartTime, ProjectID: record.ProjectID}
	setContinuityCheckpointLocked(restored, guard)
	if err := tr.reconcileStoredActiveTest(restored); err != nil {
		t.Fatal(err)
	}
	if restored.Status != "STOPPING" {
		t.Fatalf("unproven cleanup status = %q", restored.Status)
	}
	failure, err := store.GetRunArchiveFailure(test.ID)
	if failure != nil || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("premature archive failure marker = %+v, %v", failure, err)
	}
}

func TestRunEvacuationCheckpointRecoveryPreservesPendingArchiveCollection(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	intent, prepared, err := store.PrepareRunArchiveCollection(test.ID, record.ProjectID, record.StartTime,
		map[string]string{"gen-a": strings.Repeat("a", 64)}, executionLeaseName, owner)
	if err != nil || !prepared {
		t.Fatalf("archive collection intent = %+v, %v, %v", intent, prepared, err)
	}
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	restored := &ManagedTest{ID: test.ID, Status: "RUNNING", Assignment: test.Assignment,
		CreatedAt: record.StartTime, ProjectID: record.ProjectID}
	setContinuityCheckpointLocked(restored, guard)
	if err := tr.reconcileStoredActiveTest(restored); err != nil {
		t.Fatal(err)
	}
	failure, err := store.GetRunArchiveFailure(test.ID)
	if failure != nil || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending collection was marked failed before terminal proof = %+v, %v", failure, err)
	}
	retained, err := store.GetRunArchiveCollection(test.ID)
	if err != nil || retained.IntentToken != intent.IntentToken {
		t.Fatalf("pending collection intent was lost: %+v, %v", retained, err)
	}
	if claimed, err := store.ClaimRunArchiveCollectionAttempt(test.ID, intent.IntentToken, 0, executionLeaseName, owner); err != nil || claimed {
		t.Fatalf("nonterminal STOPPING run allowed archive collection = %v, %v", claimed, err)
	}
}

func TestRunContinuityCASDoesNotOverwriteConcurrentEvacuationIntent(t *testing.T) {
	tr, store, test, generation, owner, token, previous, _ := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	const concurrent = `{"version":2,"source":"gen-a"}`
	tr.testStore = sqliteOverlay(&evacuationCheckpointFaultStore{SQLiteStore: store, before: func() {
		if _, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, concurrent, executionLeaseName, owner); err != nil {
			t.Fatal(err)
		}
	}})
	if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, previous, ""); err == nil {
		t.Fatal("stale arm overwrote concurrent intent")
	}
	record, _ := store.GetTest(test.ID)
	if record.ContinuityCheckpoint != concurrent {
		t.Fatal("durable intent overwritten")
	}
}

func TestRunEvacuationCheckpointPreservesHistoryAndRejectsInvalidCompletion(t *testing.T) {
	tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	// A previous re-arm may have failed after recording an adoption in memory.
	test.continuityFailovers = []RunFailover{{PriorGeneration: 1, Generation: 2, AdoptedAt: time.Now().UTC(), AdoptedGenerators: 1}}
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner)
	if err != nil {
		t.Fatal(err)
	}
	restored := &ManagedTest{Assignment: test.Assignment}
	setContinuityCheckpointLocked(restored, guard)
	if len(restored.continuityFailovers) != 1 || runContinuityStatusLocked(restored).Armed {
		t.Fatal("interruption lost historical failover or became armed")
	}
	for _, field := range []string{"version", "test", "generation", "owner", "source", "time", "phase", "staged-time", "staged-targets", "staged-mismatch", "fenced-time", "committed-time", "topology"} {
		t.Run(field, func(t *testing.T) {
			var changed runEvacuationCheckpoint
			if err := json.Unmarshal([]byte(guard), &changed); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "version":
				changed.Version = 1
			case "test":
				changed.TestID = "other"
			case "generation":
				changed.Generation++
			case "owner":
				changed.OwnerID = "other"
			case "source":
				changed.Source = ""
			case "time":
				changed.PreparedAt = time.Time{}
			case "phase":
				changed.Phase = runEvacuationPhasePrepared
			case "staged-time":
				changed.StagedAt = time.Time{}
			case "staged-targets":
				changed.StagedTargets = nil
			case "staged-mismatch":
				changed.StagedTargets = []string{"gen-c"}
			case "fenced-time":
				changed.SourceFencedAt = time.Time{}
			case "committed-time":
				changed.ReplacementCommittedAt = time.Time{}
			case "topology":
				changed.NextShards["ga"][0].VUsers++
			}
			raw, _ := json.Marshal(changed)
			if err := validateRunEvacuationCompletion(string(raw), test.ID, generation, owner, next); err == nil {
				t.Fatal("mismatched completion accepted")
			}
		})
	}
	if err := validateRunEvacuationCompletion("{", test.ID, generation, owner, next); err == nil {
		t.Fatal("malformed completion accepted")
	}
}

func TestRunEvacuationCheckpointPreservesNonContinuityBehavior(t *testing.T) {
	tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	for _, policy := range []string{"fail", "continue"} {
		test.Assignment.FailoverPolicy = policy
		if policy == "continue" {
			tr.continuityKeyWrapper = nil
		}
		guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
		if err != nil || guard != "" {
			t.Fatalf("unarmed %s run unexpectedly guarded: %v", policy, err)
		}
	}
}
