package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/jasonczech/apexload/pkg/compute"
	computeaws "github.com/jasonczech/apexload/pkg/compute/aws"
	computeazure "github.com/jasonczech/apexload/pkg/compute/azure"
	"github.com/jasonczech/apexload/pkg/storage"
)

// cloudConnectionProbeResolver lets a cloud credential be validated and its
// resources enumerated WITHOUT an execution target. Without it an operator can
// only discover that a credential is wrong after creating a target that uses
// it, which inverts the natural setup order.
type cloudConnectionProbeResolver interface {
	ProbeCloudConnection(context.Context, *storage.CloudConnection) []ClusterProbe
	DiscoverCloudResources(context.Context, *storage.CloudConnection) (CloudResourceInventory, error)
}

// CloudConnectionTestResponse mirrors ExecutionTargetTestResponse so the
// dashboard renders both with one component.
type CloudConnectionTestResponse struct {
	CloudConnectionID string         `json:"cloud_connection_id"`
	Status            string         `json:"status"`
	Summary           string         `json:"summary"`
	CheckedAt         time.Time      `json:"checked_at"`
	Probes            []ClusterProbe `json:"probes"`
}

// CloudResource is one provider resource an execution target can be bound to.
type CloudResource struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location,omitempty"`
}

// CloudResourceInventory is the discovery payload behind the execution-target
// wizard's resource pickers.
type CloudResourceInventory struct {
	CloudConnectionID string          `json:"cloud_connection_id"`
	Provider          string          `json:"provider"`
	Scope             string          `json:"scope,omitempty"`
	Resources         []CloudResource `json:"resources"`
	Truncated         bool            `json:"truncated,omitempty"`
	Message           string          `json:"message,omitempty"`
}

// ExecutionTargetCapacityResponse reports what the target is allowed to run and
// what it is running now. Provider quota is deliberately absent: neither ARM nor
// ECS exposes a per-target generator quota that is meaningful here, and
// inventing one would report a number the scheduler does not honor.
type ExecutionTargetCapacityResponse struct {
	ExecutionTargetID string                        `json:"execution_target_id"`
	Kind              string                        `json:"kind"`
	CheckedAt         time.Time                     `json:"checked_at"`
	Capabilities      storage.TargetCapabilities    `json:"capabilities"`
	Pools             []ExecutionTargetPoolCapacity `json:"pools"`
	ConfiguredWarm    int                           `json:"configured_warm_floor"`
	ConfiguredMax     int                           `json:"configured_max"`
	LiveInstances     int                           `json:"live_instances"`
	HealthyInstances  int                           `json:"healthy_instances"`
	Message           string                        `json:"message,omitempty"`
}

// ExecutionTargetPoolCapacity is one bound pool's declared envelope.
type ExecutionTargetPoolCapacity struct {
	FleetPoolID   string `json:"fleet_pool_id"`
	Name          string `json:"name"`
	LifecycleMode string `json:"lifecycle_mode"`
	WarmFloor     int    `json:"warm_floor"`
	MaxSize       int    `json:"max_size"`
	DesiredSize   int    `json:"desired_size"`
}

