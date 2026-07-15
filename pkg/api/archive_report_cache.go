package api

import (
	"container/list"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	runArchiveReportCacheEntries   = 16
	runArchiveManifestCacheEntries = 128
	runArchiveReportCacheBytes     = int64(128 << 20)
	runArchiveManifestCacheBytes   = int64(64 << 20)
)

type boundedArchiveCacheEntry[T any] struct {
	key   string
	value *T
	size  int64
}

type boundedArchiveCacheCall[T any] struct {
	done  chan struct{}
	value *T
	err   error
	cache bool
	size  int64
}

// boundedArchiveCache is a small LRU with per-key singleflight. Reports can be
// sizable, so an unbounded process-global map leaks memory as archives are
// viewed over the lifetime of a controller.
type boundedArchiveCache[T any] struct {
	mu       sync.Mutex
	max      int
	maxBytes int64
	bytes    int64
	weigh    func(*T) int64
	items    map[string]*list.Element
	lru      list.List
	inFlight map[string]*boundedArchiveCacheCall[T]
}

func newBoundedArchiveCache[T any](max int) *boundedArchiveCache[T] {
	return newWeightedBoundedArchiveCache[T](max, 0, nil)
}

func newWeightedBoundedArchiveCache[T any](max int, maxBytes int64, weigh func(*T) int64) *boundedArchiveCache[T] {
	if max < 1 {
		max = 1
	}
	return &boundedArchiveCache[T]{
		max:      max,
		maxBytes: maxBytes,
		weigh:    weigh,
		items:    make(map[string]*list.Element),
		inFlight: make(map[string]*boundedArchiveCacheCall[T]),
	}
}

func (c *boundedArchiveCache[T]) getOrLoad(key string, load func() (*T, error)) (*T, error) {
	if key == "" {
		return load()
	}
	c.mu.Lock()
	if element := c.items[key]; element != nil {
		c.lru.MoveToFront(element)
		value := element.Value.(*boundedArchiveCacheEntry[T]).value
		c.mu.Unlock()
		return value, nil
	}
	if call := c.inFlight[key]; call != nil {
		c.mu.Unlock()
		<-call.done
		return call.value, call.err
	}
	call := &boundedArchiveCacheCall[T]{done: make(chan struct{}), cache: true}
	c.inFlight[key] = call
	c.mu.Unlock()

	call.value, call.err = load()
	loadedSize := int64(0)
	cacheEligible := true
	if call.err == nil && call.value != nil && c.weigh != nil {
		loadedSize = c.weigh(call.value)
		if loadedSize < 0 || (c.maxBytes > 0 && loadedSize > c.maxBytes) {
			cacheEligible = false
		}
	}

	c.mu.Lock()
	delete(c.inFlight, key)
	call.size = loadedSize
	call.cache = call.cache && cacheEligible
	if call.cache && call.err == nil && call.value != nil {
		element := c.lru.PushFront(&boundedArchiveCacheEntry[T]{key: key, value: call.value, size: call.size})
		c.items[key] = element
		c.bytes += call.size
		for c.lru.Len() > c.max || (c.maxBytes > 0 && c.bytes > c.maxBytes) {
			oldest := c.lru.Back()
			entry := oldest.Value.(*boundedArchiveCacheEntry[T])
			delete(c.items, entry.key)
			c.bytes -= entry.size
			c.lru.Remove(oldest)
		}
	}
	close(call.done)
	c.mu.Unlock()
	return call.value, call.err
}

func (c *boundedArchiveCache[T]) deleteTest(testID string) {
	prefix := strings.TrimSpace(testID) + "|"
	if prefix == "|" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, element := range c.items {
		if strings.HasPrefix(key, prefix) {
			c.bytes -= element.Value.(*boundedArchiveCacheEntry[T]).size
			delete(c.items, key)
			c.lru.Remove(element)
		}
	}
	for key, call := range c.inFlight {
		if strings.HasPrefix(key, prefix) {
			call.cache = false
		}
	}
}

func (c *boundedArchiveCache[T]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

type archiveJSONCountingWriter struct {
	bytes int64
}

func (w *archiveJSONCountingWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > int64(^uint64(0)>>1)-w.bytes {
		return 0, io.ErrShortWrite
	}
	w.bytes += int64(len(data))
	return len(data), nil
}

func archiveJSONWeight[T any](value *T) int64 {
	if value == nil {
		return 0
	}
	writer := &archiveJSONCountingWriter{}
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return -1
	}
	return writer.bytes
}

var runArchiveReportCache = newWeightedBoundedArchiveCache(runArchiveReportCacheEntries, runArchiveReportCacheBytes, archiveJSONWeight[RunArchiveReport])
var runArchiveManifestCache = newWeightedBoundedArchiveCache(runArchiveManifestCacheEntries, runArchiveManifestCacheBytes, archiveJSONWeight[RunArchiveManifest])

// boundedCache is the small untyped LRU shared by lightweight process caches
// whose callers need explicit Load, Store, and Delete operations.
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
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	return element.Value.(*boundedCacheEntry).value, true
}

func (c *boundedCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.order.Remove(element)
		delete(c.entries, key)
	}
}

func (c *boundedCache) Store(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		element.Value.(*boundedCacheEntry).value = value
		c.order.MoveToFront(element)
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
	report, err := runArchiveReportCache.getOrLoad(key, func() (*RunArchiveReport, error) {
		return buildRunArchiveReport(record)
	})
	if err != nil {
		return nil, err
	}
	return cloneRunArchiveReport(report)
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
	lease, err := acquireArchiveMaterialized(context.Background(), record)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	key := runArchiveReportCacheKey(record)
	manifest, err := runArchiveManifestCache.getOrLoad(key, func() (*RunArchiveManifest, error) {
		return loadRunArchiveManifestFromRecord(record)
	})
	if err != nil {
		return nil, err
	}
	return cloneRunArchiveManifest(manifest)
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

func forgetRunArchiveCache(testID string) {
	runArchiveReportCache.deleteTest(testID)
	runArchiveManifestCache.deleteTest(testID)
}
