package generatorrpc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSetPausedRejectsStaleOperationAfterCompensation(t *testing.T) {
	service := NewService("pause-ordering", 10)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", committed: true, groups: map[string]int32{}}
	service.mu.Unlock()

	compensation, err := service.SetPaused(context.Background(), &pb.PauseRequest{
		TestId: "run", Pause: true, OperationId: "compensation", OperationSequence: 2,
	})
	if err != nil || !compensation.GetAccepted() || !compensation.GetCurrentPaused() {
		t.Fatalf("compensation SetPaused = (%#v, %v)", compensation, err)
	}
	if _, err := service.SetPaused(context.Background(), &pb.PauseRequest{
		TestId: "run", Pause: false, OperationId: "late-forward", OperationSequence: 1,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("late forward error = %v, want FailedPrecondition", err)
	}
	service.mu.RLock()
	assignment := service.assignments["run"]
	service.mu.RUnlock()
	if !assignment.paused || assignment.pauseOperationSequence != 2 || assignment.pauseOperationID != "compensation" {
		t.Fatalf("assignment after stale operation = %+v", assignment)
	}
}

func TestSetPausedDoesNotApplyCanceledRequest(t *testing.T) {
	service := NewService("pause-context", 10)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", groups: map[string]int32{}}
	service.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.SetPaused(ctx, &pb.PauseRequest{
		TestId: "run", Pause: true, OperationId: "canceled", OperationSequence: 1,
	}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled SetPaused error = %v, want Canceled", err)
	}
	service.mu.RLock()
	assignment := service.assignments["run"]
	service.mu.RUnlock()
	if assignment.paused || assignment.pauseOperationSequence != 0 {
		t.Fatalf("canceled operation mutated assignment = %+v", assignment)
	}
}

func TestGeneratorRejectsDurationOverflow(t *testing.T) {
	service := NewService("duration-overflow", 10)
	_, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "overflow", GroupName: "group", ScriptId: "script", VuserCount: 1,
		TargetDurationSeconds: maxGeneratorDurationSeconds + 1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("AssignWork error = %v, want InvalidArgument", err)
	}
	service.mu.Lock()
	service.assignments["adjust-overflow"] = assignedTest{testID: "adjust-overflow"}
	service.mu.Unlock()
	_, err = service.AdjustDuration(context.Background(), &pb.DurationAdjustment{TestId: "adjust-overflow", TargetDurationSeconds: maxGeneratorDurationSeconds + 1})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("AdjustDuration error = %v, want InvalidArgument", err)
	}
}

func TestGeneratorCapacityAdmissionCannotOverflowInt32(t *testing.T) {
	service := NewService("capacity-overflow", 10)
	content := []byte(`{"name":"capacity"}`)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script", Content: content}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	service.mu.Lock()
	service.assignments["active"] = assignedTest{
		testID:       "active",
		committed:    true,
		vuserCount:   10,
		groups:       map[string]int32{"a": 5, "b": 5},
		activeGroups: map[string]int32{"a": 5, "b": 5},
	}
	service.mu.Unlock()

	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "overflow", GroupName: "group", ScriptId: "script", VuserCount: int32(maxGeneratorVUsers),
	})
	if err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if ack.GetAccepted() {
		t.Fatal("int32-overflowing assignment bypassed generator capacity")
	}
	if _, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{
		TestId: "active", GroupName: "a", TargetVusers: int32(maxGeneratorVUsers),
	}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("overflowing AdjustVUsers error = %v (%s), want ResourceExhausted", err, status.Code(err))
	}
	service.mu.RLock()
	_, overflowAssigned := service.assignments["overflow"]
	active := service.assignments["active"]
	service.mu.RUnlock()
	if overflowAssigned || active.vuserCount != 10 || active.groups["a"] != 5 {
		t.Fatalf("overflow admission mutated state: overflow=%v active=%+v", overflowAssigned, active)
	}
}

