package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type evacuationFenceFakeClient struct {
	adoptionFakeClient
	accepted atomic.Bool
	claims   atomic.Int32
}

type flakyEvacuationArchiveStore struct {
	SQLiteStore  *storage.SQLiteStore
	blockArchive atomic.Bool
}

func (s *flakyEvacuationArchiveStore) SaveRunArchiveFailure(failure *storage.RunArchiveFailure, leaseName, leaseOwner string) (bool, error) {
	if s.blockArchive.Load() {
		return false, errors.New("archive failure store unavailable")
	}
	return s.SQLiteStore.SaveRunArchiveFailure(failure, leaseName, leaseOwner)
}

func (c *evacuationFenceFakeClient) ClaimExecutionEpoch(_ context.Context, generation uint64, _ string, _ time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	c.claims.Add(1)
	return generatorrpc.ExecutionEpochResult{Accepted: c.accepted.Load(), CurrentGeneration: generation, AdoptionSupported: true, ErrorMessage: "lower-generation drain has not completed"}, nil
}

func markEvacuationRecoveryRunStarted(t *testing.T, store *storage.SQLiteStore, test *ManagedTest) {
	t.Helper()
	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	record.StartedAt = &started
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	test.StartedAt = &started
	test.CreatedAt = record.StartTime
	test.ProjectID = record.ProjectID
}

func TestInterruptedEvacuationRecoveryRequiresEveryProcessFence(t *testing.T) {
	for _, phase := range []string{runEvacuationPhasePrepared, runEvacuationPhaseStaged, runEvacuationPhaseSourceFenced, "legacy-v2-committed"} {
		t.Run(phase, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			record, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC()
			record.StartedAt = &started
			if err := store.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			test.StartedAt = &started
			test.CreatedAt = record.StartTime
			test.ProjectID = record.ProjectID
			tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
			if err != nil {
				t.Fatal(err)
			}
			if phase != runEvacuationPhasePrepared {
				guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == runEvacuationPhaseSourceFenced || phase == "legacy-v2-committed" {
				guard, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner)
				if err != nil {
					t.Fatal(err)
				}
			}
			if phase == "legacy-v2-committed" {
				guard, err = tr.markRunEvacuationReplacementCommitted(context.Background(), test, guard, generation, owner)
				if err != nil {
					t.Fatal(err)
				}
				var legacy runEvacuationCheckpoint
				if err := json.Unmarshal([]byte(guard), &legacy); err != nil {
					t.Fatal(err)
				}
				legacy.Version = 2
				raw, err := json.Marshal(legacy)
				if err != nil {
					t.Fatal(err)
				}
				if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, string(raw), executionLeaseName, owner); err != nil || result != storage.TestStatusTransitionUpdated {
					t.Fatalf("persist v2 guard: %v, %v", result, err)
				}
				guard = string(raw)
				setContinuityCheckpointLocked(test, guard)
			}
			if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
				t.Fatal(err)
			}
			successor := newExecutionLeadership(store, "guard-recovery-successor")
			successor.tick()
			tr.leadership = successor
			source, replacement := &evacuationFenceFakeClient{}, &evacuationFenceFakeClient{}
			source.accepted.Store(true)
			tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 200*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
				switch target {
				case "gen-a":
					return source, nil
				case "gen-b":
					return replacement, nil
				default:
					return nil, errors.New("unexpected generator target")
				}
			})
			if err := tr.reconcileStoredActiveTest(test); err != nil {
				t.Fatal(err)
			}
			tr.resumeAdoptedRuns()
			sourceMustBeFenced := phase == runEvacuationPhasePrepared || phase == runEvacuationPhaseStaged
			deadline := time.Now().Add(3 * time.Second)
			// Process fences run concurrently; observing the replacement call
			// does not establish that the source goroutine has been scheduled.
			for (replacement.claims.Load() == 0 || sourceMustBeFenced && source.claims.Load() == 0) && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if (source.claims.Load() > 0) != sourceMustBeFenced || replacement.claims.Load() == 0 {
				t.Fatalf("process fences did not match durable phase proof: source=%d replacement=%d phase=%s", source.claims.Load(), replacement.claims.Load(), phase)
			}
			record, err = store.GetTest(test.ID)
			if err != nil || record.Status != "STOPPING" || record.EndTime != nil {
				t.Fatalf("partial process fence was terminal: %+v, %v", record, err)
			}
			replacement.accepted.Store(true)
			deadline = time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				record, err = store.GetTest(test.ID)
				if err == nil && record.Status == "FAILED" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil || record.Status != "FAILED" || record.EndTime == nil || !strings.Contains(record.Error, "successor confirmed process-epoch fences") {
				t.Fatalf("fully fenced evacuation was not resolved: %+v, %v", record, err)
			}
		})
	}
}

