package generatorrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func supervisionTestToken(value byte) []byte {
	token := make([]byte, RunSupervisionTokenSize)
	for i := range token {
		token[i] = value
	}
	return token
}

func supervisedStageRequest(testID, scriptID string, generation uint64, token []byte) *pb.WorkAssignment {
	content := []byte(`{"name":"supervised-stage","protocol":"http","config":{"base_url":"http://127.0.0.1"},"actions":[{"name":"request","type":"GET","target":"/"}]}`)
	request := &pb.WorkAssignment{
		TestId: testID, GroupName: "group", ScriptId: scriptID, ScriptContent: content,
		VuserCount: 1, ExecutionGeneration: generation, RunSupervisionToken: token,
	}
	if generation > 0 {
		request.ExpectedScriptHash = scriptContentHash(content)
	}
	return request
}

func shutdownSupervisionTestService(t *testing.T, service *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown generator service: %v", err)
	}
}

func supervisionHeartbeatClient(t *testing.T, service *Service) *Client {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := Dial(
		ctx,
		"restart-generator",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatalf("dial restarted generator: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		server.Stop()
		_ = listener.Close()
	})
	return client
}

func TestRunSupervisionConfigFromEnvRejectsUnsafeValues(t *testing.T) {
	tests := []map[string]string{
		{"APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL": "29s"},
		{"APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL": "61s"},
		{"APEXLOAD_GENERATOR_ABANDON_DRAIN": "11s"},
		{"APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL": "30s", "APEXLOAD_GENERATOR_ABANDON_DRAIN": "30s"},
	}
	for _, values := range tests {
		if _, err := RunSupervisionConfigFromEnv(func(key string) string { return values[key] }); err == nil {
			t.Fatalf("RunSupervisionConfigFromEnv(%v) succeeded, want error", values)
		}
	}
}

func TestClaimRunSupervisionValidatesRawTTLBeforeDurationConversion(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	token := supervisionTestToken(17)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", runSupervisionTokenHash: runSupervisionTokenHash(token), groups: map[string]int32{},
	}
	service.mu.Unlock()
	defer func() {
		service.mu.Lock()
		if lease := service.runSupervisions["run"]; lease != nil && lease.timer != nil {
			lease.timer.Stop()
		}
		service.mu.Unlock()
	}()

	for _, ttlMilliseconds := range []int64{-1, 29_999, 60_001, int64(^uint64(0) >> 1)} {
		_, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
			TestId: "run", RunSupervisionToken: token, RequestedTtlMilliseconds: ttlMilliseconds,
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("ClaimRunSupervision ttl=%d error = %v, want InvalidArgument", ttlMilliseconds, err)
		}
	}
	for _, ttlMilliseconds := range []int64{30_000, 60_000} {
		ack, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
			TestId: "run", RunSupervisionToken: token, RequestedTtlMilliseconds: ttlMilliseconds,
		})
		if err != nil || !ack.GetAccepted() || ack.GetGrantedTtlMilliseconds() != ttlMilliseconds {
			t.Fatalf("ClaimRunSupervision boundary ttl=%d = (%+v, %v)", ttlMilliseconds, ack, err)
		}
	}
}

func TestRunSupervisionRequiredRejectsUnsupervisedAssignment(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	_, err := service.AssignWork(context.Background(), &pb.WorkAssignment{TestId: "run", GroupName: "group", ScriptId: "script", VuserCount: 1})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("AssignWork error = %v, want FailedPrecondition", err)
	}
}

func TestRunSupervisionCommitRequiresClaimAndMutationRequiresToken(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	token := supervisionTestToken(4)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", runSupervisionTokenHash: runSupervisionTokenHash(token), groups: map[string]int32{}}
	service.mu.Unlock()
	if _, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "run", RunSupervisionToken: token}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CommitWork before claim error = %v, want FailedPrecondition", err)
	}
	if _, err := service.SetPaused(context.Background(), &pb.PauseRequest{TestId: "run", Pause: true, RunSupervisionToken: supervisionTestToken(5)}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SetPaused with wrong token error = %v, want FailedPrecondition", err)
	}
	select {
	case request := <-service.SelfTerminationRequested():
		t.Fatalf("commit-before-claim unexpectedly started traffic: %+v", request)
	default:
	}
}

func TestRunSupervisionRenewalMakesOldTimerCallbackHarmless(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	token := supervisionTestToken(6)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", runSupervisionTokenHash: runSupervisionTokenHash(token), groups: map[string]int32{}}
	service.mu.Unlock()
	claim := &pb.RunSupervisionClaim{TestId: "run", RunSupervisionToken: token, RequestedTtlMilliseconds: 60_000}
	if ack, err := service.ClaimRunSupervision(context.Background(), claim); err != nil || !ack.GetAccepted() {
		t.Fatalf("initial claim = (%+v, %v)", ack, err)
	}
	service.mu.RLock()
	oldTimerToken := service.runSupervisions["run"].timerToken
	service.mu.RUnlock()
	if ack, err := service.ClaimRunSupervision(context.Background(), claim); err != nil || !ack.GetAccepted() {
		t.Fatalf("renewal = (%+v, %v)", ack, err)
	}
	service.expireRunSupervision("run", oldTimerToken)
	service.mu.RLock()
	_, assigned := service.assignments["run"]
	lease := service.runSupervisions["run"]
	service.mu.RUnlock()
	if !assigned || lease == nil || lease.timerToken == oldTimerToken {
		t.Fatalf("stale callback altered renewed lease: assigned=%v lease=%+v", assigned, lease)
	}
}

func TestDuplicateCommitDoesNotRenewRunSupervision(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	token := supervisionTestToken(15)
	deadline := time.Now().Add(45 * time.Second)
	lease := &runSupervisionLease{
		generation: 0,
		tokenHash:  runSupervisionTokenHash(token),
		ttl:        60 * time.Second,
		deadline:   deadline,
		state:      pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE,
		timerToken: 41,
		timer:      time.AfterFunc(time.Hour, func() {}),
	}
	defer func() {
		service.mu.Lock()
		if current := service.runSupervisions["run"]; current != nil && current.timer != nil {
			current.timer.Stop()
		}
		service.mu.Unlock()
	}()
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID:                  "run",
		lifecycle:               assignmentLifecycleRunning,
		committed:               true,
		runSupervisionTokenHash: runSupervisionTokenHash(token),
		groups:                  map[string]int32{"group": 1},
	}
	service.runSupervisions["run"] = lease
	service.mu.Unlock()

	ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{
		TestId: "run", RunSupervisionToken: token,
	})
	if err != nil || !ack.GetAccepted() || !ack.GetRunSupervisionArmed() {
		t.Fatalf("duplicate CommitWork = (%+v, %v), want existing active lease ACK", ack, err)
	}
	service.mu.RLock()
	gotDeadline := lease.deadline
	gotTimerToken := lease.timerToken
	service.mu.RUnlock()
	if !gotDeadline.Equal(deadline) || gotTimerToken != 41 {
		t.Fatalf("duplicate CommitWork renewed lease: deadline/token = %s/%d, want %s/41", gotDeadline, gotTimerToken, deadline)
	}
	if got := time.Duration(ack.GetRunSupervisionTtlMilliseconds()) * time.Millisecond; got != 60*time.Second {
		t.Fatalf("duplicate CommitWork granted TTL = %s, want 60s contract", got)
	}
}