func TestWaitForGeneratorExecutionsHonorsCompletionAndCancellation(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		close(done)
	}()
	if err := waitForGeneratorExecutions(context.Background(), []*groupExecution{{done: done}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForGeneratorExecutions(ctx, []*groupExecution{{done: make(chan struct{})}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context canceled", err)
	}
}

func TestExecutionDoneWaitsForPublisherAndTerminalCallbacks(t *testing.T) {
	service := NewService("callback-barrier", 10)
	publisherDone := make(chan struct{})
	done := make(chan struct{})
	canceled := make(chan struct{})
	var cancelOnce sync.Once
	exec := &groupExecution{
		groupName:           "group",
		executionGeneration: 7,
		cancel: func() {
			cancelOnce.Do(func() { close(canceled) })
		},
		publisherDone: publisherDone,
		done:          done,
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID:              "run",
		executionGeneration: 7,
		vuserCount:          1,
		groups:              map[string]int32{"group": 1},
		activeGroups:        map[string]int32{"group": 1},
	}
	service.executions["run"] = map[string]*groupExecution{"group": exec}
	service.mu.Unlock()

	go service.completeExecutionCallbacks("run", "group", exec, nil)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("execution completion did not cancel its periodic publisher")
	}
	select {
	case <-done:
		t.Fatal("execution signaled done while its publisher callback could still be in flight")
	default:
	}
	service.mu.RLock()
	_, stillAssigned := service.assignments["run"]
	service.mu.RUnlock()
	if !stillAssigned {
		t.Fatal("terminal callback ran before the periodic publisher joined")
	}

	close(publisherDone)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("execution did not signal done after all callbacks completed")
	}
	service.mu.RLock()
	_, stillAssigned = service.assignments["run"]
	service.mu.RUnlock()
	if stillAssigned {
		t.Fatal("terminal callback did not retire the completed assignment")
	}
}

func TestStaleExecutionCallbacksCannotMutateReusedTestAndGroupID(t *testing.T) {
	service := NewService("callback-identity", 10)
	oldExec := &groupExecution{groupName: "group", executionGeneration: 11}
	newExec := &groupExecution{groupName: "group", executionGeneration: 11}
	service.mu.Lock()
	service.assignments["reused"] = assignedTest{
		testID:              "reused",
		executionGeneration: 11,
		vuserCount:          4,
		groups:              map[string]int32{"group": 4},
		activeGroups:        map[string]int32{"group": 4},
	}
	service.executions["reused"] = map[string]*groupExecution{"group": newExec}
	service.mu.Unlock()

	err := service.publishMetricsForExecution(
		"reused",
		"group",
		oldExec,
		GeneratorStats{ActiveVUsers: 999},
		nil,
		MetricsTelemetry{},
		metricsAggregateSet{},
	)
	if status.Code(err) != codes.Aborted {
		t.Fatalf("stale publisher error = %v (%s), want Aborted", err, status.Code(err))
	}
	service.finishExecutionInstance("reused", "group", oldExec, errors.New("stale execution failed"))

	service.mu.RLock()
	assignment, assigned := service.assignments["reused"]
	currentExec := service.executions["reused"]["group"]
	service.mu.RUnlock()
	if !assigned || currentExec != newExec {
		t.Fatalf("stale terminal callback retired replacement: assigned=%v current=%p want=%p", assigned, currentExec, newExec)
	}
	if assignment.vuserCount != 4 || assignment.terminalError != "" || len(assignment.groupStats) != 0 {
		t.Fatalf("stale callbacks mutated replacement assignment: %#v", assignment)
	}
}

func TestGeneratorShutdownStopsAssignmentsAndReleasesStreams(t *testing.T) {
	service := NewService("shutdown", 10)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", vuserCount: 1, groups: map[string]int32{"group": 1}}
	service.mu.Unlock()
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := service.ActiveVUsers(); got != 0 {
		t.Fatalf("active VUsers after shutdown = %d", got)
	}
	select {
	case <-service.shutdown:
	default:
		t.Fatal("shutdown signal was not closed")
	}
}

