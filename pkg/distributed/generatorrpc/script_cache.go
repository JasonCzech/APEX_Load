package generatorrpc

import (
	"fmt"
	"sync"
)

type cachedScript struct {
	content  []byte
	hash     string
	protocol string
	lastUsed uint64
}

// scriptCacheManager owns generator script-cache data, bounds, LRU accounting,
// and synchronization. Service snapshots the active assignment pins while
// holding its coordinator lock, then delegates cache mutation without exposing
// cache internals to RPC handlers.
type scriptCacheManager struct {
	mu            sync.Mutex
	entries       map[string]cachedScript
	entryLimit    int
	retainedBytes int64
	byteLimit     int64
	useCounter    uint64
}

func newScriptCacheManager(entryLimit int, byteLimit int64) *scriptCacheManager {
	if entryLimit <= 0 {
		entryLimit = defaultGeneratorScriptCacheMax
	}
	if byteLimit <= 0 {
		byteLimit = defaultGeneratorScriptMaxBytes
	}
	return &scriptCacheManager{
		entries:    make(map[string]cachedScript),
		entryLimit: entryLimit,
		byteLimit:  byteLimit,
	}
}

func (cache *scriptCacheManager) setEntryLimit(limit int) {
	if cache == nil || limit <= 0 {
		return
	}
	cache.mu.Lock()
	cache.entryLimit = limit
	cache.mu.Unlock()
}

func (cache *scriptCacheManager) setByteLimit(limit int64) {
	if cache == nil || limit <= 0 {
		return
	}
	cache.mu.Lock()
	cache.byteLimit = limit
	cache.mu.Unlock()
}

func (cache *scriptCacheManager) maxBytes() int64 {
	if cache == nil {
		return 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.byteLimit
}

func (cache *scriptCacheManager) put(id string, content []byte, contentHash string, pinned map[string]struct{}) error {
	if cache == nil {
		return fmt.Errorf("generator script cache is unavailable")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()

	contentBytes := int64(len(content))
	if contentBytes > cache.byteLimit {
		return fmt.Errorf("script %q is %d bytes and exceeds generator cache item limit %d", id, contentBytes, cache.byteLimit)
	}
	if existing, ok := cache.entries[id]; ok {
		requiredBytes := cache.retainedBytes - int64(len(existing.content)) + contentBytes
		for requiredBytes > cache.byteLimit {
			if !cache.evictOneExceptLocked(id, pinned) {
				return fmt.Errorf("generator script cache byte limit %d is pinned by active entries", cache.byteLimit)
			}
			requiredBytes = cache.retainedBytes - int64(len(existing.content)) + contentBytes
		}
		cache.useCounter++
		cache.retainedBytes -= int64(len(existing.content))
		existing.content = append(existing.content[:0], content...)
		existing.hash = contentHash
		existing.protocol = scriptProtocol(content)
		existing.lastUsed = cache.useCounter
		cache.entries[id] = existing
		cache.retainedBytes += contentBytes
		return nil
	}
	for len(cache.entries) >= cache.entryLimit || cache.retainedBytes+contentBytes > cache.byteLimit {
		if !cache.evictOneExceptLocked("", pinned) {
			return fmt.Errorf("generator script cache is full (%d entries, %d bytes)", cache.entryLimit, cache.byteLimit)
		}
	}
	cache.useCounter++
	cache.entries[id] = cachedScript{
		content:  append([]byte(nil), content...),
		hash:     contentHash,
		protocol: scriptProtocol(content),
		lastUsed: cache.useCounter,
	}
	cache.retainedBytes += contentBytes
	return nil
}

func (cache *scriptCacheManager) touch(id string) (cachedScript, bool) {
	if cache == nil {
		return cachedScript{}, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cached, ok := cache.entries[id]
	if !ok {
		return cachedScript{}, false
	}
	cache.useCounter++
	cached.lastUsed = cache.useCounter
	cache.entries[id] = cached
	return cached, true
}

func (cache *scriptCacheManager) get(id string) (cachedScript, bool) {
	if cache == nil {
		return cachedScript{}, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cached, ok := cache.entries[id]
	return cached, ok
}

func (cache *scriptCacheManager) snapshot() (map[string]cachedScript, int64) {
	if cache == nil {
		return nil, 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entries := make(map[string]cachedScript, len(cache.entries))
	for id, cached := range cache.entries {
		entries[id] = cached
	}
	return entries, cache.retainedBytes
}

func (cache *scriptCacheManager) evictOneExceptLocked(excludedID string, pinned map[string]struct{}) bool {
	victim := ""
	var oldest uint64
	for id, cached := range cache.entries {
		if id == excludedID {
			continue
		}
		if _, active := pinned[id]; active {
			continue
		}
		if victim == "" || cached.lastUsed < oldest || (cached.lastUsed == oldest && id < victim) {
			victim = id
			oldest = cached.lastUsed
		}
	}
	if victim == "" {
		return false
	}
	cache.retainedBytes -= int64(len(cache.entries[victim].content))
	delete(cache.entries, victim)
	return true
}

// cacheScriptLocked and touchScriptLocked retain the existing Service call
// seam while the protobuf facade is migrated incrementally. The caller holds
// Service.mu for a consistent assignment-pin snapshot; the cache owns its own
// lock and never calls back into Service.
func (s *Service) cacheScriptLocked(id string, content []byte, contentHash string) error {
	pinned := make(map[string]struct{})
	for _, assignment := range s.assignments {
		for _, scriptID := range assignment.groupScripts {
			pinned[scriptID] = struct{}{}
		}
	}
	if s.lifecycleCoordinator != nil {
		for _, pending := range s.lifecycleCoordinator.pendingStages {
			if pending != nil && pending.scriptID != "" {
				pinned[pending.scriptID] = struct{}{}
			}
		}
	}
	return s.scriptCache.put(id, content, contentHash, pinned)
}

func (s *Service) touchScriptLocked(id string) (cachedScript, bool) {
	return s.scriptCache.touch(id)
}