func TestInitialCommitReportsExactMinimumGrantedRunSupervisionTTL(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(30*time.Second, 5*time.Second))
	token := supervisionTestToken(16)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID:                  "run",
		lifecycle:               assignmentLifecycleStaged,
		runSupervisionTokenHash: runSupervisionTokenHash(token),
		groups:                  map[string]int32{"group": 1},
	}
	service.mu.Unlock()
	defer func() {
		service.mu.Lock()
		if lease := service.runSupervisions["run"]; lease != nil && lease.timer != nil {
			lease.timer.Stop()
		}
		service.mu.Unlock()
	}()

	claim, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "run", RunSupervisionToken: token, RequestedTtlMilliseconds: 30_000,
	})
	if err != nil || !claim.GetAccepted() {
		t.Fatalf("ClaimRunSupervision = (%+v, %v)", claim, err)
	}
	commit, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{
		TestId: "run", RunSupervisionToken: token,
	})
	if err != nil || !commit.GetAccepted() || !commit.GetRunSupervisionArmed() {
		t.Fatalf("CommitWork = (%+v, %v), want armed", commit, err)
	}
	if got := commit.GetRunSupervisionTtlMilliseconds(); got != 30_000 {
		t.Fatalf("CommitWork granted TTL = %dms, want exact 30000ms minimum", got)
	}
}

func TestRunSupervisionClaimFencesWrongAndExpiredTokens(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	token := supervisionTestToken(1)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", runSupervisionTokenHash: runSupervisionTokenHash(token), groups: map[string]int32{}}
	service.mu.Unlock()

	wrong := supervisionTestToken(2)
	ack, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{TestId: "run", RunSupervisionToken: wrong, RequestedTtlMilliseconds: 60_000})
	if err != nil || ack.GetAccepted() || ack.GetState() != pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED {
		t.Fatalf("wrong-token claim = (%+v, %v), want fenced rejection", ack, err)
	}

	ack, err = service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{TestId: "run", RunSupervisionToken: token, RequestedTtlMilliseconds: 60_000})
	if err != nil || !ack.GetAccepted() || ack.GetContractVersion() != RunSupervisionContractV1 {
		t.Fatalf("valid claim = (%+v, %v), want accepted v1", ack, err)
	}
	service.mu.Lock()
	lease := service.runSupervisions["run"]
	lease.deadline = time.Now().Add(-time.Second)
	timerToken := lease.timerToken
	service.mu.Unlock()
	service.expireRunSupervision("run", timerToken)

	ack, err = service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{TestId: "run", RunSupervisionToken: token, RequestedTtlMilliseconds: 60_000})
	if err != nil || ack.GetAccepted() || ack.GetState() != pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED {
		t.Fatalf("expired-token claim = (%+v, %v), want non-revivable fence", ack, err)
	}
}

