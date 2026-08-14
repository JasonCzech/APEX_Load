package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCapacityBackendResolverRequiresExplicitFeatureOptIn(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "resolver.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	connection := &storage.CloudConnection{ID: "cloud-1", Name: "AWS", Provider: storage.CloudProviderAWS, AuthMode: storage.CloudAuthAWSAmbient, AWSRegion: "us-east-1", Status: "active"}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &storage.ExecutionTarget{ID: "target-1", Name: "ECS", Kind: storage.ExecutionTargetAWSECSFargate, CloudConnectionID: connection.ID, ConnectivityMode: storage.ConnectivityDirect, StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerDisabled, AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "cluster", WarmService: "warm"}}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}
	resolver := &capacityBackendResolver{targets: store, clouds: store}
	_, _, err = resolver.BackendForPool(context.Background(), &storage.FleetPool{ExecutionTargetID: target.ID, LifecycleMode: storage.FleetLifecycleWarmBurst})
	if !errors.Is(err, errCapacityBackendUnavailable) || !strings.Contains(err.Error(), "APEXLOAD_PAAS_COMPUTE_ENABLED") {
		t.Fatalf("error = %v, want feature flag refusal", err)
	}
}

func TestCapacityBackendMaintenanceResolutionBypassesMutationAndStatusGates(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "maintenance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	connection := &storage.CloudConnection{ID: "cloud-disabled", Name: "AWS disabled", Provider: storage.CloudProviderAWS, AuthMode: storage.CloudAuthAWSAmbient, AWSRegion: "us-east-1", Status: "disabled"}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &storage.ExecutionTarget{ID: "target-disabled", Name: "ECS disabled", Kind: storage.ExecutionTargetAWSECSFargate, CloudConnectionID: connection.ID, ConnectivityMode: storage.ConnectivityDirect, StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex, Status: "disabled", AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "cluster", BurstTaskDefinition: "burst:1", Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"}}}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}
	resolver := &capacityBackendResolver{enabled: false, targets: store, clouds: store}
	pool := &storage.FleetPool{ExecutionTargetID: target.ID, LifecycleMode: storage.FleetLifecycleWarmBurst}
	if _, _, err := resolver.BackendForPool(context.Background(), pool); !errors.Is(err, errCapacityBackendUnavailable) {
		t.Fatalf("mutation resolution error = %v", err)
	}
	backend, resolved, err := resolver.BackendForPoolMaintenance(context.Background(), pool)
	if err != nil || backend == nil || resolved == nil || resolved.ID != target.ID {
		t.Fatalf("maintenance resolution = (%T, %+v, %v)", backend, resolved, err)
	}
}

func TestAzureCloudConfigurationUsesSovereignAuthorities(t *testing.T) {
	if got := azureCloudConfiguration(storage.AzureCloudUSGovernment).ActiveDirectoryAuthorityHost; got != "https://login.microsoftonline.us/" {
		t.Fatalf("government authority = %q", got)
	}
	if got := azureCloudConfiguration(storage.AzureCloudChina).ActiveDirectoryAuthorityHost; got != "https://login.chinacloudapi.cn/" {
		t.Fatalf("China authority = %q", got)
	}
}

func TestCapacityBackendTargetResolutionAllowsReadOnlyValidationWhenMutationsDisabled(t *testing.T) {
	resolver := &capacityBackendResolver{}
	_, err := resolver.BackendForTarget(context.Background(), &storage.ExecutionTarget{Kind: storage.ExecutionTargetAWSECSFargate, CloudConnectionID: "missing"})
	if err != nil && strings.Contains(err.Error(), "APEXLOAD_PAAS_COMPUTE_ENABLED") {
		t.Fatalf("read-only target resolution was blocked by mutation feature flag: %v", err)
	}
}
