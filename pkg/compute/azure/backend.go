package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

const apiVersion = "2025-07-01"

var allowedARMHosts = map[string]string{
	"management.azure.com":         "https://management.azure.com/.default",
	"management.usgovcloudapi.net": "https://management.core.usgovcloudapi.net/.default",
	"management.chinacloudapi.cn":  "https://management.core.chinacloudapi.cn/.default",
}

type API interface {
	Do(context.Context, string, string, any, any) (operationID string, retryAfter time.Duration, err error)
}

type RESTClient struct {
	Endpoint   string
	Credential azcore.TokenCredential
	HTTP       *http.Client
}

func ValidateARMEndpoint(raw string) (endpoint, scope string, err error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		value = "https://management.azure.com"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return "", "", errors.New("Azure Resource Manager endpoint must be a bare https origin")
	}
	host := strings.ToLower(parsed.Hostname())
	scope, ok := allowedARMHosts[host]
	if !ok {
		return "", "", fmt.Errorf("Azure Resource Manager endpoint host %q is not allowed", host)
	}
	return "https://" + host, scope, nil
}

func (c *RESTClient) Do(ctx context.Context, method, path string, body any, output any) (string, time.Duration, error) {
	if c == nil || c.Credential == nil {
		return "", 0, errors.New("Azure credential is not configured")
	}
	endpoint, scope, err := ValidateARMEndpoint(c.Endpoint)
	if err != nil {
		return "", 0, err
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return "", 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	requestURL := endpoint + path
	if parsedPath, parseErr := url.Parse(strings.TrimSpace(path)); parseErr == nil && parsedPath.IsAbs() {
		if parsedPath.Scheme != "https" || parsedPath.User != nil || !strings.EqualFold(parsedPath.Hostname(), strings.TrimPrefix(endpoint, "https://")) {
			return "", 0, errors.New("Azure long-running operation URL must use the configured ARM origin")
		}
		requestURL = parsedPath.String()
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return "", 0, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	token, err := c.Credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
	if err != nil {
		return "", 0, fmt.Errorf("resolve Azure ARM token: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token.Token)
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", 0, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", 0, fmt.Errorf("Azure ARM request failed with HTTP %d", response.StatusCode)
	}
	if output != nil && len(data) > 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return "", 0, err
		}
	}
	retry := time.Duration(0)
	if value := response.Header.Get("Retry-After"); value != "" {
		if parsed, parseErr := time.ParseDuration(value + "s"); parseErr == nil {
			retry = parsed
		}
	}
	operationID := first(response.Header.Get("Azure-AsyncOperation"), response.Header.Get("Location"))
	return operationID, retry, nil
}

type Backend struct {
	API            API
	Target         *storage.ExecutionTarget
	SubscriptionID string
	ControllerID   string
}

func (b *Backend) Describe() compute.BackendInfo {
	info := compute.BackendInfo{Name: storage.ExecutionTargetAzureContainerApps, SupportsReverse: true, Details: map[string]string{"capacity_model": "container_app_and_job"}}
	if b != nil && b.Target != nil && b.Target.AzureContainerApps != nil {
		info.CanScale = b.Target.StandingOwner == storage.CapacityOwnerApex && b.Target.AzureContainerApps.WarmContainerApp != ""
		info.CanLaunch = b.Target.BurstOwner == storage.CapacityOwnerApex && b.Target.AzureContainerApps.BurstJob != ""
		for _, profile := range b.Target.RuntimeProfiles {
			if profile.CPUMillis > 0 && profile.MemoryMiB > 0 {
				info.ResourceShapes = append(info.ResourceShapes, fmt.Sprintf("%dm/%dMi", profile.CPUMillis, profile.MemoryMiB))
			}
		}
	}
	return info
}

func (b *Backend) resourcePath(resourceType, name, suffix string) string {
	spec := b.Target.AzureContainerApps
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/%s/%s%s?api-version=%s",
		url.PathEscape(b.SubscriptionID), url.PathEscape(spec.ResourceGroup), resourceType, url.PathEscape(name), suffix, apiVersion)
}

