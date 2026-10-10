package generatorrpc

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRequiredRuntimeFeatureForJavaVuserLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		script *dsl.Script
		want   string
	}{
		{name: "combined action", script: &dsl.Script{Protocol: "java_vuser", Actions: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}}}, want: protocol.FeatureJavaVuserPersistentLifecycleV1},
		{name: "source alias", script: &dsl.Script{Protocol: "java_vuser", Init: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "initialize"}}}}, want: protocol.FeatureJavaVuserPersistentLifecycleV1},
		{name: "nested combined action", script: &dsl.Script{Protocol: "java_vuser", Actions: []dsl.Action{{Type: "loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "run"}}}}}}}, want: protocol.FeatureJavaVuserPersistentLifecycleV1},
		{name: "legacy full lifecycle", script: &dsl.Script{Protocol: "java_vuser", Actions: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "full_lifecycle"}}}}},
		{name: "legacy omitted method", script: &dsl.Script{Protocol: "java_vuser", Actions: []dsl.Action{{Type: "java_vuser_run"}}}},
		{name: "other protocol", script: &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := requiredRuntimeFeature(test.script); got != test.want {
				t.Fatalf("requiredRuntimeFeature() = %q, want %q", got, test.want)
			}
		})
	}
}

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
	request.ScriptContent = []byte(`{"name":"stage","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"request","type":"request","method":"GET","target":"/"}]}`)
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

func TestHTTP3CapabilityAdvertisementAndRequirement(t *testing.T) {
	previous, previousUpgrade := http3ExecutionError, http3UpgradeExecutionError
	t.Cleanup(func() { http3ExecutionError, http3UpgradeExecutionError = previous, previousUpgrade })
	advertised := func(feature string) bool {
		_, features, _ := runtimeAdvertisementWithCapacities(context.Background(), false, nil, nil)
		return slices.Contains(features, feature)
	}
	refuse := func() error { return httpprotocol.ErrHTTP3NotEnabled }
	allow := func() error { return nil }
	http3ExecutionError, http3UpgradeExecutionError = refuse, refuse
	if advertised(dsl.FeatureNativeHTTP3V1) || advertised(dsl.FeatureHTTP3UpgradeV1) {
		t.Fatal("a generator that cannot run HTTP/3 advertised an HTTP/3 capability")
	}
	http3ExecutionError = allow
	if !advertised(dsl.FeatureNativeHTTP3V1) || advertised(dsl.FeatureHTTP3UpgradeV1) {
		t.Fatal("an exact-only generator advertised the wrong capabilities")
	}
	http3UpgradeExecutionError = allow
	if !advertised(dsl.FeatureHTTP3UpgradeV1) {
		t.Fatal("an upgrade-capable generator did not advertise http3_upgrade_v1")
	}
	script := &dsl.Script{Name: "h3", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://app.example.test", HTTP: dsl.HTTPConfig{Version: "3"}},
		Actions: []dsl.Action{{Name: "home", Type: "GET", Target: "/"}}}
	if got := requiredRuntimeFeature(script); !strings.Contains(got, dsl.FeatureNativeHTTP3V1) {
		t.Fatalf("requiredRuntimeFeature = %q, want native_http3_v1", got)
	}
	script.Config.HTTP.Version = "2"
	if got := requiredRuntimeFeature(script); strings.Contains(got, dsl.FeatureNativeHTTP3V1) {
		t.Fatalf("an HTTP/2 script required %q", got)
	}
}

// HTTP/3 support is a generator-process capability: staging admits HTTP/3
// work when this process can run it and names the flag when it cannot.
func TestStagingAdmitsHTTP3WorkOnlyWhenTheGeneratorCanRunIt(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	previous, previousUpgrade := http3ExecutionError, http3UpgradeExecutionError
	t.Cleanup(func() { http3ExecutionError, http3UpgradeExecutionError = previous, previousUpgrade })
	service := newIsolatedService(t, 10, WithExecution())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Shutdown(ctx)
	})
	stage := func(testID, version string) error {
		script := `{"name":"h3","protocol":"http","config":{"base_url":"https://127.0.0.1:1","http":{"version":"` + version + `"}},"actions":[{"name":"home","type":"GET","target":"/"}]}`
		ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{TestId: testID, GroupName: "g", ScriptId: testID, ScriptContent: []byte(script), VuserCount: 1, PrepareOnly: true})
		if err != nil {
			return err
		}
		if !ack.GetAccepted() {
			return status.Error(codes.FailedPrecondition, ack.GetErrorMessage())
		}
		return nil
	}
	t.Setenv(httpprotocol.EnvHTTP3, "true")
	if err := stage("exact", "3"); err != nil {
		t.Fatalf("exact HTTP/3 work was refused on a capable generator: %v", err)
	}
	if err := stage("upgrade", "auto+h3"); err != nil {
		t.Fatalf("auto+h3 work was refused on a capable generator: %v", err)
	}
	http3UpgradeExecutionError = func() error { return httpprotocol.ErrHTTP3NotEnabled }
	if err := stage("upgrade-off", "auto+h3"); err == nil {
		t.Fatal("auto+h3 work was staged on a generator that cannot upgrade")
	}
	http3UpgradeExecutionError = previousUpgrade
	t.Setenv(httpprotocol.EnvHTTP3, "")
	if err := stage("exact-off", "3"); err == nil || !strings.Contains(err.Error(), httpprotocol.EnvHTTP3) {
		t.Fatalf("exact HTTP/3 work on a disabled generator: %v", err)
	}
}
