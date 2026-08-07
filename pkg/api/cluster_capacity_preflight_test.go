package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

func quota(name string, hard, used map[string]string) kubeops.ResourceQuotaRef {
	return kubeops.ResourceQuotaRef{Name: name, Hard: hard, Used: used}
}

func node(name, pool, zone, cpu, memory string) kubeops.NodeRef {
	return kubeops.NodeRef{
		Name:        name,
		Labels:      map[string]string{storage.NodeSelectorAgentPool: pool, storage.NodeSelectorZone: zone},
		Allocatable: kubeops.ResourceList{"cpu": cpu, "memory": memory},
	}
}

func TestEvaluateAllocationPreflightQuota(t *testing.T) {
	cases := []struct {
		name            string
		snapshot        clusterCapacitySnapshot
		allocation      *storage.PoolAllocation
		currentReplicas int
		targetReplicas  int
		wantAllowed     bool
		wantReason      string
	}{
		{
			name: "scale up beyond the CPU quota is blocked",
			snapshot: clusterCapacitySnapshot{
				Quotas:          []kubeops.ResourceQuotaRef{quota("apex", map[string]string{"requests.cpu": "8"}, map[string]string{"requests.cpu": "2"})},
				CurrentRequests: kubeops.ResourceList{"cpu": "1"},
				NodesAvailable:  false,
			},
			allocation:      &storage.PoolAllocation{CPURequest: "1"},
			currentReplicas: 2,
			targetReplicas:  10,
			wantAllowed:     false,
			wantReason:      "requests.cpu",
		},
		{
			// The delta is computed against current state, so shrinking can
			// never be refused for exceeding a quota it is moving away from.
			name: "scale down under an already-full quota is allowed",
			snapshot: clusterCapacitySnapshot{
				Quotas:          []kubeops.ResourceQuotaRef{quota("apex", map[string]string{"requests.cpu": "8"}, map[string]string{"requests.cpu": "8"})},
				CurrentRequests: kubeops.ResourceList{"cpu": "1"},
				NodesAvailable:  false,
			},
			allocation:      &storage.PoolAllocation{CPURequest: "1"},
			currentReplicas: 8,
			targetReplicas:  2,
			wantAllowed:     true,
		},
		{
			name: "no quota in the namespace allows the change",
			snapshot: clusterCapacitySnapshot{
				CurrentRequests: kubeops.ResourceList{"cpu": "1"},
				NodesAvailable:  false,
			},
			allocation:      &storage.PoolAllocation{CPURequest: "2"},
			currentReplicas: 1,
			targetReplicas:  5,
			wantAllowed:     true,
		},
		{
			name: "pod count quota is enforced",
			snapshot: clusterCapacitySnapshot{
				Quotas:         []kubeops.ResourceQuotaRef{quota("apex", map[string]string{"pods": "6"}, map[string]string{"pods": "4"})},
				NodesAvailable: false,
			},
			allocation:      &storage.PoolAllocation{},
			currentReplicas: 4,
			targetReplicas:  10,
			wantAllowed:     false,
			wantReason:      "pods",
		},
		{
			name: "memory quota is enforced",
			snapshot: clusterCapacitySnapshot{
				Quotas: []kubeops.ResourceQuotaRef{quota("apex",
					map[string]string{"requests.memory": "16Gi"}, map[string]string{"requests.memory": "8Gi"})},
				NodesAvailable: false,
			},
			allocation:      &storage.PoolAllocation{MemoryRequest: "4Gi"},
			currentReplicas: 0,
			targetReplicas:  4,
			wantAllowed:     false,
			wantReason:      "requests.memory",
		},
		{
			// Kubernetes defaults an unset request to the limit, so the quota
			// projection must count the limit or it would under-report.
			name: "a limit with no request still counts toward quota",
			snapshot: clusterCapacitySnapshot{
				Quotas:         []kubeops.ResourceQuotaRef{quota("apex", map[string]string{"requests.cpu": "2"}, nil)},
				NodesAvailable: false,
			},
			allocation:      &storage.PoolAllocation{CPULimit: "1"},
			currentReplicas: 0,
			targetReplicas:  4,
			wantAllowed:     false,
			wantReason:      "requests.cpu",
		},
		{
			name: "the bare cpu quota alias is honored",
			snapshot: clusterCapacitySnapshot{
				Quotas:         []kubeops.ResourceQuotaRef{quota("apex", map[string]string{"cpu": "2"}, map[string]string{"cpu": "1"})},
				NodesAvailable: false,
			},
			allocation:      &storage.PoolAllocation{CPURequest: "1"},
			currentReplicas: 0,
			targetReplicas:  4,
			wantAllowed:     false,
			wantReason:      "cpu",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := evaluateAllocationPreflight(tc.snapshot, tc.allocation, tc.currentReplicas, tc.targetReplicas)
			if result.Allowed != tc.wantAllowed {
				t.Fatalf("Allowed = %v, want %v (reasons: %v)", result.Allowed, tc.wantAllowed, result.Reasons)
			}
			if tc.wantReason != "" && !strings.Contains(strings.Join(result.Reasons, " "), tc.wantReason) {
				t.Fatalf("Reasons = %v, want one mentioning %q", result.Reasons, tc.wantReason)
			}
		})
	}
}

