package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	ecsTargetPrefix                    = "AmazonEC2ContainerServiceV20141113."
	applicationAutoscalingTargetPrefix = "AnyScaleFrontendService."
)

var ecsEndpointHost = regexp.MustCompile(`^ecs(-fips)?\.[a-z0-9-]{1,32}\.amazonaws\.com(\.cn)?$`)
var applicationAutoscalingEndpointHost = regexp.MustCompile(`^application-autoscaling(-fips)?\.[a-z0-9-]{1,32}\.amazonaws\.com(\.cn)?$`)

type API interface {
	Call(context.Context, string, any, any) error
}

// JSONClient is the small hand-rolled ECS JSON protocol client used by the
// backend. Endpoint validation prevents a SigV4 credential from being sent to
// an operator-supplied host that is not AWS ECS.
type JSONClient struct {
	Region      string
	Endpoint    string
	Credentials awsconfig.CredentialsProvider
	Signer      *v4.Signer
	HTTP        *http.Client
}

// ApplicationAutoscalingJSONClient uses the same guarded SigV4 JSON transport
// for the one read needed to fence ECS desiredCount ownership.
type ApplicationAutoscalingJSONClient struct {
	Region      string
	Endpoint    string
	Credentials awsconfig.CredentialsProvider
	Signer      *v4.Signer
	HTTP        *http.Client
}

func ResolveEndpoint(raw, region string) (string, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return "", errors.New("aws region is required")
	}
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		suffix := "amazonaws.com"
		if strings.HasPrefix(region, "cn-") {
			suffix = "amazonaws.com.cn"
		}
		return "https://ecs." + region + "." + suffix + "/", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return "", errors.New("ECS endpoint must be a bare https origin")
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.HasPrefix(host, "vpce-") {
		return "", fmt.Errorf("ECS endpoint host %q is not allowed; use AWS private DNS for VPC endpoints", host)
	}
	if !ecsEndpointHost.MatchString(host) {
		return "", fmt.Errorf("ECS endpoint host %q is not allowed; use AWS private DNS for VPC endpoints", host)
	}
	return "https://" + host + "/", nil
}

func ResolveApplicationAutoscalingEndpoint(raw, region string) (string, error) {
	region = strings.TrimSpace(region)
	if region == "" {
		return "", errors.New("aws region is required")
	}
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		suffix := "amazonaws.com"
		if strings.HasPrefix(region, "cn-") {
			suffix = "amazonaws.com.cn"
		}
		return "https://application-autoscaling." + region + "." + suffix + "/", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return "", errors.New("Application Auto Scaling endpoint must be a bare https origin")
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.HasPrefix(host, "vpce-") {
		return "", fmt.Errorf("Application Auto Scaling endpoint host %q is not allowed; use AWS private DNS for VPC endpoints", host)
	}
	if !applicationAutoscalingEndpointHost.MatchString(host) {
		return "", fmt.Errorf("Application Auto Scaling endpoint host %q is not allowed", host)
	}
	return "https://" + host + "/", nil
}

func (c *JSONClient) Call(ctx context.Context, action string, input any, output any) error {
	if c == nil || c.Credentials == nil {
		return errors.New("ECS credentials are not configured")
	}
	endpoint, err := ResolveEndpoint(c.Endpoint, c.Region)
	if err != nil {
		return err
	}
	return callAWSJSON(ctx, c.Region, endpoint, "ecs", ecsTargetPrefix, "ECS", c.Credentials, c.Signer, c.HTTP, action, input, output)
}

func (c *ApplicationAutoscalingJSONClient) Call(ctx context.Context, action string, input any, output any) error {
	if c == nil || c.Credentials == nil {
		return errors.New("Application Auto Scaling credentials are not configured")
	}
	endpoint, err := ResolveApplicationAutoscalingEndpoint(c.Endpoint, c.Region)
	if err != nil {
		return err
	}
	return callAWSJSON(ctx, c.Region, endpoint, "application-autoscaling", applicationAutoscalingTargetPrefix, "Application Auto Scaling", c.Credentials, c.Signer, c.HTTP, action, input, output)
}

