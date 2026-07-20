package generatorrpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type shortExecutionEpochWriter struct{}

func (shortExecutionEpochWriter) Write(content []byte) (int, error) {
	if len(content) == 0 {
		return 0, nil
	}
	return len(content) - 1, nil
}

func TestWriteExecutionEpochStateRejectsShortWrite(t *testing.T) {
	if err := writeExecutionEpochState(shortExecutionEpochWriter{}, []byte("durable-state")); err == nil {
		t.Fatal("short execution epoch state write unexpectedly succeeded")
	}
}

func newEpochTestService(t *testing.T, statePath string) *Service {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("epoch-test", 10, WithExecutionEpochStatePath(statePath))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return service
}

func claimEpoch(t *testing.T, service *Service, generation uint64, ttl time.Duration) *pb.ExecutionEpochAck {
	return claimEpochForOwner(t, service, generation, fmt.Sprintf("controller/%d", generation), ttl)
}

func claimEpochForOwner(t *testing.T, service *Service, generation uint64, leaseOwnerID string, ttl time.Duration) *pb.ExecutionEpochAck {
	t.Helper()
	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   generation,
		LeaseTtlMilliseconds:  ttl.Milliseconds(),
		ExecutionLeaseOwnerId: leaseOwnerID,
	})
	if err != nil {
		t.Fatalf("ClaimExecutionEpoch(%d): %v", generation, err)
	}
	return ack
}

func distributeEpochScript(t *testing.T, service *Service, generation uint64, scriptID string, content []byte) string {
	t.Helper()
	hash := scriptContentHash(content)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{
		ScriptId:            scriptID,
		Content:             content,
		ContentHash:         hash,
		ExecutionGeneration: generation,
	}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	return hash
}

func assignEpochTest(t *testing.T, service *Service, generation uint64, testID, scriptID, expectedHash string) *pb.AssignmentAck {
	t.Helper()
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:              testID,
		GroupName:           "group",
		ScriptId:            scriptID,
		ExpectedScriptHash:  expectedHash,
		ExecutionGeneration: generation,
	})
	if err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	return ack
}

func TestExecutionEpochFencesOldAssignmentsControlsAndDistribution(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 10, time.Second); !ack.GetAccepted() {
		t.Fatalf("generation 10 rejected: %s", ack.GetErrorMessage())
	}
	oldContent := []byte(`{"name":"old"}`)
	oldHash := distributeEpochScript(t, service, 10, "script", oldContent)
	if ack := assignEpochTest(t, service, 10, "test-old", "script", oldHash); !ack.GetAccepted() {
		t.Fatalf("old assignment rejected: %s", ack.GetErrorMessage())
	}

	if ack := claimEpoch(t, service, 11, time.Second); !ack.GetAccepted() {
		t.Fatalf("generation 11 rejected: %s", ack.GetErrorMessage())
	}
	service.mu.RLock()
	_, oldStillAssigned := service.assignments["test-old"]
	service.mu.RUnlock()
	if oldStillAssigned {
		t.Fatal("lower-generation assignment survived a higher claim")
	}

	assertFailedPrecondition := func(name string, err error) {
		t.Helper()
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("%s error = %v (%s), want FailedPrecondition", name, err, status.Code(err))
		}
	}
	_, err := service.AdjustVUsers(context.Background(), &pb.VUserAdjustment{TestId: "test-old", GroupName: "group", ExecutionGeneration: 10})
	assertFailedPrecondition("old adjustment", err)
	_, err = service.AdjustDuration(context.Background(), &pb.DurationAdjustment{TestId: "test-old", TargetDurationSeconds: 1, ExecutionGeneration: 10})
	assertFailedPrecondition("old duration", err)
	_, err = service.StopTest(context.Background(), &pb.StopRequest{TestId: "test-old", ExecutionGeneration: 10})
	assertFailedPrecondition("old stop", err)
	_, err = service.StopTest(context.Background(), &pb.StopRequest{TestId: "test-old"})
	assertFailedPrecondition("legacy stop", err)
	_, err = service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script", Content: oldContent, ContentHash: oldHash, ExecutionGeneration: 10})
	assertFailedPrecondition("delayed old distribution", err)

	newContent := []byte(`{"name":"new"}`)
	newHash := distributeEpochScript(t, service, 11, "script", newContent)
	if ack := assignEpochTest(t, service, 11, "test-new", "script", newHash); !ack.GetAccepted() {
		t.Fatalf("new assignment rejected: %s", ack.GetErrorMessage())
	}
	service.mu.RLock()
	assignment := service.assignments["test-new"]
	service.mu.RUnlock()
	if assignment.executionGeneration != 11 {
		t.Fatalf("assignment generation = %d, want 11", assignment.executionGeneration)
	}
}

