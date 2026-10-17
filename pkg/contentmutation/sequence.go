package contentmutation

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// SequenceStore is the compatibility settings persistence surface. Production
// adapters also implement the atomic interfaces for cross-process ownership.
type SequenceStore interface {
	GetSetting(string) (string, error)
	SaveSetting(string, string) error
}

type AtomicSequenceStore interface {
	NextSettingSequence(key string) (int, error)
}

type AtomicSequenceFloorStore interface {
	EnsureSettingSequenceAtLeast(key string, minimum int) (int, error)
}

// SequenceAllocator keeps reference-key storage I/O independent of the live
// runtime mutex. Its mutex preserves the old single-controller
// serialization for compatibility stores that do not implement atomic sequence
// allocation; production stores still provide cross-replica uniqueness.
type SequenceAllocator struct {
	mu       sync.Mutex
	key      string
	store    SequenceStore
	current  int // last successfully persisted/allocated sequence
	observed int // floor learned from a content snapshot, not yet necessarily persisted
}

func NewSequenceAllocator(key string, current int, store SequenceStore) *SequenceAllocator {
	return &SequenceAllocator{key: key, current: current, store: store}
}

func (allocator *SequenceAllocator) Next() (int, error) {
	if allocator == nil {
		return 0, fmt.Errorf("artifact reference sequence allocator is not initialized")
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	minimum := max(allocator.current, allocator.observed)
	if store, ok := allocator.store.(AtomicSequenceStore); ok {
		if minimum > 0 {
			if floorStore, floorOK := allocator.store.(AtomicSequenceFloorStore); floorOK {
				floor, err := floorStore.EnsureSettingSequenceAtLeast(allocator.key, minimum)
				if err != nil {
					return 0, err
				}
				allocator.current = max(allocator.current, floor)
				minimum = max(minimum, floor)
			}
		}
		next, err := store.NextSettingSequence(allocator.key)
		for err == nil && next <= minimum {
			next, err = store.NextSettingSequence(allocator.key)
		}
		if err == nil {
			allocator.current = next
		}
		return next, err
	}
	current := minimum
	if allocator.store != nil {
		raw, err := allocator.store.GetSetting(allocator.key)
		if err != nil {
			return 0, err
		}
		if persisted, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil && persisted > current {
			current = persisted
		}
	}
	next := current + 1
	if allocator.store != nil {
		if err := allocator.store.SaveSetting(allocator.key, strconv.Itoa(next)); err != nil {
			return 0, err
		}
	}
	allocator.current = next
	return next, nil
}

func (allocator *SequenceAllocator) Observe(sequence int) {
	if allocator == nil || sequence <= 0 {
		return
	}
	allocator.mu.Lock()
	if sequence > allocator.observed {
		allocator.observed = sequence
	}
	allocator.mu.Unlock()
}

func (allocator *SequenceAllocator) BindStore(store SequenceStore) {
	if allocator == nil {
		return
	}
	allocator.mu.Lock()
	allocator.store = store
	allocator.mu.Unlock()
}

func (allocator *SequenceAllocator) PersistFloor(minimum int) error {
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	minimum = max(minimum, allocator.observed)
	if minimum <= allocator.current {
		return nil
	}
	if store, ok := allocator.store.(AtomicSequenceFloorStore); ok {
		floor, err := store.EnsureSettingSequenceAtLeast(allocator.key, minimum)
		if err != nil {
			return err
		}
		allocator.current = max(minimum, floor)
		return nil
	}
	if allocator.store != nil {
		raw, err := allocator.store.GetSetting(allocator.key)
		if err != nil {
			return err
		}
		existing, _ := strconv.Atoi(strings.TrimSpace(raw))
		minimum = max(minimum, existing)
		if err := allocator.store.SaveSetting(allocator.key, strconv.Itoa(minimum)); err != nil {
			return err
		}
	}
	allocator.current = minimum
	return nil
}

// Key returns the immutable sequence namespace.
func (allocator *SequenceAllocator) Key() string { return allocator.key }
