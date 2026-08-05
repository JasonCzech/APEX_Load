package generatorrpc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
)

func newTestLeaseService(t *testing.T) *Service {
	t.Helper()
	service := NewService("test-lease-generator", 100, WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   1,
		LeaseTtlMilliseconds:  2000,
		ExecutionLeaseOwnerId: "controller/1",
	})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("claim process epoch: ack=%+v err=%v", ack, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(ctx)
	})
	return service
}

func TestAssignmentLeaseExpiryStopsOnlyItsTestAndCannotBeRevived(t *testing.T) {
	service := newTestLeaseService(t)
	service.mu.Lock()
	service.assignments["lost"] = assignedTest{testID: "lost", executionGeneration: 1, committed: true, requireTestExecutionLease: true}
	service.assignments["healthy"] = assignedTest{testID: "healthy", executionGeneration: 1, committed: true, requireTestExecutionLease: true}
	service.mu.Unlock()

	for _, testID := range []string{"lost", "healthy"} {
		ttl := int64(1200)
		if testID == "lost" {
			ttl = 25
		}
		ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
			ExecutionGeneration:   1,
			LeaseTtlMilliseconds:  ttl,
			ExecutionLeaseOwnerId: "controller/1",
			TestId:                testID,
		})
		if err != nil || !ack.GetAccepted() || !ack.GetTestLeaseAccepted() {
			t.Fatalf("claim %s assignment lease: ack=%+v err=%v", testID, ack, err)
		}
	}

	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		_, lostExists := service.assignments["lost"]
		_, healthyExists := service.assignments["healthy"]
		service.mu.RUnlock()
		if !lostExists {
			if !healthyExists {
				t.Fatal("lost assignment lease expiry stopped unrelated healthy assignment")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lost assignment did not stop after its lease expired")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Even if a stale controller restages the same identity, expiry is a
	// permanent fence for this test/generation on the old source.
	service.mu.Lock()
	service.assignments["lost"] = assignedTest{testID: "lost", executionGeneration: 1, requireTestExecutionLease: true}
	service.mu.Unlock()
	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   1,
		LeaseTtlMilliseconds:  500,
		ExecutionLeaseOwnerId: "controller/1",
		TestId:                "lost",
	})
	if err != nil {
		t.Fatalf("reclaim expired assignment lease: %v", err)
	}
	if ack.GetAccepted() || !ack.GetTestLeaseAccepted() || !strings.Contains(ack.GetErrorMessage(), "cannot be revived") {
		t.Fatalf("expired assignment lease was revived: %+v", ack)
	}
}

func TestAssignmentLeaseWrongOwnerRejectionAcknowledgesCapability(t *testing.T) {
	service := newTestLeaseService(t)
	service.mu.Lock()
	service.assignments["owned"] = assignedTest{testID: "owned", executionGeneration: 1, requireTestExecutionLease: true}
	service.mu.Unlock()
	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   1,
		LeaseTtlMilliseconds:  500,
		ExecutionLeaseOwnerId: "different-controller/1",
		TestId:                "owned",
	})
	if err != nil {
		t.Fatalf("reject wrong assignment lease owner: %v", err)
	}
	if ack.GetAccepted() || !ack.GetTestLeaseAccepted() || !strings.Contains(ack.GetErrorMessage(), "different controller lease owner") {
		t.Fatalf("wrong-owner rejection did not preserve capability and reason: %+v", ack)
	}
}

func TestAssignmentLeaseDetachDoesNotFenceUnrelatedMutations(t *testing.T) {
	service := newTestLeaseService(t)
	service.mu.Lock()
	service.assignments["expired"] = assignedTest{testID: "expired", executionGeneration: 1, committed: true, requireTestExecutionLease: true}
	service.assignments["healthy"] = assignedTest{testID: "healthy", executionGeneration: 1, committed: true, requireTestExecutionLease: true}
	service.mu.Unlock()

	// Leave the detached stop unfinished while exercising the healthy run. This
	// models a slow engine/archive finalizer and proves assignment-scoped cleanup
	// does not enter the process-wide epoch-fencing state.
	stops := service.detachFencedTests([]string{"expired"}, 1, false)
	if len(stops) != 1 {
		t.Fatalf("detached assignment count = %d, want 1", len(stops))
	}
	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   1,
		LeaseTtlMilliseconds:  500,
		ExecutionLeaseOwnerId: "controller/1",
		TestId:                "healthy",
	})
	if err != nil || !ack.GetAccepted() || !ack.GetTestLeaseAccepted() {
		t.Fatalf("healthy assignment renewal during unrelated finalization = (%+v, %v)", ack, err)
	}
	if err := service.finishDetachedFencedTest(context.Background(), stops[0]); err != nil {
		t.Fatalf("finish detached assignment: %v", err)
	}
}