// TestPreflightWarnsRatherThanBlocksWithoutNodeData pins the graceful
// degradation the optional node ClusterRole requires.
func TestPreflightWarnsRatherThanBlocksWithoutNodeData(t *testing.T) {
	snapshot := clusterCapacitySnapshot{NodesAvailable: false}
	result := evaluateAllocationPreflight(snapshot, &storage.PoolAllocation{CPURequest: "64"}, 0, 1)
	if !result.Allowed {
		t.Fatalf("Allowed = false, want true; missing node data must warn, not block (reasons: %v)", result.Reasons)
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, " "), "node capacity") {
		t.Fatalf("Warnings = %v, want a node-capacity warning", result.Warnings)
	}
}

func TestPreflightPodFit(t *testing.T) {
	nodes := []kubeops.NodeRef{
		node("loadgen-0", "loadgen", "eastus-2", "4", "16Gi"),
		node("system-0", "system", "eastus-1", "2", "8Gi"),
	}

	// A pod that no eligible node can hold is guaranteed unschedulable.
	tooBig := evaluateAllocationPreflight(
		clusterCapacitySnapshot{Nodes: nodes, NodesAvailable: true},
		&storage.PoolAllocation{CPURequest: "64", NodePool: "loadgen"}, 0, 1)
	if tooBig.Allowed {
		t.Fatal("a pod larger than every eligible node was admitted")
	}

	// A pod that fits is admitted even though the cluster could not hold many.
	fits := evaluateAllocationPreflight(
		clusterCapacitySnapshot{Nodes: nodes, NodesAvailable: true},
		&storage.PoolAllocation{CPURequest: "2", MemoryRequest: "4Gi", NodePool: "loadgen"}, 0, 50)
	if !fits.Allowed {
		t.Fatalf("a fitting pod was refused: %v", fits.Reasons)
	}

	// Placement that matches no node at all is reported distinctly.
	noMatch := evaluateAllocationPreflight(
		clusterCapacitySnapshot{Nodes: nodes, NodesAvailable: true},
		&storage.PoolAllocation{CPURequest: "1", NodePool: "gpu"}, 0, 1)
	if noMatch.Allowed || !strings.Contains(strings.Join(noMatch.Reasons, " "), "no schedulable node matches") {
		t.Fatalf("Reasons = %v, want a placement-mismatch reason", noMatch.Reasons)
	}
}

