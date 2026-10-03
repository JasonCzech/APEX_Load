package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// RuntimeResourcePlan is a target-free, immutable profile of protocol resources.
// Publication TPS and configured starting VUsers are not resource ceilings.
type RuntimeResourcePlan struct {
	protocolName                              string
	initOnce                                  bool
	initConsume, mainConsume, teardownConsume bool
	partitions                                int64
	open                                      bool
	maxWorkers, iterations, globalIterations  int64
}

func NewRuntimeResourcePlan(script *dsl.Script, initStrategy string) (RuntimeResourcePlan, error) {
	if script == nil {
		return RuntimeResourcePlan{}, nil
	}
	plan := RuntimeResourcePlan{protocolName: strings.ToLower(strings.TrimSpace(script.Protocol))}
	if plan.protocolName != "eventhubs" {
		return plan, nil
	}
	mode := strings.TrimSpace(script.Config.EventHubs.ConsumerMode)
	if mode != "" && mode != "observer" && mode != "processor" && mode != "exclusive" {
		return plan, fmt.Errorf("invalid Event Hubs consumer mode")
	}
	if mode != "processor" {
		return plan, nil
	}
	ceiling := script.Config.EventHubs.ProcessorMaxPartitions
	if ceiling == 0 {
		ceiling = 32
	}
	if ceiling < 1 || ceiling > 32 {
		return plan, fmt.Errorf("Event Hubs processor partition ceiling must be between 1 and 32")
	}
	plan.partitions = int64(ceiling)
	switch strings.ToLower(strings.TrimSpace(initStrategy)) {
	case "", "at_runtime":
	case "at_once":
		plan.initOnce = true
	default:
		return plan, fmt.Errorf("invalid init strategy for runtime resources")
	}
	if err := dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if !strings.EqualFold(strings.TrimSpace(ref.Action.Type), "eventhubs_consume") {
			return nil
		}
		switch ref.Phase {
		case dslwalk.Init:
			plan.initConsume = true
		case dslwalk.Actions:
			plan.mainConsume = true
		case dslwalk.Teardown:
			plan.teardownConsume = true
		}
		return nil
	}); err != nil {
		return RuntimeResourcePlan{}, err
	}
	plan.open = isOpenWorkload(script)
	if plan.open {
		plan.maxWorkers = int64(openWorkloadInFlightLimit(script))
		plan.iterations = int64(script.Config.RunLogic.Iterations)
		plan.globalIterations = int64(script.Config.RunLogic.GlobalIterations)
	}
	return plan, nil
}

func (plan RuntimeResourcePlan) RequiresReservation() bool {
	return plan.partitions > 0 && (plan.initConsume || plan.mainConsume || plan.teardownConsume)
}

func (plan RuntimeResourcePlan) Demand(peakVUsers int) (protocol.RuntimeResourceDemand, error) {
	if peakVUsers < 0 {
		return protocol.RuntimeResourceDemand{}, fmt.Errorf("runtime resource peak must be non-negative")
	}
	if !plan.RequiresReservation() {
		return protocol.RuntimeResourceDemand{}, nil
	}
	concurrent := int64(peakVUsers)
	if plan.open && concurrent > 0 {
		concurrent = plan.maxWorkers
		if plan.iterations > 0 {
			concurrent = min(concurrent, saturatingPositiveProduct(plan.iterations, int64(peakVUsers)))
		}
		if plan.globalIterations > 0 {
			concurrent = min(concurrent, plan.globalIterations)
		}
	}
	processors := concurrent
	if plan.initOnce {
		processors = 0
		if plan.mainConsume {
			processors = concurrent
		}
		if plan.initConsume || plan.teardownConsume {
			processors = max(processors, 1)
		}
	}
	if processors == 0 {
		return protocol.RuntimeResourceDemand{}, nil
	}
	if processors < 0 || processors > math.MaxInt64/plan.partitions {
		return protocol.RuntimeResourceDemand{}, fmt.Errorf("runtime resource demand overflow")
	}
	return protocol.RuntimeResourceDemand{Processors: processors, Workers: processors * plan.partitions, MaxPartitionsPerProcessor: plan.partitions}, nil
}

type runtimeResourceBinding struct {
	script       *dsl.Script
	initStrategy string
	plan         RuntimeResourcePlan
	peak         int
	floor        int
	frozen       bool
	reservation  protocol.RuntimeResourceReservation
	cleanupOwned bool
}

type engineRuntimeResources struct {
	mu              sync.Mutex
	bindings        map[string]*runtimeResourceBinding
	adopted, closed bool
	started         bool
}

