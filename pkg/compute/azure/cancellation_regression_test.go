package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/compute"
	"github.com/jasonczech/apexload/pkg/storage"
)

type cloudLaunchFailureAPI struct {
	calls        []call
	resources    []map[string]any
	instances    bool
	failDescribe bool
}

func (a *cloudLaunchFailureAPI) Do(_ context.Context, method, path string, body, output any) (string, time.Duration, error) {
	a.calls = append(a.calls, call{method: method, path: path, body: body})
	list := strings.Contains(path, "/executions?") || strings.Contains(path, "/containerGroups?")
	if method == http.MethodGet && list {
		encode(map[string]any{"value": a.resources}, output)
		return "", 0, nil
	}
	if method == http.MethodGet && strings.Contains(path, "/containerGroups/template?") {
		encode((&containerGroupAPI{}).group("template"), output)
		return "", 0, nil
	}
	if method == http.MethodPut || (method == http.MethodPost && strings.Contains(path, "/start?")) {
		obj := body.(map[string]any)
		var resource map[string]any
		if a.instances {
			resource = (&containerGroupAPI{tags: obj["tags"].(map[string]string)}).group(pathBase(strings.Split(path, "?")[0]))
		} else {
			resource = map[string]any{"name": fmt.Sprintf("execution-%d", len(a.resources)+1), "properties": map[string]any{"status": "Running", "template": obj}}
		}
		a.resources = append(a.resources, resource)
		if len(a.resources) == 2 && !a.failDescribe {
			return "", 0, errors.New("accepted launch response lost")
		}
		encode(resource, output)
		return "", 0, nil
	}
	if method == http.MethodGet {
		if a.failDescribe {
			return "", 0, errors.New("observation unavailable")
		}
		for _, resource := range a.resources {
			if strings.Contains(path, "/"+resource["name"].(string)+"?") {
				encode(resource, output)
				return "", 0, nil
			}
		}
	}
	return "", 0, nil
}

func TestAzureFailedReconcileRetainsAndCancelsLaunches(t *testing.T) {
	for _, aci := range []bool{false, true} {
		for _, observationFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("aci=%t/observe=%t", aci, observationFailure), func(t *testing.T) {
				api := &cloudLaunchFailureAPI{instances: aci, failDescribe: observationFailure}
				var backend compute.Backend
				if aci {
					backend = &InstancesBackend{API: api, ControllerID: "controller-1", SubscriptionID: "sub", Target: aciTarget("/subnets/a")}
				} else {
					backend = &Backend{API: api, ControllerID: "controller-1", SubscriptionID: "sub", Target: &storage.ExecutionTarget{
						StandingOwner: storage.CapacityOwnerExternal, BurstOwner: storage.CapacityOwnerApex,
						AzureContainerApps: &storage.AzureContainerAppsTargetSpec{ResourceGroup: "rg", WarmContainerApp: "external", BurstJob: "job"},
					}}
				}
				request := compute.CapacityRequest{OperationID: "op", ExecutionTargetID: "target", FleetPoolID: "pool", Desired: 2, RestoreStanding: true,
					LaunchIntents: []compute.LaunchIntent{{ClientToken: "c1", GeneratorID: "g1", LaunchNonce: "n1"}, {ClientToken: "c2", GeneratorID: "g2", LaunchNonce: "n2"}}}
				obs, err := backend.Reconcile(context.Background(), request)
				if err == nil || obs.ProviderOperationID == "" {
					t.Fatalf("lost identity: %+v, %v", obs, err)
				}
				request.ProviderOperationIDs = strings.Split(obs.ProviderOperationID, ",")
				wantIDs := 1
				if observationFailure {
					wantIDs = 2
				}
				if len(request.ProviderOperationIDs) != wantIDs {
					t.Fatalf("IDs=%q", obs.ProviderOperationID)
				}
				api.failDescribe = false
				if err := backend.Cancel(context.Background(), request); err != nil {
					t.Fatal(err)
				}
				stopped := 0
				for _, c := range api.calls {
					if c.method == http.MethodPatch {
						t.Fatal("mutated external warm app")
					}
					if c.method == http.MethodDelete || strings.Contains(c.path, "/stop?") {
						stopped++
					}
				}
				if stopped != 2 {
					t.Fatalf("stopped %d resources", stopped)
				}
			})
		}
	}
}
