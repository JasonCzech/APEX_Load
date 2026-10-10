package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestEphemeralCapacityPreservesPreparationRevision(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "review-prepare-pool", Name: "Review prepare pool", Status: "active", ExecutionTargetID: "review-prepare-target", LifecycleMode: storage.FleetLifecycleWarmBurst, ReadinessTimeoutSeconds: 10, MaxSize: 5}
	saveCapacityTestPool(t, store, pool)
	backend := &fakeCapacityBackend{observation: compute.Observation{
		ProviderOperationID: "review-task", Requested: 1, Observed: 1, Ready: 1, Terminal: true,
		Instances: []compute.Instance{{ProviderID: "review-task", Address: "10.0.0.8:9090", State: "running", Healthy: true}},
	}}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate, ConnectivityMode: storage.ConnectivityDirect}}
	server.capacityHeartbeatProbe = func(string) bool { return true }
	script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "review-capacity-script", Name: "Review capacity", Version: "1", Protocol: "http", ProjectID: "proj-a", Config: dsl.ScriptConfig{BaseURL: "http://127.0.0.1:1"}, Actions: []dsl.Action{{Name: "load", Type: "request", Method: "GET", Target: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := server.runner.CreateTest(CreateTestRequest{Name: "Review preparation", ProjectID: "proj-a", ScriptID: script.ID, VUsers: 1, Duration: "1s", scriptShakeoutProvided: true, resourceBaselineProvided: true})
	if err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	managed := server.runner.tests[run.ID]
	managed.Assignment = &GeneratorAssignmentSettings{PoolID: pool.ID, Mode: generatorAssignmentModeAuto}
	server.runner.mu.Unlock()
	revision, err := server.runner.preparationRevision(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	p, err := store.CreatePreparation(storage.Preparation{ID: "review-preparation", TestID: run.ID, ProjectID: "proj-a", ExecutionRevision: revision, IdempotencyKey: "review-idempotency", CreatedAt: now, ExpiresAt: now.Add(time.Hour), Capacity: compute.CapacityPolicy{Selection: "ephemeral"}})
	if err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	managed.preparationID = p.ID
	server.runner.mu.Unlock()
	if err := server.ensureRunCapacity(context.Background(), managed); err != nil {
		t.Fatal(err)
	}
	actualRevision, err := server.runner.preparationRevision(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actualRevision != revision {
		t.Fatal("successful ephemeral provisioning changed execution revision")
	}

	resolved := cloneGeneratorAssignmentSettings(managed.Assignment)
	for _, tc := range []struct {
		name   string
		change func(*GeneratorAssignmentSettings)
	}{
		{"target", func(a *GeneratorAssignmentSettings) { a.SelectedTargets[0] = "10.0.0.9:9090" }},
		{"pool", func(a *GeneratorAssignmentSettings) { a.PoolID = "another-pool" }},
		{"placement policy", func(a *GeneratorAssignmentSettings) { a.RequiredLabels = map[string]string{"zone": "different"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server.runner.mu.Lock()
			managed.Assignment = cloneGeneratorAssignmentSettings(resolved)
			tc.change(managed.Assignment)
			server.runner.mu.Unlock()
			changed, err := server.runner.preparationRevision(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if changed == revision {
				t.Fatal("authored placement change did not invalidate preparation")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.runner.mu.Lock()
	managed.Assignment = cloneGeneratorAssignmentSettings(resolved)
	managed.Status = "INITIALIZING"
	managed.executionCtx, managed.cancel = ctx, cancel
	err = server.runner.saveManagedTestRecord(managed)
	server.runner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.runner.awaitPreparedStart(managed) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := store.GetPreparation(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == "ready" {
			break
		}
		select {
		case err := <-done:
			// Keep deferred joining safe after observing an unexpected exit.
			done <- err
			if errors.Is(err, context.Canceled) {
				t.Fatal("preparation canceled before ready")
			}
			t.Fatalf("preparation did not become ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("preparation remained %s", current.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
