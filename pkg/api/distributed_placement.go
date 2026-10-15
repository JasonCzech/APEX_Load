package api

// distributed_placement.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type regionPlacementState struct {
	remaining      map[string]float64
	targetRegion   map[string]string
	weightedRegion map[string]struct{}
}

func newRegionPlacementState(groups []TestGroup, assignment *GeneratorAssignmentSettings, targetSpecs []generatorTargetSpec) *regionPlacementState {
	weights := effectiveRegionWeights(assignment)
	if assignment == nil || (assignment.Mode != generatorAssignmentModeRegionWeighted && assignment.Mode != generatorAssignmentModeLoadZoneWeighted) || len(weights) == 0 {
		return nil
	}
	available := make(map[string]struct{})
	targetRegion := make(map[string]string, len(targetSpecs))
	for _, spec := range targetSpecs {
		targetRegion[spec.Address] = spec.Region
		if spec.Region == "" {
			continue
		}
		available[spec.Region] = struct{}{}
	}
	totalWeight := 0
	remaining := make(map[string]float64)
	weightedRegions := make(map[string]struct{})
	// Region/load-zone weighting is a SOFT preference, not a hard quota. The
	// denominator sums the VUsers of inheriting groups only (pinned and explicit-auto
	// groups are excluded), but it counts ALL inheriting demand even though some may
	// ultimately land in a non-weighted region (e.g. when every weighted region is
	// capacity-exhausted, orderedLoadTargets falls back to capacity ordering and
	// consume() does not decrement for unweighted regions). The result is that the
	// per-region remaining quota can stay slightly inflated; ranking by remaining
	// quota still steers placement toward under-filled weighted regions first, then
	// degrades gracefully to capacity-based selection. This is intentional.
	totalVUsers := 0
	for _, group := range groups {
		if !groupInheritsAssignment(group) {
			continue
		}
		totalVUsers += group.VUsers
	}
	for region, weight := range weights {
		if weight <= 0 {
			continue
		}
		if _, ok := available[region]; !ok {
			continue
		}
		totalWeight += weight
		weightedRegions[region] = struct{}{}
	}
	if totalWeight == 0 || totalVUsers == 0 {
		return nil
	}
	for region, weight := range weights {
		if _, ok := weightedRegions[region]; !ok {
			continue
		}
		remaining[region] = float64(totalVUsers) * float64(weight) / float64(totalWeight)
	}
	return &regionPlacementState{remaining: remaining, targetRegion: targetRegion, weightedRegion: weightedRegions}
}

func (s *regionPlacementState) preference(target string) (float64, bool) {
	if s == nil {
		return 0, false
	}
	region, ok := s.targetRegion[target]
	if !ok || region == "" {
		return 0, false
	}
	_, weighted := s.weightedRegion[region]
	if !weighted {
		return 0, false
	}
	return s.remaining[region], true
}

func (s *regionPlacementState) consume(target string, vusers int) {
	if s == nil {
		return
	}
	region, ok := s.targetRegion[target]
	if !ok || region == "" {
		return
	}
	if _, weighted := s.weightedRegion[region]; !weighted {
		return
	}
	s.remaining[region] -= float64(vusers)
}

func planDistributedGroupTargets(groups []TestGroup, loadByTarget map[string]generatorLoadSnapshot, preferred []string, assignment *GeneratorAssignmentSettings, targetSpecs []generatorTargetSpec) map[string]string {
	return primaryTargetsFromShards(planDistributedGroupShards(groups, loadByTarget, preferred, assignment, targetSpecs))
}

func planDistributedGroupShards(groups []TestGroup, loadByTarget map[string]generatorLoadSnapshot, preferred []string, assignment *GeneratorAssignmentSettings, targetSpecs []generatorTargetSpec) map[string][]GeneratorShardSummary {
	return planDistributedGroupShardsForCandidates(groups, loadByTarget, loadByTarget, preferred, preferred, assignment, targetSpecs)
}

