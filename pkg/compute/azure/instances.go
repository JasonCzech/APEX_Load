package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

// containerInstanceAPIVersion is pinned separately from the Microsoft.App
// version: ACI is a different resource provider with its own release cadence.
const containerInstanceAPIVersion = "2023-05-01"

// generatorContainerName is resolved by name rather than index because an
// operator template may run bruno or java-vuser-runner sidecars beside it.
const generatorContainerName = "generator"

// InstancesBackend drives Azure Container Instances.
//
// ACI is burst-only: a container group is a single instance with its own IP,
// created and deleted per run. There is no replica count, so Scale has no
// meaning here and Plan reports CanScale=false rather than pretending.
//
// Its reason to exist beside Container Apps is that per-group IP. Container Apps
// replicas sit behind one load-balanced ingress and can only be reached through
// the reverse tunnel; an ACI group on a delegated subnet can be dialed directly,
// which is the escape hatch for operators who cannot run the tunnel.
type InstancesBackend struct {
	API            API
	Target         *storage.ExecutionTarget
	SubscriptionID string
	ControllerID   string
	// GeneratorPort is the generator's gRPC control port. Zero means 9090.
	GeneratorPort int
}

func (b *InstancesBackend) Describe() compute.BackendInfo {
	info := compute.BackendInfo{
		Name:            storage.ExecutionTargetAzureContainerInstances,
		SupportsReverse: true,
		Details:         map[string]string{"capacity_model": "container_group_per_instance"},
	}
	if b == nil || b.Target == nil || b.Target.AzureContainerInstances == nil {
		return info
	}
	// A container group is one instance; there is nothing to scale, so standing
	// capacity is never available regardless of StandingOwner.
	info.CanScale = false
	info.CanLaunch = b.Target.BurstOwner == storage.CapacityOwnerApex && b.Target.AzureContainerInstances.ContainerGroupTemplate != ""
	// Direct dialing only works on a delegated subnet; a public group would
	// expose the generator control port to the internet.
	info.CanReachDirectly = strings.TrimSpace(b.Target.AzureContainerInstances.SubnetID) != ""
	for _, profile := range b.Target.RuntimeProfiles {
		if profile.CPUMillis > 0 && profile.MemoryMiB > 0 {
			info.ResourceShapes = append(info.ResourceShapes, fmt.Sprintf("%dm/%dMi", profile.CPUMillis, profile.MemoryMiB))
		}
	}
	return info
}

func (b *InstancesBackend) groupPath(name string) string {
	spec := b.Target.AzureContainerInstances
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerInstance/containerGroups/%s?api-version=%s",
		url.PathEscape(b.SubscriptionID), url.PathEscape(spec.ResourceGroup), url.PathEscape(name), containerInstanceAPIVersion)
}

func (b *InstancesBackend) listPath() string {
	spec := b.Target.AzureContainerInstances
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerInstance/containerGroups?api-version=%s",
		url.PathEscape(b.SubscriptionID), url.PathEscape(spec.ResourceGroup), containerInstanceAPIVersion)
}

func (b *InstancesBackend) Validate(ctx context.Context) []compute.ProbeResult {
	if b == nil || b.API == nil || b.Target == nil || b.Target.AzureContainerInstances == nil || strings.TrimSpace(b.SubscriptionID) == "" {
		return []compute.ProbeResult{{Probe: "configuration", Message: "Azure Container Instances backend is incomplete"}}
	}
	spec := b.Target.AzureContainerInstances
	var response containerGroupResponse
	_, _, err := b.API.Do(ctx, http.MethodGet, b.groupPath(spec.ContainerGroupTemplate), nil, &response)
	probes := []compute.ProbeResult{{
		Probe: "container_group_template", OK: err == nil,
		Message: message(err, "container group template is accessible"),
	}}
	probes = append(probes, identityProbe("container_group_identity", err, response.principalIDs(), spec.ExpectedPrincipalID))
	probes = append(probes, sidecarProbe("container_group_sidecars", err, requiredSidecars(b.Target.RuntimeProfiles), response.containerNames()))
	if strings.TrimSpace(spec.SubnetID) == "" {
		probes = append(probes, compute.ProbeResult{
			Probe: "network", Optional: b.Target.ConnectivityMode == storage.ConnectivityReverse,
			Message: "no delegated subnet is configured; container groups will receive public addresses and cannot be dialed directly",
		})
	} else {
		probes = append(probes, compute.ProbeResult{Probe: "network", OK: true, Message: "container groups are placed on a delegated subnet"})
	}
	return probes
}

