package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type podCapacityStore interface {
	ReservePodCapacity([]storage.PodCapacityReservation, string, string, uint64) error
	ListPodCapacityReservations() ([]storage.PodCapacityReservation, error)
	TransitionPodCapacity(string, string, string, string, string, string, string, uint64) error
}

type podCapacityClient interface {
	ReservePodCapacity(context.Context, *pb.PodCapacityClaim) (*pb.PodCapacityAck, error)
}

func (tr *TestRunner) reservePodLedger(store podCapacityStore, claims []storage.PodCapacityReservation, lease, owner string, generation uint64) error {
	return tr.trackDurableWrite(func() error { return store.ReservePodCapacity(claims, lease, owner, generation) })
}

func (tr *TestRunner) transitionPodLedger(store podCapacityStore, target, testID, incarnation, reservationID, state, lease, owner string, generation uint64) error {
	return tr.trackDurableWrite(func() error {
		return store.TransitionPodCapacity(target, testID, incarnation, reservationID, state, lease, owner, generation)
	})
}

type PodCapacityPreview struct {
	Target   string                    `json:"target"`
	Before   protocol.CapacitySnapshot `json:"before"`
	Proposed protocol.CapacityVector   `json:"proposed"`
	After    protocol.CapacitySnapshot `json:"after"`
}

func podReservationGroups(test *ManagedTest) ([]TestGroup, error) {
	groups := append([]TestGroup(nil), test.Groups...)
	adaptive := normalizeScenarioMode(test.ScenarioMode) == "goal_oriented"
	maximum := 0
	if adaptive {
		if test.GoalConfig == nil || test.GoalConfig.MaxVUsers == nil || *test.GoalConfig.MaxVUsers <= 0 {
			return nil, fmt.Errorf("adaptive pod reservations require a bounded maximum VUser count")
		}
		maximum = *test.GoalConfig.MaxVUsers
	}
	for i := range groups {
		for _, stage := range groups[i].Stages {
			groups[i].VUsers = max(groups[i].VUsers, stage.TargetVUsers)
		}
		// Adaptive group weights may change during execution. Reserve each load
		// group's possible maximum, rather than assuming today's distribution.
		if adaptive && groups[i].Role != GroupRoleObserver {
			groups[i].VUsers = max(groups[i].VUsers, maximum)
		}
	}
	return groups, nil
}

