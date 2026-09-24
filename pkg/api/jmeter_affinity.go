package api

// Placement is all-or-nothing: every group must fit on the same generator after
// accounting for the groups already placed there. This never enables the still
// gated distributed source-session execution contract.
func planSourceAffineGroups(groups []TestGroup, candidates map[string]generatorLoadSnapshot, preferred []string, assignment *GeneratorAssignmentSettings, specs []generatorTargetSpec) map[string][]GeneratorShardSummary {
	var population int64
	for _, group := range groups {
		if group.VUsers < 0 {
			return nil
		}
		population += int64(group.VUsers)
	}
	for _, target := range orderedLoadTargets(candidates, preferred, nil) {
		if population > int64(candidates[target].AvailableCapacity) {
			continue
		}
		if assignment.Mode == generatorAssignmentModeSpecificHosts {
			selected := false
			for _, allowed := range assignment.SelectedTargets {
				selected = selected || allowed == target
			}
			if !selected {
				continue
			}
		}
		copies := append([]TestGroup(nil), groups...)
		eligible := true
		for i := range copies {
			original := copies[i].Assignment
			if original != nil && original.Mode == generatorAssignmentModeSpecificHosts {
				selected := false
				for _, allowed := range original.SelectedTargets {
					selected = selected || allowed == target
				}
				if !selected {
					eligible = false
					break
				}
			}
			override := *assignment
			if original != nil {
				override = *original
			}
			override.SourceAffinity = ""
			override.Mode = generatorAssignmentModeSpecificHosts
			override.SelectedTargets = []string{target}
			copies[i].Assignment = &override
		}
		if !eligible {
			continue
		}
		inherited := *assignment
		inherited.SourceAffinity = ""
		one := map[string]generatorLoadSnapshot{target: candidates[target]}
		planned := planDistributedGroupShardsForCandidates(copies, one, one, []string{target}, []string{target}, &inherited, specs)
		for _, group := range groups {
			shards := planned[group.Name]
			if len(shards) != 1 || shards[0].Target != target || shards[0].VUsers != group.VUsers {
				eligible = false
				break
			}
		}
		if eligible {
			return planned
		}
	}
	return nil
}
