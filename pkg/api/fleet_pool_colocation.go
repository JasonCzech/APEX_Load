package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Co-location enforcement for domain-scoped fleet pools.
//
// A generator belongs to exactly one pool (fleet_pool_members' unique index) and
// a pool to at most one domain, so the remaining way two domains end up sharing
// hardware is placement: two pools whose generator pods can be scheduled onto
// the same node.
//
// The isolation boundary is (cluster, node placement). The StatefulSet name is
// deliberately NOT part of it — two different StatefulSets in one cluster still
// land on the same nodes unless their node placement separates them.
//
// Disjointness is proven, not assumed. Two selectors are separated only when
// they share a label key with different values; that is the only case where no
// node can satisfy both. Two selectors with no key in common may well match the
// same node, and an empty selector matches every node, so both are treated as
// overlapping.

// poolPlacement is a pool's scheduling footprint within one cluster.
type poolPlacement struct {
	clusterID string
	selector  map[string]string
	// allocation is retained so descriptions can reuse the storage layer's own
	// stable key ordering rather than re-implementing it.
	allocation *storage.PoolAllocation
}

// placementForPool derives the footprint. An empty cluster id is meaningful: it
// is the controller's own cluster, and two such pools share it.
func placementForPool(pool *storage.FleetPool) poolPlacement {
	placement := poolPlacement{clusterID: strings.TrimSpace(pool.ClusterID), allocation: pool.Allocation}
	if pool.Allocation != nil {
		// EffectiveNodeSelector is the canonical merge of NodePool, Zone and the
		// free-form selector; comparing the raw fields would miss that NodePool
		// maps to a cloud-specific label key.
		placement.selector = pool.Allocation.EffectiveNodeSelector()
	}
	return placement
}

// canShareNodes reports whether two placements could put pods on one node.
func canShareNodes(a, b poolPlacement) bool {
	if a.clusterID != b.clusterID {
		return false
	}
	for key, valueA := range a.selector {
		if valueB, ok := b.selector[key]; ok && valueA != valueB {
			// A node cannot carry two values for one label key.
			return false
		}
	}
	return true
}

// describePlacement renders a placement for an error message.
func describePlacement(placement poolPlacement) string {
	if len(placement.selector) == 0 {
		return "no node placement (its pods can be scheduled onto any node in the cluster)"
	}
	parts := make([]string, 0, len(placement.selector))
	// SelectorKeys is the storage layer's stable ordering for exactly this map.
	for _, key := range placement.allocation.SelectorKeys() {
		parts = append(parts, key+"="+placement.selector[key])
	}
	return strings.Join(parts, ", ")
}

// validatePoolColocation rejects a pool whose generators could land on the same
// nodes as another domain's. Like the domain deployment-target check, an
// unscoped pool (empty domain_id) is exempt: it is every pool that predates
// domain scoping, and it makes no isolation claim to violate.
func (s *Server) validatePoolColocation(pool *storage.FleetPool) (int, error) {
	if pool == nil {
		return http.StatusBadRequest, fmt.Errorf("fleet pool is required")
	}
	domainID := strings.TrimSpace(pool.DomainID)
	if domainID == "" {
		return http.StatusOK, nil
	}
	if s.store == nil {
		return http.StatusOK, nil
	}
	pools, err := s.store.ListFleetPools(0, 0)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("load fleet pools to check placement: %w", err)
	}
	placement := placementForPool(pool)
	for _, other := range pools {
		if other == nil || other.ID == pool.ID {
			continue
		}
		otherDomain := strings.TrimSpace(other.DomainID)
		if otherDomain == "" || otherDomain == domainID {
			continue
		}
		otherPlacement := placementForPool(other)
		if !canShareNodes(placement, otherPlacement) {
			continue
		}
		return http.StatusConflict, fmt.Errorf(
			"pool %q (domain %q) would share nodes with pool %q (domain %q): this pool has %s and that pool has %s. "+
				"Give the two pools node placements that cannot match the same node — for example the same node-pool label with different values — or bind them to different clusters",
			pool.Name, domainID, other.Name, otherDomain, describePlacement(placement), describePlacement(otherPlacement))
	}
	return http.StatusOK, nil
}
