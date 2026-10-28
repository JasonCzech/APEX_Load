package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSameLeaderEvacuationAttestationEligibility(t *testing.T) {
	tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	raw, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{})
	if err != nil {
		t.Fatal(err)
	}
	active, cancelActive := context.WithCancel(context.Background())
	defer cancelActive()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name       string
		status     string
		finalizing bool
		ctx        context.Context
		cancel     context.CancelFunc
		reason     string
		checkpoint string
		want       bool
	}{
		{"canceled fail closed", "STOPPING", true, canceled, cancel, uncertainEvacuationReasonPrefix + "replacement restarted", raw, true},
		{"active runtime", "STOPPING", true, active, cancelActive, uncertainEvacuationReasonPrefix + "replacement restarted", raw, false},
		{"ordinary stop", "STOPPING", true, canceled, cancel, "operator stop", raw, false},
		{"not finalizing", "STOPPING", false, canceled, cancel, uncertainEvacuationReasonPrefix + "replacement restarted", raw, false},
		{"still running", "RUNNING", true, canceled, cancel, uncertainEvacuationReasonPrefix + "replacement restarted", raw, false},
		{"no journal", "STOPPING", true, canceled, cancel, uncertainEvacuationReasonPrefix + "replacement restarted", "", false},
		{"legacy successor", "STOPPING", false, nil, nil, "old owner lost", raw, true},
		{"incomplete local runtime", "STOPPING", true, nil, cancel, uncertainEvacuationReasonPrefix + "replacement restarted", raw, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := &ManagedTest{Status: tc.status, remoteFinalizing: tc.finalizing, executionCtx: tc.ctx, cancel: tc.cancel, Error: tc.reason, continuityCheckpoint: tc.checkpoint}
			if got := interruptedEvacuationAwaitingProofLocked(candidate); got != tc.want {
				t.Fatalf("eligible=%v, want %v", got, tc.want)
			}
		})
	}
	if interruptedEvacuationAwaitingProofLocked(nil) {
		t.Fatal("nil run eligible")
	}
}

func TestSameLeaderEvacuationAttestationPreservesProofAndStopsRetries(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(fmt.Sprintf("retries_exhausted_%t", exhausted), func(t *testing.T) {
			testSameLeaderEvacuationAttestation(t, exhausted)
		})
	}
}