func callAWSJSON(ctx context.Context, region, endpoint, signingService, targetPrefix, serviceLabel string, credentialsProvider awsconfig.CredentialsProvider, signer *v4.Signer, httpClient *http.Client, action string, input any, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-amz-json-1.1")
	request.Header.Set("X-Amz-Target", targetPrefix+action)
	credentials, err := credentialsProvider.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("resolve %s credentials: %w", serviceLabel, err)
	}
	digest := sha256.Sum256(body)
	if signer == nil {
		signer = v4.NewSigner()
	}
	if err := signer.SignHTTP(ctx, credentials, request, hex.EncodeToString(digest[:]), signingService, region, time.Now()); err != nil {
		return err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limited, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("%s %s failed with HTTP %d", serviceLabel, action, response.StatusCode)
	}
	if output != nil && len(limited) > 0 {
		if err := json.Unmarshal(limited, output); err != nil {
			return fmt.Errorf("decode %s %s response: %w", serviceLabel, action, err)
		}
	}
	return nil
}

type Backend struct {
	API            API
	AutoscalingAPI API
	Target         *storage.ExecutionTarget
	ControllerID   string
	ContainerName  string
	GeneratorPort  int
}

func (b *Backend) Describe() compute.BackendInfo {
	info := compute.BackendInfo{Name: storage.ExecutionTargetAWSECSFargate, CanReachDirectly: true, SupportsReverse: true, Details: map[string]string{"capacity_model": "ecs_service_and_run_task"}}
	if b != nil && b.Target != nil && b.Target.AWSECSFargate != nil {
		info.CanScale = b.Target.StandingOwner == storage.CapacityOwnerApex && b.Target.AWSECSFargate.WarmService != ""
		info.CanLaunch = b.Target.BurstOwner == storage.CapacityOwnerApex && b.Target.AWSECSFargate.BurstTaskDefinition != ""
		for _, profile := range b.Target.RuntimeProfiles {
			if profile.CPUMillis > 0 && profile.MemoryMiB > 0 {
				info.ResourceShapes = append(info.ResourceShapes, fmt.Sprintf("%dm/%dMi", profile.CPUMillis, profile.MemoryMiB))
			}
			if strings.EqualFold(strings.TrimSpace(profile.CapacityType), "spot") {
				info.SupportsSpot = true
			}
		}
	}
	return info
}

func (b *Backend) Validate(ctx context.Context) []compute.ProbeResult {
	if b == nil || b.API == nil || b.Target == nil || b.Target.AWSECSFargate == nil {
		return []compute.ProbeResult{{Probe: "configuration", Message: "ECS backend is incomplete"}}
	}
	spec := b.Target.AWSECSFargate
	probes := []compute.ProbeResult{}
	taskDefinition := strings.TrimSpace(spec.BurstTaskDefinition)
	if spec.WarmService != "" {
		var response describeServicesResponse
		err := b.API.Call(ctx, "DescribeServices", map[string]any{"cluster": spec.Cluster, "services": []string{spec.WarmService}}, &response)
		probes = append(probes, compute.ProbeResult{Probe: "ecs_service", OK: err == nil && len(response.Services) == 1, Message: errorMessage(err, "warm ECS service is accessible")})
		probes = append(probes, b.ecsAutoscalingOwnershipProbe(ctx))
		if taskDefinition == "" && len(response.Services) == 1 {
			taskDefinition = strings.TrimSpace(response.Services[0].TaskDefinition)
		}
	}
	if taskDefinition != "" {
		var response describeTaskDefinitionResponse
		err := b.API.Call(ctx, "DescribeTaskDefinition", map[string]any{"taskDefinition": taskDefinition, "include": []string{"TAGS"}}, &response)
		probes = append(probes, compute.ProbeResult{Probe: "task_definition", OK: err == nil, Message: errorMessage(err, "generator task definition is accessible")})
		probes = append(probes, taskRoleProbe(err, response.TaskDefinition.TaskRoleARN, spec.ExpectedTaskRoleARN))
		probes = append(probes, taskSidecarProbe(err, requiredTaskSidecars(b.Target.RuntimeProfiles), response.containerNames()))
	}
	return probes
}

