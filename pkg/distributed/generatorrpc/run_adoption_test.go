package generatorrpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// startAdoptableRun stages, supervises and commits one real, executing run
// under generation 1 with the given adoption grace, on an epoch that lapses
// after epochTTL. The run sends paced requests to a local HTTP server.
func startAdoptableRun(t *testing.T, grace, epochTTL time.Duration) (*Service, []byte) {
	return startAdoptableRunWithLease(t, grace, epochTTL, 0)
}

// startAdoptableRunWithLease is startAdoptableRun for a loss-tolerant run
// (require_test_execution_lease) whose run lease lasts runLeaseTTL; zero
// stages an ordinary run.
func startAdoptableRunWithLease(t *testing.T, grace, epochTTL, runLeaseTTL time.Duration) (*Service, []byte) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(target.Close)
	service := NewService("adoption-test", 10, WithExecution(), WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	if ack := claimEpoch(t, service, 1, epochTTL); !ack.GetAccepted() {
		t.Fatalf("claim generation 1 rejected: %s", ack.GetErrorMessage())
	}
	if !claimEpoch(t, service, 1, epochTTL).GetAdoptionSupported() {
		t.Fatal("generator does not advertise adoption support")
	}
	token := supervisionTestToken(21)
	content := []byte(fmt.Sprintf(`{"name":"adoptable","protocol":"http","config":{"base_url":%q,"think_time":{"type":"fixed","mean_ms":50}},"actions":[{"name":"request","type":"GET","target":"/","think_time_after":true}]}`, target.URL))
	request := &pb.WorkAssignment{
		TestId: "run", GroupName: "group", ScriptId: "script", ScriptContent: content,
		ExpectedScriptHash: scriptContentHash(content), VuserCount: 1, ExecutionGeneration: 1,
		RunSupervisionToken: token, TargetDuration: durationpb.New(time.Minute),
		RequireTestExecutionLease: runLeaseTTL > 0,
	}
	if grace > 0 {
		request.AdoptionGrace = durationpb.New(grace)
	}
	distributeEpochScript(t, service, 1, "script", content)
	if ack, err := service.AssignWork(context.Background(), request); err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%+v, %v)", ack, err)
	}
	if runLeaseTTL > 0 {
		lease, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{ExecutionGeneration: 1, LeaseTtlMilliseconds: runLeaseTTL.Milliseconds(), ExecutionLeaseOwnerId: "controller/1", TestId: "run"})
		if err != nil || !lease.GetAccepted() {
			t.Fatalf("run lease claim = (%+v, %v)", lease, err)
		}
	}
	claim := &pb.RunSupervisionClaim{TestId: "run", ExecutionGeneration: 1, ExecutionLeaseOwnerId: "controller/1", RunSupervisionToken: token, RequestedTtlMilliseconds: 60_000}
	if ack, err := service.ClaimRunSupervision(context.Background(), claim); err != nil || !ack.GetAccepted() {
		t.Fatalf("ClaimRunSupervision = (%+v, %v)", ack, err)
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "run", ExecutionGeneration: 1, RunSupervisionToken: token}); err != nil || !ack.GetAccepted() {
		t.Fatalf("CommitWork = (%+v, %v)", ack, err)
	}
	return service, token
}

func runAssignment(service *Service) (assignedTest, bool) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	assignment, ok := service.assignments["run"]
	return assignment, ok
}

func waitFor(t *testing.T, what string, within time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForOrphan(t *testing.T, service *Service) assignedTest {
	t.Helper()
	var orphan assignedTest
	waitFor(t, "the run to be orphaned", 10*time.Second, func() bool {
		assignment, ok := runAssignment(service)
		orphan = assignment
		return ok && !assignment.orphanedUntil.IsZero()
	})
	return orphan
}

func claimWithAdoptions(t *testing.T, service *Service, generation uint64, adoptions ...*pb.RunAdoption) *pb.ExecutionEpochAck {
	t.Helper()
	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: generation, LeaseTtlMilliseconds: 5_000,
		ExecutionLeaseOwnerId: "controller/2", Adoptions: adoptions,
	})
	if err != nil {
		t.Fatalf("ClaimExecutionEpoch(%d): %v", generation, err)
	}
	return ack
}