func TestExecutionEpochAssignmentVerifiesSelectedScriptHashAtomically(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 1, time.Second); !ack.GetAccepted() {
		t.Fatalf("claim rejected: %s", ack.GetErrorMessage())
	}
	content := []byte(`{"name":"selected"}`)
	actualHash := distributeEpochScript(t, service, 1, "script", content)
	wrongHash := scriptContentHash([]byte(`{"name":"other"}`))
	ack := assignEpochTest(t, service, 1, "wrong-hash", "script", wrongHash)
	if ack.GetAccepted() || ack.GetErrorMessage() == "" {
		t.Fatalf("mismatched expected hash accepted: %#v", ack)
	}
	service.mu.RLock()
	_, mutated := service.assignments["wrong-hash"]
	service.mu.RUnlock()
	if mutated {
		t.Fatal("hash-mismatched assignment mutated generator state")
	}
	if ack := assignEpochTest(t, service, 1, "right-hash", "script", actualHash); !ack.GetAccepted() {
		t.Fatalf("matching hash rejected: %s", ack.GetErrorMessage())
	}
}

func TestExecutionEpochExpiryStopsLoadAndCannotBeRevived(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 4, 60*time.Millisecond); !ack.GetAccepted() {
		t.Fatalf("claim rejected: %s", ack.GetErrorMessage())
	}
	hash := distributeEpochScript(t, service, 4, "script", []byte(`{"name":"expiring"}`))
	if ack := assignEpochTest(t, service, 4, "expiring", "script", hash); !ack.GetAccepted() {
		t.Fatalf("assignment rejected: %s", ack.GetErrorMessage())
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.RLock()
		_, assigned := service.assignments["expiring"]
		service.mu.RUnlock()
		if !assigned {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("assignment remained active after execution epoch expiry")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{ExecutionGeneration: 4, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/4"})
	if err != nil {
		t.Fatalf("expired same-generation claim returned RPC error: %v", err)
	}
	if ack.GetAccepted() {
		t.Fatal("expired execution generation was revived")
	}
	// Assignment detachment is the safety-critical first phase of expiry. The
	// generator deliberately keeps a second fence while the detached engine
	// quiesces and its archive finalizes; a higher generation must not be accepted
	// until that cleanup completes. Under full-suite contention this can outlive
	// the assignments-map deletion observed above.
	for {
		service.mu.RLock()
		pending := service.executionFencePending
		fencing := service.executionEpochFencing
		service.mu.RUnlock()
		if pending == 0 && !fencing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution epoch cleanup remained fenced: pending=%d fencing=%v", pending, fencing)
		}
		time.Sleep(time.Millisecond)
	}
	if ack := claimEpoch(t, service, 5, time.Second); !ack.GetAccepted() {
		t.Fatalf("higher generation rejected after expiry: %s", ack.GetErrorMessage())
	}
}

func TestExecutionEpochExpiryCancelsAndDetachesAllAssignmentsBeforeWaiting(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	const (
		generation = uint64(41)
		count      = maxExecutionFenceWorkers*2 + 3
	)
	if ack := claimEpoch(t, service, generation, time.Second); !ack.GetAccepted() {
		t.Fatalf("claim rejected: %s", ack.GetErrorMessage())
	}

	blockedDone := make(chan struct{})
	canceled := make(chan string, count)
	expiresAt := time.Now().Add(-time.Millisecond)
	service.mu.Lock()
	if service.executionEpochTimer != nil {
		service.executionEpochTimer.Stop()
	}
	service.executionExpiresAt = expiresAt
	for i := range count {
		testID := fmt.Sprintf("blocked-%02d", i)
		service.assignments[testID] = assignedTest{
			testID:              testID,
			executionGeneration: generation,
			groups:              map[string]int32{"group": 1},
			activeGroups:        map[string]int32{"group": 1},
			vuserCount:          1,
		}
		id := testID
		service.executions[testID] = map[string]*groupExecution{
			"group": {
				groupName: "group",
				cancel:    func() { canceled <- id },
				done:      blockedDone,
			},
		}
	}
	service.mu.Unlock()

	expired := make(chan struct{})
	go func() {
		service.expireExecutionEpoch(generation, expiresAt)
		close(expired)
	}()
	seen := make(map[string]struct{}, count)
	for range count {
		select {
		case testID := <-canceled:
			seen[testID] = struct{}{}
		case <-time.After(time.Second):
			t.Fatalf("only %d/%d assignments were canceled before bounded waits", len(seen), count)
		}
	}
	if len(seen) != count {
		t.Fatalf("distinct canceled assignments = %d, want %d", len(seen), count)
	}
	service.mu.RLock()
	remainingAssignments := len(service.assignments)
	remainingExecutions := len(service.executions)
	service.mu.RUnlock()
	if remainingAssignments != 0 || remainingExecutions != 0 {
		t.Fatalf("expired assignments were not synchronously detached: assignments=%d executions=%d", remainingAssignments, remainingExecutions)
	}
	select {
	case <-expired:
		t.Fatal("expiry cleanup returned while execution done channels were still blocked")
	default:
	}
	close(blockedDone)
	select {
	case <-expired:
	case <-time.After(2 * time.Second):
		t.Fatal("expiry cleanup did not complete after all executions quiesced")
	}
}

func TestExecutionEpochRetryRemainsFencedUntilDetachedCleanupFinishes(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 1, 5*time.Second); !ack.GetAccepted() {
		t.Fatalf("initial claim rejected: %s", ack.GetErrorMessage())
	}

	blockedDone := make(chan struct{})
	canceled := make(chan struct{})
	var cancelOnce sync.Once
	service.mu.Lock()
	service.assignments["blocked-old-term"] = assignedTest{
		testID:              "blocked-old-term",
		executionGeneration: 1,
		groups:              map[string]int32{"group": 1},
		activeGroups:        map[string]int32{"group": 1},
		vuserCount:          1,
	}
	service.executions["blocked-old-term"] = map[string]*groupExecution{
		"group": {
			groupName:           "group",
			executionGeneration: 1,
			cancel: func() {
				cancelOnce.Do(func() { close(canceled) })
			},
			done: blockedDone,
		},
	}
	service.mu.Unlock()

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	cleanupErr := service.forceStopFencedTests(cleanupCtx, []string{"blocked-old-term"}, 1)
	cleanupCancel()
	if cleanupErr == nil {
		t.Fatal("blocked detached cleanup unexpectedly completed")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("blocked old-term execution was not canceled before the cleanup timeout")
	}
	service.mu.RLock()
	pending := service.executionFencePending
	fencing := service.executionEpochFencing
	service.mu.RUnlock()
	if pending != 1 || !fencing {
		t.Fatalf("detached cleanup state = pending:%d fencing:%v, want 1/true", pending, fencing)
	}

	claim := &pb.ExecutionEpochClaim{
		ExecutionGeneration:   2,
		LeaseTtlMilliseconds:  (2 * time.Second).Milliseconds(),
		ExecutionLeaseOwnerId: "controller/2",
	}
	firstRetry, err := service.ClaimExecutionEpoch(context.Background(), claim)
	if err != nil {
		t.Fatalf("higher-generation claim with pending cleanup: %v", err)
	}
	if firstRetry.GetAccepted() {
		t.Fatal("higher generation was accepted while detached old-term work was still live")
	}
	secondRetry, err := service.ClaimExecutionEpoch(context.Background(), claim)
	if err != nil {
		t.Fatalf("same-generation retry with pending cleanup: %v", err)
	}
	if secondRetry.GetAccepted() {
		t.Fatal("same-generation retry bypassed pending detached cleanup")
	}

	close(blockedDone)
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		pending = service.executionFencePending
		fencing = service.executionEpochFencing
		service.mu.RUnlock()
		if pending == 0 && !fencing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("detached cleanup did not release epoch fence: pending=%d fencing=%v", pending, fencing)
		}
		time.Sleep(time.Millisecond)
	}
	finalRetry, err := service.ClaimExecutionEpoch(context.Background(), claim)
	if err != nil {
		t.Fatalf("same-generation retry after cleanup: %v", err)
	}
	if !finalRetry.GetAccepted() {
		t.Fatalf("same generation remained fenced after detached cleanup: %s", finalRetry.GetErrorMessage())
	}
}

