package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPreparationCancellationCancelsCapacityWork(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := newDistributedExecutionBackend(nil, time.Second, nil)
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	test := &ManagedTest{
		ID: "review-preparation-cancel", Status: "INITIALIZING", ProjectID: storage.DefaultProjectID,
		Groups: []TestGroup{{Name: "buyers", VUsers: 1}}, TotalVUsers: 1,
		executionCtx: runCtx, cancel: cancelRun, preparationID: "review-preparation",
	}
	runner.tests[test.ID] = test
	runner.setExecutionBackend(backend)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	backend.ensureRunCapacity = func(ctx context.Context, _ *ManagedTest) error {
		entered <- ctx
		<-release
		return context.Canceled
	}
	go func() { defer close(done); backend.Run(runner, test) }()
	defer func() { close(release); <-done }()
	var capacityCtx context.Context
	select {
	case capacityCtx = <-entered:
	case <-time.After(time.Second):
		t.Fatal("did not enter provisioning")
	}
	runner.cancelPreparationExecution(test.ID, test.preparationID)
	if runCtx.Err() == nil {
		t.Fatal("preparation execution was not cancelled")
	}
	if capacityCtx.Err() == nil {
		t.Fatal("preparation was cancelled but capacity provisioning context remains live")
	}
}

func TestDistributedCapacityDoesNotStartAfterCancellation(t *testing.T) {
	for _, state := range []string{"cancelled context", "aborted without context"} {
		t.Run(state, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			backend := newDistributedExecutionBackend(nil, time.Second, nil)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			test := &ManagedTest{ID: "cancelled-capacity", Status: "INITIALIZING", Groups: []TestGroup{{Name: "load", VUsers: 1}}, TotalVUsers: 1, executionCtx: ctx}
			if state == "aborted without context" {
				test.Status, test.executionCtx = "ABORTED", nil
			}
			runner.tests[test.ID] = test
			runner.setExecutionBackend(backend)
			called := false
			backend.ensureRunCapacity = func(context.Context, *ManagedTest) error { called = true; return nil }
			backend.Run(runner, test)
			if called {
				t.Fatal("capacity provisioning started after cancellation")
			}
		})
	}
}

func TestEnsureRunCapacityRejectsSuccessAfterContextCancellation(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-cancel-context", Name: "cancel context", Status: "active", ExecutionTargetID: "target-cancel-context", LifecycleMode: storage.FleetLifecycleWarmBurst, ReadinessTimeoutSeconds: 10}
	saveCapacityTestPool(t, store, pool)
	started, release := make(chan struct{}, 1), make(chan struct{})
	backend := &fakeCapacityBackend{observation: compute.Observation{
		ProviderOperationID: "task-cancel-context", Requested: 1, Observed: 1, Ready: 1, Terminal: true,
		Instances: []compute.Instance{{ProviderID: "task-cancel-context", Address: "10.0.0.8:9090", State: "running", Healthy: true}},
	}, reconcileStarted: started, reconcileRelease: release}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate, ConnectivityMode: storage.ConnectivityDirect}}
	server.capacityHeartbeatProbe = func(string) bool { return true }
	test := &ManagedTest{ID: "run-cancel-context", ProjectID: "proj-a", Assignment: &GeneratorAssignmentSettings{PoolID: pool.ID}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.ensureRunCapacity(ctx, test) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("capacity provisioning did not start")
	}
	cancel()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("ensureRunCapacity = %v, want cancellation", err)
	}
	if len(test.Assignment.SelectedTargets) != 0 {
		t.Fatal("cancelled provisioning published targets")
	}
	backend.mu.Lock()
	cancelCalls := backend.cancel
	backend.mu.Unlock()
	if cancelCalls != 1 {
		t.Fatalf("provider cleanup calls = %d, want 1", cancelCalls)
	}
	requests, err := store.ListProvisioningRequests(pool.ID, 10, 0)
	if err != nil || len(requests) != 1 {
		t.Fatalf("provisioning requests = %v, %v", requests, err)
	}
	if requests[0].Status == "success" {
		t.Fatal("cancelled provisioning recorded success")
	}
}

type capacityReadinessCommitStore struct {
	SQLiteStore  *storage.SQLiteStore
	afterSuccess func(*storage.ProvisioningRequest)
}

func (s *capacityReadinessCommitStore) CommitCapacityOperation(request *storage.ProvisioningRequest, revision int, token, leaseName, leaseOwnerID string, leaseGeneration uint64, commitWarmSize bool) (bool, error) {
	changed, err := s.SQLiteStore.CommitCapacityOperation(request, revision, token, leaseName, leaseOwnerID, leaseGeneration, commitWarmSize)
	if err == nil && changed && request.Status == "success" && s.afterSuccess != nil {
		s.afterSuccess(request)
	}
	return changed, err
}

