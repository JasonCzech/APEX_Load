package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/protobuf/proto"
)

// JavaHostAssignmentSettings explicitly selects a generator's operator-owned
// dedicated profile. No endpoint, credential or executable content is accepted.
type JavaHostAssignmentSettings struct {
	ProfileID     string `json:"profile_id"`
	PreparationMS int64  `json:"preparation_ms"`
	RunLifetimeMS int64  `json:"run_lifetime_ms"`
}

// JavaHostPlacementSummary freezes the selection before reservation. ReservationID
// is populated only after the generator confirms cold preparation; it is not a token.
type JavaHostPlacementSummary struct {
	PreparedVUsers int32                  `json:"prepared_vusers,omitempty"`
	AdmittedVUsers int32                  `json:"admitted_vusers,omitempty"`
	Contract       string                 `json:"contract"`
	Mode           string                 `json:"mode"`
	Containment    string                 `json:"containment"`
	Profile        JavaHostProfileSummary `json:"profile"`
	PreparationMS  int64                  `json:"preparation_ms"`
	RunLifetimeMS  int64                  `json:"run_lifetime_ms"`
	MaxHosts       int32                  `json:"max_hosts"`
	MaxVUsers      int32                  `json:"max_vusers"`
	ReservationID  string                 `json:"reservation_id,omitempty"`
}