func TestExecutionEpochFenceArchivesAsFailedEvidence(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 1, time.Second); !ack.GetAccepted() {
		t.Fatalf("initial claim rejected: %s", ack.GetErrorMessage())
	}
	writer, err := newArchiveRecorderInNamespace(service.archiveNamespace, "fenced-evidence", "Fenced Evidence")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	service.mu.Lock()
	service.assignments["fenced-evidence"] = assignedTest{
		testID:              "fenced-evidence",
		executionGeneration: 1,
		groups:              map[string]int32{"group": 1},
	}
	service.executions["fenced-evidence"] = map[string]*groupExecution{
		"group": {groupName: "group", executionGeneration: 1, cancel: func() {}, done: done},
	}
	service.archiveWriters["fenced-evidence"] = writer
	service.mu.Unlock()

	if err := service.forceStopFencedTests(context.Background(), []string{"fenced-evidence"}, 1); err != nil {
		t.Fatalf("force stop fenced evidence: %v", err)
	}
	writer.mu.Lock()
	statusValue := writer.manifest.Status
	errorValue := writer.manifest.Error
	writer.mu.Unlock()
	if statusValue != "FAILED" || !strings.Contains(strings.ToLower(errorValue), "fenced") {
		t.Fatalf("fenced archive terminal evidence = status:%q error:%q, want FAILED/fenced", statusValue, errorValue)
	}
}

