package generatorrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestAssignWorkStagesNewGroupAfterCommitWithoutReopeningLiveRun(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("late-assignment", 10, WithExecution(), WithAssignmentStageTTL(time.Minute))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	ctx := context.Background()
	first := &pb.WorkAssignment{
		TestId:                "committed-run",
		GroupName:             "group-a",
		ScriptId:              "script",
		ScriptContent:         []byte(`{"name":"demo","protocol":"http","config":{"base_url":"http://127.0.0.1"},"actions":[{"name":"request","type":"GET","target":"/"}]}`),
		VuserCount:            0,
		TargetDurationSeconds: 3600,
	}
	ack, err := service.AssignWork(ctx, first)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("stage first group = (%#v, %v)", ack, err)
	}
	commit, err := service.CommitWork(ctx, &pb.WorkCommitRequest{TestId: first.GetTestId()})
	if err != nil || !commit.GetAccepted() {
		t.Fatalf("commit first group = (%#v, %v)", commit, err)
	}

	// The exact original assignment remains an idempotent success even after
	// commit. A new group is staged independently without moving the running
	// assignment itself back to STAGED.
	retry, err := service.AssignWork(ctx, first)
	if err != nil || !retry.GetAccepted() {
		t.Fatalf("idempotent committed retry = (%#v, %v)", retry, err)
	}
	late, err := service.AssignWork(ctx, &pb.WorkAssignment{
		TestId:                first.GetTestId(),
		GroupName:             "group-b",
		ScriptId:              first.GetScriptId(),
		VuserCount:            0,
		TargetDurationSeconds: 1800,
	})
	if err != nil || !late.GetAccepted() {
		t.Fatalf("stage late group = (%#v, %v)", late, err)
	}

	service.mu.RLock()
	assignment := service.assignments[first.GetTestId()]
	_, hasStageTimer := service.stageTimers[first.GetTestId()]
	firstExecution := service.executions[first.GetTestId()][first.GetGroupName()]
	lateExecution := service.executions[first.GetTestId()]["group-b"]
	service.mu.RUnlock()
	if !assignment.committed || len(assignment.groups) != 2 {
		t.Fatalf("late group reopened the committed assignment: %#v", assignment)
	}
	if firstExecution == nil || !firstExecution.started.Load() {
		t.Fatal("existing group stopped or became staged after late assignment")
	}
	if lateExecution == nil || lateExecution.started.Load() {
		t.Fatal("late group was not held behind the commit boundary")
	}
	if lateExecution.durationOffset != 30*time.Minute || assignment.duration != time.Hour {
		t.Fatalf("late duration state = offset:%v test:%v, want 30m/1h", lateExecution.durationOffset, assignment.duration)
	}
	if !hasStageTimer {
		t.Fatal("late staged group has no expiry timer")
	}

	commit, err = service.CommitWork(ctx, &pb.WorkCommitRequest{TestId: first.GetTestId()})
	if err != nil || !commit.GetAccepted() {
		t.Fatalf("commit late group = (%#v, %v)", commit, err)
	}
	service.mu.RLock()
	assignment = service.assignments[first.GetTestId()]
	_, hasStageTimer = service.stageTimers[first.GetTestId()]
	lateStarted := service.executions[first.GetTestId()]["group-b"].started.Load()
	service.mu.RUnlock()
	if !assignment.committed || !lateStarted || hasStageTimer {
		t.Fatalf("late group commit state = committed:%v started:%v timer:%v", assignment.committed, lateStarted, hasStageTimer)
	}
	service.mu.RLock()
	firstExecution = service.executions[first.GetTestId()][first.GetGroupName()]
	lateExecution = service.executions[first.GetTestId()]["group-b"]
	service.mu.RUnlock()
	for name, execution := range map[string]*groupExecution{"first": firstExecution, "late": lateExecution} {
		select {
		case <-execution.engine.Ready():
		case <-time.After(time.Second):
			t.Fatalf("%s execution did not become ready", name)
		}
	}
	if got := firstExecution.engine.GetBaseTargetDuration(); got != time.Hour {
		t.Fatalf("initial engine duration = %v, want 1h", got)
	}
	if got := lateExecution.engine.GetBaseTargetDuration(); got != 30*time.Minute {
		t.Fatalf("replacement engine duration = %v, want remaining 30m", got)
	}
	adjusted, err := service.AdjustDuration(ctx, &pb.DurationAdjustment{TestId: first.GetTestId(), TargetDurationSeconds: 5400})
	if err != nil || !adjusted.GetAccepted() {
		t.Fatalf("adjust logical duration = (%#v, %v)", adjusted, err)
	}
	if got := firstExecution.engine.GetBaseTargetDuration(); got != 90*time.Minute {
		t.Fatalf("adjusted initial duration = %v, want 90m", got)
	}
	if got := lateExecution.engine.GetBaseTargetDuration(); got != time.Hour {
		t.Fatalf("adjusted replacement duration = %v, want 60m", got)
	}
}