func (tr *TestRunner) previewPodPlacementLocked(test *ManagedTest) ([]PodCapacityPreview, []string) {
	if !tr.settings.CapacityPlanning.PodReservationsEnabled {
		if test.Assignment != nil && test.Assignment.SharingMode == "dedicated" {
			return nil, []string{"enable peak pod reservations before selecting dedicated generators"}
		}
		return nil, nil
	}
	groups, err := podReservationGroups(test)
	if err != nil {
		return nil, []string{err.Error()}
	}
	b, ok := tr.execution.(*distributedExecutionBackend)
	if !ok {
		return nil, []string{"distributed generator capacity unavailable"}
	}
	entries, at := tr.cachedGeneratorInventorySnapshot()
	if at.IsZero() || time.Since(at) > defaultGeneratorSnapshotTTL {
		return nil, []string{"generator capacity snapshot is stale"}
	}
	allowed, err := b.allowedTargetsForTest(test)
	if err != nil {
		return nil, []string{err.Error()}
	}
	loads := map[string]generatorLoadSnapshot{}
	for _, e := range entries {
		if e.DrainState != "active" || e.Cordoned {
			continue
		}
		loads[e.Target] = generatorLoadSnapshot{Target: e.Target, HeartbeatOK: generatorInventoryEntryReachable(e), PodCapacity: e.PodCapacity, AvailableCapacity: e.AvailableCapacity, CPULimitCores: e.CPULimitCores, MemoryLimitBytes: e.MemoryLimitBytes, MaxVUsers: e.MaxVUsers, SupportedProtocols: e.SupportedProtocols, CompatibleFeatures: e.CompatibleFeatures, RuntimeCapacities: e.RuntimeCapacities}
	}
	configurePodLoads(tr.settings, loads)
	shards := planDistributedGroupShardsForCandidates(groups, loads, loadSnapshotsForTargets(loads, allowed), orderedUniqueTargets(mapKeys(loads)), allowed, test.Assignment, tr.enrichGeneratorTargetSpecsWithLabels(b.configuredTargetSpecs()))
	demand := map[string]protocol.CapacityVector{}
	for _, group := range groups {
		if group.VUsers <= 0 || len(shards[group.Name]) == 0 {
			return nil, []string{podPlacementShortage(group, loadSnapshotsForTargets(loads, allowed))}
		}
		for _, s := range shards[group.Name] {
			demand[s.Target] = demand[s.Target].Add(podGroupDemand(group).Scale(float64(s.VUsers) / float64(group.VUsers)))
		}
	}
	var out []PodCapacityPreview
	for target, d := range demand {
		before := *loads[target].PodCapacity
		if test.Assignment != nil && test.Assignment.SharingMode == "dedicated" {
			d = before.Schedulable
		}
		after := protocol.NewCapacitySnapshot(before.Schedulable, before.Reserved.Add(d), before.HeadroomPct)
		after.Incarnation = before.Incarnation
		after.ObservedAtUnixMS = before.ObservedAtUnixMS
		after.Dedicated = before.Dedicated || (test.Assignment != nil && test.Assignment.SharingMode == "dedicated")
		out = append(out, PodCapacityPreview{Target: target, Before: before, Proposed: d, After: after})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out, nil
}

func podPlacementShortage(group TestGroup, loads map[string]generatorLoadSnapshot) string {
	targets := mapKeys(loads)
	sort.Strings(targets)
	var reasons []string
	for _, target := range targets {
		capacity := loads[target].PodCapacity
		if capacity == nil || capacity.State != "ready" {
			reasons = append(reasons, target+": capacity unavailable or unreconciled")
		} else if capacity.Dedicated {
			reasons = append(reasons, target+": dedicated")
		} else if err := capacity.Schedulable.Fits(capacity.Reserved.Add(podGroupDemand(group))); err != nil {
			reasons = append(reasons, target+": "+err.Error())
		}
		if len(reasons) == 3 {
			break
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "eligible generator, runtime or placement capacity unavailable")
	}
	return fmt.Sprintf("group %s peak cannot be placed: %s", group.Name, strings.Join(reasons, "; "))
}

// Only the pre-AssignWork reservation failure path may call this: no generator
// execution exists, so the initializing ownership can become a passive queue row.
func (tr *TestRunner) requeueBeforePodWork(test *ManagedTest, reason string, generation uint64, owner string) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[test.ID] != test || test.Status != "INITIALIZING" || len(test.remoteClientsByTarget) > 0 {
		return fmt.Errorf("test changed before capacity queue transition")
	}
	if test.cancel != nil {
		test.cancel()
		test.cancel = nil
	}
	test.executionCtx = nil
	test.executionFence = ""
	test.executionGeneration = 0
	test.passiveOperationToken = ""
	return tr.markTestPendingLockedWithExpectedTerm(test, reason, &executionLeaseTerm{ownerID: owner, generation: generation})
}

func podGroupDemand(group TestGroup) protocol.CapacityVector {
	score, _ := calculateScenarioComplexity([]TestGroup{group}, nil)
	profile := classifyCapacityWorkload([]TestGroup{group}, nil)
	_, estimate := estimateCapacityFootprint(score, profile, group.VUsers)
	name := strings.ToLower(group.Protocol)
	if group.script != nil && group.script.Protocol != "" {
		name = strings.ToLower(group.script.Protocol)
	}
	return protocol.CapacityVector{CPUCores: estimate.CPUCores, MemoryMB: estimate.MemoryMB, NetworkMbps: estimate.NetworkMbps, VUsers: float64(group.VUsers), Protocols: map[string]float64{name: float64(group.VUsers)}}
}