func TestGenerationZeroSupervisionExpiryDurablyFencesRestartAndNewTraffic(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	generatorID := "generation-zero-expiry-" + filepath.Base(filepath.Dir(statePath))
	token := supervisionTestToken(31)
	service := NewService(generatorID, 10,
		WithExecutionEpochStatePath(statePath),
		WithRunSupervisionRequired(30*time.Second, 0),
	)
	assignment := supervisedStageRequest("expired-run", "expired-script", 0, token)
	if ack, err := service.AssignWork(context.Background(), assignment); err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%+v, %v)", ack, err)
	}
	claim, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "expired-run", RunSupervisionToken: token, RequestedTtlMilliseconds: 30_000,
	})
	if err != nil || !claim.GetAccepted() {
		t.Fatalf("ClaimRunSupervision = (%+v, %v)", claim, err)
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "expired-run", RunSupervisionToken: token}); err != nil || !ack.GetAccepted() {
		t.Fatalf("CommitWork = (%+v, %v)", ack, err)
	}
	survivorToken := supervisionTestToken(32)
	if ack, err := service.AssignWork(context.Background(), supervisedStageRequest("survivor-run", "survivor-script", 0, survivorToken)); err != nil || !ack.GetAccepted() {
		t.Fatalf("survivor AssignWork = (%+v, %v)", ack, err)
	}
	if claim, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "survivor-run", RunSupervisionToken: survivorToken, RequestedTtlMilliseconds: 30_000,
	}); err != nil || !claim.GetAccepted() {
		t.Fatalf("survivor ClaimRunSupervision = (%+v, %v)", claim, err)
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "survivor-run", RunSupervisionToken: survivorToken}); err != nil || !ack.GetAccepted() {
		t.Fatalf("survivor CommitWork = (%+v, %v)", ack, err)
	}
	service.mu.Lock()
	lease := service.runSupervisions["expired-run"]
	lease.deadline = time.Now().Add(-time.Second)
	timerToken := lease.timerToken
	service.mu.Unlock()
	service.expireRunSupervision("expired-run", timerToken)
	if renewal, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "survivor-run", RunSupervisionToken: survivorToken, RequestedTtlMilliseconds: 30_000,
	}); err != nil || !renewal.GetAccepted() {
		t.Fatalf("unrelated active run stopped renewing after expiry = (%+v, %v)", renewal, err)
	}

	// The fail-closed marker applies immediately in the process that observed
	// expiry, even after the cooperative drain quiesces without a safety exit.
	otherToken := supervisionTestToken(38)
	if _, err := service.AssignWork(context.Background(), supervisedStageRequest("other-run", "other-script", 0, otherToken)); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("post-expiry generation-zero AssignWork error = %v, want FailedPrecondition", err)
	}
	shutdownSupervisionTestService(t, service)

	restarted := NewService(generatorID, 10,
		WithExecution(),
		WithExecutionEpochStatePath(statePath),
		WithRunSupervisionRequired(30*time.Second, 0),
	)
	defer shutdownSupervisionTestService(t, restarted)
	if _, err := restarted.AssignWork(context.Background(), supervisedStageRequest("different-run", "different-script", 0, otherToken)); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("restart generation-zero AssignWork error = %v, want FailedPrecondition", err)
	}

	// Bypass staging to prove both later wire boundaries independently fail
	// closed and cannot create an execution when a stale controller reconnects.
	restarted.mu.Lock()
	restarted.assignments["stale-run"] = assignedTest{
		testID: "stale-run", lifecycle: assignmentLifecycleStaged,
		runSupervisionTokenHash: runSupervisionTokenHash(token),
		groups:                  map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
	}
	restarted.runSupervisions["stale-run"] = &runSupervisionLease{
		tokenHash: runSupervisionTokenHash(token), ttl: 30 * time.Second,
		deadline: time.Now().Add(30 * time.Second), state: pb.RunSupervisionState_RUN_SUPERVISION_STATE_STAGED,
	}
	restarted.mu.Unlock()
	claim, err = restarted.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "stale-run", RunSupervisionToken: token, RequestedTtlMilliseconds: 30_000,
	})
	if err != nil || claim.GetAccepted() || claim.GetErrorMessage() != runSupervisionRestartFenceMessage {
		t.Fatalf("restart generation-zero claim = (%+v, %v), want durable fence", claim, err)
	}
	if _, err := restarted.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "stale-run", RunSupervisionToken: token}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("restart generation-zero CommitWork error = %v, want FailedPrecondition", err)
	}
	restarted.mu.RLock()
	committed := restarted.assignments["stale-run"].committed
	executionCount := len(restarted.executions)
	restarted.mu.RUnlock()
	if committed || executionCount != 0 {
		t.Fatalf("durably fenced restart released traffic: committed=%v executions=%d", committed, executionCount)
	}

	// A durably higher generation is the explicit recovery operation. Its epoch
	// write clears the old marker before any generation-one assignment is staged.
	const owner = "controller/replacement"
	epoch, err := restarted.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: 1, ExecutionLeaseOwnerId: owner, LeaseTtlMilliseconds: 60_000,
	})
	if err != nil || !epoch.GetAccepted() {
		t.Fatalf("claim replacement generation = (%+v, %v)", epoch, err)
	}
	positiveToken := supervisionTestToken(33)
	if ack, err := restarted.AssignWork(context.Background(), supervisedStageRequest("replacement-run", "replacement-script", 1, positiveToken)); err != nil || !ack.GetAccepted() {
		t.Fatalf("replacement AssignWork = (%+v, %v)", ack, err)
	}
	claim, err = restarted.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "replacement-run", ExecutionGeneration: 1, ExecutionLeaseOwnerId: owner,
		RunSupervisionToken: positiveToken, RequestedTtlMilliseconds: 30_000,
	})
	if err != nil || !claim.GetAccepted() {
		t.Fatalf("replacement ClaimRunSupervision = (%+v, %v)", claim, err)
	}
}

func TestPositiveGenerationSupervisionExpiryRejectsSameOwnerAfterRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	generatorID := "positive-generation-expiry-" + filepath.Base(filepath.Dir(statePath))
	const owner = "controller/generation-7"
	token := supervisionTestToken(34)
	service := NewService(generatorID, 10,
		WithExecutionEpochStatePath(statePath),
		WithRunSupervisionRequired(30*time.Second, 0),
	)
	epoch, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: 7, ExecutionLeaseOwnerId: owner, LeaseTtlMilliseconds: 60_000,
	})
	if err != nil || !epoch.GetAccepted() {
		t.Fatalf("ClaimExecutionEpoch = (%+v, %v)", epoch, err)
	}
	if ack, err := service.AssignWork(context.Background(), supervisedStageRequest("expired-positive", "positive-script", 7, token)); err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%+v, %v)", ack, err)
	}
	claim, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "expired-positive", ExecutionGeneration: 7, ExecutionLeaseOwnerId: owner,
		RunSupervisionToken: token, RequestedTtlMilliseconds: 30_000,
	})
	if err != nil || !claim.GetAccepted() {
		t.Fatalf("ClaimRunSupervision = (%+v, %v)", claim, err)
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "expired-positive", ExecutionGeneration: 7, RunSupervisionToken: token}); err != nil || !ack.GetAccepted() {
		t.Fatalf("CommitWork = (%+v, %v)", ack, err)
	}
	service.mu.Lock()
	lease := service.runSupervisions["expired-positive"]
	lease.deadline = time.Now().Add(-time.Second)
	timerToken := lease.timerToken
	service.mu.Unlock()
	service.expireRunSupervision("expired-positive", timerToken)
	shutdownSupervisionTestService(t, service)

	restarted := NewService(generatorID, 10,
		WithExecutionEpochStatePath(statePath),
		WithRunSupervisionRequired(30*time.Second, 0),
	)
	defer shutdownSupervisionTestService(t, restarted)
	stale, err := restarted.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: 7, ExecutionLeaseOwnerId: owner, LeaseTtlMilliseconds: 60_000,
	})
	if err != nil {
		t.Fatalf("reclaim expired generation: %v", err)
	}
	if stale.GetAccepted() || stale.GetErrorMessage() != ExecutionGenerationExpiredMessage {
		t.Fatalf("expired positive generation was revived after restart: %+v", stale)
	}
	if _, err := restarted.AssignWork(context.Background(), supervisedStageRequest("stale-positive", "stale-script", 7, supervisionTestToken(35))); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale positive AssignWork error = %v, want FailedPrecondition", err)
	}
	restarted.mu.RLock()
	executionCount := len(restarted.executions)
	restarted.mu.RUnlock()
	if executionCount != 0 {
		t.Fatalf("stale positive generation created %d executions", executionCount)
	}

	replacement, err := restarted.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: 8, ExecutionLeaseOwnerId: "controller/generation-8", LeaseTtlMilliseconds: 60_000,
	})
	if err != nil || !replacement.GetAccepted() {
		t.Fatalf("claim higher generation = (%+v, %v)", replacement, err)
	}
}

