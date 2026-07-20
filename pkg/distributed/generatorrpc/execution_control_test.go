package generatorrpc

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPauseCoordinatorTransitionTable(t *testing.T) {
	tests := []struct {
		name       string
		assignment assignedTest
		operation  string
		sequence   uint64
		paused     bool
		wantPlan   bool
		wantRetry  bool
		wantCode   codes.Code
	}{
		{
			name:       "valid transition",
			assignment: assignedTest{testID: "run", committed: true, groups: map[string]int32{}},
			operation:  "pause-1",
			sequence:   1,
			paused:     true,
			wantPlan:   true,
		},
		{
			name:       "exact duplicate",
			assignment: assignedTest{testID: "run", committed: true, paused: true, pauseOperationID: "pause-1", pauseOperationSequence: 1, groups: map[string]int32{}},
			operation:  "pause-1",
			sequence:   1,
			paused:     true,
			wantRetry:  true,
		},
		{
			name:       "staged assignment",
			assignment: assignedTest{testID: "run", groups: map[string]int32{}},
			operation:  "pause-1",
			sequence:   1,
			paused:     true,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "stale operation",
			assignment: assignedTest{testID: "run", committed: true, paused: true, pauseOperationID: "pause-2", pauseOperationSequence: 2, groups: map[string]int32{}},
			operation:  "pause-1",
			sequence:   1,
			paused:     true,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "sequence reused for different state",
			assignment: assignedTest{testID: "run", committed: true, paused: true, pauseOperationID: "pause-1", pauseOperationSequence: 1, groups: map[string]int32{}},
			operation:  "resume-1",
			sequence:   1,
			paused:     false,
			wantCode:   codes.FailedPrecondition,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newIsolatedService(t, 10)
			service.mu.Lock()
			service.assignments["run"] = test.assignment
			decision := service.lifecycleCoordinator.planPauseLocked(service, "run", test.paused, 0, test.operation, test.sequence)
			if decision.plan != nil {
				service.lifecycleCoordinator.completeControlLocked(decision.plan.control)
			}
			service.mu.Unlock()

			if status.Code(decision.err) != test.wantCode {
				t.Fatalf("transition error = %v (%s), want %s", decision.err, status.Code(decision.err), test.wantCode)
			}
			if got := decision.plan != nil; got != test.wantPlan {
				t.Fatalf("transition plan present = %v, want %v", got, test.wantPlan)
			}
			if decision.idempotent != test.wantRetry {
				t.Fatalf("idempotent = %v, want %v", decision.idempotent, test.wantRetry)
			}
		})
	}
}

func TestCommitCoordinatorTransitionTable(t *testing.T) {
	tests := []struct {
		name       string
		assignment assignedTest
		stopping   bool
		wantKind   commitTransitionKind
		wantCode   codes.Code
	}{
		{
			name:       "staged commits",
			assignment: assignedTest{testID: "run", lifecycle: assignmentLifecycleStaged, groups: map[string]int32{"group": 1}, vuserCount: 1},
			wantKind:   commitTransitionApply,
		},
		{
			name:       "duplicate committed",
			assignment: assignedTest{testID: "run", lifecycle: assignmentLifecycleRunning, committed: true, groups: map[string]int32{"group": 1}, vuserCount: 1},
			wantKind:   commitTransitionAccepted,
		},
		{
			name:       "stopping rejects",
			assignment: assignedTest{testID: "run", lifecycle: assignmentLifecycleStopping, committed: true, groups: map[string]int32{"group": 1}, vuserCount: 1},
			stopping:   true,
			wantCode:   codes.FailedPrecondition,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newIsolatedService(t, 10)
			service.mu.Lock()
			service.assignments["run"] = test.assignment
			if test.stopping {
				service.lifecycleCoordinator.stoppingTests["run"] = struct{}{}
			}
			transition := service.lifecycleCoordinator.commitWorkLocked(service, context.Background(), &pb.WorkCommitRequest{TestId: "run"}, time.Now())
			service.stopCompletionTimerLocked("run")
			service.mu.Unlock()
			if status.Code(transition.err) != test.wantCode {
				t.Fatalf("commit error = %v (%s), want %s", transition.err, status.Code(transition.err), test.wantCode)
			}
			if transition.err == nil && transition.kind != test.wantKind {
				t.Fatalf("commit kind = %v, want %v", transition.kind, test.wantKind)
			}
		})
	}
}

