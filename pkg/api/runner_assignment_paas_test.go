package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestResolvePoolAssignmentAllowsEmptyDynamicPaaSPool(t *testing.T) {
	store := newCapacityOperationStore(t)
	connection := &storage.CloudConnection{ID: "cloud-empty", Name: "AWS", Provider: storage.CloudProviderAWS, AuthMode: storage.CloudAuthAWSAmbient, AWSRegion: "us-east-1"}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &storage.ExecutionTarget{ID: "target-empty", Name: "ECS", Kind: storage.ExecutionTargetAWSECSFargate, CloudConnectionID: connection.ID, ConnectivityMode: storage.ConnectivityDirect, StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex, AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "cluster", BurstTaskDefinition: "burst:1", Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"}}}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}
	pool := &storage.FleetPool{ID: "pool-empty", Name: "Empty burst pool", Status: "active", ExecutionTargetID: target.ID}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	runner := &TestRunner{fleetStore: store}
	got, err := runner.resolvePoolAssignmentLocked(&GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto, PoolID: pool.ID})
	if err != nil {
		t.Fatalf("resolvePoolAssignmentLocked: %v", err)
	}
	if got.Mode != generatorAssignmentModeAuto || got.PoolID != pool.ID || len(got.SelectedTargets) != 0 {
		t.Fatalf("assignment = %+v", got)
	}
}
