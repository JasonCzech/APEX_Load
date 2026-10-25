package generatorrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type terminalCauseRun struct {
	testID         string
	duration       time.Duration
	durationOffset time.Duration
	iterations     bool
	staged         bool
}

// startTerminalCauseRun stages, supervises and (unless staged) commits one
// real executing run under generation 1 on service. The archive root comes
// from the caller's APEXLOAD_ARCHIVE_DIR.
func startTerminalCauseRun(t *testing.T, service *Service, run terminalCauseRun) []byte {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(target.Close)
	runLogic := ""
	if run.iterations {
		runLogic = `,"run_logic":{"iterations":1}`
	}
	content := []byte(fmt.Sprintf(`{"name":"terminal-cause","protocol":"http","config":{"base_url":%q,"think_time":{"type":"fixed","mean_ms":20}%s},"actions":[{"name":"request","type":"GET","target":"/","think_time_after":true}]}`, target.URL, runLogic))
	scriptID := "script-" + run.testID
	distributeEpochScript(t, service, 1, scriptID, content)
	token := supervisionTestToken(41)
	request := &pb.WorkAssignment{
		TestId: run.testID, GroupName: "group", ScriptId: scriptID, ScriptContent: content,
		ExpectedScriptHash: scriptContentHash(content), VuserCount: 1, ExecutionGeneration: 1,
		RunSupervisionToken: token,
	}
	if run.iterations {
		request.TerminationMode = "iterations"
	} else {
		request.TargetDuration = durationpb.New(run.duration)
	}
	if run.durationOffset > 0 {
		request.DurationOffset = durationpb.New(run.durationOffset)
	}
	if ack, err := service.AssignWork(context.Background(), request); err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%+v, %v)", ack, err)
	}
	claim := &pb.RunSupervisionClaim{TestId: run.testID, ExecutionGeneration: 1, ExecutionLeaseOwnerId: "controller/1", RunSupervisionToken: token, RequestedTtlMilliseconds: 60_000}
	if ack, err := service.ClaimRunSupervision(context.Background(), claim); err != nil || !ack.GetAccepted() {
		t.Fatalf("ClaimRunSupervision = (%+v, %v)", ack, err)
	}
	if run.staged {
		return token
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: run.testID, ExecutionGeneration: 1, RunSupervisionToken: token}); err != nil || !ack.GetAccepted() {
		t.Fatalf("CommitWork = (%+v, %v)", ack, err)
	}
	return token
}

