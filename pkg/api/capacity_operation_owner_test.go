package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/compute/azure"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newOwnedCapacityFixture(t *testing.T, ha bool) (*Server, *storage.SQLiteStore, *storage.FleetPool, *storage.ProvisioningRequest, *storage.CapacityInstanceIntent) {
	t.Helper()
	store := newCapacityOperationStore(t)
	pool := &storage.FleetPool{ID: "owner-pool", Name: "owner pool", Status: "active", ExecutionTargetID: "owner-target", DesiredSize: 2, ReadinessTimeoutSeconds: 30}
	saveCapacityTestPool(t, store, pool)
	deadline := time.Now().UTC().Add(time.Minute)
	request := &storage.ProvisioningRequest{ID: "owner-request", FleetPoolID: pool.ID, ExecutionTargetID: pool.ExecutionTargetID, Revision: 1, AttemptToken: "original-attempt", Status: "provisioning", DesiredCount: 4, DeadlineAt: &deadline}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatal(err)
	}
	intent := &storage.CapacityInstanceIntent{ID: "owner-intent", FleetPoolID: pool.ID, ExecutionTargetID: pool.ExecutionTargetID, ProvisioningRequestID: request.ID, ClientToken: "durable-client-token", LaunchNonce: "durable-nonce", GeneratorID: "durable-generator", CleanupDeadline: &deadline}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatal(err)
	}
	runner := &TestRunner{scenarioStore: store}
	if ha {
		runner.leadership = newExecutionLeadership(store, "capacity-controller")
		runner.durableExecutionFence = true
		runner.leadership.tick()
		waitForCondition(t, time.Second, runner.leadership.IsLeader, "capacity leadership")
		t.Cleanup(runner.leadership.relinquish)
	}
	return &Server{store: store, runner: runner}, store, pool, request, intent
}

func TestCapacityOwnerFencesLateProviderCompletionAfterTakeover(t *testing.T) {
	for _, sameProcess := range []bool{false, true} {
		for _, providerFails := range []bool{false, true} {
			name := map[bool]string{false: "success", true: "error"}[providerFails] + map[bool]string{false: "/successor", true: "/same_process"}[sameProcess]
			t.Run(name, func(t *testing.T) {
				server, store, pool, request, intent := newOwnedCapacityFixture(t, true)
				owner, err := server.capacityOperationOwner(request)
				if err != nil || !server.beginCapacityOperation(owner) {
					t.Fatalf("capture owner: %v", err)
				}
				started := make(chan struct{}, 1)
				release, releaseProvider := capacityTestBarrier(t)
				backend := &fakeCapacityBackend{reconcileStarted: started, reconcileRelease: release, observation: compute.Observation{ProviderOperationID: "late-provider-id", Requested: 4, Observed: 4, Ready: 4, Terminal: true, Instances: []compute.Instance{{ProviderID: "late-provider-id", GeneratorID: intent.GeneratorID, Healthy: true}}}}
				if providerFails {
					backend.reconcileErr = errors.New("old provider failure")
				}
				base := server.asyncCapacityRequest(pool, request)
				base.RestoreStanding, base.RollbackStanding = true, 2
				done := make(chan struct{})
				go func() {
					defer close(done)
					server.reconcileAsyncCapacityOperation(owner, pool, backend, base, []*storage.CapacityInstanceIntent{intent})
				}()
				waitCapacityTestSignal(t, started, "provider start")
				server.runner.leadership.relinquish()
				successor := server.runner.leadership
				if !sameProcess {
					successor = newExecutionLeadership(store, "capacity-successor")
					t.Cleanup(successor.relinquish)
				}
				successor.tick()
				waitForCondition(t, time.Second, successor.IsLeader, "successor capacity leadership")
				select {
				case <-owner.ctx.Done():
				case <-time.After(time.Second):
					t.Fatal("old provider context survived leadership loss")
				}
				if sameProcess && server.fleetProvisioningAttemptActive(request.ID) {
					t.Fatal("blocked old provider prevented same-process recovery")
				}
				claimed := *request
				claimed.Revision++
				claimed.AttemptToken, claimed.Status, claimed.ProviderOperationID = "successor-attempt", "success", `["successor-provider-id"]`
				if ok, err := store.TransitionProvisioningRequest(&claimed, request.Revision, request.AttemptToken); err != nil || !ok {
					t.Fatalf("successor claim = %v, %v", ok, err)
				}
				if err := updateFleetPoolDesiredSize(store, pool.ID, 4); err != nil {
					t.Fatal(err)
				}
				// Also ensure deferred old-worker cleanup cannot remove the successor.
				if sameProcess && !server.beginFleetProvisioningAttempt(request.ID, claimed.AttemptToken) {
					t.Fatal("could not register successor attempt")
				}
				before, _ := store.GetCapacityInstanceIntent(intent.ID)
				releaseProvider()
				waitCapacityTestSignal(t, done, "worker completion")
				after, _ := store.GetCapacityInstanceIntent(intent.ID)
				got, _ := store.GetProvisioningRequest(request.ID)
				gotPool, _ := store.GetFleetPool(pool.ID)
				if backend.cancel != 0 || got.AttemptToken != claimed.AttemptToken || got.ProviderOperationID != claimed.ProviderOperationID || got.Revision != claimed.Revision || gotPool.DesiredSize != 4 || !reflect.DeepEqual(before, after) {
					t.Fatalf("stale completion mutated successor: cancel=%d request=%+v pool=%+v intent=%+v", backend.cancel, got, gotPool, after)
				}
				if sameProcess && !server.fleetProvisioningAttemptActive(request.ID) {
					t.Fatal("old deferred cleanup removed successor attempt")
				}
			})
		}
	}
}

