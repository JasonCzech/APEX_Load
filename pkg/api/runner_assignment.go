package api

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// resolvePoolAssignmentLocked expands a pool-scoped assignment into the pool's
// member targets (as specific_hosts). The PoolID is retained so placement can
// enforce it leniently (skipping members not live on this controller).
func (tr *TestRunner) resolvePoolAssignmentLocked(assignment *GeneratorAssignmentSettings) (*GeneratorAssignmentSettings, error) {
	if assignment == nil || strings.TrimSpace(assignment.PoolID) == "" {
		return assignment, nil
	}
	poolID := strings.TrimSpace(assignment.PoolID)
	if tr.fleetStore == nil {
		return nil, fmt.Errorf("fleet pool assignment requires storage")
	}
	pool, err := tr.fleetStore.GetFleetPool(poolID)
	if err != nil {
		return nil, fmt.Errorf("fleet pool %q not found", poolID)
	}
	targets := tr.poolMemberTargets(poolID)
	if len(targets) == 0 {
		if pool != nil && strings.TrimSpace(pool.ExecutionTargetID) != "" {
			if targetsStore, ok := tr.fleetStore.(interface {
				GetExecutionTarget(string) (*storage.ExecutionTarget, error)
			}); ok {
				target, targetErr := targetsStore.GetExecutionTarget(pool.ExecutionTargetID)
				if targetErr == nil && target != nil && target.Kind != storage.ExecutionTargetKubernetes && target.Kind != storage.ExecutionTargetStatic {
					out := *assignment
					out.Mode = generatorAssignmentModeAuto
					out.SelectedTargets = nil
					out.PoolID = poolID
					return &out, nil
				}
			}
		}
		return nil, fmt.Errorf("fleet pool %q has no generators", poolID)
	}
	out := *assignment
	out.Mode = generatorAssignmentModeSpecificHosts
	out.SelectedTargets = targets
	out.PoolID = poolID
	return &out, nil
}

// poolMemberTargets returns the deduplicated member targets of a fleet pool,
// using the shared runtime-membership definition in fleet_pool_membership.go.
func (tr *TestRunner) poolMemberTargets(poolID string) []string {
	if tr.fleetStore == nil {
		return nil
	}
	pool := &storage.FleetPool{ID: poolID}
	if members, err := tr.fleetStore.ListFleetPoolMembers(poolID); err == nil {
		pool.Members = members
	}
	// A metadata read failure degrades to members-only rather than failing
	// placement outright, matching the previous best-effort behavior.
	metadata, _ := tr.fleetStore.ListGeneratorMetadata(0, 0)
	targets := fleetPoolTargets(pool, metadata)
	if targets == nil {
		return []string{}
	}
	return targets
}

func normalizeGeneratorAssignmentSettings(in *GeneratorAssignmentSettings) (*GeneratorAssignmentSettings, error) {
	if in != nil && in.JavaHost != nil {
		return nil, fmt.Errorf("assignment.java_host is group-scoped")
	}
	if in != nil && strings.TrimSpace(in.Mode) == generatorAssignmentModeInherit {
		return nil, fmt.Errorf("assignment.mode inherit is group-scoped")
	}
	return normalizeGeneratorAssignmentSettingsBase(in)
}

