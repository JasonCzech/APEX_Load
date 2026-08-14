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

type call struct {
	method, path string
	body         any
}
type fakeAPI struct {
	calls  []call
	starts int
}

type validationAPI struct {
	principal     string
	userPrincipal string
	containers    []string
	scaleRules    int
}

type longRunningAPI struct{}

type executionDiscoveryAPI struct{}

type lostStartResponseRecoveryAPI struct {
	calls  []call
	starts int
}

type adoptionAPI struct{}

func (*adoptionAPI) Do(_ context.Context, method, path string, _ any, output any) (string, time.Duration, error) {
	if method != http.MethodGet || !strings.Contains(path, "/executions/execution-1") {
		return "", 0, nil
	}
	payload := `{"name":"execution-1","properties":{"status":"Running","template":{"containers":[{"name":"generator","env":[
		{"name":"APEXLOAD_CONTROLLER_ID","value":"controller-1"},
		{"name":"APEXLOAD_EXECUTION_TARGET_ID","value":"target-1"},
		{"name":"APEXLOAD_FLEET_POOL_ID","value":"pool-1"},
		{"name":"APEXLOAD_CAPACITY_OPERATION_ID","value":"operation-1"},
		{"name":"APEXLOAD_GENERATOR_ID","value":"generator-1"},
		{"name":"APEXLOAD_LAUNCH_NONCE","value":"nonce-1"}
	]}]}}}`
	return "", 0, json.Unmarshal([]byte(payload), output)
}

func (f *lostStartResponseRecoveryAPI) Do(_ context.Context, method, path string, body any, output any) (string, time.Duration, error) {
	f.calls = append(f.calls, call{method: method, path: path, body: body})
	if method == http.MethodPost {
		f.starts++
		return "", 0, errors.New("duplicate start must not be attempted")
	}
	payload := `{"name":"execution-recovered","properties":{"status":"Running","template":{"containers":[{"name":"generator","env":[
		{"name":"APEXLOAD_CONTROLLER_ID","value":"controller-1"},
		{"name":"APEXLOAD_FLEET_POOL_ID","value":"pool-1"},
		{"name":"APEXLOAD_CAPACITY_OPERATION_ID","value":"operation-1"},
		{"name":"APEXLOAD_GENERATOR_ID","value":"generator-1"},
		{"name":"APEXLOAD_LAUNCH_NONCE","value":"nonce-1"}
	]}]}}}`
	if method == http.MethodGet && strings.Contains(path, "/executions?") {
		return "", 0, json.Unmarshal([]byte(`{"value":[`+payload+`]}`), output)
	}
	if method == http.MethodGet && strings.Contains(path, "/executions/execution-recovered") {
		return "", 0, json.Unmarshal([]byte(payload), output)
	}
	return "", 0, nil
}

func (*executionDiscoveryAPI) Do(_ context.Context, method, path string, _ any, output any) (string, time.Duration, error) {
	if method != http.MethodGet || !strings.Contains(path, "/executions?") {
		return "", 0, nil
	}
	payload := `{"value":[
		{"name":"execution-owned","properties":{"status":"Running","template":{"containers":[{"name":"generator","env":[
			{"name":"APEXLOAD_CONTROLLER_ID","value":"controller-1"},
			{"name":"APEXLOAD_FLEET_POOL_ID","value":"pool-1"},
			{"name":"APEXLOAD_CAPACITY_OPERATION_ID","value":"operation-1"},
			{"name":"APEXLOAD_GENERATOR_ID","value":"generator-1"}
		]}]}}},
		{"name":"execution-other","properties":{"status":"Running","template":{"containers":[{"name":"generator","env":[
			{"name":"APEXLOAD_CONTROLLER_ID","value":"other-controller"},
			{"name":"APEXLOAD_FLEET_POOL_ID","value":"pool-1"},
			{"name":"APEXLOAD_CAPACITY_OPERATION_ID","value":"operation-other"}
		]}]}}}
	]}`
	return "", 0, json.Unmarshal([]byte(payload), output)
}

func (*longRunningAPI) Do(_ context.Context, method, path string, _ any, output any) (string, time.Duration, error) {
	if method == http.MethodGet && strings.HasPrefix(path, "https://management.azure.com/") {
		encoded, _ := json.Marshal(map[string]any{
			"name": "operation-123", "status": "Succeeded",
			"properties": map[string]any{"resourceId": "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.App/jobs/job/executions/execution-42"},
		})
		return "", 0, json.Unmarshal(encoded, output)
	}
	return "", 0, nil
}