func TestCapacityOwnerCannotAdoptAttemptChangedBeforeWorkerStarts(t *testing.T) {
	for _, status := range []string{"provisioning", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			server, store, pool, request, intent := newOwnedCapacityFixture(t, false)
			owner, err := server.capacityOperationOwner(request)
			if err != nil {
				t.Fatal(err)
			}
			replacement := *request
			replacement.Revision++
			replacement.AttemptToken, replacement.Status = "replacement-attempt", status
			if ok, err := store.TransitionProvisioningRequest(&replacement, request.Revision, request.AttemptToken); err != nil || !ok {
				t.Fatalf("replace attempt: %v, %v", ok, err)
			}
			backend := &fakeCapacityBackend{}
			server.reconcileAsyncCapacityOperation(owner, pool, backend, server.asyncCapacityRequest(pool, request), []*storage.CapacityInstanceIntent{intent})
			if backend.reconcile != 0 || backend.cancel != 0 {
				t.Fatalf("stale launch reached provider: %+v", backend)
			}
		})
	}
}

func TestRunCapacityOwnerDoesNotCleanUpAfterLeaseReacquisition(t *testing.T) {
	server, store, pool, _, _ := newOwnedCapacityFixture(t, true)
	// The fixture's unrelated request must not block creating the run request.
	if _, err := store.TransitionProvisioningRequest(&storage.ProvisioningRequest{ID: "owner-request", FleetPoolID: pool.ID, Revision: 2, AttemptToken: "original-attempt", Status: "success"}, 1, "original-attempt"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	release, releaseProvider := capacityTestBarrier(t)
	backend := &fakeCapacityBackend{reconcileStarted: started, reconcileRelease: release, reconcileErr: errors.New("late failure"), observation: compute.Observation{ProviderOperationID: "late-run-instance"}}
	server.capacityBackends = fakeCapacityResolver{backend, &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAzureContainerInstances}}
	done := make(chan error, 1)
	go func() {
		done <- server.ensureRunCapacity(context.Background(), &ManagedTest{ID: "run-owner", Assignment: &GeneratorAssignmentSettings{PoolID: pool.ID}})
	}()
	waitCapacityTestSignal(t, started, "provider start")
	server.runner.leadership.relinquish()
	server.runner.leadership.tick()
	waitForCondition(t, time.Second, server.runner.leadership.IsLeader, "reacquisition")
	releaseProvider()
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run capacity did not stop")
	}
	if runErr == nil {
		t.Fatal("stale run unexpectedly became ready")
	}
	if backend.cancel != 0 {
		t.Fatal("old run rolled back capacity after reacquisition")
	}
	intents, err := store.ListCapacityInstanceIntents("", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, intent := range intents {
		if intent.TestID == "run-owner" && (intent.State != storage.CapacityIntentPlanned || intent.ProviderID != "") {
			t.Fatalf("stale run mutated recovery intent: %+v", intent)
		}
	}
}