func (b *InstancesBackend) Plan(_ context.Context, request compute.CapacityRequest) (compute.Plan, error) {
	if request.Desired < 0 {
		return compute.Plan{}, errors.New("desired capacity must not be negative")
	}
	info := b.Describe()
	plan := compute.Plan{Requested: request.Desired}
	if request.WarmFloor > 0 {
		// Surfacing this as a warning rather than an error keeps a pool that
		// declares a warm floor usable on ACI; it simply gets no warm capacity.
		plan.Warnings = append(plan.Warnings, "Azure Container Instances cannot hold a warm floor; every instance is created per run")
	}
	plan.Launch = request.Desired
	if plan.Launch > 0 && !info.CanLaunch {
		if b.Target.BurstOwner != storage.CapacityOwnerApex {
			return compute.Plan{}, fmt.Errorf("%w: burst capacity on this target is not owned by Apex", compute.ErrOwnershipConflict)
		}
		return compute.Plan{}, fmt.Errorf("%w: no container group template is configured", compute.ErrUnsupportedOperation)
	}
	return plan, nil
}

func (b *InstancesBackend) Reconcile(ctx context.Context, request compute.CapacityRequest) (compute.Observation, error) {
	plan, err := b.Plan(ctx, request)
	if err != nil {
		return compute.Observation{}, err
	}
	spec := b.Target.AzureContainerInstances
	observed := append([]string(nil), request.ProviderOperationIDs...)
	covered := make(map[string]struct{}, len(observed))

	var template containerGroupResponse
	if plan.Launch > 0 {
		// ACI group names ARE caller-selected, so unlike Container Apps Jobs the
		// deterministic name is itself the idempotency fence: a repeated PUT with
		// the same name updates the existing group instead of creating a second.
		discovered, discoverErr := b.Discover(ctx, request)
		if discoverErr != nil {
			return compute.Observation{}, fmt.Errorf("discover Azure container groups before launch: %w", discoverErr)
		}
		for _, instance := range discovered {
			// An untagged group must not register the empty string as a covered
			// nonce, or a launch intent carrying no nonce would be skipped.
			if nonce := strings.TrimSpace(instance.Tags["apexload.io/launch-nonce"]); nonce != "" {
				covered[nonce] = struct{}{}
			}
			if !containsString(observed, instance.ProviderID) {
				observed = append(observed, instance.ProviderID)
			}
		}
		// The template carries image, resources, identity, and registry
		// credentials. ARM requires image and resources on every container, so a
		// launch built without reading it is rejected outright.
		if _, _, err := b.API.Do(ctx, http.MethodGet, b.groupPath(spec.ContainerGroupTemplate), nil, &template); err != nil {
			return compute.Observation{}, fmt.Errorf("read Azure container group template %q: %w", spec.ContainerGroupTemplate, err)
		}
	}

	for launchIndex := 0; launchIndex < plan.Launch && launchIndex < len(request.LaunchIntents); launchIndex++ {
		intent := request.LaunchIntents[launchIndex]
		if _, done := covered[intent.LaunchNonce]; done {
			continue
		}
		name := containerGroupName(intent)
		containers, err := b.cloneContainers(template, request, intent)
		if err != nil {
			return compute.Observation{}, err
		}
		properties := map[string]any{
			"containers":    containers,
			"osType":        first(template.Properties.OSType, "Linux"),
			"restartPolicy": first(template.Properties.RestartPolicy, "Never"),
		}
		if len(template.Properties.ImageRegistryCredentials) > 0 {
			properties["imageRegistryCredentials"] = template.Properties.ImageRegistryCredentials
		}
		if len(template.Properties.Volumes) > 0 {
			properties["volumes"] = template.Properties.Volumes
		}
		if strings.TrimSpace(template.Properties.SKU) != "" {
			properties["sku"] = template.Properties.SKU
		}
		if len(template.Properties.Diagnostics) > 0 {
			properties["diagnostics"] = template.Properties.Diagnostics
		}
		body := map[string]any{
			"location":   first(spec.Location, b.Target.Region),
			"properties": properties,
			"tags": map[string]string{
				"apexload.io/controller-id":  b.ControllerID,
				"apexload.io/pool-id":        request.FleetPoolID,
				"apexload.io/operation-id":   request.OperationID,
				"apexload.io/test-id":        request.TestID,
				"apexload.io/generator-id":   intent.GeneratorID,
				"apexload.io/launch-nonce":   intent.LaunchNonce,
				"apexload.io/group-template": spec.ContainerGroupTemplate,
			},
		}
		// The identity is what lets the generator pull from ACR and resolve
		// {{secret.*}} from Key Vault, so it is carried over verbatim.
		if strings.TrimSpace(template.Identity.Type) != "" {
			identity := map[string]any{"type": template.Identity.Type}
			if len(template.Identity.UserAssignedIdentities) > 0 {
				// Assignment values are read-only projections; ARM wants the
				// resource IDs as keys with empty objects.
				assigned := make(map[string]any, len(template.Identity.UserAssignedIdentities))
				for resourceID := range template.Identity.UserAssignedIdentities {
					assigned[resourceID] = map[string]any{}
				}
				identity["userAssignedIdentities"] = assigned
			}
			body["identity"] = identity
		}
		if subnet := strings.TrimSpace(spec.SubnetID); subnet != "" {
			properties["subnetIds"] = []any{map[string]string{"id": subnet}}
			properties["ipAddress"] = map[string]any{"type": "Private", "ports": b.exposedPorts(template)}
		}
		if _, _, err := b.API.Do(ctx, http.MethodPut, b.groupPath(name), body, nil); err != nil {
			return compute.Observation{}, err
		}
		observed = append(observed, name)
	}

	instances, err := b.describeGroups(ctx, observed)
	if err != nil {
		return compute.Observation{}, err
	}
	observation := compute.Observation{Requested: request.Desired, Instances: instances, Observed: len(instances)}
	for _, instance := range instances {
		switch {
		case instance.Healthy:
			observation.Ready++
		case isTerminalContainerGroupState(instance.State):
			observation.Failed++
		default:
			observation.Pending++
		}
	}
	observation.Terminal = observation.Pending == 0
	if !observation.Terminal {
		observation.RetryAfter = 5 * time.Second
	}
	return observation, nil
}

