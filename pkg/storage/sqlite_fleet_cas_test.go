package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestProvisioningAttemptCASFencesStaleCompletion(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveFleetPool(&FleetPool{ID: "pool-a", Name: "pool-a", CloudProvider: "kubernetes", Status: "active"}); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
	req := &ProvisioningRequest{
		ID: "provisioning-cas", Revision: 1, AttemptToken: "attempt-a", FleetPoolID: "pool-a",
		ProjectID: DefaultProjectID, Status: "provisioning", DesiredCount: 3, CreatedAt: now, UpdatedAt: now,
	}
	created, err := store.CreateProvisioningRequestIfAbsent(req)
	if err != nil || !created {
		t.Fatalf("CreateProvisioningRequestIfAbsent = %v, %v", created, err)
	}
	cancelled := *req
	cancelled.Revision = 2
	cancelled.AttemptToken = "cancel-fence"
	cancelled.Status = "cancelled"
	cancelled.CompletedAt = &now
	changed, err := store.TransitionProvisioningRequest(&cancelled, 1, "attempt-a")
	if err != nil || !changed {
		t.Fatalf("cancel transition = %v, %v", changed, err)
	}
	stale := *req
	stale.Revision = 2
	stale.Status = "success"
	stale.CompletedAt = &now
	changed, err = store.TransitionProvisioningRequest(&stale, 1, "attempt-a")
	if err != nil || changed {
		t.Fatalf("stale completion = %v, %v; want false, nil", changed, err)
	}
	loaded, err := store.GetProvisioningRequest(req.ID)
	if err != nil {
		t.Fatalf("GetProvisioningRequest: %v", err)
	}
	if loaded.Status != "cancelled" || loaded.Revision != 2 || loaded.AttemptToken != "cancel-fence" {
		t.Fatalf("durable request = %#v, want fenced cancellation", loaded)
	}
}

