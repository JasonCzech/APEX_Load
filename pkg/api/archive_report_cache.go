package api

import (
	"container/list"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	runArchiveReportCacheEntries   = 16
	runArchiveManifestCacheEntries = 128
)

type boundedArchiveCacheEntry[T any] struct {
	key   string
	value *T
}

type boundedArchiveCacheCall[T any] struct {
	done  chan struct{}
	value *T
	err   error
	cache bool
}

// boundedArchiveCache is a small LRU with per-key singleflight. Reports can be
// sizable, so an unbounded process-global map leaks memory as archives are
// viewed over the lifetime of a controller.
type boundedArchiveCache[T any] struct {
	mu       sync.Mutex
	max      int
	items    map[string]*list.Element
	lru      list.List
	inFlight map[string]*boundedArchiveCacheCall[T]
}

func newBoundedArchiveCache[T any](max int) *boundedArchiveCache[T] {
	if max < 1 {
		max = 1
	}
	return &boundedArchiveCache[T]{
		max:      max,
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

	c.mu.Lock()
	delete(c.inFlight, key)
	if call.cache && call.err == nil && call.value != nil {
		element := c.lru.PushFront(&boundedArchiveCacheEntry[T]{key: key, value: call.value})
		c.items[key] = element
		for c.lru.Len() > c.max {
			oldest := c.lru.Back()
			delete(c.items, oldest.Value.(*boundedArchiveCacheEntry[T]).key)
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

var runArchiveReportCache = newBoundedArchiveCache[RunArchiveReport](runArchiveReportCacheEntries)
var runArchiveManifestCache = newBoundedArchiveCache[RunArchiveManifest](runArchiveManifestCacheEntries)

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
