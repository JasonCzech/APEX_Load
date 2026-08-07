package api

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Runtime pool membership was computed independently in three places —
// placement, provisioning validation, and reservation envelopes — and the three
// copies had to be kept in step by hand. This is the single definition.
//
// fleet_pool_members is authoritative; generator_metadata.fleet_pool_id is the
// legacy pointer, retained here so a generator bound only through the older
// path is still counted until consolidateFleetPoolMembership has run.
// Deduplication is by target (host:port), because two records naming the same
// address are one generator no matter how they were bound.

// fleetPoolTargetsByPool groups each pool's deduplicated runtime targets.
// Order is stable: explicit members in pool order, then metadata-bound targets
// in metadata order, so placement stays deterministic across calls.
func fleetPoolTargetsByPool(pools []*storage.FleetPool, metadata []*storage.GeneratorMetadata) map[string][]string {
	byPool := make(map[string][]string, len(pools))
	seen := make(map[string]map[string]struct{}, len(pools))
	add := func(poolID, target string) {
		poolID = strings.TrimSpace(poolID)
		target = strings.TrimSpace(target)
		if poolID == "" || target == "" {
			return
		}
		if seen[poolID] == nil {
			seen[poolID] = make(map[string]struct{})
		}
		if _, ok := seen[poolID][target]; ok {
			return
		}
		seen[poolID][target] = struct{}{}
		byPool[poolID] = append(byPool[poolID], target)
	}
	for _, pool := range pools {
		if pool == nil {
			continue
		}
		poolID := strings.TrimSpace(pool.ID)
		if poolID == "" {
			continue
		}
		// Represent a pool with no targets as present-but-empty; reservation
		// envelopes distinguish "pool with zero capacity" from "unknown pool".
		if _, ok := byPool[poolID]; !ok {
			byPool[poolID] = nil
		}
		for _, member := range pool.Members {
			if member != nil {
				add(poolID, member.Target)
			}
		}
	}
	for _, generator := range metadata {
		if generator != nil && generator.FleetPoolID != nil {
			add(*generator.FleetPoolID, generator.Target)
		}
	}
	return byPool
}

// fleetPoolTargets returns one pool's deduplicated runtime targets.
func fleetPoolTargets(pool *storage.FleetPool, metadata []*storage.GeneratorMetadata) []string {
	if pool == nil {
		return nil
	}
	return fleetPoolTargetsByPool([]*storage.FleetPool{pool}, metadata)[strings.TrimSpace(pool.ID)]
}