func TestGeneratorShutdownCancelsEveryAssignmentBeforeBoundedWait(t *testing.T) {
	service := NewService("shutdown-fanout", 100)
	const count = maxExecutionFenceWorkers*2 + 3
	blockedDone := make(chan struct{})
	canceled := make(chan string, count)
	service.mu.Lock()
	for index := range count {
		testID := fmt.Sprintf("run-%02d", index)
		service.assignments[testID] = assignedTest{testID: testID, vuserCount: 1, groups: map[string]int32{"group": 1}}
		id := testID
		service.executions[testID] = map[string]*groupExecution{
			"group": {groupName: "group", cancel: func() { canceled <- id }, done: blockedDone},
		}
	}
	service.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	err := service.Shutdown(ctx)
	cancel()
	if err == nil {
		t.Fatal("shutdown with blocked executions unexpectedly completed")
	}
	seen := make(map[string]struct{}, count)
	for range count {
		select {
		case testID := <-canceled:
			seen[testID] = struct{}{}
		default:
			t.Fatalf("only %d/%d assignments were canceled before the bounded wait", len(seen), count)
		}
	}
	service.mu.RLock()
	assignmentCount := len(service.assignments)
	executionCount := len(service.executions)
	service.mu.RUnlock()
	if len(seen) != count || assignmentCount != 0 || executionCount != 0 {
		t.Fatalf("shutdown fanout = canceled:%d assignments:%d executions:%d, want %d/0/0", len(seen), assignmentCount, executionCount, count)
	}
	close(blockedDone)
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		pending := service.executionFencePending
		service.mu.RUnlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("shutdown background cleanup still has %d pending tests", pending)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDrainingGeneratorRejectsWorkGrowthButAllowsReduction(t *testing.T) {
	service := NewService("draining-controls", 10)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID:          "run",
		committed:       true,
		vuserCount:      5,
		durationSeconds: 100,
		groups:          map[string]int32{"group": 5},
		activeGroups:    map[string]int32{"group": 5},
	}
	service.mu.Unlock()
	service.SetDraining(true)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "late", Content: []byte(`{"name":"late"}`)}); status.Code(err) != codes.Unavailable {
		t.Fatalf("script distribution while draining error = %v (%s), want Unavailable", err, status.Code(err))
	}
	if err := service.ReadinessError(); err == nil || !strings.Contains(err.Error(), "draining") {
		t.Fatalf("draining readiness error = %v, want draining", err)
	}

	if _, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: "run", GroupName: "group", TargetVusers: 6}); status.Code(err) != codes.Unavailable {
		t.Fatalf("scale-up while draining error = %v (%s), want Unavailable", err, status.Code(err))
	}
	if ack, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: "run", GroupName: "group", TargetVusers: 4}); err != nil || !ack.GetAccepted() {
		t.Fatalf("scale-down while draining = (%#v, %v)", ack, err)
	}
	if _, err := service.AdjustDuration(context.Background(), &pb.DurationAdjustment{TestId: "run", TargetDurationSeconds: 101}); status.Code(err) != codes.Unavailable {
		t.Fatalf("duration extension while draining error = %v (%s), want Unavailable", err, status.Code(err))
	}
	if ack, err := service.AdjustDuration(context.Background(), &pb.DurationAdjustment{TestId: "run", TargetDurationSeconds: 90}); err != nil || !ack.GetAccepted() {
		t.Fatalf("duration reduction while draining = (%#v, %v)", ack, err)
	}
}

func TestCanceledGeneratorMutationsDoNotCommit(t *testing.T) {
	service := NewService("canceled-controls", 10)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID:          "run",
		committed:       true,
		vuserCount:      3,
		durationSeconds: 60,
		groups:          map[string]int32{"group": 3},
		activeGroups:    map[string]int32{"group": 3},
	}
	service.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.DistributeScript(ctx, &pb.ScriptPayload{ScriptId: "late", Content: []byte(`{"name":"late"}`)}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled DistributeScript error = %v (%s), want Canceled", err, status.Code(err))
	}
	if _, err := service.AssignWork(ctx, &pb.WorkAssignment{TestId: "late", GroupName: "group", ScriptId: "late", VuserCount: 1}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled AssignWork error = %v (%s), want Canceled", err, status.Code(err))
	}
	if _, err := service.AdjustVUsers(ctx, &pb.VUserAdjustment{TestId: "run", GroupName: "group", TargetVusers: 9}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled AdjustVUsers error = %v (%s), want Canceled", err, status.Code(err))
	}
	if _, err := service.AdjustDuration(ctx, &pb.DurationAdjustment{TestId: "run", TargetDurationSeconds: 600}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled AdjustDuration error = %v (%s), want Canceled", err, status.Code(err))
	}
	if _, err := service.StopTest(ctx, &pb.StopRequest{TestId: "run"}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled StopTest error = %v (%s), want Canceled", err, status.Code(err))
	}
	service.mu.RLock()
	assignment := service.assignments["run"]
	_, cached := service.scripts["late"]
	_, lateAssigned := service.assignments["late"]
	service.mu.RUnlock()
	if cached || lateAssigned || assignment.vuserCount != 3 || assignment.durationSeconds != 60 {
		t.Fatalf("canceled mutations changed state: cached=%v late_assigned=%v assignment=%#v", cached, lateAssigned, assignment)
	}
}

