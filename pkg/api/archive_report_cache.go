package api

import (
	"container/list"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Archive report/manifest clones can be multiple MB each (soak-run timelines,
// error-pattern snapshots). The cache key includes ManifestSHA256, which is
// rotated by every test-notes sync, so a plain unbounded map would orphan the
// previous entry on each edit and grow monotonically until the controller is
// OOM-killed. A bounded LRU caps resident memory and evicts stale/orphaned
// entries over time.
const (
	maxRunArchiveReportCacheEntries   = 64
	maxRunArchiveManifestCacheEntries = 64
)

var runArchiveReportCache = newBoundedCache(maxRunArchiveReportCacheEntries)     // *RunArchiveReport
var runArchiveManifestCache = newBoundedCache(maxRunArchiveManifestCacheEntries) // *RunArchiveManifest

// boundedCache is a small mutex-guarded LRU keyed by string. Store evicts the
// least-recently-used entry once the entry count exceeds max.
type boundedCache struct {
	mu      sync.Mutex
	max     int
	entries map[string]*list.Element
	order   *list.List // front = most recently used
}

type boundedCacheEntry struct {
	key   string
	value any
}

func newBoundedCache(max int) *boundedCache {
	return &boundedCache{max: max, entries: make(map[string]*list.Element), order: list.New()}
}

func (c *boundedCache) Load(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*boundedCacheEntry).value, true
}

func (c *boundedCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.order.Remove(el)
		delete(c.entries, key)
	}
}

func (c *boundedCache) Store(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		el.Value.(*boundedCacheEntry).value = value
		c.order.MoveToFront(el)
		return
	}
	c.entries[key] = c.order.PushFront(&boundedCacheEntry{key: key, value: value})
	for c.max > 0 && c.order.Len() > c.max {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*boundedCacheEntry).key)
	}
}

func cachedRunArchiveReport(record *storage.RunArchiveRecord) (*RunArchiveReport, error) {
	key := runArchiveReportCacheKey(record)
	if key != "" {
		if cached, ok := runArchiveReportCache.Load(key); ok {
			return cloneRunArchiveReport(cached.(*RunArchiveReport))
		}
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return report, nil
	}
	cacheCopy, err := cloneRunArchiveReport(report)
	if err != nil {
		return nil, err
	}
	runArchiveReportCache.Store(key, cacheCopy)
	return report, nil
}

func runArchiveReportCacheKey(record *storage.RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	parts := []string{
		strings.TrimSpace(record.TestID),
		strings.TrimSpace(record.ManifestPath),
		strings.TrimSpace(record.ManifestSHA256),
		strconv.FormatInt(record.CompletedAt.UnixNano(), 10),
		strconv.FormatInt(record.TransactionCount, 10),
		strconv.FormatInt(record.ResourceSampleCount, 10),
		strconv.FormatInt(record.IntervalSummaryCount, 10),
	}
	return strings.Join(parts, "|")
}

func cloneRunArchiveReport(report *RunArchiveReport) (*RunArchiveReport, error) {
	if report == nil {
		return nil, nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	var cloned RunArchiveReport
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func cachedRunArchiveManifest(record *storage.RunArchiveRecord) (*RunArchiveManifest, error) {
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, err
	}
	key := runArchiveReportCacheKey(record)
	if key != "" {
		if cached, ok := runArchiveManifestCache.Load(key); ok {
			return cloneRunArchiveManifest(cached.(*RunArchiveManifest))
		}
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return manifest, nil
	}
	cacheCopy, err := cloneRunArchiveManifest(manifest)
	if err != nil {
		return nil, err
	}
	runArchiveManifestCache.Store(key, cacheCopy)
	return manifest, nil
}

func cloneRunArchiveManifest(manifest *RunArchiveManifest) (*RunArchiveManifest, error) {
	if manifest == nil {
		return nil, nil
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	var cloned RunArchiveManifest
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}
