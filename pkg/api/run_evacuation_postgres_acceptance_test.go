//go:build postgres_integration

package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newPostgresEvacuationRunner(t *testing.T, dsn, replicaID string) (*TestRunner, *storage.PostgresStore) {
	t.Helper()
	tr, store := newPostgresLiveRunner(t, dsn, replicaID)
	tr.evacuationRollbackRecovery = true
	tr.evacuationRollForwardRecovery = true
	tr.evacuationReplacementRestart = true
	return tr, store
}

func TestPostgresEvacuationReplacementRestartAndSuccessiveLeaderQuarantine(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")

	t.Run("postgres_replacement_restart_and_deposed_leader_fencing", func(t *testing.T) {
		dsn := newPostgresScriptAuthoringAPIFixture(t)
		leader1, store1 := newPostgresEvacuationRunner(t, dsn, "pg-evac-leader-1")
		leader1.leadership.tick()
		owner1, gen1 := leader1.leadership.executionLeaseTerm()
		if owner1 == "" || gen1 == 0 {
			t.Fatalf("leader 1 term=(%q,%d)", owner1, gen1)
		}

		now := time.Now().UTC().Truncate(time.Microsecond)
		startedAt := now.Add(-15 * time.Second)
		test := &ManagedTest{
			ID:          "pg-evac-restart-1",
			Name:        "PG Evacuation Restart",
			ProjectID:   storage.DefaultProjectID,
			Status:      "RUNNING",
			TotalVUsers: 10,
			Duration:    2 * time.Minute,
			DurationStr: "2m0s",
			CreatedAt:   now,
			StartedAt:   &startedAt,
			Assignment:  &GeneratorAssignmentSettings{FailoverPolicy: "continue"},
			Groups:      []TestGroup{{Name: "ga", VUsers: 5, Protocol: "http"}, {Name: "gb", VUsers: 5, Protocol: "http"}},
		}
		record := buildManagedTestRecord(test)
		record.Status = "RUNNING"
		record.StartedAt = &startedAt
		record.ExecutionGeneration = gen1
		if err := store1.SaveTest(record); err != nil {
			t.Fatal(err)
		}
		leader1.mu.Lock()
		leader1.tests[test.ID] = test
		if err := leader1.claimTestExecutionFence(test); err != nil {
			leader1.mu.Unlock()
			t.Fatal(err)
		}
		leader1.mu.Unlock()
		if err := leader1.persistStoredTestStatus(test.ID, test.executionFence, "RUNNING", nil, ""); err != nil {
			t.Fatal(err)
		}

		previous := map[string][]GeneratorShardSummary{
			"ga": {{Group: "ga", Target: "gen-a", VUsers: 5}},
			"gb": {{Group: "gb", Target: "gen-c", VUsers: 5}},
		}
		next := map[string][]GeneratorShardSummary{
			"ga": {{Group: "ga", Target: "gen-b", VUsers: 5}},
			"gb": {{Group: "gb", Target: "gen-c", VUsers: 5}},
		}
		token := bytes.Repeat([]byte{9}, generatorrpc.RunSupervisionTokenSize)
		leader1.armRunContinuity(context.Background(), test, gen1, owner1, token, previous)

		claim := storage.PodCapacityReservation{
			ReservationID: "pg-attempt",
			Revision:      1,
			Target:        "gen-b",
			TestID:        test.ID,
			Incarnation:   "pod-b",
			Generation:    gen1,
			State:         "pending",
			ModelVersion:  protocol.CapacityReservationModel,
			Budget:        rollbackBudget,
			Demand:        rollbackDemand,
			GroupPeaks:    map[string]int32{"ga": 5},
			GroupDemands:  map[string]protocol.CapacityVector{"ga": rollbackDemand},
		}
		if err := store1.ReservePodCapacity([]storage.PodCapacityReservation{claim}, executionLeaseName, owner1, gen1); err != nil {
			t.Fatal(err)
		}
		raw, err := leader1.beginRunEvacuationCheckpoint(context.Background(), test, gen1, owner1, "gen-a", token, previous, next, runEvacuationCapacity{claims: []storage.PodCapacityReservation{claim}})
		if err != nil {
			t.Fatal(err)
		}
		raw, err = leader1.markRunEvacuationStaged(context.Background(), test, raw, gen1, owner1, []string{"gen-b"}, runEvacuationStagedTiming{steadyElapsed: 15 * time.Second, remaining: 105 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		raw, err = leader1.markRunEvacuationSourceFenced(context.Background(), test, raw, gen1, owner1)
		if err != nil {
			t.Fatal(err)
		}

		// Hand off to Leader 2 on a separate Postgres connection.
		if err := store1.ReleaseSchedulerLease(executionLeaseName, owner1); err != nil {
			t.Fatal(err)
		}
		leader2, store2 := newPostgresEvacuationRunner(t, dsn, "pg-evac-leader-2")
		leader2.leadership.tick()
		owner2, gen2 := leader2.leadership.executionLeaseTerm()
		if owner2 == "" || gen2 <= gen1 {
			t.Fatalf("leader 2 term=(%q,%d), prior=%d", owner2, gen2, gen1)
		}

		// Deposed leader 1 attempts stale checkpoint CAS and status transition in PostgreSQL.
		casRes, err := store1.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(test.ID, raw, `{"version":1}`, executionLeaseName, owner1)
		if err != nil || casRes == storage.TestStatusTransitionUpdated {
			t.Fatalf("deposed leader PG checkpoint CAS succeeded: res=%s err=%v", casRes, err)
		}
		ended := time.Now().UTC()
		res, err := store1.UpdateTestStatusIfActiveAndLeaseHeld(test.ID, "FAILED", &ended, "stale leader 1 stop", executionLeaseName, owner1)
		if err != nil || res == storage.TestStatusTransitionUpdated {
			t.Fatalf("deposed leader PG status transition succeeded: res=%s err=%v", res, err)
		}

		// Leader 2 adopts and writes replacement_restarting intent in PostgreSQL.
		storedRec, err := store2.GetTest(test.ID)
		if err != nil {
			t.Fatal(err)
		}
		restored2 := managedTestFromRecord(storedRec)
		restored2.Assignment = test.Assignment
		restored2.Groups = test.Groups
		leader2.mu.Lock()
		leader2.tests[test.ID] = restored2
		leader2.mu.Unlock()

		sourceGen := newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "pod-a")
		spareGen := newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, "pod-b")
		survivorGen := newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "pod-c")
		leader2.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b", "gen-c"}, 100*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
			switch target {
			case "gen-a":
				return sourceGen, nil
			case "gen-b":
				return spareGen, nil
			case "gen-c":
				return survivorGen, nil
			default:
				return nil, errors.New("unknown target " + target)
			}
		})
		if err := leader2.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		afterAdopt, err := store2.GetTest(test.ID)
		if err != nil {
			t.Fatal(err)
		}
		guard, targets, err := interruptedEvacuationTargets(afterAdopt.ContinuityCheckpoint, test.ID)
		if err != nil || guard.Phase != runEvacuationPhaseReplacementRestarting || guard.RestartGeneration != gen2 || len(targets) == 0 {
			t.Fatalf("PG restart intent journal=%+v targets=%v err=%v", guard, targets, err)
		}
	})

	t.Run("postgres_successive_leader_quarantine_convergence_and_attestation", func(t *testing.T) {
		dsn := newPostgresScriptAuthoringAPIFixture(t)
		leader1, store1 := newPostgresEvacuationRunner(t, dsn, "pg-quarantine-leader-1")
		leader1.leadership.tick()
		owner1, gen1 := leader1.leadership.executionLeaseTerm()

		now := time.Now().UTC().Truncate(time.Microsecond)
		startedAt := now.Add(-10 * time.Second)
		test := &ManagedTest{
			ID:          "pg-quarantine-run-1",
			Name:        "PG Quarantine Run",
			ProjectID:   storage.DefaultProjectID,
			Status:      "RUNNING",
			TotalVUsers: 10,
			Duration:    time.Minute,
			DurationStr: "1m0s",
			CreatedAt:   now,
			StartedAt:   &startedAt,
			Assignment:  &GeneratorAssignmentSettings{FailoverPolicy: "continue"},
			Groups:      []TestGroup{{Name: "ga", VUsers: 5, Protocol: "http"}},
		}
		rec := buildManagedTestRecord(test)
		rec.Status = "RUNNING"
		rec.StartedAt = &startedAt
		rec.ExecutionGeneration = gen1
		if err := store1.SaveTest(rec); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		leader1.mu.Lock()
		test.executionCtx, test.cancel = ctx, cancel
		test.executionGeneration = gen1
		leader1.tests[test.ID] = test
		if err := leader1.claimTestExecutionFence(test); err != nil {
			leader1.mu.Unlock()
			t.Fatal(err)
		}
		leader1.mu.Unlock()
		if err := leader1.persistStoredTestStatus(test.ID, test.executionFence, "RUNNING", nil, ""); err != nil {
			t.Fatal(err)
		}

		previous := map[string][]GeneratorShardSummary{"ga": {{Group: "ga", Target: "gen-a", VUsers: 5}}}
		next := map[string][]GeneratorShardSummary{"ga": {{Group: "ga", Target: "gen-b", VUsers: 5}}}
		token := bytes.Repeat([]byte{11}, generatorrpc.RunSupervisionTokenSize)
		leader1.armRunContinuity(context.Background(), test, gen1, owner1, token, previous)
		if _, err := leader1.beginRunEvacuationCheckpoint(context.Background(), test, gen1, owner1, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
			t.Fatal(err)
		}

		source := newFaultEvacuationGeneratorClient("gen-a", 5)
		replacement := newFaultEvacuationGeneratorClient("gen-b", 5)
		source.loseInitialStopResponses = 1 << 30
		replacement.loseInitialStopResponses = 1 << 30
		backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 10*time.Millisecond, nil)
		leader1.execution = backend
		uncertain := &distributedEvacuationUncertainError{
			testID:             test.ID,
			participantClients: map[string]generatorClient{"gen-a": source, "gen-b": replacement},
			err:                errors.New("pg replacement generation rejected after restart"),
		}
		if err := backend.failClosedUncertainEvacuation(leader1, test, uncertain, nil, nil); err == nil {
			t.Fatal("expected unproven stop")
		}

		waitRetired := func(tr *TestRunner, key string) {
			t.Helper()
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				tr.terminalRetryMu.Lock()
				_, pending := tr.terminalRetrying[key]
				tr.terminalRetryMu.Unlock()
				if !pending {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("retry worker %s did not retire", key)
		}
		waitRetired(leader1, "evacuation:"+test.ID)

		// Hand off to Leader 2 on Postgres; gen-b stays partitioned until Leader 2 exhausts retries.
		if err := store1.ReleaseSchedulerLease(executionLeaseName, owner1); err != nil {
			t.Fatal(err)
		}
		leader2, store2 := newPostgresEvacuationRunner(t, dsn, "pg-quarantine-leader-2")
		leader2.leadership.tick()
		stored2, err := store2.GetTest(test.ID)
		if err != nil {
			t.Fatal(err)
		}
		reloaded2 := managedTestFromRecord(stored2)
		var genBReachable atomic.Bool
		genAClient := newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, "pod-a")
		genBClient := newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, "pod-b")
		leader2.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 10*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
			if target == "gen-a" {
				return genAClient, nil
			}
			if target == "gen-b" && genBReachable.Load() {
				return genBClient, nil
			}
			return nil, errors.New("gen-b unreachable")
		})
		leader2.mu.Lock()
		leader2.tests[test.ID] = reloaded2
		if err := leader2.reconcileStoredActiveTest(reloaded2); err != nil {
			leader2.mu.Unlock()
			t.Fatal(err)
		}
		leader2.mu.Unlock()
		leader2.resumeInterruptedRunEvacuations()
		waitRetired(leader2, "evacuation-guard:"+test.ID)

		snap2, ok := leader2.GetTest(test.ID)
		if !ok || snap2.EvacuationQuarantine == nil || !snap2.EvacuationQuarantine.RetriesExhausted ||
			!strings.Contains(snap2.EvacuationQuarantine.OriginalCause, "pg replacement generation rejected after restart") {
			t.Fatalf("leader 2 PG quarantine snapshot=%+v ok=%v", snap2, ok)
		}

		// Attest on Leader 2 via HTTP handler.
		server := &Server{runner: leader2, leadership: leader2.leadership, store: store2}
		recHTTP := httptest.NewRecorder()
		reqHTTP := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/interrupted-stop/resolve", strings.NewReader(`{"attestation":"all-generator-processes-stopped","note":"pg operator attestation"}`))
		reqHTTP.SetPathValue("id", test.ID)
		server.handleResolveInterruptedRun(recHTTP, reqHTTP)
		if recHTTP.Code != http.StatusOK {
			t.Fatalf("PG attestation=%d %s", recHTTP.Code, recHTTP.Body.String())
		}
		waitRetired(leader2, "evacuation-guard:"+test.ID)
		finalRec, err := store2.GetTest(test.ID)
		if err != nil || finalRec.Status != "FAILED" || finalRec.EndTime == nil ||
			!strings.HasPrefix(finalRec.Error, interruptedEvacuationAttestedProof) ||
			!strings.Contains(finalRec.Error, "pg replacement generation rejected after restart") {
			t.Fatalf("PG final attested record=%+v err=%v", finalRec, err)
		}
	})
}
