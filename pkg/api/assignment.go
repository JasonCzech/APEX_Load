package api

// Generator assignment and capacity-advisory planning: resolve assignment
// target specs, match/weight regions, adjust + estimate capacity advisories for
// an assignment, build assignment recommendations and draft-capacity estimates,
// and hydrate capacity groups. Complements runner_assignment.go (settings
// normalization) and capacity.go (quota/fair-share).

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func buildScenarioFootprint(test *ManagedTest) *ScenarioFootprint {
	if test == nil || test.CapacityAdvisory == nil {
		return nil
	}
	return &ScenarioFootprint{
		TotalVUsers:      test.TotalVUsers,
		Duration:         test.DurationStr,
		ComplexityScore:  test.CapacityAdvisory.ComplexityScore,
		EstimatedCPU:     test.CapacityAdvisory.EstimatedTotal.CPUCores,
		EstimatedMemory:  test.CapacityAdvisory.EstimatedTotal.MemoryMB,
		EstimatedNetwork: test.CapacityAdvisory.EstimatedTotal.NetworkMbps,
	}
}

func (tr *TestRunner) assignmentTargets() []string {
	provider, ok := tr.execution.(interface{ configuredTargets() []string })
	if !ok {
		return nil
	}
	return provider.configuredTargets()
}

func (tr *TestRunner) assignmentTargetSpecs() []generatorTargetSpec {
	provider, ok := tr.execution.(interface{ configuredTargetSpecs() []generatorTargetSpec })
	if !ok {
		return nil
	}
	return tr.enrichGeneratorTargetSpecsWithLabels(provider.configuredTargetSpecs())
}

