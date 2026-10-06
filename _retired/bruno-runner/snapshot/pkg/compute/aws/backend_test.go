package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

type recordedCall struct {
	action string
	input  map[string]any
}
type fakeAPI struct {
	calls                []recordedCall
	taskIP               string
	warmRunning          int
	warmPending          int
	useWarmServiceCounts bool
	taskRole             string
	taskContainers       []string
	taskTags             map[string]string
}

type fakeAutoscalingAPI struct {
	conflict bool
	calls    []recordedCall
}

func (f *fakeAutoscalingAPI) Call(_ context.Context, action string, input any, output any) error {
	encoded, _ := json.Marshal(input)
	var object map[string]any
	_ = json.Unmarshal(encoded, &object)
	f.calls = append(f.calls, recordedCall{action: action, input: object})
	if f.conflict {
		response := output.(*describeScalableTargetsResponse)
		response.ScalableTargets = append(response.ScalableTargets, struct {
			ResourceID string `json:"resourceId"`
		}{ResourceID: "service/apex/warm"})
	}
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func (f *fakeAPI) Call(_ context.Context, action string, input any, output any) error {
	encoded, _ := json.Marshal(input)
	var object map[string]any
	_ = json.Unmarshal(encoded, &object)
	f.calls = append(f.calls, recordedCall{action: action, input: object})
	switch result := output.(type) {
	case *describeServicesResponse:
		running, pending := 1, 0
		if f.useWarmServiceCounts {
			running, pending = f.warmRunning, f.warmPending
		}
		result.Services = append(result.Services, struct {
			ServiceARN     string `json:"serviceArn"`
			TaskDefinition string `json:"taskDefinition"`
			DesiredCount   int    `json:"desiredCount"`
			RunningCount   int    `json:"runningCount"`
			PendingCount   int    `json:"pendingCount"`
		}{ServiceARN: "arn:service/warm", TaskDefinition: "warm-task:1", DesiredCount: running + pending, RunningCount: running, PendingCount: pending})
	case *describeTaskDefinitionResponse:
		result.TaskDefinition.TaskRoleARN = f.taskRole
		for _, name := range f.taskContainers {
			result.TaskDefinition.ContainerDefinitions = append(result.TaskDefinition.ContainerDefinitions, struct {
				Name string `json:"name"`
			}{Name: name})
		}
	case *runTaskResponse:
		result.Tasks = append(result.Tasks, struct {
			TaskARN string `json:"taskArn"`
		}{TaskARN: "arn:aws:ecs:us-east-1:123:task/one"})
	case *listTasksResponse:
		if _, serviceListing := object["serviceName"]; serviceListing && (!f.useWarmServiceCounts || f.warmRunning > 0) {
			result.TaskARNs = []string{"arn:aws:ecs:us-east-1:123:task/warm"}
		}
	case *describeTasksResponse:
		for _, value := range object["tasks"].([]any) {
			task := describedTask{TaskARN: value.(string), LastStatus: "RUNNING"}
			if f.taskIP != "" {
				task.Attachments = []taskAttachment{{Details: []taskAttachmentDetail{{Name: "privateIPv4Address", Value: f.taskIP}}}}
				task.Tags = []taskTag{{Key: "apexload.io/generator-id", Value: "generator-direct"}, {Key: "apexload.io/launch-nonce", Value: "nonce-direct"}}
			}
			for key, value := range f.taskTags {
				task.Tags = append(task.Tags, taskTag{Key: key, Value: value})
			}
			result.Tasks = append(result.Tasks, task)
		}
	}
	return nil
}

func TestFargateReconcileWaitsForWarmServiceTasksToRun(t *testing.T) {
	api := &fakeAPI{useWarmServiceCounts: true, warmPending: 1}
	backend := &Backend{API: api, Target: &storage.ExecutionTarget{
		StandingOwner: storage.CapacityOwnerApex,
		BurstOwner:    storage.CapacityOwnerDisabled,
		AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "apex", WarmService: "warm"},
	}}
	observation, err := backend.Reconcile(context.Background(), compute.CapacityRequest{Desired: 1, WarmFloor: 1})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Terminal || observation.Ready != 0 || observation.Pending != 1 {
		t.Fatalf("observation = %+v, want pending warm task", observation)
	}
}