func TestAssignmentControlWaitsForPendingAssignWork(t *testing.T) {
	service := newIsolatedService(t, 10)
	pending := &pendingAssignmentStage{testID: "run", done: make(chan struct{})}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", committed: true, groups: map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1}, vuserCount: 1}
	service.lifecycleCoordinator.pendingStages["run"] = pending
	service.mu.Unlock()

	type result struct {
		ack *pb.PauseAck
		err error
	}
	done := make(chan result, 1)
	go func() {
		ack, err := service.SetPaused(context.Background(), &pb.PauseRequest{
			TestId: "run", Pause: true, OperationId: "pause", OperationSequence: 1,
		})
		done <- result{ack: ack, err: err}
	}()
	select {
	case outcome := <-done:
		t.Fatalf("pause bypassed pending AssignWork: (%#v, %v)", outcome.ack, outcome.err)
	case <-time.After(20 * time.Millisecond):
	}

	service.mu.Lock()
	delete(service.lifecycleCoordinator.pendingStages, "run")
	close(pending.done)
	service.mu.Unlock()
	select {
	case outcome := <-done:
		if outcome.err != nil || !outcome.ack.GetAccepted() || !outcome.ack.GetCurrentPaused() {
			t.Fatalf("pause after pending AssignWork = (%#v, %v)", outcome.ack, outcome.err)
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not resume after pending AssignWork completed")
	}
}

func TestAssignWorkWaitsForPendingCoordinatorControl(t *testing.T) {
	service := newIsolatedService(t, 10)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", committed: true, groups: map[string]int32{"existing": 1},
		activeGroups: map[string]int32{"existing": 1}, vuserCount: 1,
	}
	decision := service.lifecycleCoordinator.planPauseLocked(service, "run", true, 0, "pause", 1)
	service.mu.Unlock()
	if decision.plan == nil {
		t.Fatalf("pause transition = %+v", decision)
	}

	request := stageRequest("run", "late-script", 1)
	request.GroupName = "late"
	type result struct {
		ack *pb.AssignmentAck
		err error
	}
	done := make(chan result, 1)
	go func() {
		ack, err := service.AssignWork(context.Background(), request)
		done <- result{ack: ack, err: err}
	}()
	select {
	case outcome := <-done:
		t.Fatalf("AssignWork bypassed pending control: (%#v, %v)", outcome.ack, outcome.err)
	case <-time.After(20 * time.Millisecond):
	}
	service.abortControl(decision.plan.control)
	select {
	case outcome := <-done:
		if outcome.err != nil || !outcome.ack.GetAccepted() {
			t.Fatalf("AssignWork after control rollback = (%#v, %v)", outcome.ack, outcome.err)
		}
	case <-time.After(time.Second):
		t.Fatal("AssignWork did not resume after control rollback")
	}
}

func TestGeneratorControlsRejectStaleGeneration(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 2, time.Minute); !ack.GetAccepted() {
		t.Fatalf("epoch claim rejected: %s", ack.GetErrorMessage())
	}
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", executionGeneration: 2, committed: true, vuserCount: 1,
		groups: map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
	}
	service.mu.Unlock()

	tests := []struct {
		name   string
		invoke func() error
	}{
		{"commit", func() error {
			_, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "run", ExecutionGeneration: 1})
			return err
		}},
		{"pause", func() error {
			_, err := service.SetPaused(context.Background(), &pb.PauseRequest{TestId: "run", Pause: true, OperationId: "pause", OperationSequence: 1, ExecutionGeneration: 1})
			return err
		}},
		{"scale", func() error {
			_, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: "run", GroupName: "group", TargetVusers: 2, ExecutionGeneration: 1})
			return err
		}},
		{"duration", func() error {
			_, err := service.AdjustDuration(context.Background(), &pb.DurationAdjustment{TestId: "run", TargetDurationSeconds: 2, ExecutionGeneration: 1})
			return err
		}},
		{"stop", func() error {
			_, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run", ExecutionGeneration: 1})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.invoke(); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("stale %s error = %v (%s), want FailedPrecondition", test.name, err, status.Code(err))
			}
		})
	}
}

func TestShutdownWaitsForCoordinatorControlRollback(t *testing.T) {
	service := newIsolatedService(t, 10)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", committed: true, groups: map[string]int32{}}
	decision := service.lifecycleCoordinator.planPauseLocked(service, "run", true, 0, "pause", 1)
	service.mu.Unlock()
	if decision.plan == nil {
		t.Fatalf("pause transition = %+v", decision)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- service.Shutdown(ctx) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown bypassed pending control: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	service.abortControl(decision.plan.control)
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not resume after control rollback")
	}
}

func TestCoordinatorStopFinalizesArchiveAndRetainsTerminalReplay(t *testing.T) {
	service := newIsolatedService(t, 10)
	request := stageRequest("terminal-run", "script", 1)
	request.TargetDurationSeconds = 3600
	assigned, err := service.AssignWork(context.Background(), request)
	if err != nil || !assigned.GetAccepted() {
		t.Fatalf("AssignWork = (%#v, %v)", assigned, err)
	}
	committed, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: request.GetTestId()})
	if err != nil || !committed.GetAccepted() {
		t.Fatalf("CommitWork = (%#v, %v)", committed, err)
	}
	stopped, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: request.GetTestId()})
	if err != nil || !stopped.GetAcknowledged() {
		t.Fatalf("StopTest = (%#v, %v)", stopped, err)
	}
	if !service.archiveManager.retainedTest(request.GetTestId()) {
		t.Fatal("coordinator stop did not retain finalized archive")
	}
	if generation, ok := service.terminalGenerationLocked(request.GetTestId()); !ok || generation != 0 {
		t.Fatalf("terminal replay generation = (%d, %v), want (0, true)", generation, ok)
	}
	duplicate, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: request.GetTestId()})
	if err != nil || !duplicate.GetAcknowledged() {
		t.Fatalf("duplicate terminal StopTest = (%#v, %v)", duplicate, err)
	}
}
