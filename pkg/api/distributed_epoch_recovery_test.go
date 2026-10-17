package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func idleEpochRecoveryRunner(t *testing.T) (*TestRunner, *storage.SQLiteStore, uint64, string) {
	t.Helper()
	s, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "epoch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	leader := newExecutionLeadership(s, "idle-epoch")
	leader.tick()
	t.Cleanup(leader.relinquish)
	generation, owner, _, ok := leader.executionGeneratorClaim()
	if !ok {
		t.Fatal("lease unavailable")
	}
	return &TestRunner{leadership: leader, durableExecutionFence: true, testStore: s, tests: map[string]*ManagedTest{}}, s, generation, owner
}

func expiredEpochError(generation uint64) error {
	return fmt.Errorf("target renewal: %w", &generatorExecutionEpochRejection{generation: generation, currentGeneration: generation, message: generatorrpc.ExecutionGenerationExpiredMessage})
}

func TestExpiredGeneratorEpochRecoveryGates(t *testing.T) {
	for _, state := range []string{"idle", "DRAFT", "PENDING", "QUEUED", "SHAKEOUT_REVIEW", "COMPLETE", "FAILED", "ABORTED", "STARTING", "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT", "unknown", "hidden-durable-run", "shutdown", "cancelled", "reconciling", "stale-owner", "stale-generation", "no-durable-fence", "transport-error", "other-rejection", "higher-generation"} {
		t.Run(state, func(t *testing.T) {
			tr, store, generation, owner := idleEpochRecoveryRunner(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			renewalErr := expiredEpochError(generation)
			want := false
			switch state {
			case "idle":
				want = true
			case "DRAFT", "PENDING", "QUEUED", "SHAKEOUT_REVIEW", "COMPLETE", "FAILED", "ABORTED":
				want = true
				tr.tests["test"] = &ManagedTest{ID: "test", Status: state}
			case "hidden-durable-run":
				if err := store.SaveTest(&storage.TestRecord{ID: "hidden", Name: "hidden", Status: "RUNNING", StartTime: time.Now(), Groups: []any{}}); err != nil {
					t.Fatal(err)
				}
			case "shutdown":
				tr.shuttingDown = true
			case "cancelled":
				cancel()
			case "reconciling":
				tr.leadership.reconciling.Store(true)
			case "stale-owner":
				owner = "old-owner"
			case "stale-generation":
				generation++
			case "no-durable-fence":
				tr.durableExecutionFence = false
			case "transport-error":
				renewalErr = errors.New("unreachable")
			case "other-rejection":
				renewalErr = &generatorExecutionEpochRejection{generation: generation, currentGeneration: generation, message: "assignment lease expired"}
			case "higher-generation":
				renewalErr = &generatorExecutionEpochRejection{generation: generation, currentGeneration: generation + 1, message: generatorrpc.ExecutionGenerationExpiredMessage}
			default:
				tr.tests["test"] = &ManagedTest{ID: "test", Status: state}
			}
			if got := tr.recoverExpiredGeneratorEpoch(ctx, generation, owner, renewalErr); got != want {
				t.Fatalf("recovery=%v want %v", got, want)
			}
			if tr.leadership.IsLeader() == want {
				t.Fatalf("leader state after recovery=%v", tr.leadership.IsLeader())
			}
			if want {
				// Wait for the ordered loss callback before reacquisition.
				tr.leadership.relinquish()
				tr.leadership.tick()
				next, nextOwner, _, ok := tr.leadership.executionGeneratorClaim()
				if !ok || next <= generation || nextOwner == owner {
					t.Fatalf("replacement term=%d/%q ok=%v", next, nextOwner, ok)
				}
			}
		})
	}
}

type restoredExpiredEpochClient struct {
	startupLifecycleFakeClient
	expired  uint64
	accepted atomic.Uint64
	attempts atomic.Uint64
}

func (c *restoredExpiredEpochClient) ClaimExecutionEpoch(ctx context.Context, generation uint64, _ string, _ time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	c.attempts.Add(1)
	if err := ctx.Err(); err != nil {
		return generatorrpc.ExecutionEpochResult{}, err
	}
	if generation <= c.expired {
		return generatorrpc.ExecutionEpochResult{CurrentGeneration: c.expired, ErrorMessage: generatorrpc.ExecutionGenerationExpiredMessage}, nil
	}
	c.accepted.Store(generation)
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation}, nil
}

