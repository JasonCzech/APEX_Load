package generatorrpc

import (
	"context"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
)

func TestAssignmentLifecycleTransitionTable(t *testing.T) {
	tests := []struct {
		name    string
		from    assignmentLifecycleState
		event   assignmentLifecycleEvent
		want    assignmentLifecycleState
		wantErr bool
	}{
		{name: "stage fresh assignment", from: assignmentLifecycleStaged, event: assignmentLifecycleStage, want: assignmentLifecycleStaged},
		{name: "commit staged assignment", from: assignmentLifecycleStaged, event: assignmentLifecycleCommit, want: assignmentLifecycleCommitted},
		{name: "start committed assignment", from: assignmentLifecycleCommitted, event: assignmentLifecycleStart, want: assignmentLifecycleRunning},
		{name: "retry commit at barrier", from: assignmentLifecycleCommitted, event: assignmentLifecycleCommit, want: assignmentLifecycleCommitted},
		{name: "stage late group without reopening run", from: assignmentLifecycleRunning, event: assignmentLifecycleStage, want: assignmentLifecycleRunning},
		{name: "commit late group without reopening run", from: assignmentLifecycleRunning, event: assignmentLifecycleCommit, want: assignmentLifecycleRunning},
		{name: "retry start", from: assignmentLifecycleRunning, event: assignmentLifecycleStart, want: assignmentLifecycleRunning},
		{name: "stop staged assignment", from: assignmentLifecycleStaged, event: assignmentLifecycleStop, want: assignmentLifecycleStopping},
		{name: "stop committed assignment", from: assignmentLifecycleCommitted, event: assignmentLifecycleStop, want: assignmentLifecycleStopping},
		{name: "stop running assignment", from: assignmentLifecycleRunning, event: assignmentLifecycleStop, want: assignmentLifecycleStopping},
		{name: "retry stop", from: assignmentLifecycleStopping, event: assignmentLifecycleStop, want: assignmentLifecycleStopping},
		{name: "finish stopping assignment", from: assignmentLifecycleStopping, event: assignmentLifecycleFinish, want: assignmentLifecycleTerminal},
		{name: "retry terminal finish", from: assignmentLifecycleTerminal, event: assignmentLifecycleFinish, want: assignmentLifecycleTerminal},
		{name: "duplicate stop after terminal", from: assignmentLifecycleTerminal, event: assignmentLifecycleStop, want: assignmentLifecycleTerminal},
		{name: "cannot start staged assignment", from: assignmentLifecycleStaged, event: assignmentLifecycleStart, want: assignmentLifecycleStaged, wantErr: true},
		{name: "cannot finish running assignment", from: assignmentLifecycleRunning, event: assignmentLifecycleFinish, want: assignmentLifecycleRunning, wantErr: true},
		{name: "cannot stage stopping assignment", from: assignmentLifecycleStopping, event: assignmentLifecycleStage, want: assignmentLifecycleStopping, wantErr: true},
		{name: "cannot commit terminal assignment", from: assignmentLifecycleTerminal, event: assignmentLifecycleCommit, want: assignmentLifecycleTerminal, wantErr: true},
		{name: "unknown state is never accepted", from: assignmentLifecycleUnknown, event: assignmentLifecycleStage, want: assignmentLifecycleUnknown, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := transitionAssignmentLifecycle(test.from, test.event)
			if (err != nil) != test.wantErr {
				t.Fatalf("transition error = %v, want error %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("transition state = %s, want %s", got, test.want)
			}
		})
	}
}

func TestAssignmentLifecycleCoordinatorPreservesRetryAndReuseBarriers(t *testing.T) {
	coordinator := newAssignmentLifecycleCoordinator()
	assignment, err := coordinator.stage(assignedTest{}, false)
	if err != nil || assignment.lifecycle != assignmentLifecycleStaged || assignment.committed {
		t.Fatalf("stage = (%+v, %v), want uncommitted STAGED", assignment, err)
	}

	assignment, err = coordinator.commit(assignment)
	if err != nil || assignment.lifecycle != assignmentLifecycleRunning || !assignment.committed {
		t.Fatalf("commit = (%+v, %v), want committed RUNNING", assignment, err)
	}

	late, err := coordinator.stage(assignment, true)
	if err != nil || late.lifecycle != assignmentLifecycleRunning || !late.committed {
		t.Fatalf("late stage = (%+v, %v), want committed RUNNING", late, err)
	}

	restaged, err := coordinator.stage(assignment, false)
	if err != nil || restaged.lifecycle != assignmentLifecycleStaged || restaged.committed {
		t.Fatalf("aggregate restage = (%+v, %v), want uncommitted STAGED", restaged, err)
	}

	stopping, completion := coordinator.beginStopping("run", 7, late)
	if stopping.lifecycle != assignmentLifecycleStopping || completion == nil || completion.generation != 7 {
		t.Fatalf("begin stopping = (%+v, %+v)", stopping, completion)
	}
	_, retryCompletion := coordinator.beginStopping("run", 7, stopping)
	if retryCompletion != completion {
		t.Fatal("duplicate stop did not retain the original completion barrier")
	}
	if finished := coordinator.finish(stopping); finished.lifecycle != assignmentLifecycleTerminal {
		t.Fatalf("finished lifecycle = %s, want TERMINAL", finished.lifecycle)
	}

	coordinator.completeStopping("run", 6)
	select {
	case <-completion.done:
		t.Fatal("stale generation released the stopping barrier")
	default:
	}
	if _, stopping := coordinator.stoppingTests["run"]; !stopping {
		t.Fatal("stale generation removed the stopping marker")
	}

	coordinator.completeStopping("run", 7)
	select {
	case <-completion.done:
	default:
		t.Fatal("exact generation did not release the stopping barrier")
	}
	if _, stopping := coordinator.stoppingTests["run"]; stopping {
		t.Fatal("completed assignment retained its stopping marker")
	}
}

