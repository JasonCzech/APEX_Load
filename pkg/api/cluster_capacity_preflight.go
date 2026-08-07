package api

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Quota preflight decides whether a scale or allocation change can be admitted
// by the target namespace. The decision logic is pure so it is table-testable;
// the caller performs the I/O and hands in a snapshot.

// clusterCapacitySnapshot is the cluster state a preflight decision needs.
type clusterCapacitySnapshot struct {
	Quotas []kubeops.ResourceQuotaRef
	// Nodes is empty when node listing is not permitted. NodesAvailable
	// distinguishes "no nodes matched" from "we were not allowed to look".
	Nodes          []kubeops.NodeRef
	NodesAvailable bool
	// CurrentRequests is the per-pod request set already on the workload,
	// which is what makes the delta calculation relative rather than absolute.
	CurrentRequests kubeops.ResourceList
}

// preflightResult is the verdict, structured so the dashboard can explain it.
type preflightResult struct {
	Allowed       bool     `json:"allowed"`
	QuotaEnforced bool     `json:"quota_enforced"`
	Reasons       []string `json:"reasons,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`

	CPUMillisDelta int64 `json:"cpu_millis_delta"`
	MemoryDelta    int64 `json:"memory_bytes_delta"`
	PodsDelta      int   `json:"pods_delta"`
}

// quotaKeyAliases maps a logical resource to the ResourceQuota keys that can
// constrain it. Kubernetes accepts both the "requests.cpu" and bare "cpu"
// spellings for a compute quota.
var quotaKeyAliases = map[string][]string{
	"cpu":    {"requests.cpu", "cpu"},
	"memory": {"requests.memory", "memory"},
	"pods":   {"pods", "count/pods"},
}

// evaluateAllocationPreflight decides whether the requested allocation at the
// target replica count fits the namespace quota and the targeted nodes.
//
// The required delta is computed against the workload's CURRENT per-pod
// requests and replica count, not from zero. Without that, every scale-down
// and every unchanged re-save would look like a fresh allocation of the whole
// pool and be falsely blocked.
func evaluateAllocationPreflight(snapshot clusterCapacitySnapshot, allocation *storage.PoolAllocation, currentReplicas, targetReplicas int) preflightResult {
	result := preflightResult{Allowed: true}
	if currentReplicas < 0 {
		currentReplicas = 0
	}
	if targetReplicas < 0 {
		targetReplicas = 0
	}
	result.PodsDelta = targetReplicas - currentReplicas

	newCPU, newMemory := allocationRequests(allocation)
	currentCPU, _, _ := kubeops.ParseCPUMillis(snapshot.CurrentRequests["cpu"])
	currentMemory, _, _ := kubeops.ParseMemoryBytes(snapshot.CurrentRequests["memory"])

	result.CPUMillisDelta = newCPU*int64(targetReplicas) - currentCPU*int64(currentReplicas)
	result.MemoryDelta = newMemory*int64(targetReplicas) - currentMemory*int64(currentReplicas)

	for _, quota := range snapshot.Quotas {
		if len(quota.Hard) == 0 {
			continue
		}
		result.QuotaEnforced = true
		checkQuotaDimension(&result, quota, "cpu", result.CPUMillisDelta, parseQuotaCPU, kubeops.FormatCPUMillis)
		checkQuotaDimension(&result, quota, "memory", result.MemoryDelta, parseQuotaMemory, kubeops.FormatMemoryBytes)
		checkQuotaDimension(&result, quota, "pods", int64(result.PodsDelta), parseQuotaCount, formatCount)
	}

	if !snapshot.NodesAvailable {
		// Node listing needs cluster-scoped RBAC that the default install does
		// not grant. Quota enforcement still applies; only pod-fit is unproven.
		result.Warnings = append(result.Warnings,
			"node capacity is unavailable (the controller lacks cluster-scoped node read permission); pod fit was not verified")
	} else if newCPU > 0 || newMemory > 0 || len(allocation.EffectiveNodeSelector()) > 0 {
		// Placement is checked even when no resources are requested: a typo in
		// the node pool or zone matches zero nodes, and without this the pods
		// would simply sit Pending with no explanation. Tolerations alone do
		// not trigger a check — they widen where a pod may land, never narrow
		// it, so they cannot make placement unsatisfiable on their own.
		if reason := checkPodFit(snapshot.Nodes, allocation, newCPU, newMemory); reason != "" {
			result.Allowed = false
			result.Reasons = append(result.Reasons, reason)
		}
	}
	sort.Strings(result.Reasons)
	return result
}

