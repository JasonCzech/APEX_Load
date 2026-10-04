package api

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type recoveryDrainBarrierStore struct {
	storage.RuntimeSecretProvisioningAttemptStore
	entered, release chan struct{}
	once             sync.Once
	calls            atomic.Int32
}

func (s *recoveryDrainBarrierStore) ListRuntimeSecretProvisioningAttempts() ([]*storage.RuntimeSecretProvisioningAttempt, error) {
	s.calls.Add(1)
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.RuntimeSecretProvisioningAttemptStore.ListRuntimeSecretProvisioningAttempts()
}
func newRecoveryDrainFixture(t *testing.T) (*Server, *recoveryDrainBarrierStore, func()) {
	t.Helper()
	records, err := storage.NewSQLiteStore(t.TempDir() + "/recovery.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := records.Close(); err != nil {
			t.Errorf("close recovery store: %v", err)
		}
	})
	barrier := &recoveryDrainBarrierStore{RuntimeSecretProvisioningAttemptStore: records, entered: make(chan struct{}), release: make(chan struct{})}
	runner := &TestRunner{secretAttemptStore: barrier}
	server := &Server{runner: runner}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	t.Cleanup(func() {
		release()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// A prior deliberately timed-out Shutdown cannot return success later; this
		// second owned drain still joins the admitted call before Store.Close.
		_ = server.Shutdown(ctx)
		if err := runner.waitRuntimeOverrideSecretAttemptReconciliation(ctx); err != nil {
			t.Errorf("drain recovery callback: %v", err)
		}
	})
	return server, barrier, release
}
func waitForRecoveryDrainAdmission(t *testing.T, server *Server, barrier *recoveryDrainBarrierStore) {
	t.Helper()
	server.runner.scheduleRuntimeOverrideSecretAttempt("fixture", time.Now())
	select {
	case <-barrier.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timer recovery never entered storage")
	}
}
func waitForRecoveryAdmissionClosed(t *testing.T, runner *TestRunner) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		runner.secretRecovery.mu.Lock()
		stopped := runner.secretRecovery.stopped
		runner.secretRecovery.mu.Unlock()
		if stopped {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("Shutdown did not close recovery admission")
		}
	}
}
func assertRecoveryDrainedAndClosed(t *testing.T, runner *TestRunner, barrier *recoveryDrainBarrierStore) {
	t.Helper()
	runner.secretRecovery.mu.Lock()
	running, timer, stopped := runner.secretRecovery.running, runner.secretRecovery.timer, runner.secretRecovery.stopped
	runner.secretRecovery.mu.Unlock()
	if running != 0 || timer != nil || !stopped {
		t.Fatalf("recovery not drained: running=%d timer=%v stopped=%v", running, timer != nil, stopped)
	}
	// Neither a synchronous recovery scan nor a delayed retry may touch storage
	// once shutdown's admission fence is closed.
	runner.scheduleRuntimeOverrideSecretAttemptReconciliation()
	runner.scheduleRuntimeOverrideSecretAttempt("late", time.Now())
	if got := barrier.calls.Load(); got != 1 {
		t.Fatalf("storage calls after shutdown = %d, want one admitted call", got)
	}
	runner.secretRecovery.mu.Lock()
	armed := runner.secretRecovery.timer != nil
	runner.secretRecovery.mu.Unlock()
	if armed {
		t.Fatal("stopped recovery timer was rearmed")
	}
}
func TestServerShutdownJoinsRuntimeCredentialRecoveryInFlight(t *testing.T) {
	server, barrier, release := newRecoveryDrainFixture(t)
	waitForRecoveryDrainAdmission(t, server, barrier)
	shutdown := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { shutdown <- server.Shutdown(ctx) }()
	waitForRecoveryAdmissionClosed(t, server.runner)
	select {
	case err := <-shutdown:
		t.Fatalf("Shutdown returned before admitted storage completed: %v", err)
	default:
	}
	release()
	select {
	case err := <-shutdown:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Shutdown did not join released callback")
	}
	assertRecoveryDrainedAndClosed(t, server.runner, barrier)
}
func TestServerShutdownDeadlineRetainsRuntimeCredentialRecoveryOwnership(t *testing.T) {
	server, barrier, release := newRecoveryDrainFixture(t)
	waitForRecoveryDrainAdmission(t, server, barrier)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := server.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want retained callback timeout", err)
	}
	server.runner.secretRecovery.mu.Lock()
	running, drained := server.runner.secretRecovery.running, server.runner.secretRecovery.drained
	server.runner.secretRecovery.mu.Unlock()
	if running == 0 {
		t.Fatal("timed-out callback lost its ownership")
	}
	select {
	case <-drained:
		t.Fatal("blocked callback was reported drained")
	default:
	}
	release()
	drainCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := server.runner.waitRuntimeOverrideSecretAttemptReconciliation(drainCtx); err != nil {
		t.Fatal(err)
	}
	assertRecoveryDrainedAndClosed(t, server.runner, barrier)
}