func (s *Server) handleTestCloudConnection(w http.ResponseWriter, r *http.Request) {
	connection, ok := s.loadCloudConnection(w, r.PathValue("id"))
	if !ok {
		return
	}
	result := CloudConnectionTestResponse{CloudConnectionID: connection.ID, CheckedAt: time.Now().UTC()}
	if err := validateCloudConnectionCredential(connection); err != nil {
		result.Probes = []ClusterProbe{{Probe: "credential", Message: err.Error()}}
	} else {
		result.Probes = []ClusterProbe{{Probe: "credential", OK: true, Message: "credential configuration is complete"}}
		resolver, hasResolver := s.capacityBackends.(cloudConnectionProbeResolver)
		if !hasResolver {
			result.Probes = append(result.Probes, ClusterProbe{Probe: "provider_identity", Optional: true, Message: "live provider validation is unavailable on this controller"})
		} else {
			result.Probes = append(result.Probes, resolver.ProbeCloudConnection(r.Context(), connection)...)
		}
	}
	result.Status, result.Summary = summarizeClusterProbes(result.Probes)
	if s.cloudStore != nil {
		_ = s.cloudStore.UpdateCloudConnectionHealth(connection.ID, result.Status, result.Summary, result.CheckedAt)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleListCloudConnectionResources(w http.ResponseWriter, r *http.Request) {
	connection, ok := s.loadCloudConnection(w, r.PathValue("id"))
	if !ok {
		return
	}
	if err := validateCloudConnectionCredential(connection); err != nil {
		writeError(w, http.StatusConflict, "cloud connection credential is incomplete: "+err.Error())
		return
	}
	resolver, hasResolver := s.capacityBackends.(cloudConnectionProbeResolver)
	if !hasResolver {
		writeError(w, http.StatusServiceUnavailable, "provider resource discovery is unavailable on this controller")
		return
	}
	inventory, err := resolver.DiscoverCloudResources(r.Context(), connection)
	if err != nil {
		// Discovery is an optional convenience over a live provider API. A
		// failure here is a provider/permission condition, not a malformed
		// request, so it must not read as client error.
		writeError(w, http.StatusBadGateway, "provider resource discovery failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inventory)
}

func (s *Server) handleGetExecutionTargetCapacity(w http.ResponseWriter, r *http.Request) {
	target, ok := s.loadExecutionTarget(w, r.PathValue("id"))
	if !ok {
		return
	}
	response := ExecutionTargetCapacityResponse{
		ExecutionTargetID: target.ID,
		Kind:              target.Kind,
		CheckedAt:         time.Now().UTC(),
		Capabilities:      configuredTargetCapabilities(target),
		Pools:             []ExecutionTargetPoolCapacity{},
	}
	// ListFleetPoolsForExecutionTarget is the indexed per-target query; scanning
	// every pool in the deployment to filter in Go would not scale.
	if s.executionTargetStore != nil {
		pools, err := s.executionTargetStore.ListFleetPoolsForExecutionTarget(target.ID)
		if err != nil {
			// A swallowed error would render as "no capacity configured", which
			// an operator would act on. Say we could not determine it instead.
			response.Message = "bound fleet pools could not be loaded: " + err.Error()
		}
		for _, pool := range pools {
			if pool == nil {
				continue
			}
			response.Pools = append(response.Pools, ExecutionTargetPoolCapacity{
				FleetPoolID: pool.ID, Name: pool.Name, LifecycleMode: pool.LifecycleMode,
				WarmFloor: pool.WarmFloor, MaxSize: pool.MaxSize, DesiredSize: pool.DesiredSize,
			})
			response.ConfiguredWarm += pool.WarmFloor
			response.ConfiguredMax += pool.MaxSize
		}
	}
	sort.Slice(response.Pools, func(i, j int) bool { return response.Pools[i].FleetPoolID < response.Pools[j].FleetPoolID })

	resolver, hasResolver := s.capacityBackends.(capacityBackendTargetResolver)
	if !hasResolver {
		response.Message = appendCapacityMessage(response.Message, "live provider inventory is unavailable on this controller")
		writeJSON(w, http.StatusOK, response)
		return
	}
	backend, err := resolver.BackendForTarget(r.Context(), target)
	if err != nil {
		response.Message = appendCapacityMessage(response.Message, err.Error())
		writeJSON(w, http.StatusOK, response)
		return
	}
	// Discovery is per pool because provider tags carry pool identity; a target
	// with no bound pool has nothing to enumerate.
	seen := make(map[string]struct{})
	for _, pool := range response.Pools {
		instances, discoverErr := backend.Discover(r.Context(), compute.CapacityRequest{
			ExecutionTargetID: target.ID, FleetPoolID: pool.FleetPoolID,
		})
		if discoverErr != nil {
			// Accumulate rather than assign: with several bound pools, assignment
			// would leave only the last failure visible while LiveInstances
			// silently undercounted the rest.
			response.Message = appendCapacityMessage(response.Message, pool.FleetPoolID+": "+discoverErr.Error())
			continue
		}
		for _, instance := range instances {
			if _, duplicate := seen[instance.ProviderID]; duplicate {
				continue
			}
			seen[instance.ProviderID] = struct{}{}
			response.LiveInstances++
			if instance.Healthy {
				response.HealthyInstances++
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// ProbeCloudConnection performs a real provider round trip. Configuration
// validity is checked by the caller; this proves the credential is accepted.
func (r *capacityBackendResolver) ProbeCloudConnection(ctx context.Context, connection *storage.CloudConnection) []ClusterProbe {
	if r == nil || connection == nil {
		return []ClusterProbe{{Probe: "provider_identity", Message: "cloud connection is unavailable"}}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	switch connection.Provider {
	case storage.CloudProviderAzure:
		return r.probeAzureConnection(ctx, connection)
	case storage.CloudProviderAWS:
		return r.probeAWSConnection(ctx, connection)
	default:
		return []ClusterProbe{{Probe: "provider_identity", Message: "unsupported cloud provider " + connection.Provider}}
	}
}

func (r *capacityBackendResolver) probeAzureConnection(ctx context.Context, connection *storage.CloudConnection) []ClusterProbe {
	credential, err := r.azureCredential(ctx, connection)
	if err != nil {
		return []ClusterProbe{{Probe: "provider_identity", Message: scrubClusterError(err)}}
	}
	endpoint, scope, err := computeazure.ValidateARMEndpoint(azureARMEndpoint(connection.AzureCloud))
	if err != nil {
		return []ClusterProbe{{Probe: "provider_identity", Message: err.Error()}}
	}
	if _, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}}); err != nil {
		return []ClusterProbe{{Probe: "provider_identity", Message: "Azure identity was rejected: " + scrubClusterError(err)}}
	}
	probes := []ClusterProbe{{Probe: "provider_identity", OK: true, Message: "Azure identity acquired an ARM token"}}
	client := &computeazure.RESTClient{Endpoint: endpoint, Credential: credential}
	var subscription struct {
		DisplayName string `json:"displayName"`
	}
	path := "/subscriptions/" + connection.AzureSubscriptionID + "?api-version=2022-12-01"
	if _, _, err := client.Do(ctx, http.MethodGet, path, nil, &subscription); err != nil {
		probes = append(probes, ClusterProbe{Probe: "subscription", Message: "subscription is not readable with this identity: " + scrubClusterError(err)})
		return probes
	}
	name := strings.TrimSpace(subscription.DisplayName)
	if name == "" {
		name = connection.AzureSubscriptionID
	}
	return append(probes, ClusterProbe{Probe: "subscription", OK: true, Message: "subscription " + name + " is readable"})
}

func (r *capacityBackendResolver) probeAWSConnection(ctx context.Context, connection *storage.CloudConnection) []ClusterProbe {
	provider, err := r.awsCredentials(ctx, connection)
	if err != nil {
		return []ClusterProbe{{Probe: "provider_identity", Message: scrubClusterError(err)}}
	}
	loaded, err := config.LoadDefaultConfig(ctx, config.WithRegion(connection.AWSRegion), config.WithCredentialsProvider(provider))
	if err != nil {
		return []ClusterProbe{{Probe: "provider_identity", Message: scrubClusterError(err)}}
	}
	identity, err := sts.NewFromConfig(loaded).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return []ClusterProbe{{Probe: "sts_identity", Message: describeAWSIdentityError(err)}}
	}
	arn := ""
	if identity != nil && identity.Arn != nil {
		arn = *identity.Arn
	}
	// The ARN is echoed because the dominant ECS failure is a valid credential
	// whose ARN lacks the task/service permissions; the operator needs the exact
	// principal to grant.
	return []ClusterProbe{{Probe: "sts_identity", OK: true, Message: "AWS identity resolved as " + arn}}
}

// DiscoverCloudResources enumerates the provider resources an execution target
// binds to, so the wizard can offer them instead of requiring hand-typed names.
func (r *capacityBackendResolver) DiscoverCloudResources(ctx context.Context, connection *storage.CloudConnection) (CloudResourceInventory, error) {
	if r == nil || connection == nil {
		return CloudResourceInventory{}, errCapacityBackendUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	inventory := CloudResourceInventory{CloudConnectionID: connection.ID, Provider: connection.Provider, Resources: []CloudResource{}}
	switch connection.Provider {
	case storage.CloudProviderAzure:
		inventory.Scope = connection.AzureSubscriptionID
		resources, truncated, err := r.discoverAzureResources(ctx, connection)
		if err != nil {
			return CloudResourceInventory{}, err
		}
		inventory.Resources, inventory.Truncated = resources, truncated
	case storage.CloudProviderAWS:
		inventory.Scope = connection.AWSRegion
		resources, truncated, err := r.discoverAWSResources(ctx, connection)
		if err != nil {
			return CloudResourceInventory{}, err
		}
		inventory.Resources, inventory.Truncated = resources, truncated
	default:
		return CloudResourceInventory{}, fmt.Errorf("unsupported cloud provider %q", connection.Provider)
	}
	sort.Slice(inventory.Resources, func(i, j int) bool {
		if inventory.Resources[i].Kind != inventory.Resources[j].Kind {
			return inventory.Resources[i].Kind < inventory.Resources[j].Kind
		}
		return inventory.Resources[i].Name < inventory.Resources[j].Name
	})
	if len(inventory.Resources) == 0 {
		inventory.Message = "no compatible resources were visible to this identity"
	}
	return inventory, nil
}

const (
	cloudResourceDiscoveryLimit     = 200
	cloudResourceDiscoveryPageLimit = 20
)

func (r *capacityBackendResolver) discoverAzureResources(ctx context.Context, connection *storage.CloudConnection) ([]CloudResource, bool, error) {
	credential, err := r.azureCredential(ctx, connection)
	if err != nil {
		return nil, false, err
	}
	endpoint, _, err := computeazure.ValidateARMEndpoint(azureARMEndpoint(connection.AzureCloud))
	if err != nil {
		return nil, false, err
	}
	client := &computeazure.RESTClient{Endpoint: endpoint, Credential: credential}
	next := "/subscriptions/" + connection.AzureSubscriptionID + "/providers/Microsoft.App/managedEnvironments?api-version=2024-03-01"
	resources := []CloudResource{}
	// ARM list responses paginate. Reading only the first page would return a
	// short list with no indication anything was missing, so follow nextLink
	// with a bounded page count and a cycle guard, as discoverExecutions does.
	seen := map[string]struct{}{}
	for page := 0; next != "" && page < cloudResourceDiscoveryPageLimit; page++ {
		if _, duplicate := seen[next]; duplicate {
			return nil, false, errors.New("Azure managed-environment pagination repeated a page")
		}
		seen[next] = struct{}{}
		var response struct {
			Value []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Location string `json:"location"`
			} `json:"value"`
			NextLink string `json:"nextLink"`
		}
		if _, _, err := client.Do(ctx, http.MethodGet, next, nil, &response); err != nil {
			return nil, false, err
		}
		for _, environment := range response.Value {
			if len(resources) >= cloudResourceDiscoveryLimit {
				return resources, true, nil
			}
			resources = append(resources, CloudResource{
				Kind: "container_app_environment", ID: environment.ID, Name: environment.Name, Location: environment.Location,
			})
		}
		next = strings.TrimSpace(response.NextLink)
	}
	// An unfollowed page is truncation too, and must be reported as such.
	return resources, next != "", nil
}

func (r *capacityBackendResolver) discoverAWSResources(ctx context.Context, connection *storage.CloudConnection) ([]CloudResource, bool, error) {
	provider, err := r.awsCredentials(ctx, connection)
	if err != nil {
		return nil, false, err
	}
	client := &computeaws.JSONClient{Region: connection.AWSRegion, Credentials: provider}
	resources := []CloudResource{}
	nextToken := ""
	// ListClusters paginates via nextToken; ignoring it would silently return
	// only the first page while reporting the result as complete.
	for page := 0; page < cloudResourceDiscoveryPageLimit; page++ {
		input := map[string]any{}
		if nextToken != "" {
			input["nextToken"] = nextToken
		}
		var response struct {
			ClusterARNs []string `json:"clusterArns"`
			NextToken   string   `json:"nextToken"`
		}
		if err := client.Call(ctx, "ListClusters", input, &response); err != nil {
			return nil, false, err
		}
		for _, arn := range response.ClusterARNs {
			if len(resources) >= cloudResourceDiscoveryLimit {
				return resources, true, nil
			}
			name := arn
			if index := strings.LastIndex(arn, "/"); index >= 0 && index+1 < len(arn) {
				name = arn[index+1:]
			}
			resources = append(resources, CloudResource{Kind: "ecs_cluster", ID: arn, Name: name, Location: connection.AWSRegion})
		}
		nextToken = strings.TrimSpace(response.NextToken)
		if nextToken == "" {
			return resources, false, nil
		}
	}
	return resources, true, nil
}

// appendCapacityMessage joins partial-failure notes so one pool's error cannot
// erase another's.
func appendCapacityMessage(existing, addition string) string {
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return addition
	}
	return existing + "; " + addition
}

func azureARMEndpoint(azureCloud string) string {
	switch azureCloud {
	case storage.AzureCloudUSGovernment:
		return "https://management.usgovcloudapi.net"
	case storage.AzureCloudChina:
		return "https://management.chinacloudapi.cn"
	default:
		return "https://management.azure.com"
	}
}
