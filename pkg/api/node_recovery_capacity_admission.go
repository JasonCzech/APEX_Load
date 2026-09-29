package api

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errRecoverySpareRequiresPodReservations = errors.New("enable peak pod reservations before starting a test with recovery spares")

func recoverySpareSettingsError(test *ManagedTest, settings SystemSettings) error {
	if recoverySpareVUsers(test) > 0 && !settings.CapacityPlanning.PodReservationsEnabled {
		return errRecoverySpareRequiresPodReservations
	}
	return nil
}

func recoverySpareVUsers(test *ManagedTest) int {
	if test == nil || test.Assignment == nil {
		return 0
	}
	return max(0, test.Assignment.RecoverySpareVUsers)
}

func simpleRecoveryAssignment(a *GeneratorAssignmentSettings) bool {
	return a == nil || (a.SourceAffinity == "" && (a.Mode == "" || a.Mode == generatorAssignmentModeAuto || a.Mode == generatorAssignmentModeSpecificHosts) && len(a.RegionWeights) == 0 && len(a.LoadZoneWeights) == 0)
}

func validateRecoverySpareWorkload(test *ManagedTest) error {
	if recoverySpareVUsers(test) == 0 {
		return nil
	}
	if !assignmentToleratesGeneratorLoss(test.Assignment) || !simpleRecoveryAssignment(test.Assignment) || test.TerminationMode == "iterations" {
		return fmt.Errorf("reserved worker recovery requires fenced stateless HTTP placement")
	}
	for _, g := range test.Groups {
		if !strings.EqualFold(g.Protocol, "http") || !simpleRecoveryAssignment(g.Assignment) {
			return fmt.Errorf("reserved worker recovery supports stateless HTTP with automatic or explicit targets")
		}
		if ok, reason := groupAllowsGeneratorLossReshard(g); !ok {
			return fmt.Errorf("reserved worker recovery: %s", reason)
		}
		if ok, reason := groupAllowsStatelessDataReshard(g); !ok {
			return fmt.Errorf("reserved worker recovery: %s", reason)
		}
	}
	return nil
}

