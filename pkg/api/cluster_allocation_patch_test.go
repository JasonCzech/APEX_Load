package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

func statefulSet(containers ...string) kubeops.StatefulSetRef {
	set := kubeops.StatefulSetRef{Name: "apexload-generator", Namespace: "apex"}
	for _, name := range containers {
		set.Containers = append(set.Containers, kubeops.ContainerRef{Name: name})
	}
	return set
}

// TestResolveGeneratorContainer covers the hazard that the shipped Helm chart
// runs a java-vuser-runner sidecar beside the generator: picking by index would
// resize the sidecar.
func TestResolveGeneratorContainer(t *testing.T) {
	cases := []struct {
		name       string
		set        kubeops.StatefulSetRef
		configured string
		want       string
		wantErr    string
	}{
		{
			name: "the conventional generator container wins over a sidecar",
			set:  statefulSet("java-vuser-runner", "generator"),
			want: "generator",
		},
		{
			name:       "an explicit configuration wins",
			set:        statefulSet("generator", "java-vuser-runner"),
			configured: "java-vuser-runner",
			want:       "java-vuser-runner",
		},
		{
			name: "a single container needs no convention",
			set:  statefulSet("loadgen"),
			want: "loadgen",
		},
		{
			name:    "an ambiguous pod is refused rather than guessed",
			set:     statefulSet("sidecar-a", "sidecar-b"),
			wantErr: "cannot determine which container",
		},
		{
			name:       "a configured container that does not exist is an error",
			set:        statefulSet("generator"),
			configured: "missing",
			wantErr:    "has no container named",
		},
		{
			name:    "a pod with no containers is an error",
			set:     statefulSet(),
			wantErr: "has no containers",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveGeneratorContainer(tc.set, tc.configured)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("container = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildAllocationPatchRemovesStaleNodeSelectorKeys pins the strategic-merge
// semantic that makes removal work: nodeSelector is a map, so a key the
// administrator deleted survives forever unless it is sent as an explicit null.
func TestBuildAllocationPatchRemovesStaleNodeSelectorKeys(t *testing.T) {
	set := statefulSet("generator")
	set.NodeSelector = map[string]string{
		storage.NodeSelectorAgentPool: "old-pool",
		"retired":                     "yes",
	}
	allocation := &storage.PoolAllocation{
		CPURequest: "1", CPULimit: "2", MemoryRequest: "2Gi", MemoryLimit: "4Gi",
		NodePool: "loadgen", Zone: "eastus-2",
	}

	patch := buildAllocationPatch(set, "generator", allocation)
	podSpec := patch["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	selector := podSpec["nodeSelector"].(map[string]any)

	if selector[storage.NodeSelectorAgentPool] != "loadgen" {
		t.Errorf("agentpool = %v, want the new pool", selector[storage.NodeSelectorAgentPool])
	}
	if selector[storage.NodeSelectorZone] != "eastus-2" {
		t.Errorf("zone = %v, want eastus-2", selector[storage.NodeSelectorZone])
	}
	value, present := selector["retired"]
	if !present || value != nil {
		t.Errorf("nodeSelector[retired] = (%v, present=%v), want an explicit null so the key is removed", value, present)
	}

	containers := podSpec["containers"].([]map[string]any)
	if len(containers) != 1 || containers[0]["name"] != "generator" {
		t.Fatalf("containers = %+v, want exactly the named generator container", containers)
	}
	resources := containers[0]["resources"].(map[string]any)
	if resources["requests"].(map[string]any)["cpu"] != "1" {
		t.Errorf("CPU request = %v, want 1", resources["requests"].(map[string]any)["cpu"])
	}
	if resources["limits"].(map[string]any)["memory"] != "4Gi" {
		t.Errorf("memory limit = %v, want 4Gi", resources["limits"].(map[string]any)["memory"])
	}
}

// TestBuildAllocationPatchClearsTolerationsWithAnEmptyList pins that clearing
// works: tolerations has no merge key, so the list replaces wholesale, but only
// if it is sent rather than omitted.
func TestBuildAllocationPatchClearsTolerationsWithAnEmptyList(t *testing.T) {
	patch := buildAllocationPatch(statefulSet("generator"), "generator", &storage.PoolAllocation{CPURequest: "1"})
	podSpec := patch["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	tolerations, present := podSpec["tolerations"]
	if !present {
		t.Fatal("tolerations was omitted; an empty list must be sent to clear existing tolerations")
	}
	if list, ok := tolerations.([]map[string]any); !ok || len(list) != 0 {
		t.Fatalf("tolerations = %v, want an empty list", tolerations)
	}
}

func TestBuildAllocationPatchOmitsTolerationValueForExists(t *testing.T) {
	allocation := &storage.PoolAllocation{
		Tolerations: []storage.Toleration{{Key: "spot", Operator: "Exists", Effect: "NoSchedule"}},
	}
	patch := buildAllocationPatch(statefulSet("generator"), "generator", allocation)
	podSpec := patch["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	entry := podSpec["tolerations"].([]map[string]any)[0]
	if _, present := entry["value"]; present {
		// Kubernetes rejects a value alongside the Exists operator.
		t.Fatalf("toleration = %+v, want no value key for the Exists operator", entry)
	}
}

func TestVerifyGeneratorWorkloadOwnership(t *testing.T) {
	set := statefulSet("generator")
	set.PodTemplateJSON = map[string]string{"app": "apexload-generator", "tier": "load"}

	if err := verifyGeneratorWorkloadOwnership(set, "app=apexload-generator"); err != nil {
		t.Fatalf("a matching selector was refused: %v", err)
	}
	if err := verifyGeneratorWorkloadOwnership(set, "app=apexload-generator,tier=load"); err != nil {
		t.Fatalf("a matching multi-key selector was refused: %v", err)
	}
	// A co-tenant workload in the same namespace must not be patchable.
	if err := verifyGeneratorWorkloadOwnership(set, "app=someone-elses-service"); err == nil {
		t.Fatal("a workload that does not match the generator selector was accepted")
	}
	// An unconfigured selector cannot verify anything, so it must not refuse.
	if err := verifyGeneratorWorkloadOwnership(set, ""); err != nil {
		t.Fatalf("an empty selector was treated as a mismatch: %v", err)
	}
}

func TestAllocationTargetWarnings(t *testing.T) {
	set := statefulSet("generator")
	set.Annotations = map[string]string{"app.kubernetes.io/managed-by": "Helm"}
	set.UpdateStrategy = "OnDelete"

	warnings := strings.Join(allocationTargetWarnings(set), " | ")
	if !strings.Contains(warnings, "Helm") {
		t.Errorf("warnings = %q, want a GitOps-revert warning", warnings)
	}
	if !strings.Contains(warnings, "OnDelete") {
		t.Errorf("warnings = %q, want an OnDelete rollout warning", warnings)
	}
	if len(allocationTargetWarnings(statefulSet("generator"))) != 0 {
		t.Error("an unmanaged RollingUpdate workload produced warnings")
	}
}

// TestAllocationDriftIsReportedAsA4xx guards a trap in writeError: it replaces
// ANY 5xx message with a generic "internal server error" plus a reference ID.
// The cluster-patched-but-not-persisted case is the one response whose detail is
// the entire point, so it must not be sent as a 5xx.
func TestAllocationDriftIsReportedAsA4xx(t *testing.T) {
	recorder := httptest.NewRecorder()
	response := FleetPoolAllocationResponse{PoolID: "pool-1", Applied: true}
	writeJSONError(recorder, http.StatusConflict,
		"the allocation was applied to the cluster but could not be saved: the fleet pool changed since it was loaded. "+
			"The live workload now differs from the stored allocation — reload the pool and re-apply to reconcile them.",
		response)

	if recorder.Code >= http.StatusInternalServerError {
		t.Fatalf("status = %d; a 5xx would have its message replaced by a generic error", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "applied to the cluster but could not be saved") {
		t.Fatalf("body = %s, want the drift detail preserved", body)
	}
	if !strings.Contains(body, "\"applied\":true") {
		t.Fatalf("body = %s, want applied=true so the caller knows the cluster changed", body)
	}
}

func TestValidateAllocationQuantities(t *testing.T) {
	cases := []struct {
		name       string
		allocation *storage.PoolAllocation
		wantErr    string
	}{
		{"empty is valid", &storage.PoolAllocation{}, ""},
		{"valid quantities", &storage.PoolAllocation{CPURequest: "500m", CPULimit: "2", MemoryRequest: "1Gi", MemoryLimit: "4Gi"}, ""},
		// Cross-unit comparison must be numeric: "1" exceeds "500m".
		{"request above limit across units", &storage.PoolAllocation{CPURequest: "1", CPULimit: "500m"}, "cannot exceed"},
		{"equal across units is fine", &storage.PoolAllocation{CPURequest: "1", CPULimit: "1000m"}, ""},
		{"memory request above limit", &storage.PoolAllocation{MemoryRequest: "4Gi", MemoryLimit: "1Gi"}, "cannot exceed"},
		{"limit only is fine", &storage.PoolAllocation{CPULimit: "2"}, ""},
		{"malformed cpu", &storage.PoolAllocation{CPURequest: "2c"}, "invalid CPU quantity"},
		{"malformed memory", &storage.PoolAllocation{MemoryRequest: "512mi"}, "invalid memory quantity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAllocationQuantities(tc.allocation)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