func TestFleetPoolDeleteRejectsActiveProvisioning(t *testing.T) {
	store := openTimeslotStore(t)
	pool := &FleetPool{ID: "pool-active", Name: "active", CloudProvider: "kubernetes", Status: "active"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	req := &ProvisioningRequest{ID: "request-active", Revision: 1, AttemptToken: "attempt", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 4}
	if created, err := store.CreateProvisioningRequestIfAbsent(req); err != nil || !created {
		t.Fatalf("CreateProvisioningRequestIfAbsent = %v, %v", created, err)
	}
	if err := store.DeleteFleetPool(pool.ID); !errors.Is(err, ErrFleetPoolHasActiveProvisioning) {
		t.Fatalf("DeleteFleetPool active error = %v, want ErrFleetPoolHasActiveProvisioning", err)
	}
	if _, err := store.GetFleetPool(pool.ID); err != nil {
		t.Fatalf("active request deletion removed pool: %v", err)
	}

	done := time.Now().UTC()
	completed := *req
	completed.Revision++
	completed.Status = "success"
	completed.CompletedAt = &done
	if changed, err := store.TransitionProvisioningRequest(&completed, req.Revision, req.AttemptToken); err != nil || !changed {
		t.Fatalf("complete provisioning = %v, %v", changed, err)
	}
	if err := store.DeleteFleetPool(pool.ID); err != nil {
		t.Fatalf("DeleteFleetPool after completion: %v", err)
	}
}

func TestFleetPoolDeleteRejectsLiveCapacityIntent(t *testing.T) {
	store := openTimeslotStore(t)
	pool := &FleetPool{ID: "pool-live-capacity", Name: "live capacity", CloudProvider: "aws", Status: "active"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	intent := &CapacityInstanceIntent{
		ID: "intent-live-capacity", ExecutionTargetID: "target-live-capacity", FleetPoolID: pool.ID,
		ClientToken: "token-live-capacity", LaunchNonce: "nonce-live-capacity", GeneratorID: "generator-live-capacity",
	}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFleetPool(pool.ID); !errors.Is(err, ErrFleetPoolHasLiveCapacity) {
		t.Fatalf("DeleteFleetPool live-capacity error = %v, want ErrFleetPoolHasLiveCapacity", err)
	}

	expected := intent.UpdatedAt
	intent.State = CapacityIntentTerminated
	if updated, err := store.UpdateCapacityInstanceIntentIfUnchanged(intent, expected); err != nil || !updated {
		t.Fatalf("terminate capacity intent = %v, %v", updated, err)
	}
	if err := store.DeleteFleetPool(pool.ID); err != nil {
		t.Fatalf("DeleteFleetPool after capacity termination: %v", err)
	}
}

func TestFleetPoolDeleteRejectsConfiguredPaaSWarmCapacity(t *testing.T) {
	store := openTimeslotStore(t)
	connection := &CloudConnection{
		ID: "connection-warm-capacity", Name: "warm capacity", Provider: CloudProviderAWS,
		AuthMode: CloudAuthAWSAmbient, AWSRegion: "us-east-1", Status: "active",
	}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &ExecutionTarget{
		ID: "target-warm-capacity", Name: "warm capacity", Kind: ExecutionTargetAWSECSFargate,
		CloudConnectionID: connection.ID, ConnectivityMode: ConnectivityDirect,
		StandingOwner: CapacityOwnerApex, BurstOwner: CapacityOwnerDisabled, ArchiveMode: ArchiveModeControllerPull,
		AWSECSFargate: &AWSECSFargateTargetSpec{Cluster: "cluster", WarmService: "service"}, Status: "active",
	}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}
	pool := &FleetPool{
		ID: "pool-warm-capacity", Name: "warm capacity", CloudProvider: DeploymentProviderAWSECSFargate,
		Status: "active", ExecutionTargetID: target.ID, DesiredSize: 2, WarmFloor: 1,
	}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFleetPool(pool.ID); !errors.Is(err, ErrFleetPoolHasLiveCapacity) {
		t.Fatalf("DeleteFleetPool standing-capacity error = %v, want ErrFleetPoolHasLiveCapacity", err)
	}

	pool.DesiredSize, pool.WarmFloor = 0, 0
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	remoteSuccess := &ProvisioningRequest{
		ID: "request-warm-capacity", FleetPoolID: pool.ID, ExecutionTargetID: target.ID,
		IdempotencyKey: "request-warm-capacity", Status: "success", DesiredCount: 2,
	}
	if err := store.SaveProvisioningRequest(remoteSuccess); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFleetPool(pool.ID); !errors.Is(err, ErrFleetPoolHasLiveCapacity) {
		t.Fatalf("DeleteFleetPool durable-success error = %v, want ErrFleetPoolHasLiveCapacity", err)
	}
	time.Sleep(2 * time.Millisecond)
	confirmedZero := &ProvisioningRequest{
		ID: "request-warm-capacity-zero", FleetPoolID: pool.ID, ExecutionTargetID: target.ID,
		IdempotencyKey: "request-warm-capacity-zero", Status: "success", DesiredCount: 0,
	}
	if err := store.SaveProvisioningRequest(confirmedZero); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFleetPool(pool.ID); err != nil {
		t.Fatalf("DeleteFleetPool after scale-to-zero configuration: %v", err)
	}
}

func TestFleetPoolTargetCannotChangeUntilPaaSCapacityIsReleased(t *testing.T) {
	store := openTimeslotStore(t)
	connection := &CloudConnection{
		ID: "connection-target-change", Name: "target change", Provider: CloudProviderAWS,
		AuthMode: CloudAuthAWSAmbient, AWSRegion: "us-east-1", Status: "active",
	}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &ExecutionTarget{
		ID: "target-change", Name: "target change", Kind: ExecutionTargetAWSECSFargate,
		CloudConnectionID: connection.ID, ConnectivityMode: ConnectivityDirect,
		StandingOwner: CapacityOwnerApex, BurstOwner: CapacityOwnerDisabled, ArchiveMode: ArchiveModeControllerPull,
		AWSECSFargate: &AWSECSFargateTargetSpec{Cluster: "cluster", WarmService: "service"}, Status: "active",
	}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}
	pool := &FleetPool{
		ID: "pool-target-change", Name: "target change", CloudProvider: DeploymentProviderAWSECSFargate,
		Status: "active", ExecutionTargetID: target.ID, DesiredSize: 1,
	}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetFleetPool(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.ExecutionTargetID = ""
	if updated, err := store.UpdateFleetPoolIfUnchanged(loaded, nil, false, loaded.UpdatedAt); updated || !errors.Is(err, ErrFleetPoolHasLiveCapacity) {
		t.Fatalf("change live target = (%v, %v), want false, ErrFleetPoolHasLiveCapacity", updated, err)
	}

	loaded, err = store.GetFleetPool(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.DesiredSize = 0
	if updated, err := store.UpdateFleetPoolIfUnchanged(loaded, nil, false, loaded.UpdatedAt); err != nil || !updated {
		t.Fatalf("scale configuration to zero = (%v, %v)", updated, err)
	}
	loaded, err = store.GetFleetPool(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.ExecutionTargetID = ""
	if updated, err := store.UpdateFleetPoolIfUnchanged(loaded, nil, false, loaded.UpdatedAt); err != nil || !updated {
		t.Fatalf("change released target = (%v, %v)", updated, err)
	}
}

func TestFleetPoolDeleteRequiresConfirmedZeroAfterStandingCleanupFailure(t *testing.T) {
	store := openTimeslotStore(t)
	connection := &CloudConnection{
		ID: "connection-cleanup-failure", Name: "cleanup failure", Provider: CloudProviderAWS,
		AuthMode: CloudAuthAWSAmbient, AWSRegion: "us-east-1", Status: "active",
	}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &ExecutionTarget{
		ID: "target-cleanup-failure", Name: "cleanup failure", Kind: ExecutionTargetAWSECSFargate,
		CloudConnectionID: connection.ID, ConnectivityMode: ConnectivityDirect,
		StandingOwner: CapacityOwnerApex, BurstOwner: CapacityOwnerDisabled, ArchiveMode: ArchiveModeControllerPull,
		AWSECSFargate: &AWSECSFargateTargetSpec{Cluster: "cluster", WarmService: "service"}, Status: "active",
	}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}
	pool := &FleetPool{
		ID: "pool-cleanup-failure", Name: "cleanup failure", CloudProvider: DeploymentProviderAWSECSFargate,
		Status: "active", ExecutionTargetID: target.ID,
	}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	failed := &ProvisioningRequest{
		ID: "request-cleanup-failure", FleetPoolID: pool.ID, ExecutionTargetID: target.ID,
		IdempotencyKey: "request-cleanup-failure", Status: "failed", DesiredCount: 2,
		LastError: "provider readiness failed; provider cleanup failed: timeout",
	}
	if err := store.SaveProvisioningRequest(failed); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFleetPool(pool.ID); !errors.Is(err, ErrFleetPoolHasLiveCapacity) {
		t.Fatalf("DeleteFleetPool unresolved-cleanup error = %v, want ErrFleetPoolHasLiveCapacity", err)
	}

	time.Sleep(2 * time.Millisecond)
	cleared := &ProvisioningRequest{
		ID: "request-confirmed-zero", FleetPoolID: pool.ID, ExecutionTargetID: target.ID,
		IdempotencyKey: "request-confirmed-zero", Status: "success", DesiredCount: 0,
	}
	if err := store.SaveProvisioningRequest(cleared); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFleetPool(pool.ID); err != nil {
		t.Fatalf("DeleteFleetPool after confirmed zero: %v", err)
	}
}

func TestProvisioningCreateAndFleetPoolDeleteAreAtomic(t *testing.T) {
	store := openTimeslotStore(t)
	pool := &FleetPool{ID: "pool-create-delete", Name: "race", CloudProvider: "kubernetes", Status: "active"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	req := &ProvisioningRequest{ID: "request-create-delete", Revision: 1, AttemptToken: "attempt", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 2}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var created bool
	var createErr, deleteErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		created, createErr = store.CreateProvisioningRequestIfAbsent(req)
	}()
	go func() {
		defer wg.Done()
		<-start
		deleteErr = store.DeleteFleetPool(pool.ID)
	}()
	close(start)
	wg.Wait()

	if created {
		if createErr != nil || !errors.Is(deleteErr, ErrFleetPoolHasActiveProvisioning) {
			t.Fatalf("create won: createErr=%v deleteErr=%v", createErr, deleteErr)
		}
		if _, err := store.GetFleetPool(pool.ID); err != nil {
			t.Fatalf("created request references deleted pool: %v", err)
		}
		return
	}
	if !errors.Is(createErr, sql.ErrNoRows) || deleteErr != nil {
		t.Fatalf("delete won: createErr=%v deleteErr=%v", createErr, deleteErr)
	}
	if _, err := store.GetProvisioningRequest(req.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete-first race left request: %v", err)
	}
}

func TestProvisioningCreateRejectsInactiveFleetPool(t *testing.T) {
	store := openTimeslotStore(t)
	pool := &FleetPool{ID: "pool-suspended", Name: "suspended", CloudProvider: "kubernetes", Status: "suspended"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateProvisioningRequestIfAbsent(&ProvisioningRequest{
		ID: "request-suspended", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 1,
	})
	if created || !errors.Is(err, ErrFleetPoolNotActive) {
		t.Fatalf("inactive provisioning create = %v, %v; want false, ErrFleetPoolNotActive", created, err)
	}
}

func TestProvisioningCreateRejectsStaleExecutionTarget(t *testing.T) {
	store := openTimeslotStore(t)
	for _, id := range []string{"target-current", "target-stale"} {
		if err := store.CreateExecutionTarget(&ExecutionTarget{
			ID: id, Name: id, Kind: ExecutionTargetStatic, ConnectivityMode: ConnectivityDirect,
			StandingOwner: CapacityOwnerApex, BurstOwner: CapacityOwnerDisabled,
			ArchiveMode: ArchiveModeControllerPull, Static: &StaticExecutionTargetSpec{Targets: []string{"generator:9090"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	pool := &FleetPool{
		ID: "pool-placement-changed", Name: "placement changed", CloudProvider: "aws",
		Status: "active", ExecutionTargetID: "target-current",
	}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateProvisioningRequestIfAbsent(&ProvisioningRequest{
		ID: "request-placement-changed", FleetPoolID: pool.ID, ExecutionTargetID: "target-stale",
		Status: "provisioning", DesiredCount: 1,
	})
	if created || !errors.Is(err, ErrFleetPoolPlacementChanged) {
		t.Fatalf("stale placement provisioning create = %v, %v; want false, ErrFleetPoolPlacementChanged", created, err)
	}
}

func TestProvisioningCreateSerializesPoolMutations(t *testing.T) {
	store := openTimeslotStore(t)
	pool := &FleetPool{ID: "pool-serialized", Name: "serialized", CloudProvider: "aws", Status: "active"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	first := &ProvisioningRequest{ID: "request-first", FleetPoolID: pool.ID, Status: "waiting_ready", DesiredCount: 2}
	if created, err := store.CreateProvisioningRequestIfAbsent(first); err != nil || !created {
		t.Fatalf("first create = (%v, %v)", created, err)
	}
	second := &ProvisioningRequest{ID: "request-second", FleetPoolID: pool.ID, Status: "provisioning", DesiredCount: 3}
	if created, err := store.CreateProvisioningRequestIfAbsent(second); created || !errors.Is(err, ErrFleetPoolHasActiveProvisioning) {
		t.Fatalf("second create = (%v, %v), want serialized conflict", created, err)
	}
}
