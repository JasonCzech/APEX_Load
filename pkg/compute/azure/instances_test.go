package azure

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

type containerGroupAPI struct {
	calls      []call
	puts       int
	state      string
	ip         string
	tags       map[string]string
	containers []any
	listEmpty  bool
	listErr    error
}

func (a *containerGroupAPI) Do(_ context.Context, method, path string, body, output any) (string, time.Duration, error) {
	a.calls = append(a.calls, call{method: method, path: path, body: body})
	switch {
	case method == http.MethodPut:
		a.puts++
		return "", 0, nil
	case method == http.MethodDelete:
		return "", 0, nil
	case strings.Contains(path, "/containerGroups?"):
		if a.listErr != nil {
			return "", 0, a.listErr
		}
		groups := []any{a.group("apexload-gen-nonce-1")}
		if a.listEmpty {
			groups = []any{}
		}
		payload := map[string]any{"value": groups}
		encode(payload, output)
		return "", 0, nil
	default:
		encode(a.group(pathBase(strings.Split(path, "?")[0])), output)
		return "", 0, nil
	}
}

func (a *containerGroupAPI) group(name string) map[string]any {
	tags := a.tags
	if tags == nil {
		tags = map[string]string{
			"apexload.io/controller-id": "controller-1",
			"apexload.io/pool-id":       "pool-1",
			"apexload.io/generator-id":  "gen-1",
			"apexload.io/launch-nonce":  "nonce-1",
		}
	}
	state := a.state
	if state == "" {
		state = "Running"
	}
	ip := a.ip
	if ip == "" {
		ip = "10.0.0.4"
	}
	containers := a.containers
	if containers == nil {
		containers = []any{map[string]any{
			"name": "generator",
			"properties": map[string]any{
				"image":     "registry.invalid/apexload/generator@sha256:abc",
				"ports":     []any{map[string]any{"port": 9090, "protocol": "TCP"}},
				"resources": map[string]any{"requests": map[string]any{"cpu": 1.0, "memoryInGB": 2.0}},
				"environmentVariables": []any{
					map[string]any{"name": "APEXLOAD_ENV", "value": "production"},
					map[string]any{"name": "APEXLOAD_GENERATOR_ID", "value": "template-placeholder"},
				},
			},
		}}
	}
	return map[string]any{
		"name": name,
		"tags": tags,
		"identity": map[string]any{
			"type":                   "UserAssigned",
			"userAssignedIdentities": map[string]any{"/subscriptions/s/uai": map[string]any{"principalId": "principal-1"}},
		},
		"properties": map[string]any{
			"provisioningState":        "Succeeded",
			"containers":               containers,
			"imageRegistryCredentials": []any{map[string]any{"server": "registry.invalid", "identity": "/subscriptions/s/uai"}},
			"osType":                   "Linux",
			"restartPolicy":            "Never",
			"ipAddress":                map[string]any{"ip": ip, "type": "Private"},
			"instanceView":             map[string]any{"state": state},
		},
	}
}

func encode(payload any, output any) {
	if output == nil {
		return
	}
	data, _ := json.Marshal(payload)
	_ = json.Unmarshal(data, output)
}

func aciTarget(subnet string) *storage.ExecutionTarget {
	return &storage.ExecutionTarget{
		ID: "target-aci", Name: "ACI", Kind: storage.ExecutionTargetAzureContainerInstances,
		CloudConnectionID: "cloud-1", ConnectivityMode: storage.ConnectivityDirect,
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex,
		ArchiveMode: storage.ArchiveModeControllerPull, Region: "eastus",
		AzureContainerInstances: &storage.AzureContainerInstancesTargetSpec{
			ResourceGroup: "rg", ContainerGroupTemplate: "template", SubnetID: subnet,
		},
	}
}

// ACI has no replica count, so advertising scale would let a pool request warm
// capacity the provider can never hold.
func TestContainerInstancesNeverAdvertisesScale(t *testing.T) {
	backend := &InstancesBackend{API: &containerGroupAPI{}, Target: aciTarget("/subnets/a"), SubscriptionID: "sub", ControllerID: "controller-1"}
	info := backend.Describe()
	if info.CanScale {
		t.Fatal("container instances must never report CanScale")
	}
	if !info.CanLaunch {
		t.Fatal("a configured template must be launchable")
	}
	if !info.CanReachDirectly {
		t.Fatal("a delegated subnet must enable direct reachability")
	}
}