func TestCleanSupervisedTerminalDisarmsRestartFence(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	generatorID := "clean-supervision-" + filepath.Base(filepath.Dir(statePath))
	token := supervisionTestToken(36)
	service := NewService(generatorID, 10,
		WithExecutionEpochStatePath(statePath),
		WithRunSupervisionRequired(30*time.Second, 0),
	)
	if ack, err := service.AssignWork(context.Background(), supervisedStageRequest("clean-run", "clean-script", 0, token)); err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%+v, %v)", ack, err)
	}
	claim, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "clean-run", RunSupervisionToken: token, RequestedTtlMilliseconds: 30_000,
	})
	if err != nil || !claim.GetAccepted() {
		t.Fatalf("ClaimRunSupervision = (%+v, %v)", claim, err)
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "clean-run", RunSupervisionToken: token}); err != nil || !ack.GetAccepted() {
		t.Fatalf("CommitWork = (%+v, %v)", ack, err)
	}
	if stopped, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "clean-run", RunSupervisionToken: token}); err != nil || !stopped.GetAcknowledged() {
		t.Fatalf("StopTest = (%+v, %v)", stopped, err)
	}
	shutdownSupervisionTestService(t, service)

	restarted := NewService(generatorID, 10,
		WithExecutionEpochStatePath(statePath),
		WithRunSupervisionRequired(30*time.Second, 0),
	)
	defer shutdownSupervisionTestService(t, restarted)
	if ack, err := restarted.AssignWork(context.Background(), supervisedStageRequest("next-clean-run", "next-clean-script", 0, supervisionTestToken(37))); err != nil || !ack.GetAccepted() {
		t.Fatalf("clean restart remained fenced: AssignWork = (%+v, %v)", ack, err)
	}
}

func TestCleanDisarmRearmsBeforeConcurrentClaimCanBeAcknowledged(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	service := NewService("concurrent-clean-disarm", 10,
		WithExecutionEpochStatePath(statePath),
		WithRunSupervisionRequired(30*time.Second, 0),
	)
	defer shutdownSupervisionTestService(t, service)

	oldToken := supervisionTestToken(39)
	if ack, err := service.AssignWork(context.Background(), supervisedStageRequest("old-run", "old-script", 0, oldToken)); err != nil || !ack.GetAccepted() {
		t.Fatalf("old AssignWork = (%+v, %v)", ack, err)
	}
	if ack, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "old-run", RunSupervisionToken: oldToken, RequestedTtlMilliseconds: 30_000,
	}); err != nil || !ack.GetAccepted() {
		t.Fatalf("old ClaimRunSupervision = (%+v, %v)", ack, err)
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "old-run", RunSupervisionToken: oldToken}); err != nil || !ack.GetAccepted() {
		t.Fatalf("old CommitWork = (%+v, %v)", ack, err)
	}

	// Hold the state-store lock so StopTest reaches the clean-disarm lifecycle
	// boundary but cannot yet clear the durable bit.
	state := service.executionEpochManager.executionEpochState
	state.mu.Lock()
	stateLocked := true
	defer func() {
		if stateLocked {
			state.mu.Unlock()
		}
	}()
	type stopResult struct {
		ack *pb.StopAck
		err error
	}
	stopDone := make(chan stopResult, 1)
	go func() {
		ack, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "old-run", RunSupervisionToken: oldToken})
		stopDone <- stopResult{ack: ack, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		disarming := service.executionEpochManager.runSupervisionRestartFenceDisarming
		service.mu.RUnlock()
		if disarming {
			break
		}
		if time.Now().After(deadline) {
			state.mu.Unlock()
			stateLocked = false
			t.Fatal("StopTest did not enter durable clean-disarm")
		}
		time.Sleep(time.Millisecond)
	}

	newToken := supervisionTestToken(40)
	if ack, err := service.AssignWork(context.Background(), supervisedStageRequest("new-run", "new-script", 0, newToken)); err != nil || !ack.GetAccepted() {
		state.mu.Unlock()
		stateLocked = false
		t.Fatalf("concurrent AssignWork = (%+v, %v)", ack, err)
	}
	type claimResult struct {
		ack *pb.RunSupervisionAck
		err error
	}
	claimStarted := make(chan struct{})
	claimDone := make(chan claimResult, 1)
	go func() {
		close(claimStarted)
		ack, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
			TestId: "new-run", RunSupervisionToken: newToken, RequestedTtlMilliseconds: 30_000,
		})
		claimDone <- claimResult{ack: ack, err: err}
	}()
	<-claimStarted
	select {
	case result := <-claimDone:
		state.mu.Unlock()
		stateLocked = false
		t.Fatalf("claim crossed the durable clean-disarm boundary: (%+v, %v)", result.ack, result.err)
	case <-time.After(50 * time.Millisecond):
	}

	state.mu.Unlock()
	stateLocked = false
	select {
	case result := <-stopDone:
		if result.err != nil || !result.ack.GetAcknowledged() {
			t.Fatalf("StopTest = (%+v, %v)", result.ack, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for clean StopTest")
	}
	select {
	case result := <-claimDone:
		if result.err != nil || !result.ack.GetAccepted() {
			t.Fatalf("concurrent ClaimRunSupervision = (%+v, %v)", result.ack, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for concurrent ClaimRunSupervision")
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read execution epoch state: %v", err)
	}
	var record executionEpochStateRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode execution epoch state: %v", err)
	}
	if !record.RunSupervisionFenceArmed {
		t.Fatal("concurrent claim was acknowledged without a durable restart fence")
	}
	if stopped, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "new-run", RunSupervisionToken: newToken}); err != nil || !stopped.GetAcknowledged() {
		t.Fatalf("new StopTest = (%+v, %v)", stopped, err)
	}
}