// RecoverySpareVUsers is an operator-declared quota envelope, not extra traffic.
// The entire union of dedicated spares must fit it. Sources and spares are then
// committed through the same ordinary capacity transaction before any work RPC.
func (tr *TestRunner) addRecoverySpareClaims(test *ManagedTest, claims []storage.PodCapacityReservation, loads map[string]generatorLoadSnapshot) ([]storage.PodCapacityReservation, error) {
	envelope := recoverySpareVUsers(test)
	if envelope == 0 {
		return claims, nil
	}
	if err := validateRecoverySpareWorkload(test); err != nil {
		return nil, err
	}
	if !tr.durableExecutionFence || len(claims) == 0 {
		return nil, fmt.Errorf("reserved worker recovery requires fenced nonempty placement")
	}
	b, ok := tr.execution.(*distributedExecutionBackend)
	if !ok {
		return nil, fmt.Errorf("reserved worker recovery requires distributed execution")
	}
	entries, at := tr.cachedGeneratorInventorySnapshot()
	if at.IsZero() || at.After(time.Now()) || time.Since(at) > defaultGeneratorSnapshotTTL || len(entries) > maxRecoveryCapacityPlanPods {
		return nil, fmt.Errorf("fresh bounded Kubernetes generator inventory is required for recovery admission")
	}
	allowed, err := b.allowedTargetsForTest(test)
	if err != nil {
		return nil, err
	}
	allowedSet := map[string]bool{}
	for _, target := range allowed {
		allowedSet[target] = true
	}
	index := map[string]GeneratorInventoryEntry{}
	for _, entry := range entries {
		if _, found := index[entry.Target]; found {
			return nil, fmt.Errorf("ambiguous recovery generator inventory")
		}
		index[entry.Target] = entry
	}
	compatibility := newGeneratorCompatibilityIndex(tr.enrichGeneratorTargetSpecsWithLabels(b.configuredTargetSpecs()), loads)
	var limit kubeops.StatefulSetAutoscalingLimit
	participants := make([]recoveryCapacityParticipant, 0, len(claims))
	participating := map[string]bool{}
	result := append([]storage.PodCapacityReservation(nil), claims...)
	for i, claim := range result {
		e := index[claim.Target]
		p := e.PodPlacement
		load := loads[claim.Target]
		if p == nil || p.Name == "" || !p.Ready || p.Terminating || !recoveryScaleLimitFresh(p.scaleLimit) || p.Namespace != p.scaleLimit.Namespace || !load.HeartbeatOK || load.PodCapacity == nil || load.PodCapacity.Incarnation != claim.Incarnation || e.PodCapacity == nil || e.PodCapacity.Incarnation != claim.Incarnation {
			return nil, fmt.Errorf("participating generator lacks current Kubernetes identity")
		}
		if limit.WorkloadUID != "" && limit != p.scaleLimit {
			return nil, fmt.Errorf("recovery placement crosses observed workloads")
		}
		limit = p.scaleLimit
		participants = append(participants, recoveryCapacityParticipant{Target: claim.Target, PodUID: p.UID, Incarnation: claim.Incarnation, Node: p.NodeName, Demand: claim.Demand})
		participating[claim.Target] = true
		result[i].RecoveryPlacement = &storage.PodCapacityRecoveryPlacement{Namespace: p.Namespace, WorkloadUID: limit.WorkloadUID, PodUID: p.UID, PodName: p.Name, NodeName: p.NodeName}
	}
	var spares []recoveryCapacitySpare
	for _, e := range entries {
		load, found := loads[e.Target]
		p := e.PodPlacement
		c := load.PodCapacity
		if !found || participating[e.Target] || !allowedSet[e.Target] || !load.HeartbeatOK || !generatorInventoryEntryReachable(e) || e.ActiveVUsers != 0 || load.Stats.ActiveVUsers != 0 || load.MaxVUsers <= 0 || load.AvailableCapacity <= 0 || e.Cordoned || e.DrainState != "active" || b.generatorDrainState(e.Target) != "active" || e.CompatibilityState != "compatible" || p == nil || p.Name == "" || !p.Ready || p.Terminating || p.Namespace != limit.Namespace || p.scaleLimit != limit || c == nil || c.State != "ready" || c.Dedicated || !capacityVectorEmpty(c.Reserved) || len(c.ReportedTestIDs) > 0 || e.PodCapacity == nil || e.PodCapacity.Incarnation != c.Incarnation || !stringSliceContainsEqualFold(load.CompatibleFeatures, generatorrpc.FeaturePodCapacityStandbyV1) {
			continue
		}
		eligible := true
		for _, g := range test.Groups {
			if g.Assignment != nil && g.Assignment.Mode == generatorAssignmentModeSpecificHosts && !slices.Contains(g.Assignment.SelectedTargets, e.Target) {
				eligible = false
				break
			}
			if !compatibility.targetCanRun(e.Target, g.Protocol, groupRequiredRuntimeFeature(g), effectiveGroupRequiredLabels(g, test.Assignment)) {
				eligible = false
				break
			}
		}
		if !eligible {
			continue
		}
		budget := c.Schedulable
		// A dedicated promise cannot exceed the current process's hard capacity.
		// Do not silently shrink the frozen generator reservation budget here.
		if budget.VUsers > float64(load.MaxVUsers) || budget.VUsers > float64(load.AvailableCapacity) {
			continue
		}
		spare := recoveryCapacitySpare{Target: e.Target, PodUID: p.UID, Incarnation: c.Incarnation, Node: p.NodeName, Budget: budget}
		for _, source := range participants {
			spare.EligibleSources = append(spare.EligibleSources, source.Target)
		}
		spares = append(spares, spare)
	}
	plan, err := planNodeRecoveryCapacity(participants, spares, limit.MaximumReplicas)
	if err != nil {
		return nil, fmt.Errorf("reserved worker recovery unavailable: %w", err)
	}
	charge := 0
	for _, target := range plan.SpareTargets {
		load := loads[target]
		c := load.PodCapacity
		p := index[target].PodPlacement
		if c.Schedulable.VUsers > float64(envelope) {
			return nil, fmt.Errorf("recovery spare budget exceeds the declared quota envelope")
		}
		cost := int(math.Ceil(c.Schedulable.VUsers))
		if nonNegativeSumExceedsLimit(charge, cost, envelope) {
			return nil, fmt.Errorf("recovery spare budget exceeds the declared quota envelope")
		}
		charge += cost
		var sources []string
		for _, replacements := range plan.Replacements {
			for source, destination := range replacements {
				if destination == target {
					sources = append(sources, source)
				}
			}
		}
		sort.Strings(sources)
		result = append(result, storage.PodCapacityReservation{ReservationID: claims[0].ReservationID, Revision: 1, Target: target, TestID: test.ID, Incarnation: c.Incarnation, Generation: claims[0].Generation, State: "pending", ModelVersion: claims[0].ModelVersion, HeadroomPct: c.HeadroomPct, Budget: c.Schedulable, Demand: c.Schedulable, Dedicated: true, RecoveryPlacement: &storage.PodCapacityRecoveryPlacement{Namespace: p.Namespace, WorkloadUID: limit.WorkloadUID, PodUID: p.UID, PodName: p.Name, NodeName: p.NodeName, ReplacementSources: sources}})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Target < result[j].Target })
	return result, nil
}

func (tr *TestRunner) recoveryStandbyIdentityCurrent(claim storage.PodCapacityReservation) bool {
	proof := claim.RecoveryPlacement
	if proof == nil {
		return true
	}
	entries, at := tr.cachedGeneratorInventorySnapshot()
	if at.IsZero() || at.After(time.Now()) || time.Since(at) > defaultGeneratorSnapshotTTL || len(entries) > maxRecoveryCapacityPlanPods {
		return false
	}
	matches := 0
	for _, e := range entries {
		if e.Target != claim.Target {
			continue
		}
		matches++
		p := e.PodPlacement
		if p == nil || p.UID != proof.PodUID || (proof.PodName != "" && p.Name != proof.PodName) || p.Namespace != proof.Namespace || p.NodeName != proof.NodeName || p.scaleLimit.WorkloadUID != proof.WorkloadUID || !recoveryScaleLimitFresh(p.scaleLimit) || !p.Ready || p.Terminating || e.PodCapacity == nil || e.PodCapacity.Incarnation != claim.Incarnation || e.Cordoned || e.DrainState != "active" {
			return false
		}
	}
	return matches == 1
}
