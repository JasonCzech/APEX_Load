package api

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	storedTestHistoryCacheEntries = 4096
	storedTestHistoryCacheBytes   = int64(128 << 20)
	// A terminal history can receive its final durable write after another HA
	// replica has already observed the terminal row. In-process invalidation
	// cannot reach that replica, so revalidate only histories that are actively
	// requested and bound the stale window without sweeping the whole cache.
	storedTestHistoryHARevalidateInterval = 30 * time.Second
)

type storedTestHistorySnapshot struct {
	History  []*LiveMetrics `json:"history"`
	LoadedAt time.Time      `json:"-"`
}

// storedTestHistoryCache adds an invalidation generation to the
// generic bounded cache. boundedArchiveCache deliberately lets waiters that
// joined an invalidated in-flight load receive its detached result. That is
// safe for immutable archive identities, but terminal history can receive one
// final write after terminalization. The generation prevents that older load
// from being attached after the write has committed.
type storedTestHistoryCache struct {
	mu       sync.Mutex
	revision uint64
	values   *boundedArchiveCache[storedTestHistorySnapshot]
}

func newStoredTestHistoryCache() *storedTestHistoryCache {
	return &storedTestHistoryCache{
		values: newWeightedBoundedArchiveCache(
			storedTestHistoryCacheEntries,
			storedTestHistoryCacheBytes,
			archiveJSONWeight[storedTestHistorySnapshot],
		),
	}
}

func (c *storedTestHistoryCache) getOrLoadAtRevision(key string, load func() (*storedTestHistorySnapshot, error)) (*storedTestHistorySnapshot, uint64, error) {
	if c == nil {
		value, err := load()
		return value, 0, err
	}
	c.mu.Lock()
	revision := c.revision
	c.mu.Unlock()
	value, err := c.values.getOrLoad(key, load)
	return value, revision, err
}

func (c *storedTestHistoryCache) applyIfCurrent(revision uint64, apply func()) bool {
	if c == nil {
		apply()
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.revision != revision {
		return false
	}
	apply()
	return true
}

func (c *storedTestHistoryCache) deleteTest(testID string) {
	if c == nil {
		return
	}
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return
	}
	c.mu.Lock()
	c.revision++
	// Keep the revision change and generic-cache detachment atomic with respect
	// to readers taking their starting revision.
	c.values.deleteTest(testID)
	c.mu.Unlock()
}

// deleteTestIfRevision invalidates an expired entry only if no newer local
// invalidation has occurred since it was read. This keeps concurrent HA
// revalidation callers from repeatedly evicting the same freshly loaded value.
func (c *storedTestHistoryCache) deleteTestIfRevision(testID string, revision uint64) bool {
	if c == nil {
		return false
	}
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.revision != revision {
		return false
	}
	c.revision++
	c.values.deleteTest(testID)
	return true
}

func (c *storedTestHistoryCache) len() int {
	if c == nil {
		return 0
	}
	return c.values.len()
}

func storedTestHistoryCacheKey(test *ManagedTest) string {
	if test == nil {
		return ""
	}
	projectID := normalizeProjectID(test.ProjectID)
	endedAt := time.Time{}
	if test.EndedAt != nil {
		endedAt = test.EndedAt.UTC()
	}
	// Keep the test ID first so boundedArchiveCache.deleteTest can invalidate
	// every identity for an ID. Project and immutable lifecycle timestamps bind
	// the cached evidence to the durable tenant/run identity, not merely the
	// reusable primary key.
	incarnation := managedTestRunIncarnation(test)
	return fmt.Sprintf("%s|%d:%s|%d|%d|%d:%s", test.ID, len(projectID), projectID, test.CreatedAt.UTC().UnixNano(), endedAt.UnixNano(), len(incarnation), incarnation)
}

func sameStoredTestHistoryIdentity(left, right *ManagedTest) bool {
	if left == nil || right == nil || left.ID != right.ID {
		return false
	}
	if normalizeProjectID(left.ProjectID) != normalizeProjectID(right.ProjectID) || !left.CreatedAt.Equal(right.CreatedAt) {
		return false
	}
	if managedTestRunIncarnation(left) != managedTestRunIncarnation(right) {
		return false
	}
	if left.EndedAt == nil || right.EndedAt == nil {
		return left.EndedAt == nil && right.EndedAt == nil
	}
	return left.EndedAt.Equal(*right.EndedAt)
}