func planDistributedGroupShardsForCandidates(groups []TestGroup, allLoadByTarget map[string]generatorLoadSnapshot, defaultLoadByTarget map[string]generatorLoadSnapshot, preferredAll []string, preferredDefault []string, assignment *GeneratorAssignmentSettings, targetSpecs []generatorTargetSpec) map[string][]GeneratorShardSummary {
	if assignment != nil && assignment.SourceAffinity == "single_generator" {
		return planSourceAffineGroups(groups, defaultLoadByTarget, preferredDefault, assignment, targetSpecs)
	}
	if len(allLoadByTarget) == 0 {
		return nil
	}
	projected := make(map[string]generatorLoadSnapshot, len(allLoadByTarget))
	for key, value := range allLoadByTarget {
		value.RuntimeCapacities = cloneProtocolRuntimeCapacities(value.RuntimeCapacities)
		projected[key] = value
	}
	defaultProjected := filterLoadSnapshots(projected, defaultLoadByTarget)
	regionState := newRegionPlacementState(groups, assignment, targetSpecs)
	compatibility := newGeneratorCompatibilityIndex(targetSpecs, allLoadByTarget)
	shardsByGroup := make(map[string][]GeneratorShardSummary, len(groups))
	for _, group := range groups {
		shards := chooseGeneratorShardsForGroup(group, projected, defaultProjected, preferredAll, preferredDefault, assignment, regionState, compatibility)
		shardsByGroup[group.Name] = shards
		for _, shard := range shards {
			if shard.Target == "" {
				continue
			}
			snapshot := projected[shard.Target]
			if shard.VUsers > 0 {
				snapshot.AvailableCapacity -= int32(shard.VUsers)
				snapshot.Stats.ActiveVUsers += int32(shard.VUsers)
			}
			consumeGroupRuntimeCapacity(&snapshot, group, shard.VUsers)
			consumePodGroupCapacity(&snapshot, group, shard.VUsers)
			projected[shard.Target] = snapshot
			if _, ok := defaultProjected[shard.Target]; ok {
				defaultProjected[shard.Target] = snapshot
			}
			if groupUsesWeightedDefault(group, assignment) {
				regionState.consume(shard.Target, shard.VUsers)
			}
		}
	}
	return shardsByGroup
}

func filterLoadSnapshots(source map[string]generatorLoadSnapshot, allowed map[string]generatorLoadSnapshot) map[string]generatorLoadSnapshot {
	if len(source) == 0 || len(allowed) == 0 {
		return nil
	}
	out := make(map[string]generatorLoadSnapshot, len(allowed))
	for target := range allowed {
		if snapshot, ok := source[target]; ok {
			out[target] = snapshot
		}
	}
	return out
}

func groupInheritsAssignment(group TestGroup) bool {
	return group.Assignment == nil || strings.TrimSpace(group.Assignment.Mode) == "" || group.Assignment.Mode == generatorAssignmentModeInherit
}

func groupUsesWeightedDefault(group TestGroup, assignment *GeneratorAssignmentSettings) bool {
	if !groupInheritsAssignment(group) || assignment == nil {
		return false
	}
	return assignment.Mode == generatorAssignmentModeRegionWeighted || assignment.Mode == generatorAssignmentModeLoadZoneWeighted
}

func effectiveGroupRequiredLabels(group TestGroup, inherited *GeneratorAssignmentSettings) map[string]string {
	if group.Assignment != nil && !groupInheritsAssignment(group) {
		return group.Assignment.RequiredLabels
	}
	if inherited != nil {
		return inherited.RequiredLabels
	}
	return nil
}

func formatRequiredLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, labels[key]))
	}
	return strings.Join(parts, ", ")
}

