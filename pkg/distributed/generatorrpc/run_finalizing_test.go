package generatorrpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// olderControllerReading decodes report with a RunAdoptionResult schema
// that has no field 7, as a controller that predates the finalizing field
// does, and returns the outcome it reads and whether field 7 stayed unknown.
func olderControllerReading(t *testing.T, report *pb.RunAdoptionResult) (pb.RunAdoptionOutcome, bool) {
	t.Helper()
	file := protodesc.ToFileDescriptorProto(pb.File_api_proto_generator_proto)
	found := false
	for _, message := range file.GetMessageType() {
		if message.GetName() != "RunAdoptionResult" {
			continue
		}
		message.Field = slices.DeleteFunc(message.Field, func(field *descriptorpb.FieldDescriptorProto) bool { return field.GetNumber() == 7 })
		found = true
	}
	if !found {
		t.Fatal("RunAdoptionResult is missing from the generator schema")
	}
	older, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := older.Messages().ByName("RunAdoptionResult")
	if descriptor.Fields().ByNumber(7) != nil {
		t.Fatal("the older schema still has field 7")
	}
	wire, err := proto.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	message := dynamicpb.NewMessage(descriptor)
	if err := proto.Unmarshal(wire, message); err != nil {
		t.Fatal(err)
	}
	outcome := pb.RunAdoptionOutcome(message.Get(descriptor.Fields().ByName("outcome")).Enum())
	return outcome, len(message.GetUnknown()) > 0
}

// holdArchiveFinalizers occupies every archive finalizer slot, so a run that
// reaches archive finalization waits there, inside the window between its
// assignment's removal and its terminal tombstone. The returned function
// releases them once.
func holdArchiveFinalizers(t *testing.T, service *Service) func() {
	t.Helper()
	slots := cap(service.archiveManager.finalizerSlots)
	if slots == 0 {
		t.Fatal("the archive manager has no finalizer slots")
	}
	releases := make([]func(), 0, slots)
	for range slots {
		releases = append(releases, service.archiveManager.acquireFinalizerSlot())
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			for _, release := range releases {
				release()
			}
		})
	}
	t.Cleanup(release)
	return release
}

func runFinalizing(service *Service, testID string) bool {
	service.mu.RLock()
	defer service.mu.RUnlock()
	_, finalizing := service.finalizingRuns[testID]
	return finalizing
}

// assertNothingExecutes checks the guarantee a finalizing answer carries: the
// assignment is gone and no execution of the run is left.
func assertNothingExecutes(t *testing.T, service *Service, testID string) {
	t.Helper()
	service.mu.RLock()
	defer service.mu.RUnlock()
	if _, assigned := service.assignments[testID]; assigned {
		t.Fatal("a finalizing run still has its assignment")
	}
	if executions := service.executions[testID]; len(executions) > 0 {
		t.Fatalf("a finalizing run still has %d executions", len(executions))
	}
}

func finalizingAdoption(testID string, token []byte, generation uint64) *pb.RunAdoption {
	return &pb.RunAdoption{TestId: testID, PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: supervisionTestToken(byte(60 + generation))}
}

func soleAdoption(t *testing.T, ack *pb.ExecutionEpochAck) *pb.RunAdoptionResult {
	t.Helper()
	if !ack.GetAccepted() || len(ack.GetAdoptions()) != 1 {
		t.Fatalf("adoption claim = %+v", ack)
	}
	return ack.GetAdoptions()[0]
}

