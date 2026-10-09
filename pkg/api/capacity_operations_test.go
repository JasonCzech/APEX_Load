package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

type fakeCapacityBackend struct {
	mu               sync.Mutex
	observation      compute.Observation
	reconcileErr     error
	reconcile        int
	cancel           int
	drain            int
	deprovision      int
	lastRequest      compute.CapacityRequest
	discovered       []compute.Instance
	discoverErr      error
	discoverCalls    int
	adopted          map[string]compute.Instance
	adoptRequests    []compute.AdoptionRequest
	reconcileStarted chan<- struct{}
	reconcileRelease <-chan struct{}
}

func (b *fakeCapacityBackend) Describe() compute.BackendInfo {
	return compute.BackendInfo{CanLaunch: true}
}
func (b *fakeCapacityBackend) Validate(context.Context) []compute.ProbeResult { return nil }
func (b *fakeCapacityBackend) Plan(_ context.Context, request compute.CapacityRequest) (compute.Plan, error) {
	return compute.Plan{Requested: request.Desired, Launch: request.Desired}, nil
}
func (b *fakeCapacityBackend) Reconcile(_ context.Context, request compute.CapacityRequest) (compute.Observation, error) {
	b.mu.Lock()
	b.reconcile++
	b.lastRequest = request
	observation, reconcileErr := b.observation, b.reconcileErr
	started, release := b.reconcileStarted, b.reconcileRelease
	b.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	return observation, reconcileErr
}
func (b *fakeCapacityBackend) Cancel(_ context.Context, request compute.CapacityRequest) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancel++
	b.lastRequest = request
	return nil
}

func (b *fakeCapacityBackend) Drain(context.Context, compute.CapacityRequest, []compute.Instance) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.drain++
	return nil
}
func (b *fakeCapacityBackend) Deprovision(context.Context, compute.CapacityRequest, []compute.Instance) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deprovision++
	return nil
}
func (b *fakeCapacityBackend) Discover(context.Context, compute.CapacityRequest) ([]compute.Instance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.discoverCalls++
	if b.discoverErr != nil {
		return nil, b.discoverErr
	}
	return append([]compute.Instance(nil), b.discovered...), nil
}
func (b *fakeCapacityBackend) Adopt(_ context.Context, request compute.AdoptionRequest) (compute.Instance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.adoptRequests = append(b.adoptRequests, request)
	instance, ok := b.adopted[request.ProviderID]
	if !ok {
		return compute.Instance{}, compute.ErrUnsupportedOperation
	}
	return instance, nil
}

type fakeCapacityResolver struct {
	backend compute.Backend
	target  *storage.ExecutionTarget
}

type fakeIdentityDrainBackend struct {
	targets map[string]string
	states  map[string]string
}

func (*fakeIdentityDrainBackend) Run(*TestRunner, *ManagedTest) {}
func (b *fakeIdentityDrainBackend) SetGeneratorDrainStateByID(generatorID, state string) (string, error) {
	if b.targets[generatorID] == "" {
		return "", errors.New("generator is not connected")
	}
	b.states[generatorID] = state
	return b.targets[generatorID], nil
}
func (b *fakeIdentityDrainBackend) GeneratorTargetForID(generatorID string) string {
	return b.targets[generatorID]
}
func (b *fakeIdentityDrainBackend) GeneratorIDForTarget(target string) string {
	for id, candidate := range b.targets {
		if candidate == target {
			return id
		}
	}
	return ""
}

func (r fakeCapacityResolver) BackendForPool(context.Context, *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error) {
	return r.backend, r.target, nil
}

func newCapacityOperationStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "capacity.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func saveCapacityTestPool(t *testing.T, store storage.Store, pool *storage.FleetPool) {
	t.Helper()
	if pool.ExecutionTargetID != "" {
		targetStore, ok := store.(storage.ExecutionTargetStore)
		if !ok {
			t.Fatal("test store does not support execution targets")
		}
		if _, err := targetStore.GetExecutionTarget(pool.ExecutionTargetID); err != nil {
			target := &storage.ExecutionTarget{
				ID: pool.ExecutionTargetID, Name: pool.ExecutionTargetID, Kind: storage.ExecutionTargetStatic,
				ConnectivityMode: storage.ConnectivityDirect, StandingOwner: storage.CapacityOwnerApex,
				BurstOwner: storage.CapacityOwnerDisabled, ArchiveMode: storage.ArchiveModeControllerPull,
				Static: &storage.StaticExecutionTargetSpec{Targets: []string{"127.0.0.1:9090"}}, Status: "active",
			}
			if err := targetStore.CreateExecutionTarget(target); err != nil {
				t.Fatalf("CreateExecutionTarget: %v", err)
			}
		}
	}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
}