func TestFargateDescribeTasksReturnsDirectGeneratorAddressAndAttestation(t *testing.T) {
	api := &fakeAPI{taskIP: "10.42.3.9"}
	backend := &Backend{API: api, GeneratorPort: 9191, Target: &storage.ExecutionTarget{AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "apex"}}}
	instances, err := backend.describeTasks(context.Background(), []string{"task-direct"})
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Address != "10.42.3.9:9191" || instances[0].GeneratorID != "generator-direct" || !instances[0].IdentityAttested {
		t.Fatalf("instances = %+v", instances)
	}
}

func TestFargateReconcileUsesOneFreshIdentityPerLaunch(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, ControllerID: "controller-1", ContainerName: "generator", Target: &storage.ExecutionTarget{
		Kind: storage.ExecutionTargetAWSECSFargate, StandingOwner: storage.CapacityOwnerApex,
		BurstOwner: storage.CapacityOwnerApex, AWSECSFargate: &storage.AWSECSFargateTargetSpec{
			Cluster: "apex", WarmService: "warm", BurstTaskDefinition: "burst:1",
			Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"}, AssignPublicIP: "disabled",
		},
	}}
	observation, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		OperationID: "operation-1", IdempotencyKey: "idempotency-1", ExecutionTargetID: "target-1", FleetPoolID: "pool-1", Desired: 3, WarmFloor: 1,
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
	var generatorIDs []string
	for _, call := range api.calls {
		if call.action != "RunTask" {
			continue
		}
		overrides := call.input["overrides"].(map[string]any)
		containers := overrides["containerOverrides"].([]any)
		environment := containers[0].(map[string]any)["environment"].([]any)
		generatorIDs = append(generatorIDs, environment[0].(map[string]any)["value"].(string))
	}
	if len(generatorIDs) != 2 || generatorIDs[0] == generatorIDs[1] {
		t.Fatalf("ephemeral generator ids = %#v, want two fresh identities", generatorIDs)
	}
}

func TestFargateReconcileAppliesRuntimeProfileAndSpotCapacity(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, ControllerID: "controller-1", Target: &storage.ExecutionTarget{
		Kind: storage.ExecutionTargetAWSECSFargate, StandingOwner: storage.CapacityOwnerApex,
		BurstOwner: storage.CapacityOwnerApex,
		AWSECSFargate: &storage.AWSECSFargateTargetSpec{
			Cluster: "apex", BurstTaskDefinition: "burst:1", Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"},
		},
		RuntimeProfiles: []storage.RuntimeProfile{{ID: "spot-large", Name: "Spot large", CPUMillis: 2048, MemoryMiB: 4096, TemplateRevision: "burst:7", CapacityType: "spot"}},
	}}
	_, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		OperationID: "operation-1", ExecutionTargetID: "target-1", FleetPoolID: "pool-1", RuntimeProfileID: "spot-large", Desired: 1,
		LaunchIntents: []compute.LaunchIntent{{ClientToken: "client-1", GeneratorID: "generator-1", LaunchNonce: "nonce-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var runTask map[string]any
	for _, call := range api.calls {
		if call.action == "RunTask" {
			runTask = call.input
			break
		}
	}
	if runTask == nil {
		t.Fatal("RunTask was not called")
	}
	if got := runTask["taskDefinition"]; got != "burst:7" {
		t.Fatalf("taskDefinition = %#v, want runtime-profile revision", got)
	}
	if _, found := runTask["launchType"]; found {
		t.Fatalf("spot request unexpectedly set launchType: %+v", runTask)
	}
	strategy, ok := runTask["capacityProviderStrategy"].([]any)
	if !ok || len(strategy) != 1 || strategy[0].(map[string]any)["capacityProvider"] != "FARGATE_SPOT" {
		t.Fatalf("capacityProviderStrategy = %#v", runTask["capacityProviderStrategy"])
	}
	info := backend.Describe()
	if !info.SupportsSpot || len(info.ResourceShapes) != 1 || info.ResourceShapes[0] != "2048m/4096Mi" {
		t.Fatalf("capabilities = %+v", info)
	}
}

func TestFargateReconcileRejectsUnknownRuntimeProfileBeforeProviderCalls(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, ControllerID: "controller-1", Target: &storage.ExecutionTarget{
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex,
		AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "apex", BurstTaskDefinition: "burst:1"},
	}}
	_, err := backend.Reconcile(context.Background(), compute.CapacityRequest{
		OperationID: "operation-1", ExecutionTargetID: "target-1", FleetPoolID: "pool-1", RuntimeProfileID: "missing", Desired: 1,
		LaunchIntents: []compute.LaunchIntent{{ClientToken: "client-1", GeneratorID: "generator-1", LaunchNonce: "nonce-1"}},
	})
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Reconcile error = %v, want unknown-profile rejection", err)
	}
	if len(api.calls) != 0 {
		t.Fatalf("provider calls = %+v, want none", api.calls)
	}
}

