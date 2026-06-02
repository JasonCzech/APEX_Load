package api

import (
	"fmt"
	"strings"
)

func normalizeGeneratorAssignmentSettings(in *GeneratorAssignmentSettings) (*GeneratorAssignmentSettings, error) {
	if in == nil {
		return nil, nil
	}
	out := &GeneratorAssignmentSettings{Mode: strings.TrimSpace(in.Mode)}
	if out.Mode == "" {
		out.Mode = generatorAssignmentModeAuto
	}
	switch out.Mode {
	case generatorAssignmentModeAuto:
		out.SelectedTargets = nil
		out.RegionWeights = nil
		out.LoadZoneWeights = nil
		return out, nil
	case generatorAssignmentModeSpecificHosts:
		seen := make(map[string]struct{})
		for _, raw := range in.SelectedTargets {
			target := strings.TrimSpace(raw)
			if target == "" {
				continue
			}
			if _, exists := seen[target]; exists {
				continue
			}
			seen[target] = struct{}{}
			out.SelectedTargets = append(out.SelectedTargets, target)
		}
		if len(out.SelectedTargets) == 0 {
			return nil, fmt.Errorf("assignment.selected_targets must include at least one hostname when mode is specific_hosts")
		}
		out.RegionWeights = nil
		out.LoadZoneWeights = nil
		return out, nil
	case generatorAssignmentModeRegionWeighted:
		out.RegionWeights = normalizePositiveWeights(in.RegionWeights, "assignment.region_weights")
		if err := validatePositiveWeights(out.RegionWeights, "assignment.region_weights"); err != nil {
			return nil, err
		}
		if len(out.RegionWeights) == 0 {
			return nil, fmt.Errorf("assignment.region_weights must include at least one region when mode is region_weighted")
		}
		out.SelectedTargets = nil
		out.LoadZoneWeights = nil
		return out, nil
	case generatorAssignmentModeLoadZoneWeighted:
		out.LoadZoneWeights = normalizePositiveWeights(in.LoadZoneWeights, "assignment.load_zone_weights")
		if err := validatePositiveWeights(out.LoadZoneWeights, "assignment.load_zone_weights"); err != nil {
			return nil, err
		}
		if len(out.LoadZoneWeights) == 0 {
			return nil, fmt.Errorf("assignment.load_zone_weights must include at least one load zone when mode is load_zone_weighted")
		}
		out.SelectedTargets = nil
		out.RegionWeights = nil
		return out, nil
	default:
		return nil, fmt.Errorf("assignment.mode must be auto, specific_hosts, region_weighted, or load_zone_weighted")
	}
}

func normalizePositiveWeights(in map[string]int, _ string) map[string]int {
	out := make(map[string]int)
	seen := make(map[string]struct{})
	for key, weight := range in {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" {
			continue
		}
		if _, exists := seen[normalizedKey]; exists {
			continue
		}
		seen[normalizedKey] = struct{}{}
		if weight <= 0 {
			out[normalizedKey] = weight
			continue
		}
		out[normalizedKey] = weight
	}
	return out
}

func validatePositiveWeights(weights map[string]int, field string) error {
	for key, weight := range weights {
		if weight <= 0 {
			return fmt.Errorf("%s[%q] must be greater than zero", field, key)
		}
	}
	return nil
}

func effectiveRegionWeights(assignment *GeneratorAssignmentSettings) map[string]int {
	if assignment == nil {
		return nil
	}
	switch assignment.Mode {
	case generatorAssignmentModeRegionWeighted:
		return assignment.RegionWeights
	case generatorAssignmentModeLoadZoneWeighted:
		return assignment.LoadZoneWeights
	default:
		return nil
	}
}

func normalizeGroupGeneratorAssignmentSettings(in *GeneratorAssignmentSettings) (*GeneratorAssignmentSettings, error) {
	if in == nil {
		return nil, nil
	}
	out, err := normalizeGeneratorAssignmentSettings(in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	switch out.Mode {
	case generatorAssignmentModeAuto:
		return out, nil
	case generatorAssignmentModeSpecificHosts:
		strategy := strings.TrimSpace(in.ShardStrategy)
		if len(out.SelectedTargets) > 1 && strategy == "" {
			strategy = generatorShardStrategyCapacityWeighted
		}
		if strategy != "" && strategy != generatorShardStrategyCapacityWeighted {
			return nil, fmt.Errorf("group assignment.shard_strategy must be capacity_weighted")
		}
		if len(out.SelectedTargets) <= 1 {
			strategy = ""
		}
		out.ShardStrategy = strategy
		return out, nil
	default:
		return nil, fmt.Errorf("group assignment.mode must be auto or specific_hosts")
	}
}

func cloneGeneratorAssignmentSettings(in *GeneratorAssignmentSettings) *GeneratorAssignmentSettings {
	if in == nil {
		return nil
	}
	out := *in
	out.SelectedTargets = append([]string(nil), in.SelectedTargets...)
	if len(in.RegionWeights) > 0 {
		out.RegionWeights = make(map[string]int, len(in.RegionWeights))
		for region, weight := range in.RegionWeights {
			out.RegionWeights[region] = weight
		}
	}
	if len(in.LoadZoneWeights) > 0 {
		out.LoadZoneWeights = make(map[string]int, len(in.LoadZoneWeights))
		for zone, weight := range in.LoadZoneWeights {
			out.LoadZoneWeights[zone] = weight
		}
	}
	return &out
}

func cloneGeneratorShardSummaries(in []GeneratorShardSummary) []GeneratorShardSummary {
	if len(in) == 0 {
		return nil
	}
	out := make([]GeneratorShardSummary, len(in))
	copy(out, in)
	return out
}