func newEngineRuntimeResources(groups []GroupConfig) (*engineRuntimeResources, error) {
	resources := &engineRuntimeResources{bindings: map[string]*runtimeResourceBinding{}}
	for _, group := range groups {
		plan, err := NewRuntimeResourcePlan(group.Script, group.InitStrategy)
		if err != nil {
			return nil, err
		}
		peak := group.RuntimeResourcePeakVUsers
		if peak == 0 {
			peak = group.VUsers
		}
		if peak < group.VUsers || peak < 0 {
			return nil, fmt.Errorf("runtime resource peak is below configured VUsers")
		}
		if _, err := plan.Demand(peak); err != nil {
			return nil, err
		}
		if plan.RequiresReservation() || group.RuntimeResourceReservation != nil {
			resources.bindings[group.Name] = &runtimeResourceBinding{script: group.Script, initStrategy: group.InitStrategy, plan: plan, peak: peak, floor: peak, frozen: group.RuntimeResourcePeakFrozen, reservation: group.RuntimeResourceReservation}
		}
	}
	return resources, nil
}

func (resources *engineRuntimeResources) reserve(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if resources == nil {
		return nil
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if err := ctx.Err(); err != nil {
		resources.releaseLocked()
		return err
	}
	if resources.closed {
		return fmt.Errorf("runtime resource reservation is closed")
	}
	fail := func(err error) error { resources.releaseLocked(); return err }
	names := make([]string, 0, len(resources.bindings))
	for name := range resources.bindings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		binding := resources.bindings[name]
		currentPlan, err := NewRuntimeResourcePlan(binding.script, binding.initStrategy)
		if err != nil || currentPlan != binding.plan {
			return fail(fmt.Errorf("runtime resource plan changed before execution"))
		}
		demand, err := binding.plan.Demand(binding.peak)
		if err != nil {
			return fail(err)
		}
		if binding.reservation != nil {
			if !binding.reservation.Current() || binding.reservation.ResourceProtocol() != binding.plan.protocolName || binding.reservation.Demand() != demand {
				return fail(fmt.Errorf("runtime resource reservation does not match execution demand"))
			}
			continue
		}
		if demand.Processors == 0 {
			continue
		}
		instance, err := protocol.Get(binding.plan.protocolName)
		if err != nil {
			return fail(err)
		}
		binding.reservation, err = protocol.ReserveRuntimeResources(ctx, instance, demand)
		if err != nil {
			return fail(err)
		}
		if binding.reservation == nil {
			return fail(fmt.Errorf("runtime resource reservation is unavailable"))
		}
		binding.cleanupOwned = true
	}
	return nil
}