func configurePodCapacity(settings SystemSettings, load generatorLoadSnapshot) *protocol.CapacitySnapshot {
	if !settings.CapacityPlanning.PodReservationsEnabled {
		return nil
	}
	unavailable := &protocol.CapacitySnapshot{State: "unavailable", ModelVersion: protocol.CapacityReservationModel, Confidence: "estimated"}
	if !load.HeartbeatOK || load.PodCapacity == nil || load.PodCapacity.Incarnation == "" || !stringSliceContainsEqualFold(load.CompatibleFeatures, generatorrpc.FeaturePodCapacityV1) {
		return unavailable
	}
	if load.PodCapacity.State == "unreconciled" {
		return load.PodCapacity
	}
	if load.PodCapacity.State == "ready" {
		copy := *load.PodCapacity
		return &copy
	}
	if load.PodCapacity.State != "unconfigured" {
		return unavailable
	}
	cfg := settings.CapacityPlanning.Generator
	raw := protocol.CapacityVector{CPUCores: cfg.CPUCores, MemoryMB: float64(cfg.MemoryMB), NetworkMbps: cfg.NetworkMbps, VUsers: float64(cfg.MaxVUsers), Protocols: map[string]float64{}}
	if load.CPULimitCores <= 0 || load.MemoryLimitBytes <= 0 || load.MaxVUsers <= 0 {
		return unavailable
	}
	raw.CPUCores = math.Min(raw.CPUCores, load.CPULimitCores)
	raw.MemoryMB = math.Min(raw.MemoryMB, float64(load.MemoryLimitBytes)/(1024*1024))
	raw.VUsers = math.Min(raw.VUsers, float64(load.MaxVUsers))
	for _, name := range load.SupportedProtocols {
		limit := float64(protocolDensityEnvelope(name).MaxVUsersPerGenerator)
		for _, cap := range load.RuntimeCapacities {
			if cap.Protocol == name {
				if !cap.Available {
					limit = 0
				} else {
					limit = math.Min(limit, float64(cap.MaxVUsers))
				}
			}
		}
		if limit > 0 {
			raw.Protocols[name] = limit
		}
	}
	budget := raw.Scale(1 - settings.CapacityPlanning.HeadroomPct/100)
	if budget.Validate(true) != nil {
		return unavailable
	}
	s := protocol.NewCapacitySnapshot(budget, protocol.CapacityVector{}, settings.CapacityPlanning.HeadroomPct)
	s.Incarnation = load.PodCapacity.Incarnation
	s.ObservedAtUnixMS = load.PodCapacity.ObservedAtUnixMS
	return &s
}

func configurePodLoads(settings SystemSettings, loads map[string]generatorLoadSnapshot) {
	for target, load := range loads {
		load.PodCapacity = configurePodCapacity(settings, load)
		loads[target] = load
	}
}

func consumePodGroupCapacity(load *generatorLoadSnapshot, group TestGroup, n int) {
	if load.PodCapacity == nil || group.VUsers <= 0 {
		return
	}
	before := load.PodCapacity
	next := protocol.NewCapacitySnapshot(before.Schedulable, before.Reserved.Add(podGroupDemand(group).Scale(float64(n)/float64(group.VUsers))), before.HeadroomPct)
	next.State = before.State
	next.Incarnation = before.Incarnation
	next.Dedicated = before.Dedicated
	next.ObservedAtUnixMS = before.ObservedAtUnixMS
	load.PodCapacity = &next
	load.podPlacementOwned = true
}