func TestOrphanedRunIsAdoptedByHigherGeneration(t *testing.T) {
	service, token := startAdoptableRun(t, 3*time.Second, 150*time.Millisecond)
	orphan := waitForOrphan(t, service)
	if orphan.executionGeneration != 1 {
		t.Fatalf("orphan generation = %d, want 1", orphan.executionGeneration)
	}
	service.mu.RLock()
	leaseDeadline := service.runSupervisions["run"].deadline
	service.mu.RUnlock()
	if !leaseDeadline.Equal(orphan.orphanedUntil) {
		t.Fatalf("supervision deadline %s does not enforce the grace %s", leaseDeadline, orphan.orphanedUntil)
	}

	newToken := supervisionTestToken(22)
	adoption := &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: newToken}
	ack := claimWithAdoptions(t, service, 2, adoption)
	if !ack.GetAccepted() || len(ack.GetAdoptions()) != 1 || ack.GetAdoptions()[0].GetOutcome() != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
		t.Fatalf("adoption ack = %+v", ack)
	}
	adopted, ok := runAssignment(service)
	if !ok || adopted.executionGeneration != 2 || !adopted.orphanedUntil.IsZero() || !sameRunSupervisionToken(adopted.runSupervisionTokenHash, newToken) {
		t.Fatalf("adopted assignment = present:%v generation:%d orphanedUntil:%s", ok, adopted.executionGeneration, adopted.orphanedUntil)
	}

	// The successor now supervises under the rotated token; the old token is dead.
	renew := &pb.RunSupervisionClaim{TestId: "run", ExecutionGeneration: 2, ExecutionLeaseOwnerId: "controller/2", RunSupervisionToken: newToken, RequestedTtlMilliseconds: 60_000}
	if ack, err := service.ClaimRunSupervision(context.Background(), renew); err != nil || !ack.GetAccepted() {
		t.Fatalf("successor supervision = (%+v, %v)", ack, err)
	}
	stale := &pb.RunSupervisionClaim{TestId: "run", ExecutionGeneration: 2, ExecutionLeaseOwnerId: "controller/2", RunSupervisionToken: token, RequestedTtlMilliseconds: 60_000}
	if ack, err := service.ClaimRunSupervision(context.Background(), stale); err != nil || ack.GetAccepted() {
		t.Fatalf("old token still supervises the run: (%+v, %v)", ack, err)
	}

	// Live metrics must keep flowing under the new generation: a subscriber of
	// generation 2 receives a RUNNING batch for the adopted run.
	_, subscriber, _, err := service.registerScopedSubscriber(2, "run")
	if err != nil {
		t.Fatalf("subscribe to generation 2: %v", err)
	}
	waitFor(t, "a live generation-2 metrics batch", 5*time.Second, func() bool {
		for batch := subscriber.pop(); batch != nil; batch = subscriber.pop() {
			if batch.GetExecutionGeneration() == 2 && batch.GetLifecycleState() == "RUNNING" {
				return true
			}
		}
		return false
	})

	// A retry after a lost acknowledgement confirms idempotently.
	retry := claimWithAdoptions(t, service, 2, adoption)
	if len(retry.GetAdoptions()) != 1 || retry.GetAdoptions()[0].GetOutcome() != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
		t.Fatalf("retried adoption = %+v", retry.GetAdoptions())
	}
}

func TestOrphanStopsWithAdoptionTimeoutWhenNoSuccessorArrives(t *testing.T) {
	service, _ := startAdoptableRun(t, 400*time.Millisecond, 150*time.Millisecond)
	waitForOrphan(t, service)
	waitFor(t, "the unadopted orphan to stop", 5*time.Second, func() bool {
		_, ok := runAssignment(service)
		return !ok
	})
	waitFor(t, "the terminal outcome", 5*time.Second, func() bool {
		_, terminal := service.metricsBroker.terminalState("run", 1)
		return terminal
	})
	service.mu.RLock()
	state, _ := service.metricsBroker.terminalState("run", 1)
	service.mu.RUnlock()
	if !strings.EqualFold(state, "FAILED") {
		t.Fatalf("unadopted orphan terminal state = %q, want FAILED", state)
	}
}

