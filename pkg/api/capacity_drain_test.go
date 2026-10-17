package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newScaleDownCapacityFixture(t *testing.T, ha bool) (*Server, *storage.SQLiteStore, *storage.FleetPool, *storage.ProvisioningRequest, *distributedExecutionBackend) {
	t.Helper()
	server, store, pool, request, _ := newOwnedCapacityFixture(t, ha)
	request.DesiredCount = 1
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatal(err)
	}
	backend := newDistributedExecutionBackend([]string{"10.0.0.1:9090", "10.0.0.2:9090"}, time.Second, nil)
	server.runner.setExecutionBackend(backend)
	for _, member := range []*storage.FleetPoolMember{
		{ID: "auto-member", PoolID: pool.ID, GeneratorID: "gen-auto", Target: "10.0.0.1:9090"},
		{ID: "operator-member", PoolID: pool.ID, GeneratorID: "gen-operator", Target: "10.0.0.2:9090"},
	} {
		if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: member.GeneratorID, Target: member.Target, FleetPoolID: &pool.ID}); err != nil {
			t.Fatal(err)
		}
		backend.recordGeneratorIdentity(member.Target, member.GeneratorID)
	}
	if err := store.SaveFleetPoolMembers(pool.ID, []*storage.FleetPoolMember{
		{ID: "auto-member", PoolID: pool.ID, GeneratorID: "gen-auto", Target: "10.0.0.1:9090"},
		{ID: "operator-member", PoolID: pool.ID, GeneratorID: "gen-operator", Target: "10.0.0.2:9090"},
	}); err != nil {
		t.Fatal(err)
	}
	return server, store, pool, request, backend
}

type scaleDownCancelBarrier struct {
	*fakeCapacityBackend
	started chan struct{}
	release <-chan struct{}
}

func (b *scaleDownCancelBarrier) Cancel(ctx context.Context, request compute.CapacityRequest) error {
	err := b.fakeCapacityBackend.Cancel(ctx, request)
	close(b.started)
	<-b.release
	return err
}

func TestScaleDownCancellationRestoresSurvivorsAndPreservesOperatorIntent(t *testing.T) {
	for _, manual := range []string{"active", "cordoned", "draining"} {
		t.Run(manual, func(t *testing.T) {
			server, store, pool, request, live := newScaleDownCapacityFixture(t, false)
			owner, err := server.capacityOperationOwner(request)
			if err != nil || !server.beginCapacityOperation(owner) {
				t.Fatalf("capture scale-down owner: %v", err)
			}
			started := make(chan struct{}, 1)
			release, releaseReconcile := capacityTestBarrier(t)
			cancelRelease, releaseCancel := capacityTestBarrier(t)
			provider := &scaleDownCancelBarrier{fakeCapacityBackend: &fakeCapacityBackend{reconcileStarted: started, reconcileRelease: release, observation: compute.Observation{Requested: 1, Ready: 1, Observed: 1, Terminal: true}}, started: make(chan struct{}), release: cancelRelease}
			server.capacityBackends = fakeCapacityResolver{provider, &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}
			done := make(chan struct{})
			go func() {
				defer close(done)
				server.reconcileAsyncCapacityOperation(owner, pool, provider, server.asyncCapacityRequest(pool, request), nil)
			}()
			waitCapacityTestSignal(t, started, "scale-down provider")
			if live.GeneratorDrainStateByID("gen-auto") != "cordoned" || len(live.configuredTargets()) != 0 || len(live.GeneratorDrainStates()) != 0 {
				t.Fatal("scale-down did not create an unpersisted placement exclusion")
			}
			// This operator action happens after the capacity operation started.
			if _, err := live.SetGeneratorDrainStateByID("gen-operator", manual); err != nil {
				t.Fatal(err)
			}
			cancelRequest := httptest.NewRequest(http.MethodPost, "/", nil)
			cancelRequest.SetPathValue("id", request.ID)
			response := httptest.NewRecorder()
			cancelDone := make(chan struct{})
			go func() {
				defer close(cancelDone)
				server.handleCancelProvisioningRequest(response, cancelRequest)
			}()
			waitCapacityTestSignal(t, provider.started, "provider cancellation")
			releaseReconcile()
			waitCapacityTestSignal(t, done, "late scale-down completion")
			if live.GeneratorDrainStateByID("gen-auto") != "cordoned" {
				t.Fatal("old cleanup cleared the cancellation owner's transferred exclusion")
			}
			releaseCancel()
			waitCapacityTestSignal(t, cancelDone, "cancellation completion")
			if response.Code != http.StatusOK {
				t.Fatalf("cancel status=%d body=%s", response.Code, response.Body.String())
			}
			if live.GeneratorDrainStateByID("gen-auto") != "active" || live.GeneratorDrainStateByID("gen-operator") != manual {
				t.Fatalf("survivor=%s operator=%s, want active/%s", live.GeneratorDrainStateByID("gen-auto"), live.GeneratorDrainStateByID("gen-operator"), manual)
			}
			got, err := store.GetProvisioningRequest(request.ID)
			if err != nil || got.Status != "cancelled" || got.AttemptToken == owner.attemptToken {
				t.Fatalf("cancel result=%+v err=%v", got, err)
			}
		})
	}
}