// A successor's claim that arrives while a finished run finalizes its archive
// is told so, with the earlier outcome kept for older controllers; a claim of
// the same generation after the tombstone reads the real outcome.
func TestAdoptionReportsARunStillFinalizing(t *testing.T) {
	cases := []struct {
		name string
		run  terminalCauseRun
		// end ends the run; it may block until the archive is finalized.
		end      func(service *Service, testID string, token []byte)
		outcome  pb.RunAdoptionOutcome
		state    string
		cause    pb.RunTerminalCause
		duration time.Duration
	}{
		{
			name: "duration elapsed", run: terminalCauseRun{duration: 300 * time.Millisecond},
			outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED, state: "COMPLETE",
			cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, duration: 300 * time.Millisecond,
		},
		{
			name: "controller stop", run: terminalCauseRun{duration: time.Minute},
			end: func(service *Service, testID string, token []byte) {
				_, _ = service.StopTest(context.Background(), &pb.StopRequest{TestId: testID, ExecutionGeneration: 1, RunSupervisionToken: token})
			},
			outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED, state: "COMPLETE",
			cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP,
		},
		{
			name: "failure", run: terminalCauseRun{duration: time.Minute},
			end: func(service *Service, testID string, _ []byte) {
				service.FailTest(testID, "injected failure")
			},
			outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, state: "FAILED",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
			service := newTerminalCauseService(t, "finalizing")
			release := holdArchiveFinalizers(t, service)
			tc.run.testID = "run"
			token := startTerminalCauseRun(t, service, tc.run)
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				if tc.end != nil {
					tc.end(service, tc.run.testID, token)
				}
			}()
			waitFor(t, "the run to finalize", 20*time.Second, func() bool { return runFinalizing(service, tc.run.testID) })
			assertNothingExecutes(t, service, tc.run.testID)
			if _, _, terminal := service.metricsBroker.terminalStateOutcome(tc.run.testID, 1); terminal {
				t.Fatal("the terminal outcome was published while the archive finalizer was held")
			}

			report := soleAdoption(t, claimWithAdoptions(t, service, 2, finalizingAdoption(tc.run.testID, token, 2)))
			if !report.GetFinalizing() || report.GetTerminalState() != "" || report.GetTerminalCause() != pb.RunTerminalCause_RUN_TERMINAL_CAUSE_UNSPECIFIED {
				t.Fatalf("report while finalizing = %+v", report)
			}
			// Every case ran supervised, so its supervision tombstone gives
			// the earlier answer: ALREADY_STOPPED.
			if outcome := report.GetOutcome(); outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED {
				t.Fatalf("a finalizing run changed its earlier outcome to %s", outcome)
			}
			// An older controller reads the same outcome, with the flag left
			// as an unknown field, and holds the run as it always did.
			if outcome, unknown := olderControllerReading(t, report); outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED || !unknown {
				t.Fatalf("an older controller reads %s (flag kept as unknown field: %v)", outcome, unknown)
			}
			// A claim of the same generation, as a successor re-queries with,
			// reads the same.
			if again := soleAdoption(t, claimWithAdoptions(t, service, 2, finalizingAdoption(tc.run.testID, token, 2))); !again.GetFinalizing() || again.GetOutcome() != report.GetOutcome() {
				t.Fatalf("same-generation report while finalizing = %+v", again)
			}
			// Another lineage is not told anything about the run.
			if other := soleAdoption(t, claimWithAdoptions(t, service, 2, finalizingAdoption(tc.run.testID, supervisionTestToken(99), 2))); other.GetFinalizing() {
				t.Fatalf("a different supervision token was told the run is finalizing: %+v", other)
			}

			// A successor whose newest intent never reached this generator
			// proves the run's lineage with an older proof in the chain.
			chained := &pb.RunAdoption{
				TestId: tc.run.testID, PriorGeneration: 2, PriorRunSupervisionToken: supervisionTestToken(98), NewRunSupervisionToken: supervisionTestToken(63),
				PriorProofs: []*pb.RunAdoptionProof{{Generation: 1, RunSupervisionToken: token}},
			}
			if proven := soleAdoption(t, claimWithAdoptions(t, service, 3, chained)); !proven.GetFinalizing() {
				t.Fatalf("a chained lineage proof was not told the run is finalizing: %+v", proven)
			}

			release()
			<-ended
			state, _ := waitForTerminal(t, service, tc.run.testID)
			if state != tc.state {
				t.Fatalf("terminal state = %s, want %s", state, tc.state)
			}
			waitFor(t, "the finalizing mark to clear", 10*time.Second, func() bool { return !runFinalizing(service, tc.run.testID) })
			final := soleAdoption(t, claimWithAdoptions(t, service, 3, finalizingAdoption(tc.run.testID, token, 3)))
			reported := time.Duration(0)
			if final.GetCompletedLogicalDuration() != nil {
				reported = final.GetCompletedLogicalDuration().AsDuration()
			}
			if final.GetFinalizing() || final.GetOutcome() != tc.outcome || final.GetTerminalState() != tc.state || final.GetTerminalCause() != tc.cause || reported != tc.duration {
				t.Fatalf("report after finalization = %+v, want %s %s %s %s", final, tc.outcome, tc.state, tc.cause, tc.duration)
			}
		})
	}
}

