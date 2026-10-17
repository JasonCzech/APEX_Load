package api

import (
	"context"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	"testing"
)

func TestIdleEpochRecoveryWaitsForTerminalArchiveWork(t *testing.T) {
	for _, mode := range []string{"client", "client-map", "archive-map", "lifecycle", "publication"} {
		t.Run(mode, func(t *testing.T) {
			tr, _, generation, owner := idleEpochRecoveryRunner(t)
			run := &ManagedTest{ID: "terminal", Status: "ABORTED"}
			tr.tests[run.ID] = run
			client := &startupLifecycleFakeClient{}
			release := func() {}
			switch mode {
			case "client":
				run.remoteClient = client
			case "client-map":
				run.remoteClientsByTarget = map[string]generatorClient{"source": client}
			case "archive-map":
				run.remoteArchiveClientsByTarget = map[string]generatorClient{"source": client}
			case "lifecycle":
				run.remoteLifecycleCancel = func() {}
			case "publication":
				release = tr.lockArchiveCommitFence(run.ID)
			}
			defer func() { release() }()
			if tr.recoverExpiredGeneratorEpoch(context.Background(), generation, owner, expiredEpochError(generation)) || !tr.leadership.IsLeader() {
				t.Fatal("idle turnover interrupted terminal archive work")
			}
			run.remoteClient = nil
			run.remoteClientsByTarget = nil
			run.remoteArchiveClientsByTarget = nil
			run.remoteLifecycleCancel = nil
			release()
			release = func() {}
			if !tr.recoverExpiredGeneratorEpoch(context.Background(), generation, owner, expiredEpochError(generation)) {
				t.Fatal("finished archive work blocked generation recovery")
			}
		})
	}
}

// The release uses a real lease store but pauses at the database boundary, where
// liveness must be able to use its bounded snapshot while the runner is locked.
type blockedIdleEpochReleaseStore struct {
	SQLiteStore *storage.SQLiteStore
	entered     chan struct{}
	resume      chan struct{}
	fail        bool
}

func (s *blockedIdleEpochReleaseStore) ReleaseSchedulerLeaseTermIfTestsIdle(name, owner string, generation uint64) (bool, error) {
	close(s.entered)
	<-s.resume
	if s.fail {
		return false, errors.New("database unavailable")
	}
	return s.SQLiteStore.ReleaseSchedulerLeaseTermIfTestsIdle(name, owner, generation)
}

func TestIdleEpochRecoveryKeepsBoundedHealthBypassDuringDatabaseRelease(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "commit"
		if fail {
			name = "error"
		}
		t.Run(name, func(t *testing.T) {
			tr, store, generation, owner := idleEpochRecoveryRunner(t)
			blocked := &blockedIdleEpochReleaseStore{SQLiteStore: store, entered: make(chan struct{}), resume: make(chan struct{}), fail: fail}
			tr.leadership.store = sqliteOverlay(blocked)
			server := &Server{runner: tr}
			server.healthSnapshot.Store(&healthCheckSnapshot{publishedAt: time.Now()})
			done := make(chan bool, 1)
			go func() {
				done <- tr.recoverExpiredGeneratorEpoch(context.Background(), generation, owner, expiredEpochError(generation))
			}()
			defer func() {
				close(blocked.resume)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("release did not finish")
				}
				if tr.durableWritesInFlight.Load() != 0 {
					t.Error("durable-write accounting leaked")
				}
				if _, cached := server.healthCheckSnapshotWithoutRunnerLock(); cached {
					t.Error("health bypass remained enabled after release")
				}
			}()
			select {
			case <-blocked.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("release did not reach database")
			}
			if _, cached := server.healthCheckSnapshotWithoutRunnerLock(); !cached {
				t.Fatal("database release blocks liveness behind the runner lock")
			}
			server.healthSnapshot.Store(&healthCheckSnapshot{publishedAt: time.Now().Add(-healthCheckDurableWriteBypassBudget() - time.Second)})
			if _, cached := server.healthCheckSnapshotWithoutRunnerLock(); cached {
				t.Fatal("database release extended the bounded health bypass")
			}
		})
	}
}