func TestEnsureRunCapacityRollsBackUnpublishedReadiness(t *testing.T) {
	for _, interruption := range []string{"cancel context", "stop run", "replace run", "newer durable transition"} {
		t.Run(interruption, func(t *testing.T) {
			server, genericStore := newTimeslotServer(t)
			store := genericStore.(*storage.SQLiteStore)
			pool := &storage.FleetPool{ID: "pool-readiness-commit", Name: "readiness commit", Status: "active", ExecutionTargetID: "target-readiness-commit", LifecycleMode: storage.FleetLifecycleWarmBurst, ReadinessTimeoutSeconds: 10}
			saveCapacityTestPool(t, store, pool)
			backend := &fakeCapacityBackend{observation: compute.Observation{
				ProviderOperationID: "task-readiness-commit", Requested: 1, Observed: 1, Ready: 1, Terminal: true,
				Instances: []compute.Instance{{ProviderID: "task-readiness-commit", Address: "10.0.0.8:9090", State: "running", Healthy: true}},
			}}
			server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate, ConnectivityMode: storage.ConnectivityDirect}}
			server.capacityHeartbeatProbe = func(string) bool { return true }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			test := &ManagedTest{ID: "run-readiness-commit", Status: "INITIALIZING", ProjectID: storage.DefaultProjectID, Assignment: &GeneratorAssignmentSettings{PoolID: pool.ID}, executionCtx: ctx}
			server.runner.mu.Lock()
			server.runner.tests[test.ID] = test
			server.runner.mu.Unlock()
			t.Cleanup(func() {
				// This test installs only run bookkeeping, without an execution
				// backend. Remove its stub before server shutdown tries to stop it.
				server.runner.mu.Lock()
				delete(server.runner.tests, test.ID)
				server.runner.mu.Unlock()
			})
			committed := false
			server.store = sqliteOverlay(&capacityReadinessCommitStore{SQLiteStore: store, afterSuccess: func(request *storage.ProvisioningRequest) {
				committed = true
				switch interruption {
				case "cancel context":
					cancel()
				case "stop run":
					server.runner.mu.Lock()
					test.Status = "STOPPING"
					server.runner.mu.Unlock()
				case "replace run":
					server.runner.mu.Lock()
					server.runner.tests[test.ID] = &ManagedTest{ID: test.ID, Status: "INITIALIZING"}
					server.runner.mu.Unlock()
				case "newer durable transition":
					newer := *request
					newer.Revision++
					newer.AttemptToken = "newer-attempt"
					newer.LastError = "preserve newer write"
					if changed, err := store.TransitionProvisioningRequest(&newer, request.Revision, request.AttemptToken); err != nil || !changed {
						t.Fatalf("supersede readiness = %v, %v", changed, err)
					}
					cancel()
				}
			}})
			if err := server.ensureRunCapacity(ctx, test); !errors.Is(err, context.Canceled) {
				t.Fatalf("ensureRunCapacity = %v, want cancellation", err)
			}
			if !committed {
				t.Fatal("did not reach durable readiness transition")
			}
			if len(test.Assignment.SelectedTargets) != 0 {
				t.Fatal("interrupted readiness published targets")
			}
			backend.mu.Lock()
			cancelCalls := backend.cancel
			backend.mu.Unlock()
			wantCancelCalls := 1
			if interruption == "newer durable transition" {
				// A successor has taken ownership; its provider resources cannot
				// be rolled back by the old run's deferred cleanup.
				wantCancelCalls = 0
			}
			if cancelCalls != wantCancelCalls {
				t.Fatalf("provider cleanup calls = %d, want %d", cancelCalls, wantCancelCalls)
			}
			requests, err := store.ListProvisioningRequests(pool.ID, 10, 0)
			if err != nil || len(requests) != 1 {
				t.Fatalf("provisioning requests = %v, %v", requests, err)
			}
			request := requests[0]
			if interruption == "newer durable transition" {
				if request.Status != "success" || request.AttemptToken != "newer-attempt" || request.LastError != "preserve newer write" {
					t.Fatalf("cleanup overwrote newer transition: %+v", request)
				}
			} else if request.Status != "cancelled" || request.CompletedAt == nil {
				t.Fatalf("interrupted readiness request = %+v, want completed cancellation", request)
			}
		})
	}
}