func chooseGeneratorShardsForGroup(group TestGroup, allLoadByTarget map[string]generatorLoadSnapshot, defaultLoadByTarget map[string]generatorLoadSnapshot, preferredAll []string, preferredDefault []string, inherited *GeneratorAssignmentSettings, regionState *regionPlacementState, compatibility *generatorCompatibilityIndex) []GeneratorShardSummary {
	if javaHostSettings(group) != nil {
		return chooseJavaHostShards(group, allLoadByTarget, defaultLoadByTarget, preferredAll, preferredDefault, inherited, compatibility)
	}
	for _, load := range allLoadByTarget {
		if load.PodCapacity != nil {
			loads, targets, effective, region := defaultLoadByTarget, preferredDefault, inherited, regionState
			if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeSpecificHosts {
				loads = allLoadByTarget
				targets = group.Assignment.SelectedTargets
				region = nil
				for _, target := range targets {
					if _, ok := loads[target]; !ok {
						return nil
					}
					if compatibility != nil && !compatibility.targetCanRun(target, group.Protocol, groupRequiredRuntimeFeature(group), effectiveGroupRequiredLabels(group, inherited)) {
						return nil
					}
				}
			}
			if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeAuto {
				loads = allLoadByTarget
				targets = preferredAll
				effective = group.Assignment
				region = nil
			}
			if inherited != nil && effective != nil {
				copy := *effective
				copy.SharingMode = inherited.SharingMode
				effective = &copy
			}
			return podCapacityShards(group, loads, targets, effective, region, compatibility)
		}
	}
	if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeSpecificHosts {
		targets := make([]string, 0, len(group.Assignment.SelectedTargets))
		requiredLabels := effectiveGroupRequiredLabels(group, inherited)
		for _, target := range group.Assignment.SelectedTargets {
			if _, ok := allLoadByTarget[target]; !ok {
				return nil
			}
			if compatibility != nil && !compatibility.targetCanRun(target, group.Protocol, groupRequiredRuntimeFeature(group), requiredLabels) {
				return nil
			}
			if !runtimeOccupancyAllowsGroup(group, allLoadByTarget[target]) {
				// Occupancy is transient capacity, so a sharded specific-host
				// group may use the remaining selected hosts. Static protocol,
				// feature, and label incompatibility above remains a hard error.
				continue
			}
			targets = append(targets, target)
		}
		return capacityWeightedShardsForGroup(group, targets, allLoadByTarget, group.Assignment.ShardStrategy)
	}

	candidateLoad := defaultLoadByTarget
	candidatePreferred := preferredDefault
	candidateRegionState := regionState
	effectiveAssignment := inherited
	if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeAuto {
		candidateLoad = allLoadByTarget
		candidatePreferred = preferredAll
		candidateRegionState = nil
		effectiveAssignment = group.Assignment
	}
	target := chooseGeneratorTargetForGroup(group, candidateLoad, candidatePreferred, candidateRegionState, compatibility, effectiveGroupRequiredLabels(group, effectiveAssignment))
	if target == "" {
		return nil
	}
	return []GeneratorShardSummary{{Group: group.Name, Target: target, VUsers: group.VUsers}}
}

func capacityWeightedShardsForGroup(group TestGroup, targets []string, loadByTarget map[string]generatorLoadSnapshot, strategy string) []GeneratorShardSummary {
	if javaVuserOccupancyModeForGroup(group) != "" {
		targets = targetsAllowingGroupRuntimeOccupancy(group, targets, loadByTarget)
		if len(targets) == 0 {
			return nil
		}
	}
	plan := javaVuserCapacityPlanForGroup(group)
	if len(targets) == 0 || group.VUsers <= 0 {
		return nil
	}

	adjusted := make(map[string]generatorLoadSnapshot, len(targets))
	maxByTarget := make(map[string]int, len(targets))
	totalMaximum := 0
	for _, target := range targets {
		snapshot, ok := loadByTarget[target]
		if !ok {
			return nil
		}
		// General generator capacity is a hard limit, even when reservation
		// enforcement is disabled. Persistent Java also needs advertised JVM
		// slots; neither limit may be exceeded by weighted rounding.
		maximum := max(0, int(snapshot.AvailableCapacity))
		if plan.RequiresPersistentLifecycle() {
			runtimeMaximum := 0
			if available, advertised, shared := groupJavaRuntimeAvailableVUsers(group, snapshot); advertised && shared {
				runtimeMaximum = int(min(int64(group.VUsers), max(available, 0)))
			} else if advertised {
				runtimeMaximum = maximumVUsersWithinRuntimeCapacity(plan, group.VUsers, available)
			}
			maximum = min(maximum, runtimeMaximum)
		}
		maximum = min(maximum, maximumVUsersWithinRuntimeResources(group, snapshot, maximum))
		if snapshot.AvailableCapacity > int32(maximum) {
			snapshot.AvailableCapacity = int32(maximum)
		}
		maxByTarget[target] = maximum
		if maximum >= group.VUsers-totalMaximum {
			totalMaximum = group.VUsers
		} else {
			totalMaximum += maximum
		}
		adjusted[target] = snapshot
	}
	if totalMaximum < group.VUsers {
		return nil
	}

	allocations := allocateCapacityWeightedVUsers(group.VUsers, targets, adjusted)
	assigned := 0
	for _, target := range targets {
		if allocations[target] > maxByTarget[target] {
			allocations[target] = maxByTarget[target]
		}
		assigned += allocations[target]
	}
	remaining := group.VUsers - assigned
	if remaining > 0 {
		ordered := append([]string(nil), targets...)
		sort.SliceStable(ordered, func(left, right int) bool {
			leftSpare := maxByTarget[ordered[left]] - allocations[ordered[left]]
			rightSpare := maxByTarget[ordered[right]] - allocations[ordered[right]]
			if leftSpare != rightSpare {
				return leftSpare > rightSpare
			}
			leftCapacity := adjusted[ordered[left]].AvailableCapacity
			rightCapacity := adjusted[ordered[right]].AvailableCapacity
			if leftCapacity != rightCapacity {
				return leftCapacity > rightCapacity
			}
			return ordered[left] < ordered[right]
		})
		for _, target := range ordered {
			spare := maxByTarget[target] - allocations[target]
			if spare <= 0 {
				continue
			}
			add := min(spare, remaining)
			allocations[target] += add
			remaining -= add
			if remaining == 0 {
				break
			}
		}
	}
	if remaining != 0 {
		return nil
	}
	if strategy == "" {
		strategy = generatorShardStrategyCapacityWeighted
	}
	shards := make([]GeneratorShardSummary, 0, len(targets))
	for _, target := range targets {
		if allocations[target] <= 0 {
			continue
		}
		shards = append(shards, GeneratorShardSummary{
			Group:         group.Name,
			Target:        target,
			VUsers:        allocations[target],
			ShardStrategy: strategy,
		})
	}
	return shards
}