func TestCleanDisarmCompensatesPostRenameFailureBeforeNextClaim(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	service := NewService("post-rename-clean-disarm", 10,
		WithExecutionEpochStatePath(statePath),
		WithRunSupervisionRequired(30*time.Second, 0),
	)
	defer shutdownSupervisionTestService(t, service)

	oldToken := supervisionTestToken(41)
	if ack, err := service.AssignWork(context.Background(), supervisedStageRequest("old-run", "old-script", 0, oldToken)); err != nil || !ack.GetAccepted() {
		t.Fatalf("old AssignWork = (%+v, %v)", ack, err)
	}
	if ack, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "old-run", RunSupervisionToken: oldToken, RequestedTtlMilliseconds: 30_000,
	}); err != nil || !ack.GetAccepted() {
		t.Fatalf("old ClaimRunSupervision = (%+v, %v)", ack, err)
	}
	if ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "old-run", RunSupervisionToken: oldToken}); err != nil || !ack.GetAccepted() {
		t.Fatalf("old CommitWork = (%+v, %v)", ack, err)
	}

	state := service.executionEpochManager.executionEpochState
	state.mu.Lock()
	failClearedRenameOnce := true
	state.postRenameForTest = func(record executionEpochStateRecord) error {
		if failClearedRenameOnce && !record.RunSupervisionFenceArmed {
			failClearedRenameOnce = false
			return errors.New("injected directory sync failure after rename")
		}
		return nil
	}
	state.mu.Unlock()
	if stopped, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "old-run", RunSupervisionToken: oldToken}); err != nil || !stopped.GetAcknowledged() {
		t.Fatalf("old StopTest = (%+v, %v)", stopped, err)
	}
	state.mu.Lock()
	state.postRenameForTest = nil
	state.mu.Unlock()

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read compensated execution epoch state: %v", err)
	}
	var record executionEpochStateRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode compensated execution epoch state: %v", err)
	}
	if !record.RunSupervisionFenceArmed {
		t.Fatalf("post-rename disarm failure was not durably re-armed: %s", raw)
	}

	newToken := supervisionTestToken(42)
	if ack, err := service.AssignWork(context.Background(), supervisedStageRequest("new-run", "new-script", 0, newToken)); err != nil || !ack.GetAccepted() {
		t.Fatalf("new AssignWork = (%+v, %v)", ack, err)
	}
	if ack, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
		TestId: "new-run", RunSupervisionToken: newToken, RequestedTtlMilliseconds: 30_000,
	}); err != nil || !ack.GetAccepted() {
		t.Fatalf("new ClaimRunSupervision = (%+v, %v)", ack, err)
	}
	if stopped, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "new-run", RunSupervisionToken: newToken}); err != nil || !stopped.GetAcknowledged() {
		t.Fatalf("new StopTest = (%+v, %v)", stopped, err)
	}
}

func TestRunSupervisionExpiryTracksDetachedExecutionAndRequestsExit(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 0))
	service.runSupervisionHardGrace = time.Millisecond
	token := supervisionTestToken(3)
	done := make(chan struct{})
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", vuserCount: 4, runSupervisionTokenHash: runSupervisionTokenHash(token),
		groups: map[string]int32{"group": 4}, activeGroups: map[string]int32{"group": 4},
	}
	service.executions["run"] = map[string]*groupExecution{
		"group": {groupName: "group", done: done, cancel: func() {}},
	}
	lease := &runSupervisionLease{tokenHash: runSupervisionTokenHash(token), ttl: 60 * time.Second, deadline: time.Now().Add(-time.Second), timerToken: 1}
	service.runSupervisions["run"] = lease
	service.mu.Unlock()

	service.expireRunSupervision("run", 1)
	select {
	case request := <-service.SelfTerminationRequested():
		if request.TestID != "run" || request.Outstanding != 1 || request.Reason != runSupervisionFailureReason {
			t.Fatalf("self-termination request = %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for self-termination request")
	}
	service.mu.RLock()
	_, stillAssigned := service.assignments["run"]
	reserved := service.runSupervisionDrainingVUsers
	draining := service.draining
	service.mu.RUnlock()
	if stillAssigned || reserved != 4 || !draining {
		t.Fatalf("detached state: assigned=%v reserved=%d draining=%v", stillAssigned, reserved, draining)
	}
	if got := service.runSupervisionExpirations.Load(); got != 1 {
		t.Fatalf("run supervision expiration counter = %d, want 1", got)
	}
	if got := service.runSupervisionEscalations.Load(); got != 1 {
		t.Fatalf("run supervision escalation counter = %d, want 1", got)
	}
}

func TestRunSupervisionClaimCannotRenewAcrossSemanticDeadlineWhileWaitingForLock(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	token := supervisionTestToken(7)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", runSupervisionTokenHash: runSupervisionTokenHash(token), groups: map[string]int32{},
	}
	service.runSupervisions["run"] = &runSupervisionLease{
		tokenHash: runSupervisionTokenHash(token), ttl: 60 * time.Second,
		deadline: time.Now().Add(15 * time.Millisecond), timerToken: 1,
		state: pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE,
	}
	result := make(chan *pb.RunSupervisionAck, 1)
	errs := make(chan error, 1)
	go func() {
		ack, err := service.ClaimRunSupervision(context.Background(), &pb.RunSupervisionClaim{
			TestId: "run", RunSupervisionToken: token, RequestedTtlMilliseconds: 60_000,
		})
		result <- ack
		errs <- err
	}()
	time.Sleep(30 * time.Millisecond)
	service.mu.Unlock()

	if err := <-errs; err != nil {
		t.Fatalf("ClaimRunSupervision error = %v", err)
	}
	if ack := <-result; ack.GetAccepted() || ack.GetState() != pb.RunSupervisionState_RUN_SUPERVISION_STATE_FENCED {
		t.Fatalf("claim after semantic deadline = %+v, want fenced", ack)
	}
}

func TestDetachedRunRetainsLeaseAndCapacityUntilExactDoneBarrier(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	service.runSupervisionHardGrace = time.Second
	token := supervisionTestToken(8)
	done := make(chan struct{})
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", committed: true, vuserCount: 3,
		runSupervisionTokenHash: runSupervisionTokenHash(token),
		groups:                  map[string]int32{"group": 3}, activeGroups: map[string]int32{"group": 3},
	}
	service.executions["run"] = map[string]*groupExecution{
		"group": {groupName: "group", done: done, cancel: func() {}},
	}
	service.runSupervisions["run"] = &runSupervisionLease{
		tokenHash: runSupervisionTokenHash(token), deadline: time.Now().Add(time.Minute),
		state: pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE,
	}
	service.mu.Unlock()

	stops := service.detachFencedTests([]string{"run"}, 0, false)
	if len(stops) != 1 {
		t.Fatalf("detached tests = %d, want 1", len(stops))
	}
	result := service.startDetachedRunDrain(stops[0], false)
	service.mu.RLock()
	lease := service.runSupervisions["run"]
	reserved := service.runSupervisionDrainingVUsers
	service.mu.RUnlock()
	if lease == nil || lease.state != pb.RunSupervisionState_RUN_SUPERVISION_STATE_STOPPING || reserved != 3 {
		t.Fatalf("retained drain state lease=%+v reserved=%d", lease, reserved)
	}
	select {
	case err := <-result:
		t.Fatalf("drain completed before exact done closed: %v", err)
	default:
	}

	close(done)
	if err := <-result; err != nil {
		t.Fatalf("finish detached run: %v", err)
	}
	service.mu.RLock()
	_, leaseRetained := service.runSupervisions["run"]
	reserved = service.runSupervisionDrainingVUsers
	service.mu.RUnlock()
	if leaseRetained || reserved != 0 {
		t.Fatalf("completed drain retained lease=%v reserved=%d", leaseRetained, reserved)
	}
}