func TestReconcileAsyncCapacityPersistsReadyProviderInstance(t *testing.T) {
	store := newCapacityOperationStore(t)
	pool := &storage.FleetPool{ID: "pool-1", Name: "burst", Status: "active", ExecutionTargetID: "target-1", LifecycleMode: storage.FleetLifecycleWarmBurst, ReadinessTimeoutSeconds: 30}
	saveCapacityTestPool(t, store, pool)
	deadline := time.Now().UTC().Add(time.Minute)
	request := &storage.ProvisioningRequest{ID: "request-1", Revision: 1, AttemptToken: "attempt-1", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 1, ExecutionTargetID: pool.ExecutionTargetID, IdempotencyKey: "request-1", DeadlineAt: &deadline}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatalf("SaveProvisioningRequest: %v", err)
	}
	intent := &storage.CapacityInstanceIntent{ID: "intent-1", ExecutionTargetID: pool.ExecutionTargetID, FleetPoolID: pool.ID, ProvisioningRequestID: request.ID, ClientToken: "token-1", LaunchNonce: "nonce-1", GeneratorID: "generator-fresh-1", CleanupDeadline: &deadline}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatalf("CreateCapacityInstanceIntent: %v", err)
	}
	backend := &fakeCapacityBackend{observation: compute.Observation{
		ProviderOperationID: "provider-task-1", Requested: 1, Observed: 1, Ready: 1, Terminal: true,
		Instances: []compute.Instance{{ProviderID: "provider-task-1", GeneratorID: intent.GeneratorID, State: "running", Healthy: true}},
	}}
	server := &Server{store: store, fleetProvisioningActive: map[string]string{request.ID: request.AttemptToken}}
	base := server.asyncCapacityRequest(pool, request)
	server.reconcileAsyncCapacityOperation(request.ID, pool, backend, base, []*storage.CapacityInstanceIntent{intent})

	got, err := store.GetProvisioningRequest(request.ID)
	if err != nil {
		t.Fatalf("GetProvisioningRequest: %v", err)
	}
	if got.Status != "success" || got.ReadyCount != 1 || got.CompletedAt == nil {
		t.Fatalf("request = %+v, want terminal success with one ready instance", got)
	}
	gotIntent, err := store.GetCapacityInstanceIntent(intent.ID)
	if err != nil {
		t.Fatalf("GetCapacityInstanceIntent: %v", err)
	}
	if gotIntent.ProviderID != "provider-task-1" || gotIntent.State != storage.CapacityIntentReady {
		t.Fatalf("intent = %+v, want ready provider identity", gotIntent)
	}
	if len(backend.lastRequest.LaunchIntents) != 1 || backend.lastRequest.LaunchIntents[0].GeneratorID != "generator-fresh-1" {
		t.Fatalf("launch intents = %+v, want durable fresh generator identity", backend.lastRequest.LaunchIntents)
	}
}

func TestReconcileAsyncCapacityDeadlineCancelsInsteadOfSpinning(t *testing.T) {
	store := newCapacityOperationStore(t)
	pool := &storage.FleetPool{ID: "pool-timeout", Name: "timeout", Status: "active", ExecutionTargetID: "target-timeout", LifecycleMode: storage.FleetLifecycleWarmBurst}
	saveCapacityTestPool(t, store, pool)
	deadline := time.Now().UTC().Add(-time.Second)
	request := &storage.ProvisioningRequest{ID: "request-timeout", Revision: 1, AttemptToken: "attempt-timeout", FleetPoolID: pool.ID, Status: "waiting_ready", DesiredCount: 1, ExecutionTargetID: pool.ExecutionTargetID, ProviderOperationID: `["task-known"]`, DeadlineAt: &deadline}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatal(err)
	}
	backend := &fakeCapacityBackend{}
	server := &Server{store: store, fleetProvisioningActive: map[string]string{request.ID: request.AttemptToken}}
	base := server.asyncCapacityRequest(pool, request)
	server.reconcileAsyncCapacityOperation(request.ID, pool, backend, base, nil)

	got, err := store.GetProvisioningRequest(request.ID)
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	cancelCalls, reconcileCalls := backend.cancel, backend.reconcile
	providerIDs := append([]string(nil), backend.lastRequest.ProviderOperationIDs...)
	backend.mu.Unlock()
	if got.Status != "timed_out" || got.CompletedAt == nil || cancelCalls != 1 || reconcileCalls != 0 {
		t.Fatalf("request=%+v cancel=%d reconcile=%d", got, cancelCalls, reconcileCalls)
	}
	if len(providerIDs) != 1 || providerIDs[0] != "task-known" {
		t.Fatalf("cleanup provider IDs = %v", providerIDs)
	}
}

