package generatorrpc

import (
	"fmt"
	"strings"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type javaVuserCapacityObservation struct {
	probed       bool
	capabilities protocol.RuntimeCapabilities
	supported    bool
	err          error
}

type javaVuserCapacityDrain struct {
	persistentDemand int64
	persistentActive bool
	legacyActive     bool
}

func observeJavaVuserCapacity() javaVuserCapacityObservation {
	instance, err := protocol.Get("java_vuser")
	if err != nil {
		return javaVuserCapacityObservation{probed: true, err: err}
	}
	capabilities, supported, err := protocol.ReadRuntimeCapabilities(instance)
	return javaVuserCapacityObservation{
		probed:       true,
		capabilities: capabilities,
		supported:    supported,
		err:          err,
	}
}

func (observation javaVuserCapacityObservation) persistentMaxVUsers() (int64, bool) {
	if !observation.probed || observation.err != nil || !observation.supported ||
		!observation.capabilities.Available ||
		!protocol.RuntimeFeaturesSatisfy(observation.capabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		observation.capabilities.MaxVUsers <= 0 {
		return 0, false
	}
	return observation.capabilities.MaxVUsers, true
}

func (observation javaVuserCapacityObservation) persistentRuntimeError() error {
	if observation.err != nil {
		return observation.err
	}
	if !observation.supported {
		return fmt.Errorf("java_vuser runtime does not advertise governed capabilities")
	}
	if !observation.capabilities.Available {
		return fmt.Errorf("java_vuser runtime is unavailable on this generator")
	}
	if missing := protocol.MissingJavaVuserPersistentRuntimeFeature(observation.capabilities.Features); missing != "" {
		return fmt.Errorf("java_vuser runtime does not advertise required capability %s", missing)
	}
	if observation.capabilities.MaxVUsers <= 0 {
		return fmt.Errorf("java_vuser persistent runtime does not advertise a positive VUser capacity")
	}
	return nil
}

func (service *Service) assignmentJavaVuserCapacityObservation(validated validatedWorkAssignment) javaVuserCapacityObservation {
	if validated.request == nil {
		return javaVuserCapacityObservation{}
	}
	request := validated.request
	var cached cachedScript
	if len(request.GetScriptContent()) > 0 {
		protocolName, requiresPhaseLifecycle, capacity := cachedScriptRuntimeProfile(request.GetScriptContent())
		cached = cachedScript{
			protocol:               protocolName,
			requiresPhaseLifecycle: requiresPhaseLifecycle,
			javaVuserCapacity:      capacity,
		}
	} else {
		var ok bool
		cached, ok = service.scriptCache.get(request.GetScriptId())
		if !ok {
			return javaVuserCapacityObservation{}
		}
	}
	if !strings.EqualFold(cached.protocol, "java_vuser") || !cached.requiresPhaseLifecycle {
		return javaVuserCapacityObservation{}
	}
	return observeJavaVuserCapacity()
}

func (service *Service) activePersistentJavaVusersLocked() int64 {
	total := service.persistentJavaDrainDemandLocked()
	for _, assignment := range service.assignments {
		total += service.assignmentPersistentJavaDemandLocked(assignment)
	}
	return total
}

func (service *Service) assignmentHasPersistentJavaLocked(assignment assignedTest) bool {
	for _, scriptID := range assignment.groupScripts {
		cached, ok := service.scriptCache.get(scriptID)
		if ok && strings.EqualFold(cached.protocol, "java_vuser") && cached.requiresPhaseLifecycle {
			return true
		}
	}
	return false
}

func (service *Service) assignmentHasLegacyJavaLocked(assignment assignedTest) bool {
	for _, scriptID := range assignment.groupScripts {
		cached, ok := service.scriptCache.get(scriptID)
		if ok && strings.EqualFold(cached.protocol, "java_vuser") && !cached.requiresPhaseLifecycle {
			return true
		}
	}
	return false
}

func (service *Service) assignmentPersistentJavaDemandLocked(assignment assignedTest) int64 {
	var total int64
	for groupName, scriptID := range assignment.groupScripts {
		cached, ok := service.scriptCache.get(scriptID)
		if !ok || !cached.requiresPhaseLifecycle {
			continue
		}
		total += cached.javaVuserCapacity.Demand(int(assignment.groups[groupName]))
	}
	return total
}

func (service *Service) reservePersistentJavaDrainLocked(demand int64) uint64 {
	return service.reserveJavaVuserDrainLocked(demand, true, false)
}

func (service *Service) reserveAssignmentJavaDrainLocked(assignment assignedTest) uint64 {
	return service.reserveJavaVuserDrainLocked(
		service.assignmentPersistentJavaDemandLocked(assignment),
		service.assignmentHasPersistentJavaLocked(assignment),
		service.assignmentHasLegacyJavaLocked(assignment),
	)
}

func (service *Service) reserveLegacyJavaDrainLocked() uint64 {
	return service.reserveJavaVuserDrainLocked(0, false, true)
}

func (service *Service) reserveJavaVuserDrainLocked(persistentDemand int64, persistentActive, legacyActive bool) uint64 {
	if persistentDemand <= 0 && !persistentActive && !legacyActive {
		return 0
	}
	if service.persistentJavaDrains == nil {
		service.persistentJavaDrains = make(map[uint64]javaVuserCapacityDrain)
	}
	service.nextPersistentJavaDrain++
	if service.nextPersistentJavaDrain == 0 {
		service.nextPersistentJavaDrain++
	}
	token := service.nextPersistentJavaDrain
	service.persistentJavaDrains[token] = javaVuserCapacityDrain{
		persistentDemand: max(persistentDemand, 0),
		persistentActive: persistentActive,
		legacyActive:     legacyActive,
	}
	return token
}

func (service *Service) releasePersistentJavaDrainLocked(token uint64) {
	if token == 0 {
		return
	}
	delete(service.persistentJavaDrains, token)
}

func (service *Service) persistentJavaDrainDemandLocked() int64 {
	var total int64
	for _, drain := range service.persistentJavaDrains {
		total += drain.persistentDemand
	}
	return total
}

func (service *Service) legacyJavaDrainActiveLocked() bool {
	for _, drain := range service.persistentJavaDrains {
		if drain.legacyActive {
			return true
		}
	}
	return false
}

func (service *Service) persistentJavaDrainActiveLocked() bool {
	for _, drain := range service.persistentJavaDrains {
		if drain.persistentActive {
			return true
		}
	}
	return false
}

func (coordinator *assignmentLifecycleCoordinator) pendingPersistentJavaVusersLocked() int64 {
	var total int64
	for _, pending := range coordinator.pendingStages {
		if pending != nil {
			total += pending.persistentJavaDemand
		}
	}
	return total
}

func (coordinator *assignmentLifecycleCoordinator) pendingPersistentJavaLifecycleLocked() bool {
	for _, pending := range coordinator.pendingStages {
		if pending != nil && pending.persistentJavaLifecycle {
			return true
		}
	}
	return false
}

func (coordinator *assignmentLifecycleCoordinator) pendingLegacyJavaLifecycleLocked() bool {
	for _, pending := range coordinator.pendingStages {
		if pending != nil && pending.legacyJavaLifecycle {
			return true
		}
	}
	return false
}

func (service *Service) persistentJavaRuntimeOccupiedLocked() bool {
	if service.persistentJavaDrainActiveLocked() {
		return true
	}
	for _, assignment := range service.assignments {
		if service.assignmentHasPersistentJavaLocked(assignment) {
			return true
		}
	}
	return service.lifecycleCoordinator != nil &&
		service.lifecycleCoordinator.pendingPersistentJavaLifecycleLocked()
}

func (service *Service) legacyJavaRuntimeOccupiedLocked() bool {
	if service.legacyJavaDrainActiveLocked() {
		return true
	}
	for _, assignment := range service.assignments {
		if service.assignmentHasLegacyJavaLocked(assignment) {
			return true
		}
	}
	return service.lifecycleCoordinator != nil &&
		service.lifecycleCoordinator.pendingLegacyJavaLifecycleLocked()
}

func (service *Service) heartbeatJavaVuserCapacityInUseLocked() int64 {
	if service.legacyJavaRuntimeOccupiedLocked() {
		// The exact instantaneous one-shot occupancy is internal to the runner.
		// Prevent persistent placement while legacy work can still acquire any
		// of the shared process slots.
		return int64(^uint64(0) >> 1)
	}
	inUse := service.activePersistentJavaVusersLocked()
	if service.lifecycleCoordinator != nil {
		inUse += service.lifecycleCoordinator.pendingPersistentJavaVusersLocked()
	}
	return inUse
}

func (service *Service) heartbeatJavaVuserOccupancyModeLocked() string {
	persistent := service.persistentJavaRuntimeOccupiedLocked()
	legacy := service.legacyJavaRuntimeOccupiedLocked()
	switch {
	case persistent:
		// Both modes should be impossible under admission control. If corrupt
		// state ever exposes both, advertising persistent combines with the
		// legacy sentinel capacity usage to reject both placement modes.
		return RuntimeOccupancyModePersistent
	case legacy:
		return RuntimeOccupancyModeLegacy
	default:
		return RuntimeOccupancyModeIdle
	}
}

// adjustmentJavaVuserCapacityObservation resolves the target group's cached
// lifecycle profile while briefly holding a read lock, then performs any
// external runner probe only after releasing it.
func (service *Service) adjustmentJavaVuserCapacityObservation(request *pb.VUserAdjustment) javaVuserCapacityObservation {
	if request == nil {
		return javaVuserCapacityObservation{}
	}
	service.mu.RLock()
	assignment, ok := service.assignments[request.GetTestId()]
	scriptID := ""
	currentVUsers := int32(0)
	if ok {
		scriptID = assignment.groupScripts[request.GetGroupName()]
		currentVUsers = assignment.groups[request.GetGroupName()]
	}
	service.mu.RUnlock()
	if scriptID == "" || request.GetTargetVusers() <= currentVUsers {
		return javaVuserCapacityObservation{}
	}
	cached, ok := service.scriptCache.get(scriptID)
	if !ok || !cached.requiresPhaseLifecycle {
		return javaVuserCapacityObservation{}
	}
	return observeJavaVuserCapacity()
}