func TestExpiredRunLeaseRejectsPauseScaleAndDurationAtomically(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	token := supervisionTestToken(9)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", committed: true, vuserCount: 1, duration: time.Minute, durationSeconds: 60,
		runSupervisionTokenHash: runSupervisionTokenHash(token),
		groups:                  map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
	}
	service.runSupervisions["run"] = &runSupervisionLease{
		tokenHash: runSupervisionTokenHash(token), deadline: time.Now().Add(-time.Millisecond),
		state: pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE,
	}
	service.mu.Unlock()

	operations := []struct {
		name string
		run  func() error
	}{
		{name: "pause", run: func() error {
			_, err := service.SetPaused(context.Background(), &pb.PauseRequest{TestId: "run", Pause: true, OperationId: "pause", OperationSequence: 1, RunSupervisionToken: token})
			return err
		}},
		{name: "metrics pause", run: func() error {
			return service.setPaused(context.Background(), "run", true, 0, supervisionTestToken(10))
		}},
		{name: "scale", run: func() error {
			_, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: "run", GroupName: "group", TargetVusers: 2, RunSupervisionToken: token})
			return err
		}},
		{name: "duration", run: func() error {
			_, err := service.AdjustDuration(context.Background(), &pb.DurationAdjustment{TestId: "run", TargetDurationSeconds: 120, RunSupervisionToken: token})
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("error = %v, want FailedPrecondition", err)
			}
		})
	}
}

func TestRunSupervisionCrashIntentIsTokenFreeAndReconcilesTerminalEvidence(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	service := NewService("generator", 10, WithExecutionEpochStatePath(statePath))
	const crashedOwner = "controller/crashed"
	claim, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: 7, LeaseTtlMilliseconds: time.Minute.Milliseconds(), ExecutionLeaseOwnerId: crashedOwner,
	})
	if err != nil || !claim.GetAccepted() {
		t.Fatalf("claim crashed execution generation = (%#v, %v)", claim, err)
	}
	requestedAt := time.Now().UTC().Truncate(time.Millisecond)
	request := SelfTerminationRequest{
		TestID: "lost-run", ExecutionGeneration: 7, Reason: runSupervisionFailureReason,
		Outstanding: 2, RequestedAt: requestedAt,
	}
	incidents := []SelfTerminationRequest{
		request,
		{TestID: "later-run", ExecutionGeneration: 7, Reason: stopEscalationReason, Outstanding: 1, RequestedAt: requestedAt.Add(time.Second)},
		// Generation-zero test IDs may be reused with a fresh run token. The token
		// cannot enter durable evidence, so requested_at is the token-free incident
		// identity that prevents the later incarnation from overwriting the first.
		{TestID: "generation-zero-run", Reason: stopEscalationReason, Outstanding: 3, RequestedAt: requestedAt.Add(2*time.Second + 100*time.Nanosecond)},
		{TestID: "generation-zero-run", Reason: testFailureEscalationReason, Outstanding: 4, RequestedAt: requestedAt.Add(2*time.Second + 200*time.Nanosecond)},
	}
	for _, incident := range incidents {
		if err := service.persistRunSupervisionCrashIntent(incident); err != nil {
			t.Fatalf("persist crash intent %+v: %v", incident, err)
		}
	}
	raw, err := os.ReadFile(statePath + ".generator-safety-exit.json")
	if err != nil {
		t.Fatalf("read crash intent: %v", err)
	}
	if bytes.Contains(raw, []byte("token")) || bytes.Contains(raw, supervisionTestToken(11)) {
		t.Fatalf("crash intent disclosed token material: %s", raw)
	}
	records, err := decodeRunSupervisionCrashJournal(raw)
	if err != nil {
		t.Fatalf("decode crash intent: %v", err)
	}
	if len(records) != len(incidents) {
		t.Fatalf("crash journal records = %+v, want %d distinct incidents", records, len(incidents))
	}
	if records[2].TestID != records[3].TestID || records[2].ExecutionGeneration != 0 || records[3].ExecutionGeneration != 0 ||
		records[2].Reason == records[3].Reason || records[2].RequestedAt.Equal(records[3].RequestedAt) {
		t.Fatalf("same-id generation-zero incidents were not retained distinctly: %+v", records[2:])
	}
	service.runSupervisionIntentMu.Lock()
	if err := service.executionEpochManager.executionEpochState.close(); err != nil {
		service.runSupervisionIntentMu.Unlock()
		t.Fatalf("close first state store: %v", err)
	}
	service.runSupervisionIntentMu.Unlock()

	restarted := NewService("generator", 10, WithExecutionEpochStatePath(statePath))
	terminal := restarted.metricsBroker.terminalBatch(terminalBatchKey{testID: "lost-run", generation: 7})
	if terminal == nil || terminal.GetLifecycleState() != "FAILED" || terminal.GetLifecycleError() != runSupervisionFailureReason {
		t.Fatalf("reconciled terminal evidence = %+v", terminal)
	}
	if got := restarted.runSupervisionEscalations.Load(); got != 1 {
		t.Fatalf("first restart escalation count = %d, want 1", got)
	}
	zeroTerminal := restarted.metricsBroker.terminalBatch(terminalBatchKey{testID: "generation-zero-run", generation: 0})
	if zeroTerminal == nil || zeroTerminal.GetLifecycleState() != "FAILED" || zeroTerminal.GetLifecycleError() != testFailureEscalationReason {
		t.Fatalf("latest same-ID/generation terminal projection = %+v", zeroTerminal)
	}
	staleClaim, err := restarted.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: 7, LeaseTtlMilliseconds: time.Minute.Milliseconds(), ExecutionLeaseOwnerId: crashedOwner,
	})
	if err != nil {
		t.Fatalf("reclaim crashed execution generation: %v", err)
	}
	if staleClaim.GetAccepted() || staleClaim.GetErrorMessage() != ExecutionGenerationExpiredMessage {
		t.Fatalf("crashed execution generation was revived after restart: %#v", staleClaim)
	}
	const replacementOwner = "controller/replacement"
	replacementClaim, err := restarted.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: 8, LeaseTtlMilliseconds: time.Minute.Milliseconds(), ExecutionLeaseOwnerId: replacementOwner,
	})
	if err != nil || !replacementClaim.GetAccepted() {
		t.Fatalf("claim replacement execution generation = (%#v, %v)", replacementClaim, err)
	}
	marked, err := os.ReadFile(statePath + ".generator-safety-exit.json")
	if err != nil {
		t.Fatalf("read reconciled crash intent: %v", err)
	}
	reconciledRecords, err := decodeRunSupervisionCrashJournal(marked)
	if err != nil || len(reconciledRecords) != len(incidents) {
		t.Fatalf("reconciled crash journal = (%+v, %v), want all incidents", reconciledRecords, err)
	}
	for _, record := range reconciledRecords {
		if !record.Reconciled {
			t.Fatalf("crash incident lacks durable reconciliation marker: %+v", record)
		}
	}
	restarted.runSupervisionIntentMu.Lock()
	if err := restarted.executionEpochManager.executionEpochState.close(); err != nil {
		restarted.runSupervisionIntentMu.Unlock()
		t.Fatalf("close first restarted state store: %v", err)
	}
	restarted.runSupervisionIntentMu.Unlock()

	restartedAgain := NewService("generator", 10, WithExecutionEpochStatePath(statePath))
	revalidated, err := restartedAgain.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: 8, LeaseTtlMilliseconds: time.Minute.Milliseconds(), ExecutionLeaseOwnerId: replacementOwner,
	})
	if err != nil || !revalidated.GetAccepted() {
		t.Fatalf("revalidate later non-crashed execution generation = (%#v, %v)", revalidated, err)
	}
	terminal = restartedAgain.metricsBroker.terminalBatch(terminalBatchKey{testID: "lost-run", generation: 7})
	if terminal == nil || terminal.GetLifecycleState() != "FAILED" || terminal.GetLifecycleError() != runSupervisionFailureReason {
		t.Fatalf("second-restart terminal evidence = %+v", terminal)
	}
	if got := restartedAgain.runSupervisionEscalations.Load(); got != 0 {
		t.Fatalf("second restart replayed escalation counter %d times, want 0", got)
	}
	// A new gRPC client models the replacement controller after both processes
	// restarted. The exact terminal reason must remain externally recoverable even
	// though the interrupted generator archive never reached normal finalization.
	client := supervisionHeartbeatClient(t, restartedAgain)
	heartbeat, err := client.OpenHeartbeat(context.Background())
	if err != nil {
		t.Fatalf("open replacement-controller heartbeat: %v", err)
	}
	defer heartbeat.CloseSend()
	if err := heartbeat.Send(Heartbeat{GeneratorID: "replacement-controller", TimestampUnixMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("send replacement-controller heartbeat: %v", err)
	}
	pong, err := heartbeat.Receive()
	if err != nil {
		t.Fatalf("receive replacement-controller heartbeat: %v", err)
	}
	if len(pong.SafetyExits) != len(incidents) {
		t.Fatalf("restart-recovered safety-exit history = %+v, want %d incidents", pong.SafetyExits, len(incidents))
	}
	for index, incident := range incidents {
		evidence := pong.SafetyExits[index]
		if evidence.TestID != incident.TestID || evidence.ExecutionGeneration != incident.ExecutionGeneration ||
			evidence.LifecycleState != "FAILED" || evidence.Reason != incident.Reason ||
			evidence.OutstandingExecutions != int32(incident.Outstanding) ||
			!evidence.RequestedAt.Equal(incident.RequestedAt) {
			t.Fatalf("restart-recovered safety-exit evidence[%d] = %+v, want %+v", index, evidence, incident)
		}
	}
	lastIncident := incidents[len(incidents)-1]
	if evidence := pong.LastSafetyExit; evidence == nil || evidence.TestID != lastIncident.TestID ||
		evidence.ExecutionGeneration != lastIncident.ExecutionGeneration || evidence.Reason != lastIncident.Reason ||
		!evidence.RequestedAt.Equal(lastIncident.RequestedAt) {
		t.Fatalf("rolling-compatible last safety exit = %+v", evidence)
	}
	restartedAgain.runSupervisionIntentMu.Lock()
	if err := restartedAgain.executionEpochManager.executionEpochState.close(); err != nil {
		restartedAgain.runSupervisionIntentMu.Unlock()
		t.Fatalf("close second restarted state store: %v", err)
	}
	restartedAgain.runSupervisionIntentMu.Unlock()
}