// cloneContainers rebuilds the template's containers for one launch, injecting
// the run's identity into the generator container and leaving sidecars intact.
//
// The generator container is selected BY NAME, never by index: an operator's
// template may run bruno or java-vuser-runner beside the generator, and
// injecting the run identity into a sidecar would leave the generator
// unidentifiable while the sidecar tried to enroll.
func (b *InstancesBackend) cloneContainers(template containerGroupResponse, request compute.CapacityRequest, intent compute.LaunchIntent) ([]any, error) {
	if len(template.Properties.Containers) == 0 {
		return nil, fmt.Errorf("container group template %q declares no containers", b.Target.AzureContainerInstances.ContainerGroupTemplate)
	}
	generatorIndex := -1
	for index, container := range template.Properties.Containers {
		if container.Name == generatorContainerName {
			generatorIndex = index
			break
		}
	}
	if generatorIndex < 0 {
		if len(template.Properties.Containers) != 1 {
			names := make([]string, 0, len(template.Properties.Containers))
			for _, container := range template.Properties.Containers {
				names = append(names, container.Name)
			}
			return nil, fmt.Errorf("container group template has no %q container and is not single-container (%s)", generatorContainerName, strings.Join(names, ", "))
		}
		generatorIndex = 0
	}

	profile, err := b.runtimeProfile(request.RuntimeProfileID)
	if err != nil {
		return nil, err
	}

	containers := make([]any, 0, len(template.Properties.Containers))
	for index, container := range template.Properties.Containers {
		if strings.TrimSpace(container.Properties.Image) == "" {
			return nil, fmt.Errorf("container %q in template %q has no image", container.Name, b.Target.AzureContainerInstances.ContainerGroupTemplate)
		}
		properties := map[string]any{"image": container.Properties.Image}
		if len(container.Properties.Command) > 0 {
			properties["command"] = container.Properties.Command
		}
		if len(container.Properties.Ports) > 0 {
			properties["ports"] = container.Properties.Ports
		}
		if len(container.Properties.VolumeMounts) > 0 {
			properties["volumeMounts"] = container.Properties.VolumeMounts
		}
		resources := container.Properties.Resources
		environment := container.Properties.EnvironmentVariables
		if index == generatorIndex {
			environment = mergeEnvironment(environment, b.launchEnvironment(request, intent))
			if applied := applyRuntimeProfileResources(resources, profile); applied != nil {
				resources = applied
			}
		}
		if len(resources) == 0 {
			// ARM requires resources.requests on every container; a template
			// missing it would fail at PUT with an opaque 400.
			return nil, fmt.Errorf("container %q in template %q declares no resource requests", container.Name, b.Target.AzureContainerInstances.ContainerGroupTemplate)
		}
		properties["resources"] = resources
		if len(environment) > 0 {
			properties["environmentVariables"] = environment
		}
		containers = append(containers, map[string]any{"name": container.Name, "properties": properties})
	}
	return containers, nil
}