func (b *Backend) Validate(ctx context.Context) []compute.ProbeResult {
	if b == nil || b.API == nil || b.Target == nil || b.Target.AzureContainerApps == nil || strings.TrimSpace(b.SubscriptionID) == "" {
		return []compute.ProbeResult{{Probe: "configuration", Message: "Azure Container Apps backend is incomplete"}}
	}
	spec := b.Target.AzureContainerApps
	probes := []compute.ProbeResult{}
	if spec.WarmContainerApp != "" {
		var response armResourceResponse
		_, _, err := b.API.Do(ctx, http.MethodGet, b.resourcePath("containerApps", spec.WarmContainerApp, ""), nil, &response)
		probes = append(probes, compute.ProbeResult{Probe: "container_app", OK: err == nil, Message: message(err, "warm Container App is accessible")})
		probes = append(probes, containerAppOwnershipProbe(err, response.hasScaleRules(), b.Target.StandingOwner))
		probes = append(probes, identityProbe("container_app_identity", err, response.principalIDs(), spec.ExpectedPrincipalID))
		probes = append(probes, sidecarProbe("container_app_sidecars", err, requiredSidecars(b.Target.RuntimeProfiles), response.containerNames()))
	}
	if spec.BurstJob != "" {
		var response armResourceResponse
		_, _, err := b.API.Do(ctx, http.MethodGet, b.resourcePath("jobs", spec.BurstJob, ""), nil, &response)
		probes = append(probes, compute.ProbeResult{Probe: "container_app_job", OK: err == nil, Message: message(err, "burst Container Apps Job is accessible")})
		probes = append(probes, identityProbe("container_app_job_identity", err, response.principalIDs(), spec.ExpectedPrincipalID))
		probes = append(probes, sidecarProbe("container_app_job_sidecars", err, requiredSidecars(b.Target.RuntimeProfiles), response.containerNames()))
	}
	return probes
}

func (b *Backend) Plan(_ context.Context, request compute.CapacityRequest) (compute.Plan, error) {
	if request.Desired < 0 {
		return compute.Plan{}, errors.New("desired capacity must not be negative")
	}
	info := b.Describe()
	standing := min(request.Desired, max(request.WarmFloor, 0))
	if standing > 0 && b.Target.StandingOwner == storage.CapacityOwnerExternal {
		return compute.Plan{}, compute.ErrOwnershipConflict
	}
	if request.Desired > standing && b.Target.BurstOwner == storage.CapacityOwnerExternal {
		return compute.Plan{}, compute.ErrOwnershipConflict
	}
	if !info.CanLaunch {
		standing = request.Desired
	}
	if standing > 0 && !info.CanScale {
		return compute.Plan{}, compute.ErrUnsupportedOperation
	}
	launch := request.Desired - standing
	if launch > 0 && !info.CanLaunch {
		return compute.Plan{}, compute.ErrUnsupportedOperation
	}
	return compute.Plan{Requested: request.Desired, Standing: standing, Launch: launch}, nil
}