func TestRejectedGroupAssignmentRestoresExistingDuration(t *testing.T) {
	service := NewService("assignment-rollback", 10, WithExecution())
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "invalid", Content: []byte("not-json")}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID:           "run",
		vuserCount:       1,
		durationSeconds:  0,
		groups:           map[string]int32{"existing": 1},
		activeGroups:     map[string]int32{"existing": 1},
		groupAssignments: make(map[string][32]byte),
		groupScripts:     map[string]string{"existing": "existing-script"},
	}
	service.mu.Unlock()

	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "run", GroupName: "rejected", ScriptId: "invalid", VuserCount: 1, TargetDurationSeconds: 120,
	})
	if err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if ack.GetAccepted() {
		t.Fatalf("invalid group assignment unexpectedly accepted: %#v", ack)
	}
	service.mu.RLock()
	assignment := service.assignments["run"]
	service.mu.RUnlock()
	if assignment.durationSeconds != 0 || assignment.vuserCount != 1 {
		t.Fatalf("existing assignment changed after rejection: %+v", assignment)
	}
	if _, exists := assignment.groups["rejected"]; exists {
		t.Fatal("rejected group remained in the existing assignment")
	}
}

func TestArchiveAdmissionFailureRestoresExistingDuration(t *testing.T) {
	root := t.TempDir()
	notDirectory := filepath.Join(root, "archive-file")
	if err := os.WriteFile(notDirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", notDirectory)
	service := NewService("archive-assignment-rollback", 10, WithExecutionEpochStatePath(filepath.Join(root, "epoch.json")))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script", Content: []byte(`{"name":"script"}`)}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID:           "run",
		vuserCount:       1,
		groups:           map[string]int32{"existing": 1},
		activeGroups:     map[string]int32{"existing": 1},
		groupAssignments: make(map[string][32]byte),
		groupScripts:     map[string]string{"existing": "existing-script"},
	}
	service.mu.Unlock()

	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "run", GroupName: "rejected", ScriptId: "script", VuserCount: 1, TargetDurationSeconds: 120,
	})
	if err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if ack.GetAccepted() {
		t.Fatalf("archive-less group assignment unexpectedly accepted: %#v", ack)
	}
	service.mu.RLock()
	assignment := service.assignments["run"]
	service.mu.RUnlock()
	if assignment.durationSeconds != 0 || assignment.vuserCount != 1 {
		t.Fatalf("existing assignment changed after archive rejection: %+v", assignment)
	}
}

func TestReadinessRejectsUnavailableArchiveNamespace(t *testing.T) {
	root := t.TempDir()
	notDirectory := filepath.Join(root, "archive-file")
	if err := os.WriteFile(notDirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", notDirectory)
	service := NewService("archive-readiness", 10, WithExecutionEpochStatePath(filepath.Join(root, "epoch.json")))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	if err := service.ReadinessError(); err == nil || !strings.Contains(err.Error(), "archive") {
		t.Fatalf("readiness error = %v, want unavailable archive namespace", err)
	}
}

func TestAdjustDurationRollsBackEarlierEnginesWhenParticipantRejects(t *testing.T) {
	script := &dsl.Script{Name: "duration", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1"}}
	newEngine := func() *engine.MultiGroupEngine {
		mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
			Groups:   []engine.GroupConfig{{Name: "group", Script: script, VUsers: 0, AllowZeroVUsers: true}},
			Duration: time.Minute,
		})
		if err != nil {
			t.Fatalf("NewMultiGroup: %v", err)
		}
		return mge
	}
	running := newEngine()
	stopped := newEngine()
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		_, _ = running.Run(runCtx)
		close(runDone)
	}()
	select {
	case <-running.Ready():
	case <-time.After(time.Second):
		t.Fatal("running duration participant did not become ready")
	}
	defer func() {
		cancelRun()
		<-runDone
	}()

	service := NewService("duration-rollback", 10, WithExecution())
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", committed: true, durationSeconds: 60, groups: map[string]int32{"a": 0, "z": 0}}
	service.executions["run"] = map[string]*groupExecution{
		"a": {groupName: "a", engine: running},
		"z": {groupName: "z", engine: stopped},
	}
	service.executions["run"]["a"].started.Store(true)
	service.executions["run"]["z"].started.Store(true)
	service.mu.Unlock()
	if _, err := service.AdjustDuration(context.Background(), &pb.DurationAdjustment{TestId: "run", TargetDurationSeconds: 120}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("partial duration adjustment error = %v (%s), want FailedPrecondition", err, status.Code(err))
	}
	if got := running.GetBaseTargetDuration(); got != time.Minute {
		t.Fatalf("earlier engine duration after rollback = %v, want %v", got, time.Minute)
	}
	service.mu.RLock()
	stored := service.assignments["run"].durationSeconds
	service.mu.RUnlock()
	if stored != 60 {
		t.Fatalf("stored duration after rejected adjustment = %d, want 60", stored)
	}
}