func TestExecutionEpochDelayedSameGenerationClaimCannotShortenExpiry(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	first := claimEpoch(t, service, 6, 500*time.Millisecond)
	if !first.GetAccepted() {
		t.Fatalf("initial claim rejected: %s", first.GetErrorMessage())
	}
	time.Sleep(10 * time.Millisecond)
	delayed := claimEpoch(t, service, 6, 100*time.Millisecond)
	if !delayed.GetAccepted() {
		t.Fatalf("delayed renewal rejected: %s", delayed.GetErrorMessage())
	}
	if delayed.GetExpiresAtUnixMs() < first.GetExpiresAtUnixMs() {
		t.Fatalf("delayed renewal shortened expiry from %d to %d", first.GetExpiresAtUnixMs(), delayed.GetExpiresAtUnixMs())
	}
	// The shorter delayed claim would have expired by now. A fenced mutation
	// must remain valid under the original later expiry.
	time.Sleep(150 * time.Millisecond)
	content := []byte(`{"name":"still-live"}`)
	hash := scriptContentHash(content)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{
		ScriptId:            "still-live",
		Content:             content,
		ContentHash:         hash,
		ExecutionGeneration: 6,
	}); err != nil {
		t.Fatalf("mutation after delayed shorter renewal: %v", err)
	}
}

func TestExecutionEpochExpiryAlsoStopsStrandedLegacyWork(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 8, 80*time.Millisecond); !ack.GetAccepted() {
		t.Fatalf("claim rejected: %s", ack.GetErrorMessage())
	}
	service.mu.Lock()
	service.assignments["stranded-legacy"] = assignedTest{
		testID:              "stranded-legacy",
		executionGeneration: 0,
		groups:              map[string]int32{"legacy": 3},
		activeGroups:        map[string]int32{"legacy": 3},
		vuserCount:          3,
	}
	service.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		service.mu.RLock()
		_, exists := service.assignments["stranded-legacy"]
		service.mu.RUnlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("legacy assignment survived positive execution-session expiry")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExecutionEpochStaleFenceCleanupCannotStopNewerAssignment(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	service.mu.Lock()
	service.assignments["reused-id"] = assignedTest{
		testID:              "reused-id",
		executionGeneration: 12,
		groups:              map[string]int32{"new": 2},
		activeGroups:        map[string]int32{"new": 2},
		vuserCount:          2,
	}
	service.mu.Unlock()
	if err := service.forceStopFencedTests(context.Background(), []string{"reused-id"}, 11); err != nil {
		t.Fatalf("stale fence cleanup: %v", err)
	}
	service.mu.RLock()
	assignment, exists := service.assignments["reused-id"]
	service.mu.RUnlock()
	if !exists || assignment.executionGeneration != 12 {
		t.Fatalf("stale fence cleanup removed newer assignment: exists=%v assignment=%+v", exists, assignment)
	}
}