func TestAssignmentLifecycleCoordinatorNormalizesLegacyCommittedFixture(t *testing.T) {
	coordinator := newAssignmentLifecycleCoordinator()
	legacy := assignedTest{committed: true}
	if got := coordinator.stateOf(legacy); got != assignmentLifecycleRunning {
		t.Fatalf("legacy lifecycle = %s, want RUNNING", got)
	}
	committed, err := coordinator.commit(legacy)
	if err != nil || committed.lifecycle != assignmentLifecycleRunning || !committed.committed {
		t.Fatalf("legacy commit = (%+v, %v)", committed, err)
	}
}

func TestServiceDelegatesAssignmentLifecycleWithoutChangingWireStates(t *testing.T) {
	service := newIsolatedService(t, 10)
	assignment := &pb.WorkAssignment{
		TestId:        "lifecycle-run",
		GroupName:     "group",
		ScriptId:      "script",
		ScriptContent: []byte(`{"name":"lifecycle"}`),
		VuserCount:    1,
	}
	ack, err := service.AssignWork(context.Background(), assignment)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%+v, %v)", ack, err)
	}
	service.mu.RLock()
	staged := service.assignments[assignment.GetTestId()]
	stagedBatch := service.metricsBatchLocked(assignment.GetTestId())
	service.mu.RUnlock()
	if staged.lifecycle != assignmentLifecycleStaged || stagedBatch.GetLifecycleState() != "STAGED" {
		t.Fatalf("staged lifecycle = %s/%q", staged.lifecycle, stagedBatch.GetLifecycleState())
	}

	commit, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: assignment.GetTestId()})
	if err != nil || !commit.GetAccepted() {
		t.Fatalf("CommitWork = (%+v, %v)", commit, err)
	}
	service.mu.RLock()
	running := service.assignments[assignment.GetTestId()]
	runningBatch := service.metricsBatchLocked(assignment.GetTestId())
	service.mu.RUnlock()
	if running.lifecycle != assignmentLifecycleRunning || runningBatch.GetLifecycleState() != "RUNNING" {
		t.Fatalf("running lifecycle = %s/%q", running.lifecycle, runningBatch.GetLifecycleState())
	}

	stopped, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: assignment.GetTestId()})
	if err != nil || !stopped.GetAcknowledged() {
		t.Fatalf("StopTest = (%+v, %v)", stopped, err)
	}
	service.mu.RLock()
	_, assigned := service.assignments[assignment.GetTestId()]
	_, stopping := service.stoppingTests[assignment.GetTestId()]
	service.mu.RUnlock()
	terminal := service.metricsBroker.terminalBatch(terminalBatchKey{testID: assignment.GetTestId()})
	if assigned || stopping || terminal.GetLifecycleState() != "COMPLETE" {
		t.Fatalf("terminal lifecycle = assigned:%v stopping:%v batch:%+v", assigned, stopping, terminal)
	}
}

func BenchmarkAssignmentLifecycleTransition(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		state, err := transitionAssignmentLifecycle(assignmentLifecycleStaged, assignmentLifecycleCommit)
		if err != nil {
			b.Fatal(err)
		}
		state, err = transitionAssignmentLifecycle(state, assignmentLifecycleStart)
		if err != nil {
			b.Fatal(err)
		}
		if _, err = transitionAssignmentLifecycle(state, assignmentLifecycleStop); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGeneratorMetricsSnapshot(b *testing.B) {
	service := &Service{generatorID: "benchmark-generator"}
	assignment := assignedTest{
		testID:              "benchmark-test",
		lifecycle:           assignmentLifecycleRunning,
		executionGeneration: 42,
		vuserCount:          200,
		committed:           true,
		groups: map[string]int32{
			"checkout": 120,
			"search":   80,
		},
		activeGroups: map[string]int32{
			"checkout": 120,
			"search":   80,
		},
		groupStats: map[string]GeneratorStats{
			"checkout": {ActiveVUsers: 120},
			"search":   {ActiveVUsers: 80},
		},
	}

	b.ReportAllocs()
	for b.Loop() {
		if batch := service.metricsBatchForAssignment(assignment.testID, assignment, "RUNNING", ""); batch.GetStats().GetActiveVusers() != 200 {
			b.Fatalf("active VUsers = %d, want 200", batch.GetStats().GetActiveVusers())
		}
	}
}