func TestAdoptionResetsPauseOrderingForSuccessor(t *testing.T) {
	service, token := startAdoptableRun(t, 3*time.Second, 5*time.Second)
	_, err := service.SetPaused(context.Background(), &pb.PauseRequest{
		TestId: "run", ExecutionGeneration: 1, RunSupervisionToken: token,
		Pause: true, OperationId: "old-pause", OperationSequence: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	newToken := supervisionTestToken(22)
	ack := claimWithAdoptions(t, service, 2, &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: newToken})
	if !ack.GetAccepted() || ack.GetAdoptions()[0].GetOutcome() != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
		t.Fatalf("adopt: %+v", ack)
	}
	resumed, err := service.SetPaused(context.Background(), &pb.PauseRequest{
		TestId: "run", ExecutionGeneration: 2, RunSupervisionToken: newToken,
		Pause: false, OperationId: "new-resume", OperationSequence: 1,
	})
	if err != nil || !resumed.GetAccepted() || resumed.GetCurrentPaused() {
		t.Fatalf("successor could not resume the paused run: %+v, %v", resumed, err)
	}
	_, err = service.SetPaused(context.Background(), &pb.PauseRequest{
		TestId: "run", ExecutionGeneration: 1, RunSupervisionToken: token,
		Pause: true, OperationId: "stale-pause", OperationSequence: 11,
	})
	if err == nil {
		t.Fatal("old controller paused the adopted run")
	}
}

func TestHigherGenerationWithoutAdoptionFencesOrphan(t *testing.T) {
	service, _ := startAdoptableRun(t, 3*time.Second, 150*time.Millisecond)
	waitForOrphan(t, service)
	if ack := claimWithAdoptions(t, service, 2); !ack.GetAccepted() {
		t.Fatalf("claim generation 2 rejected: %s", ack.GetErrorMessage())
	}
	if _, ok := runAssignment(service); ok {
		t.Fatal("a successor that did not adopt the orphan left it running")
	}
}

func TestFailPolicyRunIsFencedNotOrphaned(t *testing.T) {
	service, token := startAdoptableRun(t, 0, 150*time.Millisecond)
	waitFor(t, "the fail-policy run to be fenced", 3*time.Second, func() bool {
		_, ok := runAssignment(service)
		return !ok
	})
	ack := claimWithAdoptions(t, service, 2, &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: supervisionTestToken(23)})
	if got := ack.GetAdoptions(); len(got) != 1 || got[0].GetOutcome() == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
		t.Fatalf("a fenced fail-policy run was reported adoptable: %+v", got)
	}
}

func TestAdoptionRejectsWrongLineage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(*pb.RunAdoption)
		expected pb.RunAdoptionOutcome
	}{
		{"wrong prior token", func(a *pb.RunAdoption) { a.PriorRunSupervisionToken = supervisionTestToken(99) }, pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED},
		{"wrong prior generation", func(a *pb.RunAdoption) { a.PriorGeneration = 3 }, pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED},
		{"unknown run", func(a *pb.RunAdoption) { a.TestId = "other" }, pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, token := startAdoptableRun(t, 3*time.Second, 150*time.Millisecond)
			waitForOrphan(t, service)
			adoption := &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: supervisionTestToken(24)}
			tc.mutate(adoption)
			generation := uint64(2)
			if adoption.PriorGeneration >= generation {
				generation = adoption.PriorGeneration + 1
			}
			ack := claimWithAdoptions(t, service, generation, adoption)
			if got := ack.GetAdoptions(); len(got) != 1 || got[0].GetOutcome() != tc.expected {
				t.Fatalf("adoption outcome = %+v, want %s", got, tc.expected)
			}
			if _, ok := runAssignment(service); ok {
				t.Fatal("an orphan with an unproven adoption was left running")
			}
		})
	}
}

