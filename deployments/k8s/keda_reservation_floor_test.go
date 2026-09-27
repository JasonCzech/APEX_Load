package k8s_test

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every trigger-list overlay must retain the ordinal floor: replacing the list
// must not allow KEDA to delete an occupied high StatefulSet ordinal.
func TestKEDAReservationFloorParity(t *testing.T) {
	for _, path := range []string{
		"aks/generator-keda-scaledobject.yaml",
		"eks/generator-keda-scaledobject.yaml",
		"local/generator-keda-scaledobject.yaml",
		"onprem/generator-keda-scaledobject.yaml",
		"onprem-autoscaling/generator-keda-scaledobject.yaml",
		"aks-azure-monitor/generator-keda-azure-monitor-patch.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			var document struct {
				Spec struct {
					Triggers []struct {
						Type              string            `yaml:"type"`
						Metadata          map[string]string `yaml:"metadata"`
						AuthenticationRef struct {
							Name string `yaml:"name"`
						} `yaml:"authenticationRef"`
					} `yaml:"triggers"`
				} `yaml:"spec"`
			}
			if err := yaml.Unmarshal([]byte(readTextFile(t, path)), &document); err != nil {
				t.Fatal(err)
			}
			floors := 0
			for _, trigger := range document.Spec.Triggers {
				if trigger.Metadata["metricName"] != "apexload_reserved_replica_floor" {
					continue
				}
				floors++
				if trigger.Type != "prometheus" || !strings.Contains(trigger.Metadata["query"], `signal="reserved_replica_floor"`) || !strings.Contains(trigger.Metadata["query"], "apexload_fleet_autoscaling_valid_until > time()") || trigger.Metadata["ignoreNullValues"] != "false" || trigger.Metadata["threshold"] != "1" || trigger.Metadata["activationThreshold"] != "0" {
					t.Fatalf("invalid reservation floor trigger: %+v", trigger)
				}
				if trigger.Metadata["serverAddress"] != document.Spec.Triggers[0].Metadata["serverAddress"] || trigger.AuthenticationRef != document.Spec.Triggers[0].AuthenticationRef {
					t.Fatal("reservation floor must use the same metrics server and authentication as demand triggers")
				}
			}
			if floors != 1 {
				t.Fatalf("reservation floor count=%d, want one", floors)
			}
		})
	}
}

// Keep the executable PromQL fixtures tied to the shipped query expressions.
func TestKEDAQueriesMatchPrometheusFixtures(t *testing.T) {
	fixture := readTextFile(t, "../../scripts/testdata/keda-promql.json")
	var suite struct {
		Tests []struct {
			Cases []struct {
				Expr string `json:"expr"`
			} `json:"promql_expr_test"`
		} `json:"tests"`
	}
	if err := json.Unmarshal([]byte(fixture), &suite); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, test := range suite.Tests {
		for _, item := range test.Cases {
			known[item.Expr] = true
		}
	}
	for _, path := range []string{"aks/generator-keda-scaledobject.yaml", "eks/generator-keda-scaledobject.yaml", "local/generator-keda-scaledobject.yaml", "onprem/generator-keda-scaledobject.yaml", "onprem-autoscaling/generator-keda-scaledobject.yaml", "aks-azure-monitor/generator-keda-azure-monitor-patch.yaml", "../helm/apexload/templates/keda.yaml"} {
		count := 0
		for _, line := range strings.Split(readTextFile(t, path), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "query:") {
				continue
			}
			query := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "query:"))
			query = strings.ReplaceAll(query, "${APEXLOAD_FLEET_ID}", "fleet-a")
			query = strings.ReplaceAll(query, "apexload-local", "fleet-a")
			query = strings.ReplaceAll(query, `{{ include "apexload.fleetID" . }}`, "fleet-a")
			if !known[query] {
				t.Errorf("untested query in %s: %s", path, query)
			}
			count++
		}
		if count != 6 {
			t.Errorf("%s has %d queries, want six", path, count)
		}
	}
}

func TestHelmFleetIdentityMatchesEveryTrigger(t *testing.T) {
	for _, azure := range []bool{false, true} {
		args := []string{"--namespace", "perf", "--set", "keda.fleetID=cluster-a/perf"}
		if azure {
			args = append(args, "--set", "keda.azureMonitor.enabled=true", "--set", "keda.azureMonitor.identityId=11111111-1111-4111-8111-111111111111", "--set", "keda.azureMonitor.serverAddress=https://example.monitor.azure.com")
		}
		documents := renderHAChart(t, args...)
		configFound, scalerFound := false, false
		for _, document := range documents {
			if document.Kind == "ConfigMap" && document.Data["APEXLOAD_FLEET_ID"] == "cluster-a/perf" {
				configFound = true
			}
			if document.Kind != "ScaledObject" {
				continue
			}
			scalerFound = true
			if len(document.Spec.Triggers) != 6 {
				t.Fatalf("trigger count %d", len(document.Spec.Triggers))
			}
			for _, trigger := range document.Spec.Triggers {
				if !strings.Contains(trigger.Metadata["query"], `fleet="cluster-a/perf"`) || trigger.Metadata["ignoreNullValues"] != "false" {
					t.Fatalf("unscoped or fail-open trigger: %+v", trigger)
				}
			}
		}
		if !configFound || !scalerFound {
			t.Fatalf("missing fleet wiring: config=%v scaler=%v", configFound, scalerFound)
		}
	}
}