func TestAdjustVUsersRollsBackStaleEngineAfterGenerationChanges(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 1, time.Second); !ack.GetAccepted() {
		t.Fatalf("initial epoch claim rejected: %s", ack.GetErrorMessage())
	}
	script := &dsl.Script{Name: "scale", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1"}}
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups:   []engine.GroupConfig{{Name: "group", Script: script, VUsers: 1}},
		Duration: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	detachedDone := make(chan struct{})
	close(detachedDone)
	service.mu.Lock()
	service.executionEnabled = true
	service.assignments["run"] = assignedTest{
		testID:              "run",
		committed:           true,
		executionGeneration: 1,
		vuserCount:          1,
		groups:              map[string]int32{"group": 1},
		activeGroups:        map[string]int32{"group": 1},
	}
	service.executions["run"] = map[string]*groupExecution{
		"group": {groupName: "group", executionGeneration: 1, engine: mge, cancel: func() {}, done: detachedDone},
	}
	service.executions["run"]["group"].started.Store(true)
	service.mu.Unlock()

	type adjustmentResult struct {
		ack *pb.AdjustmentAck
		err error
	}
	result := make(chan adjustmentResult, 1)
	go func() {
		ack, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{
			TestId: "run", GroupName: "group", TargetVusers: 2, ExecutionGeneration: 1,
		})
		result <- adjustmentResult{ack: ack, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		reserved := service.assignments["run"].groups["group"] == 2
		service.mu.RUnlock()
		if reserved {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("AdjustVUsers did not reserve capacity before waiting for engine readiness")
		}
		time.Sleep(time.Millisecond)
	}
	if ack := claimEpoch(t, service, 2, time.Second); !ack.GetAccepted() {
		t.Fatalf("replacement epoch claim rejected: %s", ack.GetErrorMessage())
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		_, _ = mge.Run(runCtx)
		close(runDone)
	}()
	select {
	case got := <-result:
		if status.Code(got.err) != codes.FailedPrecondition {
			t.Fatalf("stale adjustment = (%#v, %v), want FailedPrecondition", got.ack, got.err)
		}
	case <-time.After(2 * time.Second):
		cancelRun()
		t.Fatal("stale adjustment did not finish after engine became ready")
	}
	if got := mge.GetGroupVUsers()["group"]; got != 1 {
		cancelRun()
		t.Fatalf("stale engine VUsers after rejected adjustment = %d, want restored 1", got)
	}
	cancelRun()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("stale test engine did not stop")
	}
}

func TestStaleCompletionTimerCannotFinishReusedTestID(t *testing.T) {
	service := NewService("timer-ownership", 10)
	const testID = "reused-run"

	service.mu.Lock()
	service.assignments[testID] = assignedTest{
		testID:              testID,
		executionGeneration: 1,
		vuserCount:          1,
		groups:              map[string]int32{"old": 1},
	}
	service.scheduleCompletionLocked(testID, time.Hour)
	staleToken := service.completionTokens[testID]
	service.stopCompletionTimerLocked(testID)
	service.assignments[testID] = assignedTest{
		testID:              testID,
		executionGeneration: 2,
		vuserCount:          2,
		groups:              map[string]int32{"replacement": 2},
	}
	service.scheduleCompletionLocked(testID, time.Hour)
	currentToken := service.completionTokens[testID]
	currentTimer := service.completionTimers[testID]
	service.mu.Unlock()

	// Simulate a callback that was already queued when Stop returned. The old
	// timer must not be able to complete an assignment created under the same ID.
	service.completeAssignmentForTimer(testID, staleToken)

	service.mu.Lock()
	replacement, exists := service.assignments[testID]
	if !exists || replacement.executionGeneration != 2 || replacement.vuserCount != 2 {
		service.mu.Unlock()
		t.Fatalf("replacement assignment after stale timer = (%+v, %v)", replacement, exists)
	}
	if got := service.completionTokens[testID]; got != currentToken || currentToken == staleToken {
		service.mu.Unlock()
		t.Fatalf("completion token after stale timer = %d, want current %d distinct from stale %d", got, currentToken, staleToken)
	}
	if got := service.completionTimers[testID]; got != currentTimer {
		service.mu.Unlock()
		t.Fatal("stale timer replaced or removed the current completion timer")
	}
	service.stopCompletionTimerLocked(testID)
	delete(service.assignments, testID)
	service.mu.Unlock()
}