// checkQuotaDimension blocks only when the projected usage exceeds the hard
// limit. A negative delta (scale-down) can never exceed anything.
func checkQuotaDimension(result *preflightResult, quota kubeops.ResourceQuotaRef, dimension string, delta int64,
	parse func(string) (int64, bool), format func(int64) string) {
	if delta <= 0 {
		return
	}
	for _, key := range quotaKeyAliases[dimension] {
		hardRaw, ok := quota.Hard[key]
		if !ok {
			continue
		}
		hard, parsed := parse(hardRaw)
		if !parsed {
			continue
		}
		used, _ := parse(quota.Used[key])
		if used+delta > hard {
			result.Allowed = false
			result.Reasons = append(result.Reasons, fmt.Sprintf(
				"resource quota %q limits %s to %s; this change needs %s on top of the %s already in use",
				quota.Name, key, format(hard), format(delta), format(used)))
		}
		// Only the first matching alias is authoritative; checking both would
		// double-report the same constraint.
		return
	}
}

// checkPodFit reports whether no eligible node could hold a single pod. It
// deliberately does not attempt whole-cluster bin-packing — that is the
// scheduler's job, and approximating it here would block valid changes.
func checkPodFit(nodes []kubeops.NodeRef, allocation *storage.PoolAllocation, cpuMillis, memoryBytes int64) string {
	selector := allocation.EffectiveNodeSelector()
	eligible := 0
	for _, node := range nodes {
		if node.Unschedulable || !nodeMatchesSelector(node, selector) || !nodeTolerated(node, allocation) {
			continue
		}
		eligible++
		nodeCPU, _, _ := node.Allocatable.CPUMillis()
		nodeMemory, _, _ := node.Allocatable.MemoryBytes()
		if nodeCPU >= cpuMillis && nodeMemory >= memoryBytes {
			return ""
		}
	}
	if eligible == 0 {
		if len(selector) == 0 {
			return "no schedulable nodes were found in the cluster"
		}
		return fmt.Sprintf("no schedulable node matches the requested placement (%s)", describeSelector(selector))
	}
	return fmt.Sprintf("no eligible node has room for a single generator pod requesting %s CPU and %s memory",
		kubeops.FormatCPUMillis(cpuMillis), kubeops.FormatMemoryBytes(memoryBytes))
}

func nodeMatchesSelector(node kubeops.NodeRef, selector map[string]string) bool {
	for key, want := range selector {
		if strings.TrimSpace(node.Labels[key]) != want {
			return false
		}
	}
	return true
}

// nodeTolerated reports whether the allocation's tolerations cover every
// scheduling-blocking taint on the node. PreferNoSchedule is advisory, so it
// never makes a node ineligible.
func nodeTolerated(node kubeops.NodeRef, allocation *storage.PoolAllocation) bool {
	for _, taint := range node.Taints {
		if taint.Effect == "PreferNoSchedule" {
			continue
		}
		if !taintTolerated(taint, allocation) {
			return false
		}
	}
	return true
}

func taintTolerated(taint kubeops.Taint, allocation *storage.PoolAllocation) bool {
	if allocation == nil {
		return false
	}
	for _, toleration := range allocation.Tolerations {
		if effect := strings.TrimSpace(toleration.Effect); effect != "" && effect != taint.Effect {
			continue
		}
		key := strings.TrimSpace(toleration.Key)
		switch strings.TrimSpace(toleration.Operator) {
		case "Exists":
			// An empty key with Exists tolerates every taint.
			if key == "" || key == taint.Key {
				return true
			}
		default:
			if key == taint.Key && strings.TrimSpace(toleration.Value) == taint.Value {
				return true
			}
		}
	}
	return false
}

func describeSelector(selector map[string]string) string {
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+selector[key])
	}
	return strings.Join(parts, ", ")
}

// allocationRequests returns the per-pod CPU and memory requests. When only a
// limit is set, Kubernetes defaults the request to the limit, so the quota
// projection must do the same or it would under-count.
func allocationRequests(allocation *storage.PoolAllocation) (cpuMillis int64, memoryBytes int64) {
	if allocation == nil {
		return 0, 0
	}
	cpuMillis, present, _ := kubeops.ParseCPUMillis(allocation.CPURequest)
	if !present {
		cpuMillis, _, _ = kubeops.ParseCPUMillis(allocation.CPULimit)
	}
	memoryBytes, present, _ = kubeops.ParseMemoryBytes(allocation.MemoryRequest)
	if !present {
		memoryBytes, _, _ = kubeops.ParseMemoryBytes(allocation.MemoryLimit)
	}
	return cpuMillis, memoryBytes
}

func parseQuotaCPU(raw string) (int64, bool) {
	value, present, err := kubeops.ParseCPUMillis(raw)
	return value, present && err == nil
}

func parseQuotaMemory(raw string) (int64, bool) {
	value, present, err := kubeops.ParseMemoryBytes(raw)
	return value, present && err == nil
}

func parseQuotaCount(raw string) (int64, bool) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return value, err == nil
}

func formatCount(value int64) string { return strconv.FormatInt(value, 10) }