func testSameLeaderEvacuationAttestation(t *testing.T, exhausted bool) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	markEvacuationRecoveryRunStarted(t, store, test)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
		t.Fatal(err)
	}
	source := newFaultEvacuationGeneratorClient("gen-a", 5)
	replacement := newFaultEvacuationGeneratorClient("gen-b", 5)
	source.loseInitialStopResponses = 1 << 30
	replacement.loseInitialStopResponses = 1 << 30
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 10*time.Millisecond, nil)
	tr.execution = backend
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.mu.Lock()
	test.executionCtx, test.cancel = ctx, cancel
	test.executionGeneration = generation
	test.pendingJointPublications = []*storage.NativePublicationEvidence{{SourceID: "gen-a", Sequence: 7}}
	test.pendingJointBytes = 256
	fenceErr := tr.claimTestExecutionFence(test)
	tr.mu.Unlock()
	if fenceErr != nil {
		t.Fatal(fenceErr)
	}
	uncertain := &distributedEvacuationUncertainError{testID: test.ID, participantClients: map[string]generatorClient{"gen-a": source, "gen-b": replacement}, err: errors.New("replacement generation rejected after restart")}
	if err := backend.failClosedUncertainEvacuation(tr, test, uncertain, nil, nil); err == nil {
		t.Fatal("expected unproven stop")
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.Contains(record.Error, "/interrupted-stop/resolve") {
		t.Fatalf("unproven state=%+v err=%v", record, err)
	}
	if exhausted {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			tr.terminalRetryMu.Lock()
			_, pending := tr.terminalRetrying["evacuation:"+test.ID]
			tr.terminalRetryMu.Unlock()
			if !pending {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		tr.terminalRetryMu.Lock()
		_, pending := tr.terminalRetrying["evacuation:"+test.ID]
		tr.terminalRetryMu.Unlock()
		if pending {
			t.Fatal("bounded stop retries did not retire")
		}
		record, err = store.GetTest(test.ID)
		if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.Contains(record.Error, sameLeaderEvacuationCleanupExhausted) {
			t.Fatalf("exhaustion lost quarantine: %+v %v", record, err)
		}
		tr.mu.RLock()
		retained := len(test.remoteClientsByTarget)
		tr.mu.RUnlock()
		if source.faultStops() != (maxSameLeaderEvacuationCleanupRetries+1)*durableTransitionAttempts || replacement.faultStops() != (maxSameLeaderEvacuationCleanupRetries+1)*durableTransitionAttempts {
			t.Fatalf("unexpected stop RPC budget: source=%d replacement=%d", source.faultStops(), replacement.faultStops())
		}
		if retained != 2 {
			t.Fatalf("unproven participants released: %d", retained)
		}
	}
	server := &Server{runner: tr, store: store}
	resolve := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/interrupted-stop/resolve", strings.NewReader(body))
		req.SetPathValue("id", test.ID)
		server.handleResolveInterruptedRun(rec, req)
		return rec
	}
	if rec := resolve(`{"note":"restart observed"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing attestation=%d %s", rec.Code, rec.Body.String())
	}
	if source.active() != 5 || replacement.active() != 5 {
		t.Fatal("mere request or failed stop released participants")
	}
	// Independent operator action stops the fake processes; their old epoch RPCs
	// continue rejecting. The attestation, not those rejections, is the proof.
	for _, client := range []*faultEvacuationGeneratorClient{source, replacement} {
		client.muFault.Lock()
		client.activeVUsers = 0
		client.muFault.Unlock()
	}
	rec := resolve(`{"attestation":"all-generator-processes-stopped","note":"both exact process incarnations fenced"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-leader resolution=%d %s", rec.Code, rec.Body.String())
	}
	record, err = store.GetTest(test.ID)
	if err != nil || record.Status != "FAILED" || record.EndTime == nil || !strings.HasPrefix(record.Error, sameLeaderEvacuationAttestedProof) || !strings.Contains(record.Error, "replacement generation rejected after restart") || strings.Contains(record.Error, uncertainEvacuationIntervention) {
		t.Fatalf("terminal proof=%+v err=%v", record, err)
	}
	if currentOwner, currentGeneration := tr.leadership.executionLeaseTerm(); currentOwner != owner || currentGeneration != generation {
		t.Fatal("intervention advanced global execution epoch")
	}
	if err := tr.recoverInterruptedRunEvacuation(test); err != nil {
		t.Fatalf("post-proof cleanup=%v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tr.terminalRetryMu.Lock()
		_, pending := tr.terminalRetrying["evacuation:"+test.ID]
		tr.terminalRetryMu.Unlock()
		if !pending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tr.terminalRetryMu.Lock()
	_, pending := tr.terminalRetrying["evacuation:"+test.ID]
	tr.terminalRetryMu.Unlock()
	if pending {
		t.Fatal("durable proof left old-generation stop retry running")
	}
	tr.mu.RLock()
	retained := len(test.remoteClientsByTarget)
	heldJoint, heldBytes, dropped := len(test.pendingJointPublications), test.pendingJointBytes, test.jointPublicationsDropped
	tr.mu.RUnlock()
	if heldJoint != 0 || heldBytes != 0 || dropped != 1 {
		t.Fatalf("terminal evidence buffers not retired once: held=%d bytes=%d dropped=%d", heldJoint, heldBytes, dropped)
	}
	if retained != 0 {
		t.Fatal("proven terminal run retains canceled transports")
	}
	failure, err := store.GetRunArchiveFailure(test.ID)
	if err != nil || failure.Reason != storage.RunArchiveFailureRemoteCollection {
		t.Fatalf("missing archive disclosure=%+v %v", failure, err)
	}
	if rec := resolve(`{"attestation":"all-generator-processes-stopped"}`); rec.Code != http.StatusConflict {
		t.Fatalf("repeated terminal intervention=%d", rec.Code)
	}
}

type blockedEvacuationInterventionStore struct {
	SQLiteStore *storage.SQLiteStore
	blocked     atomic.Bool
}

func (s *blockedEvacuationInterventionStore) UpdateTestStatusIfActiveAndLeaseHeld(id, status string, ended *time.Time, reason, lease, owner string) (storage.TestStatusTransitionResult, error) {
	if s.blocked.Load() {
		return "", errors.New("injected intervention persistence failure")
	}
	return s.SQLiteStore.UpdateTestStatusIfActiveAndLeaseHeld(id, status, ended, reason, lease, owner)
}

func TestSameLeaderEvacuationInterventionRequiresDurableOwnedState(t *testing.T) {
	for _, mode := range []string{"persistence failure", "expired lease", "stale runtime", "already terminal", "success", "bounded diagnostic"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			markEvacuationRecoveryRunStarted(t, store, test)
			tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			original := uncertainEvacuationReasonPrefix + "replacement restarted" + uncertainEvacuationIntervention
			stopErr := errors.New("rpc unavailable")
			if mode == "bounded diagnostic" {
				original = uncertainEvacuationReasonPrefix + "replacement restarted password=private-cause-sentinel " + strings.Repeat("界 ", 900) + uncertainEvacuationIntervention
				stopErr = errors.New("rpc unavailable token=private-stop-sentinel " + strings.Repeat("界 ", 700))
			}
			tr.mu.Lock()
			test.Status, test.Error, test.executionCtx, test.cancel, test.remoteFinalizing = "STOPPING", original, ctx, cancel, true
			err := tr.claimTestExecutionFence(test)
			tr.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err := tr.persistStoredTestStatus(test.ID, test.executionFence, "STOPPING", nil, original); err != nil {
				t.Fatal(err)
			}
			fault := &blockedEvacuationInterventionStore{SQLiteStore: store}
			switch mode {
			case "persistence failure":
				fault.blocked.Store(true)
				tr.testStore = sqliteOverlay(fault)
			case "expired lease":
				if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "stale runtime":
				tr.tests[test.ID] = &ManagedTest{ID: test.ID, Status: "RUNNING"}
			case "already terminal":
				test.Status = "FAILED"
			}
			held, err := tr.holdSameLeaderEvacuationForIntervention(test, stopErr)
			if mode == "success" || mode == "bounded diagnostic" {
				if err != nil || !held {
					t.Fatalf("hold=%v err=%v", held, err)
				}
				record, err := store.GetTest(test.ID)
				if err != nil || record.Status != "STOPPING" || record.EndTime != nil || record.Error != test.Error || !strings.Contains(record.Error, sameLeaderEvacuationCleanupExhausted) {
					t.Fatalf("hold not durable: %+v %v", record, err)
				}
				if !utf8.ValidString(record.Error) || len(record.Error) > 2048 || strings.Contains(record.Error, "private-cause-sentinel") || strings.Contains(record.Error, "private-stop-sentinel") || !strings.Contains(record.Error, uncertainEvacuationIntervention) {
					t.Fatal("intervention diagnostic lost its bounded scrubbed operator contract")
				}
				first := record.Error
				if held, err := tr.holdSameLeaderEvacuationForIntervention(test, stopErr); err != nil || !held || test.Error != first {
					t.Fatalf("repeated hold is not stable: %v %v %q", held, err, test.Error)
				}
				return
			}
			if held || test.Error != original {
				t.Fatalf("unproven hold=%v error=%q", held, test.Error)
			}
			if (mode == "persistence failure" || mode == "expired lease") && err == nil {
				t.Fatal("failed durable write reported success")
			}
			record, readErr := store.GetTest(test.ID)
			if readErr != nil || record.Status != "STOPPING" || record.EndTime != nil || record.Error != original {
				t.Fatalf("failed hold changed durable state: %+v %v", record, readErr)
			}
			if mode == "persistence failure" {
				fault.blocked.Store(false)
				if held, err := tr.holdSameLeaderEvacuationForIntervention(test, errors.New("rpc unavailable")); err != nil || !held {
					t.Fatalf("retry=%v %v", held, err)
				}
			}
		})
	}
}

func TestSameLeaderEvacuationInterventionRemainsSystemAdminOnly(t *testing.T) {
	requirement, ok := accessRequirementFromRouteCatalog(http.MethodPost, "/api/v1/tests/example/interrupted-stop/resolve")
	if !ok {
		t.Fatal("resolution route missing")
	}
	for _, role := range []string{RoleSystemAdmin, RoleViewer, RoleProjectAdmin} {
		t.Run(role, func(t *testing.T) {
			user := authResolution{User: &AuthenticatedUser{GlobalRoles: map[string]struct{}{role: {}}, ProjectRoles: map[string]map[string]struct{}{storage.DefaultProjectID: {role: {}}}}}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/example/interrupted-stop/resolve", nil)
			allowed := authorizeAccessRequirement(response, request, nil, user, requirement)
			if allowed != (role == RoleSystemAdmin) {
				t.Fatalf("role %s allowed=%v status=%d", role, allowed, response.Code)
			}
		})
	}
}

func TestSameLeaderEvacuationAttestationSurvivesSuccessorReload(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	markEvacuationRecoveryRunStarted(t, store, test)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
		t.Fatal(err)
	}
	// Model a crash after the exact terminal proof committed but before any
	// post-terminal cleanup. The request/attestation path is covered above.
	ended := time.Now().UTC()
	proof := sameLeaderEvacuationAttestedProof + "; original replacement restart"
	result, err := store.UpdateTestStatusIfActiveAndLeaseHeld(test.ID, "FAILED", &ended, proof, executionLeaseName, owner)
	if err != nil || result != storage.TestStatusTransitionUpdated {
		t.Fatalf("persist proof=%s %v", result, err)
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "same-leader-proof-successor")
	successor.tick()
	record, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := managedTestFromRecord(record)
	tr.mu.Lock()
	tr.leadership = successor
	tr.tests[test.ID] = reloaded
	tr.mu.Unlock()
	var dials atomic.Int32
	tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 10*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		dials.Add(1)
		return nil, errors.New("attested processes no longer exist")
	})
	if err := tr.recoverInterruptedRunEvacuation(reloaded); err != nil {
		t.Fatalf("recover durable same-leader proof: %v", err)
	}
	if dials.Load() != 0 {
		t.Fatal("reloaded proof required another process acknowledgement")
	}
	record, err = store.GetTest(test.ID)
	if err != nil || record.Status != "FAILED" || record.EndTime == nil || !record.EndTime.Equal(ended) || record.Error != proof {
		t.Fatalf("successor rewrote durable terminal outcome: %+v %v", record, err)
	}
	failure, err := store.GetRunArchiveFailure(test.ID)
	if err != nil || failure.Reason != storage.RunArchiveFailureRemoteCollection {
		t.Fatalf("archive failure disclosure=%+v %v", failure, err)
	}
}