// Without a delegated subnet the group gets a public address, so the control
// port would be internet-reachable; direct mode must not claim otherwise.
func TestContainerInstancesWithoutSubnetIsNotDirectlyReachable(t *testing.T) {
	backend := &InstancesBackend{API: &containerGroupAPI{}, Target: aciTarget(""), SubscriptionID: "sub", ControllerID: "controller-1"}
	if backend.Describe().CanReachDirectly {
		t.Fatal("a public container group must not report direct reachability")
	}
	target := aciTarget("")
	if err := target.Validate(); err == nil {
		t.Fatal("direct connectivity without a delegated subnet must be refused")
	}
	// Reverse mode does not dial in, so it stays valid without a subnet.
	target.ConnectivityMode = storage.ConnectivityReverse
	if err := target.Validate(); err != nil {
		t.Fatalf("reverse connectivity without a subnet must remain valid: %v", err)
	}
}

func TestContainerInstancesWarmFloorIsAWarningNotAFailure(t *testing.T) {
	backend := &InstancesBackend{API: &containerGroupAPI{}, Target: aciTarget("/subnets/a"), SubscriptionID: "sub", ControllerID: "controller-1"}
	plan, err := backend.Plan(context.Background(), compute.CapacityRequest{Desired: 2, WarmFloor: 1})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Launch != 2 || plan.Standing != 0 {
		t.Fatalf("launch=%d standing=%d, want every instance launched per run", plan.Launch, plan.Standing)
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("a warm floor on ACI must warn that it cannot be held")
	}
}

func TestContainerInstancesRefusesLaunchWhenBurstIsNotApexOwned(t *testing.T) {
	target := aciTarget("/subnets/a")
	target.BurstOwner = storage.CapacityOwnerDisabled
	backend := &InstancesBackend{API: &containerGroupAPI{}, Target: target, SubscriptionID: "sub", ControllerID: "controller-1"}
	_, err := backend.Plan(context.Background(), compute.CapacityRequest{Desired: 1})
	if !errors.Is(err, compute.ErrOwnershipConflict) {
		t.Fatalf("err = %v, want ErrOwnershipConflict", err)
	}
}

