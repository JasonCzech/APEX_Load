package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// A pool carries at most one domain, so a generator that belongs to two pools
// could be claimed by two domains at once. The unique index on
// fleet_pool_members(generator_id) is the hard guarantee; these checks run first
// so the API answers 409 with the name of the pool that already holds the
// generator, rather than surfacing a raw constraint violation as a 500.

// generatorPoolOwnerLookup is an optional store capability. Lightweight test
// adapters that do not implement it skip the pre-check and fall back to the
// database constraint.
type generatorPoolOwnerLookup interface {
	FleetPoolIDForGenerator(generatorID string) (string, error)
}

// generatorPoolConflict names a generator already claimed by another pool.
type generatorPoolConflict struct {
	GeneratorID string
	PoolID      string
	PoolName    string
}

func (c generatorPoolConflict) Error() string {
	owner := c.PoolID
	if strings.TrimSpace(c.PoolName) != "" {
		owner = fmt.Sprintf("%s (%s)", c.PoolName, c.PoolID)
	}
	return fmt.Sprintf("generator %s already belongs to fleet pool %s; remove it from that pool first", c.GeneratorID, owner)
}

// checkGeneratorPoolExclusivity reports the first generator in generatorIDs that
// another pool already owns. Membership in poolID itself is not a conflict, so
// re-saving a pool's own members is idempotent.
func (s *Server) checkGeneratorPoolExclusivity(poolID string, generatorIDs []string) *generatorPoolConflict {
	lookup, ok := s.store.(generatorPoolOwnerLookup)
	if !ok {
		return nil
	}
	poolID = strings.TrimSpace(poolID)
	for _, generatorID := range generatorIDs {
		generatorID = strings.TrimSpace(generatorID)
		if generatorID == "" {
			continue
		}
		owner, err := lookup.FleetPoolIDForGenerator(generatorID)
		if err != nil {
			// A transient lookup failure must not block a legitimate write; the
			// unique index still backstops the invariant.
			continue
		}
		if owner == "" || owner == poolID {
			continue
		}
		conflict := generatorPoolConflict{GeneratorID: generatorID, PoolID: owner}
		if pool, err := s.store.GetFleetPool(owner); err == nil && pool != nil {
			conflict.PoolName = pool.Name
		}
		return &conflict
	}
	return nil
}

// writeFleetMembershipError answers a membership write failure. The pre-check
// above catches the ordinary case, but it is skipped when the store lacks the
// lookup capability and it can lose a race against a concurrent write, so the
// unique index remains the real guarantee — and its violation is a conflict,
// not a malformed request.
func writeFleetMembershipError(w http.ResponseWriter, err error, fallbackStatus int) {
	if errors.Is(err, storage.ErrGeneratorAlreadyInPool) {
		writeError(w, http.StatusConflict,
			"a generator in this request already belongs to another fleet pool; remove it from that pool first")
		return
	}
	if errors.Is(err, storage.ErrFleetPoolHasActiveProvisioning) {
		writeError(w, http.StatusConflict, "fleet pool has an active provisioning request; wait for it to finish before changing its execution target")
		return
	}
	if errors.Is(err, storage.ErrFleetPoolPlacementChanged) {
		writeError(w, http.StatusConflict, "fleet pool execution target changed while the operation was being prepared; reload and retry")
		return
	}
	if errors.Is(err, storage.ErrFleetPoolHasLiveCapacity) {
		writeError(w, http.StatusConflict, "fleet pool still owns live cloud capacity; scale it to zero before changing its execution target")
		return
	}
	writeError(w, fallbackStatus, err.Error())
}

// memberGeneratorIDs extracts the generator ids from a member set.
func memberGeneratorIDs(members []*storage.FleetPoolMember) []string {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		if member != nil {
			ids = append(ids, member.GeneratorID)
		}
	}
	return ids
}