func TestSuccessiveLeaderEvacuationQuarantineConvergenceAndAttestation(t *testing.T) {
	for _, mode := range []string{"attested_on_third_leader", "partition_heals_on_third_leader"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			markEvacuationRecoveryRunStarted(t, store, test)
			tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next, runEvacuationCapacity{}); err != nil {
				t.Fatal(err)
			}
			source := newFaultEvacuationGeneratorClient("gen-a", 5)
			replacement := newFaultEvacuationGeneratorClient("gen-b", 5)
			source.loseInitialStopResponses = 1 << 30
			replacement.loseInitialStopResponses = 1 << 30
			backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 10*time.Millisecond, nil)
			tr.execution = backend
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tr.mu.Lock()
			test.executionCtx, test.cancel = ctx, cancel
			test.executionGeneration = generation
			fenceErr := tr.claimTestExecutionFence(test)
			tr.mu.Unlock()
			if fenceErr != nil {
				t.Fatal(fenceErr)
			}

			uncertain := &distributedEvacuationUncertainError{
				testID:             test.ID,
				participantClients: map[string]generatorClient{"gen-a": source, "gen-b": replacement},
				err:                errors.New("replacement generation rejected after restart"),
			}
			if err := backend.failClosedUncertainEvacuation(tr, test, uncertain, nil, nil); err == nil {
				t.Fatal("expected unproven stop")
			}

			waitRetryWorkerRetired := func(key string) {
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
				t.Fatalf("retry worker %s did not retire within budget", key)
			}

			waitRetryWorkerRetired("evacuation:" + test.ID)
			snap1, ok := tr.GetTest(test.ID)
			if !ok || snap1.EvacuationQuarantine == nil {
				t.Fatalf("leader 1 quarantine snapshot=%+v ok=%v", snap1, ok)
			}
			if !snap1.EvacuationQuarantine.RetriesExhausted ||
				snap1.EvacuationQuarantine.RetryCount != maxSameLeaderEvacuationCleanupRetries ||
				snap1.EvacuationQuarantine.MaxRetries != maxSameLeaderEvacuationCleanupRetries ||
				snap1.EvacuationQuarantine.NextRetryAt != nil ||
				len(snap1.EvacuationQuarantine.QuarantinedOwners) != 2 ||
				snap1.EvacuationQuarantine.QuarantinedOwners[0] != "gen-a" ||
				snap1.EvacuationQuarantine.QuarantinedOwners[1] != "gen-b" ||
				!strings.Contains(snap1.EvacuationQuarantine.OriginalCause, "replacement generation rejected after restart") ||
				!strings.Contains(snap1.EvacuationQuarantine.RequiredInterventionAction, "/interrupted-stop/resolve") {
				t.Fatalf("leader 1 structured quarantine mismatch: %+v", snap1.EvacuationQuarantine)
			}

			// Hand off to Leader 2 while gen-b remains unreachable.
			if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
				t.Fatal(err)
			}
			leader2 := newExecutionLeadership(store, "leader-2")
			leader2.tick()
			owner2, gen2 := leader2.executionLeaseTerm()
			if owner2 == "" || gen2 <= generation {
				t.Fatalf("leader 2 term=(%q,%d), prior=%d", owner2, gen2, generation)
			}
			record, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			reloaded2 := managedTestFromRecord(record)
			var genBReachable atomic.Bool
			genAClient := newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, "pod-a")
			genBClient := newRollbackGeneratorClient(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, "pod-b")
			tr.execution = newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 10*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
				if target == "gen-a" {
					return genAClient, nil
				}
				if target == "gen-b" && genBReachable.Load() {
					return genBClient, nil
				}
				return nil, errors.New("gen-b partitioned")
			})
			tr.mu.Lock()
			tr.leadership = leader2
			tr.tests[test.ID] = reloaded2
			reconcileErr := tr.reconcileStoredActiveTest(reloaded2)
			simNext := time.Now().UTC().Add(evacuationQuarantineRetryDelay(1))
			reloaded2.evacuationQuarantineRetries = 1
			reloaded2.evacuationQuarantineNextRetryAt = &simNext
			activeStatus2 := runEvacuationQuarantineStatusLocked(reloaded2)
			tr.mu.Unlock()
			if reconcileErr != nil {
				t.Fatal(reconcileErr)
			}
			if activeStatus2 == nil || activeStatus2.RetriesExhausted || activeStatus2.RetryCount != 1 || activeStatus2.NextRetryAt == nil || !activeStatus2.NextRetryAt.Equal(simNext) {
				t.Fatalf("successor active retry status masked by prior leader exhaustion: %+v", activeStatus2)
			}
			tr.resumeInterruptedRunEvacuations()
			waitRetryWorkerRetired("evacuation-guard:" + test.ID)

			record2, err := store.GetTest(test.ID)
			if err != nil || record2.Status != "STOPPING" || record2.EndTime != nil ||
				!strings.Contains(record2.Error, "replacement generation rejected after restart") ||
				strings.Count(record2.Error, sameLeaderEvacuationCleanupExhausted) != 1 ||
				!strings.Contains(record2.Error, "gen-b") {
				t.Fatalf("leader 2 quarantine persistence=%+v err=%v", record2, err)
			}
			snap2, ok := tr.GetTest(test.ID)
			if !ok || snap2.EvacuationQuarantine == nil ||
				!snap2.EvacuationQuarantine.RetriesExhausted ||
				snap2.EvacuationQuarantine.RetryCount != maxSameLeaderEvacuationCleanupRetries ||
				!strings.Contains(snap2.EvacuationQuarantine.OriginalCause, "replacement generation rejected after restart") {
				t.Fatalf("leader 2 structured quarantine=%+v ok=%v", snap2, ok)
			}

			// Hand off to Leader 3.
			if err := store.ReleaseSchedulerLease(executionLeaseName, owner2); err != nil {
				t.Fatal(err)
			}
			leader3 := newExecutionLeadership(store, "leader-3")
			leader3.tick()
			owner3, gen3 := leader3.executionLeaseTerm()
			if owner3 == "" || gen3 <= gen2 {
				t.Fatalf("leader 3 term=(%q,%d), prior=%d", owner3, gen3, gen2)
			}
			reloaded3 := managedTestFromRecord(record2)
			tr.mu.Lock()
			tr.leadership = leader3
			tr.tests[test.ID] = reloaded3
			reconcileErr = tr.reconcileStoredActiveTest(reloaded3)
			tr.mu.Unlock()
			if reconcileErr != nil {
				t.Fatal(reconcileErr)
			}

			if mode == "partition_heals_on_third_leader" {
				genBReachable.Store(true)
				tr.resumeInterruptedRunEvacuations()
				waitRetryWorkerRetired("evacuation-guard:" + test.ID)
				record3, err := store.GetTest(test.ID)
				if err != nil || record3.Status != "FAILED" || record3.EndTime == nil ||
					!strings.HasPrefix(record3.Error, interruptedEvacuationFenceProof) ||
					!strings.Contains(record3.Error, "replacement generation rejected after restart") ||
					strings.Contains(record3.Error, sameLeaderEvacuationCleanupExhausted) ||
					strings.Contains(record3.Error, uncertainEvacuationIntervention) {
					t.Fatalf("leader 3 automatic convergence=%+v err=%v", record3, err)
				}
				snap3, ok := tr.GetTest(test.ID)
				if !ok || snap3.EvacuationQuarantine != nil {
					t.Fatalf("terminal run still exposes quarantine: %+v ok=%v", snap3, ok)
				}
				return
			}

			server := &Server{runner: tr, leadership: leader3, store: store}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/interrupted-stop/resolve", strings.NewReader(`{"attestation":"all-generator-processes-stopped","note":"third leader operator attestation"}`))
			req.SetPathValue("id", test.ID)
			server.handleResolveInterruptedRun(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("leader 3 attestation=%d %s", rec.Code, rec.Body.String())
			}
			waitRetryWorkerRetired("evacuation-guard:" + test.ID)
			record3, err := store.GetTest(test.ID)
			if err != nil || record3.Status != "FAILED" || record3.EndTime == nil ||
				!strings.HasPrefix(record3.Error, interruptedEvacuationAttestedProof) ||
				!strings.Contains(record3.Error, "replacement generation rejected after restart") ||
				strings.Contains(record3.Error, uncertainEvacuationIntervention) {
				t.Fatalf("leader 3 attested terminal outcome=%+v err=%v", record3, err)
			}
			snap3, ok := tr.GetTest(test.ID)
			if !ok || snap3.EvacuationQuarantine != nil {
				t.Fatalf("terminal attested run still exposes quarantine: %+v ok=%v", snap3, ok)
			}
		})
	}
}