// attachStoredTerminalHistory loads immutable terminal history through the
// runner-wide weighted LRU. The canonical ManagedTest retains only its latest
// point, keeping boot and steady-state memory bounded by the cache budget.
func (tr *TestRunner) attachStoredTerminalHistory(test *ManagedTest) {
	if tr == nil || test == nil || !isTerminalTestStatus(test.Status) || tr.historyStore == nil || tr.storedHistoryCache == nil {
		return
	}
	key := storedTestHistoryCacheKey(test)
	load := func() (*storedTestHistorySnapshot, error) {
		loaded := &ManagedTest{ID: test.ID}
		if err := tr.loadStoredTestHistoryLimit(loaded, maxManagedTestHistory); err != nil {
			return nil, err
		}
		return &storedTestHistorySnapshot{
			History:  cloneLiveMetricsHistory(loaded.History),
			LoadedAt: time.Now(),
		}, nil
	}
	// A terminal run normally invalidates at most once, for its final interval
	// write. Keep retries bounded so a pathological compatibility store cannot
	// hold a request forever while continuously rewriting terminal history.
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, revision, err := tr.storedHistoryCache.getOrLoadAtRevision(key, load)
		if err != nil || snapshot == nil {
			if err != nil {
				slog.Warn("failed to load terminal test history", "test_id", test.ID, "error", err)
			}
			return
		}
		if tr.leadership != nil && !snapshot.LoadedAt.IsZero() && time.Since(snapshot.LoadedAt) >= storedTestHistoryHARevalidateInterval {
			tr.storedHistoryCache.deleteTestIfRevision(test.ID, revision)
			continue
		}

		canonicalTail := cloneLiveMetrics(test.LiveMetrics)
		if len(test.History) > 0 {
			canonicalTail = cloneLiveMetrics(test.History[len(test.History)-1])
		}
		merged := mergeStoredHistoryWithCanonicalTail(snapshot.History, canonicalTail)

		// Lock order matches all replacement invalidations: runner identity first,
		// then cache generation. This makes the identity and generation decision
		// atomic with the attachment without introducing a cache->runner inversion.
		tr.mu.RLock()
		canonical := tr.tests[test.ID]
		identityCurrent := sameStoredTestHistoryIdentity(canonical, test)
		attached := false
		if identityCurrent {
			attached = tr.storedHistoryCache.applyIfCurrent(revision, func() {
				test.History = merged
				if len(test.History) > 0 {
					test.LiveMetrics = cloneLiveMetrics(test.History[len(test.History)-1])
					if test.LiveMetrics != nil {
						annotateLiveObservation(test.LiveMetrics, test.Status, time.Now())
					}
				}
			})
		}
		tr.mu.RUnlock()
		if !identityCurrent || attached {
			return
		}
	}
}

func mergeStoredHistoryWithCanonicalTail(history []*LiveMetrics, canonicalTail *LiveMetrics) []*LiveMetrics {
	merged := cloneLiveMetricsHistory(history)
	if canonicalTail == nil {
		return merged
	}
	if len(merged) == 0 {
		return []*LiveMetrics{cloneLiveMetrics(canonicalTail)}
	}
	last := merged[len(merged)-1]
	if last != nil && last.Timestamp.After(canonicalTail.Timestamp) {
		return merged
	}
	if last != nil && last.Timestamp.Equal(canonicalTail.Timestamp) {
		merged[len(merged)-1] = cloneLiveMetrics(canonicalTail)
		return merged
	}
	if len(merged) >= maxManagedTestHistory {
		copy(merged, merged[len(merged)-maxManagedTestHistory+1:])
		merged = merged[:maxManagedTestHistory-1]
	}
	return append(merged, cloneLiveMetrics(canonicalTail))
}

func cloneLiveMetricsHistory(history []*LiveMetrics) []*LiveMetrics {
	if len(history) == 0 {
		return nil
	}
	cloned := make([]*LiveMetrics, len(history))
	for index, metrics := range history {
		cloned[index] = cloneLiveMetrics(metrics)
	}
	return cloned
}
