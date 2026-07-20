package generatorrpc

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func validatedStageRequest(t testing.TB, request *pb.WorkAssignment) validatedWorkAssignment {
	t.Helper()
	validated, err := validateWorkAssignment(request, defaultGeneratorScriptMaxBytes, true)
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func stageRequest(testID, scriptID string, vusers int32) *pb.WorkAssignment {
	return &pb.WorkAssignment{
		TestId:        testID,
		GroupName:     "group",
		ScriptId:      scriptID,
		ScriptContent: []byte(`{"name":"stage"}`),
		VuserCount:    vusers,
	}
}

func TestAssignmentCoordinatorDuplicateWaitsForPendingStage(t *testing.T) {
	service := newIsolatedService(t, 10)
	validated := validatedStageRequest(t, stageRequest("run", "script", 1))

	service.mu.Lock()
	first := service.lifecycleCoordinator.reserveStageLocked(service, validated)
	second := service.lifecycleCoordinator.reserveStageLocked(service, validated)
	service.mu.Unlock()
	if first.kind != assignmentStagePrepare || first.plan == nil {
		t.Fatalf("first staging decision = %+v", first)
	}
	if second.kind != assignmentStageWait || second.wait == nil {
		t.Fatalf("duplicate staging decision = %+v, want wait", second)
	}
	select {
	case <-second.wait:
		t.Fatal("duplicate wait completed before reservation rollback")
	default:
	}

	service.mu.Lock()
	if !service.lifecycleCoordinator.rollbackStageLocked("run", first.plan.token) {
		t.Fatal("coordinator did not roll back current reservation")
	}
	service.mu.Unlock()
	select {
	case <-second.wait:
	case <-time.After(time.Second):
		t.Fatal("duplicate waiter was not released by rollback")
	}
}

func TestAssignmentCoordinatorCapacityIncludesPendingReservations(t *testing.T) {
	service := newIsolatedService(t, 1)
	firstValidated := validatedStageRequest(t, stageRequest("run-1", "script-1", 1))
	secondValidated := validatedStageRequest(t, stageRequest("run-2", "script-2", 1))

	service.mu.Lock()
	first := service.lifecycleCoordinator.reserveStageLocked(service, firstValidated)
	second := service.lifecycleCoordinator.reserveStageLocked(service, secondValidated)
	service.lifecycleCoordinator.rollbackStageLocked("run-1", first.plan.token)
	service.mu.Unlock()
	if first.kind != assignmentStagePrepare {
		t.Fatalf("first staging decision = %+v", first)
	}
	if second.kind != assignmentStageRejected || second.errorMessage != "insufficient generator capacity" {
		t.Fatalf("second staging decision = %+v, want capacity rejection", second)
	}
}

func TestAssignmentCoordinatorFencesStaleGenerations(t *testing.T) {
	service := newIsolatedService(t, 10)
	service.mu.Lock()
	service.executionGeneration = 2
	service.executionExpiresAt = time.Now().Add(time.Minute)
	legacy := service.lifecycleCoordinator.reserveStageLocked(service, validatedStageRequest(t, stageRequest("legacy", "legacy-script", 1)))
	staleRequest := stageRequest("stale", "stale-script", 1)
	staleRequest.ExecutionGeneration = 1
	staleRequest.ExpectedScriptHash = strings.Repeat("0", 64)
	stale := service.lifecycleCoordinator.reserveStageLocked(service, validatedStageRequest(t, staleRequest))
	service.mu.Unlock()
	if status.Code(legacy.err) != codes.FailedPrecondition || !strings.Contains(legacy.err.Error(), "legacy generation 0 is fenced") {
		t.Fatalf("legacy generation decision = %+v", legacy)
	}
	if status.Code(stale.err) != codes.FailedPrecondition || !strings.Contains(stale.err.Error(), "is not active") {
		t.Fatalf("stale generation decision = %+v", stale)
	}
}

func TestAssignmentStageCommitUsesControlGuardAndExplicitRollback(t *testing.T) {
	service := newIsolatedService(t, 10)
	validated := validatedStageRequest(t, stageRequest("run", "script", 1))
	service.mu.Lock()
	decision := service.lifecycleCoordinator.reserveStageLocked(service, validated)
	service.assignments["other"] = assignedTest{testID: "other", groups: map[string]int32{"group": 1}, vuserCount: 1}
	service.assignments["run"] = assignedTest{testID: "run", groups: map[string]int32{"changed": 1}, vuserCount: 1}
	_, _, err := service.lifecycleCoordinator.commitStageLocked(service, decision.plan, &preparedAssignmentStage{})
	rolledBack := service.lifecycleCoordinator.rollbackStageLocked("run", decision.plan.token)
	_, pending := service.lifecycleCoordinator.pendingStages["run"]
	service.mu.Unlock()
	if status.Code(err) != codes.Aborted {
		t.Fatalf("changed-control commit error = %v, want Aborted", err)
	}
	if !rolledBack || pending {
		t.Fatalf("rollback = %v pending=%v", rolledBack, pending)
	}
}

func TestAssignmentEnginePreparationDoesNotHoldCoordinatorLock(t *testing.T) {
	service := newIsolatedService(t, 10, WithExecution())
	request := stageRequest("run", "script", 1)
	request.ScriptContent = []byte(`{"name":"stage","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"request","type":"request","target":"/"}]}`)
	validated := validatedStageRequest(t, request)
	service.mu.Lock()
	decision := service.lifecycleCoordinator.reserveStageLocked(service, validated)
	service.mu.Unlock()
	if decision.kind != assignmentStagePrepare || decision.plan == nil {
		t.Fatalf("staging decision = %+v", decision)
	}

	type result struct {
		prepared *preparedAssignmentStage
		err      error
	}
	completed := make(chan result, 1)
	service.mu.Lock()
	go func() {
		prepared, err := service.prepareAssignmentStage(context.Background(), decision.plan)
		completed <- result{prepared: prepared, err: err}
	}()
	var prepared *preparedAssignmentStage
	select {
	case outcome := <-completed:
		prepared = outcome.prepared
		if outcome.err != nil {
			service.mu.Unlock()
			t.Fatal(outcome.err)
		}
	case <-time.After(2 * time.Second):
		service.mu.Unlock()
		t.Fatal("engine preparation blocked on the coordinator compatibility lock")
	}
	service.lifecycleCoordinator.rollbackStageLocked("run", decision.plan.token)
	service.mu.Unlock()
	service.cleanupPreparedAssignmentStage(decision.plan, prepared)
}

func TestGeneratorShutdownWaitsForPendingStageRollback(t *testing.T) {
	service := newIsolatedService(t, 10)
	validated := validatedStageRequest(t, stageRequest("run", "script", 1))
	service.mu.Lock()
	decision := service.lifecycleCoordinator.reserveStageLocked(service, validated)
	service.mu.Unlock()
	if decision.kind != assignmentStagePrepare {
		t.Fatalf("staging decision = %+v", decision)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- service.Shutdown(ctx) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown bypassed pending stage: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	service.mu.Lock()
	service.lifecycleCoordinator.rollbackStageLocked("run", decision.plan.token)
	service.mu.Unlock()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not resume after pending stage rollback")
	}
}

func TestConcurrentDuplicateAssignWorkCommitsOnce(t *testing.T) {
	service := newIsolatedService(t, 10)
	request := stageRequest("run", "script", 2)
	const workers = 32
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ack, err := service.AssignWork(context.Background(), request)
			if err == nil && (ack == nil || !ack.GetAccepted()) {
				err = status.Error(codes.Internal, "assignment was rejected")
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	service.mu.RLock()
	assignment := service.assignments["run"]
	pending := len(service.lifecycleCoordinator.pendingStages)
	service.mu.RUnlock()
	if assignment.vuserCount != 2 || len(assignment.groups) != 1 || pending != 0 {
		t.Fatalf("concurrent duplicate result = %+v pending=%d", assignment, pending)
	}
}

func TestConcurrentAssignWorkHonorsCapacityReservation(t *testing.T) {
	service := newIsolatedService(t, 1)
	const workers = 16
	start := make(chan struct{})
	accepted := make(chan bool, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ack, err := service.AssignWork(context.Background(), stageRequest("run-"+string(rune('a'+worker)), "script-"+string(rune('a'+worker)), 1))
			accepted <- err == nil && ack.GetAccepted()
		}()
	}
	close(start)
	wg.Wait()
	close(accepted)
	count := 0
	for value := range accepted {
		if value {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("accepted assignments = %d, want 1", count)
	}
	service.mu.RLock()
	active := service.activeVUsersLocked()
	pending := len(service.lifecycleCoordinator.pendingStages)
	service.mu.RUnlock()
	if active != 1 || pending != 0 {
		t.Fatalf("capacity state = active:%d pending:%d", active, pending)
	}
}

func TestAssignmentCoordinatorStageTimerExpiresUncommittedWork(t *testing.T) {
	service := newIsolatedService(t, 10, WithAssignmentStageTTL(10*time.Millisecond))
	ack, err := service.AssignWork(context.Background(), stageRequest("expiring", "script", 1))
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%#v, %v)", ack, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.RLock()
		_, assigned := service.assignments["expiring"]
		_, timer := service.stageTimers["expiring"]
		service.mu.RUnlock()
		terminal := service.metricsBroker.terminalBatch(terminalBatchKey{testID: "expiring"})
		if !assigned && !timer && terminal != nil {
			if terminal.GetLifecycleState() != "FAILED" || !strings.Contains(terminal.GetLifecycleError(), "expired before commit") {
				t.Fatalf("expired terminal = %+v", terminal)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stage timer did not expire: assigned=%v timer=%v terminal=%+v", assigned, timer, terminal)
		}
		time.Sleep(time.Millisecond)
	}
}