func (b *Backend) Reconcile(ctx context.Context, request compute.CapacityRequest) (observation compute.Observation, err error) {
	operationIDs := append([]string(nil), request.ProviderOperationIDs...)
	defer func() {
		if err != nil {
			observation.ProviderOperationID = strings.Join(operationIDs, ",")
		}
	}()
	plan, err := b.Plan(ctx, request)
	if err != nil {
		return compute.Observation{}, err
	}
	profile, err := b.runtimeProfile(request.RuntimeProfileID)
	if err != nil {
		return compute.Observation{}, err
	}
	if err := compute.ValidateLaunchIntents(request.LaunchIntents, plan.Launch); err != nil {
		return compute.Observation{}, err
	}
	if err := compute.ValidateLaunchScope(request, b.ControllerID, plan.Launch); err != nil {
		return compute.Observation{}, err
	}
	spec := b.Target.AzureContainerApps
	if b.Target.StandingOwner == storage.CapacityOwnerApex && spec.WarmContainerApp != "" {
		var current armResourceResponse
		if _, _, err := b.API.Do(ctx, http.MethodGet, b.resourcePath("containerApps", spec.WarmContainerApp, ""), nil, &current); err != nil {
			return compute.Observation{}, fmt.Errorf("inspect warm Container App autoscaling ownership: %w", err)
		}
		if b.Target.StandingOwner == storage.CapacityOwnerApex && current.hasScaleRules() {
			return compute.Observation{}, fmt.Errorf("%w: warm Container App has provider-side scale rules", compute.ErrOwnershipConflict)
		}
		body := map[string]any{"properties": map[string]any{"template": map[string]any{"scale": map[string]any{"minReplicas": plan.Standing, "maxReplicas": max(plan.Standing, 1)}}}}
		if _, _, err := b.API.Do(ctx, http.MethodPatch, b.resourcePath("containerApps", spec.WarmContainerApp, ""), body, nil); err != nil {
			return compute.Observation{}, err
		}
	}
	operationIDs = make([]string, 0, plan.Launch)
	operationIDSet := make(map[string]struct{}, plan.Launch)
	appendOperationID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, exists := operationIDSet[id]; exists {
			return
		}
		operationIDSet[id] = struct{}{}
		operationIDs = append(operationIDs, id)
	}
	coveredNonces := make(map[string]struct{}, plan.Launch)
	for index, id := range request.ProviderOperationIDs {
		appendOperationID(id)
		// Provider operation IDs are persisted in launch-intent order. Preserve
		// that association even while an Azure long-running operation has not
		// exposed its execution template for discovery yet.
		if index < plan.Launch {
			coveredNonces[request.LaunchIntents[index].LaunchNonce] = struct{}{}
		}
	}
	if plan.Launch > 0 {
		// Container Apps Jobs has no caller-selected execution name. Discovery
		// by the durable operation and launch nonce is therefore the idempotency
		// fence when ARM accepted /start but its response was lost.
		discovered, discoverErr := b.discoverExecutions(ctx, request)
		if discoverErr != nil {
			return compute.Observation{}, fmt.Errorf("discover Azure Job executions before launch: %w", discoverErr)
		}
		for _, instance := range discovered {
			appendOperationID(instance.ProviderID)
			if nonce := strings.TrimSpace(instance.Tags["apexload.io/launch-nonce"]); nonce != "" {
				coveredNonces[nonce] = struct{}{}
			}
		}
	}
	for launchIndex := 0; launchIndex < plan.Launch; launchIndex++ {
		intent := request.LaunchIntents[launchIndex]
		if _, covered := coveredNonces[intent.LaunchNonce]; covered {
			continue
		}
		container := map[string]any{"name": "generator", "env": []any{
			map[string]string{"name": "APEXLOAD_GENERATOR_ID", "value": intent.GeneratorID},
			map[string]string{"name": "APEXLOAD_LAUNCH_NONCE", "value": intent.LaunchNonce},
			map[string]string{"name": "APEXLOAD_EXECUTION_TARGET_ID", "value": request.ExecutionTargetID},
			map[string]string{"name": "APEXLOAD_CONTROLLER_ID", "value": b.ControllerID},
			map[string]string{"name": "APEXLOAD_FLEET_POOL_ID", "value": request.FleetPoolID},
			map[string]string{"name": "APEXLOAD_CAPACITY_OPERATION_ID", "value": request.OperationID},
			map[string]string{"name": "APEXLOAD_TEST_ID", "value": request.TestID},
			map[string]string{"name": "APEXLOAD_GENERATOR_LIFECYCLE", "value": "burst"},
			map[string]string{"name": "APEXLOAD_GENERATOR_CONNECTIVITY_MODE", "value": b.Target.ConnectivityMode},
			map[string]string{"name": "APEXLOAD_RUNTIME_PROFILE_ID", "value": request.RuntimeProfileID},
		}}
		if profile != nil {
			resources := map[string]any{}
			if profile.CPUMillis > 0 {
				resources["cpu"] = float64(profile.CPUMillis) / 1000
			}
			if profile.MemoryMiB > 0 {
				resources["memory"] = fmt.Sprintf("%dMi", profile.MemoryMiB)
			}
			if profile.EphemeralStorageGiB > 0 {
				resources["ephemeralStorage"] = fmt.Sprintf("%dGi", profile.EphemeralStorageGiB)
			}
			if len(resources) > 0 {
				container["resources"] = resources
			}
		}
		body := map[string]any{"containers": []any{container}}
		var response struct {
			Name string `json:"name"`
		}
		operationID, _, err := b.API.Do(ctx, http.MethodPost, b.resourcePath("jobs", spec.BurstJob, "/start"), body, &response)
		if err != nil {
			return compute.Observation{}, err
		}
		operationID = first(response.Name, operationID)
		if operationID == "" {
			return compute.Observation{}, errors.New("Azure Container Apps Job start returned no execution identity")
		}
		appendOperationID(operationID)
		coveredNonces[intent.LaunchNonce] = struct{}{}
	}
	instances, err := b.describeExecutions(ctx, operationIDs)
	if err != nil {
		return compute.Observation{}, err
	}
	resolvedOperationIDs := make([]string, 0, len(instances))
	for _, instance := range instances {
		resolvedOperationIDs = append(resolvedOperationIDs, instance.ProviderID)
	}
	observation = compute.Observation{ProviderOperationID: strings.Join(resolvedOperationIDs, ","), Requested: request.Desired, Observed: plan.Standing + len(instances), Ready: plan.Standing, Instances: instances}
	for _, instance := range instances {
		if instance.Healthy {
			observation.Ready++
		} else if strings.EqualFold(instance.State, "Failed") {
			observation.Failed++
		} else {
			observation.Pending++
		}
	}
	observation.Terminal = observation.Ready >= request.Desired
	if !observation.Terminal {
		observation.RetryAfter = 3 * time.Second
		observation.Message = "waiting for reverse-connected Container Apps generators"
	}
	return observation, nil
}

