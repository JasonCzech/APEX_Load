package importreview

import (
	"container/list"
	"sync"
)

const (
	MaskedScriptCacheEntries = 16
	MaskedScriptCacheBytes   = 64 << 20
)

var MaskedScripts = NewMaskedScriptCache(MaskedScriptCacheEntries, MaskedScriptCacheBytes)

type MaskedScriptCache struct {
	mu         sync.Mutex
	maxEntries int
	maxBytes   int
	bytes      int
	order      *list.List
	entries    map[[32]byte]*list.Element
}

type MaskedScriptEntry struct {
	key     [32]byte
	encoded []byte
}

func NewMaskedScriptCache(maxEntries, maxBytes int) *MaskedScriptCache {
	return &MaskedScriptCache{maxEntries: maxEntries, maxBytes: maxBytes, order: list.New(), entries: make(map[[32]byte]*list.Element)}
}

func (c *MaskedScriptCache) Get(key [32]byte) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil
	}
	c.order.MoveToFront(element)
	return element.Value.(*MaskedScriptEntry).encoded
}

func (c *MaskedScriptCache) Put(key [32]byte, encoded []byte) {
	if len(encoded) > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.order.MoveToFront(element)
		return
	}
	c.entries[key] = c.order.PushFront(&MaskedScriptEntry{key: key, encoded: encoded})
	c.bytes += len(encoded)
	for c.order.Len() > c.maxEntries || c.bytes > c.maxBytes {
		oldest := c.order.Back()
		entry := oldest.Value.(*MaskedScriptEntry)
		c.order.Remove(oldest)
		delete(c.entries, entry.key)
		c.bytes -= len(entry.encoded)
	}
}