func podCapacityShards(group TestGroup, loads map[string]generatorLoadSnapshot, targets []string, assignment *GeneratorAssignmentSettings, region *regionPlacementState, compatibility *generatorCompatibilityIndex) []GeneratorShardSummary {
	demand := podGroupDemand(group)
	if group.VUsers <= 0 || demand.Validate(false) != nil {
		return nil
	}
	unit := demand.Scale(1 / float64(group.VUsers))
	type candidate struct {
		target string
		max    int
		used   float64
	}
	var candidates []candidate
	for _, target := range targets {
		load, ok := loads[target]
		if !ok || load.PodCapacity == nil || load.PodCapacity.State != "ready" || load.PodCapacity.Dedicated {
			continue
		}
		if assignment != nil && assignment.SharingMode == "dedicated" && load.PodCapacity.ReservedPct > 0 && !load.podPlacementOwned {
			continue
		}
		if compatibility != nil && !compatibility.targetCanRun(target, group.Protocol, groupRequiredRuntimeFeature(group), effectiveGroupRequiredLabels(group, assignment)) {
			continue
		}
		if !runtimeOccupancyAllowsGroup(group, load) {
			continue
		}
		maxV := math.Min(float64(group.VUsers), float64(load.AvailableCapacity))
		for key, n := range unit.Dimensions() {
			if n > 0 {
				maxV = math.Min(maxV, load.PodCapacity.Remaining.Dimensions()[key]/n)
			}
		}
		if available, ok := protocolRuntimeAvailableVUsers(load, group.Protocol); ok {
			maxV = math.Min(maxV, float64(available))
		}
		if n := int(math.Floor(maxV + 1e-9)); n > 0 {
			candidates = append(candidates, candidate{target, n, load.PodCapacity.ReservedPct})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if region != nil {
			av, aw := region.preference(a.target)
			bv, bw := region.preference(b.target)
			if aw != bw {
				return aw
			}
			if aw && av != bv {
				return av > bv
			}
		}
		if a.used != b.used {
			return a.used > b.used
		}
		aFits, bFits := a.max >= group.VUsers, b.max >= group.VUsers
		if aFits != bFits {
			return aFits
		}
		if a.max != b.max {
			if aFits {
				return a.max < b.max
			}
			return a.max > b.max
		}
		return a.target < b.target
	})
	left := group.VUsers
	var out []GeneratorShardSummary
	for _, c := range candidates {
		n := min(left, c.max)
		out = append(out, GeneratorShardSummary{Group: group.Name, Target: c.target, VUsers: n})
		left -= n
		if left == 0 {
			return out
		}
	}
	return nil
}

func podClaim(r storage.PodCapacityReservation, release bool) *pb.PodCapacityClaim {
	return &pb.PodCapacityClaim{ReservationId: r.ReservationID, TestId: r.TestID, ExecutionGeneration: r.Generation, Incarnation: r.Incarnation, ModelVersion: r.ModelVersion, Schedulable: generatorrpc.CapacityVectorToProto(r.Budget), Demand: generatorrpc.CapacityVectorToProto(r.Demand), HeadroomPct: r.HeadroomPct, Dedicated: r.Dedicated, GroupPeaks: r.GroupPeaks, Release: release, Revision: r.Revision}
}

func (tr *TestRunner) reservePodPlacement(ctx context.Context, test *ManagedTest, loads map[string]generatorLoadSnapshot, shards map[string][]GeneratorShardSummary, clients map[string]generatorClient, generation uint64, owner string) ([]storage.PodCapacityReservation, error) {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return nil, fmt.Errorf("durable pod capacity store unavailable")
	}
	groups, err := podReservationGroups(test)
	if err != nil {
		return nil, err
	}
	byTarget := map[string]*storage.PodCapacityReservation{}
	reservationID := uuid.NewString()
	for _, group := range groups {
		if group.VUsers <= 0 || len(shards[group.Name]) == 0 {
			return nil, fmt.Errorf("%s", podPlacementShortage(group, loads))
		}
		for _, shard := range shards[group.Name] {
			load := loads[shard.Target]
			if load.PodCapacity == nil || load.PodCapacity.State != "ready" {
				return nil, fmt.Errorf("generator %s capacity unavailable", shard.Target)
			}
			r := byTarget[shard.Target]
			if r == nil {
				r = &storage.PodCapacityReservation{ReservationID: reservationID, Revision: 1, GroupDemands: map[string]protocol.CapacityVector{}, Target: shard.Target, TestID: test.ID, Incarnation: load.PodCapacity.Incarnation, Generation: generation, State: "pending", ModelVersion: protocol.CapacityReservationModel, HeadroomPct: load.PodCapacity.HeadroomPct, Budget: load.PodCapacity.Schedulable, GroupPeaks: map[string]int32{}, Dedicated: test.Assignment != nil && test.Assignment.SharingMode == "dedicated"}
				byTarget[shard.Target] = r
			}
			r.Demand = r.Demand.Add(podGroupDemand(group).Scale(float64(shard.VUsers) / float64(group.VUsers)))
			r.GroupPeaks[group.Name] += int32(shard.VUsers)
			r.GroupDemands[group.Name] = r.GroupDemands[group.Name].Add(podGroupDemand(group).Scale(float64(shard.VUsers) / float64(group.VUsers)))
		}
	}
	var claims []storage.PodCapacityReservation
	for _, r := range byTarget {
		if r.Dedicated {
			r.Demand = r.Budget
		}
		claims = append(claims, *r)
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].Target < claims[j].Target })
	if err := tr.reservePodLedger(store, claims, executionLeaseName, owner, generation); err != nil {
		return nil, err
	}
	for _, claim := range claims {
		client, ok := clients[claim.Target].(podCapacityClient)
		if !ok {
			return claims, fmt.Errorf("generator %s lacks capacity reservation client", claim.Target)
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ack, err := client.ReservePodCapacity(callCtx, podClaim(claim, false))
		cancel()
		if err != nil {
			return claims, err
		}
		if !ack.Accepted {
			return claims, fmt.Errorf("generator %s: %s", claim.Target, ack.ErrorMessage)
		}
	}
	return claims, nil
}

