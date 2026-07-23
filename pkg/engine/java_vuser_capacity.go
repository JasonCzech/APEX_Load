package engine

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// JavaVuserCapacityPlan is the compact, immutable admission profile for a
// java_vuser script. It retains only workload bounds so distributed caches can
// account for pinned JVM sessions without retaining another parsed script.
type JavaVuserCapacityPlan struct {
	persistent        bool
	open              bool
	maxLiveWorkers    int64
	iterationsPerUser int64
	globalIterations  int64
}

// javaVuserCapacityCoordinator owns process-wide admission for persistent Java
// VUser sessions. The protocol-level process semaphore remains the final fork
// guard, while this coordinator prevents multiple Engine/MultiGroupEngine runs
// from each admitting up to the full advertised generator limit.
var persistentJavaCapacity = struct {
	sync.Mutex
	limit        int64
	reserved     int64
	reservations map[*javaVuserCapacityReservation]struct{}
	legacyRuns   int64
}{reservations: make(map[*javaVuserCapacityReservation]struct{})}

type javaVuserCapacityReservation struct {
	amount   int64
	limit    int64
	released bool
}

type javaVuserLegacyRunReservation struct {
	released bool
}

func reserveJavaVuserCapacity(demand, limit int64) (*javaVuserCapacityReservation, error) {
	if demand < 0 {
		return nil, fmt.Errorf("java_vuser persistent runtime capacity demand must be non-negative")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("java_vuser persistent runtime does not advertise a positive VUser capacity")
	}
	persistentJavaCapacity.Lock()
	defer persistentJavaCapacity.Unlock()
	if persistentJavaCapacity.legacyRuns > 0 {
		return nil, fmt.Errorf("persistent java_vuser lifecycle cannot share process slots with an active legacy full_lifecycle run")
	}

	reservation := &javaVuserCapacityReservation{amount: demand, limit: limit}
	effectiveLimit := minJavaVuserCapacityLimitLocked(limit)
	if demand > 0 && demand > effectiveLimit-persistentJavaCapacity.reserved {
		return nil, fmt.Errorf(
			"java_vuser persistent runtime capacity exceeded: requires %d additional VUsers, %d reserved, maximum %d",
			demand,
			persistentJavaCapacity.reserved,
			effectiveLimit,
		)
	}
	if persistentJavaCapacity.reservations == nil {
		persistentJavaCapacity.reservations = make(map[*javaVuserCapacityReservation]struct{})
	}
	persistentJavaCapacity.reservations[reservation] = struct{}{}
	persistentJavaCapacity.limit = effectiveLimit
	persistentJavaCapacity.reserved += demand
	return reservation, nil
}

func reserveLegacyJavaVuserRun() (*javaVuserLegacyRunReservation, error) {
	persistentJavaCapacity.Lock()
	defer persistentJavaCapacity.Unlock()
	if len(persistentJavaCapacity.reservations) > 0 {
		return nil, fmt.Errorf("legacy java_vuser full_lifecycle cannot share process slots with an active persistent lifecycle run")
	}
	persistentJavaCapacity.legacyRuns++
	return &javaVuserLegacyRunReservation{}, nil
}

func (reservation *javaVuserLegacyRunReservation) Release() {
	if reservation == nil {
		return
	}
	persistentJavaCapacity.Lock()
	defer persistentJavaCapacity.Unlock()
	if reservation.released {
		return
	}
	if persistentJavaCapacity.legacyRuns > 0 {
		persistentJavaCapacity.legacyRuns--
	}
	reservation.released = true
}

func (reservation *javaVuserCapacityReservation) Resize(target int64) error {
	return reservation.resizeWithinLimit(target, 0)
}