func TestFreshEvacuationTargetRetainsLogicalDurationOffset(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("fresh-evacuation", 10, WithExecution(), WithAssignmentStageTTL(time.Minute))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	assignment := &pb.WorkAssignment{
		TestId:         "evacuated-run",
		GroupName:      "replacement",
		ScriptId:       "script",
		ScriptContent:  []byte(`{"name":"demo","protocol":"http","config":{"base_url":"http://127.0.0.1"},"actions":[{"name":"request","type":"GET","target":"/"}]}`),
		VuserCount:     0,
		TargetDuration: durationpb.New(30 * time.Minute),
		DurationOffset: durationpb.New(30 * time.Minute),
	}
	ack, err := service.AssignWork(context.Background(), assignment)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("stage fresh replacement = (%#v, %v)", ack, err)
	}
	service.mu.RLock()
	logical := service.assignments[assignment.GetTestId()].duration
	execution := service.executions[assignment.GetTestId()][assignment.GetGroupName()]
	service.mu.RUnlock()
	if logical != time.Hour || execution == nil || execution.durationOffset != 30*time.Minute {
		t.Fatalf("fresh replacement duration state = logical:%v execution:%#v, want 1h with 30m offset", logical, execution)
	}
	commitTestWork(t, service, assignment.GetTestId(), 0)
	select {
	case <-execution.engine.Ready():
	case <-time.After(time.Second):
		t.Fatal("fresh replacement did not become ready")
	}
	if got := execution.engine.GetBaseTargetDuration(); got != 30*time.Minute {
		t.Fatalf("fresh replacement local duration = %v, want remaining 30m", got)
	}
	adjusted, err := service.AdjustDuration(context.Background(), &pb.DurationAdjustment{
		TestId: assignment.GetTestId(), TargetDuration: durationpb.New(90 * time.Minute),
	})
	if err != nil || !adjusted.GetAccepted() {
		t.Fatalf("adjust logical duration = (%#v, %v)", adjusted, err)
	}
	if got := execution.engine.GetBaseTargetDuration(); got != time.Hour {
		t.Fatalf("adjusted fresh replacement duration = %v, want 60m", got)
	}
}

func TestFreshEvacuationDurationOffsetValidation(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("invalid-evacuation-offset", 10, WithExecution())
	base := &pb.WorkAssignment{
		TestId: "run", GroupName: "group", ScriptId: "script",
		ScriptContent: []byte(`{"name":"demo","protocol":"http","config":{"base_url":"http://127.0.0.1"}}`),
	}

	missingTarget := proto.Clone(base).(*pb.WorkAssignment)
	missingTarget.DurationOffset = durationpb.New(time.Minute)
	if _, err := service.AssignWork(context.Background(), missingTarget); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("offset without target error = %v, want InvalidArgument", err)
	}

	overflow := proto.Clone(base).(*pb.WorkAssignment)
	overflow.TargetDuration = durationpb.New(time.Duration(1<<63 - 1))
	overflow.DurationOffset = durationpb.New(time.Nanosecond)
	if _, err := service.AssignWork(context.Background(), overflow); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("overflowing logical duration error = %v, want InvalidArgument", err)
	}
}