func capacityWeightedShardsForGroupIncludingZero(group TestGroup, targets []string, loadByTarget map[string]generatorLoadSnapshot, strategy string) []GeneratorShardSummary {
	if javaVuserOccupancyModeForGroup(group) != "" {
		targets = targetsAllowingGroupRuntimeOccupancy(group, targets, loadByTarget)
		if len(targets) == 0 {
			return nil
		}
	}
	if len(targets) == 0 || group.VUsers < 0 {
		return nil
	}
	if strategy == "" {
		strategy = generatorShardStrategyCapacityWeighted
	}
	allocated := map[string]int{}
	if group.VUsers > 0 {
		shards := capacityWeightedShardsForGroup(group, targets, loadByTarget, strategy)
		if len(shards) == 0 {
			return nil
		}
		for _, shard := range shards {
			allocated[shard.Target] = shard.VUsers
		}
	}
	shards := make([]GeneratorShardSummary, 0, len(targets))
	for _, target := range targets {
		if !runtimeResourcesAllowGroup(group, loadByTarget[target], allocated[target]) {
			return nil
		}
		shards = append(shards, GeneratorShardSummary{
			Group:         group.Name,
			Target:        target,
			VUsers:        allocated[target],
			ShardStrategy: strategy,
		})
	}
	return shards
}