func TestEnsureRunCapacityCancellationCannotBeOverwrittenBySuccess(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{
		ID: "pool-run-cancel", Name: "run cancel", CloudProvider: storage.DeploymentProviderAWSECSFargate,
		Status: "active", ExecutionTargetID: "target-run-cancel", LifecycleMode: storage.FleetLifecycleOnDemand,
		ReadinessTimeoutSeconds: 30,
	}
	saveCapacityTestPool(t, store, pool)
	target := &storage.ExecutionTarget{
		ID: pool.ExecutionTargetID, Name: "run cancel", Kind: storage.ExecutionTargetAWSECSFargate,
		ConnectivityMode: storage.ConnectivityDirect,
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	backend := &fakeCapacityBackend{
		observation: compute.Observation{
			ProviderOperationID: "task-after-cancel", Requested: 1, Observed: 1, Ready: 1, Terminal: true,
			Instances: []compute.Instance{{ProviderID: "task-after-cancel", GeneratorID: "generator-after-cancel", Address: "10.0.0.9:9090", State: "running", Healthy: true}},
		},
		reconcileStarted: started, reconcileRelease: release,
	}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: target}
	server.capacityHeartbeatProbe = func(string) bool { return true }
	test := &ManagedTest{ID: "test-run-cancel", ProjectID: storage.DefaultProjectID, Assignment: &GeneratorAssignmentSettings{PoolID: pool.ID}}

	errCh := make(chan error, 1)
	go func() { errCh <- server.ensureRunCapacity(context.Background(), test) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("run capacity reconcile did not start")
	}
	requests, err := store.ListProvisioningRequests(pool.ID, 10, 0)
	if err != nil || len(requests) != 1 {
		t.Fatalf("provisioning requests = %d, %v", len(requests), err)
	}
	cancel := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests/"+requests[0].ID+"/cancel", nil)
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", cancel.Code, cancel.Body.String())
	}
	close(release)
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "changed concurrently") {
		t.Fatalf("ensureRunCapacity error = %v, want fenced concurrent-change failure", err)
	}
	durable, err := store.GetProvisioningRequest(requests[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Status != "cancelled" {
		t.Fatalf("durable request status = %q, want cancelled", durable.Status)
	}
}

func TestEffectiveProvisioningDesiredCountAllowsZeroOnlyForPaaS(t *testing.T) {
	paas := &storage.FleetPool{ExecutionTargetID: "target-paas", CloudProvider: storage.DeploymentProviderAWSECSFargate}
	if got, err := effectiveProvisioningDesiredCount(paas, 0); err != nil || got != 0 {
		t.Fatalf("PaaS scale-to-zero = %d, %v", got, err)
	}
	kubernetes := &storage.FleetPool{ExecutionTargetID: "target-kubernetes", CloudProvider: storage.DeploymentProviderKubernetes}
	if _, err := effectiveProvisioningDesiredCount(kubernetes, 0); err == nil {
		t.Fatal("Kubernetes scale-to-zero unexpectedly changed the existing positive-count contract")
	}
}

func TestProvisioningInstanceObservationOmitsLaunchCredentials(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	now := time.Now().UTC()
	request := &storage.ProvisioningRequest{
		ID: "request-observation", FleetPoolID: "pool-observation", ExecutionTargetID: "target-observation",
		Status: "waiting_ready", DesiredCount: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatal(err)
	}
	intent := &storage.CapacityInstanceIntent{
		ID: "intent-observation", ExecutionTargetID: request.ExecutionTargetID, FleetPoolID: request.FleetPoolID,
		ProvisioningRequestID: request.ID, ClientToken: "must-not-leak", LaunchNonce: "also-must-not-leak",
		GeneratorID: "generator-1", ProviderID: "provider-1", State: storage.CapacityIntentReady,
		ArchiveState: storage.CapacityArchivePending,
	}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatal(err)
	}
	response := timeslotRequest(t, server, http.MethodGet, "/api/v1/fleet/provision-requests/"+request.ID+"/instances", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), intent.ClientToken) || strings.Contains(response.Body.String(), intent.LaunchNonce) {
		t.Fatalf("instance observation leaked launch credentials: %s", response.Body.String())
	}
	var body CapacityInstanceListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Instances) != 1 || body.Instances[0].ProviderID != intent.ProviderID {
		t.Fatalf("instances = %+v", body)
	}
}

func TestCapacityIntentObservationDoesNotBindWarmTaskByPosition(t *testing.T) {
	store := newCapacityOperationStore(t)
	intent := &storage.CapacityInstanceIntent{
		ID: "intent-burst", ExecutionTargetID: "target-1", FleetPoolID: "pool-1",
		ClientToken: "token", LaunchNonce: "nonce-burst", GeneratorID: "generator-burst",
	}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatalf("CreateCapacityInstanceIntent: %v", err)
	}
	server := &Server{store: store}
	server.updateCapacityIntentObservations([]*storage.CapacityInstanceIntent{intent}, []compute.Instance{
		{ProviderID: "task-warm", GeneratorID: "generator-warm", State: "running", Healthy: true},
		{ProviderID: "task-burst", GeneratorID: "generator-burst", State: "running", Healthy: true},
	})

	got, err := store.GetCapacityInstanceIntent(intent.ID)
	if err != nil {
		t.Fatalf("GetCapacityInstanceIntent: %v", err)
	}
	if got.ProviderID != "task-burst" || got.State != storage.CapacityIntentReady {
		t.Fatalf("intent = %+v, want the identity-matched burst task", got)
	}
}

func TestEnsureRunCapacityCancelsKnownPartialCapacityOnReconcileFailure(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-partial", Name: "burst", Status: "active", ExecutionTargetID: "target-partial", LifecycleMode: storage.FleetLifecycleWarmBurst, ReadinessTimeoutSeconds: 30}
	saveCapacityTestPool(t, store, pool)
	backend := &fakeCapacityBackend{
		observation:  compute.Observation{ProviderOperationID: "provider-partial", Instances: []compute.Instance{{ProviderID: "provider-partial", GeneratorID: "generator-partial"}}},
		reconcileErr: errors.New("provider readiness failed"),
	}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}
	test := &ManagedTest{ID: "test-partial", ProjectID: "project", Assignment: &GeneratorAssignmentSettings{PoolID: pool.ID}}
	if err := server.ensureRunCapacity(context.Background(), test); err == nil {
		t.Fatal("ensureRunCapacity succeeded, want provider failure")
	}
	backend.mu.Lock()
	cancelCalls := backend.cancel
	backend.mu.Unlock()
	if cancelCalls != 1 {
		t.Fatalf("provider cancel calls = %d, want 1", cancelCalls)
	}
	requests, err := store.ListProvisioningRequests("", 10, 0)
	if err != nil || len(requests) != 1 || requests[0].Status != "failed" || requests[0].CompletedAt == nil {
		t.Fatalf("provisioning requests = %+v, err=%v", requests, err)
	}
}