func (reservation *javaVuserCapacityReservation) resizeWithinLimit(target, observedLimit int64) error {
	if reservation == nil {
		if target == 0 {
			return nil
		}
		return fmt.Errorf("java_vuser persistent runtime capacity reservation is unavailable")
	}
	if target < 0 {
		return fmt.Errorf("java_vuser persistent runtime capacity target must be non-negative")
	}
	persistentJavaCapacity.Lock()
	defer persistentJavaCapacity.Unlock()
	if reservation.released {
		return fmt.Errorf("java_vuser persistent runtime capacity reservation is released")
	}
	if observedLimit > 0 && (reservation.limit <= 0 || observedLimit < reservation.limit) {
		reservation.limit = observedLimit
	}
	effectiveLimit := minJavaVuserCapacityLimitLocked(0)
	persistentJavaCapacity.limit = effectiveLimit
	if target > reservation.amount {
		increase := target - reservation.amount
		if effectiveLimit <= 0 || increase > effectiveLimit-persistentJavaCapacity.reserved {
			return fmt.Errorf(
				"java_vuser persistent runtime capacity exceeded: requires %d additional VUsers, %d reserved, maximum %d",
				increase,
				persistentJavaCapacity.reserved,
				effectiveLimit,
			)
		}
		persistentJavaCapacity.reserved += increase
	} else {
		persistentJavaCapacity.reserved -= reservation.amount - target
		if persistentJavaCapacity.reserved < 0 {
			persistentJavaCapacity.reserved = 0
		}
	}
	reservation.amount = target
	return nil
}

func (reservation *javaVuserCapacityReservation) Release() {
	if reservation == nil {
		return
	}
	persistentJavaCapacity.Lock()
	defer persistentJavaCapacity.Unlock()
	if reservation.released {
		return
	}
	delete(persistentJavaCapacity.reservations, reservation)
	persistentJavaCapacity.reserved -= reservation.amount
	if persistentJavaCapacity.reserved < 0 {
		persistentJavaCapacity.reserved = 0
	}
	persistentJavaCapacity.limit = minJavaVuserCapacityLimitLocked(0)
	reservation.amount = 0
	reservation.released = true
}

func minJavaVuserCapacityLimitLocked(additional int64) int64 {
	effective := additional
	for reservation := range persistentJavaCapacity.reservations {
		if reservation == nil || reservation.released || reservation.limit <= 0 {
			continue
		}
		if effective <= 0 || reservation.limit < effective {
			effective = reservation.limit
		}
	}
	return effective
}

// NewJavaVuserCapacityPlan captures the maximum simultaneously live Actions
// sessions required by a Java script. Explicit init/action/end calls need one
// persistent session per live VUser; legacy full_lifecycle calls are one-shot.
func NewJavaVuserCapacityPlan(script *dsl.Script) JavaVuserCapacityPlan {
	if script == nil ||
		!strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") ||
		!javaVuserScriptUsesPhaseLifecycle(script) {
		return JavaVuserCapacityPlan{}
	}
	plan := JavaVuserCapacityPlan{persistent: true}
	if !isOpenWorkload(script) {
		return plan
	}
	plan.open = true
	plan.maxLiveWorkers = int64(openWorkloadInFlightLimit(script))
	plan.iterationsPerUser = int64(script.Config.RunLogic.Iterations)
	plan.globalIterations = int64(script.Config.RunLogic.GlobalIterations)
	return plan
}

func javaVuserScriptUsesLegacyOneShot(script *dsl.Script) bool {
	return script != nil &&
		strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") &&
		javaVuserScriptHasLegacyRunAction(script) &&
		!javaVuserScriptUsesPhaseLifecycle(script)
}

func (plan JavaVuserCapacityPlan) RequiresPersistentLifecycle() bool {
	return plan.persistent
}

// Demand returns the maximum persistent JVM sessions that can be live for the
// configured VUser count. Open workloads use their live-worker ceiling,
// bounded by any finite per-worker or global arrival limit.
func (plan JavaVuserCapacityPlan) Demand(vusers int) int64 {
	if !plan.persistent || vusers <= 0 {
		return 0
	}
	if !plan.open {
		return int64(vusers)
	}
	demand := plan.maxLiveWorkers
	if demand <= 0 {
		demand = int64(vusers)
	}
	if plan.iterationsPerUser > 0 {
		finite := saturatingPositiveProduct(plan.iterationsPerUser, int64(vusers))
		if finite < demand {
			demand = finite
		}
	}
	if plan.globalIterations > 0 && plan.globalIterations < demand {
		demand = plan.globalIterations
	}
	if demand < 0 {
		return 0
	}
	return demand
}

func saturatingPositiveProduct(left, right int64) int64 {
	if left <= 0 || right <= 0 {
		return 0
	}
	if left > math.MaxInt64/right {
		return math.MaxInt64
	}
	return left * right
}