func maximumVUsersWithinRuntimeCapacity(plan engine.JavaVuserCapacityPlan, upperBound int, available int64) int {
	if upperBound <= 0 || available <= 0 {
		return 0
	}
	low, high := 0, upperBound
	for low < high {
		middle := low + (high-low+1)/2
		if plan.Demand(middle) <= available {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return low
}

func protocolRuntimeAvailableVUsers(snapshot generatorLoadSnapshot, protocolName string) (int64, bool) {
	found := false
	var available int64
	for _, capacity := range snapshot.RuntimeCapacities {
		if !strings.EqualFold(strings.TrimSpace(capacity.Protocol), protocolName) {
			continue
		}
		if capacity.Available && capacity.MaxVUsers == 0 && capacity.Resources != nil {
			continue
		}
		candidate := int64(0)
		if capacity.Available && capacity.MaxVUsers > 0 && capacity.AvailableVUsers > 0 {
			candidate = min(capacity.AvailableVUsers, capacity.MaxVUsers)
		}
		if !found || candidate < available {
			available = candidate
		}
		found = true
	}
	return available, found
}

func javaVuserOccupancyModeForGroup(group TestGroup) string {
	protocolName := strings.TrimSpace(group.Protocol)
	if group.script != nil && strings.TrimSpace(group.script.Protocol) != "" {
		protocolName = strings.TrimSpace(group.script.Protocol)
	}
	if !strings.EqualFold(protocolName, "java_vuser") {
		return ""
	}
	if javaVuserCapacityPlanForGroup(group).RequiresPersistentLifecycle() {
		return generatorrpc.RuntimeOccupancyModePersistent
	}
	// A java_vuser script without phase methods uses the backward-compatible
	// one-shot runner path. Invalid/empty Java scripts are classified
	// conservatively and will still fail normal script validation.
	return generatorrpc.RuntimeOccupancyModeLegacy
}

func javaVuserCapacityPlanForGroup(group TestGroup) engine.JavaVuserCapacityPlan {
	if group.javaVuserCapacityPlanKnown {
		return group.javaVuserCapacityPlan
	}
	return engine.NewJavaVuserCapacityPlan(group.script)
}

func protocolRuntimeOccupancyMode(snapshot generatorLoadSnapshot, protocolName string) (string, bool) {
	mode := ""
	for _, capacity := range snapshot.RuntimeCapacities {
		if !strings.EqualFold(strings.TrimSpace(capacity.Protocol), protocolName) {
			continue
		}
		candidate := strings.ToLower(strings.TrimSpace(capacity.OccupancyMode))
		if candidate == "" {
			// Empty is the rolling-upgrade representation from generators
			// predating occupancy-mode advertisement.
			continue
		}
		switch candidate {
		case generatorrpc.RuntimeOccupancyModeIdle,
			generatorrpc.RuntimeOccupancyModePersistent,
			generatorrpc.RuntimeOccupancyModeLegacy:
		default:
			return candidate, true
		}
		if mode != "" && mode != candidate {
			return "conflict", true
		}
		mode = candidate
	}
	return mode, mode != ""
}

func runtimeOccupancyAllowsGroup(group TestGroup, snapshot generatorLoadSnapshot) bool {
	required := javaVuserOccupancyModeForGroup(group)
	if required == "" {
		return true
	}
	occupied, advertised := protocolRuntimeOccupancyMode(snapshot, "java_vuser")
	if !advertised {
		// Persistent lifecycle is a governed, capacity-pinned contract and current
		// generators advertise the shared process pool's occupancy with every
		// java_vuser capacity observation. Without it the controller cannot prove
		// that legacy one-shot work is not already using the same slots.
		if required == generatorrpc.RuntimeOccupancyModePersistent {
			return false
		}
		// Preserve rolling-upgrade compatibility for genuinely legacy-only
		// generators. A target that advertises governed persistent lifecycle is
		// current enough to owe an occupancy observation; accepting legacy work
		// there would otherwise race an existing persistent assignment.
		return !stringSliceContainsEqualFold(
			snapshot.CompatibleFeatures,
			protocol.FeatureJavaVuserPersistentLifecycleV1,
		)
	}
	return occupied == generatorrpc.RuntimeOccupancyModeIdle || occupied == required
}

func targetsAllowingGroupRuntimeOccupancy(group TestGroup, targets []string, loadByTarget map[string]generatorLoadSnapshot) []string {
	filtered := make([]string, 0, len(targets))
	for _, target := range targets {
		snapshot, ok := loadByTarget[target]
		if !ok || !runtimeOccupancyAllowsGroup(group, snapshot) {
			continue
		}
		filtered = append(filtered, target)
	}
	return filtered
}

func runtimeCapacityAllowsGroup(group TestGroup, snapshot generatorLoadSnapshot, vusers int) bool {
	if !runtimeResourcesAllowGroup(group, snapshot, vusers) {
		return false
	}
	if !runtimeOccupancyAllowsGroup(group, snapshot) {
		return false
	}
	plan := javaVuserCapacityPlanForGroup(group)
	if !plan.RequiresPersistentLifecycle() {
		return true
	}
	available, advertised, shared := groupJavaRuntimeAvailableVUsers(group, snapshot)
	return advertised && available > 0 && groupJavaRuntimeDemand(group, shared, vusers) <= available
}

// groupJavaRuntimeAvailableVUsers is the java_vuser capacity a group draws on
// at a target: the runner's shared-JVM envelope for a shared-eligible script on
// a generator that hosts shared JVMs, otherwise dedicated JVM slots.
func groupJavaRuntimeAvailableVUsers(group TestGroup, snapshot generatorLoadSnapshot) (available int64, advertised, shared bool) {
	if javaVuserCapacityPlanForGroup(group).SharedJVM() {
		if capacity := protocolRuntimeSharedJVM(snapshot); capacity != nil {
			return capacity.AvailableVUsers, true, true
		}
	}
	available, advertised = protocolRuntimeAvailableVUsers(snapshot, "java_vuser")
	return available, advertised, false
}

// groupJavaRuntimeDemand is the capacity vusers consume: one shared-JVM slot
// each, or the dedicated plan's pinned JVM sessions.
func groupJavaRuntimeDemand(group TestGroup, shared bool, vusers int) int64 {
	if shared {
		return int64(max(vusers, 0))
	}
	return javaVuserCapacityPlanForGroup(group).Demand(vusers)
}

// protocolRuntimeSharedJVM returns a target's shared-JVM envelope, if any.
func protocolRuntimeSharedJVM(snapshot generatorLoadSnapshot) *generatorrpc.SharedJVMCapacity {
	for index := range snapshot.RuntimeCapacities {
		capacity := &snapshot.RuntimeCapacities[index]
		if strings.EqualFold(strings.TrimSpace(capacity.Protocol), "java_vuser") && capacity.Available && capacity.SharedJVM != nil {
			return capacity.SharedJVM
		}
	}
	return nil
}

func consumeGroupRuntimeCapacity(snapshot *generatorLoadSnapshot, group TestGroup, vusers int) {
	consumeGroupRuntimeResources(snapshot, group, vusers)
	if javaHostSettings(group) != nil {
		consumeJavaHostCapacity(snapshot, group, vusers)
		return
	}
	if snapshot == nil {
		return
	}
	occupancyMode := javaVuserOccupancyModeForGroup(group)
	if occupancyMode == "" {
		return
	}
	plan := javaVuserCapacityPlanForGroup(group)
	_, _, shared := groupJavaRuntimeAvailableVUsers(group, *snapshot)
	demand := groupJavaRuntimeDemand(group, shared, vusers)
	for index := range snapshot.RuntimeCapacities {
		if !strings.EqualFold(strings.TrimSpace(snapshot.RuntimeCapacities[index].Protocol), "java_vuser") {
			continue
		}
		capacity := &snapshot.RuntimeCapacities[index]
		currentMode := strings.ToLower(strings.TrimSpace(capacity.OccupancyMode))
		if currentMode == "" || currentMode == generatorrpc.RuntimeOccupancyModeIdle {
			capacity.OccupancyMode = occupancyMode
		}
		if shared && capacity.SharedJVM != nil {
			capacity.SharedJVM.AvailableVUsers = max(0, capacity.SharedJVM.AvailableVUsers-demand)
		} else if plan.RequiresPersistentLifecycle() {
			capacity.AvailableVUsers -= demand
			if capacity.AvailableVUsers < 0 {
				capacity.AvailableVUsers = 0
			}
		}
	}
}

func restoreGroupRuntimeCapacity(snapshot *generatorLoadSnapshot, group TestGroup, vusers int) {
	if snapshot == nil {
		return
	}
	plan := javaVuserCapacityPlanForGroup(group)
	if !plan.RequiresPersistentLifecycle() {
		return
	}
	_, _, shared := groupJavaRuntimeAvailableVUsers(group, *snapshot)
	demand := groupJavaRuntimeDemand(group, shared, vusers)
	for index := range snapshot.RuntimeCapacities {
		capacity := &snapshot.RuntimeCapacities[index]
		if !strings.EqualFold(strings.TrimSpace(capacity.Protocol), "java_vuser") ||
			!capacity.Available || capacity.MaxVUsers <= 0 {
			continue
		}
		if shared && capacity.SharedJVM != nil {
			capacity.SharedJVM.AvailableVUsers = min(capacity.SharedJVM.MaxVUsers, capacity.SharedJVM.AvailableVUsers+demand)
			continue
		}
		capacity.AvailableVUsers += demand
		if capacity.AvailableVUsers > capacity.MaxVUsers {
			capacity.AvailableVUsers = capacity.MaxVUsers
		}
	}
}

func allocateCapacityWeightedVUsers(total int, targets []string, loadByTarget map[string]generatorLoadSnapshot) map[string]int {
	allocations := make(map[string]int, len(targets))
	if total <= 0 || len(targets) == 0 {
		return allocations
	}
	remaining := total
	if total >= len(targets) {
		for _, target := range targets {
			allocations[target] = 1
		}
		remaining -= len(targets)
	}
	if remaining <= 0 {
		return allocations
	}

	type weightedAllocation struct {
		target    string
		weight    float64
		base      int
		remainder float64
		index     int
	}
	weighted := make([]weightedAllocation, 0, len(targets))
	totalWeight := 0.0
	for index, target := range targets {
		weight := float64(loadByTarget[target].AvailableCapacity)
		if weight < 0 {
			weight = 0
		}
		weighted = append(weighted, weightedAllocation{target: target, weight: weight, index: index})
		totalWeight += weight
	}
	if totalWeight <= 0 {
		totalWeight = float64(len(weighted))
		for index := range weighted {
			weighted[index].weight = 1
		}
	}

	assigned := 0
	for index := range weighted {
		exact := float64(remaining) * weighted[index].weight / totalWeight
		base := int(math.Floor(exact))
		weighted[index].base = base
		weighted[index].remainder = exact - float64(base)
		allocations[weighted[index].target] += base
		assigned += base
	}
	sort.SliceStable(weighted, func(left int, right int) bool {
		if weighted[left].remainder != weighted[right].remainder {
			return weighted[left].remainder > weighted[right].remainder
		}
		if weighted[left].weight != weighted[right].weight {
			return weighted[left].weight > weighted[right].weight
		}
		return weighted[left].index < weighted[right].index
	})
	for index, remainingAssigned := 0, remaining-assigned; remainingAssigned > 0; index, remainingAssigned = index+1, remainingAssigned-1 {
		next := weighted[index%len(weighted)]
		allocations[next.target]++
	}
	return allocations
}

func chooseGeneratorTargetForGroup(group TestGroup, loadByTarget map[string]generatorLoadSnapshot, preferred []string, regionState *regionPlacementState, compatibility *generatorCompatibilityIndex, requiredLabels map[string]string) string {
	ordered := orderedLoadTargets(loadByTarget, preferred, regionState)
	if len(ordered) == 0 {
		return ""
	}
	for _, target := range ordered {
		if compatibility != nil && !compatibility.targetCanRun(target, group.Protocol, groupRequiredRuntimeFeature(group), requiredLabels) {
			continue
		}
		if !runtimeCapacityAllowsGroup(group, loadByTarget[target], group.VUsers) {
			continue
		}
		if int64(loadByTarget[target].AvailableCapacity) >= int64(group.VUsers) {
			return target
		}
	}
	return ""
}

func groupRequiredRuntimeFeature(group TestGroup) string {
	if group.script == nil && group.requiredRuntimeFeatureKnown {
		return group.requiredRuntimeFeature
	}
	base := groupBaseRuntimeFeature(group)
	for _, feature := range groupRequiredDataRuntimeFeatures(group) {
		if base != "" {
			base += "+"
		}
		base += feature
	}
	for _, feature := range append(append(append(requiredMessagingFeaturesForGroup(group), dsl.RequiredCompositionRuntimeFeatures(group.script)...), requiredBrowserFeaturesForGroup(group)...), dsl.RequiredHTTPRuntimeFeatures(group.script)...) {
		if base != "" {
			base += "+"
		}
		base += feature
	}
	if group.script != nil && len(group.script.Config.Results.PropertyAnalytics) > 0 {
		if base != "" {
			base += "+"
		}
		base += dsl.FeaturePropertyAnalyticsV1
	}
	if dsl.RequiresJMeterMessaging(group.script) {
		if base != "" {
			base += "+"
		}
		base += dsl.FeatureJMeterMessagingV1
	}
	if dsl.RequiresLRFidelity(group.script) {
		if base != "" {
			return base + "+" + dsl.FeatureLRFidelityV1
		}
		return dsl.FeatureLRFidelityV1
	}
	return base
}

func groupRequiredDataRuntimeFeatures(group TestGroup) []string {
	features := dsl.RequiredNativeRuntimeFeatures(group.script)
	if javaHostSettings(group) != nil {
		for i, feature := range features {
			if feature == protocol.FeatureNativeDataJavaV1 {
				features[i] = protocol.FeatureNativeDataBrokerV1
			}
		}
	}
	if barriers, _ := dsl.ScriptDistributedRendezvous(group.script); len(barriers) > 0 {
		features = append(features, dsl.FeatureDistributedRendezvousV1)
	}
	return features
}

func groupBaseRuntimeFeature(group TestGroup) string {
	detail := group.script != nil && scriptRequestsSuccessfulMessageDetail(group.script)
	parallel := group.script != nil && scriptRequestsControlParallel(group.script)
	if detail && parallel {
		return generatorrpc.FeatureMessageDetailControlParallelV1
	}
	if detail {
		return generatorrpc.FeatureMessageDetailCaptureV1
	}
	if parallel {
		return generatorrpc.FeatureControlParallelV1
	}
	if javaVuserCapacityPlanForGroup(group).RequiresPersistentLifecycle() {
		return protocol.FeatureJavaVuserPersistentLifecycleV1
	}
	return ""
}

func scriptRequestsSuccessfulMessageDetail(script *dsl.Script) bool {
	if script == nil || script.Config.Results.MessageDetail == nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(script.Config.Results.MessageDetail.Mode))
	return mode == "sampled" || mode == "adaptive"
}

func orderedLoadTargets(loadByTarget map[string]generatorLoadSnapshot, preferred []string, regionState *regionPlacementState) []string {
	ordered := make([]string, 0, len(loadByTarget))
	for target := range loadByTarget {
		ordered = append(ordered, target)
	}
	preferenceIndex := make(map[string]int, len(preferred))
	for index, target := range preferred {
		preferenceIndex[target] = index
	}
	sort.SliceStable(ordered, func(left int, right int) bool {
		leftSnapshot := loadByTarget[ordered[left]]
		rightSnapshot := loadByTarget[ordered[right]]
		if regionState != nil {
			leftQuota, leftWeighted := regionState.preference(ordered[left])
			rightQuota, rightWeighted := regionState.preference(ordered[right])
			if leftWeighted != rightWeighted {
				return leftWeighted
			}
			if leftWeighted && leftQuota != rightQuota {
				return leftQuota > rightQuota
			}
		}
		if leftSnapshot.AvailableCapacity != rightSnapshot.AvailableCapacity {
			return leftSnapshot.AvailableCapacity > rightSnapshot.AvailableCapacity
		}
		if leftSnapshot.Stats.ActiveVUsers != rightSnapshot.Stats.ActiveVUsers {
			return leftSnapshot.Stats.ActiveVUsers < rightSnapshot.Stats.ActiveVUsers
		}
		leftIndex, leftOK := preferenceIndex[ordered[left]]
		rightIndex, rightOK := preferenceIndex[ordered[right]]
		if leftOK && rightOK && leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		if leftOK != rightOK {
			return leftOK
		}
		return ordered[left] < ordered[right]
	})
	return ordered
}

func orderedAssignedTargets(groupTargets map[string]string, preferred []string) []string {
	set := make(map[string]struct{})
	for _, target := range groupTargets {
		set[target] = struct{}{}
	}
	return orderedTargetList(set, preferred)
}

func orderedAssignedShardTargets(groupShards map[string][]GeneratorShardSummary, preferred []string) []string {
	set := make(map[string]struct{})
	for _, shards := range groupShards {
		for _, shard := range shards {
			if shard.Target == "" {
				continue
			}
			set[shard.Target] = struct{}{}
		}
	}
	return orderedTargetList(set, preferred)
}

func orderedGeneratorTargets(clients map[string]generatorClient, preferred []string) []string {
	set := make(map[string]struct{}, len(clients))
	for target := range clients {
		set[target] = struct{}{}
	}
	return orderedTargetList(set, preferred)
}

func orderedMetricsTargets(streams map[string]*generatorrpc.MetricsStream, preferred []string) []string {
	set := make(map[string]struct{}, len(streams))
	for target := range streams {
		set[target] = struct{}{}
	}
	return orderedTargetList(set, preferred)
}

func orderedTargetList(targetSet map[string]struct{}, preferred []string) []string {
	ordered := make([]string, 0, len(targetSet))
	seen := make(map[string]struct{}, len(targetSet))
	for _, target := range preferred {
		if _, ok := targetSet[target]; ok {
			ordered = append(ordered, target)
			seen[target] = struct{}{}
		}
	}
	extras := make([]string, 0)
	for target := range targetSet {
		if _, ok := seen[target]; ok {
			continue
		}
		extras = append(extras, target)
	}
	sort.Strings(extras)
	return append(ordered, extras...)
}

func primaryTargetsFromShards(groupShards map[string][]GeneratorShardSummary) map[string]string {
	if len(groupShards) == 0 {
		return nil
	}
	primary := make(map[string]string, len(groupShards))
	for group, shards := range groupShards {
		for _, shard := range shards {
			if shard.Target == "" {
				continue
			}
			primary[group] = shard.Target
			break
		}
	}
	return primary
}

func cloneGroupShardMap(values map[string][]GeneratorShardSummary) map[string][]GeneratorShardSummary {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string][]GeneratorShardSummary, len(values))
	for key, shards := range values {
		cloned[key] = cloneGeneratorShardSummaries(shards)
	}
	return cloned
}

