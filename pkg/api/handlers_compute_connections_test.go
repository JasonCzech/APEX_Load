package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestExecutionTargetIDForCreate(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		want      string
		wantErr   bool
	}{
		{name: "stable manifest id", requested: "apex-prod:aca_generators.v1", want: "apex-prod:aca_generators.v1"},
		{name: "path separator refused", requested: "target/escape", wantErr: true},
		{name: "whitespace refused after trim leaves spaces", requested: "target name", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := executionTargetIDForCreate(tt.requested)
			if (err != nil) != tt.wantErr {
				t.Fatalf("executionTargetIDForCreate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("executionTargetIDForCreate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExecutionTargetIDForCreateGeneratesDefault(t *testing.T) {
	got, err := executionTargetIDForCreate("")
	if err != nil {
		t.Fatal(err)
	}
	if !executionTargetIDPattern.MatchString(got) {
		t.Fatalf("generated id %q is invalid", got)
	}
}

func TestCloudCredentialValidationRequiresProviderSpecificSecret(t *testing.T) {
	tests := []struct {
		name       string
		connection *storage.CloudConnection
		want       string
	}{
		{
			name: "azure rejects aws secret envelope",
			connection: &storage.CloudConnection{
				ID: "azure", Name: "Azure", Provider: storage.CloudProviderAzure,
				AuthMode: storage.CloudAuthAzureServicePrincipal, AzureSubscriptionID: "sub",
				AzureTenantID: "tenant", AzureClientID: "client", AWSSecretKeyCiphertext: "cipher", AWSSecretKeyWrappedKey: "wrapped",
			},
			want: "client secret",
		},
		{
			name: "aws rejects azure secret envelope",
			connection: &storage.CloudConnection{
				ID: "aws", Name: "AWS", Provider: storage.CloudProviderAWS,
				AuthMode: storage.CloudAuthAWSStaticKeys, AWSRegion: "us-east-1", AWSAccessKeyID: "access",
				ClientSecretCiphertext: "cipher", ClientSecretWrappedKey: "wrapped",
			},
			want: "secret access key",
		},
		{
			name: "assume role rejects ambiguous credential env",
			connection: &storage.CloudConnection{
				ID: "assume", Name: "Assume", Provider: storage.CloudProviderAWS,
				AuthMode: storage.CloudAuthAWSAssumeRole, AWSRegion: "us-east-1", AWSRoleARN: "arn:aws:iam::123456789012:role/load", CredentialEnvVar: "AWS_EXTERNAL_ID",
			},
			want: "not supported for assume-role",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCloudConnectionCredential(tt.connection)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestAzureContainerAppsRequiresReverseConnectivity(t *testing.T) {
	target := &storage.ExecutionTarget{
		ID: "azure-target", Name: "Azure", Kind: storage.ExecutionTargetAzureContainerApps,
		CloudConnectionID: "cloud", ConnectivityMode: storage.ConnectivityDirect,
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", EnvironmentName: "env", WarmContainerApp: "warm"},
	}
	if err := target.Validate(); err == nil || !strings.Contains(err.Error(), "reverse connectivity") {
		t.Fatalf("Validate error = %v, want reverse-connectivity refusal", err)
	}
}

func TestExecutionTargetRejectsUnimplementedObjectDirectArchive(t *testing.T) {
	target := &storage.ExecutionTarget{
		ID: "ecs-target", Name: "ECS", Kind: storage.ExecutionTargetAWSECSFargate,
		CloudConnectionID: "cloud", ConnectivityMode: storage.ConnectivityDirect,
		ArchiveMode:   storage.ArchiveModeObjectDirect,
		AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "cluster", WarmService: "warm"},
	}
	if err := target.Validate(); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("Validate error = %v, want object-direct fail-closed error", err)
	}
}