func (f *validationAPI) Do(_ context.Context, _, _ string, _ any, output any) (string, time.Duration, error) {
	response, ok := output.(*armResourceResponse)
	if !ok {
		return "", 0, nil
	}
	response.Identity.PrincipalID = f.principal
	if f.userPrincipal != "" {
		response.Identity.UserAssignedIdentities = map[string]struct {
			PrincipalID string `json:"principalId"`
		}{"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/generator": {PrincipalID: f.userPrincipal}}
	}
	for _, name := range f.containers {
		response.Properties.Template.Containers = append(response.Properties.Template.Containers, struct {
			Name string `json:"name"`
		}{Name: name})
	}
	for index := 0; index < f.scaleRules; index++ {
		response.Properties.Template.Scale.Rules = append(response.Properties.Template.Scale.Rules, json.RawMessage(`{"name":"external"}`))
	}
	return "", 0, nil
}

func (f *fakeAPI) Do(_ context.Context, method, path string, body any, output any) (string, time.Duration, error) {
	f.calls = append(f.calls, call{method: method, path: path, body: body})
	if method == http.MethodPost && f.starts < 2 {
		f.starts++
		if result, ok := output.(*struct {
			Name string `json:"name"`
		}); ok {
			result.Name = "execution-" + string(rune('0'+f.starts))
		}
	}
	if result, ok := output.(*struct {
		Name       string `json:"name"`
		Properties struct {
			Status string `json:"status"`
		} `json:"properties"`
	}); ok {
		result.Name = "execution"
		result.Properties.Status = "Running"
	}
	if result, ok := output.(*jobExecutionResponse); ok {
		result.Name = "execution"
		result.Properties.Status = "Running"
	}
	return "", 0, nil
}

func TestContainerAppsWarmAndBurstRequestShapes(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, SubscriptionID: "sub-1", ControllerID: "controller-1", Target: &storage.ExecutionTarget{
		Kind: storage.ExecutionTargetAzureContainerApps, StandingOwner: storage.CapacityOwnerApex,
		BurstOwner: storage.CapacityOwnerApex, AzureContainerApps: &storage.AzureContainerAppsTargetSpec{
			ResourceGroup: "rg-load", EnvironmentName: "env", WarmContainerApp: "warm", BurstJob: "burst",
		},
	}}
	observation, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		OperationID: "op", IdempotencyKey: "key", ExecutionTargetID: "target-1", FleetPoolID: "pool-1", Desired: 3, WarmFloor: 1,
		LaunchIntents: []compute.LaunchIntent{
			{ClientToken: "client-1", GeneratorID: "generator-1", LaunchNonce: "nonce-1"},
			{ClientToken: "client-2", GeneratorID: "generator-2", LaunchNonce: "nonce-2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Terminal || observation.Ready != 3 {
		t.Fatalf("observation = %+v", observation)
	}
	if len(api.calls) != 7 {
		t.Fatalf("calls = %+v, want ownership read + patch + discovery + 2 starts + 2 gets", api.calls)
	}
	if api.calls[0].method != http.MethodGet || api.calls[1].method != http.MethodPatch || api.calls[2].method != http.MethodGet || api.calls[3].method != http.MethodPost || api.calls[4].method != http.MethodPost {
		t.Fatalf("mutation order = %+v", api.calls[:5])
	}
}

func TestContainerAppsReconcileBindsRegisteredRuntimeProfile(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, SubscriptionID: "sub-1", ControllerID: "controller-1", Target: &storage.ExecutionTarget{
		Kind: storage.ExecutionTargetAzureContainerApps, StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex,
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg-load", BurstJob: "burst"},
		RuntimeProfiles:    []storage.RuntimeProfile{{ID: "large", Name: "Large", CPUMillis: 2000, MemoryMiB: 4096, EphemeralStorageGiB: 8}},
	}}
	_, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		OperationID: "op", ExecutionTargetID: "target-1", FleetPoolID: "pool-1", RuntimeProfileID: "large", Desired: 1,
		LaunchIntents: []compute.LaunchIntent{{ClientToken: "client-1", GeneratorID: "generator-1", LaunchNonce: "nonce-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var startBody map[string]any
	for _, call := range api.calls {
		if call.method == http.MethodPost && strings.Contains(call.path, "/start") {
			startBody = call.body.(map[string]any)
			break
		}
	}
	if startBody == nil {
		t.Fatal("job start was not called")
	}
	containers := startBody["containers"].([]any)
	env := containers[0].(map[string]any)["env"].([]any)
	found := false
	for _, raw := range env {
		value := raw.(map[string]string)
		if value["name"] == "APEXLOAD_RUNTIME_PROFILE_ID" && value["value"] == "large" {
			found = true
		}
	}
	if !found {
		t.Fatalf("runtime profile environment is missing: %+v", env)
	}
	resources := containers[0].(map[string]any)["resources"].(map[string]any)
	if resources["cpu"] != float64(2) || resources["memory"] != "4096Mi" || resources["ephemeralStorage"] != "8Gi" {
		t.Fatalf("runtime profile resources = %+v", resources)
	}
	info := backend.Describe()
	if len(info.ResourceShapes) != 1 || info.ResourceShapes[0] != "2000m/4096Mi" {
		t.Fatalf("capabilities = %+v", info)
	}
}

func TestContainerAppsReconcileRejectsUnknownRuntimeProfileBeforeProviderCalls(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, SubscriptionID: "sub-1", ControllerID: "controller-1", Target: &storage.ExecutionTarget{
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex,
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg-load", BurstJob: "burst"},
	}}
	_, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		OperationID: "op", ExecutionTargetID: "target-1", FleetPoolID: "pool-1", RuntimeProfileID: "missing", Desired: 1,
		LaunchIntents: []compute.LaunchIntent{{ClientToken: "client-1", GeneratorID: "generator-1", LaunchNonce: "nonce-1"}},
	})
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Reconcile error = %v, want unknown-profile rejection", err)
	}
	if len(api.calls) != 0 {
		t.Fatalf("provider calls = %+v, want none", api.calls)
	}
}

