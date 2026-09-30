package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

type launchFailureAPI struct {
	fakeAPI
	starts       int
	failDescribe bool
	hideAccepted bool
	tasks        []describedTask
}

func (f *launchFailureAPI) Call(ctx context.Context, action string, input, output any) error {
	switch action {
	case "RunTask":
		f.starts++
		raw, _ := json.Marshal(input)
		var launch struct {
			Tags []taskTag `json:"tags"`
		}
		_ = json.Unmarshal(raw, &launch)
		id := fmt.Sprintf("task-%d", f.starts)
		f.tasks = append(f.tasks, describedTask{TaskARN: id, LastStatus: "RUNNING", Tags: launch.Tags})
		if f.starts == 2 && !f.failDescribe {
			return errors.New("accepted launch response lost")
		}
		return json.Unmarshal([]byte(fmt.Sprintf(`{"tasks":[{"taskArn":%q}]}`, id)), output)
	case "ListTasks":
		if !f.hideAccepted {
			for _, task := range f.tasks {
				output.(*listTasksResponse).TaskARNs = append(output.(*listTasksResponse).TaskARNs, task.TaskARN)
			}
		}
		return nil
	case "DescribeTasks":
		if f.failDescribe {
			return errors.New("observation unavailable")
		}
		raw, _ := json.Marshal(input)
		var req struct {
			Tasks []string `json:"tasks"`
		}
		_ = json.Unmarshal(raw, &req)
		for _, id := range req.Tasks {
			for _, task := range f.tasks {
				if id == task.TaskARN {
					output.(*describeTasksResponse).Tasks = append(output.(*describeTasksResponse).Tasks, task)
				}
			}
		}
		return nil
	}
	return f.fakeAPI.Call(ctx, action, input, output)
}

func TestFargateFailedReconcileRetainsAndCancelsLaunches(t *testing.T) {
	for _, observationFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(observationFailure), func(t *testing.T) {
			api := &launchFailureAPI{failDescribe: observationFailure}
			backend := &Backend{API: api, ControllerID: "controller-1", Target: &storage.ExecutionTarget{
				StandingOwner: storage.CapacityOwnerExternal, BurstOwner: storage.CapacityOwnerApex,
				AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "cluster", WarmService: "external", BurstTaskDefinition: "burst"},
			}}
			request := compute.CapacityRequest{OperationID: "op", ExecutionTargetID: "target", FleetPoolID: "pool", Desired: 2, RestoreStanding: true,
				LaunchIntents: []compute.LaunchIntent{{ClientToken: "c1", GeneratorID: "g1", LaunchNonce: "n1"}, {ClientToken: "c2", GeneratorID: "g2", LaunchNonce: "n2"}}}
			obs, err := backend.Reconcile(context.Background(), request)
			if err == nil || obs.ProviderOperationID == "" {
				t.Fatalf("lost launch identity: %+v, %v", obs, err)
			}
			request.ProviderOperationIDs = []string{"task-1"}
			if observationFailure {
				if obs.ProviderOperationID != "task-1,task-2" {
					t.Fatalf("IDs=%q", obs.ProviderOperationID)
				}
				request.ProviderOperationIDs = append(request.ProviderOperationIDs, "task-2")
			}
			api.failDescribe = false
			if err := backend.Cancel(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			stopped := map[string]bool{}
			for _, call := range api.calls {
				if call.action == "UpdateService" {
					t.Fatal("mutated externally owned warm service")
				}
				if call.action == "StopTask" {
					stopped[call.input["task"].(string)] = true
				}
			}
			if !stopped["task-1"] || !stopped["task-2"] {
				t.Fatalf("stopped=%v", stopped)
			}
		})
	}
}

func TestFargateCancellationDoesNotConfirmInvisibleLaunch(t *testing.T) {
	api := &launchFailureAPI{hideAccepted: true, tasks: []describedTask{{TaskARN: "task-1", LastStatus: "RUNNING"}}}
	backend := &Backend{API: api, ControllerID: "controller", Target: &storage.ExecutionTarget{AWSECSFargate: &storage.AWSECSFargateTargetSpec{Cluster: "cluster"}}}
	err := backend.Cancel(context.Background(), compute.CapacityRequest{OperationID: "op", FleetPoolID: "pool", ProviderOperationIDs: []string{"task-1"}, LaunchIntents: []compute.LaunchIntent{{LaunchNonce: "n1"}, {LaunchNonce: "n2"}}})
	if err == nil {
		t.Fatal("confirmed cancellation while a launch is still unobservable")
	}
	if len(api.calls) != 1 || api.calls[0].action != "StopTask" {
		t.Fatalf("known task not stopped: %v", api.calls)
	}
}