func TestConfinedArchiveArtifactPath(t *testing.T) {
	base := "/var/lib/apexload/archives/test-1"
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"nested ok", "results/page-1.json", false},
		{"simple ok", "summary.json", false},
		{"parent traversal", "../../etc/passwd", true},
		{"absolute", "/etc/passwd", true},
		{"backslash", "results\\..\\..\\etc", true},
		{"nul byte", "results/page\x00.json", true},
		{"bare dotdot", "..", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := confinedArchiveArtifactPath(base, tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got path %q", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if got == "" {
				t.Fatalf("expected a confined path for %q", tc.input)
			}
		})
	}
}

func TestServiceAssignWorkRejectsNegativeVUserCount(t *testing.T) {
	service := NewService("generator-1", 50)
	_, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:     "test-1",
		GroupName:  "Group A",
		ScriptId:   "script-1",
		VuserCount: -5,
	})
	if err == nil {
		t.Fatal("expected error for negative vuser_count")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", status.Code(err))
	}
}

func TestServiceAssignWorkIsIdempotentForSameGroup(t *testing.T) {
	service := NewService("generator-1", 10)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script-1", Content: []byte(`{"name":"demo"}`)}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	assignment := &pb.WorkAssignment{TestId: "test-1", GroupName: "group-1", ScriptId: "script-1", VuserCount: 6}
	for attempt := 0; attempt < 2; attempt++ {
		ack, err := service.AssignWork(context.Background(), assignment)
		if err != nil {
			t.Fatalf("AssignWork attempt %d: %v", attempt+1, err)
		}
		if !ack.GetAccepted() {
			t.Fatalf("AssignWork attempt %d rejected: %s", attempt+1, ack.GetErrorMessage())
		}
	}
	if got := service.ActiveVUsers(); got != 6 {
		t.Fatalf("active VUsers after duplicate assignment = %d, want 6", got)
	}
	conflict := &pb.WorkAssignment{TestId: "test-1", GroupName: "group-1", ScriptId: "script-1", VuserCount: 6, RampUpSeconds: 30}
	ack, err := service.AssignWork(context.Background(), conflict)
	if err != nil {
		t.Fatalf("conflicting AssignWork returned transport error: %v", err)
	}
	if ack.GetAccepted() {
		t.Fatal("same-count assignment with different execution settings was accepted as an idempotent retry")
	}
}

func TestServiceConcurrentAdjustVUsersCannotExceedCapacity(t *testing.T) {
	service := NewService("generator-1", 10)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script-1", Content: []byte(`{"name":"demo"}`)}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	for _, testID := range []string{"test-a", "test-b"} {
		ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{TestId: testID, GroupName: "group", ScriptId: "script-1", VuserCount: 2})
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("AssignWork %s: ack=%#v err=%v", testID, ack, err)
		}
		commitTestWork(t, service, testID, 0)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, testID := range []string{"test-a", "test-b"} {
		testID := testID
		go func() {
			<-start
			_, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: testID, GroupName: "group", TargetVusers: 7})
			errs <- err
		}()
	}
	close(start)
	var rejected int
	for range 2 {
		if err := <-errs; err != nil {
			if status.Code(err) != codes.ResourceExhausted {
				t.Fatalf("AdjustVUsers error = %v, want ResourceExhausted", err)
			}
			rejected++
		}
	}
	if rejected != 1 {
		t.Fatalf("rejected adjustments = %d, want 1", rejected)
	}
	if got := service.ActiveVUsers(); got > 10 {
		t.Fatalf("active VUsers = %d, exceeds max 10", got)
	}
}

func TestServiceAdjustVUsersDoesNotFenceFutureAssignments(t *testing.T) {
	service := NewService("generator-adjust-fence", 10)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script-1", Content: []byte(`{"name":"demo"}`)}); err != nil {
		t.Fatal(err)
	}
	first, err := service.AssignWork(context.Background(), &pb.WorkAssignment{TestId: "test", GroupName: "group-a", ScriptId: "script-1", VuserCount: 1})
	if err != nil || !first.GetAccepted() {
		t.Fatalf("initial assignment: ack=%v err=%v", first, err)
	}
	commitTestWork(t, service, "test", 0)
	if _, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: "test", GroupName: "group-a", TargetVusers: 2}); err != nil {
		t.Fatalf("AdjustVUsers: %v", err)
	}
	second, err := service.AssignWork(context.Background(), &pb.WorkAssignment{TestId: "test", GroupName: "group-b", ScriptId: "script-1", VuserCount: 1})
	if err != nil || !second.GetAccepted() {
		t.Fatalf("assignment after scale was fenced: ack=%v err=%v", second, err)
	}
	commitTestWork(t, service, "test", 0)
}