func (tr *TestRunner) releasePodClaims(claims []storage.PodCapacityReservation, clients map[string]generatorClient, owner string, generation uint64) {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return
	}
	for _, claim := range claims {
		if err := tr.transitionPodLedger(store, claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "releasing", executionLeaseName, owner, generation); err != nil {
			continue
		}
		client, ok := clients[claim.Target].(podCapacityClient)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		request := podClaim(claim, true)
		request.ExecutionGeneration = generation
		ack, err := client.ReservePodCapacity(ctx, request)
		cancel()
		if err == nil && ack.Accepted {
			_ = tr.transitionPodLedger(store, claim.Target, claim.TestID, claim.Incarnation, claim.ReservationID, "released", executionLeaseName, owner, generation)
		}
	}
}

func (tr *TestRunner) releasePodClaimsWithDial(b *distributedExecutionBackend, claims []storage.PodCapacityReservation) {
	generation, owner, _, err := tr.generatorExecutionClaim()
	if err != nil {
		return
	}
	clients := map[string]generatorClient{}
	defer func() {
		for _, client := range clients {
			_ = client.Close()
		}
	}()
	for _, claim := range claims {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		client, err := b.dial(ctx, claim.Target)
		cancel()
		if err == nil {
			clients[claim.Target] = client
		}
	}
	tr.releasePodClaims(claims, clients, owner, generation)
}