func (resources *engineRuntimeResources) adopt(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := resources.reserve(ctx); err != nil {
		return err
	}
	if resources == nil {
		return nil
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if len(resources.bindings) == 0 {
		return nil
	}
	if resources.adopted || resources.closed {
		return fmt.Errorf("runtime resource execution is already consumed")
	}
	for _, binding := range resources.bindings {
		if binding.reservation != nil {
			if err := binding.reservation.Adopt(ctx); err != nil {
				if errors.Is(err, protocol.ErrRuntimeResourceAlreadyAdopted) {
					binding.reservation = nil
				}
				resources.releaseLocked()
				return err
			}
			binding.cleanupOwned = true
		}
	}
	resources.adopted = true
	return nil
}

func (resources *engineRuntimeResources) bind(ctx context.Context, group string) context.Context {
	if resources == nil {
		return ctx
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if binding := resources.bindings[group]; binding != nil {
		return protocol.WithRuntimeResourceReservation(ctx, binding.plan.protocolName, binding.reservation)
	}
	return ctx
}

func (resources *engineRuntimeResources) releaseLocked() {
	if len(resources.bindings) == 0 {
		return
	}
	resources.closed = true
	for _, binding := range resources.bindings {
		if binding.reservation != nil {
			if binding.cleanupOwned {
				binding.reservation.Release()
			} else {
				// A repeated inherited pointer may already belong to another Run.
				// Only the provider can atomically discard a never-adopted handle.
				protocol.ReleaseUnadoptedRuntimeResourceReservation(binding.reservation)
			}
		}
	}
}
func (resources *engineRuntimeResources) release() {
	if resources == nil {
		return
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	resources.releaseLocked()
}
func (resources *engineRuntimeResources) current() bool {
	if resources == nil {
		return true
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if resources.closed {
		return false
	}
	for _, binding := range resources.bindings {
		demand, err := binding.plan.Demand(binding.peak)
		if err != nil || !demand.IsZero() && binding.reservation == nil || binding.reservation != nil && !binding.reservation.Current() {
			return false
		}
	}
	return true
}
func (resources *engineRuntimeResources) demands() map[string]protocol.RuntimeResourceDemand {
	out := map[string]protocol.RuntimeResourceDemand{}
	if resources == nil {
		return out
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	for name, binding := range resources.bindings {
		demand, _ := binding.plan.Demand(binding.peak)
		out[name] = demand
	}
	return out
}

// resize admits a closed-group increase before its target changes. Existing
// retirement claims remain charged by the provider until their actual exit.
func (resources *engineRuntimeResources) resize(ctx context.Context, group string, required int) error {
	if resources == nil {
		return nil
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	binding := resources.bindings[group]
	if binding == nil || required <= binding.peak {
		return nil
	}
	if binding.frozen {
		return fmt.Errorf("runtime resource target exceeds frozen peak")
	}
	if resources.closed {
		return fmt.Errorf("runtime resource reservation is closed")
	}
	demand, err := binding.plan.Demand(required)
	if err != nil {
		return err
	}
	if demand.Processors == 0 {
		binding.peak = required
		return nil
	}
	if binding.reservation == nil {
		instance, err := protocol.Get(binding.plan.protocolName)
		if err != nil {
			return err
		}
		reservation, err := protocol.ReserveRuntimeResources(ctx, instance, demand)
		if err != nil {
			return err
		}
		if reservation == nil {
			return fmt.Errorf("runtime resource reservation is unavailable")
		}
		if resources.adopted {
			if err := reservation.Adopt(ctx); err != nil {
				reservation.Release()
				return err
			}
		}
		binding.reservation = reservation
		binding.cleanupOwned = true
	} else if err := binding.reservation.Resize(ctx, demand); err != nil {
		return err
	}
	binding.peak = required
	return nil
}

// ReserveRuntimeResources reserves target-free process demand before readiness.
// It does not adopt the lease or initialize any protocol client.
func (mge *MultiGroupEngine) ReserveRuntimeResources(ctx context.Context) error {
	if mge == nil {
		return fmt.Errorf("multi-group engine is unavailable")
	}
	return mge.runtimeResources.reserve(ctx)
}
func (mge *MultiGroupEngine) RuntimeResourceDemands() map[string]protocol.RuntimeResourceDemand {
	if mge == nil {
		return map[string]protocol.RuntimeResourceDemand{}
	}
	return mge.runtimeResources.demands()
}
func (mge *MultiGroupEngine) RuntimeResourceReservationsCurrent() bool {
	return mge != nil && mge.runtimeResources.current()
}
func (e *Engine) ReserveRuntimeResources(ctx context.Context) error {
	if e == nil {
		return fmt.Errorf("engine is unavailable")
	}
	return e.runtimeResources.reserve(ctx)
}
func (e *Engine) ClosePreparedRuntime() error {
	if e != nil {
		e.runtimeResources.release()
	}
	return nil
}

// reconcileDesired releases only an unused manual scale extension. Pinned
// planned peaks remain reserved; live child entitlements remain provider-owned.
func (resources *engineRuntimeResources) reconcileDesired(ctx context.Context, group string, configured, live int) error {
	if resources == nil {
		return nil
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	binding := resources.bindings[group]
	if binding == nil || resources.closed {
		return nil
	}
	required := max(binding.floor, configured, live)
	if required >= binding.peak {
		return nil
	}
	demand, err := binding.plan.Demand(required)
	if err != nil {
		return err
	}
	if binding.reservation != nil {
		if demand.IsZero() {
			// Reducing a desired population to zero must preserve the parent's
			// immutable child ceiling while the provider holds draining work.
			demand.MaxPartitionsPerProcessor = binding.reservation.Demand().MaxPartitionsPerProcessor
		}
		if err := binding.reservation.Resize(context.WithoutCancel(ctx), demand); err != nil {
			return err
		}
		if demand.IsZero() {
			// Abandon the empty desired parent, retaining any actual children in
			// the provider's drain accounting. A later manual growth must obtain
			// a fresh reservation rather than reactivate retiring clients.
			if binding.cleanupOwned {
				binding.reservation.Release()
			} else {
				protocol.ReleaseUnadoptedRuntimeResourceReservation(binding.reservation)
			}
			binding.reservation = nil
			binding.cleanupOwned = false
		}
	}
	binding.peak = required
	return nil
}

func (mge *MultiGroupEngine) reconcileRuntimeResourceCapacityLocked() error {
	live := mge.liveVUsersByGroupLocked()
	for _, group := range mge.groups {
		if err := mge.runtimeResources.reconcileDesired(context.Background(), group.Name, group.VUsers, int(live[group.Name])); err != nil {
			return err
		}
	}
	return nil
}

var errRuntimeResourceExecutionConsumed = errors.New("runtime resource execution is already consumed")

// beginRun claims cleanup ownership before any launch gate can fail. A duplicate
// Run must not install cleanup that releases the first execution's resources.
func (resources *engineRuntimeResources) beginRun() error {
	if resources == nil {
		return nil
	}
	resources.mu.Lock()
	defer resources.mu.Unlock()
	if len(resources.bindings) == 0 {
		return nil
	}
	if resources.started {
		return errRuntimeResourceExecutionConsumed
	}
	if resources.closed {
		return fmt.Errorf("runtime resource reservation is closed")
	}
	resources.started = true
	return nil
}