func TestScaleDownCordonsConnectedPoolButPreservesOperatorCordon(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-drain", Name: "drain", Status: "active"}
	saveCapacityTestPool(t, store, pool)
	poolID := pool.ID
	for _, metadata := range []*storage.GeneratorMetadata{
		{GeneratorID: "gen-auto", Target: "10.0.0.1:9090", FleetPoolID: &poolID},
		{GeneratorID: "gen-operator", Target: "10.0.0.2:9090", FleetPoolID: &poolID, Cordoned: true},
	} {
		if err := store.SaveGeneratorMetadata(metadata); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveFleetPoolMembers(pool.ID, []*storage.FleetPoolMember{
		{ID: "member-auto", PoolID: pool.ID, GeneratorID: "gen-auto", Target: "10.0.0.1:9090"},
		{ID: "member-operator", PoolID: pool.ID, GeneratorID: "gen-operator", Target: "10.0.0.2:9090"},
	}); err != nil {
		t.Fatal(err)
	}
	backend := &fakeIdentityDrainBackend{targets: map[string]string{"gen-auto": "10.0.0.1:9090", "gen-operator": "10.0.0.2:9090"}, states: map[string]string{}}
	server.runner.setExecutionBackend(backend)
	cordoned, err := server.cordonFleetPoolForScaleDown(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cordoned) != 1 || cordoned[0] != "gen-auto" || backend.states["gen-auto"] != "cordoned" {
		t.Fatalf("transient cordons = %v states=%v", cordoned, backend.states)
	}
	if _, changed := backend.states["gen-operator"]; changed {
		t.Fatal("operator-owned cordon was modified")
	}
	server.restoreFleetPoolAfterScaleDown(cordoned)
	if backend.states["gen-auto"] != "active" {
		t.Fatalf("restored state = %q, want active", backend.states["gen-auto"])
	}
}

func TestPaaSExecutionTargetRefusesAmbiguousMultiPoolBinding(t *testing.T) {
	store := newCapacityOperationStore(t)
	connection := &storage.CloudConnection{ID: "cloud-exclusive", Name: "AWS", Provider: storage.CloudProviderAWS, AuthMode: storage.CloudAuthAWSAmbient, AWSRegion: "us-east-1"}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &storage.ExecutionTarget{ID: "target-exclusive", Name: "ECS exclusive", Kind: storage.ExecutionTargetAWSECSFargate, CloudConnectionID: connection.ID, ConnectivityMode: storage.ConnectivityDirect, StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex, AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "cluster", BurstTaskDefinition: "burst:1", Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"}}}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}
	first := &storage.FleetPool{ID: "pool-first", Name: "First", Status: "active", ExecutionTargetID: target.ID}
	if err := store.SaveFleetPool(first); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	status, err := server.validatePoolExecutionTarget(&storage.FleetPool{ID: "pool-second", Name: "Second", ExecutionTargetID: target.ID})
	if status != http.StatusConflict || err == nil || !strings.Contains(err.Error(), "one pool") {
		t.Fatalf("validation = (%d, %v)", status, err)
	}
	if status, err := server.validatePoolExecutionTarget(first); status != http.StatusOK || err != nil {
		t.Fatalf("self update validation = (%d, %v)", status, err)
	}
}

func TestPaaSExecutionTargetDefaultsNewPoolToWarmBurst(t *testing.T) {
	store := newCapacityOperationStore(t)
	connection := &storage.CloudConnection{ID: "cloud-default-lifecycle", Name: "AWS", Provider: storage.CloudProviderAWS, AuthMode: storage.CloudAuthAWSAmbient, AWSRegion: "us-east-1"}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &storage.ExecutionTarget{
		ID: "target-default-lifecycle", Name: "ECS", Kind: storage.ExecutionTargetAWSECSFargate,
		CloudConnectionID: connection.ID, ConnectivityMode: storage.ConnectivityDirect,
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex,
		Capabilities:  storage.TargetCapabilities{CanLaunch: true},
		AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "cluster", BurstTaskDefinition: "burst:1", Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"}},
	}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}
	pool := &storage.FleetPool{ID: "pool-default-lifecycle", Name: "Pool", ExecutionTargetID: target.ID}
	server := &Server{store: store}
	if status, err := server.validatePoolExecutionTarget(pool); status != http.StatusOK || err != nil {
		t.Fatalf("validation = (%d, %v)", status, err)
	}
	if pool.LifecycleMode != storage.FleetLifecycleWarmBurst {
		t.Fatalf("lifecycle_mode = %q, want %q", pool.LifecycleMode, storage.FleetLifecycleWarmBurst)
	}
}

func TestAsyncCapacityRequestRetainsProviderOperationsForCancel(t *testing.T) {
	request := &storage.ProvisioningRequest{ID: "request-1", FleetPoolID: "pool-1", DesiredCount: 2, ExecutionTargetID: "target-1", ProviderOperationID: `["task-1","task-2"]`}
	pool := &storage.FleetPool{ID: "pool-1", LifecycleMode: storage.FleetLifecycleWarmBurst, WarmFloor: 1, CapacityAdmission: storage.CapacityAdmissionStrict}
	got := (&Server{}).asyncCapacityRequest(pool, request)
	if len(got.ProviderOperationIDs) != 2 || got.ProviderOperationIDs[0] != "task-1" || got.WarmFloor != request.DesiredCount {
		t.Fatalf("capacity request = %+v", got)
	}
	request.Reason = automaticRunCapacityReasonPrefix + "test-1"
	pool.DesiredSize = 3
	got = (&Server{}).asyncCapacityRequest(pool, request)
	if got.WarmFloor != 3 {
		t.Fatalf("automatic run warm floor = %d, want committed warm capacity", got.WarmFloor)
	}
}

func TestOperatorWarmScaleCancellationRestoresCommittedDesiredSize(t *testing.T) {
	pool := &storage.FleetPool{ID: "pool-warm", LifecycleMode: storage.FleetLifecycleWarmBurst, WarmFloor: 1, DesiredSize: 2}
	request := compute.CapacityRequest{Desired: 4, WarmFloor: 4}
	applyStandingRollback(&request, pool, compute.BackendInfo{CanLaunch: true})
	if !request.RestoreStanding || request.RollbackStanding != 2 {
		t.Fatalf("rollback request = %+v", request)
	}
}

func TestCancelAsyncCapacityFencesWorkerAndCallsProvider(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-cancel", Name: "burst", Status: "active", ExecutionTargetID: "target-1", LifecycleMode: storage.FleetLifecycleWarmBurst}
	saveCapacityTestPool(t, store, pool)
	request := &storage.ProvisioningRequest{
		ID: "request-cancel", Revision: 1, AttemptToken: "attempt-old", FleetPoolID: pool.ID,
		Status: "waiting_ready", DesiredCount: 2, ExecutionTargetID: pool.ExecutionTargetID,
		ProviderOperationID: `["task-1","task-2"]`,
	}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatalf("SaveProvisioningRequest: %v", err)
	}
	backend := &fakeCapacityBackend{}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}
	server.fleetProvisioningActive[request.ID] = request.AttemptToken

	rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/fleet/provision-requests/"+request.ID+"/cancel", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response storage.ProvisioningRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "cancelled" || response.CompletedAt == nil {
		t.Fatalf("response = %+v, want terminal cancellation", response)
	}
	backend.mu.Lock()
	cancelCalls := backend.cancel
	providerIDs := append([]string(nil), backend.lastRequest.ProviderOperationIDs...)
	backend.mu.Unlock()
	if cancelCalls != 1 || len(providerIDs) != 2 || providerIDs[1] != "task-2" {
		t.Fatalf("cancel calls=%d provider IDs=%v", cancelCalls, providerIDs)
	}
	if server.fleetProvisioningAttemptActive(request.ID) {
		t.Fatal("cancelled request retained the old active-attempt fence")
	}
}