// TestPreflightCatchesUnsatisfiablePlacementWithoutResourceRequests covers the
// case a resource-only guard would miss: an allocation that sets a node pool or
// zone but no CPU/memory still has to land somewhere, and a typo'd pool name
// otherwise leaves the pods Pending with nothing explaining why.
func TestPreflightCatchesUnsatisfiablePlacementWithoutResourceRequests(t *testing.T) {
	nodes := []kubeops.NodeRef{node("loadgen-0", "loadgen", "eastus-2", "4", "16Gi")}
	snapshot := clusterCapacitySnapshot{Nodes: nodes, NodesAvailable: true}

	typo := evaluateAllocationPreflight(snapshot, &storage.PoolAllocation{NodePool: "loadgenn"}, 0, 1)
	if typo.Allowed {
		t.Fatal("a node pool matching no node was admitted when no resources were requested")
	}
	if !strings.Contains(strings.Join(typo.Reasons, " "), "no schedulable node matches") {
		t.Fatalf("Reasons = %v, want a placement-mismatch reason", typo.Reasons)
	}

	wrongZone := evaluateAllocationPreflight(snapshot, &storage.PoolAllocation{Zone: "westus-1"}, 0, 1)
	if wrongZone.Allowed {
		t.Fatal("a zone matching no node was admitted when no resources were requested")
	}

	valid := evaluateAllocationPreflight(snapshot, &storage.PoolAllocation{NodePool: "loadgen"}, 0, 1)
	if !valid.Allowed {
		t.Fatalf("a satisfiable placement was refused: %v", valid.Reasons)
	}

	// An allocation with nothing to validate must stay allowed.
	empty := evaluateAllocationPreflight(snapshot, &storage.PoolAllocation{}, 0, 1)
	if !empty.Allowed {
		t.Fatalf("an empty allocation was refused: %v", empty.Reasons)
	}
	// Tolerations only widen placement, so they alone cannot make it fail.
	tolerationOnly := evaluateAllocationPreflight(snapshot,
		&storage.PoolAllocation{Tolerations: []storage.Toleration{{Key: "spot", Operator: "Exists"}}}, 0, 1)
	if !tolerationOnly.Allowed {
		t.Fatalf("a toleration-only allocation was refused: %v", tolerationOnly.Reasons)
	}
}

func TestPreflightRespectsTaintsAndTolerations(t *testing.T) {
	tainted := node("loadgen-0", "loadgen", "eastus-2", "4", "16Gi")
	tainted.Taints = []kubeops.Taint{{Key: "apexload/loadgen", Value: "true", Effect: "NoSchedule"}}
	snapshot := clusterCapacitySnapshot{Nodes: []kubeops.NodeRef{tainted}, NodesAvailable: true}

	untolerated := evaluateAllocationPreflight(snapshot, &storage.PoolAllocation{CPURequest: "1", NodePool: "loadgen"}, 0, 1)
	if untolerated.Allowed {
		t.Fatal("a pod with no toleration was admitted onto a NoSchedule-tainted node")
	}

	tolerated := evaluateAllocationPreflight(snapshot, &storage.PoolAllocation{
		CPURequest: "1", NodePool: "loadgen",
		Tolerations: []storage.Toleration{{Key: "apexload/loadgen", Operator: "Equal", Value: "true", Effect: "NoSchedule"}},
	}, 0, 1)
	if !tolerated.Allowed {
		t.Fatalf("a correctly-tolerated pod was refused: %v", tolerated.Reasons)
	}

	// PreferNoSchedule is advisory and must never make a node ineligible.
	preferred := node("loadgen-1", "loadgen", "eastus-2", "4", "16Gi")
	preferred.Taints = []kubeops.Taint{{Key: "spot", Effect: "PreferNoSchedule"}}
	advisory := evaluateAllocationPreflight(
		clusterCapacitySnapshot{Nodes: []kubeops.NodeRef{preferred}, NodesAvailable: true},
		&storage.PoolAllocation{CPURequest: "1", NodePool: "loadgen"}, 0, 1)
	if !advisory.Allowed {
		t.Fatalf("a PreferNoSchedule taint blocked scheduling: %v", advisory.Reasons)
	}
}

func TestPreflightSkipsUnschedulableNodes(t *testing.T) {
	cordoned := node("loadgen-0", "loadgen", "eastus-2", "4", "16Gi")
	cordoned.Unschedulable = true
	result := evaluateAllocationPreflight(
		clusterCapacitySnapshot{Nodes: []kubeops.NodeRef{cordoned}, NodesAvailable: true},
		&storage.PoolAllocation{CPURequest: "1", NodePool: "loadgen"}, 0, 1)
	if result.Allowed {
		t.Fatal("a cordoned node was treated as available capacity")
	}
}