func (b *InstancesBackend) launchEnvironment(request compute.CapacityRequest, intent compute.LaunchIntent) []map[string]any {
	return []map[string]any{
		{"name": "APEXLOAD_GENERATOR_ID", "value": intent.GeneratorID},
		{"name": "APEXLOAD_LAUNCH_NONCE", "value": intent.LaunchNonce},
		{"name": "APEXLOAD_EXECUTION_TARGET_ID", "value": request.ExecutionTargetID},
		{"name": "APEXLOAD_CONTROLLER_ID", "value": b.ControllerID},
		{"name": "APEXLOAD_FLEET_POOL_ID", "value": request.FleetPoolID},
		{"name": "APEXLOAD_CAPACITY_OPERATION_ID", "value": request.OperationID},
		{"name": "APEXLOAD_TEST_ID", "value": request.TestID},
		{"name": "APEXLOAD_GENERATOR_LIFECYCLE", "value": "burst"},
		{"name": "APEXLOAD_GENERATOR_CONNECTIVITY_MODE", "value": b.Target.ConnectivityMode},
	}
}

// mergeEnvironment overlays the run's variables on the template's, replacing by
// name so a template default never shadows a launch identity.
func mergeEnvironment(base, overlay []map[string]any) []map[string]any {
	merged := make([]map[string]any, 0, len(base)+len(overlay))
	overridden := make(map[string]struct{}, len(overlay))
	for _, entry := range overlay {
		if name, _ := entry["name"].(string); name != "" {
			overridden[name] = struct{}{}
		}
	}
	for _, entry := range base {
		name, _ := entry["name"].(string)
		if _, replaced := overridden[name]; replaced {
			continue
		}
		merged = append(merged, entry)
	}
	return append(merged, overlay...)
}

// applyRuntimeProfileResources overrides the template's CPU/memory with the
// pool's runtime profile. It returns nil when the profile declares neither, so
// the template's own sizing stands.
func applyRuntimeProfileResources(resources map[string]any, profile *storage.RuntimeProfile) map[string]any {
	if profile == nil || (profile.CPUMillis <= 0 && profile.MemoryMiB <= 0) {
		return nil
	}
	requests := map[string]any{}
	if existing, ok := resources["requests"].(map[string]any); ok {
		for key, value := range existing {
			requests[key] = value
		}
	}
	if profile.CPUMillis > 0 {
		requests["cpu"] = float64(profile.CPUMillis) / 1000
	}
	if profile.MemoryMiB > 0 {
		requests["memoryInGB"] = float64(profile.MemoryMiB) / 1024
	}
	applied := map[string]any{}
	for key, value := range resources {
		applied[key] = value
	}
	applied["requests"] = requests
	return applied
}

// exposedPorts mirrors the generator container's ports onto the group address so
// a privately-addressed group is reachable on the control port.
func (b *InstancesBackend) exposedPorts(template containerGroupResponse) []map[string]any {
	for _, container := range template.Properties.Containers {
		if container.Name != generatorContainerName && len(template.Properties.Containers) != 1 {
			continue
		}
		if len(container.Properties.Ports) > 0 {
			return container.Properties.Ports
		}
	}
	return []map[string]any{{"port": b.generatorPort(), "protocol": "TCP"}}
}