func TestRunAdoptionRequestValidation(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	valid := func() *pb.RunAdoption {
		return &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: supervisionTestToken(1), NewRunSupervisionToken: supervisionTestToken(2)}
	}
	for name, adoptions := range map[string][]*pb.RunAdoption{
		"missing test id":   {{PriorGeneration: 1, PriorRunSupervisionToken: supervisionTestToken(1), NewRunSupervisionToken: supervisionTestToken(2)}},
		"duplicate test":    {valid(), valid()},
		"zero prior":        {{TestId: "run", PriorRunSupervisionToken: supervisionTestToken(1), NewRunSupervisionToken: supervisionTestToken(2)}},
		"short token":       {{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: []byte{1}, NewRunSupervisionToken: supervisionTestToken(2)}},
		"token not rotated": {{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: supervisionTestToken(1), NewRunSupervisionToken: supervisionTestToken(1)}},
	} {
		_, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{ExecutionGeneration: 2, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/2", Adoptions: adoptions})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: error = %v, want InvalidArgument", name, err)
		}
	}
	_, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{ExecutionGeneration: 2, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/2", TestId: "run", Adoptions: []*pb.RunAdoption{valid()}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("run-scoped claim with adoptions: error = %v, want InvalidArgument", err)
	}
}

func TestAdoptionGraceValidationAndCap(t *testing.T) {
	token := supervisionTestToken(3)
	for name, assignment := range map[string]*pb.WorkAssignment{
		"negative":            {AdoptionGrace: durationpb.New(-time.Second), RunSupervisionToken: token},
		"without supervision": {AdoptionGrace: durationpb.New(time.Second)},
	} {
		if err := validateAdoptionGrace(assignment); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: error = %v, want InvalidArgument", name, err)
		}
	}
	if err := validateAdoptionGrace(&pb.WorkAssignment{AdoptionGrace: durationpb.New(time.Second), RunSupervisionToken: token}); err != nil {
		t.Errorf("valid grace rejected: %v", err)
	}
	if err := validateAdoptionGrace(&pb.WorkAssignment{AdoptionGrace: durationpb.New(time.Second), RunSupervisionToken: token, RequireTestExecutionLease: true}); err != nil {
		t.Errorf("grace on a loss-tolerant assignment rejected: %v", err)
	}
	service := &Service{maxAdoptionGrace: 45 * time.Second}
	if got := service.effectiveAdoptionGrace(&pb.WorkAssignment{AdoptionGrace: durationpb.New(10 * time.Minute)}); got != 45*time.Second {
		t.Errorf("effective grace = %s, want the 45s generator cap", got)
	}
	WithMaxAdoptionGrace(time.Hour)(service)
	if service.maxAdoptionGrace != maxAdoptionGrace {
		t.Errorf("WithMaxAdoptionGrace(1h) = %s, want hard cap %s", service.maxAdoptionGrace, maxAdoptionGrace)
	}
}

func TestRunSupervisionConfigMaxAdoptionGrace(t *testing.T) {
	config, err := RunSupervisionConfigFromEnv(func(string) string { return "" })
	if err != nil || config.MaxAdoptionGrace != maxAdoptionGrace {
		t.Fatalf("default config = (%+v, %v), want %s cap", config, err, maxAdoptionGrace)
	}
	for value, wantErr := range map[string]bool{"0s": false, "45s": false, "121s": true, "-1s": true, "soon": true} {
		_, err := RunSupervisionConfigFromEnv(func(key string) string {
			if key == envGeneratorMaxAdoptionGrace {
				return value
			}
			return ""
		})
		if (err != nil) != wantErr {
			t.Errorf("%s=%s: err = %v, wantErr %v", envGeneratorMaxAdoptionGrace, value, err, wantErr)
		}
	}
}