func TestReconcileProvisioningRequestsResumesAsyncCapacity(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-recover", Name: "burst", Status: "active", ExecutionTargetID: "target-1", LifecycleMode: storage.FleetLifecycleWarmBurst, ReadinessTimeoutSeconds: 30}
	saveCapacityTestPool(t, store, pool)
	deadline := time.Now().UTC().Add(time.Minute)
	request := &storage.ProvisioningRequest{ID: "request-recover", Revision: 1, AttemptToken: "attempt-old", FleetPoolID: pool.ID, Status: "waiting_ready", DesiredCount: 1, ExecutionTargetID: pool.ExecutionTargetID, ProviderOperationID: `["task-existing"]`, DeadlineAt: &deadline}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatalf("SaveProvisioningRequest: %v", err)
	}
	intent := &storage.CapacityInstanceIntent{ID: "intent-recover", ExecutionTargetID: pool.ExecutionTargetID, FleetPoolID: pool.ID, ProvisioningRequestID: request.ID, ClientToken: "token-recover", LaunchNonce: "nonce-recover", GeneratorID: "generator-recover"}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatalf("CreateCapacityInstanceIntent: %v", err)
	}
	backend := &fakeCapacityBackend{observation: compute.Observation{ProviderOperationID: "task-existing", Requested: 1, Observed: 1, Ready: 1, Terminal: true, Instances: []compute.Instance{{ProviderID: "task-existing", Healthy: true}}}}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}

	if err := server.reconcileProvisioningRequests(context.Background()); err != nil {
		t.Fatalf("reconcileProvisioningRequests: %v", err)
	}
	deadlineWait := time.Now().Add(3 * time.Second)
	for {
		got, err := store.GetProvisioningRequest(request.ID)
		if err != nil {
			t.Fatalf("GetProvisioningRequest: %v", err)
		}
		if got.Status == "success" {
			break
		}
		if time.Now().After(deadlineWait) {
			t.Fatalf("recovered request status = %q, want success", got.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEnsureRunCapacityCompletesBeforeRunAndAddsReadyTarget(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-run", Name: "run burst", Status: "active", ExecutionTargetID: "target-run", LifecycleMode: storage.FleetLifecycleWarmBurst, ReadinessTimeoutSeconds: 10, MaxSize: 5}
	saveCapacityTestPool(t, store, pool)
	backend := &fakeCapacityBackend{observation: compute.Observation{
		ProviderOperationID: "task-run", Requested: 1, Observed: 1, Ready: 1, Terminal: true,
		Instances: []compute.Instance{{ProviderID: "task-run", Address: "10.0.0.8:9090", State: "running", Healthy: true}},
	}}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate, ConnectivityMode: storage.ConnectivityDirect}}
	server.capacityHeartbeatProbe = func(address string) bool { return address == "10.0.0.8:9090" }
	test := &ManagedTest{ID: "test-run", ProjectID: "proj-a", Assignment: &GeneratorAssignmentSettings{PoolID: pool.ID}, CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: 1}}
	if err := server.ensureRunCapacity(context.Background(), test); err != nil {
		t.Fatalf("ensureRunCapacity: %v", err)
	}
	if len(test.Assignment.SelectedTargets) != 1 || test.Assignment.SelectedTargets[0] != "10.0.0.8:9090" {
		t.Fatalf("selected targets = %v", test.Assignment.SelectedTargets)
	}
	if test.Assignment.Mode != generatorAssignmentModeSpecificHosts {
		t.Fatalf("assignment mode = %q, want pool-isolated specific_hosts", test.Assignment.Mode)
	}
	intents, err := store.ListCapacityInstanceIntents("", "")
	if err != nil || len(intents) != 1 || intents[0].TestID != test.ID {
		t.Fatalf("run intents = %+v err=%v", intents, err)
	}
}

