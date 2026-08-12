package generatorrpc

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
)

func TestRuntimeContainmentFailureHardCancelsRenewedRunsAndPersistsExactEvidence(t *testing.T) {
	failure := make(chan struct{})
	service := newIsolatedService(t, 10,
		WithRunSupervisionRequired(time.Minute, 5*time.Second),
		WithRuntimeContainmentFailureSignal(failure),
	)

	type activeRun struct {
		testID   string
		token    []byte
		done     chan struct{}
		canceled chan struct{}
	}
	runs := []activeRun{
		{testID: "run-a", token: supervisionTestToken(21), done: make(chan struct{}), canceled: make(chan struct{})},
		{testID: "run-b", token: supervisionTestToken(22), done: make(chan struct{}), canceled: make(chan struct{})},
	}
	service.mu.Lock()
	for _, run := range runs {
		service.assignments[run.testID] = assignedTest{
			testID:                  run.testID,
			lifecycle:               assignmentLifecycleRunning,
			committed:               true,
			vuserCount:              1,
			runSupervisionTokenHash: runSupervisionTokenHash(run.token),
			groups:                  map[string]int32{"group": 1},
			activeGroups:            map[string]int32{"group": 1},
		}
		canceled := run.canceled
		service.executions[run.testID] = map[string]*groupExecution{
			"group": {
				groupName: "group",
				done:      run.done,
				cancel:    func() { close(canceled) },
			},
		}
	}
	service.mu.Unlock()

	// Give both otherwise-long-running tests fresh controller renewals. The
	// process containment signal must remain an independent, stronger fence.
	for _, run := range runs {
		ack, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
			TestId: run.testID, RunSupervisionToken: run.token, RequestedTtlMilliseconds: time.Minute.Milliseconds(),
		})
		if err != nil || !ack.GetAccepted() || ack.GetExpiresAtUnixMs() <= time.Now().Add(50*time.Second).UnixMilli() {
			t.Fatalf("renew %s = (%+v, %v), want a fresh long lease", run.testID, ack, err)
		}
	}

	close(failure)
	for _, run := range runs {
		select {
		case <-run.canceled:
		case <-time.After(time.Second):
			t.Fatalf("%s was not hard-canceled after containment failure", run.testID)
		}
	}
	select {
	case request := <-service.SelfTerminationRequested():
		if request.TestID != "" || request.Reason != runtimeContainmentFailureReason || request.Outstanding != len(runs) {
			t.Fatalf("generator-level termination request = %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("containment failure did not request process termination")
	}
	if err := service.ReadinessError(); err == nil || !strings.Contains(err.Error(), "draining") {
		t.Fatalf("readiness after containment failure = %v, want draining", err)
	}

	deadline := time.Now().Add(time.Second)
	var evidence []*pb.GeneratorSafetyExitEvidence
	for time.Now().Before(deadline) {
		_, evidence = service.safetyExitEvidenceProto()
		if len(evidence) == len(runs) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(evidence) != len(runs) {
		t.Fatalf("durable containment evidence entries = %d, want %d", len(evidence), len(runs))
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].GetTestId() < evidence[j].GetTestId() })
	for index, entry := range evidence {
		if entry.GetTestId() != runs[index].testID || entry.GetReason() != runtimeContainmentFailureReason ||
			entry.GetOutstandingExecutions() != 1 {
			t.Fatalf("containment evidence[%d] = %+v", index, entry)
		}
	}

	// Release the retained exact barriers so the unit-test service can finalize;
	// production still exits under main's independent two-second watchdog if a
	// context-ignoring execution never closes one of these channels.
	for _, run := range runs {
		close(run.done)
	}
}

func TestRuntimeContainmentEvidenceRejectsGeneratorScopedIdentity(t *testing.T) {
	service := newIsolatedService(t, 10)
	service.runSupervisionIntentMu.Lock()
	emptyErr := service.persistRuntimeContainmentEvidenceLocked(nil)
	service.runSupervisionIntentMu.Unlock()
	if emptyErr != nil {
		t.Fatalf("empty affected-run batch = %v, want no-op", emptyErr)
	}
	err := service.persistRuntimeContainmentEvidence([]SelfTerminationRequest{{
		Reason: runtimeContainmentFailureReason, RequestedAt: time.Now().UTC(),
	}})
	if err == nil || !strings.Contains(err.Error(), "exact test id") {
		t.Fatalf("empty-identity containment evidence error = %v", err)
	}
	_, evidence := service.safetyExitEvidenceProto()
	if len(evidence) != 0 {
		t.Fatalf("empty-identity containment request was persisted: %+v", evidence)
	}
}