func TestClientClaimExecutionEpochWithAdoptionsRoundTrip(t *testing.T) {
	service, token := startAdoptableRun(t, 3*time.Second, 150*time.Millisecond)
	waitForOrphan(t, service)
	client := supervisionHeartbeatClient(t, service)
	result, err := client.ClaimExecutionEpochWithAdoptions(context.Background(), 2, "controller/2", 5*time.Second, []RunAdoption{
		{TestID: "run", PriorGeneration: 1, PriorToken: token, NewToken: supervisionTestToken(25)},
		{TestID: "missing", PriorGeneration: 1, PriorToken: supervisionTestToken(26), NewToken: supervisionTestToken(27)},
	})
	if err != nil || !result.Accepted || !result.AdoptionSupported {
		t.Fatalf("claim = (%+v, %v)", result, err)
	}
	outcomes := map[string]pb.RunAdoptionOutcome{}
	for _, adoption := range result.Adoptions {
		outcomes[adoption.TestID] = adoption.Outcome
	}
	if outcomes["run"] != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED || outcomes["missing"] != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST {
		t.Fatalf("adoption outcomes = %v", outcomes)
	}
}

// A run already stopping on the generator is reported, not adopted: the
// successor finishes the stop instead of reviving the run.
func TestAdoptionReportsAlreadyStoppingRun(t *testing.T) {
	service, token := startAdoptableRun(t, 30*time.Second, 5*time.Second)
	service.mu.Lock()
	service.stoppingTests["run"] = struct{}{}
	service.mu.Unlock()
	t.Cleanup(func() {
		service.mu.Lock()
		delete(service.stoppingTests, "run")
		service.mu.Unlock()
	})
	ack := claimWithAdoptions(t, service, 2, &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: supervisionTestToken(31)})
	if got := ack.GetAdoptions(); len(got) != 1 || got[0].GetOutcome() != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED {
		t.Fatalf("stopping run adoption outcome = %+v, want ALREADY_STOPPED", got)
	}
}

// Controller death lapses a loss-tolerant run's lease together with its epoch:
// the run is orphaned, then adopted, and the successor can take a fresh run
// lease and supervision under its own generation.
func TestLossTolerantRunIsOrphanedAndAdoptedWhenItsControllerDies(t *testing.T) {
	service, token := startAdoptableRunWithLease(t, 3*time.Second, 300*time.Millisecond, 300*time.Millisecond)
	waitForOrphan(t, service)
	newToken := supervisionTestToken(41)
	ack := claimWithAdoptions(t, service, 2, &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: newToken})
	if got := ack.GetAdoptions(); len(got) != 1 || got[0].GetOutcome() != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
		t.Fatalf("loss-tolerant adoption = %+v", got)
	}
	lease, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{ExecutionGeneration: 2, LeaseTtlMilliseconds: 5_000, ExecutionLeaseOwnerId: "controller/2", TestId: "run"})
	if err != nil || !lease.GetAccepted() {
		t.Fatalf("successor run lease = (%+v, %v)", lease, err)
	}
	renew := &pb.RunSupervisionClaim{TestId: "run", ExecutionGeneration: 2, ExecutionLeaseOwnerId: "controller/2", RunSupervisionToken: newToken, RequestedTtlMilliseconds: 60_000}
	if ack, err := service.ClaimRunSupervision(context.Background(), renew); err != nil || !ack.GetAccepted() {
		t.Fatalf("successor supervision = (%+v, %v)", ack, err)
	}
	if assignment, ok := runAssignment(service); !ok || assignment.executionGeneration != 2 {
		t.Fatalf("adopted loss-tolerant run = present:%v generation:%d", ok, assignment.executionGeneration)
	}
}

// A live controller fences one generator by letting only its run lease lapse
// (generator-loss evacuation) while the epoch stays renewed. That must still
// stop the run: orphaning it would leave the evacuated source generating load
// next to its replacement.
func TestDeliberatelyFencedLossTolerantRunStillStops(t *testing.T) {
	service, _ := startAdoptableRunWithLease(t, 30*time.Second, 30*time.Second, 300*time.Millisecond)
	waitFor(t, "the fenced run to stop", 5*time.Second, func() bool {
		assignment, ok := runAssignment(service)
		if ok && !assignment.orphanedUntil.IsZero() {
			t.Fatal("a deliberately fenced run was orphaned")
		}
		return !ok
	})
}