func newTerminalCauseService(t *testing.T, generatorID string) *Service {
	t.Helper()
	service := NewService(generatorID, 10, WithExecution(), WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	if ack := claimEpoch(t, service, 1, time.Minute); !ack.GetAccepted() {
		t.Fatalf("claim generation 1 rejected: %s", ack.GetErrorMessage())
	}
	return service
}

func waitForTerminal(t *testing.T, service *Service, testID string) (string, terminalOutcome) {
	t.Helper()
	var state string
	var outcome terminalOutcome
	waitFor(t, "the terminal outcome of "+testID, 20*time.Second, func() bool {
		var ok bool
		state, outcome, ok = service.metricsBroker.terminalStateOutcome(testID, 1)
		return ok
	})
	return state, outcome
}

// adoptionReport is what a successor controller's epoch claim reads.
func adoptionReport(t *testing.T, service *Service, generation uint64, testID string, token []byte) *pb.RunAdoptionResult {
	t.Helper()
	ack := claimWithAdoptions(t, service, generation, &pb.RunAdoption{TestId: testID, PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: supervisionTestToken(byte(60 + generation))})
	if !ack.GetAccepted() || len(ack.GetAdoptions()) != 1 {
		t.Fatalf("adoption claim = %+v", ack)
	}
	return ack.GetAdoptions()[0]
}

func TestRunTerminalCauseTellsAStopFromAnElapsedDuration(t *testing.T) {
	cases := []struct {
		name     string
		run      terminalCauseRun
		act      func(t *testing.T, service *Service, testID string, token []byte)
		state    string
		cause    pb.RunTerminalCause
		duration time.Duration
	}{
		{
			name: "duration elapsed", run: terminalCauseRun{duration: 300 * time.Millisecond},
			state: "COMPLETE", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, duration: 300 * time.Millisecond,
		},
		{
			name: "controller stop", run: terminalCauseRun{duration: time.Minute},
			act: func(t *testing.T, service *Service, testID string, token []byte) {
				if ack, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: testID, ExecutionGeneration: 1, RunSupervisionToken: token}); err != nil || !ack.GetAcknowledged() {
					t.Fatalf("StopTest = (%+v, %v)", ack, err)
				}
			},
			state: "COMPLETE", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP,
		},
		{
			// A stopped staged replacement must stay distinguishable from one
			// that ran out its duration: it never crossed its start barrier.
			name: "staged stop", run: terminalCauseRun{duration: time.Minute, staged: true},
			act: func(t *testing.T, service *Service, testID string, token []byte) {
				if ack, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: testID, ExecutionGeneration: 1, RunSupervisionToken: token}); err != nil || !ack.GetAcknowledged() {
					t.Fatalf("StopTest = (%+v, %v)", ack, err)
				}
			},
			state: "COMPLETE", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP,
		},
		{
			name: "executions ended early", run: terminalCauseRun{iterations: true},
			state: "COMPLETE", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_EXECUTIONS_ENDED_EARLY,
		},
		{
			name: "failure", run: terminalCauseRun{duration: time.Minute},
			act: func(t *testing.T, service *Service, testID string, _ []byte) {
				service.FailTest(testID, "injected failure")
			},
			state: "FAILED", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_UNSPECIFIED,
		},
		{
			// The logical target is the one the last duration control set.
			name: "adjusted duration", run: terminalCauseRun{duration: time.Minute},
			act: func(t *testing.T, service *Service, testID string, token []byte) {
				ack, err := service.AdjustDuration(context.Background(), &pb.DurationAdjustment{TestId: testID, ExecutionGeneration: 1, TargetDuration: durationpb.New(400 * time.Millisecond), RunSupervisionToken: token})
				if err != nil || !ack.GetAccepted() {
					t.Fatalf("AdjustDuration = (%+v, %v)", ack, err)
				}
			},
			state: "COMPLETE", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, duration: 400 * time.Millisecond,
		},
		{
			// A late replacement's logical duration includes the steady time
			// that elapsed before it was staged, as a duration control's does.
			name: "duration offset", run: terminalCauseRun{duration: 300 * time.Millisecond, durationOffset: 2 * time.Second},
			state: "COMPLETE", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, duration: 2300 * time.Millisecond,
		},
		{
			// A pause stops the logical clock: a pause that outlasts the
			// whole duration does not end the run, which still reports the
			// duration it was given. The duration leaves room for a loaded
			// runner to pause well inside it.
			name: "pause", run: terminalCauseRun{duration: 2 * time.Second},
			act: func(t *testing.T, service *Service, testID string, token []byte) {
				for sequence, pause := range []bool{true, false} {
					if sequence == 1 {
						time.Sleep(2500 * time.Millisecond)
						if state, _, ok := service.metricsBroker.terminalStateOutcome(testID, 1); ok {
							t.Fatalf("the paused run ended %s", state)
						}
					}
					ack, err := service.SetPaused(context.Background(), &pb.PauseRequest{TestId: testID, Pause: pause, ExecutionGeneration: 1, OperationId: "pause", OperationSequence: uint64(sequence + 1), RunSupervisionToken: token})
					if err != nil || !ack.GetAccepted() {
						t.Fatalf("SetPaused(%v) = (%+v, %v)", pause, ack, err)
					}
				}
			},
			state: "COMPLETE", cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, duration: 2 * time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
			service := newTerminalCauseService(t, "terminal-cause")
			tc.run.testID = "run"
			token := startTerminalCauseRun(t, service, tc.run)
			if tc.act != nil {
				tc.act(t, service, tc.run.testID, token)
			}
			state, outcome := waitForTerminal(t, service, tc.run.testID)
			if state != tc.state || outcome.cause != tc.cause || outcome.duration != tc.duration {
				t.Fatalf("terminal = %s %s %s, want %s %s %s", state, outcome.cause, outcome.duration, tc.state, tc.cause, tc.duration)
			}
			// A successor controller reads the same outcome from its adoption.
			report := adoptionReport(t, service, 2, tc.run.testID, token)
			wantOutcome := pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
			if tc.state != "COMPLETE" {
				wantOutcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED
			}
			reported := time.Duration(0)
			if report.GetCompletedLogicalDuration() != nil {
				reported = report.GetCompletedLogicalDuration().AsDuration()
			}
			if report.GetOutcome() != wantOutcome || report.GetTerminalState() != tc.state || report.GetTerminalCause() != tc.cause || reported != tc.duration {
				t.Fatalf("adoption report = %+v, want %s %s %s %s", report, wantOutcome, tc.state, tc.cause, tc.duration)
			}
		})
	}
}

