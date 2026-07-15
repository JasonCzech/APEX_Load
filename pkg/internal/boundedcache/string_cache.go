// Package boundedcache provides small process-wide caches whose admission and
// retention are bounded by both entry count and key bytes. It is intentionally
// simple: these caches protect compilation hot paths, not authoritative state.
package boundedcache

import "sync"

const (
	DefaultMaxEntries       = 1024
	DefaultMaxKeyBytes      = 4096
	DefaultMaxTotalKeyBytes = 256 << 10
)

// StringCache is a concurrency-safe FIFO cache for string-keyed values. An
// oversized key is returned to the caller but is never retained. Callers must
// use bounded values or compiled artifacts derived solely from the bounded key;
// the cache cannot infer the retained size of an arbitrary generic V.
type StringCache[V any] struct {
	mu               sync.Mutex
	items            map[string]V
	order            []string
	maxEntries       int
	maxKeyBytes      int
	maxTotalKeyBytes int
	totalKeyBytes    int
}

func NewStringCache[V any](maxEntries, maxKeyBytes, maxTotalKeyBytes int) *StringCache[V] {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	if maxKeyBytes <= 0 {
		maxKeyBytes = DefaultMaxKeyBytes
	}
	if maxTotalKeyBytes <= 0 {
		maxTotalKeyBytes = DefaultMaxTotalKeyBytes
	}
	return &StringCache[V]{
		items:            make(map[string]V),
		maxEntries:       maxEntries,
		maxKeyBytes:      maxKeyBytes,
		maxTotalKeyBytes: maxTotalKeyBytes,
	}
}

func (c *StringCache[V]) Load(key string) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}
	if len(key) > c.maxKeyBytes || len(key) > c.maxTotalKeyBytes {
		return zero, false
	}
	c.mu.Lock()
	value, ok := c.items[key]
	c.mu.Unlock()
	return value, ok
}

// LoadOrStore mirrors sync.Map.LoadOrStore. When key is too large for
// admission, value is returned with loaded=false without mutating the cache.
func (c *StringCache[V]) LoadOrStore(key string, value V) (actual V, loaded bool) {
	if c == nil {
		return value, false
	}
	if len(key) > c.maxKeyBytes || len(key) > c.maxTotalKeyBytes {
		return value, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.items[key]; ok {
		return existing, true
	}
	for len(c.items) >= c.maxEntries || c.totalKeyBytes+len(key) > c.maxTotalKeyBytes {
		if len(c.order) == 0 {
			return value, false
		}
		victim := c.order[0]
		c.order[0] = ""
		c.order = c.order[1:]
		if _, ok := c.items[victim]; ok {
			delete(c.items, victim)
			c.totalKeyBytes -= len(victim)
		}
	}
	c.items[key] = value
	c.order = append(c.order, key)
	c.totalKeyBytes += len(key)
	return value, false
}

func (c *StringCache[V]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}