func TestInterruptedEvacuationRecoveryRejectsIncompleteJournal(t *testing.T) {
	tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*runEvacuationCheckpoint)
	}{
		{name: "missing prior topology", mutate: func(g *runEvacuationCheckpoint) { g.Prior.Shards = nil }},
		{name: "source absent from prior topology", mutate: func(g *runEvacuationCheckpoint) { g.Source = "gen-c" }},
		{name: "unknown phase", mutate: func(g *runEvacuationCheckpoint) { g.Phase = "unknown" }},
		{name: "unknown prior archive target", mutate: func(g *runEvacuationCheckpoint) { g.Prior.ArchiveTargets = []string{" bad"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed runEvacuationCheckpoint
			if err := json.Unmarshal([]byte(guard), &changed); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&changed)
			raw, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := interruptedEvacuationTargets(string(raw), test.ID); err == nil {
				t.Fatal("incomplete journal was accepted for automatic terminalization")
			}
		})
	}
}

func TestInterruptedEvacuationRecoveryFencesReusedArchiveTargets(t *testing.T) {
	tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	raw, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
	if err != nil {
		t.Fatal(err)
	}
	var guard runEvacuationCheckpoint
	if err := json.Unmarshal([]byte(raw), &guard); err != nil {
		t.Fatal(err)
	}
	guard.Prior.CompletedTargets = []string{"gen-c"}
	guard.Prior.ArchiveTargets = []string{"gen-d"}
	guard.BeforeShards["ga"] = append(guard.BeforeShards["ga"],
		GeneratorShardSummary{Group: "ga", Target: "gen-c", VUsers: 5, ShardCount: 1},
		GeneratorShardSummary{Group: "ga", Target: "gen-d", VUsers: 5, ShardCount: 1})
	guard.Prior.Shards = cloneGroupShardMap(guard.BeforeShards)
	encode := func() string {
		t.Helper()
		encoded, err := json.Marshal(guard)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	_, targets, err := interruptedEvacuationTargets(encode(), test.ID)
	if err != nil || strings.Join(targets, ",") != "gen-a,gen-b,gen-d" {
		t.Fatalf("active archived generator was omitted from fence set: %v, %v", targets, err)
	}
	guard.NextShards["ga"] = append(guard.NextShards["ga"], GeneratorShardSummary{Group: "ga", Target: "gen-c", VUsers: 5, ShardCount: 1})
	_, targets, err = interruptedEvacuationTargets(encode(), test.ID)
	if err != nil || strings.Join(targets, ",") != "gen-a,gen-b,gen-d" {
		t.Fatalf("unchanged completed shard required fresh fence: %v, %v", targets, err)
	}
	guard.NextShards["ga"][1].VUsers = 6
	_, targets, err = interruptedEvacuationTargets(encode(), test.ID)
	if err != nil || strings.Join(targets, ",") != "gen-a,gen-b,gen-c,gen-d" {
		t.Fatalf("new work did not restore prior terminal target to fence set: %v, %v", targets, err)
	}
	guard.NextShards["ga"] = append(guard.NextShards["ga"], GeneratorShardSummary{Group: "ga", Target: "gen-d", VUsers: 5, ShardCount: 1})
	_, targets, err = interruptedEvacuationTargets(encode(), test.ID)
	if err != nil || strings.Join(targets, ",") != "gen-a,gen-b,gen-c,gen-d" {
		t.Fatalf("unchanged reused archive shard was omitted from fence set: %v, %v", targets, err)
	}
}

func TestInterruptedEvacuationRecoveryRequiresCapacityLedger(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
		t.Fatal(err)
	}
	test.Status = "STOPPING"
	tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 100*time.Millisecond, nil)
	// This narrow persistence facade deliberately omits the capacity ledger
	// capability. A valid journal alone cannot enumerate hidden staged pods.
	tr.testStore = struct{ testPersistence }{store}
	if err := tr.recoverInterruptedRunEvacuation(test); err == nil || !strings.Contains(err.Error(), "pod-capacity ledger") {
		t.Fatalf("missing ledger permitted terminalization: %v", err)
	}
	if test.Status != "STOPPING" || test.EndedAt != nil {
		t.Fatalf("missing ledger changed active run: %s %v", test.Status, test.EndedAt)
	}
}

