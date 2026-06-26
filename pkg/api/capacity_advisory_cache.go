package api

// Capacity-advisory memoization. estimateCapacityAdvisoryForAssignment derives the
// advisory from three runtime inputs, all accounted for in the cache:
//   - system settings (generator specs, headroom, configured reservations) — the
//     generation is folded into every key and bumped on each settings change, so
//     settings edits invalidate exactly;
//   - the APEXLOAD_LIVE_AVAILABLE_GENERATORS env var (the runtime live-capacity
//     snapshot) — read and folded into the key, so a changed snapshot misses;
//   - wall-clock time, only via configured reservation windows opening/closing —
//     bounded by a short TTL (the advisory is a pre-launch hint that is always
//     re-validated by fresh admission checks at launch, so ≤TTL reservation lag is
//     acceptable).
// It does NOT read live in-memory test state (the resolvers take settings by
// value); that capacity gate is evaluated separately and uncached.
//
// Real win: estimate/preflight are polled as the dashboard form is edited, and
// the launch-window schedule projection recomputes the same advisory once per
// projected occurrence (same instant, env, generation) — caching collapses those
// repeats to a single computation.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync/atomic"
	"time"
)

const (
	// Short TTL: the only cache input not in the key is wall-clock-driven
	// reservation-window state, so this bounds how stale that can be.
	capacityAdvisoryCacheTTL        = 5 * time.Second
	capacityAdvisoryCacheMaxEntries = 512
)

type cachedCapacityAdvisory struct {
	advisory  *CapacityAdvisory
	expiresAt time.Time
}

// capacityAdvisoryCacheKey hashes the advisory inputs together with the current
// generation. Caller holds tr.mu (R or W) — the same contract that guards the
// tr.settings read in estimateCapacityAdvisoryForAssignment. Returns "" if the
// inputs cannot be marshaled, signaling the caller to bypass the cache.
func (tr *TestRunner) capacityAdvisoryCacheKey(projectID string, groups []TestGroup, totalVUsers int, duration time.Duration, resourceBaseline bool, runtimeSettings *RuntimeSettingsOverride, assignment *GeneratorAssignmentSettings) string {
	payload := struct {
		Gen              uint64                       `json:"g"`
		Live             string                       `json:"l"`
		ProjectID        string                       `json:"p"`
		Groups           []TestGroup                  `json:"gr"`
		TotalVUsers      int                          `json:"v"`
		DurationSeconds  int64                        `json:"d"`
		ResourceBaseline bool                         `json:"rb"`
		Runtime          *RuntimeSettingsOverride     `json:"rt"`
		Assignment       *GeneratorAssignmentSettings `json:"a"`
	}{
		Gen: tr.capacityAdvisoryGen,
		// The live-availability snapshot is supplied out-of-band via env, so a
		// change to it must produce a fresh key (see resolveCapacityAvailabilityAt).
		Live:             os.Getenv("APEXLOAD_LIVE_AVAILABLE_GENERATORS"),
		ProjectID:        projectID,
		Groups:           groups,
		TotalVUsers:      totalVUsers,
		DurationSeconds:  int64(duration / time.Second),
		ResourceBaseline: resourceBaseline,
		Runtime:          runtimeSettings,
		Assignment:       assignment,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// lookupCapacityAdvisory returns a CLONE of the cached advisory (so callers can
// freely mutate the result without corrupting the shared cache entry) and whether
// it was a live hit. Expired entries are evicted on read.
func (tr *TestRunner) lookupCapacityAdvisory(key string) (*CapacityAdvisory, bool) {
	if key == "" {
		return nil, false
	}
	tr.capacityAdvisoryCacheMu.Lock()
	defer tr.capacityAdvisoryCacheMu.Unlock()
	entry, ok := tr.capacityAdvisoryCache[key]
	if !ok {
		atomic.AddUint64(&tr.capacityAdvisoryMisses, 1)
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(tr.capacityAdvisoryCache, key)
		atomic.AddUint64(&tr.capacityAdvisoryMisses, 1)
		return nil, false
	}
	atomic.AddUint64(&tr.capacityAdvisoryHits, 1)
	return cloneCapacityAdvisory(entry.advisory), true
}

// storeCapacityAdvisory caches a CLONE of the advisory under key. A flood of
// distinct scenarios can't grow the map without bound — it is dropped wholesale
// when it reaches the cap (cheaper and simpler than per-entry LRU for a cache
// whose entries are already short-lived via the TTL + generation).
func (tr *TestRunner) storeCapacityAdvisory(key string, advisory *CapacityAdvisory) {
	if key == "" {
		return
	}
	tr.capacityAdvisoryCacheMu.Lock()
	defer tr.capacityAdvisoryCacheMu.Unlock()
	if tr.capacityAdvisoryCache == nil || len(tr.capacityAdvisoryCache) >= capacityAdvisoryCacheMaxEntries {
		tr.capacityAdvisoryCache = make(map[string]cachedCapacityAdvisory)
	}
	tr.capacityAdvisoryCache[key] = cachedCapacityAdvisory{
		advisory:  cloneCapacityAdvisory(advisory),
		expiresAt: time.Now().Add(capacityAdvisoryCacheTTL),
	}
}

// invalidateCapacityAdvisoryCacheLocked bumps the generation (so no existing key
// can match again) and drops cached entries. Caller holds tr.mu for writing.
func (tr *TestRunner) invalidateCapacityAdvisoryCacheLocked() {
	tr.capacityAdvisoryGen++
	tr.capacityAdvisoryCacheMu.Lock()
	tr.capacityAdvisoryCache = nil
	tr.capacityAdvisoryCacheMu.Unlock()
}

// capacityAdvisoryCacheStats reports cumulative hit/miss counts (test + ops
// observability).
func (tr *TestRunner) capacityAdvisoryCacheStats() (hits, misses uint64) {
	return atomic.LoadUint64(&tr.capacityAdvisoryHits), atomic.LoadUint64(&tr.capacityAdvisoryMisses)
}