func TestFargateAdoptRequiresExactOwnershipTags(t *testing.T) {
	api := &fakeAPI{taskTags: map[string]string{
		"apexload.io/controller-id":       "controller-1",
		"apexload.io/execution-target-id": "target-1",
		"apexload.io/pool-id":             "pool-1",
		"apexload.io/operation-id":        "operation-1",
		"apexload.io/generator-id":        "generator-1",
		"apexload.io/launch-nonce":        "nonce-1",
	}}
	backend := &Backend{API: api, ControllerID: "controller-1", Target: &storage.ExecutionTarget{AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "apex"}}}
	request := compute.AdoptionRequest{ProviderID: "task-1", ExecutionTargetID: "target-1", FleetPoolID: "pool-1", OperationID: "operation-1", GeneratorID: "generator-1", LaunchNonce: "nonce-1"}
	instance, err := backend.Adopt(context.Background(), request)
	if err != nil || instance.ProviderID != "task-1" || !instance.IdentityAttested {
		t.Fatalf("Adopt() = %+v, %v", instance, err)
	}
	request.FleetPoolID = "other-pool"
	if _, err := backend.Adopt(context.Background(), request); err == nil {
		t.Fatal("Adopt accepted a task owned by another pool")
	}
}

func TestFargateReconcileRefusesMissingDurableLaunchIntent(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, Target: &storage.ExecutionTarget{
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex,
		AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "apex", BurstTaskDefinition: "burst:1", Subnets: []string{"subnet-1"}, SecurityGroups: []string{"sg-1"}},
	}}
	if _, err := backend.Reconcile(context.Background(), compute.CapacityRequest{Desired: 1}); err == nil {
		t.Fatal("Reconcile succeeded without a durable launch intent")
	}
	if len(api.calls) != 0 {
		t.Fatalf("provider was mutated without a durable launch intent: %+v", api.calls)
	}
}

func TestFargateDeprovisionRequiresTerminalArchive(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, Target: &storage.ExecutionTarget{AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "apex"}}}
	err := backend.Deprovision(context.Background(), compute.CapacityRequest{}, []compute.Instance{{ProviderID: "task-1"}})
	if !errors.Is(err, compute.ErrArchiveNotDrained) {
		t.Fatalf("error = %v", err)
	}
	if len(api.calls) != 0 {
		t.Fatalf("StopTask called before archive terminal: %+v", api.calls)
	}
}