func TestGeneratorRejectsUncoordinatedDistributedRunGoals(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("run-goal-fencing", 10, WithExecution())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	scriptBytes := func(id string, runLogic dsl.RunLogicConfig) []byte {
		t.Helper()
		content, err := json.Marshal(&dsl.Script{
			ID: id, Name: id, Protocol: "http",
			Config:  dsl.ScriptConfig{BaseURL: "http://127.0.0.1", RunLogic: runLogic},
			Actions: []dsl.Action{{Name: "request", Type: http.MethodGet, Target: "/"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	tests := []struct {
		name       string
		runLogic   dsl.RunLogicConfig
		shardCount uint32
		offset     time.Duration
		want       string
	}{
		{name: "global iterations across shards", runLogic: dsl.RunLogicConfig{GlobalIterations: 10}, shardCount: 2, want: "single shard"},
		{name: "transactions across shards", runLogic: dsl.RunLogicConfig{MaxTransactions: 10}, shardCount: 2, want: "single shard"},
		{name: "per-vuser iterations after evacuation", runLogic: dsl.RunLogicConfig{Iterations: 2}, shardCount: 1, offset: 30 * time.Second, want: "progress checkpoint"},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := fmt.Sprintf("goal-%d", index)
			ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
				TestId: id, GroupName: "group", ScriptId: id, ScriptContent: scriptBytes(id, tt.runLogic),
				VuserCount: 0, TargetDuration: durationpb.New(time.Minute), DurationOffset: preciseDurationProto(tt.offset, 0),
				ShardCount: tt.shardCount,
			})
			if err != nil {
				t.Fatalf("AssignWork returned transport error: %v", err)
			}
			if ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), tt.want) {
				t.Fatalf("AssignWork = %#v, want rejection containing %q", ack, tt.want)
			}
		})
	}
}