// A lost PUT response must not double-create. The group name is derived from the
// launch nonce, so discovery before launch recognizes the existing group.
func TestContainerInstancesDoesNotRelaunchAnAlreadyCreatedGroup(t *testing.T) {
	api := &containerGroupAPI{}
	backend := &InstancesBackend{API: api, Target: aciTarget("/subnets/a"), SubscriptionID: "sub", ControllerID: "controller-1"}
	_, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		Desired: 1, ExecutionTargetID: "target-aci", FleetPoolID: "pool-1",
		LaunchIntents: []compute.LaunchIntent{{GeneratorID: "gen-1", LaunchNonce: "nonce-1", ClientToken: "token-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if api.puts != 0 {
		t.Fatalf("PUT count = %d, want 0 because discovery already found the group for this launch nonce", api.puts)
	}
}

func TestContainerInstancesDeprovisionRequiresTerminalArchive(t *testing.T) {
	api := &containerGroupAPI{}
	backend := &InstancesBackend{API: api, Target: aciTarget("/subnets/a"), SubscriptionID: "sub", ControllerID: "controller-1"}
	err := backend.Deprovision(context.Background(), compute.CapacityRequest{}, []compute.Instance{{ProviderID: "g1", ArchiveTerminal: false}})
	if !errors.Is(err, compute.ErrArchiveNotDrained) {
		t.Fatalf("err = %v, want ErrArchiveNotDrained", err)
	}
	for _, recorded := range api.calls {
		if recorded.method == http.MethodDelete {
			t.Fatal("a group was deleted before its archive reached durable storage")
		}
	}
}

// Discover feeds orphan termination, so a group this controller does not own
// must never be returned.
func TestContainerInstancesDiscoverExcludesForeignGroups(t *testing.T) {
	api := &containerGroupAPI{tags: map[string]string{
		"apexload.io/controller-id": "another-controller",
		"apexload.io/pool-id":       "pool-1",
	}}
	backend := &InstancesBackend{API: api, Target: aciTarget("/subnets/a"), SubscriptionID: "sub", ControllerID: "controller-1"}
	instances, err := backend.Discover(context.Background(), compute.CapacityRequest{FleetPoolID: "pool-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Fatalf("instances = %d, want 0 for a group owned by another controller", len(instances))
	}
}

func TestContainerInstancesAddressUsesTheGroupIP(t *testing.T) {
	backend := &InstancesBackend{API: &containerGroupAPI{ip: "10.1.2.3"}, Target: aciTarget("/subnets/a"), SubscriptionID: "sub", ControllerID: "controller-1", GeneratorPort: 9090}
	instances, err := backend.Discover(context.Background(), compute.CapacityRequest{FleetPoolID: "pool-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Address != "10.1.2.3:9090" {
		t.Fatalf("instances = %#v, want the group's own IP as the dial address", instances)
	}
	if !instances[0].IdentityAttested {
		t.Fatal("a group tagged with a controller-issued generator id and launch nonce must be identity-attested")
	}
}

func TestContainerGroupNameIsDeterministicAndARMLegal(t *testing.T) {
	name := containerGroupName(compute.LaunchIntent{LaunchNonce: "Nonce_With/Illegal Chars"})
	if name != containerGroupName(compute.LaunchIntent{LaunchNonce: "Nonce_With/Illegal Chars"}) {
		t.Fatal("the group name must be deterministic for the same launch nonce")
	}
	if len(name) > 63 {
		t.Fatalf("name %q exceeds the ARM 63-character limit", name)
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			t.Fatalf("name %q contains an ARM-illegal rune %q", name, r)
		}
	}
}

// containerFromPut digs the launch body's containers out of a recorded PUT.
func containerFromPut(t *testing.T, recorded call, name string) map[string]any {
	t.Helper()
	body, ok := recorded.body.(map[string]any)
	if !ok {
		t.Fatalf("PUT body is %T, want a map", recorded.body)
	}
	properties, ok := body["properties"].(map[string]any)
	if !ok {
		t.Fatal("PUT body has no properties")
	}
	containers, ok := properties["containers"].([]any)
	if !ok {
		t.Fatal("PUT body has no containers")
	}
	for _, entry := range containers {
		container, _ := entry.(map[string]any)
		if container["name"] == name {
			inner, _ := container["properties"].(map[string]any)
			return inner
		}
	}
	t.Fatalf("PUT body has no container named %q", name)
	return nil
}

// ARM rejects a container group whose containers carry no image or resource
// requests, so a launch that does not clone the template fails outright.
func TestContainerInstancesLaunchClonesTemplateImageAndResources(t *testing.T) {
	api := &containerGroupAPI{listEmpty: true}
	backend := &InstancesBackend{API: api, Target: aciTarget("/subnets/a"), SubscriptionID: "sub", ControllerID: "controller-1"}
	if _, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		Desired: 1, ExecutionTargetID: "target-aci", FleetPoolID: "pool-1", OperationID: "op-1",
		LaunchIntents: []compute.LaunchIntent{{GeneratorID: "gen-1", LaunchNonce: "nonce-1"}},
	}); err != nil {
		t.Fatal(err)
	}
	if api.puts != 1 {
		t.Fatalf("PUT count = %d, want 1", api.puts)
	}
	var put call
	for _, recorded := range api.calls {
		if recorded.method == http.MethodPut {
			put = recorded
		}
	}
	generator := containerFromPut(t, put, "generator")
	if generator["image"] != "registry.invalid/apexload/generator@sha256:abc" {
		t.Fatalf("image = %v, want the template's image", generator["image"])
	}
	if generator["resources"] == nil {
		t.Fatal("resources.requests is required by ARM and was not cloned from the template")
	}
	body := put.body.(map[string]any)
	if body["identity"] == nil {
		t.Fatal("identity was not cloned; the generator could not pull its image or reach Key Vault")
	}
	properties := body["properties"].(map[string]any)
	if properties["imageRegistryCredentials"] == nil {
		t.Fatal("registry credentials were not cloned; the image pull would fail")
	}
}