func TestInterruptedEvacuationRecoveryRetriesIncompleteTerminalState(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
		t.Fatal(err)
	}
	if result, err := store.UpdateTestStatusIfActive(test.ID, "FAILED", nil, "prior incomplete terminal write"); err != nil || result != storage.TestStatusTransitionUpdated {
		t.Fatalf("persist incomplete terminal fixture: %v, %v", result, err)
	}
	test.Status = "FAILED"
	test.Error = "prior incomplete terminal write"
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "incomplete-evacuation-successor")
	successor.tick()
	tr.leadership = successor
	client := &evacuationFenceFakeClient{}
	client.accepted.Store(true)
	tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 100*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return client, nil
	})
	if err := tr.recoverInterruptedRunEvacuation(test); err == nil || !strings.Contains(err.Error(), "terminality remains incomplete") {
		t.Fatalf("incomplete terminal state retired recovery retry: %v", err)
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.EndTime != nil || test.EndedAt != nil {
		t.Fatalf("unproven incomplete terminal row changed: %+v, %v", record, err)
	}
}

func TestInterruptedEvacuationRecoveryUsesDurableSourceFenceAfterSourceDisappears(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	markEvacuationRecoveryRunStarted(t, store, test)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
	if err != nil {
		t.Fatal(err)
	}
	guard, err = tr.markRunEvacuationStaged(context.Background(), test, guard, generation, owner, []string{"gen-b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tr.markRunEvacuationSourceFenced(context.Background(), test, guard, generation, owner); err != nil {
		t.Fatal(err)
	}
	budget := protocol.CapacityVector{CPUCores: 1, MemoryMB: 100, NetworkMbps: 100, VUsers: 5}
	claim := storage.PodCapacityReservation{ReservationID: "old-source", Revision: 1, Target: "gen-a", TestID: test.ID,
		Incarnation: "gen-a-pod", Generation: generation, ModelVersion: protocol.CapacityReservationModel,
		Budget: budget, Demand: budget, GroupPeaks: map[string]int32{"ga": 5}}
	if err := store.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "fenced-source-successor")
	successor.tick()
	tr.leadership = successor
	replacement := &evacuationFenceFakeClient{}
	replacement.accepted.Store(true)
	var sourceDials atomic.Int32
	tr.execution = newDistributedExecutionBackend([]string{"gen-b"}, 100*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		if target == "gen-a" {
			sourceDials.Add(1)
			return nil, errors.New("source node disappeared")
		}
		return replacement, nil
	})
	if err := tr.reconcileStoredActiveTest(test); err != nil {
		t.Fatal(err)
	}
	if err := tr.recoverInterruptedRunEvacuation(test); err != nil {
		t.Fatalf("durably fenced source incorrectly blocked recovery: %v", err)
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "FAILED" || record.EndTime == nil || sourceDials.Load() != 0 || replacement.claims.Load() == 0 {
		t.Fatalf("source-fenced recovery did not finish from replacement proof: %+v, %v, source dials=%d replacement claims=%d", record, err, sourceDials.Load(), replacement.claims.Load())
	}
}

func TestInterruptedEvacuationRecoverySkipsLedgerClaimForCompletedTarget(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	markEvacuationRecoveryRunStarted(t, store, test)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	raw, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
	if err != nil {
		t.Fatal(err)
	}
	var guard runEvacuationCheckpoint
	if err := json.Unmarshal([]byte(raw), &guard); err != nil {
		t.Fatal(err)
	}
	completed := GeneratorShardSummary{Group: "ga", Target: "gen-c", VUsers: 5, ShardCount: 1}
	guard.BeforeShards["ga"] = append(guard.BeforeShards["ga"], completed)
	guard.NextShards["ga"] = append(guard.NextShards["ga"], completed)
	guard.Prior.Shards = cloneGroupShardMap(guard.BeforeShards)
	guard.Prior.CompletedTargets = []string{"gen-c"}
	encoded, err := json.Marshal(guard)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, string(encoded), executionLeaseName, owner); err != nil || result != storage.TestStatusTransitionUpdated {
		t.Fatalf("persist completed-target journal: %v, %v", result, err)
	}
	setContinuityCheckpointLocked(test, string(encoded))
	budget := protocol.CapacityVector{CPUCores: 1, MemoryMB: 100, NetworkMbps: 100, VUsers: 5}
	claim := storage.PodCapacityReservation{ReservationID: "completed-target", Revision: 1, Target: "gen-c", TestID: test.ID,
		Incarnation: "gen-c-pod", Generation: generation, ModelVersion: protocol.CapacityReservationModel,
		Budget: budget, Demand: budget, GroupPeaks: map[string]int32{"ga": 5}}
	if err := store.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "completed-target-successor")
	successor.tick()
	tr.leadership = successor
	source, replacement := &evacuationFenceFakeClient{}, &evacuationFenceFakeClient{}
	source.accepted.Store(true)
	replacement.accepted.Store(true)
	var completedDials atomic.Int32
	tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 100*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		switch target {
		case "gen-a":
			return source, nil
		case "gen-b":
			return replacement, nil
		default:
			completedDials.Add(1)
			return nil, errors.New("completed generator disappeared")
		}
	})
	if err := tr.reconcileStoredActiveTest(test); err != nil {
		t.Fatal(err)
	}
	if err := tr.recoverInterruptedRunEvacuation(test); err != nil {
		t.Fatalf("durably completed target blocked recovery: %v", err)
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "FAILED" || record.EndTime == nil || completedDials.Load() != 0 || source.claims.Load() == 0 || replacement.claims.Load() == 0 {
		t.Fatalf("completed-target recovery did not finish from live participant proof: %+v, %v, completed dials=%d source claims=%d replacement claims=%d", record, err, completedDials.Load(), source.claims.Load(), replacement.claims.Load())
	}
}