func normalizeGeneratorAssignmentSettingsBase(in *GeneratorAssignmentSettings) (*GeneratorAssignmentSettings, error) {
	if in == nil {
		return nil, nil
	}
	out := &GeneratorAssignmentSettings{
		JavaHost:              cloneJavaHostAssignmentSettings(in.JavaHost),
		SourceAffinity:        strings.TrimSpace(in.SourceAffinity),
		SharingMode:           strings.TrimSpace(in.SharingMode),
		Mode:                  strings.TrimSpace(in.Mode),
		PoolID:                strings.TrimSpace(in.PoolID),
		RequiredLabels:        normalizeRequiredLabels(in.RequiredLabels),
		TolerateGeneratorLoss: in.TolerateGeneratorLoss,
		RecoverySpareVUsers:   in.RecoverySpareVUsers,
		FailoverPolicy:        strings.ToLower(strings.TrimSpace(in.FailoverPolicy)),
	}
	if out.RecoverySpareVUsers < 0 || out.RecoverySpareVUsers > maxManagedTestVUsers {
		return nil, fmt.Errorf("assignment.recovery_spare_vusers must be between 0 and %d", maxManagedTestVUsers)
	}
	if out.RecoverySpareVUsers > 0 && (!out.TolerateGeneratorLoss || !simpleRecoveryAssignment(out)) {
		return nil, fmt.Errorf("reserved recovery capacity requires generator-loss tolerance and automatic or explicit-host placement")
	}
	if err := validateJavaHostAssignmentSettings(out.JavaHost); err != nil {
		return nil, err
	}
	switch out.FailoverPolicy {
	case "", failoverPolicyFail, failoverPolicyContinue:
	default:
		return nil, fmt.Errorf("assignment.failover_policy must be fail or continue")
	}
	if out.SourceAffinity != "" && out.SourceAffinity != "single_generator" {
		return nil, fmt.Errorf("assignment.source_affinity must be single_generator")
	}
	if out.SourceAffinity != "" && out.TolerateGeneratorLoss {
		return nil, fmt.Errorf("source-engine affinity forbids generator-loss recovery")
	}
	if out.Mode == "" {
		out.Mode = generatorAssignmentModeAuto
	}
	if out.SharingMode != "" && out.SharingMode != "shared" && out.SharingMode != "dedicated" {
		return nil, fmt.Errorf("assignment.sharing_mode must be shared or dedicated")
	}
	switch out.Mode {
	case generatorAssignmentModeInherit:
		// A Java host profile selects execution mechanics without replacing the
		// scenario's generator placement. Inherit is only valid for this group
		// overlay and cannot carry another placement directive of its own.
		if out.JavaHost == nil || out.SourceAffinity != "" || out.SharingMode != "" || out.PoolID != "" ||
			len(in.SelectedTargets) != 0 || len(in.RequiredLabels) != 0 || len(in.RegionWeights) != 0 ||
			len(in.LoadZoneWeights) != 0 || in.ShardStrategy != "" || out.TolerateGeneratorLoss ||
			out.RecoverySpareVUsers != 0 || out.FailoverPolicy != "" {
			return nil, fmt.Errorf("inherit group assignment may only select a Java host profile")
		}
		return out, nil
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

func (tr *TestRunner) validateGeneratorLossTolerance(assignment *GeneratorAssignmentSettings) error {
	if assignment != nil && assignment.SourceAffinity != "" {
		return fmt.Errorf("source-engine affinity requires JMeter messaging runtime compatibility, which is not yet available")
	}
	if assignmentToleratesGeneratorLoss(assignment) && (tr == nil || !tr.durableExecutionFence) {
		return fmt.Errorf("assignment.tolerate_generator_loss requires HA execution leadership backed by the same durable test store")
	}
	return nil
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

func normalizeRequiredLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
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
	if in.RecoverySpareVUsers != 0 {
		return nil, fmt.Errorf("assignment.recovery_spare_vusers is test-scoped")
	}
	if strings.TrimSpace(in.SharingMode) != "" {
		return nil, fmt.Errorf("assignment.sharing_mode is test-scoped; set it on the test-level assignment")
	}
	if strings.TrimSpace(in.PoolID) != "" {
		// Pool membership is expanded to concrete targets only for the test-level
		// assignment (resolvePoolAssignmentLocked). A group-scoped pool_id is never
		// resolved, so the group would silently fall back to auto and run
		// fleet-wide — reject it rather than ignore an explicit isolation directive.
		return nil, fmt.Errorf("assignment.pool_id is not supported at group scope; set the pool on the test-level assignment")
	}
	if strings.TrimSpace(in.FailoverPolicy) != "" {
		return nil, fmt.Errorf("assignment.failover_policy is test-scoped; set it on the test-level assignment")
	}
	out, err := normalizeGeneratorAssignmentSettingsBase(in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	switch out.Mode {
	case generatorAssignmentModeInherit:
		return out, nil
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
		return nil, fmt.Errorf("group assignment.mode must be inherit, auto, or specific_hosts")
	}
}

func cloneGeneratorAssignmentSettings(in *GeneratorAssignmentSettings) *GeneratorAssignmentSettings {
	if in == nil {
		return nil
	}
	out := *in
	out.JavaHost = cloneJavaHostAssignmentSettings(in.JavaHost)
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
	if len(in.RequiredLabels) > 0 {
		out.RequiredLabels = make(map[string]string, len(in.RequiredLabels))
		for key, value := range in.RequiredLabels {
			out.RequiredLabels[key] = value
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
	for i := range out {
		out[i].JavaHost = cloneJavaHostPlacementSummary(in[i].JavaHost)
		out[i].RuntimeResourceClaim = generatorrpc.CloneRuntimeResourceClaim(in[i].RuntimeResourceClaim)
		out[i].RuntimeResourceAdmission = generatorrpc.CloneRuntimeResourceAdmission(in[i].RuntimeResourceAdmission)
	}
	return out
}
