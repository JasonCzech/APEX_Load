package generatorrpc

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExecutionEpochPersistenceRunsOutsideServiceLock(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	originalPersist := service.executionEpochManager.persistState
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	service.mu.Lock()
	service.executionEpochManager.persistState = func(state *executionEpochStateStore, generation uint64, leaseOwner string) error {
		once.Do(func() { close(started) })
		<-release
		return originalPersist(state, generation, leaseOwner)
	}
	service.mu.Unlock()

	type result struct {
		ack *pb.ExecutionEpochAck
		err error
	}
	claimDone := make(chan result, 1)
	go func() {
		ack, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
			ExecutionGeneration: 1, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/1",
		})
		claimDone <- result{ack: ack, err: err}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("epoch persistence did not start")
	}

	lockAcquired := make(chan struct{})
	go func() {
		service.mu.Lock()
		close(lockAcquired)
		service.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("durable epoch persistence held Service.mu")
	}
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{ScriptId: "blocked"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("mutation during epoch persistence error = %v (%s), want Unavailable", err, status.Code(err))
	}
	close(release)
	select {
	case outcome := <-claimDone:
		if outcome.err != nil || !outcome.ack.GetAccepted() {
			t.Fatalf("ClaimExecutionEpoch = (%#v, %v)", outcome.ack, outcome.err)
		}
	case <-time.After(time.Second):
		t.Fatal("claim did not complete after durable persistence resumed")
	}
}

func TestExecutionEpochDuplicateClaimWaitsForSinglePersistence(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	originalPersist := service.executionEpochManager.persistState
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	service.mu.Lock()
	service.executionEpochManager.persistState = func(state *executionEpochStateStore, generation uint64, leaseOwner string) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return originalPersist(state, generation, leaseOwner)
	}
	service.mu.Unlock()

	claim := &pb.ExecutionEpochClaim{ExecutionGeneration: 1, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/1"}
	type result struct {
		ack *pb.ExecutionEpochAck
		err error
	}
	results := make(chan result, 2)
	go func() {
		ack, err := service.ClaimExecutionEpoch(context.Background(), claim)
		results <- result{ack, err}
	}()
	<-started
	go func() {
		ack, err := service.ClaimExecutionEpoch(context.Background(), claim)
		results <- result{ack, err}
	}()
	select {
	case outcome := <-results:
		t.Fatalf("duplicate claim bypassed pending persistence: (%#v, %v)", outcome.ack, outcome.err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	for range 2 {
		select {
		case outcome := <-results:
			if outcome.err != nil || !outcome.ack.GetAccepted() {
				t.Fatalf("duplicate claim result = (%#v, %v)", outcome.ack, outcome.err)
			}
		case <-time.After(time.Second):
			t.Fatal("duplicate claims did not complete")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("durable persist calls = %d, want 1", got)
	}
}

func TestExecutionEpochShutdownWaitsForPersistence(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	originalPersist := service.executionEpochManager.persistState
	started := make(chan struct{})
	release := make(chan struct{})
	service.mu.Lock()
	service.executionEpochManager.persistState = func(state *executionEpochStateStore, generation uint64, leaseOwner string) error {
		close(started)
		<-release
		return originalPersist(state, generation, leaseOwner)
	}
	service.mu.Unlock()

	claimDone := make(chan error, 1)
	go func() {
		_, err := service.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
			ExecutionGeneration: 1, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/1",
		})
		claimDone <- err
	}()
	<-started
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- service.Shutdown(shutdownCtx) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown bypassed epoch persistence: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-claimDone:
		if err != nil {
			t.Fatalf("claim after persistence release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("claim did not finish")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("shutdown after persistence release: %v", err)
		}
	case <-shutdownCtx.Done():
		t.Fatal("shutdown did not finish after persistence release")
	}
}

func TestExecutionEpochCanceledHigherClaimAfterRestartCannotBeRevived(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "epoch.json")
	first := newEpochTestService(t, statePath)
	if ack := claimEpoch(t, first, 1, time.Second); !ack.GetAccepted() {
		t.Fatalf("initial claim rejected: %s", ack.GetErrorMessage())
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	if err := first.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	shutdownCancel()

	restarted := newEpochTestService(t, statePath)
	originalPersist := restarted.executionEpochManager.persistState
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	restarted.mu.Lock()
	restarted.executionEpochManager.persistState = func(state *executionEpochStateStore, generation uint64, leaseOwner string) error {
		once.Do(func() {
			close(started)
			<-release
		})
		return originalPersist(state, generation, leaseOwner)
	}
	restarted.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	claimDone := make(chan error, 1)
	go func() {
		_, err := restarted.ClaimExecutionEpoch(ctx, &pb.ExecutionEpochClaim{
			ExecutionGeneration: 2, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/2",
		})
		claimDone <- err
	}()
	<-started
	cancel()
	close(release)
	if err := <-claimDone; status.Code(err) != codes.Canceled {
		t.Fatalf("canceled persisted claim error = %v (%s), want Canceled", err, status.Code(err))
	}
	ack, err := restarted.ClaimExecutionEpoch(context.Background(), &pb.ExecutionEpochClaim{
		ExecutionGeneration: 2, LeaseTtlMilliseconds: 1000, ExecutionLeaseOwnerId: "controller/2",
	})
	if err != nil {
		t.Fatalf("same-generation retry: %v", err)
	}
	if ack.GetAccepted() || ack.GetErrorMessage() == "" {
		t.Fatalf("canceled higher generation was revived: %#v", ack)
	}
	if ack := claimEpoch(t, restarted, 3, time.Second); !ack.GetAccepted() {
		t.Fatalf("higher generation after canceled persist rejected: %s", ack.GetErrorMessage())
	}
}

func TestExecutionEpochTimerTokenRejectsStaleCallback(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	expiresAt := time.Now().Add(-time.Millisecond)
	service.mu.Lock()
	service.executionGeneration = 1
	service.executionExpiresAt = expiresAt
	service.executionEpochTimerToken = 2
	service.assignments["run"] = assignedTest{
		testID: "run", executionGeneration: 1, groups: map[string]int32{"group": 1},
	}
	service.mu.Unlock()
	service.expireExecutionEpochToken(1, expiresAt, 1, true)
	service.mu.RLock()
	_, exists := service.assignments["run"]
	service.mu.RUnlock()
	if !exists {
		t.Fatal("stale epoch timer token fenced the current assignment")
	}
}

func TestExecutionEpochCanceledRenewalDoesNotExtendLease(t *testing.T) {
	service := newEpochTestService(t, filepath.Join(t.TempDir(), "epoch.json"))
	if ack := claimEpoch(t, service, 1, time.Second); !ack.GetAccepted() {
		t.Fatalf("initial claim rejected: %s", ack.GetErrorMessage())
	}
	service.mu.RLock()
	originalExpiry := service.executionExpiresAt
	service.mu.RUnlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.claimValidatedExecutionEpoch(ctx, 1, "controller/1", originalExpiry.Add(time.Minute))
	if status.Code(err) != codes.Canceled {
		t.Fatalf("canceled renewal error = %v (%s), want Canceled", err, status.Code(err))
	}
	service.mu.RLock()
	gotExpiry := service.executionExpiresAt
	service.mu.RUnlock()
	if !gotExpiry.Equal(originalExpiry) {
		t.Fatalf("canceled renewal expiry = %s, want unchanged %s", gotExpiry, originalExpiry)
	}
}