func TestRuntimeContainmentEvidenceFencesRestartBeforeShutdownClosesState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "execution-epoch.json")
	failure := make(chan struct{})
	service := NewService("containment-restart-generator", 10,
		WithExecutionEpochStatePath(statePath),
		WithRunSupervisionRequired(time.Minute, 5*time.Second),
		WithRuntimeContainmentFailureSignal(failure),
	)
	const (
		testID     = "renewed-run"
		generation = uint64(7)
		owner      = "controller/original"
	)
	epoch, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: generation, ExecutionLeaseOwnerId: owner,
		LeaseTtlMilliseconds: time.Minute.Milliseconds(),
	})
	if err != nil || !epoch.GetAccepted() {
		t.Fatalf("claim execution epoch = (%+v, %v)", epoch, err)
	}

	token := supervisionTestToken(23)
	done := make(chan struct{})
	service.mu.Lock()
	service.assignments[testID] = assignedTest{
		testID: testID, executionGeneration: generation,
		lifecycle: assignmentLifecycleRunning, committed: true, vuserCount: 1,
		runSupervisionTokenHash: runSupervisionTokenHash(token),
		groups:                  map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
	}
	service.executions[testID] = map[string]*groupExecution{
		"group": {
			groupName: "group", executionGeneration: generation, done: done,
			cancel: func() { close(done) },
		},
	}
	service.mu.Unlock()
	renewed, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: testID, ExecutionGeneration: generation, ExecutionLeaseOwnerId: owner,
		RunSupervisionToken: token, RequestedTtlMilliseconds: time.Minute.Milliseconds(),
	})
	if err != nil || !renewed.GetAccepted() {
		t.Fatalf("renew run supervision = (%+v, %v)", renewed, err)
	}

	atCriticalPoint := make(chan struct{})
	allowPersist := make(chan struct{})
	service.runtimeContainmentBeforePersist = func() {
		close(atCriticalPoint)
		<-allowPersist
	}
	close(failure)
	select {
	case request := <-service.SelfTerminationRequested():
		if request.TestID != "" || request.Reason != runtimeContainmentFailureReason {
			t.Fatalf("process termination request = %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("containment watchdog notification was not published")
	}
	select {
	case <-atCriticalPoint:
	case <-time.After(time.Second):
		t.Fatal("containment handler did not reach the pre-persist critical point")
	}

	shutdownResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownResult <- service.Shutdown(ctx)
	}()
	select {
	case <-service.shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not reach durable-state close")
	}
	select {
	case err := <-shutdownResult:
		t.Fatalf("shutdown bypassed the containment intent lock before persistence: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(allowPersist)
	select {
	case err := <-shutdownResult:
		if err != nil {
			t.Fatalf("shutdown after containment persistence: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not resume after containment evidence persisted")
	}

	restarted := NewService("containment-restart-generator", 10, WithExecutionEpochStatePath(statePath))
	_, evidence := restarted.safetyExitEvidenceProto()
	if len(evidence) != 1 || evidence[0].GetTestId() != testID ||
		evidence[0].GetExecutionGeneration() != generation ||
		evidence[0].GetReason() != runtimeContainmentFailureReason {
		t.Fatalf("restarted containment evidence = %+v", evidence)
	}
	stale, err := restarted.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: generation, ExecutionLeaseOwnerId: owner,
		LeaseTtlMilliseconds: time.Minute.Milliseconds(),
	})
	if err != nil {
		t.Fatalf("reclaim contained generation: %v", err)
	}
	if stale.GetAccepted() || stale.GetErrorMessage() != ExecutionGenerationExpiredMessage {
		t.Fatalf("contained generation was revalidated after restart: %+v", stale)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := restarted.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown restarted service: %v", err)
	}
}