// The synthetic completion timer (no engine execution) and a controller stop
// both remove the assignment under s.mu: whichever does so first decides the
// cause, and the other changes nothing.
func TestRunTerminalCauseStopRacingTheTimer(t *testing.T) {
	commit := func(t *testing.T, service *Service, testID string) uint64 {
		t.Helper()
		request := stageRequest(testID, "script", 1)
		request.TargetDurationSeconds = 3600
		if ack, err := service.AssignWork(context.Background(), request); err != nil || !ack.GetAccepted() {
			t.Fatalf("AssignWork = (%+v, %v)", ack, err)
		}
		if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: testID}); err != nil || !ack.GetAccepted() {
			t.Fatalf("CommitWork = (%+v, %v)", ack, err)
		}
		service.mu.RLock()
		defer service.mu.RUnlock()
		token, armed := service.completionTokens[testID]
		if !armed {
			t.Fatal("the completion timer was not armed")
		}
		return token
	}
	// stop reports whether this stop removed the assignment: only then does
	// its acknowledgement count the assignment's VUsers as pending.
	stop := func(t *testing.T, service *Service, testID string) bool {
		t.Helper()
		ack, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: testID})
		if err != nil || !ack.GetAcknowledged() {
			t.Errorf("StopTest = (%+v, %v)", ack, err)
		}
		return ack.GetPendingVusers() > 0
	}
	outcome := func(t *testing.T, service *Service, testID string) terminalOutcome {
		t.Helper()
		state, outcome, ok := service.metricsBroker.terminalStateOutcome(testID, 0)
		if !ok || state != "COMPLETE" {
			t.Fatalf("terminal = (%s, %v), want COMPLETE", state, ok)
		}
		return outcome
	}
	elapsed := terminalOutcome{cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, duration: time.Hour}
	stopped := terminalOutcome{cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP}

	t.Run("stop first", func(t *testing.T) {
		service := newIsolatedService(t, 10)
		token := commit(t, service, "run")
		if !stop(t, service, "run") {
			t.Fatal("the first stop did not remove the assignment")
		}
		service.completeAssignmentForTimer("run", token)
		if got := outcome(t, service, "run"); got != stopped {
			t.Fatalf("outcome = %+v, want %+v", got, stopped)
		}
	})
	t.Run("timer first", func(t *testing.T) {
		service := newIsolatedService(t, 10)
		token := commit(t, service, "run")
		service.completeAssignmentForTimer("run", token)
		if stop(t, service, "run") {
			t.Fatal("a stop after the timer removed an assignment")
		}
		if got := outcome(t, service, "run"); got != elapsed {
			t.Fatalf("outcome = %+v, want %+v", got, elapsed)
		}
	})
	t.Run("paused timer", func(t *testing.T) {
		service := newIsolatedService(t, 10)
		token := commit(t, service, "run")
		service.mu.Lock()
		assignment := service.assignments["run"]
		assignment.paused = true
		service.assignments["run"] = assignment
		service.mu.Unlock()
		service.completeAssignmentForTimer("run", token)
		if got := outcome(t, service, "run"); got != (terminalOutcome{}) {
			t.Fatalf("outcome of a timer that ran through a pause = %+v, want unspecified", got)
		}
	})
	t.Run("unowned completion", func(t *testing.T) {
		service := newIsolatedService(t, 10)
		commit(t, service, "run")
		service.completeAssignment("run")
		if got := outcome(t, service, "run"); got != (terminalOutcome{}) {
			t.Fatalf("outcome of a completion without the timer = %+v, want unspecified", got)
		}
	})
	t.Run("concurrent", func(t *testing.T) {
		service := newIsolatedService(t, 1000)
		for index := range 40 {
			testID := fmt.Sprintf("run-%d", index)
			token := commit(t, service, testID)
			var wg sync.WaitGroup
			var stopWon bool
			wg.Add(2)
			go func() { defer wg.Done(); stopWon = stop(t, service, testID) }()
			go func() { defer wg.Done(); service.completeAssignmentForTimer(testID, token) }()
			wg.Wait()
			want := elapsed
			if stopWon {
				want = stopped
			}
			if got := outcome(t, service, testID); got != want {
				t.Fatalf("%s outcome = %+v, want %+v (stop won: %v)", testID, got, want, stopWon)
			}
		}
	})
}