// The run identity must land on the generator, not on a sidecar, or the
// generator enrolls anonymously while the sidecar tries to claim its identity.
func TestContainerInstancesInjectsIdentityIntoGeneratorContainerOnly(t *testing.T) {
	container := func(name string) any {
		return map[string]any{
			"name": name,
			"properties": map[string]any{
				"image":                "registry.invalid/" + name + "@sha256:abc",
				"resources":            map[string]any{"requests": map[string]any{"cpu": 1.0, "memoryInGB": 2.0}},
				"environmentVariables": []any{map[string]any{"name": "KEEP_ME", "value": "yes"}},
			},
		}
	}
	api := &containerGroupAPI{listEmpty: true, containers: []any{container("java-vuser-runner"), container("generator")}}
	backend := &InstancesBackend{API: api, Target: aciTarget("/subnets/a"), SubscriptionID: "sub", ControllerID: "controller-1"}
	if _, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		Desired: 1, ExecutionTargetID: "target-aci", FleetPoolID: "pool-1", OperationID: "op-1",
		LaunchIntents: []compute.LaunchIntent{{GeneratorID: "gen-1", LaunchNonce: "nonce-1"}},
	}); err != nil {
		t.Fatal(err)
	}
	var put call
	for _, recorded := range api.calls {
		if recorded.method == http.MethodPut {
			put = recorded
		}
	}
	envNames := func(container map[string]any) map[string]any {
		out := map[string]any{}
		entries, _ := container["environmentVariables"].([]map[string]any)
		for _, entry := range entries {
			name, _ := entry["name"].(string)
			out[name] = entry["value"]
		}
		return out
	}
	generator := envNames(containerFromPut(t, put, "generator"))
	if generator["APEXLOAD_GENERATOR_ID"] != "gen-1" {
		t.Fatalf("generator env = %v, want the launch identity injected", generator)
	}
	if generator["KEEP_ME"] != "yes" {
		t.Fatal("template environment was dropped instead of merged")
	}
	sidecar := envNames(containerFromPut(t, put, "java-vuser-runner"))
	if _, leaked := sidecar["APEXLOAD_GENERATOR_ID"]; leaked {
		t.Fatal("launch identity leaked into the java-vuser-runner sidecar")
	}
	if sidecar["KEEP_ME"] != "yes" {
		t.Fatal("sidecar environment was not preserved")
	}
}

// Discover feeds both the launch fence and orphan reaping, so an operation ID
// must scope it while an empty one still enumerates the whole pool.
func TestContainerInstancesDiscoverScopesByOperationID(t *testing.T) {
	api := &containerGroupAPI{tags: map[string]string{
		"apexload.io/controller-id": "controller-1",
		"apexload.io/pool-id":       "pool-1",
		"apexload.io/operation-id":  "op-1",
		"apexload.io/generator-id":  "gen-1",
		"apexload.io/launch-nonce":  "nonce-1",
	}}
	backend := &InstancesBackend{API: api, Target: aciTarget("/subnets/a"), SubscriptionID: "sub", ControllerID: "controller-1"}

	foreign, err := backend.Discover(context.Background(), compute.CapacityRequest{FleetPoolID: "pool-1", OperationID: "op-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(foreign) != 0 {
		t.Fatal("another operation's container group was counted toward this operation")
	}
	all, err := backend.Discover(context.Background(), compute.CapacityRequest{FleetPoolID: "pool-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatal("an empty operation ID must enumerate every controller-owned group so orphan reaping still works")
	}
}

func TestContainerInstancesRequireALocation(t *testing.T) {
	target := aciTarget("/subnets/a")
	target.Region = ""
	target.AzureContainerInstances.Location = ""
	if err := target.Validate(); err == nil {
		t.Fatal("a target with no location and no region must be refused at save time, not at first launch")
	}
}