func TestRunSupervisionCrashJournalIsBoundedByIncidentNotGeneration(t *testing.T) {
	requestedAt := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	var records []runSupervisionCrashIntent
	for index := range maxRunSupervisionCrashEntries + 2 {
		records = appendRunSupervisionCrashRecord(records, runSupervisionCrashIntent{
			Version: runSupervisionCrashVersion, TestID: "reused-generation-zero-run",
			Reason: stopEscalationReason, RequestedAt: requestedAt.Add(time.Duration(index) * time.Second),
		})
	}
	if len(records) != maxRunSupervisionCrashEntries {
		t.Fatalf("bounded journal length = %d, want %d", len(records), maxRunSupervisionCrashEntries)
	}
	if !records[0].RequestedAt.Equal(requestedAt.Add(2 * time.Second)) {
		t.Fatalf("bounded journal retained oldest evicted incident: first=%s", records[0].RequestedAt)
	}
	for index := 1; index < len(records); index++ {
		if !records[index].RequestedAt.After(records[index-1].RequestedAt) {
			t.Fatalf("journal incident order is not monotonic at %d: %+v", index, records)
		}
	}

	replacement := records[5]
	replacement.Reason = testFailureEscalationReason
	records = appendRunSupervisionCrashRecord(records, replacement)
	if len(records) != maxRunSupervisionCrashEntries || records[len(records)-1].Reason != testFailureEscalationReason ||
		runSupervisionCrashKey(records[len(records)-1]) != runSupervisionCrashKey(replacement) {
		t.Fatalf("exact incident replacement did not remain bounded and newest: %+v", records)
	}
}

func TestRunSupervisionCrashJournalReadsLegacySingleton(t *testing.T) {
	legacy := runSupervisionCrashIntent{
		Version: runSupervisionCrashVersion, TestID: "legacy-run", ExecutionGeneration: 3,
		Reason: runSupervisionFailureReason, Outstanding: 1, RequestedAt: time.Now().UTC(),
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy singleton: %v", err)
	}
	records, err := decodeRunSupervisionCrashJournal(raw)
	if err != nil || len(records) != 1 || runSupervisionCrashKey(records[0]) != runSupervisionCrashKey(legacy) || records[0].Reason != legacy.Reason {
		t.Fatalf("decode legacy singleton = (%+v, %v)", records, err)
	}
}