func (b *Backend) runtimeProfile(id string) (*storage.RuntimeProfile, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	if b == nil || b.Target == nil {
		return nil, errors.New("runtime profile target is unavailable")
	}
	for index := range b.Target.RuntimeProfiles {
		if b.Target.RuntimeProfiles[index].ID == id {
			return &b.Target.RuntimeProfiles[index], nil
		}
	}
	return nil, fmt.Errorf("runtime profile %q is not registered on the Azure execution target", id)
}

func (b *Backend) Cancel(ctx context.Context, request compute.CapacityRequest) error {
	if request.RestoreStanding && b.Target.StandingOwner == storage.CapacityOwnerApex && strings.TrimSpace(b.Target.AzureContainerApps.WarmContainerApp) != "" {
		standing := max(request.RollbackStanding, 0)
		body := map[string]any{"properties": map[string]any{"template": map[string]any{"scale": map[string]any{
			"minReplicas": standing, "maxReplicas": max(standing, 1),
		}}}}
		if _, _, err := b.API.Do(ctx, http.MethodPatch, b.resourcePath("containerApps", b.Target.AzureContainerApps.WarmContainerApp, ""), body, nil); err != nil {
			return err
		}
	}
	var instances []compute.Instance
	for {
		var err error
		instances, err = b.describeExecutions(ctx, request.ProviderOperationIDs)
		if err != nil {
			return err
		}
		unresolved := false
		for _, instance := range instances {
			if strings.Contains(instance.ProviderID, "/") {
				unresolved = true
				break
			}
		}
		if !unresolved {
			break
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("resolve Azure Job execution before cancellation: %w", ctx.Err())
		case <-timer.C:
		}
	}
	instances, discoveryErr := compute.DiscoverCancellationInstances(ctx, request, instances, b.Discover)
	for i := range instances {
		instances[i].ArchiveTerminal = true
	}
	return errors.Join(discoveryErr, b.Deprovision(ctx, request, instances))
}
func (b *Backend) Drain(context.Context, compute.CapacityRequest, []compute.Instance) error {
	return nil
}
func (b *Backend) Deprovision(ctx context.Context, _ compute.CapacityRequest, instances []compute.Instance) error {
	if err := compute.EnsureArchiveTerminal(instances); err != nil {
		return err
	}
	for _, instance := range instances {
		if _, _, err := b.API.Do(ctx, http.MethodPost, b.resourcePath("jobs", b.Target.AzureContainerApps.BurstJob, "/executions/"+url.PathEscape(instance.ProviderID)+"/stop"), map[string]any{}, nil); err != nil {
			return err
		}
	}
	return nil
}
func (b *Backend) Discover(ctx context.Context, request compute.CapacityRequest) ([]compute.Instance, error) {
	if len(request.ProviderOperationIDs) == 0 {
		return b.discoverExecutions(ctx, request)
	}
	return b.describeExecutions(ctx, request.ProviderOperationIDs)
}
func (b *Backend) Adopt(ctx context.Context, request compute.AdoptionRequest) (compute.Instance, error) {
	if b == nil || b.Target == nil || strings.TrimSpace(request.ProviderID) == "" || strings.TrimSpace(request.ExecutionTargetID) == "" || strings.TrimSpace(request.FleetPoolID) == "" ||
		strings.TrimSpace(request.OperationID) == "" || strings.TrimSpace(request.GeneratorID) == "" || strings.TrimSpace(request.LaunchNonce) == "" {
		return compute.Instance{}, errors.New("provider, execution-target, fleet-pool, operation, generator, and launch identities are required for adoption")
	}
	instances, err := b.describeExecutions(ctx, []string{request.ProviderID})
	if err != nil {
		return compute.Instance{}, err
	}
	if len(instances) != 1 {
		return compute.Instance{}, fmt.Errorf("Azure Job execution %q could not be observed for adoption", request.ProviderID)
	}
	instance := instances[0]
	if instance.Tags["apexload.io/controller-id"] != b.ControllerID ||
		instance.Tags["apexload.io/execution-target-id"] != request.ExecutionTargetID ||
		instance.Tags["apexload.io/pool-id"] != request.FleetPoolID ||
		instance.Tags["apexload.io/operation-id"] != request.OperationID ||
		instance.Tags["apexload.io/generator-id"] != request.GeneratorID ||
		instance.Tags["apexload.io/launch-nonce"] != request.LaunchNonce || !instance.IdentityAttested {
		return compute.Instance{}, errors.New("Azure Job execution ownership metadata does not authorize adoption")
	}
	return instance, nil
}

