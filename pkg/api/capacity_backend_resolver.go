package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	awsconfig "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/jasonczech/apexload/pkg/compute"
	computeaws "github.com/jasonczech/apexload/pkg/compute/aws"
	computeazure "github.com/jasonczech/apexload/pkg/compute/azure"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errCapacityBackendUnavailable = errors.New("the execution target capacity backend is unavailable")

type capacityBackendPoolResolver interface {
	BackendForPool(context.Context, *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error)
}

type capacityBackendTargetResolver interface {
	BackendForTarget(context.Context, *storage.ExecutionTarget) (compute.Backend, error)
}

type capacityBackendMaintenanceResolver interface {
	BackendForPoolMaintenance(context.Context, *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error)
}

type capacityBackendResolver struct {
	enabled      bool
	targets      storage.ExecutionTargetStore
	clouds       storage.CloudConnectionStore
	controllerID string
	server       *Server
}

func (r *capacityBackendResolver) BackendForPool(ctx context.Context, pool *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error) {
	return r.backendForPool(ctx, pool, false)
}

// BackendForPoolMaintenance resolves credentials even after an administrator
// disables a feature, target, or connection. Those gates must stop new cloud
// mutations, but they must never prevent cancellation and orphan cleanup of
// resources that are already accruing cost.
func (r *capacityBackendResolver) BackendForPoolMaintenance(ctx context.Context, pool *storage.FleetPool) (compute.Backend, *storage.ExecutionTarget, error) {
	return r.backendForPool(ctx, pool, true)
}

func (r *capacityBackendResolver) backendForPool(ctx context.Context, pool *storage.FleetPool, maintenance bool) (compute.Backend, *storage.ExecutionTarget, error) {
	if r == nil || pool == nil || strings.TrimSpace(pool.ExecutionTargetID) == "" {
		return nil, nil, errCapacityBackendUnavailable
	}
	if r.targets == nil {
		return nil, nil, fmt.Errorf("%w: execution targets are unsupported", errCapacityBackendUnavailable)
	}
	target, err := r.targets.GetExecutionTarget(pool.ExecutionTargetID)
	if err != nil || target == nil {
		return nil, nil, fmt.Errorf("%w: target %q could not be loaded", errCapacityBackendUnavailable, pool.ExecutionTargetID)
	}
	if !maintenance && target.StandingOwner == storage.CapacityOwnerExternal && (pool.WarmFloor > 0 || pool.LifecycleMode == "" || pool.LifecycleMode == storage.FleetLifecycleStanding) {
		return nil, target, fmt.Errorf("%w: standing capacity on target %q is owned by an external autoscaler", compute.ErrOwnershipConflict, target.Name)
	}
	if !maintenance && !r.enabled {
		return nil, target, fmt.Errorf("%w: PaaS provider mutations require APEXLOAD_PAAS_COMPUTE_ENABLED=true", errCapacityBackendUnavailable)
	}
	if !maintenance && target.Status != "active" {
		return nil, target, fmt.Errorf("%w: execution target %q is %s", errCapacityBackendUnavailable, target.Name, target.Status)
	}
	backend, err := r.backendForTarget(ctx, target, maintenance)
	return backend, target, err
}

func (r *capacityBackendResolver) BackendForTarget(ctx context.Context, target *storage.ExecutionTarget) (compute.Backend, error) {
	return r.backendForTarget(ctx, target, false)
}

func (r *capacityBackendResolver) backendForTarget(ctx context.Context, target *storage.ExecutionTarget, maintenance bool) (compute.Backend, error) {
	if r == nil || target == nil {
		return nil, errCapacityBackendUnavailable
	}
	switch target.Kind {
	case storage.ExecutionTargetAzureContainerApps:
		connection, err := r.cloudConnection(target.CloudConnectionID, storage.CloudProviderAzure, maintenance)
		if err != nil {
			return nil, err
		}
		credential, err := r.azureCredential(ctx, connection)
		if err != nil {
			return nil, err
		}
		endpoint := map[string]string{
			storage.AzureCloudUSGovernment: "https://management.usgovcloudapi.net",
			storage.AzureCloudChina:        "https://management.chinacloudapi.cn",
		}[connection.AzureCloud]
		return &computeazure.Backend{
			API:    &computeazure.RESTClient{Endpoint: endpoint, Credential: credential},
			Target: target, SubscriptionID: connection.AzureSubscriptionID, ControllerID: r.controllerID,
		}, nil
	case storage.ExecutionTargetAzureContainerInstances:
		connection, err := r.cloudConnection(target.CloudConnectionID, storage.CloudProviderAzure, maintenance)
		if err != nil {
			return nil, err
		}
		credential, err := r.azureCredential(ctx, connection)
		if err != nil {
			return nil, err
		}
		endpoint := map[string]string{
			storage.AzureCloudUSGovernment: "https://management.usgovcloudapi.net",
			storage.AzureCloudChina:        "https://management.chinacloudapi.cn",
		}[connection.AzureCloud]
		return &computeazure.InstancesBackend{
			API:    &computeazure.RESTClient{Endpoint: endpoint, Credential: credential},
			Target: target, SubscriptionID: connection.AzureSubscriptionID, ControllerID: r.controllerID,
		}, nil
	case storage.ExecutionTargetAWSECSFargate:
		connection, err := r.cloudConnection(target.CloudConnectionID, storage.CloudProviderAWS, maintenance)
		if err != nil {
			return nil, err
		}
		provider, err := r.awsCredentials(ctx, connection)
		if err != nil {
			return nil, err
		}
		return &computeaws.Backend{
			API:            &computeaws.JSONClient{Region: connection.AWSRegion, Credentials: provider},
			AutoscalingAPI: &computeaws.ApplicationAutoscalingJSONClient{Region: connection.AWSRegion, Credentials: provider},
			Target:         target, ControllerID: r.controllerID, ContainerName: "generator",
		}, nil
	default:
		return nil, fmt.Errorf("%w: target kind %q uses the legacy scaler adapter", errCapacityBackendUnavailable, target.Kind)
	}
}