func cloneJavaHostAssignmentSettings(in *JavaHostAssignmentSettings) *JavaHostAssignmentSettings {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
func cloneJavaHostPlacementSummary(in *JavaHostPlacementSummary) *JavaHostPlacementSummary {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
func javaHostSettings(group TestGroup) *JavaHostAssignmentSettings {
	if group.Assignment == nil {
		return nil
	}
	return group.Assignment.JavaHost
}
func validateJavaHostAssignmentSettings(s *JavaHostAssignmentSettings) error {
	if s == nil {
		return nil
	}
	if s.ProfileID == "" || strings.TrimSpace(s.ProfileID) != s.ProfileID || len(s.ProfileID) > 128 || s.PreparationMS < 1 || s.PreparationMS > 3600000 || s.RunLifetimeMS < 1 || s.RunLifetimeMS > 86400000 {
		return errors.New("java_host_selection_invalid")
	}
	return nil
}

func javaHostPlacementForGroup(group TestGroup, load generatorLoadSnapshot) (*JavaHostPlacementSummary, error) {
	s := javaHostSettings(group)
	if s == nil {
		return nil, errors.New("java_host_selection_missing")
	}
	if err := validateJavaHostAssignmentSettings(s); err != nil {
		return nil, err
	}
	if group.Protocol != "java_vuser" || group.script == nil || group.stageDriven && len(group.Stages) == 0 || group.VUsers < 1 {
		return nil, errors.New("java_host_workload_unsupported")
	}
	c := load.JavaHost
	if !load.HeartbeatOK || c == nil || c.Contract != generatorrpc.JavaHostSelectionContract || c.Mode != "dedicated" || c.Containment != "execution_pod" || !protocol.RuntimeFeaturesSatisfy(load.CompatibleFeatures, generatorrpc.FeatureJavaHostBrokerV1) {
		return nil, errors.New("java_host_backend_unavailable")
	}
	for _, feature := range groupRequiredDataRuntimeFeatures(group) {
		if !protocol.RuntimeFeaturesSatisfy(load.CompatibleFeatures, feature) {
			return nil, fmt.Errorf("java_host_native_capability_unavailable: %s", feature)
		}
	}
	if len(group.Stages) > 0 && !c.PreparedPopulation {
		return nil, errors.New("java_host_prepared_population_unavailable")
	}
	digest, err := engine.JavaPooledScriptSHA256(group.script)
	if err != nil {
		return nil, errors.New("java_host_script_identity_invalid")
	}
	for _, p := range c.Profiles {
		if p.ScriptSHA256 != digest || p.ProfileID != s.ProfileID {
			continue
		}
		if s.PreparationMS > p.PreparationMS || s.RunLifetimeMS > p.MaxRunMS {
			return nil, errors.New("java_host_profile_deadline_exceeded")
		}
		if p.MemoryBytes < 1 || group.VUsers > int(c.MaxHosts) || group.VUsers > int(c.MaxVUsers) || int64(group.VUsers) > c.MaxMemoryBytes/p.MemoryBytes || group.VUsers > int(load.AvailableCapacity) {
			return nil, errors.New("java_host_capacity_unavailable")
		}
		if load.PodCapacity != nil {
			unit := javaHostFacadeUnit(p)
			if p.FacadeResourcesSHA256 == "" || load.PodCapacity.State != "ready" || load.PodCapacity.Dedicated || load.PodCapacity.Schedulable.Fits(load.PodCapacity.Reserved.Add(unit.Scale(float64(group.VUsers)))) != nil {
				return nil, errors.New("java_host_generator_capacity_unavailable")
			}
		}
		return &JavaHostPlacementSummary{Contract: c.Contract, Mode: c.Mode, Containment: c.Containment, Profile: p, PreparationMS: s.PreparationMS, RunLifetimeMS: s.RunLifetimeMS, MaxHosts: c.MaxHosts, MaxVUsers: c.MaxVUsers}, nil
	}
	return nil, errors.New("java_host_profile_not_approved")
}

func (p *JavaHostPlacementSummary) selection() *pb.JavaHostSelection {
	if p == nil {
		return nil
	}
	return &pb.JavaHostSelection{Contract: p.Contract, ScriptSha256: p.Profile.ScriptSHA256, ProfileId: p.Profile.ProfileID, ConfigurationSha256: p.Profile.ConfigurationSHA256, PolicySha256: p.Profile.PolicySHA256, PreparationMs: p.PreparationMS, RunLifetimeMs: p.RunLifetimeMS, FacadeResourcesSha256: p.Profile.FacadeResourcesSHA256, PreparedVusers: p.PreparedVUsers}
}

func javaHostPlacementStillAdvertised(p *JavaHostPlacementSummary, load generatorLoadSnapshot) bool {
	if p == nil || p.PreparedVUsers > 0 && (load.JavaHost == nil || !load.JavaHost.PreparedPopulation) || !load.HeartbeatOK || load.JavaHost == nil || load.JavaHost.Contract != p.Contract || load.JavaHost.Mode != p.Mode || load.JavaHost.Containment != p.Containment {
		return false
	}
	for _, profile := range load.JavaHost.Profiles {
		if profile == p.Profile {
			return true
		}
	}
	return false
}

func javaHostPlacementAcknowledged(p *JavaHostPlacementSummary, ack *pb.JavaHostPlacement) bool {
	return p != nil && ack != nil && proto.Equal(p.selection(), ack.Selection) && ack.Mode == p.Mode && ack.Containment == p.Containment && ack.ReservationId != "" && len(ack.ReservationId) <= 256 && ack.MaxHosts > 0 && ack.MaxVusers > 0
}

func chooseJavaHostShards(group TestGroup, all, defaults map[string]generatorLoadSnapshot, preferredAll, preferredDefault []string, inherited *GeneratorAssignmentSettings, compatibility *generatorCompatibilityIndex) []GeneratorShardSummary {
	if groupInheritsAssignment(group) && inherited != nil &&
		(inherited.Mode == generatorAssignmentModeRegionWeighted || inherited.Mode == generatorAssignmentModeLoadZoneWeighted) {
		// The host allocator has hard per-generator ceilings but no regional
		// quota input. Do not silently replace an inherited weighted placement
		// policy with capacity weighting.
		return nil
	}
	admissions, err := javaHostScheduleAdmissions(group)
	if err != nil {
		return nil
	}
	group.VUsers = admissions
	loads, targets := defaults, preferredDefault
	if group.Assignment != nil {
		switch group.Assignment.Mode {
		case generatorAssignmentModeAuto:
			loads, targets = all, preferredAll
		case generatorAssignmentModeSpecificHosts:
			loads, targets = all, group.Assignment.SelectedTargets
		}
	}
	// A fleet pool is a run-wide boundary, including for a group that explicitly
	// requests automatic placement. The group may narrow it, never expand it.
	if inherited != nil && inherited.PoolID != "" {
		// A dynamic pool without concrete members cannot yet prove that an
		// advertised Java host belongs to it. Do not widen that unresolved
		// boundary to every configured generator.
		if inherited.Mode != generatorAssignmentModeSpecificHosts {
			return nil
		}
		poolTargets := make(map[string]struct{}, len(inherited.SelectedTargets))
		for _, target := range inherited.SelectedTargets {
			poolTargets[target] = struct{}{}
		}
		bounded := make([]string, 0, len(targets))
		for _, target := range targets {
			if _, ok := poolTargets[target]; ok {
				bounded = append(bounded, target)
			}
		}
		targets = bounded
	}
	// Validate each candidate independently, then freeze its exact approved
	// profile. Host and memory ceilings are hard bounds; the broker remains the
	// authority for live reservations, including shared broker deployments.
	placements := map[string]*JavaHostPlacementSummary{}
	ceilings := map[string]int{}
	eligible := []string{}
	probe := group
	probe.VUsers = 1
	for _, target := range targets {
		if _, seen := ceilings[target]; seen {
			continue
		}
		load, ok := loads[target]
		if !ok {
			continue
		}
		if compatibility != nil {
			spec, ok := compatibility.byTarget[target]
			if !ok || generatorCompatibilityState(spec) != "compatible" || !targetMatchesRequiredLabels(spec, effectiveGroupRequiredLabels(group, inherited)) {
				continue
			}
		}
		placement, err := javaHostPlacementForGroup(probe, load)
		if err != nil {
			continue
		}
		maximum := min(int64(group.VUsers), int64(load.AvailableCapacity), int64(load.JavaHost.MaxHosts), int64(load.JavaHost.MaxVUsers), load.JavaHost.MaxMemoryBytes/placement.Profile.MemoryBytes)
		if load.PodCapacity != nil {
			if inherited != nil && inherited.SharingMode == "dedicated" && load.PodCapacity.ReservedPct > 0 && !load.podPlacementOwned {
				continue
			}
			remaining := load.PodCapacity.Remaining.Dimensions()
			for key, n := range javaHostFacadeUnit(placement.Profile).Dimensions() {
				if n > 0 {
					maximum = min(maximum, int64(math.Floor(remaining[key]/n+1e-9)))
				}
			}
		}
		if maximum <= 0 {
			continue
		}
		eligible = append(eligible, target)
		ceilings[target] = int(maximum)
		placements[target] = placement
	}
	allocations := boundedJavaHostAllocations(group.VUsers, eligible, ceilings)
	if allocations == nil {
		return nil
	}
	shards := make([]GeneratorShardSummary, 0, len(eligible))
	for _, target := range eligible {
		if n := allocations[target]; n > 0 {
			if len(group.Stages) > 0 {
				placements[target].PreparedVUsers = int32(n)
			}
			shards = append(shards, GeneratorShardSummary{Group: group.Name, Target: target, VUsers: n, ShardStrategy: generatorShardStrategyCapacityWeighted, JavaHost: placements[target]})
		}
	}
	return shards
}

func consumeJavaHostCapacity(load *generatorLoadSnapshot, group TestGroup, n int) {
	if load == nil || load.JavaHost == nil || n <= 0 {
		return
	}
	s := javaHostSettings(group)
	if s == nil {
		return
	}
	load.JavaHost = cloneJavaHostCapabilitySummary(load.JavaHost)
	c := load.JavaHost
	c.MaxHosts = max(0, c.MaxHosts-int32(n))
	c.MaxVUsers = max(0, c.MaxVUsers-int32(n))
	digest, _ := engine.JavaPooledScriptSHA256(group.script)
	for _, p := range c.Profiles {
		if p.ProfileID == s.ProfileID && p.ScriptSHA256 == digest && p.MemoryBytes > 0 {
			if int64(n) > c.MaxMemoryBytes/p.MemoryBytes {
				c.MaxMemoryBytes = 0
			} else {
				c.MaxMemoryBytes -= int64(n) * p.MemoryBytes
			}
			break
		}
	}
}

func validateJavaHostRun(test *ManagedTest, group TestGroup, placement *JavaHostPlacementSummary) error {
	if placement == nil {
		return nil
	}
	if test == nil || normalizeScenarioMode(test.ScenarioMode) == "goal_oriented" || test.Assignment != nil && (test.Assignment.SourceAffinity != "" || test.Assignment.TolerateGeneratorLoss || test.Assignment.FailoverPolicy == failoverPolicyContinue) {
		return errors.New("java_host_recovery_unsupported")
	}
	_, _, initStrategy := distributedGroupRuntimeConfig(test, group)
	if initStrategy == "at_once" {
		return errors.New("java_host_initialization_unsupported")
	}
	pre, post := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	pre += testexec.StartDelayWindow(test.StartDelay)
	required := test.Duration + pre + post + time.Duration(placement.PreparationMS+placement.Profile.InvocationMS)*time.Millisecond
	if required > time.Duration(placement.RunLifetimeMS)*time.Millisecond {
		return fmt.Errorf("java_host_lifetime_margin_insufficient")
	}
	return nil
}

// boundedJavaHostAllocations uses the established weighting policy, but caps
// every allocation. Rounding must never create an unreserved host or resurrect
// a session on a shard during scale-down. Callers provide unique targets.
func boundedJavaHostAllocations(total int, targets []string, ceilings map[string]int) map[string]int {
	if total < 0 {
		return nil
	}
	loads := make(map[string]generatorLoadSnapshot, len(targets))
	available := 0
	for _, target := range targets {
		maximum := max(0, min(total, ceilings[target]))
		loads[target] = generatorLoadSnapshot{AvailableCapacity: int32(maximum)}
		available += min(maximum, total-available)
	}
	if available < total {
		return nil
	}
	allocation := allocateCapacityWeightedVUsers(total, targets, loads)
	remaining := total
	for _, target := range targets {
		allocation[target] = min(allocation[target], max(0, ceilings[target]))
		remaining -= allocation[target]
	}
	for _, target := range targets {
		add := min(remaining, max(0, ceilings[target]-allocation[target]))
		allocation[target] += add
		remaining -= add
	}
	return allocation
}

// A closed Java session cannot be resurrected by a rollback. On uncertain or
// partially applied scale-down, the controller stops the run instead.
func (b *distributedExecutionBackend) scaleJavaHostGroup(tr *TestRunner, test *ManagedTest, group TestGroup, shards []GeneratorShardSummary, clients map[string]generatorClient, ctx context.Context, generation, controlGeneration uint64, token []byte, target int) error {
	if len(shards) == 0 {
		return errors.New("java_host_placement_missing")
	}
	targets := make([]string, 0, len(shards))
	ceilings := map[string]int{}
	for _, shard := range shards {
		if _, duplicate := ceilings[shard.Target]; duplicate || shard.JavaHost == nil || clients[shard.Target] == nil || shard.VUsers < 0 {
			return errors.New("java_host_placement_missing")
		}
		targets = append(targets, shard.Target)
		ceilings[shard.Target] = shard.VUsers
	}
	current := 0
	for _, shard := range shards {
		current += shard.VUsers
	}
	increase := target > current
	demand := target
	if increase {
		demand = target - current
		for _, shard := range shards {
			ceilings[shard.Target] = max(0, int(shard.JavaHost.PreparedVUsers)-int(shard.JavaHost.AdmittedVUsers))
		}
	}
	allocation := boundedJavaHostAllocations(demand, targets, ceilings)
	if increase && allocation != nil {
		for _, shard := range shards {
			allocation[shard.Target] += shard.VUsers
		}
	}
	if allocation == nil {
		return errors.New("java_host_additional_ready_reservation_required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	uncertain := func(e error) error {
		return &distributedVUserUncertainError{testID: test.ID, unresolvedTargets: targets, err: e}
	}
	next := cloneGeneratorShardSummaries(shards)
	applied := false
	for i, shard := range shards {
		n := allocation[shard.Target]
		if n == shard.VUsers {
			continue
		}
		call, cancel := context.WithTimeout(ctx, b.timeout)
		result, err := clients[shard.Target].AdjustVUsers(call, generatorrpc.VUserAdjustment{TestID: test.ID, ExecutionGeneration: generation, GroupName: group.Name, TargetVUsers: int32(n), RunSupervisionToken: token})
		cancel()
		if err != nil {
			if !applied && errors.Is(err, engine.ErrInitialRampInProgress) {
				return err
			}
			return uncertain(err)
		}
		if !result.Accepted {
			err := errors.New("java_host_scale_rejected")
			if applied {
				return uncertain(err)
			}
			return err
		}
		applied = true
		next[i].VUsers = n
		next[i].JavaHost.AdmittedVUsers += int32(max(0, n-shard.VUsers))
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if !distributedControlCommitCurrentLocked(tr, test, "RUNNING", controlGeneration) {
		return uncertain(errors.New("java_host_scale_commit_stale"))
	}
	if test.remoteGroupShards == nil {
		test.remoteGroupShards = map[string][]GeneratorShardSummary{}
	}
	test.remoteGroupShards[group.Name] = next
	test.remoteGroupTargets = primaryTargetsFromShards(test.remoteGroupShards)
	test.GeneratorShards = flattenGroupShards(test.remoteGroupShards)
	return nil
}

// javaHostScheduleAdmissions prepares every fresh identity the authored schedule
// can require. A down/up cycle requires new hosts rather than recycling state.
func javaHostScheduleAdmissions(group TestGroup) (int, error) {
	if len(group.Stages) == 0 {
		return group.VUsers, nil
	}
	current := groupCurrentVUsers(group)
	total := current
	for _, stage := range group.Stages {
		if stage.TargetVUsers < 0 || stage.TargetVUsers > maxManagedTestVUsers {
			return 0, errors.New("java_host_schedule_capacity_invalid")
		}
		add := max(0, stage.TargetVUsers-current)
		if total > maxManagedTestVUsers-add {
			return 0, errors.New("java_host_schedule_admissions_exceeded")
		}
		total += add
		current = stage.TargetVUsers
	}
	return max(total, group.VUsers), nil
}

func initialJavaHostShards(group TestGroup, peak []GeneratorShardSummary) []GeneratorShardSummary {
	targets := make([]string, 0, len(peak))
	ceilings := map[string]int{}
	for _, shard := range peak {
		if _, duplicate := ceilings[shard.Target]; duplicate || shard.JavaHost == nil || shard.VUsers < 0 {
			return nil
		}
		targets = append(targets, shard.Target)
		ceilings[shard.Target] = shard.VUsers
	}
	allocation := boundedJavaHostAllocations(groupCurrentVUsers(group), targets, ceilings)
	if allocation == nil {
		return nil
	}
	out := cloneGeneratorShardSummaries(peak)
	for i := range out {
		out[i].VUsers = allocation[out[i].Target]
		out[i].JavaHost.AdmittedVUsers = int32(out[i].VUsers)
	}
	return out
}