func (b *Backend) describeExecutions(ctx context.Context, ids []string) ([]compute.Instance, error) {
	instances := make([]compute.Instance, 0, len(ids))
	for _, id := range ids {
		if strings.Contains(id, "/") {
			var operation struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Properties struct {
					Status     string `json:"status"`
					ResourceID string `json:"resourceId"`
				} `json:"properties"`
			}
			_, _, err := b.API.Do(ctx, http.MethodGet, id, nil, &operation)
			if err != nil {
				return nil, err
			}
			state := first(operation.Properties.Status, operation.Status)
			providerID := ""
			if operation.Properties.ResourceID != "" {
				providerID = pathBase(operation.Properties.ResourceID)
			}
			if providerID == "" {
				providerID = strings.TrimSpace(operation.Name)
			}
			if providerID == "" || strings.EqualFold(state, "InProgress") || strings.EqualFold(state, "Accepted") {
				providerID = id
			}
			instances = append(instances, compute.Instance{ProviderID: providerID, State: first(state, "Pending"), Healthy: strings.EqualFold(state, "Running")})
			continue
		}
		var response jobExecutionResponse
		_, _, err := b.API.Do(ctx, http.MethodGet, b.resourcePath("jobs", b.Target.AzureContainerApps.BurstJob, "/executions/"+url.PathEscape(id)), nil, &response)
		if err != nil {
			return nil, err
		}
		instance := response.instance()
		instance.ProviderID = first(instance.ProviderID, id)
		instances = append(instances, instance)
	}
	return instances, nil
}