type plannedCapacityBackend struct {
	*fakeCapacityBackend
	planner     compute.Backend
	onReconcile func(compute.CapacityRequest)
}

func (b *plannedCapacityBackend) Plan(ctx context.Context, request compute.CapacityRequest) (compute.Plan, error) {
	return b.planner.Plan(ctx, request)
}
func (b *plannedCapacityBackend) Reconcile(ctx context.Context, request compute.CapacityRequest) (compute.Observation, error) {
	if b.onReconcile != nil {
		b.onReconcile(request)
	}
	return b.fakeCapacityBackend.Reconcile(ctx, request)
}

func TestOperatorCapacityRejectsACIWithoutPersistingOrLaunching(t *testing.T) {
	store := newCapacityOperationStore(t)
	pool := &storage.FleetPool{ID: "aci-pool", Name: "aci", Status: "active", ExecutionTargetID: "aci-target"}
	saveCapacityTestPool(t, store, pool)
	target := &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAzureContainerInstances, BurstOwner: storage.CapacityOwnerApex, AzureContainerInstances: &storage.AzureContainerInstancesTargetSpec{ContainerGroupTemplate: "template"}}
	backend := &plannedCapacityBackend{fakeCapacityBackend: &fakeCapacityBackend{}, planner: &azure.InstancesBackend{Target: target}}
	server := &Server{store: store, executionTargetStore: store, capacityBackends: fakeCapacityResolver{backend, target}}
	request := &storage.ProvisioningRequest{ID: "unsupported-warm", Revision: 1, AttemptToken: "attempt", FleetPoolID: pool.ID, DesiredCount: 2}
	handled, err := server.startAsyncCapacityProvisioning(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), pool, request, time.Now().UTC())
	if !handled || !errors.Is(err, compute.ErrUnsupportedOperation) {
		t.Fatalf("operator admission = %v, %v", handled, err)
	}
	requests, err := store.ListProvisioningRequests(pool.ID, 10, 0)
	if err != nil || len(requests) != 0 {
		t.Fatalf("unsupported request persisted: %+v, %v", requests, err)
	}
	intents, err := store.ListCapacityInstanceIntents("", "")
	if err != nil || len(intents) != 0 || backend.reconcile != 0 || backend.cancel != 0 {
		t.Fatalf("unsupported request launched resources: intents=%+v, err=%v", intents, err)
	}
}

func TestRunOwnedACIRemainsSupportedForDirectAndReverse(t *testing.T) {
	for _, mode := range []string{storage.ConnectivityDirect, storage.ConnectivityReverse} {
		t.Run(mode, func(t *testing.T) {
			server, genericStore := newTimeslotServer(t)
			store := genericStore.(*storage.SQLiteStore)
			pool := &storage.FleetPool{ID: "run-aci-pool", Name: "run ACI", Status: "active", ExecutionTargetID: "run-aci-target", ReadinessTimeoutSeconds: 30}
			saveCapacityTestPool(t, store, pool)
			target := &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAzureContainerInstances, ConnectivityMode: mode, BurstOwner: storage.CapacityOwnerApex, AzureContainerInstances: &storage.AzureContainerInstancesTargetSpec{ContainerGroupTemplate: "template"}}
			backend := &plannedCapacityBackend{fakeCapacityBackend: &fakeCapacityBackend{}, planner: &azure.InstancesBackend{Target: target}}
			backend.onReconcile = func(request compute.CapacityRequest) {
				if request.TestID != "aci-run" || len(request.LaunchIntents) != 1 {
					t.Fatalf("missing run owner: %+v", request)
				}
				id := request.LaunchIntents[0].GeneratorID
				server.reverseTargetSessions = map[string]map[string]struct{}{target.ID: {id: {}}}
				backend.observation = compute.Observation{Requested: 1, Observed: 1, Ready: 1, Terminal: true, ProviderOperationID: "aci-instance", Instances: []compute.Instance{{ProviderID: "aci-instance", GeneratorID: id, Address: "10.0.0.8:9090", Healthy: true}}}
			}
			server.capacityBackends = fakeCapacityResolver{backend, target}
			server.capacityHeartbeatProbe = func(string) bool { return true }
			test := &ManagedTest{ID: "aci-run", Assignment: &GeneratorAssignmentSettings{PoolID: pool.ID}}
			if err := server.ensureRunCapacity(context.Background(), test); err != nil {
				t.Fatal(err)
			}
			intents, err := store.ListCapacityInstanceIntents(target.ID, "")
			if err != nil || len(intents) != 1 || intents[0].TestID != test.ID || intents[0].State != storage.CapacityIntentReady {
				t.Fatalf("run intents = %+v, %v", intents, err)
			}
			want := "10.0.0.8:9090"
			if mode == storage.ConnectivityReverse {
				want = generatorrpc.ReverseTargetPrefix + intents[0].GeneratorID
			}
			if !reflect.DeepEqual(test.Assignment.SelectedTargets, []string{want}) {
				t.Fatalf("run targets = %v", test.Assignment.SelectedTargets)
			}
		})
	}
}