func TestInterruptedEvacuationTerminalCleanupDoesNotReclaimGenerators(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	record.StartedAt = &started
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	test.StartedAt = &started
	test.CreatedAt = record.StartTime
	test.ProjectID = record.ProjectID
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "terminal-cleanup-successor")
	successor.tick()
	tr.leadership = successor
	source, replacement := &evacuationFenceFakeClient{}, &evacuationFenceFakeClient{}
	source.accepted.Store(true)
	replacement.accepted.Store(true)
	tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 200*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		if target == "gen-a" {
			return source, nil
		}
		return replacement, nil
	})
	if err := tr.reconcileStoredActiveTest(test); err != nil {
		t.Fatal(err)
	}
	flaky := &flakyEvacuationArchiveStore{SQLiteStore: store}
	flaky.blockArchive.Store(true)
	tr.testStore = sqliteOverlay(flaky)
	if err := tr.recoverInterruptedRunEvacuation(test); err == nil || !strings.Contains(err.Error(), "archive failure store unavailable") {
		t.Fatalf("terminal archive failure did not request a retry: %v", err)
	}
	record, err = store.GetTest(test.ID)
	if err != nil || record.Status != "FAILED" || record.EndTime == nil || !strings.HasPrefix(record.Error, interruptedEvacuationFenceProof) {
		t.Fatalf("process-fenced terminal proof was not durable: %+v, %v", record, err)
	}
	sourceClaims, replacementClaims := source.claims.Load(), replacement.claims.Load()
	flaky.blockArchive.Store(false)
	tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 200*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return nil, errors.New("generator was removed")
	})
	if err := tr.recoverInterruptedRunEvacuation(test); err != nil {
		t.Fatalf("terminal cleanup incorrectly required generator ACKs: %v", err)
	}
	if source.claims.Load() != sourceClaims || replacement.claims.Load() != replacementClaims {
		t.Fatal("post-terminal cleanup reclaimed generators")
	}
	failure, err := store.GetRunArchiveFailure(test.ID)
	if err != nil || failure.Reason != storage.RunArchiveFailureRemoteCollection {
		t.Fatalf("archive cleanup was not retried: %+v, %v", failure, err)
	}
}

func TestInterruptedEvacuationLegacyFailedStillRequiresProcessFence(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	result, err := store.UpdateTestStatusIfActiveAndLeaseHeld(test.ID, "FAILED", &ended, "legacy failure without process proof", executionLeaseName, owner)
	if err != nil || result != storage.TestStatusTransitionUpdated {
		t.Fatalf("persist legacy terminal: %v, %v", result, err)
	}
	test.Status, test.EndedAt, test.Error = "FAILED", &ended, "legacy failure without process proof"
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "legacy-terminal-successor")
	successor.tick()
	tr.leadership = successor
	source, replacement := &evacuationFenceFakeClient{}, &evacuationFenceFakeClient{}
	source.accepted.Store(true)
	tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 200*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		if target == "gen-a" {
			return source, nil
		}
		return replacement, nil
	})
	if err := tr.recoverInterruptedRunEvacuation(test); err == nil {
		t.Fatal("legacy FAILED row skipped required process fences")
	}
	if source.claims.Load() == 0 || replacement.claims.Load() == 0 {
		t.Fatal("legacy FAILED row did not attempt each required process fence")
	}
}