func TestContainerAppsAdoptRequiresExactOwnershipMetadata(t *testing.T) {
	backend := &Backend{API: &adoptionAPI{}, SubscriptionID: "sub-1", ControllerID: "controller-1", Target: &storage.ExecutionTarget{
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", BurstJob: "job"},
	}}
	request := compute.AdoptionRequest{ProviderID: "execution-1", ExecutionTargetID: "target-1", FleetPoolID: "pool-1", OperationID: "operation-1", GeneratorID: "generator-1", LaunchNonce: "nonce-1"}
	instance, err := backend.Adopt(context.Background(), request)
	if err != nil || instance.ProviderID != "execution-1" || !instance.IdentityAttested {
		t.Fatalf("Adopt() = %+v, %v", instance, err)
	}
	request.FleetPoolID = "other-pool"
	if _, err := backend.Adopt(context.Background(), request); err == nil {
		t.Fatal("Adopt accepted an execution owned by another pool")
	}
}

func TestContainerAppsReconcileRecoversLostStartResponseByDurableIntent(t *testing.T) {
	api := &lostStartResponseRecoveryAPI{}
	backend := &Backend{API: api, SubscriptionID: "sub", ControllerID: "controller-1", Target: &storage.ExecutionTarget{
		Kind: storage.ExecutionTargetAzureContainerApps, StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex,
		ConnectivityMode:   storage.ConnectivityReverse,
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", EnvironmentName: "env", BurstJob: "job"},
	}}
	observation, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		OperationID: "operation-1", ExecutionTargetID: "target-1", FleetPoolID: "pool-1", Desired: 1,
		LaunchIntents: []compute.LaunchIntent{{ClientToken: "client-1", GeneratorID: "generator-1", LaunchNonce: "nonce-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if api.starts != 0 {
		t.Fatalf("duplicate Azure Job starts = %d, want 0", api.starts)
	}
	if !observation.Terminal || observation.Ready != 1 || len(observation.Instances) != 1 || observation.Instances[0].ProviderID != "execution-recovered" {
		t.Fatalf("observation = %+v", observation)
	}
}

func TestContainerAppsReconcileRefusesMissingDurableLaunchIntent(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, SubscriptionID: "sub", Target: &storage.ExecutionTarget{
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex,
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", BurstJob: "job"},
	}}
	if _, err := backend.Reconcile(context.Background(), compute.CapacityRequest{Desired: 1}); err == nil {
		t.Fatal("Reconcile succeeded without a durable launch intent")
	}
	if len(api.calls) != 0 {
		t.Fatalf("provider was mutated without a durable launch intent: %+v", api.calls)
	}
}