func TestEpochKeeperRecoversRestoredGeneratorOnlyAfterActiveRunFinishes(t *testing.T) {
	tr, store, generation, _ := idleEpochRecoveryRunner(t)
	tr.leadership.ttl = time.Second
	tr.leadership.renew = 20 * time.Millisecond
	tr.tests["active"] = &ManagedTest{ID: "active", Status: "RUNNING"}
	if err := store.SaveTest(&storage.TestRecord{ID: "active", Name: "active", Status: "RUNNING", StartTime: time.Now(), Groups: []any{}}); err != nil {
		t.Fatal(err)
	}
	client := &restoredExpiredEpochClient{expired: generation}
	backend := newDistributedExecutionBackend([]string{"restored-generator"}, 15*time.Millisecond, func(context.Context, string) (generatorClient, error) { return client, nil })
	tr.execution = backend
	s := &Server{leadership: tr.leadership, runner: tr}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunExecutionLeadership(ctx); close(done) }()
	defer stopEpochKeeperServer(t, cancel, done)
	activeDeadline := time.Now().Add(3 * time.Second)
	for client.attempts.Load() < 2 && time.Now().Before(activeDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if client.attempts.Load() < 2 {
		t.Fatal("keeper did not retry the expired generator while the run was active")
	}
	if client.accepted.Load() != 0 || !tr.leadership.IsLeader() {
		t.Fatal("active run was interrupted by generation rotation")
	}
	// A terminal run and a waiting next run no longer prevent recovery.
	tr.mu.Lock()
	tr.tests["active"].Status = "COMPLETE"
	tr.tests["next"] = &ManagedTest{ID: "next", Status: "PENDING"}
	tr.mu.Unlock()
	if err := store.UpdateTestStatus("active", "COMPLETE", nil, ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for client.accepted.Load() <= generation && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := client.accepted.Load(); got <= generation {
		t.Fatalf("restored generator still rejected generation: %d", got)
	}
}

func TestExpiredEpochRecoveryCancellationWhileRenewalIsBlocked(t *testing.T) {
	tr, _, generation, owner := idleEpochRecoveryRunner(t)
	tr.leadership.tickMu.Lock()
	defer tr.leadership.tickMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	defer cancel()
	go func() { done <- tr.recoverExpiredGeneratorEpoch(ctx, generation, owner, expiredEpochError(generation)) }()
	blocked := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !tr.mu.TryRLock() {
			blocked = true
			break
		}
		tr.mu.RUnlock()
		time.Sleep(time.Millisecond)
	}
	if !blocked {
		t.Fatal("recovery did not reach the blocked renewal gate")
	}
	cancel()
	select {
	case released := <-done:
		if released || !tr.leadership.IsLeader() {
			t.Fatal("cancelled recovery released leadership")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled recovery blocked on renewal")
	}
}

type uncertainIdleReleaseStore struct {
	SQLiteStore *storage.SQLiteStore
	commit      bool
}

func (s *uncertainIdleReleaseStore) ReleaseSchedulerLeaseTermIfTestsIdle(name, owner string, generation uint64) (bool, error) {
	if s.commit {
		if _, err := s.SQLiteStore.ReleaseSchedulerLeaseTermIfTestsIdle(name, owner, generation); err != nil {
			return false, err
		}
	}
	return false, errors.New("release outcome unavailable")
}
func TestIdleEpochRecoveryStepsDownOnAmbiguousCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			tr, s, generation, owner := idleEpochRecoveryRunner(t)
			tr.leadership.store = sqliteOverlay(&uncertainIdleReleaseStore{SQLiteStore: s, commit: committed})
			epoch, cancel := tr.leadership.operationContext(context.Background())
			defer cancel()
			if !tr.recoverExpiredGeneratorEpoch(context.Background(), generation, owner, expiredEpochError(generation)) || tr.leadership.IsLeader() {
				t.Fatal("uncertain release did not close admission")
			}
			// operationContext propagates epoch cancellation with AfterFunc;
			// the child callback may run after the synchronous admission gate.
			select {
			case <-epoch.Done():
			case <-time.After(time.Second):
				t.Fatal("uncertain release did not cancel the old operation")
			}
		})
	}
}