func (b *Backend) discoverExecutions(ctx context.Context, request compute.CapacityRequest) ([]compute.Instance, error) {
	if strings.TrimSpace(b.ControllerID) == "" || strings.TrimSpace(request.FleetPoolID) == "" {
		return nil, nil
	}
	next := b.resourcePath("jobs", b.Target.AzureContainerApps.BurstJob, "/executions")
	seenPages := make(map[string]struct{})
	instances := []compute.Instance{}
	for page := 0; next != "" && page < 20; page++ {
		if _, duplicate := seenPages[next]; duplicate {
			return nil, errors.New("Azure Container Apps executions pagination repeated a page")
		}
		seenPages[next] = struct{}{}
		var response struct {
			Value    []jobExecutionResponse `json:"value"`
			NextLink string                 `json:"nextLink"`
		}
		if _, _, err := b.API.Do(ctx, http.MethodGet, next, nil, &response); err != nil {
			return nil, err
		}
		allowedIntents := make(map[string]string, len(request.LaunchIntents))
		for _, intent := range request.LaunchIntents {
			allowedIntents[strings.TrimSpace(intent.LaunchNonce)] = strings.TrimSpace(intent.GeneratorID)
		}
		expectedOperationID := strings.TrimSpace(request.OperationID)
		for _, execution := range response.Value {
			env := execution.environment()
			operationID := strings.TrimSpace(env["APEXLOAD_CAPACITY_OPERATION_ID"])
			if env["APEXLOAD_CONTROLLER_ID"] != b.ControllerID || env["APEXLOAD_FLEET_POOL_ID"] != request.FleetPoolID || operationID == "" {
				continue
			}
			if expectedOperationID != "" && operationID != expectedOperationID {
				continue
			}
			if len(allowedIntents) > 0 {
				nonce := strings.TrimSpace(env["APEXLOAD_LAUNCH_NONCE"])
				expectedGeneratorID, allowed := allowedIntents[nonce]
				if !allowed || expectedGeneratorID == "" || env["APEXLOAD_GENERATOR_ID"] != expectedGeneratorID {
					continue
				}
			}
			if strings.TrimSpace(execution.Name) == "" {
				continue
			}
			instance := execution.instance()
			if isTerminalAzureJobState(instance.State) {
				continue
			}
			instances = append(instances, instance)
		}
		next = strings.TrimSpace(response.NextLink)
	}
	if next != "" {
		return nil, errors.New("Azure Container Apps executions pagination exceeded 20 pages")
	}
	return instances, nil
}