func TestContainerAppsDeprovisionRequiresTerminalArchive(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, SubscriptionID: "sub", Target: &storage.ExecutionTarget{AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", BurstJob: "job"}}}
	err := backend.Deprovision(context.Background(), compute.CapacityRequest{}, []compute.Instance{{ProviderID: "execution-1"}})
	if !errors.Is(err, compute.ErrArchiveNotDrained) {
		t.Fatalf("error = %v", err)
	}
	if len(api.calls) != 0 {
		t.Fatal("Azure stop called before archive terminal")
	}
}

func TestContainerAppsCancelRollsBackWarmAppBeforeStoppingJob(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, SubscriptionID: "sub", Target: &storage.ExecutionTarget{AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", WarmContainerApp: "warm", BurstJob: "job"}}}
	if err := backend.Cancel(context.Background(), compute.CapacityRequest{RestoreStanding: true, RollbackStanding: 0, ProviderOperationIDs: []string{"execution-1"}}); err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 3 || api.calls[0].method != http.MethodPatch || api.calls[1].method != http.MethodGet || api.calls[2].method != http.MethodPost {
		t.Fatalf("cancel calls = %+v", api.calls)
	}
	body := api.calls[0].body.(map[string]any)
	scale := body["properties"].(map[string]any)["template"].(map[string]any)["scale"].(map[string]any)
	if scale["minReplicas"] != 0 || scale["maxReplicas"] != 1 {
		t.Fatalf("rollback scale = %+v", scale)
	}
}

func TestContainerAppsPlanRefusesExternallyOwnedStandingCapacity(t *testing.T) {
	backend := &Backend{Target: &storage.ExecutionTarget{
		StandingOwner: storage.CapacityOwnerExternal, BurstOwner: storage.CapacityOwnerApex,
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{WarmContainerApp: "warm", BurstJob: "burst"},
	}}
	_, err := backend.Plan(context.Background(), compute.CapacityRequest{Desired: 2, WarmFloor: 1})
	if !errors.Is(err, compute.ErrOwnershipConflict) {
		t.Fatalf("Plan error = %v, want ownership conflict", err)
	}
}

func TestContainerAppsRefusesProviderAutoscalerOwnershipConflict(t *testing.T) {
	api := &validationAPI{scaleRules: 1}
	backend := &Backend{API: api, SubscriptionID: "sub", Target: &storage.ExecutionTarget{
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerDisabled,
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", WarmContainerApp: "warm"},
	}}
	probes := backend.Validate(context.Background())
	var ownership compute.ProbeResult
	for _, probe := range probes {
		if probe.Probe == "container_app_autoscaling_owner" {
			ownership = probe
		}
	}
	if ownership.OK || !strings.Contains(ownership.Message, "scale rules") {
		t.Fatalf("ownership probe = %+v", ownership)
	}
	if _, err := backend.Reconcile(context.Background(), compute.CapacityRequest{Desired: 1, WarmFloor: 1}); !errors.Is(err, compute.ErrOwnershipConflict) {
		t.Fatalf("Reconcile error = %v, want ownership conflict", err)
	}
}

func TestContainerAppsValidateProbesIdentityAndRequiredSidecars(t *testing.T) {
	api := &validationAPI{userPrincipal: "principal-1", containers: []string{"generator", "bruno"}}
	backend := &Backend{API: api, SubscriptionID: "sub", Target: &storage.ExecutionTarget{
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{
			ResourceGroup: "rg", EnvironmentName: "env", WarmContainerApp: "warm", ExpectedPrincipalID: "principal-1",
		},
		RuntimeProfiles: []storage.RuntimeProfile{{ID: "profile", Name: "Profile", Sidecars: []string{"bruno", "java-vuser-runner"}}},
	}}
	probes := backend.Validate(context.Background())
	byName := make(map[string]compute.ProbeResult, len(probes))
	for _, probe := range probes {
		byName[probe.Probe] = probe
	}
	if !byName["container_app_identity"].OK {
		t.Fatalf("identity probe = %+v", byName["container_app_identity"])
	}
	if byName["container_app_sidecars"].OK || !strings.Contains(byName["container_app_sidecars"].Message, "java-vuser-runner") {
		t.Fatalf("sidecar probe = %+v", byName["container_app_sidecars"])
	}
}

func TestContainerAppsLongRunningOperationResolvesExecutionIdentity(t *testing.T) {
	backend := &Backend{API: &longRunningAPI{}, SubscriptionID: "sub", Target: &storage.ExecutionTarget{
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", BurstJob: "job"},
	}}
	instances, err := backend.describeExecutions(context.Background(), []string{"https://management.azure.com/providers/Microsoft.App/locations/eastus/operations/operation-123?api-version=2025-07-01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ProviderID != "execution-42" || instances[0].State != "Succeeded" {
		t.Fatalf("instances = %+v", instances)
	}
}

func TestContainerAppsDiscoverFindsOnlyControllerOwnedExecutions(t *testing.T) {
	backend := &Backend{API: &executionDiscoveryAPI{}, SubscriptionID: "sub", ControllerID: "controller-1", Target: &storage.ExecutionTarget{
		AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", BurstJob: "job"},
	}}
	instances, err := backend.Discover(context.Background(), compute.CapacityRequest{FleetPoolID: "pool-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ProviderID != "execution-owned" || instances[0].GeneratorID != "generator-1" {
		t.Fatalf("discovered executions = %+v", instances)
	}
}

func TestValidateARMEndpointAllowlist(t *testing.T) {
	for _, endpoint := range []string{"http://management.azure.com", "https://example.com", "https://management.azure.com/path"} {
		if _, _, err := ValidateARMEndpoint(endpoint); err == nil {
			t.Errorf("ValidateARMEndpoint(%q) succeeded", endpoint)
		}
	}
	if got, _, err := ValidateARMEndpoint(""); err != nil || got != "https://management.azure.com" {
		t.Fatalf("default endpoint = (%q, %v)", got, err)
	}
}