func (b *InstancesBackend) runtimeProfile(id string) (*storage.RuntimeProfile, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	for index := range b.Target.RuntimeProfiles {
		if b.Target.RuntimeProfiles[index].ID == id {
			return &b.Target.RuntimeProfiles[index], nil
		}
	}
	return nil, fmt.Errorf("runtime profile %q is not registered on execution target %q", id, b.Target.ID)
}

func (b *InstancesBackend) Cancel(ctx context.Context, request compute.CapacityRequest) error {
	instances, err := b.describeGroups(ctx, request.ProviderOperationIDs)
	if err != nil {
		return err
	}
	// A cancelled launch never ran a test, so no controller-pulled archive can
	// exist on it. Deprovision still fences on the flag, so set it explicitly
	// rather than letting the fence read an unset zero value.
	for i := range instances {
		instances[i].ArchiveTerminal = true
	}
	return b.Deprovision(ctx, request, instances)
}

func (b *InstancesBackend) Drain(context.Context, compute.CapacityRequest, []compute.Instance) error {
	return nil
}

func (b *InstancesBackend) Deprovision(ctx context.Context, _ compute.CapacityRequest, instances []compute.Instance) error {
	if err := compute.EnsureArchiveTerminal(instances); err != nil {
		return err
	}
	for _, instance := range instances {
		if _, _, err := b.API.Do(ctx, http.MethodDelete, b.groupPath(instance.ProviderID), nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func (b *InstancesBackend) Discover(ctx context.Context, request compute.CapacityRequest) ([]compute.Instance, error) {
	if b == nil || b.API == nil || strings.TrimSpace(b.ControllerID) == "" || strings.TrimSpace(request.FleetPoolID) == "" {
		return nil, nil
	}
	var response struct {
		Value []containerGroupResponse `json:"value"`
	}
	if _, _, err := b.API.Do(ctx, http.MethodGet, b.listPath(), nil, &response); err != nil {
		return nil, err
	}
	// A non-empty operation ID scopes discovery to that operation's own groups;
	// an empty one deliberately asks for every controller-owned group in the
	// pool, which is what orphan reaping needs. This mirrors the AWS backend's
	// startedBy scoping and the contract documented at the reconcile call site.
	operationID := strings.TrimSpace(request.OperationID)
	instances := []compute.Instance{}
	for _, group := range response.Value {
		// Ownership is asserted from tags before anything is returned: Discover
		// feeds orphan termination, and a group this controller does not own must
		// never reach that path.
		if group.Tags["apexload.io/controller-id"] != b.ControllerID || group.Tags["apexload.io/pool-id"] != request.FleetPoolID {
			continue
		}
		if operationID != "" && group.Tags["apexload.io/operation-id"] != operationID {
			continue
		}
		instance := group.instance(b.generatorPort())
		if isTerminalContainerGroupState(instance.State) {
			continue
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

func (b *InstancesBackend) Adopt(ctx context.Context, request compute.AdoptionRequest) (compute.Instance, error) {
	if b == nil || b.Target == nil || strings.TrimSpace(request.ProviderID) == "" ||
		strings.TrimSpace(request.ExecutionTargetID) == "" || strings.TrimSpace(request.FleetPoolID) == "" {
		return compute.Instance{}, errors.New("provider, execution-target, and fleet-pool identities are required for adoption")
	}
	var group containerGroupResponse
	if _, _, err := b.API.Do(ctx, http.MethodGet, b.groupPath(request.ProviderID), nil, &group); err != nil {
		return compute.Instance{}, err
	}
	if group.Tags["apexload.io/controller-id"] != b.ControllerID || group.Tags["apexload.io/pool-id"] != request.FleetPoolID {
		return compute.Instance{}, errors.New("container group is not owned by this controller and fleet pool")
	}
	return group.instance(b.generatorPort()), nil
}

func (b *InstancesBackend) generatorPort() int {
	if b != nil && b.GeneratorPort > 0 {
		return b.GeneratorPort
	}
	return 9090
}

func (b *InstancesBackend) describeGroups(ctx context.Context, names []string) ([]compute.Instance, error) {
	instances := make([]compute.Instance, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		var group containerGroupResponse
		if _, _, err := b.API.Do(ctx, http.MethodGet, b.groupPath(name), nil, &group); err != nil {
			return nil, err
		}
		instances = append(instances, group.instance(b.generatorPort()))
	}
	return instances, nil
}

// containerGroupName derives a deterministic, ARM-legal group name from the
// launch nonce. ARM allows [a-z0-9-] up to 63 characters, so the nonce is
// lowercased and bounded rather than passed through.
func containerGroupName(intent compute.LaunchIntent) string {
	nonce := strings.ToLower(strings.TrimSpace(intent.LaunchNonce))
	cleaned := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, nonce)
	cleaned = strings.Trim(cleaned, "-")
	const prefix = "apexload-gen-"
	if len(cleaned) > 63-len(prefix) {
		cleaned = cleaned[:63-len(prefix)]
	}
	return prefix + strings.Trim(cleaned, "-")
}

// containerGroupIdentity is preserved verbatim when cloning: the generator uses
// it to reach Key Vault and the registry, so dropping it would break secret
// resolution and image pull.
type containerGroupIdentity struct {
	Type                   string         `json:"type,omitempty"`
	PrincipalID            string         `json:"principalId,omitempty"`
	UserAssignedIdentities map[string]any `json:"userAssignedIdentities,omitempty"`
}

type containerGroupContainer struct {
	Name       string `json:"name"`
	Properties struct {
		Image                string           `json:"image,omitempty"`
		Command              []string         `json:"command,omitempty"`
		Ports                []map[string]any `json:"ports,omitempty"`
		EnvironmentVariables []map[string]any `json:"environmentVariables,omitempty"`
		Resources            map[string]any   `json:"resources,omitempty"`
		VolumeMounts         []map[string]any `json:"volumeMounts,omitempty"`
	} `json:"properties"`
}

type containerGroupResponse struct {
	Name       string                 `json:"name"`
	Tags       map[string]string      `json:"tags"`
	Identity   containerGroupIdentity `json:"identity"`
	Properties struct {
		ProvisioningState        string                    `json:"provisioningState"`
		Containers               []containerGroupContainer `json:"containers"`
		ImageRegistryCredentials []map[string]any          `json:"imageRegistryCredentials,omitempty"`
		Volumes                  []map[string]any          `json:"volumes,omitempty"`
		OSType                   string                    `json:"osType,omitempty"`
		RestartPolicy            string                    `json:"restartPolicy,omitempty"`
		SKU                      string                    `json:"sku,omitempty"`
		Diagnostics              map[string]any            `json:"diagnostics,omitempty"`
		IPAddress                struct {
			IP    string           `json:"ip"`
			Type  string           `json:"type"`
			Ports []map[string]any `json:"ports,omitempty"`
		} `json:"ipAddress"`
		InstanceView struct {
			State string `json:"state"`
		} `json:"instanceView"`
	} `json:"properties"`
}

func (g containerGroupResponse) principalIDs() []string {
	ids := []string{}
	if strings.TrimSpace(g.Identity.PrincipalID) != "" {
		ids = append(ids, g.Identity.PrincipalID)
	}
	for _, assigned := range g.Identity.UserAssignedIdentities {
		values, ok := assigned.(map[string]any)
		if !ok {
			continue
		}
		if principal, _ := values["principalId"].(string); strings.TrimSpace(principal) != "" {
			ids = append(ids, principal)
		}
	}
	return ids
}

func (g containerGroupResponse) containerNames() []string {
	names := make([]string, 0, len(g.Properties.Containers))
	for _, container := range g.Properties.Containers {
		names = append(names, container.Name)
	}
	return names
}

func (g containerGroupResponse) instance(port int) compute.Instance {
	state := first(g.Properties.InstanceView.State, g.Properties.ProvisioningState)
	instance := compute.Instance{
		ProviderID:  g.Name,
		GeneratorID: g.Tags["apexload.io/generator-id"],
		State:       state,
		Healthy:     strings.EqualFold(state, "Running") || strings.EqualFold(state, "Succeeded"),
		Tags:        g.Tags,
		// A launch nonce present in the provider's own tags is what proves this
		// group came from a controller-issued intent rather than being created
		// out of band.
		IdentityAttested: g.Tags["apexload.io/generator-id"] != "" && g.Tags["apexload.io/launch-nonce"] != "",
	}
	if ip := strings.TrimSpace(g.Properties.IPAddress.IP); ip != "" {
		instance.Address = fmt.Sprintf("%s:%d", ip, port)
	}
	return instance
}

func isTerminalContainerGroupState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "succeeded", "failed", "terminated", "stopped", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
