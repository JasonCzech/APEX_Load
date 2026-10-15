package javarunner

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrHostCapacityClosed = errors.New("pooled_admission_closed")

// HostCapacity is the shared, in-memory execution-host budget for a runner.
// Every participating run/group and dedicated JVM must use the same instance.
// Budget memory is operator-reserved memory, not measured usage or an OS limit;
// runner overhead and the sidecar's separate bounded probe need their own headroom.
// Losing this object is not termination evidence. Disposable brokers require a
// durable reservation authority before using this model.
type HostCapacity struct {
	mu      sync.Mutex
	budget  HostBudget
	usage   HostUsage
	closed  bool
	changed chan struct{}
	done    chan struct{}
}

func NewHostCapacity(budget HostBudget) (*HostCapacity, error) {
	if !validHostBudget(budget) {
		return nil, ErrHostState
	}
	return &HostCapacity{budget: budget, changed: make(chan struct{}), done: make(chan struct{})}, nil
}

func validHostBudget(b HostBudget) bool {
	return b.MaxHosts > 0 && b.MaxHosts <= 10000 && b.MaxVUsers > 0 && b.MaxVUsers <= 100000 && b.MaxMemoryBytes > 0
}

func (c *HostCapacity) Snapshot() HostUsage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

// CloseAdmission permanently fences new reservations and wakes dedicated waiters.
// It never releases charges or claims that already-running hosts have stopped.
func (c *HostCapacity) CloseAdmission() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		if c.done != nil {
			close(c.done)
		}
		c.notify()
	}
}

func (c *HostCapacity) notify() {
	if c.changed != nil {
		close(c.changed)
	}
	c.changed = make(chan struct{})
}

// All mutations take only the capacity lock. Capacity never calls into a ledger
// or server, so ledger -> capacity is the only lock ordering.
type hostCharge struct {
	capacity *HostCapacity
	vusers   int
	memory   int64
	released bool
}

func (c *HostCapacity) reserve(vusers int, p HostProfile) ([]*hostCharge, <-chan struct{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, c.changed, ErrHostCapacityClosed
	}
	if !validHostBudget(c.budget) || vusers <= 0 || vusers > 100000 || p.SlotsPerHost <= 0 || p.SlotsPerHost > MaxPooledSlotsPerHost || p.MemoryBytesPerHost <= 0 {
		return nil, c.changed, ErrHostState
	}
	n := (vusers-1)/p.SlotsPerHost + 1
	if n > c.budget.MaxHosts-c.usage.Hosts || vusers > c.budget.MaxVUsers-c.usage.VUsers || int64(n) > (c.budget.MaxMemoryBytes-c.usage.MemoryBytes)/p.MemoryBytesPerHost {
		return nil, c.changed, ErrHostCapacity
	}
	charges := make([]*hostCharge, 0, n)
	for remaining := vusers; remaining > 0; {
		slots := min(remaining, p.SlotsPerHost)
		remaining -= slots
		charges = append(charges, &hostCharge{capacity: c, vusers: slots, memory: p.MemoryBytesPerHost})
	}
	c.usage.Hosts += n
	c.usage.VUsers += vusers
	c.usage.MemoryBytes += int64(n) * p.MemoryBytesPerHost
	return charges, c.changed, nil
}

func (r *hostCharge) closeSlot() {
	if r == nil {
		return
	}
	c := r.capacity
	c.mu.Lock()
	defer c.mu.Unlock()
	if !r.released && r.vusers > 0 {
		r.vusers--
		c.usage.VUsers--
		c.notify()
	}
}

func (r *hostCharge) release() {
	if r == nil {
		return
	}
	c := r.capacity
	c.mu.Lock()
	defer c.mu.Unlock()
	if !r.released {
		r.released = true
		c.usage.Hosts--
		c.usage.VUsers -= r.vusers
		c.usage.MemoryBytes -= r.memory
		r.vusers = 0
		c.notify()
	}
}

// DedicatedHostAdmission binds a trusted dedicated memory reservation to the
// same budget as pooled ledgers. It does not change script VM arguments.
type DedicatedHostAdmission struct {
	capacity *HostCapacity
	memory   int64
}

func NewDedicatedHostAdmission(c *HostCapacity, memoryBytes int64) (*DedicatedHostAdmission, error) {
	if c == nil || !validHostBudget(c.budget) || memoryBytes <= 0 || memoryBytes > c.budget.MaxMemoryBytes {
		return nil, ErrHostState
	}
	return &DedicatedHostAdmission{capacity: c, memory: memoryBytes}, nil
}

// Acquire waits for one host and one Vuser. Release is idempotent, and may only
// be called after definitive no-start or proven whole-process-tree termination.
func (a *DedicatedHostAdmission) Acquire(ctx context.Context) (release func(), err error) {
	if a == nil || a.capacity == nil {
		return nil, ErrHostState
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		charges, changed, err := a.capacity.reserve(1, HostProfile{SlotsPerHost: 1, MemoryBytesPerHost: a.memory})
		if err == nil {
			if err := ctx.Err(); err != nil {
				charges[0].release()
				return nil, err
			}
			return charges[0].release, nil
		}
		if !errors.Is(err, ErrHostCapacity) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (a *DedicatedHostAdmission) CloseAdmission() {
	if a != nil && a.capacity != nil {
		a.capacity.CloseAdmission()
	}
}

// Done signals terminal admission closure, not process termination.
func (a *DedicatedHostAdmission) Done() <-chan struct{} {
	if a == nil || a.capacity == nil {
		return nil
	}
	return a.capacity.done
}

// NewHostLedger joins a pooled run to this dedicated admission authority so a
// runner cannot accidentally wire its two execution modes to different budgets.
func (a *DedicatedHostAdmission) NewHostLedger(binding HostBinding, budget HostBudget, lease time.Duration) (*HostLedger, error) {
	if a == nil {
		return nil, ErrHostState
	}
	return NewHostLedgerWithCapacity(binding, budget, lease, a.capacity)
}

// Snapshot reports current charges against the shared budget, including
// draining and quarantined hosts that still hold their reservations.
func (a *DedicatedHostAdmission) Snapshot() HostUsage {
	if a == nil || a.capacity == nil {
		return HostUsage{}
	}
	return a.capacity.Snapshot()
}

// MemoryBytes is the budget charge of one dedicated JVM.
func (a *DedicatedHostAdmission) MemoryBytes() int64 {
	if a == nil {
		return 0
	}
	return a.memory
}

// Budget returns immutable ceilings, not currently available capacity.
func (a *DedicatedHostAdmission) Budget() HostBudget {
	if a == nil || a.capacity == nil {
		return HostBudget{}
	}
	return a.capacity.budget
}