func TestExecutionEpochHighWaterSurvivesServiceRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	service1 := newEpochTestService(t, statePath)
	if ack := claimEpoch(t, service1, 27, time.Second); !ack.GetAccepted() {
		t.Fatalf("initial claim rejected: %s", ack.GetErrorMessage())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := service1.Shutdown(ctx); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	cancel()

	service2 := newEpochTestService(t, statePath)
	if service2.executionGeneration != 27 {
		t.Fatalf("reloaded high-water = %d, want 27", service2.executionGeneration)
	}
	if _, err := service2.AssignWork(context.Background(), &pb.WorkAssignment{TestId: "legacy", GroupName: "group", ScriptId: "script"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("legacy assignment after restart error = %v, want FailedPrecondition", err)
	}
	wrongOwnerAck, err := service2.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{ExecutionGeneration: 27, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller-other/27"})
	if err != nil {
		t.Fatalf("wrong-owner restart claim: %v", err)
	}
	if wrongOwnerAck.GetAccepted() {
		t.Fatal("replacement generator admitted a different owner for the persisted generation")
	}
	if oldAck := claimEpochForOwner(t, service2, 26, "controller/26", time.Second); oldAck.GetAccepted() {
		t.Fatal("replacement generator admitted a controller generation below its durable high-water")
	}
	ack, err := service2.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{ExecutionGeneration: 27, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/27"})
	if err != nil {
		t.Fatalf("same-generation restart claim: %v", err)
	}
	if !ack.GetAccepted() {
		t.Fatalf("exact live controller term was not revalidated after restart: %s", ack.GetErrorMessage())
	}
	if ack := claimEpoch(t, service2, 28, time.Second); !ack.GetAccepted() {
		t.Fatalf("higher post-restart claim rejected: %s", ack.GetErrorMessage())
	}
}

func TestExecutionEpochCorruptStateFailsClosed(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	if err := os.WriteFile(statePath, []byte(`{"version":1,"generation":`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := newEpochTestService(t, statePath)
	if service.executionEpochStateErr == nil {
		t.Fatal("corrupt durable state did not fail initialization")
	}
	if err := service.ReadinessError(); err == nil {
		t.Fatal("service advertised readiness with corrupt durable state")
	}
	if _, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{ExecutionGeneration: 1, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/1"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("claim with corrupt state error = %v, want FailedPrecondition", err)
	}
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "script"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("legacy mutation with corrupt state error = %v, want FailedPrecondition", err)
	}
}

func TestExecutionEpochRuntimePersistenceFailureRevokesReadiness(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	service := newEpochTestService(t, statePath)
	if ack := claimEpoch(t, service, 1, time.Second); !ack.GetAccepted() {
		t.Fatalf("initial claim rejected: %s", ack.GetErrorMessage())
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   2,
		LeaseTtlMilliseconds:  1000,
		ExecutionLeaseOwnerId: "controller/2",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("claim with failed runtime persistence = %v (%s), want FailedPrecondition", err, status.Code(err))
	}
	if err := service.ReadinessError(); err == nil {
		t.Fatal("service remained ready after its durable high-water store failed")
	}
	select {
	case <-service.ReadinessChanges():
	case <-time.After(time.Second):
		t.Fatal("runtime high-water failure did not notify the process health watcher")
	}
}

func TestExecutionEpochClaimRejectedWhileGeneratorIsDraining(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	service.SetDraining(true)
	if _, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   1,
		LeaseTtlMilliseconds:  1000,
		ExecutionLeaseOwnerId: "controller/1",
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("claim while draining error = %v (%s), want Unavailable", err, status.Code(err))
	}
	service.mu.RLock()
	generation := service.executionGeneration
	service.mu.RUnlock()
	if generation != 0 {
		t.Fatalf("draining claim advanced execution high-water to %d", generation)
	}
}

func TestExecutionEpochPostCommitFenceCleanupOutlivesClaimCancellation(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 1, time.Second); !ack.GetAccepted() {
		t.Fatalf("initial claim rejected: %s", ack.GetErrorMessage())
	}
	stopStarted := make(chan struct{})
	allowQuiesce := make(chan struct{})
	var stopOnce sync.Once
	service.mu.Lock()
	service.assignments["old-load"] = assignedTest{
		testID:              "old-load",
		executionGeneration: 1,
		groups:              map[string]int32{"group": 1},
		activeGroups:        map[string]int32{"group": 1},
		vuserCount:          1,
	}
	service.executions["old-load"] = map[string]*groupExecution{
		"group": {
			groupName: "group",
			cancel:    func() { stopOnce.Do(func() { close(stopStarted) }) },
			done:      allowQuiesce,
		},
	}
	service.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	type claimResult struct {
		ack *pb.ExecutionEpochAck
		err error
	}
	result := make(chan claimResult, 1)
	go func() {
		ack, err := service.ClaimExecutionEpoch(ctx, &pb.ExecutionEpochClaim{
			ExecutionGeneration:   2,
			LeaseTtlMilliseconds:  1000,
			ExecutionLeaseOwnerId: "controller/2",
		})
		result <- claimResult{ack: ack, err: err}
	}()
	select {
	case <-stopStarted:
	case <-time.After(time.Second):
		t.Fatal("higher generation did not start fencing lower-generation work")
	}
	cancel()
	select {
	case got := <-result:
		t.Fatalf("claim returned before displaced execution quiesced: ack=%#v err=%v", got.ack, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(allowQuiesce)
	select {
	case got := <-result:
		if status.Code(got.err) != codes.Canceled {
			t.Fatalf("canceled post-commit claim error = %v (%s), want Canceled", got.err, status.Code(got.err))
		}
	case <-time.After(time.Second):
		t.Fatal("post-commit cleanup did not finish after displaced execution quiesced")
	}
	service.mu.RLock()
	_, stillAssigned := service.assignments["old-load"]
	generation := service.executionGeneration
	service.mu.RUnlock()
	if stillAssigned || generation != 2 {
		t.Fatalf("post-commit cleanup state = assigned:%v generation:%d, want false/2", stillAssigned, generation)
	}
}

func TestExecutionEpochProductionRequiresExplicitDurableStateDirectory(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv(executionEpochStateDirEnv, "")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("production-state-test", 10)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	if err := service.ReadinessError(); err == nil {
		t.Fatal("production service was ready without APEXLOAD_GENERATOR_STATE_DIR")
	}
	if _, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{ExecutionGeneration: 1, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/1"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("production claim without durable state error = %v, want FailedPrecondition", err)
	}
}

func TestExecutionEpochCanceledClaimDoesNotAdvanceHighWater(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	service := newEpochTestService(t, statePath)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.ClaimExecutionEpoch(ctx, &pb.ExecutionEpochClaim{ExecutionGeneration: 7, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/7"}); status.Code(err) != codes.Canceled {
		t.Fatalf("canceled claim error = %v (%s), want Canceled", err, status.Code(err))
	}
	service.mu.RLock()
	generation := service.executionGeneration
	service.mu.RUnlock()
	if generation != 0 {
		t.Fatalf("canceled claim advanced in-memory high-water to %d", generation)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\"version\":2,\"generation\":0}\n" {
		t.Fatalf("canceled claim changed durable high-water: %s", raw)
	}
}

func TestExecutionEpochClosesOldMetricsStreamOnHigherClaim(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(conn)
	t.Cleanup(func() { _ = client.Close() })
	if result, err := client.ClaimExecutionEpoch(ctx, 1, "controller/1", time.Second); err != nil || !result.Accepted {
		t.Fatalf("claim generation 1 = (%#v, %v)", result, err)
	}
	content := []byte(`{"name":"stream"}`)
	hash := scriptContentHash(content)
	if _, err := client.DistributeScript(ctx, Script{ID: "script", ExecutionGeneration: 1, Content: content, ContentHash: hash}); err != nil {
		t.Fatal(err)
	}
	if result, err := client.AssignWork(ctx, Assignment{TestID: "stream-test", ExecutionGeneration: 1, GroupName: "group", ScriptID: "script", ExpectedScriptHash: hash}); err != nil || !result.Accepted {
		t.Fatalf("AssignWork = (%#v, %v)", result, err)
	}
	stream, err := client.OpenMetricsStreamForGeneration(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if batch, err := stream.Receive(); err != nil || batch.ExecutionGeneration != 1 {
		t.Fatalf("initial metrics = (%#v, %v), want generation 1", batch, err)
	}
	if result, err := client.ClaimExecutionEpoch(ctx, 2, "controller/2", time.Second); err != nil || !result.Accepted {
		t.Fatalf("claim generation 2 = (%#v, %v)", result, err)
	}
	if _, err := stream.Receive(); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("old stream receive error = %v (%s), want FailedPrecondition", err, status.Code(err))
	}
}
