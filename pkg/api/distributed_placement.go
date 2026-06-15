package api

// distributed_placement.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"math"
	"sort"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
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
	totalVUsers := 0
	for _, group := range groups {
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
	if len(loadByTarget) == 0 {
		return nil
	}
	projected := make(map[string]generatorLoadSnapshot, len(loadByTarget))
	for key, value := range loadByTarget {
		projected[key] = value
	}
	regionState := newRegionPlacementState(groups, assignment, targetSpecs)
	compatibility := newGeneratorCompatibilityIndex(targetSpecs, loadByTarget)
	shardsByGroup := make(map[string][]GeneratorShardSummary, len(groups))
	for _, group := range groups {
		shards := chooseGeneratorShardsForGroup(group, projected, preferred, regionState, compatibility)
		shardsByGroup[group.Name] = shards
		for _, shard := range shards {
			if shard.Target == "" || shard.VUsers <= 0 {
				continue
			}
			snapshot := projected[shard.Target]
			snapshot.AvailableCapacity -= int32(shard.VUsers)
			snapshot.Stats.ActiveVUsers += int32(shard.VUsers)
			projected[shard.Target] = snapshot
			regionState.consume(shard.Target, shard.VUsers)
		}
	}
	return shardsByGroup
}

func chooseGeneratorShardsForGroup(group TestGroup, loadByTarget map[string]generatorLoadSnapshot, preferred []string, regionState *regionPlacementState, compatibility *generatorCompatibilityIndex) []GeneratorShardSummary {
	if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeSpecificHosts {
		targets := make([]string, 0, len(group.Assignment.SelectedTargets))
		for _, target := range group.Assignment.SelectedTargets {
			if _, ok := loadByTarget[target]; !ok {
				return nil
			}
			if compatibility != nil && !compatibility.targetCanRun(target, group.Protocol) {
				return nil
			}
			targets = append(targets, target)
		}
		return capacityWeightedShards(group.Name, group.VUsers, targets, loadByTarget, group.Assignment.ShardStrategy)
	}

	target := chooseGeneratorTargetForGroup(group, loadByTarget, preferred, regionState, compatibility)
	if target == "" {
		return nil
	}
	return []GeneratorShardSummary{{Group: group.Name, Target: target, VUsers: group.VUsers}}
}

func capacityWeightedShards(groupName string, vusers int, targets []string, loadByTarget map[string]generatorLoadSnapshot, strategy string) []GeneratorShardSummary {
	return buildCapacityWeightedShards(groupName, vusers, targets, loadByTarget, strategy, false)
}

func capacityWeightedShardsIncludingZero(groupName string, vusers int, targets []string, loadByTarget map[string]generatorLoadSnapshot, strategy string) []GeneratorShardSummary {
	return buildCapacityWeightedShards(groupName, vusers, targets, loadByTarget, strategy, true)
}

func buildCapacityWeightedShards(groupName string, vusers int, targets []string, loadByTarget map[string]generatorLoadSnapshot, strategy string, includeZero bool) []GeneratorShardSummary {
	if len(targets) == 0 || vusers <= 0 {
		if !includeZero {
			return nil
		}
		shards := make([]GeneratorShardSummary, 0, len(targets))
		for _, target := range targets {
			shards = append(shards, GeneratorShardSummary{
				Group:         groupName,
				Target:        target,
				VUsers:        0,
				ShardStrategy: firstNonEmpty(strategy, generatorShardStrategyCapacityWeighted),
			})
		}
		return shards
	}
	if len(targets) == 1 {
		return []GeneratorShardSummary{{Group: groupName, Target: targets[0], VUsers: vusers}}
	}
	if strategy == "" {
		strategy = generatorShardStrategyCapacityWeighted
	}
	allocations := allocateCapacityWeightedVUsers(vusers, targets, loadByTarget)
	shards := make([]GeneratorShardSummary, 0, len(targets))
	for _, target := range targets {
		if allocations[target] <= 0 && !includeZero {
			continue
		}
		shards = append(shards, GeneratorShardSummary{
			Group:         groupName,
			Target:        target,
			VUsers:        allocations[target],
			ShardStrategy: strategy,
		})
	}
	return shards
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

func chooseGeneratorTargetForGroup(group TestGroup, loadByTarget map[string]generatorLoadSnapshot, preferred []string, regionState *regionPlacementState, compatibility *generatorCompatibilityIndex) string {
	ordered := orderedLoadTargets(loadByTarget, preferred, regionState)
	if len(ordered) == 0 {
		return ""
	}
	for _, target := range ordered {
		if compatibility != nil && !compatibility.targetCanRun(target, group.Protocol) {
			continue
		}
		if loadByTarget[target].AvailableCapacity >= int32(group.VUsers) {
			return target
		}
	}
	for _, target := range ordered {
		if compatibility == nil || compatibility.targetCanRun(target, group.Protocol) {
			return target
		}
	}
	return ""
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
			if shard.Target == "" || shard.VUsers <= 0 {
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
			if shard.Target == "" || shard.VUsers <= 0 {
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
			total += shard.VUsers
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