func TestFargateCancelRollsBackWarmServiceBeforeStoppingBurst(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, Target: &storage.ExecutionTarget{StandingOwner: storage.CapacityOwnerApex, AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "apex", WarmService: "warm"}}}
	if err := backend.Cancel(context.Background(), compute.CapacityRequest{RestoreStanding: true, RollbackStanding: 0, ProviderOperationIDs: []string{"task-1"}}); err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 3 || api.calls[0].action != "UpdateService" || api.calls[1].action != "DescribeTasks" || api.calls[2].action != "StopTask" {
		t.Fatalf("cancel calls = %+v", api.calls)
	}
	if desired, ok := api.calls[0].input["desiredCount"].(float64); !ok || desired != 0 {
		t.Fatalf("rollback desiredCount = %#v", api.calls[0].input["desiredCount"])
	}
}

func TestFargatePlanRefusesExternallyOwnedBurstCapacity(t *testing.T) {
	backend := &Backend{Target: &storage.ExecutionTarget{
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerExternal,
		AWSECSFargate: &storage.AWSECSFargateTargetSpec{WarmService: "warm", BurstTaskDefinition: "burst"},
	}}
	_, err := backend.Plan(context.Background(), compute.CapacityRequest{Desired: 3, WarmFloor: 1})
	if !errors.Is(err, compute.ErrOwnershipConflict) {
		t.Fatalf("Plan error = %v, want ownership conflict", err)
	}
}

func TestFargateRefusesApplicationAutoscalingOwnershipConflict(t *testing.T) {
	autoscaling := &fakeAutoscalingAPI{conflict: true}
	backend := &Backend{API: &fakeAPI{}, AutoscalingAPI: autoscaling, Target: &storage.ExecutionTarget{
		StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerDisabled,
		AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "apex", WarmService: "warm"},
	}}
	probes := backend.Validate(context.Background())
	var ownership compute.ProbeResult
	for _, probe := range probes {
		if probe.Probe == "ecs_autoscaling_owner" {
			ownership = probe
		}
	}
	if ownership.OK || !strings.Contains(ownership.Message, "Application Auto Scaling") {
		t.Fatalf("ownership probe = %+v", ownership)
	}
	if _, err := backend.Reconcile(context.Background(), compute.CapacityRequest{Desired: 1, WarmFloor: 1}); !errors.Is(err, compute.ErrOwnershipConflict) {
		t.Fatalf("Reconcile error = %v, want ownership conflict", err)
	}
	if len(autoscaling.calls) != 2 || autoscaling.calls[0].action != "DescribeScalableTargets" {
		t.Fatalf("autoscaling calls = %+v", autoscaling.calls)
	}
}

func TestAWSJSONClientsProduceServiceScopedSigV4(t *testing.T) {
	tests := []struct {
		name       string
		wantTarget string
		wantScope  string
		call       func(*http.Client) error
	}{
		{
			name: "ecs", wantTarget: ecsTargetPrefix + "ListClusters", wantScope: "/us-east-1/ecs/aws4_request",
			call: func(client *http.Client) error {
				return (&JSONClient{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""), HTTP: client}).Call(context.Background(), "ListClusters", map[string]any{}, &map[string]any{})
			},
		},
		{
			name: "application autoscaling", wantTarget: applicationAutoscalingTargetPrefix + "DescribeScalableTargets", wantScope: "/us-east-1/application-autoscaling/aws4_request",
			call: func(client *http.Client) error {
				return (&ApplicationAutoscalingJSONClient{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""), HTTP: client}).Call(context.Background(), "DescribeScalableTargets", map[string]any{"serviceNamespace": "ecs"}, &map[string]any{})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if got := request.Header.Get("X-Amz-Target"); got != test.wantTarget {
					t.Fatalf("X-Amz-Target = %q, want %q", got, test.wantTarget)
				}
				authorization := request.Header.Get("Authorization")
				if !strings.Contains(authorization, "AWS4-HMAC-SHA256 Credential=AKID/") || !strings.Contains(authorization, test.wantScope) || !strings.Contains(authorization, "Signature=") {
					t.Fatalf("Authorization is not service-scoped SigV4: %q", authorization)
				}
				if request.Header.Get("X-Amz-Date") == "" {
					t.Fatal("SigV4 request omitted X-Amz-Date")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			})}
			if err := test.call(client); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFargateValidateProbesTaskRoleAndRequiredSidecars(t *testing.T) {
	api := &fakeAPI{taskRole: "arn:aws:iam::123:role/generator", taskContainers: []string{"generator", "bruno"}}
	backend := &Backend{API: api, Target: &storage.ExecutionTarget{
		AWSECSFargate: &storage.AWSECSFargateTargetSpec{
			Cluster: "apex", BurstTaskDefinition: "burst:1", ExpectedTaskRoleARN: "arn:aws:iam::123:role/generator",
		},
		RuntimeProfiles: []storage.RuntimeProfile{{ID: "profile", Name: "Profile", Sidecars: []string{"bruno", "java-vuser-runner"}}},
	}}
	probes := backend.Validate(context.Background())
	byName := make(map[string]compute.ProbeResult, len(probes))
	for _, probe := range probes {
		byName[probe.Probe] = probe
	}
	if !byName["task_role_identity"].OK {
		t.Fatalf("identity probe = %+v", byName["task_role_identity"])
	}
	if byName["task_definition_sidecars"].OK || !strings.Contains(byName["task_definition_sidecars"].Message, "java-vuser-runner") {
		t.Fatalf("sidecar probe = %+v", byName["task_definition_sidecars"])
	}
}