func TestCorruptRunSupervisionCrashIntentMakesServiceUnready(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	service := NewService("generator", 10, WithExecutionEpochStatePath(statePath))
	service.runSupervisionIntentMu.Lock()
	if err := service.executionEpochManager.executionEpochState.close(); err != nil {
		service.runSupervisionIntentMu.Unlock()
		t.Fatalf("close first state store: %v", err)
	}
	service.runSupervisionIntentMu.Unlock()
	if err := os.WriteFile(statePath+".generator-safety-exit.json", []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt safety-exit evidence: %v", err)
	}

	restarted := NewService("generator", 10, WithExecutionEpochStatePath(statePath))
	err := restarted.ReadinessError()
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("generator safety-exit evidence")) {
		t.Fatalf("ReadinessError = %v, want corrupt safety-exit evidence rejection", err)
	}
	restarted.runSupervisionIntentMu.Lock()
	if closeErr := restarted.executionEpochManager.executionEpochState.close(); closeErr != nil {
		restarted.runSupervisionIntentMu.Unlock()
		t.Fatalf("close restarted state store: %v", closeErr)
	}
	restarted.runSupervisionIntentMu.Unlock()
}

func TestSupervisedStopRequiresExactTokenWhileActiveStoppingAndTerminal(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 5*time.Second))
	service.runSupervisionHardGrace = time.Second
	token := supervisionTestToken(12)
	done := make(chan struct{})
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", committed: true, vuserCount: 1,
		runSupervisionTokenHash: runSupervisionTokenHash(token),
		groups:                  map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
	}
	service.executions["run"] = map[string]*groupExecution{
		"group": {groupName: "group", done: done, cancel: func() {}},
	}
	service.runSupervisions["run"] = &runSupervisionLease{
		tokenHash: runSupervisionTokenHash(token), deadline: time.Now().Add(time.Minute),
		state: pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE,
	}
	service.mu.Unlock()

	assertRejected := func(t *testing.T, supplied []byte) {
		t.Helper()
		_, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run", RunSupervisionToken: supplied})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("StopTest token=%x error = %v, want FailedPrecondition", supplied, err)
		}
	}
	assertRejected(t, nil)
	assertRejected(t, supervisionTestToken(13))

	stops := service.detachFencedTests([]string{"run"}, 0, false)
	if len(stops) != 1 {
		t.Fatalf("detached tests = %d, want 1", len(stops))
	}
	result := service.startDetachedRunDrain(stops[0], false)
	assertRejected(t, nil)
	assertRejected(t, supervisionTestToken(13))

	close(done)
	if err := <-result; err != nil {
		t.Fatalf("finish detached run: %v", err)
	}
	assertRejected(t, nil)
	assertRejected(t, supervisionTestToken(13))
	ack, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run", RunSupervisionToken: token})
	if err != nil || !ack.GetAcknowledged() {
		t.Fatalf("exact-token terminal StopTest = (%+v, %v)", ack, err)
	}
}

func TestNonLeaseDrainEscalationUsesDistinctReasonAndCounter(t *testing.T) {
	service := newIsolatedService(t, 10, WithRunSupervisionRequired(60*time.Second, 0))
	service.runSupervisionHardGrace = time.Millisecond
	token := supervisionTestToken(14)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{
		testID: "run", committed: true, vuserCount: 1,
		runSupervisionTokenHash: runSupervisionTokenHash(token),
		groups:                  map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
	}
	service.executions["run"] = map[string]*groupExecution{
		"group": {groupName: "group", done: make(chan struct{}), cancel: func() {}},
	}
	service.runSupervisions["run"] = &runSupervisionLease{
		tokenHash: runSupervisionTokenHash(token), deadline: time.Now().Add(time.Minute),
		state: pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE,
	}
	service.mu.Unlock()

	stops := service.detachFencedTests([]string{"run"}, 0, false)
	stops[0].escalationReason = stopEscalationReason
	result := service.startDetachedRunDrain(stops[0], false)
	select {
	case request := <-service.SelfTerminationRequested():
		if request.Reason != stopEscalationReason {
			t.Fatalf("self-termination reason = %q, want %q", request.Reason, stopEscalationReason)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for self-termination request")
	}
	<-result
	if got := service.runSupervisionEscalations.Load(); got != 0 {
		t.Fatalf("ordinary stop incremented supervision escalation counter to %d", got)
	}
}

func TestConcurrentDetachedRunEscalationsAllPersistAfterOnceOnlyExitRequest(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	service := NewService("generator", 10, WithExecutionEpochStatePath(statePath), WithRunSupervisionRequired(60*time.Second, 0))
	service.runSupervisionHardGrace = time.Millisecond
	doneByTest := map[string]chan struct{}{"run-a": make(chan struct{}), "run-b": make(chan struct{})}
	service.mu.Lock()
	for testID, done := range doneByTest {
		service.assignments[testID] = assignedTest{
			testID: testID, committed: true, vuserCount: 1,
			groups: map[string]int32{"group": 1}, activeGroups: map[string]int32{"group": 1},
		}
		service.executions[testID] = map[string]*groupExecution{
			"group": {groupName: "group", done: done, cancel: func() {}},
		}
	}
	service.mu.Unlock()
	stops := service.detachFencedTests([]string{"run-a", "run-b"}, 0, false)
	if len(stops) != 2 {
		t.Fatalf("detached tests = %d, want 2", len(stops))
	}
	results := make([]<-chan error, 0, len(stops))
	for index := range stops {
		stops[index].escalationReason = stopEscalationReason
		results = append(results, service.startDetachedRunDrain(stops[index], false))
	}
	for _, result := range results {
		select {
		case <-result:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for detached escalation")
		}
	}
	select {
	case <-service.SelfTerminationRequested():
	case <-time.After(time.Second):
		t.Fatal("process self-termination was not requested")
	}
	select {
	case duplicate := <-service.SelfTerminationRequested():
		t.Fatalf("process self-termination was requested more than once: %+v", duplicate)
	default:
	}

	service.runSupervisionIntentMu.Lock()
	records, err := service.readRunSupervisionCrashJournalLocked()
	service.runSupervisionIntentMu.Unlock()
	if err != nil {
		t.Fatalf("read concurrent escalation journal: %v", err)
	}
	retained := make(map[string]bool, len(records))
	for _, record := range records {
		retained[record.TestID] = true
	}
	if len(records) != 2 || !retained["run-a"] || !retained["run-b"] {
		t.Fatalf("concurrent escalation journal = %+v, want both exact runs", records)
	}
	for _, done := range doneByTest {
		close(done)
	}
	service.runSupervisionIntentMu.Lock()
	closeErr := service.executionEpochManager.executionEpochState.close()
	service.runSupervisionIntentMu.Unlock()
	if closeErr != nil {
		t.Fatalf("close state store: %v", closeErr)
	}
}