func TestServiceSerializesSameGroupEngineAdjustments(t *testing.T) {
	mge, err := engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups: []engine.GroupConfig{{
			Name: "group",
			Script: &dsl.Script{
				Name:     "waiting-engine",
				Protocol: "http",
				Config:   dsl.ScriptConfig{BaseURL: "http://127.0.0.1"},
				Actions:  []dsl.Action{{Name: "request", Type: "GET", Target: "/"}},
			},
			VUsers: 1,
		}},
		Duration: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	service := NewService("generator-1", 10, WithExecution())
	service.assignments["test"] = assignedTest{
		testID:       "test",
		committed:    true,
		vuserCount:   1,
		groups:       map[string]int32{"group": 1},
		activeGroups: map[string]int32{"group": 1},
	}
	service.executions["test"] = map[string]*groupExecution{"group": {groupName: "group", engine: mge}}
	service.executions["test"]["group"].started.Store(true)

	ctxFirst, cancelFirst := context.WithCancel(context.Background())
	ctxSecond, cancelSecond := context.WithCancel(context.Background())
	defer cancelFirst()
	defer cancelSecond()
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, err := service.AdjustVUsers(ctxFirst, &pb.VUserAdjustment{TestId: "test", GroupName: "group", TargetVusers: 3})
		firstDone <- err
	}()
	waitForAssignedVUsers(t, service, "test", "group", 3)
	go func() {
		_, err := service.AdjustVUsers(ctxSecond, &pb.VUserAdjustment{TestId: "test", GroupName: "group", TargetVusers: 4})
		secondDone <- err
	}()

	// The first request is blocked on engine readiness. The second must not be
	// allowed to reserve/scale the same group until the first sequence exits.
	time.Sleep(25 * time.Millisecond)
	service.mu.RLock()
	got := service.assignments["test"].groups["group"]
	service.mu.RUnlock()
	if got != 3 {
		t.Fatalf("second adjustment overtook first: reserved VUsers = %d, want 3", got)
	}

	cancelFirst()
	if err := <-firstDone; status.Code(err) != codes.Canceled {
		t.Fatalf("first adjustment error = %v, want Canceled", err)
	}
	waitForAssignedVUsers(t, service, "test", "group", 4)
	cancelSecond()
	if err := <-secondDone; status.Code(err) != codes.Canceled {
		t.Fatalf("second adjustment error = %v, want Canceled", err)
	}
}

func waitForAssignedVUsers(t *testing.T, service *Service, testID string, group string, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		service.mu.RLock()
		got := service.assignments[testID].groups[group]
		service.mu.RUnlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("group %s did not reserve %d VUsers", group, want)
}

func TestValidateArchiveTestIDRejectsTraversal(t *testing.T) {
	for _, testID := range []string{"../escape", "a/b", `a\b`, "..", "/absolute"} {
		if err := validateArchiveTestID(testID); err == nil {
			t.Errorf("validateArchiveTestID(%q) unexpectedly succeeded", testID)
		}
	}
	if err := validateArchiveTestID("run-1234"); err != nil {
		t.Fatalf("safe test id rejected: %v", err)
	}
}