func TestReleaseRunCapacityWaitsForTerminalArchive(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-release", Name: "release", Status: "active", ExecutionTargetID: "target-release", LifecycleMode: storage.FleetLifecycleWarmBurst}
	saveCapacityTestPool(t, store, pool)
	intent := &storage.CapacityInstanceIntent{ID: "intent-release", ExecutionTargetID: pool.ExecutionTargetID, FleetPoolID: pool.ID, TestID: "test-release", ClientToken: "token", LaunchNonce: "nonce", GeneratorID: "generator-release", ProviderID: "provider-release", State: storage.CapacityIntentReady}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatal(err)
	}
	unknown := &storage.CapacityInstanceIntent{ID: "intent-provider-unknown", ExecutionTargetID: pool.ExecutionTargetID, FleetPoolID: pool.ID, TestID: intent.TestID, ClientToken: "unknown-token", LaunchNonce: "unknown-nonce", GeneratorID: "generator-provider-unknown", State: storage.CapacityIntentLaunching}
	if err := store.CreateCapacityInstanceIntent(unknown); err != nil {
		t.Fatal(err)
	}
	backend := &fakeCapacityBackend{}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}
	if err := server.associateDynamicGenerator(intent.GeneratorID, "10.0.0.9:9090", pool.ID, intent.ProviderID, "running"); err != nil {
		t.Fatal(err)
	}
	test := &ManagedTest{ID: intent.TestID}
	server.releaseRunCapacity(test, false)
	backend.mu.Lock()
	if backend.deprovision != 0 {
		backend.mu.Unlock()
		t.Fatal("capacity was deprovisioned before archive terminality")
	}
	backend.mu.Unlock()
	server.releaseRunCapacity(test, true)
	backend.mu.Lock()
	drainCalls, deprovisionCalls := backend.drain, backend.deprovision
	backend.mu.Unlock()
	if drainCalls != 1 || deprovisionCalls != 1 {
		t.Fatalf("drain=%d deprovision=%d", drainCalls, deprovisionCalls)
	}
	got, err := store.GetCapacityInstanceIntent(intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchiveState != storage.CapacityArchiveTerminal || got.State != storage.CapacityIntentTerminated {
		t.Fatalf("intent after release = %+v", got)
	}
	members, err := store.ListFleetPoolMembers(pool.ID)
	if err != nil || len(members) != 0 {
		t.Fatalf("terminated generator membership = %+v err=%v", members, err)
	}
	retained, err := store.GetCapacityInstanceIntent(unknown.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retained.State == storage.CapacityIntentTerminated {
		t.Fatal("intent without a provider identity was marked terminated and hidden from orphan reconciliation")
	}
}

func TestReverseCapacityReadinessRequiresProviderHealthAndAuthenticatedSession(t *testing.T) {
	server := &Server{reverseTargetSessions: map[string]map[string]struct{}{
		"target-reverse": {"generator-1": {}},
	}}
	request := compute.CapacityRequest{ExecutionTargetID: "target-reverse", ConnectivityMode: storage.ConnectivityReverse, Desired: 2}
	got := server.applyCapacityConnectivityReadiness(request, compute.Observation{Requested: 2, Observed: 2, Ready: 2, Terminal: true})
	if got.Ready != 1 || got.Pending != 1 || got.Terminal {
		t.Fatalf("one-session observation = %+v", got)
	}
	server.reverseTargetSessions["target-reverse"]["generator-2"] = struct{}{}
	got = server.applyCapacityConnectivityReadiness(request, compute.Observation{Requested: 2, Observed: 2, Ready: 2, Terminal: true})
	if got.Ready != 2 || !got.Terminal {
		t.Fatalf("two-session observation = %+v", got)
	}
}

func TestReverseCapacityReadinessPersistsPoolMembership(t *testing.T) {
	store := newCapacityOperationStore(t)
	pool := &storage.FleetPool{ID: "pool-reverse-membership", Name: "reverse", Status: "active"}
	saveCapacityTestPool(t, store, pool)
	server := &Server{store: store, reverseTargetSessions: map[string]map[string]struct{}{
		"target-reverse": {"generator-reverse": {}},
	}}
	request := compute.CapacityRequest{ExecutionTargetID: "target-reverse", FleetPoolID: pool.ID, ConnectivityMode: storage.ConnectivityReverse, Desired: 1}
	got := server.applyCapacityConnectivityReadiness(request, compute.Observation{Requested: 1, Observed: 1, Ready: 1, Terminal: true})
	if !got.Terminal || got.Ready != 1 {
		t.Fatalf("observation = %+v", got)
	}
	members, err := store.ListFleetPoolMembers(pool.ID)
	if err != nil || len(members) != 1 || members[0].GeneratorID != "generator-reverse" || members[0].Target != generatorrpc.ReverseTargetPrefix+"generator-reverse" {
		if len(members) == 1 {
			t.Fatalf("member = %+v err=%v", members[0], err)
		}
		t.Fatalf("members = %d err=%v", len(members), err)
	}
}

func TestReverseCapacityReadinessExcludesAnotherRunsBurstSession(t *testing.T) {
	store := newCapacityOperationStore(t)
	pool := &storage.FleetPool{ID: "pool-isolated", Name: "isolated", Status: "active"}
	saveCapacityTestPool(t, store, pool)
	for _, intent := range []*storage.CapacityInstanceIntent{
		{ID: "intent-current", ExecutionTargetID: "target-isolated", FleetPoolID: pool.ID, TestID: "test-current", ClientToken: "token-current", LaunchNonce: "nonce-current", GeneratorID: "generator-current"},
		{ID: "intent-other", ExecutionTargetID: "target-isolated", FleetPoolID: pool.ID, TestID: "test-other", ClientToken: "token-other", LaunchNonce: "nonce-other", GeneratorID: "generator-other"},
		{ID: "intent-terminated", ExecutionTargetID: "target-isolated", FleetPoolID: pool.ID, TestID: "test-current", ClientToken: "token-terminated", LaunchNonce: "nonce-terminated", GeneratorID: "generator-terminated", State: storage.CapacityIntentTerminated},
	} {
		if err := store.CreateCapacityInstanceIntent(intent); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{store: store, reverseTargetSessions: map[string]map[string]struct{}{
		"target-isolated": {"generator-warm": {}, "generator-current": {}, "generator-other": {}, "generator-terminated": {}},
	}}
	request := compute.CapacityRequest{ExecutionTargetID: "target-isolated", FleetPoolID: pool.ID, TestID: "test-current", ConnectivityMode: storage.ConnectivityReverse, Desired: 2}
	got := server.applyCapacityConnectivityReadiness(request, compute.Observation{Requested: 2, Observed: 3, Ready: 3, Terminal: true})
	if !got.Terminal || got.Ready != 2 {
		t.Fatalf("observation = %+v", got)
	}
	eligible := server.reverseCapacitySessionIDs(request)
	if strings.Join(eligible, ",") != "generator-current,generator-warm" {
		t.Fatalf("eligible sessions = %v", eligible)
	}
}

func TestDirectCapacityReadinessRequiresGeneratorHeartbeat(t *testing.T) {
	server, _ := newTimeslotServer(t)
	server.capacityHeartbeatProbe = func(string) bool { return false }
	request := compute.CapacityRequest{ConnectivityMode: storage.ConnectivityDirect, Desired: 1}
	providerReady := compute.Observation{
		Requested: 1, Observed: 1, Ready: 1, Terminal: true,
		Instances: []compute.Instance{{Address: "10.0.0.8:9090", Healthy: true}},
	}
	got := server.applyCapacityConnectivityReadiness(request, providerReady)
	if got.Ready != 0 || got.Pending != 1 || got.Terminal || !strings.Contains(got.Message, "direct generator heartbeats") {
		t.Fatalf("direct readiness without heartbeat = %+v", got)
	}
}

// runTakeoverCapacitySweep runs the first post-takeover provider sweep pass
// for a fresh leadership term.
func runTakeoverCapacitySweep(server *Server) error {
	server.capacitySweep.begin(1)
	return server.reconcileOrphanedCapacityForTerm(context.Background(), 1)
}

func TestOrphanReconcileHardExpiresTrackedCapacity(t *testing.T) {
	t.Setenv(generatorMaxInstanceLifetimeEnv, "30m")
	if got := generatorMaxInstanceLifetime(); got != 30*time.Minute {
		t.Fatalf("configured max lifetime = %s", got)
	}
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-expired", Name: "expired", Status: "active", ExecutionTargetID: "target-expired", LifecycleMode: storage.FleetLifecycleWarmBurst}
	saveCapacityTestPool(t, store, pool)
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.SaveTest(&storage.TestRecord{ID: "active-run-without-archive", Name: "active", Status: "RUNNING", ProjectID: storage.DefaultProjectID, StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	intent := &storage.CapacityInstanceIntent{ID: "intent-expired", ExecutionTargetID: pool.ExecutionTargetID, FleetPoolID: pool.ID, TestID: "active-run-without-archive", ClientToken: "token", LaunchNonce: "nonce", GeneratorID: "generator-expired", ProviderID: "provider-expired", State: storage.CapacityIntentReady, CleanupDeadline: &expiredAt}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatal(err)
	}
	backend := &fakeCapacityBackend{discovered: []compute.Instance{{ProviderID: intent.ProviderID, State: "running", Healthy: true}}}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}
	if err := runTakeoverCapacitySweep(server); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	drained, deprovisioned := backend.drain, backend.deprovision
	backend.mu.Unlock()
	if drained != 1 || deprovisioned != 1 {
		t.Fatalf("drain=%d deprovision=%d", drained, deprovisioned)
	}
	got, err := store.GetCapacityInstanceIntent(intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != storage.CapacityIntentTerminated || got.ArchiveState != storage.CapacityArchiveExpired {
		t.Fatalf("expired intent = %+v", got)
	}
}

func TestOrphanReconcileAdoptsLostProviderIdentityByDurableLaunchIntent(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-adopt", Name: "adopt", Status: "active", ExecutionTargetID: "target-adopt", LifecycleMode: storage.FleetLifecycleWarmBurst}
	saveCapacityTestPool(t, store, pool)
	intent := &storage.CapacityInstanceIntent{
		ID: "intent-adopt", ExecutionTargetID: pool.ExecutionTargetID, FleetPoolID: pool.ID,
		TestID: "test-adopt", ProvisioningRequestID: "operation-adopt", ClientToken: "token-adopt", LaunchNonce: "nonce-adopt", GeneratorID: "generator-adopt",
	}
	if err := store.SaveTest(&storage.TestRecord{ID: intent.TestID, Name: "adopting", Status: "RUNNING", ProjectID: storage.DefaultProjectID, StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatal(err)
	}
	instance := compute.Instance{
		ProviderID: "provider-adopt", GeneratorID: intent.GeneratorID, State: "running", Healthy: true, IdentityAttested: true,
		Tags: map[string]string{
			"apexload.io/operation-id": intent.ProvisioningRequestID,
			"apexload.io/generator-id": intent.GeneratorID,
			"apexload.io/launch-nonce": intent.LaunchNonce,
		},
	}
	backend := &fakeCapacityBackend{discovered: []compute.Instance{instance}, adopted: map[string]compute.Instance{instance.ProviderID: instance}}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}

	if err := runTakeoverCapacitySweep(server); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCapacityInstanceIntent(intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderID != instance.ProviderID || got.State != storage.CapacityIntentReady {
		t.Fatalf("adopted intent = %+v, want ready provider identity", got)
	}
	backend.mu.Lock()
	deprovisioned, requests := backend.deprovision, append([]compute.AdoptionRequest(nil), backend.adoptRequests...)
	backend.mu.Unlock()
	if deprovisioned != 0 {
		t.Fatal("successfully adopted capacity was reaped")
	}
	if len(requests) != 1 || requests[0].OperationID != intent.ProvisioningRequestID || requests[0].LaunchNonce != intent.LaunchNonce {
		t.Fatalf("adoption requests = %+v, want exact durable launch coordinates", requests)
	}
}

func TestOrphanReconcileReapsUntrackedProviderCapacity(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-untracked", Name: "untracked", Status: "active", ExecutionTargetID: "target-untracked", LifecycleMode: storage.FleetLifecycleWarmBurst}
	saveCapacityTestPool(t, store, pool)
	backend := &fakeCapacityBackend{discovered: []compute.Instance{{
		ProviderID: "provider-untracked", GeneratorID: "generator-untracked", State: "running", Healthy: true,
		Tags: map[string]string{"apexload.io/launch-nonce": "nonce-untracked"},
	}}}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}

	if err := runTakeoverCapacitySweep(server); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	deprovisioned, adopted := backend.deprovision, len(backend.adoptRequests)
	backend.mu.Unlock()
	if deprovisioned != 1 || adopted != 0 {
		t.Fatalf("untracked reconciliation deprovision=%d adopt=%d, want 1/0", deprovisioned, adopted)
	}
}

func TestOrphanReconcileAdoptsLaunchIntentOnlyOnce(t *testing.T) {
	server, genericStore := newTimeslotServer(t)
	store := genericStore.(*storage.SQLiteStore)
	pool := &storage.FleetPool{ID: "pool-duplicate", Name: "duplicate", Status: "active", ExecutionTargetID: "target-duplicate", LifecycleMode: storage.FleetLifecycleWarmBurst}
	saveCapacityTestPool(t, store, pool)
	intent := &storage.CapacityInstanceIntent{
		ID: "intent-duplicate", ExecutionTargetID: pool.ExecutionTargetID, FleetPoolID: pool.ID,
		TestID: "test-duplicate", ProvisioningRequestID: "operation-duplicate", ClientToken: "token-duplicate",
		LaunchNonce: "nonce-duplicate", GeneratorID: "generator-duplicate",
	}
	if err := store.SaveTest(&storage.TestRecord{ID: intent.TestID, Name: "duplicate", Status: "RUNNING", ProjectID: storage.DefaultProjectID, StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatal(err)
	}
	instance := func(providerID string) compute.Instance {
		return compute.Instance{
			ProviderID: providerID, GeneratorID: intent.GeneratorID, State: "running", Healthy: true, IdentityAttested: true,
			Tags: map[string]string{
				"apexload.io/operation-id": intent.ProvisioningRequestID,
				"apexload.io/generator-id": intent.GeneratorID,
				"apexload.io/launch-nonce": intent.LaunchNonce,
			},
		}
	}
	first, duplicate := instance("provider-first"), instance("provider-duplicate")
	backend := &fakeCapacityBackend{
		discovered: []compute.Instance{first, duplicate},
		adopted:    map[string]compute.Instance{first.ProviderID: first, duplicate.ProviderID: duplicate},
	}
	server.capacityBackends = fakeCapacityResolver{backend: backend, target: &storage.ExecutionTarget{ID: pool.ExecutionTargetID, Kind: storage.ExecutionTargetAWSECSFargate}}

	if err := runTakeoverCapacitySweep(server); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCapacityInstanceIntent(intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	adopted, deprovisioned := len(backend.adoptRequests), backend.deprovision
	backend.mu.Unlock()
	if got.ProviderID != first.ProviderID || adopted != 1 || deprovisioned != 1 {
		t.Fatalf("duplicate recovery provider=%q adopt=%d deprovision=%d, want %q/1/1", got.ProviderID, adopted, deprovisioned, first.ProviderID)
	}
}
