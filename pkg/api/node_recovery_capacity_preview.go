package api

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// This advisory intentionally makes no admission or availability promise. A
// subsequent reservation must revalidate pod identities, idle state, quotas and
// controller ownership atomically with the whole placement.
func (tr *TestRunner) nodeRecoveryCapacityWarningLocked(test *ManagedTest, placements []PodCapacityPreview) *LaunchConflict {
	if test == nil || !assignmentToleratesGeneratorLoss(test.Assignment) {
		return nil
	}
	warning := func(reason string) *LaunchConflict {
		return &LaunchConflict{Code: "node_recovery_capacity_unreserved", Severity: "warning", Source: "controller", Message: reason + " Recovery capacity is not reserved; worker-loss continuity is not guaranteed."}
	}
	simpleAssignment := func(a *GeneratorAssignmentSettings) bool {
		return a == nil || (a.SourceAffinity == "" && (a.Mode == "" || a.Mode == generatorAssignmentModeAuto || a.Mode == generatorAssignmentModeSpecificHosts) && len(a.RegionWeights) == 0 && len(a.LoadZoneWeights) == 0)
	}
	if !simpleAssignment(test.Assignment) {
		return warning("The worker-loss capacity estimate does not cover weighted regional or source-affinity placement.")
	}
	for _, group := range test.Groups {
		if strings.ToLower(group.Protocol) != "http" || !simpleAssignment(group.Assignment) {
			return warning("The worker-loss capacity estimate currently covers HTTP with automatic or explicit-host placement.")
		}
	}
	if !tr.settings.CapacityPlanning.PodReservationsEnabled || len(placements) == 0 {
		return warning("A complete peak pod placement is required to assess worker-loss capacity.")
	}
	b, ok := tr.execution.(*distributedExecutionBackend)
	if !ok {
		return warning("Distributed generator inventory is unavailable.")
	}
	entries, at := tr.cachedGeneratorInventorySnapshot()
	if at.IsZero() || at.After(time.Now()) || time.Since(at) > defaultGeneratorSnapshotTTL {
		return warning("Generator placement observations are stale.")
	}
	if len(entries) > maxRecoveryCapacityPlanPods || len(placements) > maxRecoveryCapacityPlanPods {
		return warning("The observed fleet exceeds the bounded worker-loss capacity preview.")
	}
	allowed, err := b.allowedTargetsForTest(test)
	if err != nil {
		return warning("Eligible recovery targets are unavailable.")
	}
	allowedSet := map[string]bool{}
	for _, target := range allowed {
		allowedSet[target] = true
	}
	index := map[string]GeneratorInventoryEntry{}
	loads := map[string]generatorLoadSnapshot{}
	for _, e := range entries {
		if _, duplicate := index[e.Target]; duplicate {
			return warning("Generator placement observations are ambiguous.")
		}
		index[e.Target] = e
		loads[e.Target] = generatorLoadSnapshot{Target: e.Target, HeartbeatOK: generatorInventoryEntryReachable(e), PodCapacity: e.PodCapacity, CPULimitCores: e.CPULimitCores, MemoryLimitBytes: e.MemoryLimitBytes, MaxVUsers: e.MaxVUsers, SupportedProtocols: e.SupportedProtocols, CompatibleFeatures: e.CompatibleFeatures, RuntimeCapacities: e.RuntimeCapacities}
	}
	configurePodLoads(tr.settings, loads)
	compatibility := newGeneratorCompatibilityIndex(tr.enrichGeneratorTargetSpecsWithLabels(b.configuredTargetSpecs()), loads)
	participants := make([]recoveryCapacityParticipant, 0, len(placements))
	participating := map[string]bool{}
	namespace := ""
	for _, placement := range placements {
		e := index[placement.Target]
		pod := e.PodPlacement
		if pod == nil || !pod.Ready || pod.Terminating || pod.Namespace == "" || e.PodCapacity == nil || e.PodCapacity.Incarnation != placement.Before.Incarnation {
			return warning("Current Kubernetes identities for all participating generators are required.")
		}
		if namespace != "" && namespace != pod.Namespace {
			return warning("A single observed Kubernetes namespace is required for this capacity estimate.")
		}
		namespace = pod.Namespace
		participants = append(participants, recoveryCapacityParticipant{Target: e.Target, PodUID: pod.UID, Incarnation: placement.Before.Incarnation, Node: pod.NodeName, Demand: placement.Proposed})
		participating[e.Target] = true
	}
	var spares []recoveryCapacitySpare
	for _, e := range entries {
		pod := e.PodPlacement
		capacity := loads[e.Target].PodCapacity
		if participating[e.Target] || !allowedSet[e.Target] || !generatorInventoryEntryReachable(e) || e.Cordoned || e.DrainState != "active" || b.generatorDrainState(e.Target) != "active" || e.CompatibilityState != "compatible" || e.ActiveVUsers != 0 || e.AvailableCapacity <= 0 || e.MaxVUsers <= 0 || pod == nil || pod.Namespace != namespace || !pod.Ready || pod.Terminating || capacity == nil || capacity.State != "ready" || capacity.Dedicated || len(capacity.ReportedTestIDs) > 0 || !capacityVectorEmpty(capacity.Reserved) || !stringSliceContainsEqualFold(e.CompatibleFeatures, generatorrpc.FeaturePodCapacityStandbyV1) {
			continue
		}
		// Requiring compatibility with every group is conservative when individual
		// source pods host different groups. Do not bypass group target/label rules.
		eligible := true
		for _, group := range test.Groups {
			if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeSpecificHosts && !slices.Contains(group.Assignment.SelectedTargets, e.Target) {
				eligible = false
				break
			}
			if !compatibility.targetCanRun(e.Target, group.Protocol, groupRequiredRuntimeFeature(group), effectiveGroupRequiredLabels(group, test.Assignment)) {
				eligible = false
				break
			}
		}
		if !eligible {
			continue
		}
		budget := capacity.Schedulable
		budget.VUsers = min(budget.VUsers, float64(e.AvailableCapacity), float64(e.MaxVUsers))
		spare := recoveryCapacitySpare{Target: e.Target, PodUID: pod.UID, Incarnation: capacity.Incarnation, Node: pod.NodeName, Budget: budget}
		for _, p := range participants {
			spare.EligibleSources = append(spare.EligibleSources, p.Target)
		}
		spares = append(spares, spare)
	}
	plan, err := planNodeRecoveryCapacity(participants, spares, len(entries))
	if err != nil {
		return warning("The observed placement lacks a verified distinct compatible spare for each generator lost with one worker.")
	}
	return warning(fmt.Sprintf("The current capacity estimate can match every single-worker loss using %d observed unused generator pods; idle state, quotas and deployment replica limits still require admission checks.", len(plan.SpareTargets)))
}

func capacityVectorEmpty(v protocol.CapacityVector) bool {
	if v.Validate(false) != nil || v.CPUCores != 0 || v.MemoryMB != 0 || v.NetworkMbps != 0 || v.VUsers != 0 {
		return false
	}
	for _, n := range v.Protocols {
		if n != 0 {
			return false
		}
	}
	return true
}