// persistentJavaScaleCapacityDemand projects the pinned sessions immediately
// after a closed-group scale reservation. Configured demand covers VUsers that
// have not spawned yet; live demand covers VUsers still tearing down. A
// scale-up adds its positive delta because those new sessions can overlap with
// VUsers from an earlier scale-down that have not been reaped yet.
func persistentJavaScaleCapacityDemand(groups []GroupConfig, liveByGroup map[string]int64, targetIndex, targetVUsers int) int64 {
	var total int64
	for index := range groups {
		plan := NewJavaVuserCapacityPlan(groups[index].Script)
		if !plan.RequiresPersistentLifecycle() {
			continue
		}
		configuredDemand := plan.Demand(groups[index].VUsers)
		groupDemand := max(configuredDemand, liveByGroup[groups[index].Name])
		if index == targetIndex {
			// New sessions can overlap old sessions that are still tearing
			// down, but open-workload demand may be capped by max-in-flight or
			// finite arrivals. Add only the capacity-plan increase, not the raw
			// VUser delta.
			if increase := plan.Demand(targetVUsers) - configuredDemand; increase > 0 {
				groupDemand += increase
			}
		}
		total += groupDemand
	}
	return total
}

func javaVuserPersistentCapacityForInstance(instance protocol.Protocol) (protocol.RuntimeCapabilities, error) {
	capabilities, supported, err := protocol.ReadRuntimeCapabilities(instance)
	if err != nil {
		return protocol.RuntimeCapabilities{}, err
	}
	if !supported {
		return protocol.RuntimeCapabilities{}, fmt.Errorf("java_vuser runtime does not advertise governed capabilities")
	}
	if !capabilities.Available {
		return protocol.RuntimeCapabilities{}, fmt.Errorf("java_vuser runtime is unavailable")
	}
	if missing := protocol.MissingJavaVuserPersistentRuntimeFeature(capabilities.Features); missing != "" {
		return protocol.RuntimeCapabilities{}, fmt.Errorf("java_vuser runtime does not advertise required capability %s", missing)
	}
	if capabilities.MaxVUsers <= 0 {
		return protocol.RuntimeCapabilities{}, fmt.Errorf("java_vuser persistent runtime does not advertise a positive VUser capacity")
	}
	return capabilities, nil
}

func validateJavaVuserRuntimeForPlan(ctx context.Context, instance protocol.Protocol, plan JavaVuserCapacityPlan, demand int64) error {
	_, err := validatedJavaVuserRuntimeCapacityForPlan(ctx, instance, plan, demand)
	return err
}

func validatedJavaVuserRuntimeCapacityForPlan(ctx context.Context, instance protocol.Protocol, plan JavaVuserCapacityPlan, demand int64) (protocol.RuntimeCapabilities, error) {
	if !plan.RequiresPersistentLifecycle() {
		legacy, ok := instance.(interface {
			LegacyRuntimeAvailable() bool
			LegacyRuntimeHealthy() bool
		})
		if !ok || !legacy.LegacyRuntimeAvailable() || !legacy.LegacyRuntimeHealthy() {
			return protocol.RuntimeCapabilities{}, fmt.Errorf("java_vuser legacy one-shot runtime is unavailable")
		}
		return protocol.RuntimeCapabilities{}, nil
	}
	capabilities, err := javaVuserPersistentCapacityForInstance(instance)
	if err != nil {
		return protocol.RuntimeCapabilities{}, err
	}
	if demand > capabilities.MaxVUsers {
		return protocol.RuntimeCapabilities{}, fmt.Errorf("java_vuser persistent runtime capacity exceeded: requires %d VUsers, maximum %d", demand, capabilities.MaxVUsers)
	}
	if healthy, supported, healthErr := protocol.CheckRuntimeHealth(ctx, instance); supported {
		if healthErr != nil {
			return protocol.RuntimeCapabilities{}, healthErr
		}
		if !healthy {
			return protocol.RuntimeCapabilities{}, fmt.Errorf("java_vuser governed runner failed its capability health check")
		}
	}
	return capabilities, nil
}

// availableJavaVuserPersistentCapacity is an early constructor admission
// check. Missing/unavailable capabilities remain authoritative preflight
// errors, while an advertised capacity can reject impossible loads early.
func availableJavaVuserPersistentCapacity() (int64, bool, error) {
	instance, err := protocol.Get("java_vuser")
	if err != nil {
		return 0, false, nil
	}
	capabilities, supported, err := protocol.ReadRuntimeCapabilities(instance)
	if err != nil {
		return 0, false, err
	}
	if !supported || !capabilities.Available ||
		!protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		capabilities.MaxVUsers <= 0 {
		return 0, false, nil
	}
	return capabilities.MaxVUsers, true, nil
}