func (b *Backend) Plan(_ context.Context, request compute.CapacityRequest) (compute.Plan, error) {
	if _, err := request.Policy.SpotCount(request.Desired, b.Describe()); err != nil {
		return compute.Plan{}, err
	}
	if request.Policy.Selection == "existing" {
		return compute.Plan{}, errors.New("existing capacity must be reserved without provider reconciliation")
	}
	if request.Policy.Selection == "ephemeral" {
		request.WarmFloor = 0
	}
	info := b.Describe()
	if request.Desired < 0 {
		return compute.Plan{}, errors.New("desired capacity must not be negative")
	}
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
	providerIDs := append([]string(nil), request.ProviderOperationIDs...)
	defer func() {
		if err != nil {
			observation.ProviderOperationID = strings.Join(providerIDs, ",")
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
	spec := b.Target.AWSECSFargate
	standingReady, standingPending := 0, 0
	standingInstances := []compute.Instance(nil)
	if request.Policy.Selection != "ephemeral" && b.Target.StandingOwner == storage.CapacityOwnerApex && spec.WarmService != "" {
		conflict, inspectErr := b.providerAutoscalerConflict(ctx)
		if inspectErr != nil {
			return compute.Observation{}, fmt.Errorf("inspect ECS Application Auto Scaling ownership: %w", inspectErr)
		}
		if conflict && b.Target.StandingOwner == storage.CapacityOwnerApex {
			return compute.Observation{}, fmt.Errorf("%w: warm ECS service is registered with Application Auto Scaling", compute.ErrOwnershipConflict)
		}
		updateInput := map[string]any{"cluster": spec.Cluster, "service": spec.WarmService, "desiredCount": plan.Standing}
		if profile != nil && strings.TrimSpace(profile.TemplateRevision) != "" {
			updateInput["taskDefinition"] = strings.TrimSpace(profile.TemplateRevision)
		}
		if profile != nil && strings.EqualFold(strings.TrimSpace(profile.CapacityType), "spot") {
			updateInput["capacityProviderStrategy"] = []any{map[string]any{"capacityProvider": "FARGATE_SPOT", "weight": 1}}
		}
		if err := b.API.Call(ctx, "UpdateService", updateInput, nil); err != nil {
			return compute.Observation{}, err
		}
		var services describeServicesResponse
		if err := b.API.Call(ctx, "DescribeServices", map[string]any{"cluster": spec.Cluster, "services": []string{spec.WarmService}}, &services); err != nil {
			return compute.Observation{}, err
		}
		if len(services.Services) != 1 {
			return compute.Observation{}, errors.New("ECS warm service could not be observed after scaling")
		}
		var listed listTasksResponse
		if err := b.API.Call(ctx, "ListTasks", map[string]any{"cluster": spec.Cluster, "serviceName": spec.WarmService, "desiredStatus": "RUNNING"}, &listed); err != nil {
			return compute.Observation{}, err
		}
		standingInstances, err = b.describeTasks(ctx, listed.TaskARNs)
		if err != nil {
			return compute.Observation{}, err
		}
		standingReady = min(len(standingInstances), min(services.Services[0].RunningCount, plan.Standing))
		standingPending = max(plan.Standing-standingReady, services.Services[0].PendingCount)
	}
	for len(providerIDs) < plan.Launch {
		launchIndex := len(providerIDs)
		intent := request.LaunchIntents[launchIndex]
		generatorID := intent.GeneratorID // never reused across ephemeral launches
		clientToken := intent.ClientToken
		if len(clientToken) > 64 {
			clientToken = clientToken[:64]
		}
		taskDefinition := spec.BurstTaskDefinition
		if profile != nil && strings.TrimSpace(profile.TemplateRevision) != "" {
			taskDefinition = strings.TrimSpace(profile.TemplateRevision)
		}
		input := map[string]any{
			"cluster": spec.Cluster, "taskDefinition": taskDefinition,
			"count": 1, "clientToken": clientToken, "startedBy": boundedStartedBy(request.OperationID),
			"enableECSManagedTags": true, "propagateTags": "TASK_DEFINITION",
			"networkConfiguration": map[string]any{"awsvpcConfiguration": map[string]any{
				"subnets": spec.Subnets, "securityGroups": spec.SecurityGroups, "assignPublicIp": strings.ToUpper(first(spec.AssignPublicIP, "disabled")),
			}},
			"overrides": map[string]any{"containerOverrides": []any{map[string]any{
				"name": first(b.ContainerName, "generator"), "environment": []any{
					map[string]string{"name": "APEXLOAD_GENERATOR_ID", "value": generatorID},
					map[string]string{"name": "APEXLOAD_LAUNCH_NONCE", "value": intent.LaunchNonce},
					map[string]string{"name": "APEXLOAD_EXECUTION_TARGET_ID", "value": request.ExecutionTargetID},
					map[string]string{"name": "APEXLOAD_GENERATOR_LIFECYCLE", "value": "burst"},
					map[string]string{"name": "APEXLOAD_GENERATOR_CONNECTIVITY_MODE", "value": b.Target.ConnectivityMode},
				},
			}}},
			"tags": []any{
				map[string]string{"key": "apexload.io/controller-id", "value": b.ControllerID},
				map[string]string{"key": "apexload.io/execution-target-id", "value": request.ExecutionTargetID},
				map[string]string{"key": "apexload.io/pool-id", "value": request.FleetPoolID},
				map[string]string{"key": "apexload.io/operation-id", "value": request.OperationID},
				map[string]string{"key": "apexload.io/test-id", "value": request.TestID},
				map[string]string{"key": "apexload.io/generator-id", "value": generatorID},
				map[string]string{"key": "apexload.io/launch-nonce", "value": intent.LaunchNonce},
			},
		}
		spotCount, _ := request.Policy.SpotCount(request.Desired, b.Describe())
		useSpot := request.Policy.Selection == "ephemeral" && launchIndex >= plan.Launch-spotCount || request.Policy.Selection == "" && profile != nil && strings.EqualFold(strings.TrimSpace(profile.CapacityType), "spot")
		if useSpot {
			input["capacityProviderStrategy"] = []any{map[string]any{"capacityProvider": "FARGATE_SPOT", "weight": 1}}
		} else {
			input["launchType"] = "FARGATE"
		}
		var response runTaskResponse
		if err := b.API.Call(ctx, "RunTask", input, &response); err != nil {
			return compute.Observation{}, err
		}
		if len(response.Tasks) != 1 || response.Tasks[0].TaskARN == "" {
			return compute.Observation{}, errors.New("ECS RunTask returned no task ARN")
		}
		providerIDs = append(providerIDs, response.Tasks[0].TaskARN)
	}
	instances, err := b.describeTasks(ctx, providerIDs)
	if err != nil {
		return compute.Observation{}, err
	}
	instances = append(standingInstances, instances...)
	observation = compute.Observation{ProviderOperationID: strings.Join(providerIDs, ","), Requested: request.Desired, Instances: instances}
	for _, instance := range instances[len(standingInstances):] {
		observation.Observed++
		if instance.Healthy {
			observation.Ready++
		} else if strings.EqualFold(instance.State, "STOPPED") {
			observation.Failed++
		} else {
			observation.Pending++
		}
	}
	observation.Ready += standingReady
	observation.Pending += standingPending
	observation.Observed += min(plan.Standing, standingReady+standingPending)
	observation.Terminal = observation.Ready >= request.Desired
	if !observation.Terminal {
		observation.RetryAfter = 3 * time.Second
		observation.Message = "waiting for healthy ECS tasks"
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
	return nil, fmt.Errorf("runtime profile %q is not registered on the ECS execution target", id)
}

type describeScalableTargetsResponse struct {
	ScalableTargets []struct {
		ResourceID string `json:"resourceId"`
	} `json:"scalableTargets"`
}

func (b *Backend) providerAutoscalerConflict(ctx context.Context) (bool, error) {
	if b == nil || b.AutoscalingAPI == nil || b.Target == nil || b.Target.AWSECSFargate == nil || strings.TrimSpace(b.Target.AWSECSFargate.WarmService) == "" {
		return false, nil
	}
	spec := b.Target.AWSECSFargate
	resourceID := "service/" + awsResourceName(spec.Cluster) + "/" + awsResourceName(spec.WarmService)
	var response describeScalableTargetsResponse
	err := b.AutoscalingAPI.Call(ctx, "DescribeScalableTargets", map[string]any{
		"serviceNamespace":  "ecs",
		"resourceIds":       []string{resourceID},
		"scalableDimension": "ecs:service:DesiredCount",
	}, &response)
	if err != nil {
		return false, err
	}
	for _, target := range response.ScalableTargets {
		if strings.TrimSpace(target.ResourceID) == resourceID {
			return true, nil
		}
	}
	return false, nil
}

func (b *Backend) ecsAutoscalingOwnershipProbe(ctx context.Context) compute.ProbeResult {
	probe := compute.ProbeResult{Probe: "ecs_autoscaling_owner"}
	if b == nil || b.AutoscalingAPI == nil {
		probe.Optional = true
		probe.Message = "Application Auto Scaling inspection is unavailable"
		return probe
	}
	conflict, err := b.providerAutoscalerConflict(ctx)
	if err != nil {
		probe.Message = err.Error()
		return probe
	}
	if conflict && b.Target.StandingOwner == storage.CapacityOwnerApex {
		probe.Message = "warm ECS service is registered with Application Auto Scaling; set standing_owner=external or deregister the scalable target before APEX Load updates desiredCount"
		return probe
	}
	probe.OK = true
	probe.Message = "standing capacity ownership does not conflict with Application Auto Scaling"
	return probe
}

func awsResourceName(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if index := strings.LastIndex(value, "/"); index >= 0 {
		return value[index+1:]
	}
	return value
}

func (b *Backend) Cancel(ctx context.Context, request compute.CapacityRequest) error {
	if request.RestoreStanding && b.Target.StandingOwner == storage.CapacityOwnerApex && strings.TrimSpace(b.Target.AWSECSFargate.WarmService) != "" {
		if err := b.API.Call(ctx, "UpdateService", map[string]any{
			"cluster": b.Target.AWSECSFargate.Cluster, "service": b.Target.AWSECSFargate.WarmService,
			"desiredCount": max(request.RollbackStanding, 0),
		}, nil); err != nil {
			return err
		}
	}
	instances, err := b.describeTasks(ctx, request.ProviderOperationIDs)
	if err != nil {
		return err
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
		if err := b.API.Call(ctx, "StopTask", map[string]any{"cluster": b.Target.AWSECSFargate.Cluster, "task": instance.ProviderID, "reason": "APEX Load run complete and archive terminal"}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) Discover(ctx context.Context, request compute.CapacityRequest) ([]compute.Instance, error) {
	if len(request.ProviderOperationIDs) > 0 {
		return b.describeTasks(ctx, request.ProviderOperationIDs)
	}
	var listed listTasksResponse
	input := map[string]any{"cluster": b.Target.AWSECSFargate.Cluster, "desiredStatus": "RUNNING"}
	if strings.TrimSpace(request.OperationID) != "" && request.OperationID != "startup-reconcile" {
		input["startedBy"] = boundedStartedBy(request.OperationID)
	}
	if err := b.API.Call(ctx, "ListTasks", input, &listed); err != nil {
		return nil, err
	}
	instances, err := b.describeTasks(ctx, listed.TaskARNs)
	if err != nil {
		return nil, err
	}
	filtered := instances[:0]
	for _, instance := range instances {
		if instance.Tags["apexload.io/controller-id"] == b.ControllerID &&
			instance.Tags["apexload.io/pool-id"] == request.FleetPoolID &&
			instance.Tags["apexload.io/operation-id"] != "" &&
			(strings.TrimSpace(request.OperationID) == "" || request.OperationID == "startup-reconcile" || instance.Tags["apexload.io/operation-id"] == request.OperationID) {
			filtered = append(filtered, instance)
		}
	}
	return filtered, nil
}

func (b *Backend) Adopt(ctx context.Context, request compute.AdoptionRequest) (compute.Instance, error) {
	if b == nil || b.Target == nil || strings.TrimSpace(request.ProviderID) == "" || strings.TrimSpace(request.ExecutionTargetID) == "" || strings.TrimSpace(request.FleetPoolID) == "" ||
		strings.TrimSpace(request.OperationID) == "" || strings.TrimSpace(request.GeneratorID) == "" || strings.TrimSpace(request.LaunchNonce) == "" {
		return compute.Instance{}, errors.New("provider, execution-target, fleet-pool, operation, generator, and launch identities are required for adoption")
	}
	instances, err := b.describeTasks(ctx, []string{request.ProviderID})
	if err != nil {
		return compute.Instance{}, err
	}
	if len(instances) != 1 {
		return compute.Instance{}, fmt.Errorf("ECS task %q could not be observed for adoption", request.ProviderID)
	}
	instance := instances[0]
	if instance.Tags["apexload.io/controller-id"] != b.ControllerID ||
		instance.Tags["apexload.io/execution-target-id"] != request.ExecutionTargetID ||
		instance.Tags["apexload.io/pool-id"] != request.FleetPoolID ||
		instance.Tags["apexload.io/operation-id"] != request.OperationID ||
		instance.Tags["apexload.io/generator-id"] != request.GeneratorID ||
		instance.Tags["apexload.io/launch-nonce"] != request.LaunchNonce || !instance.IdentityAttested {
		return compute.Instance{}, errors.New("ECS task ownership tags do not authorize adoption")
	}
	return instance, nil
}

func (b *Backend) describeTasks(ctx context.Context, ids []string) ([]compute.Instance, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var response describeTasksResponse
	if err := b.API.Call(ctx, "DescribeTasks", map[string]any{"cluster": b.Target.AWSECSFargate.Cluster, "tasks": ids, "include": []string{"TAGS"}}, &response); err != nil {
		return nil, err
	}
	instances := make([]compute.Instance, 0, len(response.Tasks))
	for _, task := range response.Tasks {
		instance := compute.Instance{ProviderID: task.TaskARN, State: task.LastStatus, Healthy: task.LastStatus == "RUNNING", Tags: map[string]string{}}
		for _, tag := range task.Tags {
			instance.Tags[tag.Key] = tag.Value
		}
		instance.GeneratorID = instance.Tags["apexload.io/generator-id"]
		instance.IdentityAttested = instance.GeneratorID != "" && instance.Tags["apexload.io/launch-nonce"] != ""
		for _, attachment := range task.Attachments {
			for _, detail := range attachment.Details {
				if detail.Name == "privateIPv4Address" && strings.TrimSpace(detail.Value) != "" {
					port := b.GeneratorPort
					if port <= 0 {
						port = 9090
					}
					instance.Address = fmt.Sprintf("%s:%d", detail.Value, port)
				}
			}
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

type runTaskResponse struct {
	Tasks []struct {
		TaskARN string `json:"taskArn"`
	} `json:"tasks"`
}
type listTasksResponse struct {
	TaskARNs []string `json:"taskArns"`
}
type describeTasksResponse struct {
	Tasks []describedTask `json:"tasks"`
}
type describedTask struct {
	TaskARN     string           `json:"taskArn"`
	LastStatus  string           `json:"lastStatus"`
	Attachments []taskAttachment `json:"attachments"`
	Tags        []taskTag        `json:"tags"`
}
type taskAttachment struct {
	Details []taskAttachmentDetail `json:"details"`
}
type taskAttachmentDetail struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type taskTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type describeServicesResponse struct {
	Services []struct {
		ServiceARN     string `json:"serviceArn"`
		TaskDefinition string `json:"taskDefinition"`
		DesiredCount   int    `json:"desiredCount"`
		RunningCount   int    `json:"runningCount"`
		PendingCount   int    `json:"pendingCount"`
	} `json:"services"`
}
type describeTaskDefinitionResponse struct {
	TaskDefinition struct {
		TaskRoleARN          string `json:"taskRoleArn"`
		ContainerDefinitions []struct {
			Name string `json:"name"`
		} `json:"containerDefinitions"`
	} `json:"taskDefinition"`
}

func (d *describeTaskDefinitionResponse) containerNames() []string {
	if d == nil {
		return nil
	}
	names := make([]string, 0, len(d.TaskDefinition.ContainerDefinitions))
	for _, container := range d.TaskDefinition.ContainerDefinitions {
		names = append(names, container.Name)
	}
	return names
}

func requiredTaskSidecars(profiles []storage.RuntimeProfile) []string {
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

func taskRoleProbe(resourceErr error, actual, expected string) compute.ProbeResult {
	err := resourceErr
	if err == nil && strings.TrimSpace(actual) == "" {
		err = errors.New("task definition has no task role")
	}
	if err == nil && strings.TrimSpace(expected) != "" && actual != expected {
		err = fmt.Errorf("task definition role %q does not match expected role %q", actual, expected)
	}
	return compute.ProbeResult{Probe: "task_role_identity", OK: err == nil, Message: errorMessage(err, "task role is attached and matches the registered identity")}
}

func taskSidecarProbe(resourceErr error, required, actual []string) compute.ProbeResult {
	if resourceErr != nil {
		return compute.ProbeResult{Probe: "task_definition_sidecars", Message: resourceErr.Error()}
	}
	if len(required) == 0 {
		return compute.ProbeResult{Probe: "task_definition_sidecars", OK: true, Optional: true, Message: "runtime profile declares no required sidecars"}
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
		return compute.ProbeResult{Probe: "task_definition_sidecars", Message: "missing required sidecars: " + strings.Join(missing, ", ")}
	}
	return compute.ProbeResult{Probe: "task_definition_sidecars", OK: true, Message: "all runtime-profile sidecars are present"}
}

func boundedStartedBy(value string) string {
	value = strings.NewReplacer("/", "-", ":", "-").Replace(value)
	if len(value) > 36 {
		value = value[:36]
	}
	return value
}
func first(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
func errorMessage(err error, success string) string {
	if err != nil {
		return err.Error()
	}
	return success
}