func (r *capacityBackendResolver) cloudConnection(id, provider string, allowInactive bool) (*storage.CloudConnection, error) {
	if r.clouds == nil {
		return nil, fmt.Errorf("%w: cloud connections are unsupported", errCapacityBackendUnavailable)
	}
	connection, err := r.clouds.GetCloudConnection(id)
	if err != nil || connection == nil {
		return nil, fmt.Errorf("%w: cloud connection %q could not be loaded", errCapacityBackendUnavailable, id)
	}
	if connection.Provider != provider {
		return nil, fmt.Errorf("%w: target requires %s but connection is %s", errCapacityBackendUnavailable, provider, connection.Provider)
	}
	if !allowInactive && connection.Status != "active" {
		return nil, fmt.Errorf("%w: cloud connection %q is %s", errCapacityBackendUnavailable, connection.Name, connection.Status)
	}
	return connection, nil
}

func (r *capacityBackendResolver) cloudSecret(ctx context.Context, connection *storage.CloudConnection, kind cloudCredentialKind, ciphertext, wrapped string) (string, error) {
	if connection.CredentialEnvVar != "" {
		value := os.Getenv(connection.CredentialEnvVar)
		if value == "" {
			return "", fmt.Errorf("credential environment variable %s is empty", connection.CredentialEnvVar)
		}
		return value, nil
	}
	if r.server == nil {
		return "", errClusterCredentialEncryptionUnavailable
	}
	return openCredentialEnvelope(ctx, r.server.clusterSecretWrapper, cloudCredentialAAD(connection.ID, kind), "cloud credential", ciphertext, wrapped)
}

func (r *capacityBackendResolver) azureCredential(ctx context.Context, connection *storage.CloudConnection) (azcore.TokenCredential, error) {
	clientOptions := azcore.ClientOptions{Cloud: azureCloudConfiguration(connection.AzureCloud)}
	switch connection.AuthMode {
	case storage.CloudAuthAzureIdentity:
		return azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{ClientOptions: clientOptions})
	case storage.CloudAuthAzureServicePrincipal:
		secret, err := r.cloudSecret(ctx, connection, cloudCredentialAzureClientSecret, connection.ClientSecretCiphertext, connection.ClientSecretWrappedKey)
		if err != nil {
			return nil, err
		}
		return azidentity.NewClientSecretCredential(connection.AzureTenantID, connection.AzureClientID, secret, &azidentity.ClientSecretCredentialOptions{ClientOptions: clientOptions})
	default:
		return nil, fmt.Errorf("unsupported Azure auth mode %q", connection.AuthMode)
	}
}

func azureCloudConfiguration(name string) cloud.Configuration {
	switch name {
	case storage.AzureCloudUSGovernment:
		return cloud.AzureGovernment
	case storage.AzureCloudChina:
		return cloud.AzureChina
	default:
		return cloud.AzurePublic
	}
}

func (r *capacityBackendResolver) awsCredentials(ctx context.Context, connection *storage.CloudConnection) (awsconfig.CredentialsProvider, error) {
	var source awsconfig.CredentialsProvider
	switch connection.AuthMode {
	case storage.CloudAuthAWSStaticKeys:
		secret, err := r.cloudSecret(ctx, connection, cloudCredentialAWSSecretKey, connection.AWSSecretKeyCiphertext, connection.AWSSecretKeyWrappedKey)
		if err != nil {
			return nil, err
		}
		source = credentials.NewStaticCredentialsProvider(connection.AWSAccessKeyID, secret, "")
	case storage.CloudAuthAWSAmbient, storage.CloudAuthAWSAssumeRole:
		loaded, err := config.LoadDefaultConfig(ctx, config.WithRegion(connection.AWSRegion))
		if err != nil {
			return nil, err
		}
		source = loaded.Credentials
	default:
		return nil, fmt.Errorf("unsupported AWS auth mode %q", connection.AuthMode)
	}
	if connection.AuthMode != storage.CloudAuthAWSAssumeRole {
		return source, nil
	}
	loaded, err := config.LoadDefaultConfig(ctx, config.WithRegion(connection.AWSRegion), config.WithCredentialsProvider(source))
	if err != nil {
		return nil, err
	}
	externalID := ""
	if connection.HasAWSExternalID() {
		externalID, err = r.cloudSecret(ctx, connection, cloudCredentialAWSExternalID, connection.AWSExternalIDCiphertext, connection.AWSExternalIDWrappedKey)
		if err != nil {
			return nil, err
		}
	}
	provider := stscreds.NewAssumeRoleProvider(sts.NewFromConfig(loaded), connection.AWSRoleARN, func(options *stscreds.AssumeRoleOptions) {
		if externalID != "" {
			options.ExternalID = &externalID
		}
	})
	return awsconfig.NewCredentialsCache(provider), nil
}