// Realistic timing: the run lease lapses seconds before the epoch because the
// two are renewed on different schedules. A silent controller still orphans.
func TestLossTolerantRunOrphansWhenRunLeaseLapsesBeforeEpoch(t *testing.T) {
	// At the run lease's expiry (6.5s) the epoch still has 5.5s left, outside
	// the near-expiry window, so only controller silence (6.5s >= 12s/2) can
	// orphan the run, as in a real failover.
	service, _ := startAdoptableRunWithLease(t, 30*time.Second, 12*time.Second, 6500*time.Millisecond)
	orphan := waitForOrphan(t, service)
	if orphan.executionGeneration != 1 {
		t.Fatalf("orphan generation = %d", orphan.executionGeneration)
	}
}

func TestRunAdoptionTransfersFrozenCapacity(t *testing.T) {
	for _, invalid := range []string{"", "generation", "incarnation", "token"} {
		t.Run("lineage_"+invalid, func(t *testing.T) {
			service, token := startAdoptableRun(t, 3*time.Second, 5*time.Second)
			budget := protocol.CapacityVector{CPUCores: 1, MemoryMB: 100, NetworkMbps: 100, VUsers: 10}
			before := &pb.PodCapacityClaim{TestId: "run", ReservationId: "attempt", Incarnation: service.podCapacityIncarnation, ExecutionGeneration: 1, ModelVersion: protocol.CapacityReservationModel, Revision: 1, Schedulable: CapacityVectorToProto(budget), Demand: CapacityVectorToProto(budget.Scale(.5)), GroupPeaks: map[string]int32{"group": 5}}
			switch invalid {
			case "generation":
				before.ExecutionGeneration = 3
			case "incarnation":
				before.Incarnation = "replaced"
			case "token":
				token = supervisionTestToken(99)
			}
			service.mu.Lock()
			service.podResources = podResourceCapacity{CPULimitCores: 1, MemoryLimitBytes: 100 * 1024 * 1024, MaxVUsers: 10}
			service.podCapacityClaims = map[string]*pb.PodCapacityClaim{"run": proto.Clone(before).(*pb.PodCapacityClaim)}
			service.mu.Unlock()
			adoption := &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: supervisionTestToken(22)}
			ack := claimWithAdoptions(t, service, 2, adoption)
			service.mu.RLock()
			got := proto.Clone(service.podCapacityClaims["run"]).(*pb.PodCapacityClaim)
			service.mu.RUnlock()
			if invalid != "" {
				if ack.GetAdoptions()[0].GetOutcome() == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED || !proto.Equal(got, before) {
					t.Fatalf("invalid lineage transferred capacity: %+v %+v", ack, got)
				}
				return
			}
			expected := proto.Clone(before).(*pb.PodCapacityClaim)
			expected.ExecutionGeneration = 2
			if ack.GetAdoptions()[0].GetOutcome() != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED || !proto.Equal(got, expected) {
				t.Fatalf("capacity transfer: %+v %+v", ack, got)
			}
			if ack, err := service.ReservePodCapacity(context.Background(), expected); err != nil || !ack.GetAccepted() {
				t.Fatalf("confirmation: %+v %v", ack, err)
			}
			retry := claimWithAdoptions(t, service, 2, adoption)
			if retry.GetAdoptions()[0].GetOutcome() != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
				t.Fatalf("retry: %+v", retry)
			}
			expected.Revision++
			expected.Demand = CapacityVectorToProto(budget.Scale(.6))
			if ack, err := service.ReservePodCapacity(context.Background(), expected); err != nil || !ack.GetAccepted() {
				t.Fatalf("resize: %+v %v", ack, err)
			}
			if ack, err := service.ReservePodCapacity(context.Background(), before); err == nil && ack.GetAccepted() {
				t.Fatal("old owner changed capacity")
			}
		})
	}
}
