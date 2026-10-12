package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// goalThroughputCapsLocked returns the TPS each goal-managed group of a
// goal-oriented test should be held to: its current goal or traffic-profile
// target. The engine caps each group at that rate (engine.GroupThroughputCap)
// while the goal controller scales VUsers to supply it, so a run delivers its
// target in both directions instead of only being pushed up when short.
//
// A traffic-profile group has no runtime target until the profile scheduler
// applies its first point, which waits for the initial ramp and runs per
// group; until then it is held at that first point, so its seed VUsers do not
// run uncapped at the start, and a sync for another group's point does not
// suspend it in the meantime.
func goalThroughputCapsLocked(test *ManagedTest, now time.Time) map[string]float64 {
	if test == nil || test.ScenarioMode != "goal_oriented" || test.GoalConfig == nil {
		return nil
	}
	targets := currentGoalTargetStatuses(test, now)
	if len(targets) == 0 {
		return nil
	}
	var firstPoints map[string]float64
	if test.TrafficProfile != nil && test.TrafficProfile.RuntimeTarget == "tps" {
		firstPoints = make(map[string]float64, len(test.TrafficProfile.Mappings))
		for _, mapping := range test.TrafficProfile.Mappings {
			if len(mapping.Points) > 0 {
				firstPoints[normalizeEffectiveGroupName(mapping.GroupName)] = mapping.Points[0].Target
			}
		}
	}
	caps := make(map[string]float64, len(targets))
	for _, group := range test.Groups {
		if group.Role == GroupRoleObserver {
			continue
		}
		target, ok := targets[group.Name]
		if !ok {
			continue
		}
		tps := target.TargetTPS
		if first, profiled := firstPoints[normalizeEffectiveGroupName(group.Name)]; profiled {
			if _, applied := test.GoalTargets[group.Name]; !applied {
				tps = first
			}
		}
		if tps < 0 {
			tps = 0
		}
		caps[group.Name] = tps
	}
	return caps
}

// applyInitialGoalThroughputCaps seeds an engine's group configs with their
// starting caps.
func applyInitialGoalThroughputCaps(groups []engine.GroupConfig, caps map[string]float64) {
	for index := range groups {
		if tps, ok := caps[groups[index].Name]; ok {
			groups[index].ThroughputCapTPS = tps
			groups[index].ThroughputCapEnabled = true
		}
	}
}

// throughputCapExecutionBackend is implemented by backends that can move a
// running group's throughput cap (the distributed backend fans it out to the
// generators hosting the group).
type throughputCapExecutionBackend interface {
	SetGroupThroughputCaps(tr *TestRunner, test *ManagedTest, caps map[string]float64)
}

// syncGoalThroughputCaps pushes the current goal targets of a running
// goal-oriented test to its engine as throughput caps. Call it after any change
// to the test's goal targets.
//
// Syncs for one run are serialized: two target changes racing would otherwise
// let the older one reach the engine last and stick.
func (tr *TestRunner) syncGoalThroughputCaps(id string) {
	tr.syncGoalThroughputCapsWith(id, false)
}

// resyncGoalThroughputCaps forgets every share already sent and sends them
// all again. An evacuation assigns replacement shards without a cap, possibly
// on a generator that held the group before, whose recorded share would
// otherwise suppress the send. The record is cleared under the run's sync
// lock, so a sync already in flight cannot record an old share after it.
func (tr *TestRunner) resyncGoalThroughputCaps(id string) {
	tr.syncGoalThroughputCapsWith(id, true)
}

func (tr *TestRunner) syncGoalThroughputCapsWith(id string, forgetSent bool) {
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	var control *goalControllerState
	if test := tr.tests[id]; test != nil && test.ScenarioMode == "goal_oriented" {
		control = test.goalControl
	}
	tr.mu.RUnlock()
	if control == nil {
		// Only a goal run that is executing is capped.
		return
	}
	control.capSync.Lock()
	defer control.capSync.Unlock()
	if forgetSent {
		tr.mu.Lock()
		if test := tr.tests[id]; test != nil {
			test.remoteThroughputCaps = nil
		}
		tr.mu.Unlock()
	}
	tr.mu.RLock()
	test := tr.tests[id]
	if test == nil || (test.Status != "RUNNING" && test.Status != "PAUSED") {
		tr.mu.RUnlock()
		return
	}
	caps := goalThroughputCapsLocked(test, time.Now().UTC())
	multiEngine := test.multiEngine
	backend := tr.resolveBackend(test)
	tr.mu.RUnlock()
	if len(caps) == 0 {
		return
	}
	if multiEngine != nil {
		for group, tps := range caps {
			if err := multiEngine.SetGroupThroughputCap(group, tps, true); err != nil {
				slog.Warn("goal throughput cap not applied", "test_id", test.ID, "group", group, "target_tps", tps, "error", err)
			}
		}
		return
	}
	if capper, ok := backend.(throughputCapExecutionBackend); ok {
		capper.SetGroupThroughputCaps(tr, test, caps)
	}
}

