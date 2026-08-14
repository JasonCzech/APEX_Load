package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newComputeConnectionTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "compute.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestExecutionTargetEnrollmentTokenExpiresAndRotatesWithBoundedOverlap(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	target := &ExecutionTarget{ID: "target-token-rotation"}
	oldToken := strings.Repeat("o", MinExecutionTargetEnrollmentTokenBytes)
	newToken := strings.Repeat("n", MinExecutionTargetEnrollmentTokenBytes)
	if err := RotateExecutionTargetEnrollmentToken(target, oldToken, now.Add(48*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if !VerifyExecutionTargetEnrollmentTokenAt(target, oldToken, now.Add(time.Hour)) {
		t.Fatal("current token was rejected before expiry")
	}
	if err := RotateExecutionTargetEnrollmentToken(target, newToken, now.Add(90*24*time.Hour), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !VerifyExecutionTargetEnrollmentTokenAt(target, oldToken, now.Add(3*time.Hour)) {
		t.Fatal("previous token was rejected inside the rotation overlap")
	}
	if VerifyExecutionTargetEnrollmentTokenAt(target, oldToken, now.Add(27*time.Hour)) {
		t.Fatal("previous token remained valid after the rotation overlap")
	}
	if !VerifyExecutionTargetEnrollmentTokenAt(target, newToken, now.Add(27*time.Hour)) {
		t.Fatal("rotated token was rejected before its expiry")
	}
	if VerifyExecutionTargetEnrollmentTokenAt(target, newToken, now.Add(91*24*time.Hour)) {
		t.Fatal("expired enrollment token verified")
	}
}

func TestExecutionTargetEnrollmentTokenIsWriteOnlyAndVerifiable(t *testing.T) {
	target := &ExecutionTarget{ID: "target-token"}
	token := "0123456789abcdef0123456789abcdef"
	if err := SetExecutionTargetEnrollmentToken(target, token); err != nil {
		t.Fatalf("SetExecutionTargetEnrollmentToken: %v", err)
	}
	if target.EnrollmentTokenHash == token || target.EnrollmentTokenHash == "" {
		t.Fatalf("persisted digest = %q", target.EnrollmentTokenHash)
	}
	if !VerifyExecutionTargetEnrollmentToken(target, token) {
		t.Fatal("configured token did not verify")
	}
	if VerifyExecutionTargetEnrollmentToken(target, token+"wrong") {
		t.Fatal("wrong token verified")
	}
	if err := SetExecutionTargetEnrollmentToken(target, "short"); err == nil {
		t.Fatal("short enrollment token was accepted")
	}
}

func TestAssumeRoleCredentialSourceUsesAmbientIdentity(t *testing.T) {
	connection := &CloudConnection{AuthMode: CloudAuthAWSAssumeRole}
	if got := connection.CredentialSource(); got != "workload_identity" {
		t.Fatalf("CredentialSource() = %q, want workload_identity", got)
	}
}

func TestExecutionTargetRejectsBurstFargateWithoutNetworkPlacement(t *testing.T) {
	target := &ExecutionTarget{
		ID: "target-missing-network", Name: "missing network", Kind: ExecutionTargetAWSECSFargate,
		CloudConnectionID: "connection", ConnectivityMode: ConnectivityDirect,
		StandingOwner: CapacityOwnerApex, BurstOwner: CapacityOwnerApex,
		AWSECSFargate: &AWSECSFargateTargetSpec{Cluster: "cluster", BurstTaskDefinition: "generator:1"},
	}
	if err := target.Validate(); err == nil {
		t.Fatal("burst Fargate target without subnet/security-group placement was accepted")
	}
}

func TestCloudConnectionRejectsAWSPartitionRegionMismatch(t *testing.T) {
	tests := []CloudConnection{
		{ID: "cn-commercial", Name: "bad", Provider: CloudProviderAWS, AuthMode: CloudAuthAWSAmbient, AWSPartition: AWSPartitionCommercial, AWSRegion: "cn-north-1"},
		{ID: "gov-commercial", Name: "bad", Provider: CloudProviderAWS, AuthMode: CloudAuthAWSAmbient, AWSPartition: AWSPartitionCommercial, AWSRegion: "us-gov-west-1"},
		{ID: "cn-region", Name: "bad", Provider: CloudProviderAWS, AuthMode: CloudAuthAWSAmbient, AWSPartition: AWSPartitionChina, AWSRegion: "us-east-1"},
		{ID: "gov-region", Name: "bad", Provider: CloudProviderAWS, AuthMode: CloudAuthAWSAmbient, AWSPartition: AWSPartitionGovCloud, AWSRegion: "us-east-1"},
		{ID: "bad-account", Name: "bad", Provider: CloudProviderAWS, AuthMode: CloudAuthAWSAmbient, AWSPartition: AWSPartitionCommercial, AWSRegion: "us-east-1", AWSAccountID: "123"},
		{ID: "bad-role-partition", Name: "bad", Provider: CloudProviderAWS, AuthMode: CloudAuthAWSAssumeRole, AWSPartition: AWSPartitionGovCloud, AWSRegion: "us-gov-west-1", AWSRoleARN: "arn:aws:iam::123456789012:role/load"},
	}
	for _, connection := range tests {
		connection := connection
		t.Run(connection.ID, func(t *testing.T) {
			if err := connection.Validate(); err == nil {
				t.Fatalf("Validate(%+v) succeeded", connection)
			}
		})
	}
	valid := &CloudConnection{ID: "valid-cn", Name: "valid", Provider: CloudProviderAWS, AuthMode: CloudAuthAWSAmbient, AWSPartition: AWSPartitionChina, AWSRegion: "cn-north-1", AWSAccountID: "123456789012"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid sovereign connection: %v", err)
	}
}

func TestCloudConnectionAndExecutionTargetReferences(t *testing.T) {
	store := newComputeConnectionTestStore(t)
	connection := &CloudConnection{
		ID: "azure-connection", Name: "Azure production", Provider: CloudProviderAzure,
		AuthMode: CloudAuthAzureIdentity, AzureSubscriptionID: "subscription-1",
	}
	if err := store.CreateCloudConnection(connection); err != nil {
		t.Fatal(err)
	}
	target := &ExecutionTarget{
		ID: "azure-target", Name: "Container Apps generators", Kind: ExecutionTargetAzureContainerApps,
		CloudConnectionID: connection.ID, ConnectivityMode: ConnectivityReverse,
		StandingOwner: CapacityOwnerApex, BurstOwner: CapacityOwnerDisabled,
		ArchiveMode: ArchiveModeControllerPull,
		AzureContainerApps: &AzureContainerAppsTargetSpec{
			ResourceGroup: "rg-load", EnvironmentName: "cae-load", WarmContainerApp: "generator",
			ExpectedPrincipalID: "principal-1",
		},
	}
	token := strings.Repeat("p", MinExecutionTargetEnrollmentTokenBytes)
	if err := SetExecutionTargetEnrollmentToken(target, token); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateExecutionTarget(target); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.GetExecutionTarget(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CloudConnectionID != connection.ID || loaded.AzureContainerApps == nil || loaded.AzureContainerApps.WarmContainerApp != "generator" || loaded.EnrollmentTokenExpiresAt == nil || !VerifyExecutionTargetEnrollmentToken(loaded, token) {
		t.Fatalf("loaded target = %+v", loaded)
	}
	if deleted, err := store.DeleteCloudConnectionIfUnreferenced(connection.ID); err != nil || deleted {
		t.Fatalf("delete referenced cloud connection = (%v, %v), want refusal", deleted, err)
	}

	providerEdit := *connection
	providerEdit.Provider = CloudProviderAWS
	providerEdit.AuthMode = CloudAuthAWSAmbient
	providerEdit.AWSRegion = "us-east-1"
	if _, err := store.UpdateCloudConnectionIfUnchanged(&providerEdit, connection.UpdatedAt); !errors.Is(err, ErrCloudConnectionProviderImmutable) {
		t.Fatalf("provider edit error = %v, want ErrCloudConnectionProviderImmutable", err)
	}
	scopeEdit := *connection
	scopeEdit.AzureSubscriptionID = "subscription-2"
	if _, err := store.UpdateCloudConnectionIfUnchanged(&scopeEdit, connection.UpdatedAt); !errors.Is(err, ErrCloudConnectionScopeImmutable) {
		t.Fatalf("scope edit error = %v, want ErrCloudConnectionScopeImmutable", err)
	}

	pool := &FleetPool{ID: "pool-1", Name: "PaaS pool", CloudProvider: CloudProviderAzure, ExecutionTargetID: target.ID}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteExecutionTargetIfUnreferenced(target.ID); err != nil || deleted {
		t.Fatalf("delete referenced target = (%v, %v), want refusal", deleted, err)
	}

	changedKind := *target
	changedKind.Kind = ExecutionTargetStatic
	changedKind.CloudConnectionID = ""
	changedKind.AzureContainerApps = nil
	changedKind.Static = &StaticExecutionTargetSpec{Targets: []string{"generator:9090"}}
	if _, err := store.UpdateExecutionTargetIfUnchanged(&changedKind, target.UpdatedAt); !errors.Is(err, ErrExecutionTargetReferenced) {
		t.Fatalf("target kind edit error = %v, want ErrExecutionTargetReferenced", err)
	}
	changedPlacement := *target
	changedSpec := *target.AzureContainerApps
	changedSpec.WarmContainerApp = "different-generator"
	changedPlacement.AzureContainerApps = &changedSpec
	if _, err := store.UpdateExecutionTargetIfUnchanged(&changedPlacement, target.UpdatedAt); !errors.Is(err, ErrExecutionTargetReferenced) {
		t.Fatalf("target placement edit error = %v, want ErrExecutionTargetReferenced", err)
	}
}

func TestKubernetesExecutionTargetBackfillIsIdempotent(t *testing.T) {
	store := newComputeConnectionTestStore(t)
	cluster := &ClusterConnection{
		ID: "cluster-1", Name: "Existing cluster", Kind: ClusterKindInCluster,
		Namespace: "apexload", TLSMode: ClusterTLSSystemRoots, AuthMode: ClusterAuthInCluster,
	}
	if err := store.CreateClusterConnection(cluster); err != nil {
		t.Fatal(err)
	}
	pool := &FleetPool{ID: "pool-legacy", Name: "Legacy pool", CloudProvider: DeploymentProviderKubernetes, ClusterID: cluster.ID}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}

	if err := store.backfillKubernetesExecutionTargets(); err != nil {
		t.Fatal(err)
	}
	if err := store.backfillKubernetesExecutionTargets(); err != nil {
		t.Fatal(err)
	}
	targets, err := store.ListExecutionTargets()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].ClusterConnectionID != cluster.ID {
		t.Fatalf("backfilled targets = %+v, want one Kubernetes projection", targets)
	}
	loadedPool, err := store.GetFleetPool(pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedPool.ExecutionTargetID != targets[0].ID || loadedPool.ClusterID != cluster.ID {
		t.Fatalf("backfilled pool = %+v, want normalized target plus legacy projection", loadedPool)
	}
}