// The outcome survives a generator restart through the archive manifest, and
// stays out of the manifest's embedded terminal batch.
func TestRunTerminalCauseSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	const generatorID = "terminal-cause-restart"
	service := newTerminalCauseService(t, generatorID)
	startTerminalCauseRun(t, service, terminalCauseRun{testID: "elapsed", duration: 300 * time.Millisecond})
	stoppedToken := startTerminalCauseRun(t, service, terminalCauseRun{testID: "stopped", duration: time.Minute})
	if ack, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "stopped", ExecutionGeneration: 1, RunSupervisionToken: stoppedToken}); err != nil || !ack.GetAcknowledged() {
		t.Fatalf("StopTest = (%+v, %v)", ack, err)
	}
	waitForTerminal(t, service, "elapsed")
	waitForTerminal(t, service, "stopped")
	waitFor(t, "both archives", 10*time.Second, func() bool {
		return service.archiveManager.retainedTest("elapsed") && service.archiveManager.retainedTest("stopped")
	})

	data, err := os.ReadFile(filepath.Join(root, generatorID, "elapsed", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest archiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.TerminalCause != "RUN_TERMINAL_CAUSE_DURATION_ELAPSED" || manifest.CompletedLogicalDuration != 300*time.Millisecond {
		t.Fatalf("manifest outcome = %q %s", manifest.TerminalCause, manifest.CompletedLogicalDuration)
	}
	if strings.Contains(string(manifest.TerminalMetrics), "terminal_cause") || strings.Contains(string(manifest.TerminalMetrics), "logical_duration") {
		t.Fatalf("the embedded terminal batch carries the outcome: %s", manifest.TerminalMetrics)
	}
	// A release that predates the outcome decodes the manifest with plain
	// encoding/json, which ignores the fields it does not know.
	var older struct {
		TestID          string          `json:"test_id"`
		Status          string          `json:"status"`
		CompletedAt     time.Time       `json:"completed_at"`
		TerminalMetrics json.RawMessage `json:"terminal_metrics"`
	}
	if err := json.Unmarshal(data, &older); err != nil || older.TestID != "elapsed" || older.Status != "COMPLETE" {
		t.Fatalf("older manifest decode = (%+v, %v)", older, err)
	}

	restarted := NewService(generatorID, 10, WithGeneratorStateLimits(10, 10, 24*time.Hour))
	for testID, want := range map[string]terminalOutcome{
		"elapsed": {cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, duration: 300 * time.Millisecond},
		"stopped": {cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP},
	} {
		state, got, ok := restarted.metricsBroker.terminalStateOutcome(testID, 1)
		if !ok || state != "COMPLETE" || got != want {
			t.Fatalf("%s after restart = (%s %+v %v), want COMPLETE %+v", testID, state, got, ok, want)
		}
	}
}

// An archive finalized by an older generator, and any manifest whose outcome
// cannot be trusted, restores an unspecified cause.
func TestManifestTerminalOutcomeDefaultsToUnspecified(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	const generatorID = "terminal-cause-older"
	writer, err := newArchiveRecorder(generatorID, "older-run", "Older")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Finalize("COMPLETE", time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	service := NewService(generatorID, 10, WithGeneratorStateLimits(10, 10, 24*time.Hour))
	if state, got, ok := service.metricsBroker.terminalStateOutcome("older-run", 0); !ok || state != "COMPLETE" || got != (terminalOutcome{}) {
		t.Fatalf("older archive = (%s %+v %v), want COMPLETE with no outcome", state, got, ok)
	}

	for name, manifest := range map[string]archiveManifest{
		"no cause":            {Status: "COMPLETE"},
		"unknown cause":       {Status: "COMPLETE", TerminalCause: "RUN_TERMINAL_CAUSE_FROM_THE_FUTURE"},
		"failed run":          {Status: "FAILED", TerminalCause: "RUN_TERMINAL_CAUSE_DURATION_ELAPSED", CompletedLogicalDuration: time.Second},
		"elapsed, no length":  {Status: "COMPLETE", TerminalCause: "RUN_TERMINAL_CAUSE_DURATION_ELAPSED"},
		"negative length":     {Status: "COMPLETE", TerminalCause: "RUN_TERMINAL_CAUSE_DURATION_ELAPSED", CompletedLogicalDuration: -time.Second},
		"unspecified by name": {Status: "COMPLETE", TerminalCause: "RUN_TERMINAL_CAUSE_UNSPECIFIED"},
	} {
		if got := manifestTerminalOutcome(manifest); got != (terminalOutcome{}) {
			t.Errorf("%s: outcome = %+v, want unspecified", name, got)
		}
	}
	stop := archiveManifest{Status: "COMPLETE", TerminalCause: "RUN_TERMINAL_CAUSE_CONTROLLER_STOP", CompletedLogicalDuration: time.Second}
	if got := manifestTerminalOutcome(stop); got != (terminalOutcome{cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP}) {
		t.Errorf("controller stop outcome = %+v", got)
	}
}

// Only a COMPLETE transition reports a cause, and an elapsed duration with no
// recorded length is not trusted.
func TestAssignmentTerminalOutcome(t *testing.T) {
	elapsed := assignedTest{terminalCause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, deadlineReached: true, deadlineDuration: time.Minute}
	if got := assignmentTerminalOutcome(elapsed, "COMPLETE"); got != (terminalOutcome{cause: elapsed.terminalCause, duration: time.Minute}) {
		t.Fatalf("elapsed outcome = %+v", got)
	}
	if got := assignmentTerminalOutcome(elapsed, "FAILED"); got != (terminalOutcome{}) {
		t.Fatalf("failed outcome = %+v, want none", got)
	}
	unmeasured := assignedTest{terminalCause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED}
	if got := assignmentTerminalOutcome(unmeasured, "COMPLETE"); got.cause != pb.RunTerminalCause_RUN_TERMINAL_CAUSE_EXECUTIONS_ENDED_EARLY {
		t.Fatalf("unmeasured elapsed outcome = %+v, want ended early", got)
	}
	// The shortest group's logical duration is reported, and one group that
	// ended early makes the whole assignment early.
	var assignment assignedTest
	foldExecutionDeadline(&assignment, time.Minute, true)
	foldExecutionDeadline(&assignment, 55*time.Second, true)
	foldExecutionDeadline(&assignment, 2*time.Minute, true)
	if executionsTerminalCause(assignment) != pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED || assignment.deadlineDuration != 55*time.Second {
		t.Fatalf("groups that all reached their deadline = %s at %s, want elapsed at 55s", executionsTerminalCause(assignment), assignment.deadlineDuration)
	}
	recordExecutionDeadline(&assignment, &groupExecution{durationOffset: time.Second})
	if executionsTerminalCause(assignment) != pb.RunTerminalCause_RUN_TERMINAL_CAUSE_EXECUTIONS_ENDED_EARLY {
		t.Fatal("a group without an engine deadline did not end the assignment early")
	}
}

// A failed archive finalization ends the run FAILED, without the cause the
// transition recorded.
func TestFailedArchiveFinalizationDropsTheTerminalCause(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("terminal-cause-archive-failure", 10)
	namespace, err := service.archiveManager.namespaceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	writer, err := newArchiveRecorderInNamespace(namespace, "run", "Run")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.txFile.Close(); err != nil {
		t.Fatal(err)
	}
	assignment := assignedTest{testID: "run", groups: map[string]int32{"group": 1}, terminalCause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, deadlineReached: true, deadlineDuration: time.Minute}
	state, _ := service.finishStoppedTestWithState("run", assignment, writer, "COMPLETE", "")
	retained, outcome, ok := service.metricsBroker.terminalStateOutcome("run", 0)
	if state != "FAILED" || !ok || retained != "FAILED" || outcome != (terminalOutcome{}) {
		t.Fatalf("terminal after a failed finalization = %s / %s %+v %v, want FAILED with no outcome", state, retained, outcome, ok)
	}
}