func assignmentAvailableRegions(specs []generatorTargetSpec) []string {
	set := make(map[string]struct{})
	for _, spec := range specs {
		if spec.Region == "" {
			continue
		}
		set[spec.Region] = struct{}{}
	}
	regions := make([]string, 0, len(set))
	for region := range set {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return regions
}

func assignmentAvailableLabels(specs []generatorTargetSpec) map[string][]string {
	values := make(map[string]map[string]struct{})
	for _, spec := range specs {
		for key, value := range spec.Labels {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key == "" || value == "" {
				continue
			}
			if values[key] == nil {
				values[key] = map[string]struct{}{}
			}
			values[key][value] = struct{}{}
		}
	}
	if len(values) == 0 {
		return nil
	}
	out := make(map[string][]string, len(values))
	for key, set := range values {
		for value := range set {
			out[key] = append(out[key], value)
		}
		sort.Strings(out[key])
	}
	return out
}

func matchingRegionWeightedTargets(specs []generatorTargetSpec, weights map[string]int) int {
	if len(specs) == 0 || len(weights) == 0 {
		return 0
	}
	count := 0
	for _, spec := range specs {
		if spec.Region == "" {
			continue
		}
		if weights[spec.Region] > 0 {
			count++
		}
	}
	return count
}

func missingWeightedRegions(specs []generatorTargetSpec, weights map[string]int) []string {
	if len(weights) == 0 {
		return nil
	}
	available := make(map[string]struct{})
	for _, spec := range specs {
		if spec.Region == "" {
			continue
		}
		available[spec.Region] = struct{}{}
	}
	missing := make([]string, 0)
	for region := range weights {
		if _, ok := available[region]; ok {
			continue
		}
		missing = append(missing, region)
	}
	sort.Strings(missing)
	return missing
}

func formatRegionWeightSummary(weights map[string]int) string {
	if len(weights) == 0 {
		return ""
	}
	total := 0
	regions := make([]string, 0, len(weights))
	for region, weight := range weights {
		if weight <= 0 {
			continue
		}
		total += weight
		regions = append(regions, region)
	}
	if total <= 0 || len(regions) == 0 {
		return ""
	}
	sort.Strings(regions)
	parts := make([]string, 0, len(regions))
	for _, region := range regions {
		parts = append(parts, fmt.Sprintf("%s %d%%", region, int(math.Round(float64(weights[region])*100/float64(total)))))
	}
	return strings.Join(parts, ", ")
}

func assignmentWeightLabel(assignment *GeneratorAssignmentSettings) string {
	if assignment != nil && assignment.Mode == generatorAssignmentModeLoadZoneWeighted {
		return "load zone"
	}
	return "region"
}

func titleAssignmentWeightLabel(assignment *GeneratorAssignmentSettings) string {
	label := assignmentWeightLabel(assignment)
	if label == "load zone" {
		return "Load-zone"
	}
	return "Region"
}

func adjustCapacityAdvisoryForAssignment(advisory *CapacityAdvisory, assignment *GeneratorAssignmentSettings, targetSpecs []generatorTargetSpec) *CapacityAdvisory {
	if advisory == nil {
		return nil
	}
	assignment, err := normalizeGeneratorAssignmentSettings(assignment)
	if err != nil {
		cloned := cloneCapacityAdvisory(advisory)
		cloned.RequiresOverride = true
		cloned.Warnings = append(cloned.Warnings, err.Error())
		return cloned
	}
	if assignment == nil {
		return advisory
	}
	availableTargets := len(targetSpecs)
	selected := 0
	warning := ""
	switch assignment.Mode {
	case generatorAssignmentModeSpecificHosts:
		selected = len(assignment.SelectedTargets)
		warning = fmt.Sprintf("Selected hosts provide %d generator slot%s, but the scenario footprint recommends %%d.", selected, pluralSuffix(selected))
	case generatorAssignmentModeRegionWeighted, generatorAssignmentModeLoadZoneWeighted:
		selected = matchingRegionWeightedTargets(targetSpecs, effectiveRegionWeights(assignment))
		warning = fmt.Sprintf("Configured %s weights match %d generator slot%s, but the scenario footprint recommends %%d.", assignmentWeightLabel(assignment), selected, pluralSuffix(selected))
	default:
		return advisory
	}
	if selected == 0 {
		cloned := cloneCapacityAdvisory(advisory)
		cloned.RequiresOverride = true
		if assignment.Mode == generatorAssignmentModeRegionWeighted || assignment.Mode == generatorAssignmentModeLoadZoneWeighted {
			cloned.Warnings = append(cloned.Warnings, "No configured generators match the requested "+assignmentWeightLabel(assignment)+" weights on this controller.")
		}
		return cloned
	}
	cloned := cloneCapacityAdvisory(advisory)
	if availableTargets > 0 && selected > availableTargets {
		selected = availableTargets
	}
	cloned.AvailableGenerators = selected
	cloned.ConfiguredAvailableGenerators = selected
	cloned.LiveAvailableGenerators = nil
	cloned.ShortfallGenerators = 0
	if cloned.RecommendedGenerators > selected {
		cloned.ShortfallGenerators = cloned.RecommendedGenerators - selected
		cloned.RequiresOverride = true
		cloned.Warnings = append(cloned.Warnings, fmt.Sprintf(warning, cloned.RecommendedGenerators))
		if assignment.Mode == generatorAssignmentModeRegionWeighted || assignment.Mode == generatorAssignmentModeLoadZoneWeighted {
			if missing := missingWeightedRegions(targetSpecs, effectiveRegionWeights(assignment)); len(missing) > 0 {
				cloned.Warnings = append(cloned.Warnings, fmt.Sprintf("No connected generator targets are tagged for %s%s %s.", assignmentWeightLabel(assignment), pluralSuffix(len(missing)), strings.Join(missing, ", ")))
			}
		}
		return cloned
	}
	cloned.RequiresOverride = false
	if assignment.Mode == generatorAssignmentModeRegionWeighted || assignment.Mode == generatorAssignmentModeLoadZoneWeighted {
		if missing := missingWeightedRegions(targetSpecs, effectiveRegionWeights(assignment)); len(missing) > 0 {
			cloned.Warnings = append(cloned.Warnings, fmt.Sprintf("No connected generator targets are tagged for %s%s %s.", assignmentWeightLabel(assignment), pluralSuffix(len(missing)), strings.Join(missing, ", ")))
		}
	}
	return cloned
}

func (tr *TestRunner) estimateCapacityAdvisoryForAssignment(projectID string, groups []TestGroup, totalVUsers int, duration time.Duration, resourceBaseline bool, runtimeSettings *RuntimeSettingsOverride, assignment *GeneratorAssignmentSettings) *CapacityAdvisory {
	// Memoized: keyed by the settings generation + these params, with a short TTL
	// for reservation-window drift. Live-inventory availability bypasses the cache
	// so a scaled-down fleet is visible immediately.
	key := tr.capacityAdvisoryCacheKey(projectID, groups, totalVUsers, duration, resourceBaseline, runtimeSettings, assignment)
	if cached, ok := tr.lookupCapacityAdvisory(key); ok {
		return cached
	}
	availability := tr.resolveCapacityAvailabilityForProjectAt(projectID, time.Now())
	advisory := estimateCapacityAdvisoryWithAvailability(tr.settings, availability, groups, totalVUsers, duration, resourceBaseline, runtimeSettings)
	advisory = adjustCapacityAdvisoryForAssignment(advisory, assignment, tr.assignmentTargetSpecs())
	tr.storeCapacityAdvisory(key, advisory)
	return advisory
}

func (tr *TestRunner) buildAssignmentRecommendation(test *ManagedTest) *AssignmentRecommendation {
	if test == nil || test.CapacityAdvisory == nil {
		return nil
	}
	availableTargets := tr.assignmentTargets()
	targetSpecs := tr.assignmentTargetSpecs()
	availableRegions := assignmentAvailableRegions(targetSpecs)
	assignment := cloneGeneratorAssignmentSettings(test.Assignment)
	if assignment == nil {
		assignment = &GeneratorAssignmentSettings{Mode: generatorAssignmentModeAuto}
	}
	recommendedCount := test.CapacityAdvisory.RecommendedGenerators
	if recommendedCount < 1 {
		recommendedCount = 1
	}
	recommendedTargets := append([]string(nil), availableTargets...)
	if len(recommendedTargets) > recommendedCount {
		recommendedTargets = recommendedTargets[:recommendedCount]
	}
	recommendation := &AssignmentRecommendation{
		Mode:               assignment.Mode,
		AvailableTargets:   availableTargets,
		AvailableRegions:   availableRegions,
		AvailableLabels:    assignmentAvailableLabels(targetSpecs),
		RecommendedTargets: recommendedTargets,
	}
	if assignment.Mode == generatorAssignmentModeSpecificHosts {
		recommendation.Summary = fmt.Sprintf("Manual host selection uses %d configured host%s for this scenario footprint.", len(assignment.SelectedTargets), pluralSuffix(len(assignment.SelectedTargets)))
		if test.CapacityAdvisory.RequiresOverride {
			recommendation.Warnings = append(recommendation.Warnings, test.CapacityAdvisory.Warnings...)
		}
		return recommendation
	}
	if assignment.Mode == generatorAssignmentModeRegionWeighted || assignment.Mode == generatorAssignmentModeLoadZoneWeighted {
		recommendation.Summary = fmt.Sprintf("%s-weighted assignment will place groups across tagged generators using %s.", titleAssignmentWeightLabel(assignment), formatRegionWeightSummary(effectiveRegionWeights(assignment)))
		if test.CapacityAdvisory.RequiresOverride {
			recommendation.Warnings = append(recommendation.Warnings, test.CapacityAdvisory.Warnings...)
		}
		return recommendation
	}
	recommendation.Summary = fmt.Sprintf("Auto-assignment recommends %d host%s for the current scenario footprint.", test.CapacityAdvisory.RecommendedGenerators, pluralSuffix(test.CapacityAdvisory.RecommendedGenerators))
	return recommendation
}

func (tr *TestRunner) hydrateCapacityGroupsLocked(groups []TestGroup) []TestGroup {
	if len(groups) == 0 {
		return nil
	}
	hydrated := make([]TestGroup, len(groups))
	for i := range groups {
		hydrated[i] = groups[i]
		if hydrated[i].script != nil {
			continue
		}
		if script, ok := tr.scripts[hydrated[i].ScriptID]; ok {
			hydrated[i].script = script.Script
			if hydrated[i].Protocol == "" {
				hydrated[i].Protocol = script.Protocol
			}
			if hydrated[i].ScriptName == "" {
				hydrated[i].ScriptName = script.Name
			}
		}
	}
	return hydrated
}