func capacityTestBarrier(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	return release, finish
}
func waitCapacityTestSignal(t *testing.T, signal <-chan struct{}, detail string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", detail)
	}
}

type blockedFirstCancelBackend struct {
	*fakeCapacityBackend
	started chan struct{}
	release <-chan struct{}
}

func (b *blockedFirstCancelBackend) Cancel(ctx context.Context, request compute.CapacityRequest) error {
	b.mu.Lock()
	b.cancel++
	first := b.cancel == 1
	b.lastRequest = request
	b.mu.Unlock()
	if first {
		close(b.started)
		<-b.release // model a provider returning after its context was cancelled
		return errors.New("late old cancellation failure")
	}
	return nil
}

func TestCapacityCancellationIsRecoveredBySuccessorWithoutScaleUp(t *testing.T) {
	server, store, pool, request, intent := newOwnedCapacityFixture(t, true)
	request.Status = "cancelling"
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatal(err)
	}
	owner, err := server.capacityOperationOwner(request)
	if err != nil || !server.beginCapacityOperation(owner) {
		t.Fatalf("cancel owner: %v", err)
	}
	release, releaseProvider := capacityTestBarrier(t)
	backend := &blockedFirstCancelBackend{fakeCapacityBackend: &fakeCapacityBackend{}, started: make(chan struct{}), release: release}
	server.capacityBackends = fakeCapacityResolver{backend, &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer owner.finish()
		_ = server.cancelOwnedCapacityOperation(owner, backend, server.asyncCapacityRequest(pool, request), []*storage.CapacityInstanceIntent{intent})
	}()
	waitCapacityTestSignal(t, backend.started, "old cancellation")
	server.runner.leadership.relinquish()
	server.runner.leadership.tick()
	waitForCondition(t, time.Second, server.runner.leadership.IsLeader, "cancel successor")
	if err := server.reconcileProvisioningRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		got, err := store.GetProvisioningRequest(request.ID)
		return err == nil && got.Status == "cancelled"
	}, "successor cancellation")
	releaseProvider()
	waitCapacityTestSignal(t, done, "old cancellation completion")
	got, err := store.GetProvisioningRequest(request.ID)
	if err != nil || got.Status != "cancelled" || got.AttemptToken == request.AttemptToken {
		t.Fatalf("cancelled request = %+v, %v", got, err)
	}
	backend.mu.Lock()
	cancels, reconciles := backend.cancel, backend.reconcile
	backend.mu.Unlock()
	if cancels != 2 || reconciles != 0 {
		t.Fatalf("provider cancel=%d reconcile=%d", cancels, reconciles)
	}
	retained, err := store.GetCapacityInstanceIntent(intent.ID)
	if err != nil || retained.State != storage.CapacityIntentPlanned || retained.LaunchNonce == "" {
		t.Fatalf("unknown provider identity lost its recovery intent: %+v, %v", retained, err)
	}
}