func TestInterruptedEvacuationRecoveryIncludesDurableCapacityTargets(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
		t.Fatal(err)
	}
	budget := protocol.CapacityVector{CPUCores: 1, MemoryMB: 100, NetworkMbps: 100, VUsers: 10}
	claim := storage.PodCapacityReservation{ReservationID: "extra-participant", Revision: 1, Target: "gen-c", TestID: test.ID,
		Incarnation: "gen-c-pod", Generation: generation, ModelVersion: protocol.CapacityReservationModel,
		Budget: budget, Demand: budget, GroupPeaks: map[string]int32{"ga": 5}}
	if err := store.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, owner, generation); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "capacity-guard-successor")
	successor.tick()
	tr.leadership = successor
	clients := map[string]*evacuationFenceFakeClient{"gen-a": {}, "gen-b": {}, "gen-c": {}}
	clients["gen-a"].accepted.Store(true)
	clients["gen-b"].accepted.Store(true)
	tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b", "gen-c"}, 200*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		if client := clients[target]; client != nil {
			return client, nil
		}
		return nil, errors.New("unexpected generator target")
	})
	if err := tr.reconcileStoredActiveTest(test); err != nil {
		t.Fatal(err)
	}
	tr.resumeAdoptedRuns()
	deadline := time.Now().Add(3 * time.Second)
	for clients["gen-c"].claims.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if clients["gen-c"].claims.Load() == 0 {
		t.Fatal("capacity-only target was omitted from the process fence")
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil {
		t.Fatalf("unfenced capacity target allowed terminality: %+v, %v", record, err)
	}
	clients["gen-c"].accepted.Store(true)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, err = store.GetTest(test.ID)
		if err == nil && record.Status == "FAILED" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || record.Status != "FAILED" || record.EndTime == nil {
		t.Fatalf("fully fenced capacity participants were not resolved: %+v, %v", record, err)
	}
}

// A generator that is gone for good can never acknowledge the successor's
// fence, which used to strand the evacuation STOPPING with its capacity. The
// audited operator attestation stands in for the fence proof; evacuation
// recovery then performs only its post-terminal cleanup.
func TestInterruptedEvacuationOperatorAttestationResolvesVanishedGenerator(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	markEvacuationRecoveryRunStarted(t, store, test)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "attested-evacuation-successor")
	successor.tick()
	tr.leadership = successor
	var dials atomic.Int32
	tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 200*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		dials.Add(1)
		return nil, errors.New("generator node was deleted")
	})
	if err := tr.reconcileStoredActiveTest(test); err != nil {
		t.Fatal(err)
	}
	if err := tr.recoverInterruptedRunEvacuation(test); err == nil {
		t.Fatal("evacuation was resolved without any process fence")
	}
	tr.mu.RLock()
	awaiting, distributed := interruptedEvacuationAwaitingProofLocked(test), interruptedDistributedRunLocked(test)
	tr.mu.RUnlock()
	if !awaiting || distributed {
		t.Fatalf("override classes = evacuation %v, distributed %v; want evacuation only", awaiting, distributed)
	}

	server := &Server{runner: tr, store: store}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/interrupted-stop/resolve", strings.NewReader(`{"attestation":"all-generator-processes-stopped","note":"node pool deleted"}`))
	req.SetPathValue("id", test.ID)
	server.handleResolveInterruptedRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("override = %d %s", rec.Code, rec.Body.String())
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "FAILED" || record.EndTime == nil || !strings.HasPrefix(record.Error, interruptedEvacuationAttestedProof) || !strings.Contains(record.Error, "node pool deleted") {
		t.Fatalf("attested evacuation = %+v, %v", record, err)
	}
	tr.mu.RLock()
	proven := interruptedEvacuationHasDurableFenceProof(test)
	tr.mu.RUnlock()
	if !proven {
		t.Fatal("attestation was not accepted as evacuation fence proof")
	}
	before := dials.Load()
	if err := tr.recoverInterruptedRunEvacuation(test); err != nil {
		t.Fatalf("post-attestation cleanup required generator ACKs: %v", err)
	}
	failure, err := store.GetRunArchiveFailure(test.ID)
	if err != nil || failure.Reason != storage.RunArchiveFailureRemoteCollection {
		t.Fatalf("archive cleanup after attestation = %+v, %v", failure, err)
	}
	if dials.Load() != before {
		t.Fatalf("post-attestation cleanup dialed generators to re-fence them: %d -> %d", before, dials.Load())
	}
}