func TestAssignmentLeaseCannotReviveExpiredLeaseBeforeTimerCallback(t *testing.T) {
	service := newTestLeaseService(t)
	testID := "delayed-expiry-callback"
	expiredAt := time.Now().Add(-time.Second)
	service.mu.Lock()
	service.assignments[testID] = assignedTest{testID: testID, executionGeneration: 1, committed: true, requireTestExecutionLease: true}
	service.testExecutionLeases[testID] = testExecutionLease{
		generation: 1,
		leaseOwner: "controller/1",
		expiresAt:  expiredAt,
		token:      99,
		// Model a delayed timer callback whose delivery no longer reflects the
		// lease's semantic expiration timestamp.
		timer: time.NewTimer(time.Hour),
	}
	service.mu.Unlock()

	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   1,
		LeaseTtlMilliseconds:  500,
		ExecutionLeaseOwnerId: "controller/1",
		TestId:                testID,
	})
	if err != nil {
		t.Fatalf("renew logically expired assignment lease: %v", err)
	}
	if ack.GetAccepted() || !ack.GetTestLeaseAccepted() || !strings.Contains(ack.GetErrorMessage(), "cannot be revived") {
		t.Fatalf("logically expired assignment lease was revived or misclassified: %+v", ack)
	}
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		_, assigned := service.assignments[testID]
		fenced := service.testExecutionLeaseFences[testID] >= 1
		_, leased := service.testExecutionLeases[testID]
		service.mu.RUnlock()
		if !assigned && fenced && !leased {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expired assignment cleanup incomplete: assigned=%v fenced=%v leased=%v", assigned, fenced, leased)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAssignmentLeaseClearsOnExplicitTerminalization(t *testing.T) {
	service := newTestLeaseService(t)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", executionGeneration: 1, committed: true, requireTestExecutionLease: true}
	service.mu.Unlock()
	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   1,
		LeaseTtlMilliseconds:  1000,
		ExecutionLeaseOwnerId: "controller/1",
		TestId:                "run",
	})
	if err != nil || !ack.GetTestLeaseAccepted() {
		t.Fatalf("claim assignment lease: ack=%+v err=%v", ack, err)
	}
	if _, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run", ExecutionGeneration: 1}); err != nil {
		t.Fatalf("stop assignment: %v", err)
	}
	service.mu.RLock()
	_, leaseExists := service.testExecutionLeases["run"]
	_, fenced := service.testExecutionLeaseFences["run"]
	service.mu.RUnlock()
	if leaseExists || fenced {
		t.Fatalf("terminalized assignment retained lease=%v tombstone=%v", leaseExists, fenced)
	}
}

func TestAssignmentLeaseTombstonesClearOnHigherControllerGeneration(t *testing.T) {
	service := newTestLeaseService(t)
	service.mu.Lock()
	service.testExecutionLeaseFences["old-run"] = 1
	service.mu.Unlock()
	ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration:   2,
		LeaseTtlMilliseconds:  1000,
		ExecutionLeaseOwnerId: "controller/2",
	})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("claim higher process epoch: ack=%+v err=%v", ack, err)
	}
	service.mu.RLock()
	_, exists := service.testExecutionLeaseFences["old-run"]
	service.mu.RUnlock()
	if exists {
		t.Fatal("higher controller generation retained stale assignment tombstone")
	}
}

func TestStagedAssignmentLeaseRenewalSurvivesSetupDelayAndCommits(t *testing.T) {
	service := newTestLeaseService(t)
	service.mu.Lock()
	service.assignments["replacement"] = assignedTest{
		testID: "replacement", lifecycle: assignmentLifecycleStaged,
		executionGeneration: 1, requireTestExecutionLease: true,
		groups: map[string]int32{"group": 1}, vuserCount: 1,
	}
	service.mu.Unlock()
	claim := func(ttl time.Duration) {
		t.Helper()
		ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
			ExecutionGeneration:   1,
			LeaseTtlMilliseconds:  ttl.Milliseconds(),
			ExecutionLeaseOwnerId: "controller/1",
			TestId:                "replacement",
		})
		if err != nil || !ack.GetTestLeaseAccepted() {
			t.Fatalf("claim staged replacement lease: ack=%+v err=%v", ack, err)
		}
	}
	claim(50 * time.Millisecond)
	time.Sleep(25 * time.Millisecond)
	// This renewal models the temporary controller keeper during initial stream
	// and barrier setup or while a source's final lease is allowed to expire.
	claim(250 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "replacement", ExecutionGeneration: 1})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("commit replacement after source fence wait: ack=%+v err=%v", ack, err)
	}
}