// A stopped run whose executions still drain is not finalizing: its answer is
// the earlier one, without the flag, until every execution has ended.
func TestDrainingStopIsNotReportedFinalizing(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	service := newTerminalCauseService(t, "draining")
	release := holdArchiveFinalizers(t, service)
	// The target holds every request until the test lets it answer, so a
	// graceful stop keeps the run's one VUser in flight.
	var arrived, answered atomic.Int32
	answer := make(chan struct{})
	var answerOnce sync.Once
	letAnswer := func() { answerOnce.Do(func() { close(answer) }) }
	t.Cleanup(letAnswer)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Add(1)
		select {
		case <-answer:
		case <-r.Context().Done():
		}
		answered.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	content := []byte(fmt.Sprintf(`{"name":"draining","protocol":"http","config":{"base_url":%q},"actions":[{"name":"request","type":"GET","target":"/"}]}`, target.URL))
	distributeEpochScript(t, service, 1, "script-draining", content)
	token := supervisionTestToken(43)
	if ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "run", GroupName: "group", ScriptId: "script-draining", ScriptContent: content,
		ExpectedScriptHash: scriptContentHash(content), VuserCount: 1, ExecutionGeneration: 1,
		RunSupervisionToken: token, TargetDuration: durationpb.New(time.Minute),
	}); err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%+v, %v)", ack, err)
	}
	claim := &pb.RunSupervisionClaim{TestId: "run", ExecutionGeneration: 1, ExecutionLeaseOwnerId: "controller/1", RunSupervisionToken: token, RequestedTtlMilliseconds: 60_000}
	if ack, err := service.ClaimRunSupervision(context.Background(), claim); err != nil || !ack.GetAccepted() {
		t.Fatalf("ClaimRunSupervision = (%+v, %v)", ack, err)
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "run", ExecutionGeneration: 1, RunSupervisionToken: token}); err != nil || !ack.GetAccepted() {
		t.Fatalf("CommitWork = (%+v, %v)", ack, err)
	}
	waitFor(t, "a request in flight", 10*time.Second, func() bool { return arrived.Load() > 0 })
	if ack, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run", Graceful: true, ExecutionGeneration: 1, RunSupervisionToken: token}); err != nil || !ack.GetAcknowledged() {
		t.Fatalf("graceful StopTest = (%+v, %v)", ack, err)
	}
	if _, assigned := runAssignment(service); assigned {
		t.Fatal("the stop left the assignment in place")
	}
	// The assignment is gone, but the VUser's request is still in flight.
	draining := soleAdoption(t, claimWithAdoptions(t, service, 2, finalizingAdoption("run", token, 2)))
	if draining.GetFinalizing() || runFinalizing(service, "run") {
		t.Fatalf("a draining run was reported finalizing: %+v", draining)
	}
	if answered.Load() != 0 {
		t.Fatal("the request ended before the claim, so the run was not draining")
	}
	if draining.GetOutcome() == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED || draining.GetOutcome() == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED {
		t.Fatalf("a draining run answered %s", draining.GetOutcome())
	}

	letAnswer()
	waitFor(t, "the drained run to finalize", 20*time.Second, func() bool { return runFinalizing(service, "run") })
	assertNothingExecutes(t, service, "run")
	if finalizing := soleAdoption(t, claimWithAdoptions(t, service, 2, finalizingAdoption("run", token, 2))); !finalizing.GetFinalizing() {
		t.Fatalf("the drained run was not reported finalizing: %+v", finalizing)
	}
	release()
	if state, outcome := waitForTerminal(t, service, "run"); state != "COMPLETE" || outcome.cause != pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP {
		t.Fatalf("terminal = %s %s", state, outcome.cause)
	}
}

// The mark carries the exact lineage, is never set while the assignment of
// that generation is still in place, and only its own generation clears it.
func TestRunFinalizingMarkRequiresTheExactLineage(t *testing.T) {
	service := newIsolatedService(t, 10)
	hash := runSupervisionTokenHash(supervisionTestToken(7))
	assignment := assignedTest{testID: "run", executionGeneration: 3, runSupervisionTokenHash: hash}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.assignments["run"] = assignment
	service.markRunFinalizingLocked("run", assignment)
	if service.runFinalizingLocked("run", 3, hash) {
		t.Fatal("a run whose assignment is in place was marked finalizing")
	}
	delete(service.assignments, "run")
	service.markRunFinalizingLocked("run", assignment)
	switch {
	case !service.runFinalizingLocked("run", 3, hash):
		t.Fatal("the exact lineage is not finalizing")
	case service.runFinalizingLocked("run", 2, hash):
		t.Fatal("another generation is finalizing")
	case service.runFinalizingLocked("run", 3, runSupervisionTokenHash(supervisionTestToken(8))):
		t.Fatal("another token is finalizing")
	}
	service.clearRunFinalizingLocked("run", 2)
	if !service.runFinalizingLocked("run", 3, hash) {
		t.Fatal("another generation cleared the mark")
	}
	service.clearRunFinalizingLocked("run", 3)
	if service.runFinalizingLocked("run", 3, hash) {
		t.Fatal("the mark outlived its generation's stop completion")
	}
	// A run without a supervision token has no lineage to report.
	unsupervised := assignedTest{testID: "bare", executionGeneration: 3}
	service.markRunFinalizingLocked("bare", unsupervised)
	if service.runFinalizingLocked("bare", 3, unsupervised.runSupervisionTokenHash) {
		t.Fatal("a run without a supervision token was reported finalizing")
	}
}