func TestGracefulStopDiscardsLateStagedGroupBeforeRunningGroupDrains(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("graceful-late-stage", 10, WithExecution(), WithAssignmentStageTTL(time.Minute))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	first := &pb.WorkAssignment{
		TestId:                "graceful-run",
		GroupName:             "running",
		ScriptId:              "script",
		ScriptContent:         []byte(`{"name":"demo","protocol":"http","config":{"base_url":"http://127.0.0.1"},"actions":[{"name":"request","type":"GET","target":"/"}]}`),
		VuserCount:            0,
		TargetDurationSeconds: 3600,
	}
	ack, err := service.AssignWork(context.Background(), first)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("stage running group = (%#v, %v)", ack, err)
	}
	commitTestWork(t, service, first.GetTestId(), 0)
	ack, err = service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: first.GetTestId(), GroupName: "staged", ScriptId: first.GetScriptId(), VuserCount: 0,
	})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("stage late group = (%#v, %v)", ack, err)
	}

	service.mu.RLock()
	stagedExecution := service.executions[first.GetTestId()]["staged"]
	service.mu.RUnlock()
	stop, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: first.GetTestId(), Graceful: true})
	if err != nil || !stop.GetAcknowledged() {
		t.Fatalf("graceful stop = (%#v, %v)", stop, err)
	}
	select {
	case <-stagedExecution.done:
	default:
		t.Fatal("late staged execution was left without a completion signal")
	}
	service.mu.RLock()
	_, stagedStillTracked := service.executions[first.GetTestId()]["staged"]
	_, hasStageTimer := service.stageTimers[first.GetTestId()]
	service.mu.RUnlock()
	if stagedStillTracked || hasStageTimer {
		t.Fatalf("graceful stop retained staged state: execution=%v timer=%v", stagedStillTracked, hasStageTimer)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.RLock()
		_, assigned := service.assignments[first.GetTestId()]
		_, stopping := service.stoppingTests[first.GetTestId()]
		service.mu.RUnlock()
		if !assigned && !stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("graceful stop did not complete: assigned=%v stopping=%v", assigned, stopping)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLateReplacementInheritsPausedExecutionGate(t *testing.T) {
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	scriptBytes, err := json.Marshal(&dsl.Script{
		Name:     "paused replacement",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL:  target.URL,
			RunLogic: dsl.RunLogicConfig{Iterations: 0},
		},
		Init:    []dsl.Action{{Name: "init", Type: http.MethodGet, Target: "/init"}},
		Actions: []dsl.Action{{Name: "request", Type: http.MethodGet, Target: "/"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("paused-replacement", 10, WithExecution(), WithAssignmentStageTTL(time.Minute))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	first := &pb.WorkAssignment{
		TestId: "paused-run", GroupName: "running", ScriptId: "script", ScriptContent: scriptBytes,
		VuserCount: 1, TargetDurationSeconds: 3600, InitStrategy: "at_once",
	}
	ack, err := service.AssignWork(context.Background(), first)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("stage initial group = (%#v, %v)", ack, err)
	}
	commitTestWork(t, service, first.GetTestId(), 0)
	deadline := time.Now().Add(time.Second)
	for requests.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if requests.Load() == 0 {
		t.Fatal("initial group never generated traffic")
	}
	paused, err := service.SetPaused(context.Background(), &pb.PauseRequest{
		TestId: first.GetTestId(), Pause: true, OperationId: "pause", OperationSequence: 1,
	})
	if err != nil || !paused.GetAccepted() {
		t.Fatalf("pause initial group = (%#v, %v)", paused, err)
	}

	// Let the action already in flight at the soft-pause boundary finish, then
	// establish a stable request count before committing the replacement.
	stable := requests.Load()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		next := requests.Load()
		if next == stable {
			break
		}
		stable = next
	}
	ack, err = service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: first.GetTestId(), GroupName: "replacement", ScriptId: first.GetScriptId(),
		VuserCount: 1, TargetDurationSeconds: 1800, InitStrategy: "at_once",
	})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("stage paused replacement = (%#v, %v)", ack, err)
	}
	commitTestWork(t, service, first.GetTestId(), 0)
	service.mu.RLock()
	replacement := service.executions[first.GetTestId()]["replacement"]
	service.mu.RUnlock()
	select {
	case <-replacement.engine.Ready():
	case <-time.After(time.Second):
		t.Fatal("paused replacement did not become ready")
	}
	time.Sleep(100 * time.Millisecond)
	if got := requests.Load(); got != stable {
		t.Fatalf("paused replacement emitted traffic: requests advanced from %d to %d", stable, got)
	}

	resumed, err := service.SetPaused(context.Background(), &pb.PauseRequest{
		TestId: first.GetTestId(), Pause: false, OperationId: "resume", OperationSequence: 2,
	})
	if err != nil || !resumed.GetAccepted() {
		t.Fatalf("resume replacement = (%#v, %v)", resumed, err)
	}
	deadline = time.Now().Add(time.Second)
	for requests.Load() == stable && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if requests.Load() == stable {
		t.Fatal("resumed replacement never generated traffic")
	}
}

func TestStoppingAssignmentRejectsEveryMutableControl(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("stopping-controls", 10)
	service.assignments["run"] = assignedTest{
		testID:       "run",
		committed:    true,
		vuserCount:   2,
		duration:     time.Minute,
		groups:       map[string]int32{"group": 2},
		activeGroups: map[string]int32{"group": 2},
	}
	service.stoppingTests["run"] = struct{}{}

	assertStopping := func(name string, err error) {
		t.Helper()
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "stopping") {
			t.Fatalf("%s error = %v, want stopping FailedPrecondition", name, err)
		}
	}
	_, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: "run", GroupName: "group", TargetVusers: 5})
	assertStopping("adjust vusers", err)
	_, err = service.AdjustDuration(context.Background(), &pb.DurationAdjustment{TestId: "run", TargetDurationSeconds: 120})
	assertStopping("adjust duration", err)
	_, err = service.SetPaused(context.Background(), &pb.PauseRequest{TestId: "run", Pause: true, OperationId: "pause-1", OperationSequence: 1})
	assertStopping("pause", err)

	service.mu.RLock()
	assignment := service.assignments["run"]
	service.mu.RUnlock()
	if assignment.vuserCount != 2 || assignment.groups["group"] != 2 || assignment.duration != time.Minute || assignment.paused {
		t.Fatalf("stopping assignment was mutated: %#v", assignment)
	}
}

