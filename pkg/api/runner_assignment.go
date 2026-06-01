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
		return out, nil
	case generatorAssignmentModeRegionWeighted:
		seen := make(map[string]struct{})
		for region, weight := range in.RegionWeights {
			normalizedRegion := strings.TrimSpace(region)
			if normalizedRegion == "" {
				continue
			}
			if _, exists := seen[normalizedRegion]; exists {
				continue
			}
			seen[normalizedRegion] = struct{}{}
			if weight <= 0 {
				return nil, fmt.Errorf("assignment.region_weights[%q] must be greater than zero", normalizedRegion)
			}
			if out.RegionWeights == nil {
				out.RegionWeights = make(map[string]int, len(in.RegionWeights))
			}
			out.RegionWeights[normalizedRegion] = weight
		}
		if len(out.RegionWeights) == 0 {
			return nil, fmt.Errorf("assignment.region_weights must include at least one region when mode is region_weighted")
		}
		out.SelectedTargets = nil
		return out, nil
	default:
		return nil, fmt.Errorf("assignment.mode must be auto, specific_hosts, or region_weighted")
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