func TestFirstPositiveDurationOnExistingAssignmentStartsCompletionTimer(t *testing.T) {
	t.Setenv(envGeneratorArchiveMinFree, "1")
	service := NewService("late-duration-timer", 10)
	content := []byte(`{"name":"timer","protocol":"http","config":{"base_url":"http://127.0.0.1"}}`)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script", Content: content}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	first, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "run", GroupName: "first", ScriptId: "script", VuserCount: 1,
	})
	if err != nil || !first.GetAccepted() {
		t.Fatalf("initial indefinite assignment = (%#v, %v)", first, err)
	}
	service.mu.RLock()
	_, timerBefore := service.completionTimers["run"]
	service.mu.RUnlock()
	if timerBefore {
		t.Fatal("zero-duration assignment unexpectedly installed a completion timer")
	}
	second, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "run", GroupName: "second", ScriptId: "script", VuserCount: 1, TargetDurationSeconds: 3600,
	})
	if err != nil || !second.GetAccepted() {
		t.Fatalf("positive-duration group assignment = (%#v, %v)", second, err)
	}
	commitTestWork(t, service, "run", 0)
	service.mu.RLock()
	assignment := service.assignments["run"]
	timer := service.completionTimers["run"]
	token := service.completionTokens["run"]
	service.mu.RUnlock()
	if assignment.durationSeconds != 3600 || timer == nil || token == 0 {
		t.Fatalf("late duration state = duration:%d timer:%v token:%d", assignment.durationSeconds, timer != nil, token)
	}
	if _, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run"}); err != nil {
		t.Fatalf("StopTest cleanup: %v", err)
	}
}

func TestServiceExecutionFailureCancelsSiblingsAndRetainsCapacityUntilTheyExit(t *testing.T) {
	service := NewService("generator-1", 10, WithExecution())
	siblingCanceled := make(chan struct{})
	var cancelOnce sync.Once
	service.assignments["test-1"] = assignedTest{
		testID:           "test-1",
		vuserCount:       10,
		groups:           map[string]int32{"failing": 4, "sibling": 6},
		activeGroups:     map[string]int32{"failing": 4, "sibling": 6},
		groupAssignments: make(map[string][32]byte),
	}
	service.executions["test-1"] = map[string]*groupExecution{
		"failing": {groupName: "failing"},
		"sibling": {
			groupName: "sibling",
			cancel: func() {
				cancelOnce.Do(func() { close(siblingCanceled) })
			},
		},
	}

	service.finishExecution("test-1", "failing", fmt.Errorf("engine failed"))

	select {
	case <-siblingCanceled:
	case <-time.After(time.Second):
		t.Fatal("sibling execution was not canceled after group failure")
	}
	if got := service.ActiveVUsers(); got != 10 {
		t.Fatalf("active VUsers while sibling unwinds = %d, want 10", got)
	}
	service.mu.RLock()
	assignment, assignmentExists := service.assignments["test-1"]
	_, siblingExists := service.executions["test-1"]["sibling"]
	service.mu.RUnlock()
	if !assignmentExists || !siblingExists {
		t.Fatal("assignment was released before all sibling engines exited")
	}
	if assignment.terminalError != "engine failed" {
		t.Fatalf("terminal error = %q, want first execution error", assignment.terminalError)
	}
	service.mu.RLock()
	_, stopping := service.stoppingTests["test-1"]
	service.mu.RUnlock()
	if !stopping {
		t.Fatal("failed execution did not install a terminal assignment fence")
	}
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "test-1", GroupName: "replacement", ScriptId: "script", VuserCount: 1,
	})
	if err != nil {
		t.Fatalf("AssignWork while sibling unwinds: %v", err)
	}
	if ack.GetAccepted() || !strings.Contains(strings.ToLower(ack.GetErrorMessage()), "stopping") {
		t.Fatalf("replacement assignment ack = %#v, want stopping rejection", ack)
	}

	service.finishExecution("test-1", "sibling", context.Canceled)
	if got := service.ActiveVUsers(); got != 0 {
		t.Fatalf("active VUsers after all executions exit = %d, want 0", got)
	}
}

// TestServiceHeartbeatReadPathIsRaceFree exercises the Heartbeat read path
// (collectResources + the locked capacity reads) concurrently with the
// AssignWork/StopTest writers. Before the fix the heartbeat read s.assignments
// and the shared resCollector without synchronization. Run with -race.
func TestServiceHeartbeatReadPathIsRaceFree(t *testing.T) {
	service := NewService("generator-1", 1000)

	done := make(chan struct{})
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			id := fmt.Sprintf("t-%d", i%4)
			service.mu.Lock()
			service.assignments[id] = assignedTest{vuserCount: int32(i % 10)}
			delete(service.assignments, fmt.Sprintf("t-%d", (i+1)%4))
			service.mu.Unlock()
		}
	}()

	var readerWG sync.WaitGroup
	for r := 0; r < 4; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for i := 0; i < 400; i++ {
				_ = service.collectResources()
				service.mu.RLock()
				_ = service.activeVUsersLocked()
				_ = service.availableCapacityLocked()
				service.mu.RUnlock()
				_ = service.activeTestIDs()
			}
		}()
	}

	readerWG.Wait()
	close(done)
	writerWG.Wait()
}