func TestArchiveWriteFailureIsStickyAndFailsActiveAssignment(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	t.Setenv(envGeneratorArchiveMaxBytes, "1")
	service := NewService("archive-failure", 10)
	ctx := context.Background()
	ack, err := service.AssignWork(ctx, &pb.WorkAssignment{
		TestId:                "run",
		GroupName:             "group",
		ScriptId:              "script",
		ScriptContent:         []byte(`{"name":"demo"}`),
		VuserCount:            1,
		TargetDurationSeconds: 3600,
	})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%#v, %v)", ack, err)
	}
	commit, err := service.CommitWork(ctx, &pb.WorkCommitRequest{TestId: "run"})
	if err != nil || !commit.GetAccepted() {
		t.Fatalf("CommitWork = (%#v, %v)", commit, err)
	}

	writer := service.archiveManager.writer("run")
	if writer == nil {
		t.Fatal("assignment did not create an archive writer")
	}
	if err := writer.RecordResult(&protocol.Result{ActionName: "transaction", StartTime: time.Now(), Success: true}); err == nil {
		t.Fatal("tiny archive budget unexpectedly accepted a transaction")
	}
	// A metrics tick observes the sticky sink error and asynchronously fails the
	// assignment; it must not continue an hour of load with incomplete evidence.
	if err := service.PublishMetrics("run", "group", GeneratorStats{ActiveVUsers: 1}, nil, MetricsTelemetry{}); err != nil {
		t.Fatalf("PublishMetrics: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		service.mu.RLock()
		_, assigned := service.assignments["run"]
		_, stopping := service.stoppingTests["run"]
		service.mu.RUnlock()
		if !assigned && !stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive failure did not terminalize assignment: assigned=%v stopping=%v", assigned, stopping)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(archiveRoot, "archive-failure", "run")); !os.IsNotExist(err) {
		t.Fatalf("failed partial archive was retained: %v", err)
	}
}

func TestExecutionTerminalErrorIsRedactedAndBoundedBeforePropagation(t *testing.T) {
	redaction, err := engine.NewRedactionConfig([]engine.RedactionRule{{
		Name: "customer reference", Pattern: `customer-[0-9]+`, Replacement: "[CUSTOMER]",
	}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("terminal-error-redaction", 10)
	service.assignments["run"] = assignedTest{
		testID: "run", groups: map[string]int32{"failed": 1, "sibling": 1},
	}
	service.executions["run"] = map[string]*groupExecution{
		"failed":  {groupName: "failed", redactionConfig: redaction},
		"sibling": {groupName: "sibling"},
	}

	secret := strings.Repeat("a", 64) + "9"
	service.finishExecution("run", "failed", errors.New("password=hunter2 customer-1234 token="+secret+" details="+strings.Repeat("z", maxGeneratorTerminalErrorBytes*2)))

	service.mu.RLock()
	got := service.assignments["run"].terminalError
	service.mu.RUnlock()
	if strings.Contains(got, "hunter2") || strings.Contains(got, "customer-1234") || strings.Contains(got, secret) {
		t.Fatalf("terminal error retained sensitive input: %q", got)
	}
	if !strings.Contains(got, "[CUSTOMER]") {
		t.Fatalf("terminal error did not apply the assignment redaction policy: %q", got)
	}
	if len(got) > maxGeneratorTerminalErrorBytes {
		t.Fatalf("terminal error bytes = %d, limit = %d", len(got), maxGeneratorTerminalErrorBytes)
	}
}

func TestGeneratorControlStreamAdmissionIsBoundedAndReleased(t *testing.T) {
	service := NewService("bounded-control-streams", 10, WithGeneratorControlStreamLimits(2, 1, 1))

	releaseUnary, err := service.beginArchiveTransfer(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.beginArchiveTransfer(true); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second unary archive transfer error = %v, want ResourceExhausted", err)
	}
	releaseStream, err := service.beginArchiveTransfer(false)
	if err != nil {
		t.Fatalf("stream archive transfer inside aggregate limit: %v", err)
	}
	if _, err := service.beginArchiveTransfer(false); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("archive aggregate limit error = %v, want ResourceExhausted", err)
	}
	releaseUnary()
	releaseUnary() // releases are idempotent
	releaseStream()
	if release, err := service.beginArchiveTransfer(true); err != nil {
		t.Fatalf("released archive capacity was not reusable: %v", err)
	} else {
		release()
	}

	releaseHeartbeat, err := service.beginHeartbeatStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.beginHeartbeatStream(); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("heartbeat stream limit error = %v, want ResourceExhausted", err)
	}
	releaseHeartbeat()
	releaseHeartbeat()
	if release, err := service.beginHeartbeatStream(); err != nil {
		t.Fatalf("released heartbeat capacity was not reusable: %v", err)
	} else {
		release()
	}
}