type jobExecutionResponse struct {
	Name       string `json:"name"`
	Properties struct {
		Status   string `json:"status"`
		Template struct {
			Containers []struct {
				Name string `json:"name"`
				Env  []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"env"`
			} `json:"containers"`
		} `json:"template"`
	} `json:"properties"`
}

func (e jobExecutionResponse) environment() map[string]string {
	values := make(map[string]string)
	for _, container := range e.Properties.Template.Containers {
		for _, variable := range container.Env {
			values[strings.TrimSpace(variable.Name)] = variable.Value
		}
	}
	return values
}

func (e jobExecutionResponse) instance() compute.Instance {
	env := e.environment()
	identityAttested := strings.TrimSpace(env["APEXLOAD_GENERATOR_ID"]) != "" && strings.TrimSpace(env["APEXLOAD_LAUNCH_NONCE"]) != ""
	return compute.Instance{
		ProviderID: e.Name, GeneratorID: env["APEXLOAD_GENERATOR_ID"], State: e.Properties.Status,
		Healthy: strings.EqualFold(e.Properties.Status, "Running"), IdentityAttested: identityAttested,
		Tags: map[string]string{
			"apexload.io/controller-id":       env["APEXLOAD_CONTROLLER_ID"],
			"apexload.io/execution-target-id": env["APEXLOAD_EXECUTION_TARGET_ID"],
			"apexload.io/pool-id":             env["APEXLOAD_FLEET_POOL_ID"],
			"apexload.io/operation-id":        env["APEXLOAD_CAPACITY_OPERATION_ID"],
			"apexload.io/test-id":             env["APEXLOAD_TEST_ID"],
			"apexload.io/generator-id":        env["APEXLOAD_GENERATOR_ID"],
			"apexload.io/launch-nonce":        env["APEXLOAD_LAUNCH_NONCE"],
		},
	}
}

func isTerminalAzureJobState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "stopped", "failed", "succeeded":
		return true
	default:
		return false
	}
}

func pathBase(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if index := strings.LastIndexByte(value, '/'); index >= 0 {
		return value[index+1:]
	}
	return value
}

func first(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
func message(err error, success string) string {
	if err != nil {
		return err.Error()
	}
	return success
}

type armResourceResponse struct {
	Identity struct {
		PrincipalID            string `json:"principalId"`
		UserAssignedIdentities map[string]struct {
			PrincipalID string `json:"principalId"`
		} `json:"userAssignedIdentities"`
	} `json:"identity"`
	Properties struct {
		Template struct {
			Containers []struct {
				Name string `json:"name"`
			} `json:"containers"`
			Scale struct {
				Rules []json.RawMessage `json:"rules"`
			} `json:"scale"`
		} `json:"template"`
	} `json:"properties"`
}

func (r armResourceResponse) hasScaleRules() bool {
	return len(r.Properties.Template.Scale.Rules) > 0
}

func containerAppOwnershipProbe(err error, hasScaleRules bool, owner string) compute.ProbeResult {
	probe := compute.ProbeResult{Probe: "container_app_autoscaling_owner"}
	if err != nil {
		probe.Message = err.Error()
		return probe
	}
	if owner == storage.CapacityOwnerApex && hasScaleRules {
		probe.Message = "warm Container App has provider-side scale rules; set standing_owner=external or remove the rules before APEX Load scales replicas"
		return probe
	}
	probe.OK = true
	probe.Message = "standing capacity ownership does not conflict with provider-side scale rules"
	return probe
}

func (r armResourceResponse) principalIDs() []string {
	ids := make([]string, 0, 1+len(r.Identity.UserAssignedIdentities))
	if id := strings.TrimSpace(r.Identity.PrincipalID); id != "" {
		ids = append(ids, id)
	}
	for _, identity := range r.Identity.UserAssignedIdentities {
		if id := strings.TrimSpace(identity.PrincipalID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (r armResourceResponse) containerNames() []string {
	names := make([]string, 0, len(r.Properties.Template.Containers))
	for _, container := range r.Properties.Template.Containers {
		names = append(names, container.Name)
	}
	return names
}

func requiredSidecars(profiles []storage.RuntimeProfile) []string {
	seen := make(map[string]struct{})
	var required []string
	for _, profile := range profiles {
		for _, name := range profile.Sidecars {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			required = append(required, name)
		}
	}
	return required
}

func identityProbe(name string, resourceErr error, actual []string, expected string) compute.ProbeResult {
	err := resourceErr
	if err == nil && len(actual) == 0 {
		err = errors.New("resource has no managed identity principal")
	}
	if err == nil && strings.TrimSpace(expected) != "" {
		matched := false
		for _, principalID := range actual {
			if strings.EqualFold(strings.TrimSpace(principalID), strings.TrimSpace(expected)) {
				matched = true
				break
			}
		}
		if !matched {
			err = fmt.Errorf("resource principals %q do not include expected principal %q", actual, expected)
		}
	}
	return compute.ProbeResult{Probe: name, OK: err == nil, Message: message(err, "managed identity is attached and matches the registered principal")}
}

func sidecarProbe(name string, resourceErr error, required, actual []string) compute.ProbeResult {
	if resourceErr != nil {
		return compute.ProbeResult{Probe: name, Message: resourceErr.Error()}
	}
	if len(required) == 0 {
		return compute.ProbeResult{Probe: name, OK: true, Optional: true, Message: "runtime profile declares no required sidecars"}
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, candidate := range actual {
		actualSet[strings.TrimSpace(candidate)] = struct{}{}
	}
	var missing []string
	for _, requiredName := range required {
		if _, exists := actualSet[requiredName]; !exists {
			missing = append(missing, requiredName)
		}
	}
	if len(missing) > 0 {
		return compute.ProbeResult{Probe: name, Message: "missing required sidecars: " + strings.Join(missing, ", ")}
	}
	return compute.ProbeResult{Probe: name, OK: true, Message: "all runtime-profile sidecars are present"}
}
