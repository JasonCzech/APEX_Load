// Package contentmutation owns local mutation ordering and the publication
// fence for storage-backed content. It has no dependency on a database, server,
// or domain model; durable revision checks remain the repository's responsibility.
package contentmutation

import (
	"sync"
	"sync/atomic"
)

// Coordinator serializes writers independently of the mutex protecting live
// runtime state. The zero value is ready to use. Do not copy after first use.
// Lock must be acquired before any live-state mutex and retained until the
// committed result has been published (or the failed operation has returned).
// It does not replace the repository's cross-process compare-and-swap fence.
type Coordinator struct {
	writers    sync.Mutex
	generation atomic.Uint64
}

func (c *Coordinator) Lock() {
	c.writers.Lock()
	c.generation.Add(1) // odd generations mean a write is in progress
}

func (c *Coordinator) Unlock() {
	c.generation.Add(1)
	c.writers.Unlock()
}

// Observation identifies a read started outside a mutation. A read overlapping
// a writer must not publish its snapshot, even if the write ultimately fails:
// an ambiguous storage failure can have committed before returning an error.
type Observation uint64

func (c *Coordinator) Observe() Observation { return Observation(c.generation.Load()) }

// CanPublish must be checked while holding the same live-state mutex the
// writers use to begin their snapshot and publish their result. Readers also
// retain their domain's deletion/incarnation and cache-revision checks.
func (c *Coordinator) CanPublish(o Observation) bool {
	return uint64(o)%2 == 0 && c.generation.Load() == uint64(o)
}

// OutsideLock executes one repository operation while releasing the live-state
// mutex, then reacquires it before returning or unwinding a panic. The caller
// must own the mutex and its Coordinator, and pass detached inputs to operation.
// No cache or other state protected by live may be read by operation.
func OutsideLock[T any](live sync.Locker, operation func() (T, error)) (T, error) {
	live.Unlock()
	defer live.Lock()
	return operation()
}

// WriteOutsideLock is OutsideLock for repository operations without a result.
func WriteOutsideLock(live sync.Locker, operation func() error) error {
	_, err := OutsideLock(live, func() (struct{}, error) { return struct{}{}, operation() })
	return err
}
