package api

import (
	"container/list"
	"sync"
)

// maskScriptSecrets is a pure function of the script's content, and one import
// request masks the same script several times: the draft claim's credential
// fingerprint, binding scopes, reconciliation and the response, then the save
// masks it again. On a multi-thousand-step VuGen import each pass costs seconds.
// Its costly projection is kept by a digest of the unmasked script's JSON, as
// masked JSON, so a repeat costs one decode and a few cheap restores. Only
// masked output is held, and the cache is bounded by entries and bytes.
const (
	maskedScriptCacheEntries = 16
	maskedScriptCacheBytes   = 64 << 20
)

var maskedScripts = newMaskedScriptCache(maskedScriptCacheEntries, maskedScriptCacheBytes)

type maskedScriptCache struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	bytes      int
	order      *list.List
	entries    map[[32]byte]*list.Element
}

type maskedScriptEntry struct {
	key     [32]byte
	encoded []byte
}

func newMaskedScriptCache(maxEntries, maxBytes int) *maskedScriptCache {
	return &maskedScriptCache{maxEntries: maxEntries, maxBytes: maxBytes, order: list.New(), entries: make(map[[32]byte]*list.Element)}
}

// get returns the masked JSON for key, or nil. Callers decode their own copy.
func (c *maskedScriptCache) get(key [32]byte) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil
	}
	c.order.MoveToFront(element)
	return element.Value.(*maskedScriptEntry).encoded
}

func (c *maskedScriptCache) put(key [32]byte, encoded []byte) {
	if len(encoded) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.order.MoveToFront(element)
		return
	}
	c.entries[key] = c.order.PushFront(&maskedScriptEntry{key: key, encoded: encoded})
	c.bytes += len(encoded)
	for c.order.Len() > c.maxEntries || c.bytes > c.maxBytes {
		oldest := c.order.Back()
		entry := oldest.Value.(*maskedScriptEntry)
		c.order.Remove(oldest)
		delete(c.entries, entry.key)
		c.bytes -= len(entry.encoded)
	}
}
