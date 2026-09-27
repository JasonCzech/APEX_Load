package k8s_test

import (
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
				if trigger.Type != "prometheus" || trigger.Metadata["query"] != "max(apexload_pod_reserved_replica_floor)" || trigger.Metadata["threshold"] != "1" || trigger.Metadata["activationThreshold"] != "0" {
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