type barrierCapacityResolver struct {
	fakeCapacityResolver
	started chan struct{}
	release <-chan struct{}
}

func (r *barrierCapacityResolver) BackendForPool(ctx context.Context, pool *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error) {
	close(r.started)
	<-r.release
	return r.fakeCapacityResolver.BackendForPool(ctx, pool)
}

func TestRunCapacityCannotAdoptNewEpochWhileResolvingProvider(t *testing.T) {
	server, store, pool, _, _ := newOwnedCapacityFixture(t, true)
	release, releaseProvider := capacityTestBarrier(t)
	backend := &fakeCapacityBackend{}
	resolver := &barrierCapacityResolver{fakeCapacityResolver: fakeCapacityResolver{backend, &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAzureContainerInstances}}, started: make(chan struct{}), release: release}
	server.capacityBackends = resolver
	ctx, cancel := server.runner.executionOperationContext(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- server.ensureRunCapacity(ctx, &ManagedTest{ID: "old-epoch-run", Assignment: &GeneratorAssignmentSettings{PoolID: pool.ID}})
	}()
	waitCapacityTestSignal(t, resolver.started, "provider resolution")
	server.runner.leadership.relinquish()
	server.runner.leadership.tick()
	waitForCondition(t, time.Second, server.runner.leadership.IsLeader, "resolver successor")
	releaseProvider()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("old caller adopted a new epoch")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old caller did not stop")
	}
	requests, err := store.ListProvisioningRequests(pool.ID, 10, 0)
	if err != nil || len(requests) != 1 || backend.reconcile != 0 || backend.cancel != 0 {
		t.Fatalf("old caller mutated capacity: requests=%d reconcile=%d cancel=%d error=%v", len(requests), backend.reconcile, backend.cancel, err)
	}
}

type warmCapacityBackend struct{ *fakeCapacityBackend }

func (b *warmCapacityBackend) Plan(_ context.Context, request compute.CapacityRequest) (compute.Plan, error) {
	return compute.Plan{Requested: request.Desired, Standing: request.Desired}, nil
}

func TestOperatorCapacityAcceptsWarmCreateAndRetry(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "retry"}[retry], func(t *testing.T) {
			store := newCapacityOperationStore(t)
			pool := &storage.FleetPool{ID: "warm-pool", Name: "warm pool", Status: "active", ExecutionTargetID: "warm-target", ReadinessTimeoutSeconds: 30}
			saveCapacityTestPool(t, store, pool)
			backend := &warmCapacityBackend{&fakeCapacityBackend{observation: compute.Observation{Requested: 2, Observed: 2, Ready: 2, Terminal: true}}}
			server := &Server{store: store, executionTargetStore: store, capacityBackends: fakeCapacityResolver{backend, &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}}
			request := &storage.ProvisioningRequest{ID: "warm-request", Revision: 1, AttemptToken: "initial-attempt", FleetPoolID: pool.ID, ExecutionTargetID: pool.ExecutionTargetID, DesiredCount: 2, Status: "failed"}
			var handled bool
			var err error
			if retry {
				if err := store.SaveProvisioningRequest(request); err != nil {
					t.Fatal(err)
				}
				handled, err = server.restartAsyncCapacityProvisioning(request, pool)
			} else {
				handled, err = server.startAsyncCapacityProvisioning(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), pool, request, time.Now().UTC())
			}
			if err != nil || !handled {
				t.Fatalf("warm admission = %v, %v", handled, err)
			}
			waitForCondition(t, 5*time.Second, func() bool {
				got, err := store.GetProvisioningRequest(request.ID)
				return err == nil && got.Status == "success"
			}, "warm capacity completion")
			server.backgroundWG.Wait()
			got, err := store.GetFleetPool(pool.ID)
			if err != nil || got.DesiredSize != 2 {
				t.Fatalf("committed warm pool = %+v, %v", got, err)
			}
			intents, err := store.ListCapacityInstanceIntents("", "")
			if err != nil || len(intents) != 0 || backend.reconcile != 1 || backend.cancel != 0 {
				t.Fatalf("warm operation launched bursts: intents=%d reconcile=%d cancel=%d err=%v", len(intents), backend.reconcile, backend.cancel, err)
			}
		})
	}
}