func TestScaleDownDrainTransfersAcrossLeaseReacquisition(t *testing.T) {
	server, store, pool, request, live := newScaleDownCapacityFixture(t, true)
	owner, err := server.capacityOperationOwner(request)
	if err != nil || !server.beginCapacityOperation(owner) {
		t.Fatalf("capture scale-down owner: %v", err)
	}
	started := make(chan struct{}, 1)
	oldRelease, releaseOld := capacityTestBarrier(t)
	newRelease, releaseNew := capacityTestBarrier(t)
	provider := &fakeCapacityBackend{reconcileStarted: started, reconcileRelease: oldRelease, observation: compute.Observation{Requested: 1, Ready: 1, Observed: 1, Terminal: true}}
	server.capacityBackends = fakeCapacityResolver{provider, &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.reconcileAsyncCapacityOperation(owner, pool, provider, server.asyncCapacityRequest(pool, request), nil)
	}()
	waitCapacityTestSignal(t, started, "old scale-down")
	if live.GeneratorDrainStateByID("gen-auto") != "cordoned" {
		t.Fatal("scale-down did not cordon the live generator")
	}
	server.runner.leadership.relinquish()
	server.runner.leadership.tick()
	waitForCondition(t, time.Second, server.runner.leadership.IsLeader, "scale-down successor")
	if live.GeneratorDrainStateByID("gen-auto") != "active" {
		t.Fatal("expired epoch stranded a cordon while its provider remained blocked")
	}
	if _, err := live.SetGeneratorDrainStateByID("gen-operator", "cordoned"); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.reconcileRelease = newRelease
	provider.mu.Unlock()
	if err := server.reconcileProvisioningRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitCapacityTestSignal(t, started, "successor scale-down")
	releaseOld()
	waitCapacityTestSignal(t, done, "old scale-down cleanup")
	if live.GeneratorDrainStateByID("gen-auto") != "cordoned" {
		t.Fatal("old cleanup removed the successor's exclusion")
	}
	releaseNew()
	successorDone := make(chan struct{})
	go func() { server.backgroundWG.Wait(); close(successorDone) }()
	waitCapacityTestSignal(t, successorDone, "successor scale-down completion")
	if live.GeneratorDrainStateByID("gen-auto") != "active" || live.GeneratorDrainStateByID("gen-operator") != "cordoned" {
		t.Fatalf("successor left incorrect live states: auto=%s operator=%s", live.GeneratorDrainStateByID("gen-auto"), live.GeneratorDrainStateByID("gen-operator"))
	}
	got, err := store.GetProvisioningRequest(request.ID)
	if err != nil || got.Status != "success" || got.AttemptToken == owner.attemptToken {
		t.Fatalf("successor result=%+v err=%v", got, err)
	}
}

func TestCapacityDrainScopesCleanupAndKeepsOperatorStateSeparate(t *testing.T) {
	b := newDistributedExecutionBackend([]string{"gen:9090"}, time.Second, nil)
	b.recordGeneratorIdentity("gen:9090", "gen")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.setCapacityDrain(ctx, "first-request", "first-attempt", []string{"gen"})
	b.setCapacityDrain(ctx, "second-request", "second-attempt", []string{"gen"})
	b.clearCapacityDrain("first-request", "first-attempt")
	if b.generatorDrainState("gen:9090") != "cordoned" || b.GeneratorDrainStateByID("gen") != "cordoned" {
		t.Fatal("clearing one operation removed another operation's exclusion")
	}
	if b.GeneratorOperatorDrainStateByID("gen") != "active" || len(b.GeneratorDrainStates()) != 0 {
		t.Fatal("capacity drain leaked into persistent operator intent")
	}
	b.ApplyGeneratorDrainStates(nil)
	if len(b.configuredTargets()) != 0 {
		t.Fatal("operator state reload removed a live capacity exclusion")
	}
	cancel()
	if b.GeneratorDrainStateByID("gen") != "active" || len(b.configuredTargets()) != 1 {
		t.Fatal("expired capacity owner still excluded placement")
	}
	b.setCapacityDrain(ctx, "late-request", "late-attempt", []string{"gen"})
	if b.GeneratorDrainStateByID("gen") != "active" {
		t.Fatal("expired owner installed a new placement exclusion")
	}
}