func TestResolveEndpointRejectsCredentialExfiltrationHosts(t *testing.T) {
	for _, endpoint := range []string{"http://ecs.us-east-1.amazonaws.com", "https://example.com", "https://vpce-123.ecs.us-east-1.vpce.amazonaws.com"} {
		if _, err := ResolveEndpoint(endpoint, "us-east-1"); err == nil {
			t.Errorf("ResolveEndpoint(%q) succeeded", endpoint)
		}
	}
}

func TestFargatePreparationSpotFloorAndStandingIsolation(t *testing.T) {
	api := &fakeAPI{}
	backend := &Backend{API: api, ControllerID: "controller", Target: &storage.ExecutionTarget{Kind: storage.ExecutionTargetAWSECSFargate, StandingOwner: storage.CapacityOwnerApex, BurstOwner: storage.CapacityOwnerApex, AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "apex", WarmService: "warm", BurstTaskDefinition: "burst", Subnets: []string{"subnet"}}, RuntimeProfiles: []storage.RuntimeProfile{{ID: "spot", CapacityType: "spot"}}}}
	request := compute.CapacityRequest{OperationID: "op", ExecutionTargetID: "target", FleetPoolID: "pool", Desired: 4, WarmFloor: 3, Policy: compute.CapacityPolicy{Selection: "ephemeral", Spot: "required", OnDemandFloor: 1, SpotCeilingPercent: 50}}
	for i := 0; i < 4; i++ {
		request.LaunchIntents = append(request.LaunchIntents, compute.LaunchIntent{ClientToken: fmt.Sprint("token", i), GeneratorID: fmt.Sprint("generator", i), LaunchNonce: fmt.Sprint("nonce", i)})
	}
	if _, err := backend.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	spot, onDemand := 0, 0
	for _, call := range api.calls {
		if call.action == "UpdateService" {
			t.Fatal("ephemeral preparation mutated standing capacity")
		}
		if call.action == "RunTask" {
			if call.input["capacityProviderStrategy"] != nil {
				spot++
			} else {
				onDemand++
			}
		}
	}
	if spot != 2 || onDemand != 2 {
		t.Fatalf("allocation spot=%d on-demand=%d", spot, onDemand)
	}
	request.Policy.Spot = "off"
	request.Policy.SpotCeilingPercent = 0
	api.calls = nil
	if _, err := backend.Reconcile(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for _, call := range api.calls {
		if call.input["capacityProviderStrategy"] != nil {
			t.Fatal("off policy launched spot")
		}
	}
}
