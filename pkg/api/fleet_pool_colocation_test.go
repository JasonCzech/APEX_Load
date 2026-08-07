package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func placementPool(clusterID string, selector map[string]string) *storage.FleetPool {
	pool := &storage.FleetPool{ClusterID: clusterID}
	if selector != nil {
		pool.Allocation = &storage.PoolAllocation{NodeSelector: selector}
	}
	return pool
}

// TestCanShareNodes pins the disjointness rule. Two placements are separated
// only when they share a label key with different values — that is the only
// case where no single node can satisfy both.
func TestCanShareNodes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		a, b  *storage.FleetPool
		share bool
	}{
		{
			name:  "different clusters never share nodes",
			a:     placementPool("cluster-1", nil),
			b:     placementPool("cluster-2", nil),
			share: false,
		},
		{
			name:  "same node pool label with different values is disjoint",
			a:     placementPool("cluster-1", map[string]string{"workload": "gen-a"}),
			b:     placementPool("cluster-1", map[string]string{"workload": "gen-b"}),
			share: false,
		},
		{
			name:  "identical placement shares nodes",
			a:     placementPool("cluster-1", map[string]string{"workload": "gen-a"}),
			b:     placementPool("cluster-1", map[string]string{"workload": "gen-a"}),
			share: true,
		},
		{
			// A node may carry both labels, so these are NOT provably disjoint.
			name:  "unrelated selector keys are not proof of separation",
			a:     placementPool("cluster-1", map[string]string{"workload": "gen-a"}),
			b:     placementPool("cluster-1", map[string]string{"topology.kubernetes.io/zone": "eu"}),
			share: true,
		},
		{
			// This is the case a naive same-key-equality check would miss.
			name:  "an unpinned pool shares nodes with every pinned pool",
			a:     placementPool("cluster-1", nil),
			b:     placementPool("cluster-1", map[string]string{"workload": "gen-b"}),
			share: true,
		},
		{
			name:  "two unpinned pools on one cluster share nodes",
			a:     placementPool("", nil),
			b:     placementPool("", nil),
			share: true,
		},
		{
			name: "one differing key separates even when another key matches",
			a: placementPool("cluster-1", map[string]string{
				"workload": "gen-a", "topology.kubernetes.io/zone": "eu"}),
			b: placementPool("cluster-1", map[string]string{
				"workload": "gen-b", "topology.kubernetes.io/zone": "eu"}),
			share: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := canShareNodes(placementForPool(tc.a), placementForPool(tc.b))
			if got != tc.share {
				t.Fatalf("canShareNodes = %v, want %v", got, tc.share)
			}
		})
	}
}

// TestPlacementUsesEffectiveNodeSelector guards the subtlety that NodePool and
// Zone are separate fields that merge into the selector under cloud-specific
// keys. Comparing raw fields would treat these two as unrelated.
func TestPlacementUsesEffectiveNodeSelector(t *testing.T) {
	viaField := &storage.FleetPool{
		ClusterID:  "cluster-1",
		Allocation: &storage.PoolAllocation{NodePool: "generators"},
	}
	placement := placementForPool(viaField)
	if len(placement.selector) == 0 {
		t.Fatal("NodePool did not surface in the effective selector")
	}
	// The same node pool declared on two pools must read as overlapping.
	if !canShareNodes(placement, placementForPool(&storage.FleetPool{
		ClusterID:  "cluster-1",
		Allocation: &storage.PoolAllocation{NodePool: "generators"},
	})) {
		t.Fatal("two pools on the same node pool were treated as separated")
	}
	if canShareNodes(placement, placementForPool(&storage.FleetPool{
		ClusterID:  "cluster-1",
		Allocation: &storage.PoolAllocation{NodePool: "other"},
	})) {
		t.Fatal("two pools on different node pools were treated as overlapping")
	}
}

// TestUnscopedPoolsAreExemptFromColocation is the compatibility hinge, matching
// validatePoolAgainstDomainTarget: a pool with no domain makes no isolation
// claim, and every pool predating domain scoping has an empty domain_id.
func TestUnscopedPoolsAreExemptFromColocation(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveFleetPool(&storage.FleetPool{
		ID: "pool-existing", Name: "existing", CloudProvider: "kubernetes", Status: "active",
		DomainID: "domain-a", ClusterID: "cluster-1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}

	// Same cluster, no placement, but unscoped — allowed.
	unscoped := &storage.FleetPool{ID: "pool-new", Name: "new", ClusterID: "cluster-1"}
	if status, err := server.validatePoolColocation(unscoped); err != nil {
		t.Fatalf("unscoped pool rejected with %d: %v", status, err)
	}
}

// TestColocationRejectsSharedNodesAcrossDomains is the invariant itself.
func TestColocationRejectsSharedNodesAcrossDomains(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveFleetPool(&storage.FleetPool{
		ID: "pool-a", Name: "pool-a", CloudProvider: "kubernetes", Status: "active",
		DomainID: "domain-a", ClusterID: "cluster-1",
		Allocation: &storage.PoolAllocation{NodePool: "generators"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}

	conflicting := &storage.FleetPool{
		ID: "pool-b", Name: "pool-b", DomainID: "domain-b", ClusterID: "cluster-1",
		Allocation: &storage.PoolAllocation{NodePool: "generators"},
	}
	status, err := server.validatePoolColocation(conflicting)
	if err == nil {
		t.Fatal("a second domain on the same node pool was accepted")
	}
	if status != 409 {
		t.Fatalf("status = %d, want 409", status)
	}

	// A different node pool in the same cluster is provably separated.
	separated := &storage.FleetPool{
		ID: "pool-c", Name: "pool-c", DomainID: "domain-b", ClusterID: "cluster-1",
		Allocation: &storage.PoolAllocation{NodePool: "generators-b"},
	}
	if status, err := server.validatePoolColocation(separated); err != nil {
		t.Fatalf("separated pool rejected with %d: %v", status, err)
	}

	// Same domain on the same node pool is fine — the rule is cross-domain only.
	sameDomain := &storage.FleetPool{
		ID: "pool-d", Name: "pool-d", DomainID: "domain-a", ClusterID: "cluster-1",
		Allocation: &storage.PoolAllocation{NodePool: "generators"},
	}
	if status, err := server.validatePoolColocation(sameDomain); err != nil {
		t.Fatalf("same-domain pool rejected with %d: %v", status, err)
	}
}

// TestColocationRejectsUnpinnedPoolAgainstAnotherDomain covers the case the
// plain "same placement key" rule misses: an unpinned pool's pods can land on
// the other domain's node pool.
func TestColocationRejectsUnpinnedPoolAgainstAnotherDomain(t *testing.T) {
	server, store := newTimeslotServer(t)
	now := time.Now().UTC()
	if err := store.SaveFleetPool(&storage.FleetPool{
		ID: "pool-a", Name: "pool-a", CloudProvider: "kubernetes", Status: "active",
		DomainID: "domain-a", ClusterID: "cluster-1",
		Allocation: &storage.PoolAllocation{NodePool: "generators"},
		CreatedAt:  now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}

	unpinned := &storage.FleetPool{
		ID: "pool-b", Name: "pool-b", DomainID: "domain-b", ClusterID: "cluster-1",
	}
	if _, err := server.validatePoolColocation(unpinned); err == nil {
		t.Fatal("an unpinned pool in a second domain was accepted; its pods can land on domain-a's nodes")
	}
}