// generatorAcceptsThroughputCap reports whether a generator's probed features
// include throughput_cap_v1. Only such a generator is sent a cap; another runs
// its shard of a goal group uncapped, as before.
func generatorAcceptsThroughputCap(features []string) bool {
	return stringSliceContainsEqualFold(features, generatorrpc.FeatureThroughputCapV1)
}

// throughputCapShare is one shard's share of its group's cap: the group's TPS
// in proportion to the shard's VUsers, or an even split before any shard has
// VUsers.
func throughputCapShare(groupTPS float64, shardVUsers, groupVUsers, shardCount int) float64 {
	if groupTPS <= 0 {
		return 0
	}
	if groupVUsers > 0 {
		return groupTPS * float64(max(shardVUsers, 0)) / float64(groupVUsers)
	}
	if shardCount > 0 {
		return groupTPS / float64(shardCount)
	}
	return groupTPS
}

// throughputCapClient is implemented by the production generator client; an
// in-process test adapter without it runs uncapped.
type throughputCapClient interface {
	SetThroughputCap(ctx context.Context, request generatorrpc.ThroughputCap) error
}

func throughputCapKey(target, group string) string {
	return target + "\x00" + group
}

// SetGroupThroughputCaps sends each generator hosting a goal group its share
// of the group's cap. Shares follow the shards' current VUsers, so a scale
// that moves VUsers between generators is followed by a resync. A generator
// that does not implement the call keeps its shard uncapped.
func (b *distributedExecutionBackend) SetGroupThroughputCaps(tr *TestRunner, test *ManagedTest, caps map[string]float64) {
	type shardCap struct {
		target, group string
		tps           float64
		client        generatorClient
	}
	tr.mu.RLock()
	if tr.tests[test.ID] != test || (test.Status != "RUNNING" && test.Status != "PAUSED") || test.remoteFinalizing {
		tr.mu.RUnlock()
		return
	}
	executionCtx := test.executionCtx
	executionGeneration := test.executionGeneration
	token := cloneRunSupervisionToken(test.remoteRunSupervisionToken)
	sends := make([]shardCap, 0, len(caps))
	for group, groupTPS := range caps {
		shards := test.remoteGroupShards[group]
		if len(shards) == 0 {
			if target, ok := test.remoteGroupTargets[group]; ok && target != "" {
				shards = []GeneratorShardSummary{{Group: group, Target: target, VUsers: testGroupCurrentShardVUsers(test, group)}}
			}
		}
		groupVUsers := 0
		for _, shard := range shards {
			groupVUsers += max(shard.VUsers, 0)
		}
		for _, shard := range shards {
			share := throughputCapShare(groupTPS, shard.VUsers, groupVUsers, len(shards))
			if sent, ok := test.remoteThroughputCaps[throughputCapKey(shard.Target, group)]; ok && sent == share {
				continue
			}
			client := test.remoteClientsByTarget[shard.Target]
			if client == nil && shard.Target == test.remoteTarget {
				client = test.remoteClient
			}
			if client != nil {
				sends = append(sends, shardCap{target: shard.Target, group: group, tps: share, client: client})
			}
		}
	}
	tr.mu.RUnlock()
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	for _, send := range sends {
		capper, ok := send.client.(throughputCapClient)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(executionCtx, b.timeout)
		err := capper.SetThroughputCap(ctx, generatorrpc.ThroughputCap{
			TestID:              test.ID,
			ExecutionGeneration: executionGeneration,
			RunSupervisionToken: token,
			GroupName:           send.group,
			Enabled:             true,
			TPS:                 send.tps,
		})
		cancel()
		if err != nil && status.Code(err) != codes.Unimplemented {
			slog.Warn("goal throughput cap not applied", "test_id", test.ID, "target", send.target, "group", send.group, "target_tps", send.tps, "error", err)
			continue
		}
		// An Unimplemented peer is recorded too: it cannot take a cap, and
		// asking again on every target change would only repeat the error.
		tr.mu.Lock()
		if tr.tests[test.ID] == test && test.executionGeneration == executionGeneration {
			if test.remoteThroughputCaps == nil {
				test.remoteThroughputCaps = make(map[string]float64)
			}
			test.remoteThroughputCaps[throughputCapKey(send.target, send.group)] = send.tps
		}
		tr.mu.Unlock()
	}
}