func flattenGroupShards(values map[string][]GeneratorShardSummary) []GeneratorShardSummary {
	if len(values) == 0 {
		return nil
	}
	groups := make([]string, 0, len(values))
	for group := range values {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	out := make([]GeneratorShardSummary, 0)
	for _, group := range groups {
		shards := cloneGeneratorShardSummaries(values[group])
		sort.SliceStable(shards, func(left int, right int) bool {
			return shards[left].Target < shards[right].Target
		})
		out = append(out, shards...)
	}
	return out
}

func shardTargets(shards []GeneratorShardSummary) []string {
	targets := make([]string, 0, len(shards))
	seen := make(map[string]struct{}, len(shards))
	for _, shard := range shards {
		if shard.Target == "" {
			continue
		}
		if _, ok := seen[shard.Target]; ok {
			continue
		}
		seen[shard.Target] = struct{}{}
		targets = append(targets, shard.Target)
	}
	return targets
}

func shardVUsersForTarget(shards []GeneratorShardSummary, target string) int {
	total := 0
	for _, shard := range shards {
		if shard.Target == target {
			total = saturatingAddNonNegative(total, shard.VUsers)
		}
	}
	return total
}

func shardStrategyForGroupShards(shards []GeneratorShardSummary) string {
	for _, shard := range shards {
		if shard.ShardStrategy != "" {
			return shard.ShardStrategy
		}
	}
	return ""
}