func (tr *TestRunner) enrichPodCapacityInventory(entries []GeneratorInventoryEntry) {
	tr.mu.RLock()
	settings := cloneSystemSettings(tr.settings)
	tr.mu.RUnlock()
	for i := range entries {
		e := &entries[i]
		load := generatorLoadSnapshot{HeartbeatOK: generatorInventoryEntryReachable(*e), PodCapacity: e.PodCapacity, CPULimitCores: e.CPULimitCores, MemoryLimitBytes: e.MemoryLimitBytes, MaxVUsers: e.MaxVUsers, SupportedProtocols: e.SupportedProtocols, CompatibleFeatures: e.CompatibleFeatures, RuntimeCapacities: e.RuntimeCapacities}
		e.PodCapacity = configurePodCapacity(settings, load)
		if e.PodCapacity != nil && e.PodCapacity.ReservedPct > 0 {
			e.Restartable = false
			e.RestartBlockedReason = "peak capacity is reserved"
		}
	}
	if !settings.CapacityPlanning.PodReservationsEnabled {
		return
	}
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return
	}
	claims, err := store.ListPodCapacityReservations()
	if err != nil {
		for i := range entries {
			if entries[i].PodCapacity != nil {
				entries[i].PodCapacity.State = "unreconciled"
			}
		}
		return
	}
	for i := range entries {
		e := &entries[i]
		if e.PodCapacity == nil {
			continue
		}
		var used protocol.CapacityVector
		dedicated := false
		knownTests := map[string]bool{}
		for _, r := range claims {
			if r.Target != e.Target {
				continue
			}
			if r.Incarnation != e.PodCapacity.Incarnation {
				e.PodCapacity.State = "unreconciled"
				continue
			}
			used = used.Add(r.Demand)
			knownTests[r.TestID] = true
			dedicated = dedicated || r.Dedicated
		}
		// Include durable pending claims which have not yet reached the generator.
		generatorUsed := e.PodCapacity.Reserved
		pending, untracked := false, false
		for _, id := range e.PodCapacity.ReportedTestIDs {
			untracked = untracked || !knownTests[id]
		}
		for key, amount := range used.Dimensions() {
			pending = pending || amount > generatorUsed.Dimensions()[key]+1e-9
		}
		for key, amount := range generatorUsed.Dimensions() {
			untracked = untracked || amount > used.Dimensions()[key]+1e-9
		}
		if pending && !untracked {
			before := e.PodCapacity
			next := protocol.NewCapacitySnapshot(before.Schedulable, used, before.HeadroomPct)
			next.Incarnation = before.Incarnation
			next.State = before.State
			next.ObservedAtUnixMS = before.ObservedAtUnixMS
			next.Dedicated = dedicated
			next.ReportedTestIDs = append([]string(nil), before.ReportedTestIDs...)
			e.PodCapacity = &next
		}
		if untracked {
			e.PodCapacity.State = "unreconciled"
		}
		if used.VUsers > 0 {
			e.Restartable = false
			e.RestartBlockedReason = "peak capacity is reserved"
		}
	}
}

func (tr *TestRunner) reconcilePodCapacity(ctx context.Context, entries []GeneratorInventoryEntry) {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return
	}
	b, ok := tr.getExecutionBackend().(*distributedExecutionBackend)
	if !ok {
		return
	}
	generation, owner, _, err := tr.generatorExecutionClaim()
	if err != nil {
		return
	}
	claims, err := store.ListPodCapacityReservations()
	if err != nil {
		return
	}
	byTarget := map[string]GeneratorInventoryEntry{}
	for _, e := range entries {
		byTarget[e.Target] = e
	}
	for _, r := range claims {
		if ctx.Err() != nil {
			return
		}
		e, ok := byTarget[r.Target]
		if !ok || !generatorInventoryEntryReachable(e) || e.PodCapacity == nil {
			continue
		}
		record, err := tr.testStore.GetTest(r.TestID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if r.State != "releasing" && record != nil {
			switch record.Status {
			case "COMPLETE", "COMPLETED", "FAILED", "STOPPED", "CANCELLED", "ABORTED":
			default:
				continue
			}
		}
		if e.PodCapacity.Incarnation != r.Incarnation {
			_ = tr.transitionPodLedger(store, r.Target, r.TestID, r.Incarnation, r.ReservationID, "released", executionLeaseName, owner, generation)
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, b.timeout)
		client, err := b.dial(callCtx, r.Target)
		if err == nil {
			if capacity, ok := client.(podCapacityClient); ok {
				request := podClaim(r, true)
				request.ExecutionGeneration = generation
				ack, releaseErr := capacity.ReservePodCapacity(callCtx, request)
				if releaseErr == nil && ack.Accepted {
					_ = tr.transitionPodLedger(store, r.Target, r.TestID, r.Incarnation, r.ReservationID, "released", executionLeaseName, owner, generation)
				}
			}
			_ = client.Close()
		}
		cancel()
	}
}

func reservedReplicaFloor(claims []storage.PodCapacityReservation, fallback int) int {
	floor := 0
	for _, claim := range claims {
		host := claim.Target
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		pod := strings.Split(host, ".")[0]
		index := strings.LastIndex(pod, "-")
		n, err := strconv.Atoi(pod[index+1:])
		if index < 0 || err != nil || n < 0 || n >= maxManagedTestVUsers {
			floor = max(floor, fallback)
			continue
		}
		floor = max(floor, n+1)
	}
	return floor
}
