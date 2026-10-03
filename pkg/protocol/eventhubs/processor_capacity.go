package eventhubs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	MaxProcessors                  = 4
	MaxProcessorWorkers            = 32
	MaxProcessorPartitions         = 32
	FeatureProcessorReservationsV1 = "eventhubs_processor_reservations_v1"
)

var ErrProcessorResourceCapacity = errors.New("Event Hubs processor resource capacity exceeded")

type processorLimits struct{ processors, workers, partitions int64 }

// These startup settings can only lower the established 4/32/32 ceilings.
// Invalid configuration fails resource advertisement/admission until restart.
func processorLimitsFromEnvironment() (processorLimits, error) {
	limits := processorLimits{MaxProcessors, MaxProcessorWorkers, MaxProcessorPartitions}
	for _, setting := range []struct {
		name    string
		value   *int64
		maximum int64
	}{
		{"APEXLOAD_EVENTHUBS_MAX_PROCESSORS", &limits.processors, MaxProcessors},
		{"APEXLOAD_EVENTHUBS_MAX_WORKERS", &limits.workers, MaxProcessorWorkers},
		{"APEXLOAD_EVENTHUBS_MAX_PARTITIONS_PER_PROCESSOR", &limits.partitions, MaxProcessorPartitions},
	} {
		value := strings.TrimSpace(os.Getenv(setting.name))
		if value == "" {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1 || n > setting.maximum {
			return processorLimits{}, fmt.Errorf("%s must be between 1 and %d", setting.name, setting.maximum)
		}
		*setting.value = n
	}
	return limits, nil
}

type processorPool struct {
	mu           sync.Mutex
	limits       processorLimits
	reservations map[*ProcessorReservation]struct{}
}

func newProcessorPool(limits processorLimits) (*processorPool, error) {
	if limits.processors < 1 || limits.processors > MaxProcessors || limits.workers < 1 || limits.workers > MaxProcessorWorkers || limits.partitions < 1 || limits.partitions > MaxProcessorPartitions {
		return nil, errors.New("Event Hubs processor limits must only lower the 4/32/32 ceilings")
	}
	return &processorPool{limits: limits, reservations: make(map[*ProcessorReservation]struct{})}, nil
}

var processProcessorPool struct {
	sync.Once
	pool *processorPool
	err  error
}

func defaultProcessorPool() (*processorPool, error) {
	processProcessorPool.Do(func() {
		limits, err := processorLimitsFromEnvironment()
		if err == nil {
			processProcessorPool.pool, err = newProcessorPool(limits)
		}
		processProcessorPool.err = err
	})
	return processProcessorPool.pool, processProcessorPool.err
}

// ProcessorReservation is issued only by this process's shared pool. No fields
// are serializable or script-selected. Children spend its reserved entitlement;
// they do not separately charge the process-wide totals.
type ProcessorReservation struct {
	pool              *processorPool
	demand            protocol.RuntimeResourceDemand
	adopted, released bool
	children          map[*processorLease]struct{}
}

type processorLease struct {
	parent          *ProcessorReservation
	closing         bool
	cleanupUnproven bool
	workers         map[string]*processorWorkerLease
}

type processorWorkerLease struct {
	processor *processorLease
	partition string
}

func resourceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (p *processorPool) reserve(ctx context.Context, demand protocol.RuntimeResourceDemand) (*ProcessorReservation, error) {
	ctx = resourceContext(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !demand.Valid() || demand.MaxPartitionsPerProcessor > p.limits.partitions {
		return nil, errors.New("invalid Event Hubs processor resource demand or partition ceiling")
	}
	if demand.IsZero() {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	capacity := p.snapshotLocked()
	if demand.Processors > capacity.AvailableProcessors || demand.Workers > capacity.AvailableWorkers {
		return nil, fmt.Errorf("%w: requested %d processors/%d workers, available %d/%d", ErrProcessorResourceCapacity, demand.Processors, demand.Workers, capacity.AvailableProcessors, capacity.AvailableWorkers)
	}
	r := &ProcessorReservation{pool: p, demand: demand, children: make(map[*processorLease]struct{})}
	p.reservations[r] = struct{}{}
	return r, nil
}

func (r *ProcessorReservation) ResourceProtocol() string { return eventHubsProtocolName }
func (r *ProcessorReservation) Demand() protocol.RuntimeResourceDemand {
	if r == nil || r.pool == nil {
		return protocol.RuntimeResourceDemand{}
	}
	r.pool.mu.Lock()
	defer r.pool.mu.Unlock()
	return r.demand
}
func (r *ProcessorReservation) currentLocked() bool {
	if r == nil || r.pool == nil || r.released {
		return false
	}
	_, current := r.pool.reservations[r]
	return current
}
func (r *ProcessorReservation) Current() bool {
	if r == nil || r.pool == nil {
		return false
	}
	r.pool.mu.Lock()
	defer r.pool.mu.Unlock()
	return r.currentLocked()
}
func (r *ProcessorReservation) Adopt(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return errors.New("Event Hubs processor reservation is unavailable")
	}
	r.pool.mu.Lock()
	defer r.pool.mu.Unlock()
	// Ownership wins over operation cancellation: a duplicate owner must never
	// mistake this for its own cancelled stage and Release a foreign live lease.
	if r.adopted {
		return protocol.ErrRuntimeResourceAlreadyAdopted
	}
	if err := resourceContext(ctx).Err(); err != nil {
		return err
	}
	if !r.currentLocked() {
		return errors.New("Event Hubs processor reservation is no longer current")
	}
	r.adopted = true
	return nil
}
func (r *ProcessorReservation) chargedProcessorsLocked() int64 {
	target := r.demand.Processors
	if r.released {
		target = 0
	}
	return max(target, int64(len(r.children)))
}
func (r *ProcessorReservation) drainingProcessorsLocked() int64 {
	if r.released {
		return int64(len(r.children))
	}
	closing := int64(0)
	for child := range r.children {
		if child.closing {
			closing++
		}
	}
	return max(max(int64(0), int64(len(r.children))-r.demand.Processors), closing)
}
func (r *ProcessorReservation) Resize(ctx context.Context, demand protocol.RuntimeResourceDemand) error {
	if r == nil || r.pool == nil {
		return errors.New("Event Hubs processor reservation is unavailable")
	}
	if !demand.Valid() {
		return errors.New("invalid Event Hubs processor resource demand")
	}
	p := r.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := resourceContext(ctx).Err(); err != nil {
		return err
	}
	if !r.currentLocked() || demand.MaxPartitionsPerProcessor != r.demand.MaxPartitionsPerProcessor {
		return errors.New("Event Hubs reservation ended or partition ceiling changed")
	}
	oldCharge := r.chargedProcessorsLocked()
	newCharge := max(demand.Processors, int64(len(r.children)))
	capacity := p.snapshotLocked()
	increase := max(int64(0), newCharge-oldCharge)
	if increase > capacity.AvailableProcessors || increase > capacity.AvailableWorkers/demand.MaxPartitionsPerProcessor {
		return fmt.Errorf("%w: resize requested %d processors/%d workers", ErrProcessorResourceCapacity, demand.Processors, demand.Workers)
	}
	r.demand = demand
	if demand.IsZero() {
		// Zero is terminal for this parent, even if draining children still
		// exist. A later growth needs a new issued/adopted reservation; zero
		// charge never retains an unbounded dormant registry entry.
		r.released = true
		if len(r.children) == 0 {
			delete(p.reservations, r)
		}
	}
	return nil
}
func (r *ProcessorReservation) Release() {
	if r == nil || r.pool == nil {
		return
	}
	r.pool.mu.Lock()
	defer r.pool.mu.Unlock()
	r.released = true
	if len(r.children) == 0 {
		delete(r.pool.reservations, r)
	}
}
func (r *ProcessorReservation) ReleaseUnadopted() bool {
	if r == nil || r.pool == nil {
		return false
	}
	r.pool.mu.Lock()
	defer r.pool.mu.Unlock()
	if !r.currentLocked() || r.adopted {
		return false
	}
	r.released = true
	delete(r.pool.reservations, r)
	return true
}
func (r *ProcessorReservation) acquireProcessor(ctx context.Context) (*processorLease, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("Event Hubs processor reservation is unavailable")
	}
	r.pool.mu.Lock()
	defer r.pool.mu.Unlock()
	if err := resourceContext(ctx).Err(); err != nil {
		return nil, err
	}
	if !r.currentLocked() || !r.adopted || int64(len(r.children)) >= r.demand.Processors {
		return nil, fmt.Errorf("Event Hubs processor reservation has no executable entitlement: current=%t adopted=%t desired=%d active=%d draining=%d", r.currentLocked(), r.adopted, r.demand.Processors, len(r.children), r.drainingProcessorsLocked())
	}
	child := &processorLease{parent: r, workers: make(map[string]*processorWorkerLease)}
	r.children[child] = struct{}{}
	return child, nil
}
func (l *processorLease) acquireWorker(partition string) (*processorWorkerLease, error) {
	if l == nil || l.parent == nil || partition == "" || len(partition) > 64 {
		return nil, errors.New("invalid Event Hubs processor partition")
	}
	r := l.parent
	r.pool.mu.Lock()
	defer r.pool.mu.Unlock()
	if !r.currentLocked() || !r.adopted || l.closing {
		return nil, errors.New("Event Hubs processor lease ended")
	}
	if _, current := r.children[l]; !current {
		return nil, errors.New("Event Hubs processor lease ended")
	}
	if l.workers[partition] != nil || int64(len(l.workers)) >= r.demand.MaxPartitionsPerProcessor {
		return nil, errors.New("Event Hubs processor partition worker entitlement exceeded")
	}
	worker := &processorWorkerLease{processor: l, partition: partition}
	l.workers[partition] = worker
	return worker, nil
}
func (l *processorLease) release() {
	if l == nil || l.parent == nil {
		return
	}
	r := l.parent
	r.pool.mu.Lock()
	defer r.pool.mu.Unlock()
	l.closing = true
	l.removeIfClosedLocked()
}
func (l *processorLease) removeIfClosedLocked() {
	if !l.closing || l.cleanupUnproven || len(l.workers) != 0 {
		return
	}
	r := l.parent
	delete(r.children, l)
	if r.released && len(r.children) == 0 {
		delete(r.pool.reservations, r)
	}
}

// An SDK partition that could not acquire a worker still needs proven cleanup.
// There is no worker handle to retain, so pin its whole processor entitlement.
func (l *processorLease) quarantine() {
	if l == nil || l.parent == nil {
		return
	}
	l.parent.pool.mu.Lock()
	defer l.parent.pool.mu.Unlock()
	l.cleanupUnproven = true
	l.closing = true
}
func (w *processorWorkerLease) release() {
	if w == nil || w.processor == nil {
		return
	}
	l := w.processor
	l.parent.pool.mu.Lock()
	defer l.parent.pool.mu.Unlock()
	if l.workers[w.partition] == w {
		delete(l.workers, w.partition)
	}
	l.removeIfClosedLocked()
}
func (p *processorPool) snapshotLocked() protocol.RuntimeResourceCapacity {
	out := protocol.RuntimeResourceCapacity{Available: true, MaxProcessors: p.limits.processors, MaxWorkers: p.limits.workers, MaxPartitionsPerProcessor: p.limits.partitions}
	for r := range p.reservations {
		charge := r.chargedProcessorsLocked()
		out.ReservedProcessors += charge
		out.ReservedWorkers += charge * r.demand.MaxPartitionsPerProcessor
		out.ActiveProcessors += int64(len(r.children))
		draining := r.drainingProcessorsLocked()
		for child := range r.children {
			out.ActiveWorkers += int64(len(child.workers))
		}
		out.DrainingProcessors += draining
		out.DrainingWorkers += draining * r.demand.MaxPartitionsPerProcessor
	}
	out.AvailableProcessors = out.MaxProcessors - out.ReservedProcessors
	out.AvailableWorkers = out.MaxWorkers - out.ReservedWorkers
	return out
}
func (p *processorPool) snapshot() protocol.RuntimeResourceCapacity {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

func (*EventHubs) RuntimeResourceCapacity() protocol.RuntimeResourceCapacity {
	if !realEventHubsAvailable {
		return protocol.RuntimeResourceCapacity{}
	}
	pool, err := defaultProcessorPool()
	if err != nil {
		return protocol.RuntimeResourceCapacity{}
	}
	return pool.snapshot()
}
func (*EventHubs) ReserveRuntimeResources(ctx context.Context, demand protocol.RuntimeResourceDemand) (protocol.RuntimeResourceReservation, error) {
	if !realEventHubsAvailable {
		return nil, errors.New("Event Hubs processor reservations require an eventhubs_real build")
	}
	pool, err := defaultProcessorPool()
	if err != nil {
		return nil, err
	}
	r, err := pool.reserve(ctx, demand)
	if r == nil {
		return nil, err
	}
	return r, err
}

// A present binding is always authoritative, even when invalid. Only a direct
// adapter with no binding may obtain and own a fresh parent reservation.
func acquireProcessorResources(ctx context.Context, cfg config) (*processorLease, bool, error) {
	pool, err := defaultProcessorPool()
	if err != nil {
		return nil, false, err
	}
	ceiling := cfg.ProcessorMaxPartitions
	if ceiling == 0 {
		ceiling = MaxProcessorPartitions
	}
	if ceiling < 1 || ceiling > pool.limits.partitions {
		return nil, false, errors.New("processor partition ceiling exceeds the configured process limit")
	}
	parent := cfg.processorReservation
	bound := cfg.processorReservationBound
	if !bound {
		if reservation, present := protocol.RuntimeResourceReservationFromContext(ctx, eventHubsProtocolName); present {
			bound = true
			parent, _ = reservation.(*ProcessorReservation)
		}
	}
	owned := !bound
	if owned {
		parent, err = pool.reserve(ctx, protocol.RuntimeResourceDemand{Processors: 1, Workers: ceiling, MaxPartitionsPerProcessor: ceiling})
		if err != nil {
			return nil, false, err
		}
		if err = parent.Adopt(ctx); err != nil {
			parent.Release()
			return nil, false, err
		}
	} else if parent == nil || parent.pool != pool || !parent.Current() || parent.Demand().MaxPartitionsPerProcessor != ceiling {
		return nil, false, errors.New("bound Event Hubs processor reservation is unavailable or mismatched")
	}
	child, err := parent.acquireProcessor(ctx)
	if err != nil && owned {
		parent.Release()
	}
	return child, owned, err
}