func TestGeneratorBoundaryRejectsOversizedIdentifiersAndMetadata(t *testing.T) {
	service := NewService("bounded-assignment-inputs", 10)
	ctx := context.Background()
	if _, err := service.DistributeScript(ctx, &pb.ScriptPayload{
		ScriptId: strings.Repeat("s", maxGeneratorScriptIDBytes+1), Content: []byte(`{}`),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized script id error = %v, want InvalidArgument", err)
	}
	if _, err := service.AssignWork(ctx, &pb.WorkAssignment{
		TestId: "run", GroupName: strings.Repeat("g", maxGeneratorGroupNameBytes+1), ScriptId: "script",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized group name error = %v, want InvalidArgument", err)
	}
	if _, err := service.AssignWork(ctx, &pb.WorkAssignment{
		TestId: "run", GroupName: "group", ScriptId: "script",
		Environment: map[string]string{"value": strings.Repeat("v", maxAssignmentEnvironmentValueBytes+1)},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized environment error = %v, want InvalidArgument", err)
	}
	rules := make([]*pb.RedactionRule, engine.MaxRedactionRules+1)
	for index := range rules {
		rules[index] = &pb.RedactionRule{Pattern: "safe"}
	}
	if _, err := service.AssignWork(ctx, &pb.WorkAssignment{
		TestId: "run", GroupName: "group", ScriptId: "script", RedactionRules: rules,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversized redaction policy error = %v, want InvalidArgument", err)
	}
	if err := validateArchiveTestID(strings.Repeat("t", maxGeneratorArchiveNamespaceBytes+1)); err == nil {
		t.Fatal("oversized archive test id was accepted")
	}
	if _, err := normalizeGeneratorArchiveArtifactName(strings.Repeat("a", maxGeneratorArchiveArtifactBytes+1)); err == nil {
		t.Fatal("oversized archive artifact name was accepted")
	}
}

func TestWorkAssignmentFingerprintIsDeterministicWithoutProtoMaterialization(t *testing.T) {
	first := &pb.WorkAssignment{
		TestId: "run", GroupName: "group", ScriptId: "script", ScriptContent: []byte("content"),
		Environment:    map[string]string{"b": "2", "a": "1"},
		DataFiles:      []*pb.DataFile{{Name: "data", Content: []byte("rows"), Sha256: "digest"}},
		RedactionRules: []*pb.RedactionRule{{Name: "rule", Pattern: "token", Replacement: "[X]"}},
	}
	second := &pb.WorkAssignment{
		TestId: "run", GroupName: "group", ScriptId: "script", ScriptContent: []byte("content"),
		Environment:    map[string]string{"a": "1", "b": "2"},
		DataFiles:      []*pb.DataFile{{Name: "data", Content: []byte("rows"), Sha256: "digest"}},
		RedactionRules: []*pb.RedactionRule{{Name: "rule", Pattern: "token", Replacement: "[X]"}},
	}
	if left, right := workAssignmentFingerprint(first), workAssignmentFingerprint(second); left != right {
		t.Fatalf("map insertion order changed fingerprint: %x != %x", left, right)
	}
	second.DataFiles[0].Content[0] = 'R'
	if left, right := workAssignmentFingerprint(first), workAssignmentFingerprint(second); left == right {
		t.Fatal("assignment content mutation did not change fingerprint")
	}
}

func TestArchiveRegistrationFailureRemovesFinalizedUntrackedRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	service := NewService("service-namespace", 10)
	writer, err := newArchiveRecorder("foreign-namespace", "run", "Unregistered")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.finalizeArchiveWriter(writer, "COMPLETE", time.Now(), ""); err == nil {
		t.Fatal("foreign finalized archive unexpectedly registered in the service namespace")
	}
	if _, err := os.Stat(filepath.Join(root, "foreign-namespace", "run")); !os.IsNotExist(err) {
		t.Fatalf("unregistered finalized archive remains on disk: %v", err)
	}
}

func TestArchiveFinalizerConcurrencyIsBounded(t *testing.T) {
	service := NewService("archive-finalizer-limit", 10)
	service.archiveManager.setFinalizerSlotsForTest(make(chan struct{}, 1))
	releaseFirst := service.archiveManager.acquireFinalizerSlot()
	acquiredSecond := make(chan func(), 1)
	go func() {
		acquiredSecond <- service.archiveManager.acquireFinalizerSlot()
	}()

	select {
	case release := <-acquiredSecond:
		release()
		t.Fatal("second archive finalizer bypassed the concurrency limit")
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst()
	select {
	case release := <-acquiredSecond:
		release()
	case <-time.After(time.Second):
		t.Fatal("second archive finalizer did not acquire a released slot")
	}
}

func TestNewArchiveRunRemovesCrashOrphanBeforeTestIDReuse(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	namespace, err := prepareGeneratorArchiveNamespace("orphan-reuse", true)
	if err != nil {
		t.Fatal(err)
	}
	defer namespace.close()
	orphanPath := filepath.Join(namespace.path, "run")
	if err := os.Mkdir(orphanPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanPath, "stale-secret.txt"), []byte("must-not-survive"), 0o600); err != nil {
		t.Fatal(err)
	}

	writer, err := newArchiveRecorderInNamespace(namespace, "run", "Replacement")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Discard()
	if _, err := os.Stat(filepath.Join(orphanPath, "stale-secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("crash-orphan artifact survived replacement run creation: %v", err)
	}
}

func TestTransactionWindowBoundsDataDrivenMetricSeries(t *testing.T) {
	window := newTransactionWindowAggregate()
	for index := 0; index < maxTransactionWindowSeries*4; index++ {
		name := fmt.Sprintf("transaction-%d", index)
		target := fmt.Sprintf("dependency-%d", index)
		window.add(TransactionMetric{
			Name: name, Group: name, SOAPOperation: name,
			DependencyType: "http", DependencyTarget: target,
			Success: true, DurationMs: 1,
		})
	}
	for label, count := range map[string]int{
		"transactions": len(window.transactions), "dependencies": len(window.dependencies),
		"soap": len(window.soap), "groups": len(window.groups),
	} {
		if count > maxTransactionWindowSeries {
			t.Fatalf("%s metric series = %d, limit = %d", label, count, maxTransactionWindowSeries)
		}
	}
	if window.transactions[metricOverflowSeries] == nil || window.dependencies[metricOverflowSeries] == nil {
		t.Fatal("overflow metric series was not retained")
	}
}

func TestRetainedTransactionSamplesAreBoundedAcrossGroups(t *testing.T) {
	values := make(map[string][]TransactionMetric)
	for group := 0; group < 16; group++ {
		groupName := fmt.Sprintf("group-%d", group)
		values[groupName] = make([]TransactionMetric, 1024)
		boundRetainedTransactionSamples(values, groupName)
	}

	total := 0
	for _, samples := range values {
		total += len(samples)
	}
	if total > maxRetainedTransactionSamples {
		t.Fatalf("retained transaction samples = %d, limit = %d", total, maxRetainedTransactionSamples)
	}
	if got := len(values["group-15"]); got != 1024 {
		t.Fatalf("newly published group samples = %d, want 1024", got)
	}

	priority := make([]TransactionMetric, maxRetainedTransactionSamples+1)
	priority[len(priority)-1].Name = "newest"
	values["priority"] = priority
	boundRetainedTransactionSamples(values, "priority")
	if got := len(values["priority"]); got != maxRetainedTransactionSamples {
		t.Fatalf("oversized priority samples = %d, want %d", got, maxRetainedTransactionSamples)
	}
	if got := values["priority"][len(values["priority"])-1].Name; got != "newest" {
		t.Fatalf("retained sample tail = %q, want newest", got)
	}
}
