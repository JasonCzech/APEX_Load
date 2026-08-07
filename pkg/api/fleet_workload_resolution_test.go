package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// TestGeneratorWorkloadForPool pins the tag-override contract that scaling and
// allocation both depend on. Allocation used to hardcode the cluster default,
// so a pool tagged with its own StatefulSet scaled one workload while its pod
// resources were patched onto another — and two pools sharing a cluster
// overwrote each other's allocation.
func TestGeneratorWorkloadForPool(t *testing.T) {
	const defaultWorkload = "apexload-generator"

	for _, tc := range []struct {
		name string
		tag  string
		want string
	}{
		{"no tag falls back to the default", "", defaultWorkload},
		{"tag equal to the default is honored", defaultWorkload, defaultWorkload},
		{"prefixed tag is honored", "apexload-generator-eu", "apexload-generator-eu"},
		{"whitespace is trimmed", "  apexload-generator-eu  ", "apexload-generator-eu"},
		// The prefix guard is what stops a mistyped or malicious pool tag from
		// patching an unrelated co-tenant StatefulSet in the same namespace.
		{"unrelated workload is refused", "postgres", defaultWorkload},
		{"near-miss prefix is refused", "apexload-generatorX", defaultWorkload},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := &storage.FleetPool{ID: "pool-1"}
			if tc.tag != "" {
				pool.Tags = map[string]string{"kubernetes.statefulset": tc.tag}
			}
			if got := generatorWorkloadForPool(pool, defaultWorkload); got != tc.want {
				t.Fatalf("generatorWorkloadForPool(tag=%q) = %q, want %q", tc.tag, got, tc.want)
			}
		})
	}

	if got := generatorWorkloadForPool(nil, defaultWorkload); got != defaultWorkload {
		t.Fatalf("nil pool = %q, want %q", got, defaultWorkload)
	}
}

// TestScalerAndAllocationResolveTheSameWorkload is the actual invariant: the
// scaler's resolution and the shared helper the allocation handlers call must
// never diverge, or a pool's replicas and its pod resources land on different
// StatefulSets.
func TestScalerAndAllocationResolveTheSameWorkload(t *testing.T) {
	const defaultWorkload = "apexload-generator"
	scaler := kubernetesFleetScaler{defaultWorkload: defaultWorkload}

	for _, tag := range []string{"", defaultWorkload, "apexload-generator-eu", "postgres"} {
		pool := &storage.FleetPool{ID: "pool-1"}
		if tag != "" {
			pool.Tags = map[string]string{"kubernetes.statefulset": tag}
		}
		scaled := scaler.workloadForPool(pool)
		allocated := generatorWorkloadForPool(pool, defaultWorkload)
		if scaled != allocated {
			t.Fatalf("tag %q: scaler resolved %q but allocation resolved %q", tag, scaled, allocated)
		}
	}
}
