package api

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

type GeneratorEvacuationImpact struct {
	TestID        string                  `json:"test_id"`
	TestName      string                  `json:"test_name,omitempty"`
	Group         string                  `json:"group"`
	Protocol      string                  `json:"protocol,omitempty"`
	FromTarget    string                  `json:"from_target"`
	VUsers        int                     `json:"vusers"`
	ToShards      []GeneratorShardSummary `json:"to_shards,omitempty"`
	Refused       bool                    `json:"refused,omitempty"`
	RefusalReason string                  `json:"refusal_reason,omitempty"`
}

func assignmentToleratesGeneratorLoss(assignment *GeneratorAssignmentSettings) bool {
	return assignment != nil && assignment.TolerateGeneratorLoss
}

func groupAllowsGeneratorLossReshard(group TestGroup) (bool, string) {
	protocol := strings.ToLower(strings.TrimSpace(group.Protocol))
	switch protocol {
	case "browser", "java_vuser", "sap_gui":
		return false, fmt.Sprintf("%s VUsers are stateful and are not eligible for live generator-loss resharding", firstNonEmpty(protocol, "stateful"))
	default:
		return true, ""
	}
}

func (b *distributedExecutionBackend) EvacuateGenerator(tr *TestRunner, test *ManagedTest, target string, dryRun bool, reason string) ([]GeneratorEvacuationImpact, error) {
	b.evacuationMu.Lock()
	defer b.evacuationMu.Unlock()
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("generator target is required")
	}
	if tr == nil || test == nil {
		return nil, fmt.Errorf("test runner state is unavailable")
	}
	unlockControl := tr.lockDistributedTopologyControl(test.ID)
	defer unlockControl()

	tr.mu.RLock()
	currentTest := tr.tests[test.ID] == test
	status := test.Status
	controlGeneration := test.remoteControlGeneration
	remoteFinalizing := test.remoteFinalizing
	testName := test.Name
	testID := test.ID
	remainingDuration := test.Duration
	if test.StartedAt != nil {
		remainingDuration -= time.Since(*test.StartedAt)
	}
	if remainingDuration < time.Second {
		remainingDuration = time.Second
	}
	groups := append([]TestGroup(nil), test.Groups...)
	shardsByGroup := cloneGroupShardMap(test.remoteGroupShards)
	clientsByTarget := cloneClientMap(test.remoteClientsByTarget)
	streamsByTarget := cloneMetricsStreamMap(test.remoteMetricsStreamsByTarget)
	states := make(map[string]string, len(test.remoteTargetStates))
	for key, value := range test.remoteTargetStates {
		states[key] = value
	}
	tr.mu.RUnlock()

	if !currentTest {
		return nil, fmt.Errorf("test %s is no longer the active distributed run", test.ID)
	}
	if (status != "RUNNING" && status != "INITIALIZING") || remoteFinalizing {
		return nil, nil
	}
	if len(shardsByGroup) == 0 {
		return nil, nil
	}
	if len(clientsByTarget) == 0 {
		return nil, fmt.Errorf("distributed remote clients are not initialized")
	}
	dialedClients := b.connectEvacuationCandidates(test, target, clientsByTarget, states)
	closeDialedClients := func() {
		for candidate, client := range dialedClients {
			if stream := streamsByTarget[candidate]; stream != nil {
				_ = stream.CloseSend()
			}
			_ = client.Close()
		}
	}

	groupByName := make(map[string]TestGroup, len(groups))
	for _, group := range groups {
		groupByName[group.Name] = group
	}
	survivors := make([]string, 0, len(clientsByTarget))
	for candidate := range clientsByTarget {
		if candidate == target {
			continue
		}
		if state := states[candidate]; state == "COMPLETE" || state == "FAILED" {
			continue
		}
		survivors = append(survivors, candidate)
	}
	survivors = orderedTargetList(targetSetFromList(survivors), b.configuredTargets())

	nextByGroup := cloneGroupShardMap(shardsByGroup)
	impacts := make([]GeneratorEvacuationImpact, 0)
	var refused []string
	for groupName, shards := range shardsByGroup {
		evacuated := shardVUsersForTarget(shards, target)
		if evacuated <= 0 {
			continue
		}
		group := groupByName[groupName]
		impact := GeneratorEvacuationImpact{
			TestID:     test.ID,
			TestName:   testName,
			Group:      groupName,
			Protocol:   group.Protocol,
			FromTarget: target,
			VUsers:     evacuated,
		}
		if ok, why := groupAllowsGeneratorLossReshard(group); !ok {
			impact.Refused = true
			impact.RefusalReason = why
			refused = append(refused, fmt.Sprintf("%s: %s", groupName, why))
			impacts = append(impacts, impact)
			continue
		}
		if len(survivors) == 0 {
			impact.Refused = true
			impact.RefusalReason = "no surviving generator targets are available"
			refused = append(refused, fmt.Sprintf("%s: %s", groupName, impact.RefusalReason))
			impacts = append(impacts, impact)
			continue
		}

		total := 0
		for _, shard := range shards {
			total += shard.VUsers
		}
		loadByTarget := b.snapshotSurvivorLoad(survivors, clientsByTarget, shards)
		nextShards := capacityWeightedShardsIncludingZero(groupName, total, survivors, loadByTarget, shardStrategyForGroupShards(shards))
		nextShards = dropUnassignedZeroShards(shards, nextShards)
		impact.ToShards = cloneGeneratorShardSummaries(nextShards)
		nextByGroup[groupName] = nextShards
		impacts = append(impacts, impact)
	}
	if len(impacts) == 0 {
		closeDialedClients()
		return nil, nil
	}
	if dryRun {
		closeDialedClients()
		return impacts, nil
	}
	if len(refused) > 0 {
		closeDialedClients()
		return impacts, fmt.Errorf("generator evacuation refused: %s", strings.Join(refused, "; "))
	}

	openedStreams, newlyAssigned, rollback, err := b.applyEvacuationShardAdjustments(testID, remainingDuration, groupByName, clientsByTarget, streamsByTarget, shardsByGroup, nextByGroup, target)
	if err != nil {
		for _, stream := range openedStreams {
			_ = stream.CloseSend()
		}
		closeDialedClients()
		return impacts, err
	}

	tr.mu.Lock()
	if !distributedControlCommitCurrentLocked(tr, test, status, controlGeneration) {
		tr.mu.Unlock()
		for _, stream := range openedStreams {
			_ = stream.CloseSend()
		}
		rollbackErr := rollback()
		closeDialedClients()
		if rollbackErr != nil {
			return impacts, fmt.Errorf("generator evacuation became stale before commit; rollback: %v", rollbackErr)
		}
		return impacts, fmt.Errorf("generator evacuation became stale before commit")
	}
	if test.remoteGroupShards == nil {
		test.remoteGroupShards = make(map[string][]GeneratorShardSummary)
	}
	for groupName, shards := range nextByGroup {
		test.remoteGroupShards[groupName] = cloneGeneratorShardSummaries(shards)
	}
	if test.remoteTargetStates == nil {
		test.remoteTargetStates = make(map[string]string)
	}
	if test.remoteClientsByTarget == nil {
		test.remoteClientsByTarget = make(map[string]generatorClient)
	}
	if test.remoteMetricsStreamsByTarget == nil {
		test.remoteMetricsStreamsByTarget = make(map[string]*generatorrpc.MetricsStream)
	}
	unusedDialedClients := make([]generatorClient, 0, len(dialedClients))
	// Mark the target with a fixed evacuation sentinel, NOT the free-text reason: the
	// state is read only by remoteTargetStateDone to suppress a spurious test failure
	// when the evacuated generator's metrics stream closes. A custom operator reason
	// (e.g. "node maintenance") would not be recognized as evacuated and would fail
	// the run right after the API reported the evacuation succeeded (F13).
	test.remoteTargetStates[target] = remoteTargetStateEvacuated
	for candidate, client := range dialedClients {
		if len(newlyAssigned[candidate]) == 0 {
			unusedDialedClients = append(unusedDialedClients, client)
			continue
		}
		test.remoteClientsByTarget[candidate] = client
		test.remoteTargetStates[candidate] = "RUNNING"
	}
	for candidate, stream := range openedStreams {
		test.remoteMetricsStreamsByTarget[candidate] = stream
	}
	delete(test.remoteClientsByTarget, target)
	delete(test.remoteMetricsStreamsByTarget, target)
	delete(test.remoteMetricsBatchesByTarget, target)
	test.remoteGroupTargets = primaryTargetsFromShards(test.remoteGroupShards)
	test.GeneratorShards = flattenGroupShards(test.remoteGroupShards)
	test.remoteControlGeneration++
	runCtx := test.executionCtx
	tr.mu.Unlock()
	// Client.Close may block in transport shutdown. Never hold runner state,
	// topology, and evacuation locks while closing placement-only candidates.
	for _, client := range unusedDialedClients {
		_ = client.Close()
	}
	if runCtx == nil {
		runCtx = context.Background()
	}
	for candidate, stream := range openedStreams {
		go b.consumeMetrics(runCtx, tr, test, candidate, stream)
	}

	if stream := streamsByTarget[target]; stream != nil {
		_ = stream.CloseSend()
	}
	if client := clientsByTarget[target]; client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		_, _ = client.StopTest(ctx, test.ID, true)
		cancel()
		_ = client.Close()
	}
	return impacts, nil
}

func (b *distributedExecutionBackend) connectEvacuationCandidates(test *ManagedTest, evacuatedTarget string, clients map[string]generatorClient, states map[string]string) map[string]generatorClient {
	candidates := b.configuredTargets()
	if allowed, err := b.allowedTargetsForTest(test); err == nil {
		candidates = allowed
	}
	if groupTargets, err := b.explicitGroupTargetsForTest(test); err == nil {
		candidates = append(candidates, groupTargets...)
	}
	candidates = orderedUniqueTargets(candidates)
	dialed := make(map[string]generatorClient)
	for _, candidate := range candidates {
		if candidate == evacuatedTarget || clients[candidate] != nil || remoteTargetStateDone(states[candidate]) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		client, err := b.dial(ctx, candidate)
		if err != nil {
			cancel()
			continue
		}
		snapshot := b.probeGeneratorLoad(ctx, candidate, client)
		cancel()
		if !snapshot.HeartbeatOK || snapshot.AvailableCapacity <= 0 {
			_ = client.Close()
			continue
		}
		clients[candidate] = client
		dialed[candidate] = client
	}
	return dialed
}

func dropUnassignedZeroShards(previous []GeneratorShardSummary, next []GeneratorShardSummary) []GeneratorShardSummary {
	previousTargets := make(map[string]struct{}, len(previous))
	for _, shard := range previous {
		previousTargets[shard.Target] = struct{}{}
	}
	out := make([]GeneratorShardSummary, 0, len(next))
	for _, shard := range next {
		_, alreadyAssigned := previousTargets[shard.Target]
		if shard.VUsers == 0 && !alreadyAssigned {
			continue
		}
		out = append(out, shard)
	}
	return out
}

func targetSetFromList(targets []string) map[string]struct{} {
	out := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target != "" {
			out[target] = struct{}{}
		}
	}
	return out
}

func (b *distributedExecutionBackend) snapshotSurvivorLoad(survivors []string, clientsByTarget map[string]generatorClient, currentShards []GeneratorShardSummary) map[string]generatorLoadSnapshot {
	loadByTarget := make(map[string]generatorLoadSnapshot, len(survivors))
	for _, target := range survivors {
		snapshot := generatorLoadSnapshot{
			Target:            target,
			GeneratorID:       firstNonEmpty(b.GeneratorIDForTarget(target), target),
			AvailableCapacity: int32(^uint32(0) >> 1),
		}
		if client := clientsByTarget[target]; client != nil {
			ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
			snapshot = b.probeGeneratorLoad(ctx, target, client)
			cancel()
		}
		snapshot.AvailableCapacity += int32(shardVUsersForTarget(currentShards, target))
		loadByTarget[target] = snapshot
	}
	return loadByTarget
}

func (b *distributedExecutionBackend) applyEvacuationShardAdjustments(testID string, remainingDuration time.Duration, groups map[string]TestGroup, clients map[string]generatorClient, streams map[string]*generatorrpc.MetricsStream, previousByGroup map[string][]GeneratorShardSummary, nextByGroup map[string][]GeneratorShardSummary, evacuatedTarget string) (map[string]*generatorrpc.MetricsStream, map[string]map[string]struct{}, func() error, error) {
	type appliedChange struct {
		target        string
		group         string
		previous      int
		newlyAssigned bool
	}
	priorRunTargets := make(map[string]struct{})
	for _, shards := range previousByGroup {
		for _, shard := range shards {
			if shard.Target != evacuatedTarget {
				priorRunTargets[shard.Target] = struct{}{}
			}
		}
	}
	changes := make([]appliedChange, 0)
	openedStreams := make(map[string]*generatorrpc.MetricsStream)
	newlyAssigned := make(map[string]map[string]struct{})
	rollback := func() error {
		var rollbackErr error
		stoppedTargets := make(map[string]struct{})
		for index := len(changes) - 1; index >= 0; index-- {
			change := changes[index]
			client := clients[change.target]
			if client == nil {
				continue
			}
			if change.newlyAssigned {
				if _, wasParticipant := priorRunTargets[change.target]; !wasParticipant {
					if _, alreadyStopped := stoppedTargets[change.target]; alreadyStopped {
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
					result, err := client.StopTest(ctx, testID, false)
					cancel()
					if err != nil || !result.Acknowledged {
						rollbackErr = fmt.Errorf("stop newly provisioned test on %s", change.target)
					}
					stoppedTargets[change.target] = struct{}{}
					continue
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
			result, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
				TestID:       testID,
				GroupName:    change.group,
				TargetVUsers: int32(change.previous),
			})
			cancel()
			if err != nil || !result.Accepted {
				rollbackErr = fmt.Errorf("rollback %s on %s", change.group, change.target)
			}
		}
		return rollbackErr
	}
	fail := func(err error) (map[string]*generatorrpc.MetricsStream, map[string]map[string]struct{}, func() error, error) {
		if rollbackErr := rollback(); rollbackErr != nil {
			return openedStreams, newlyAssigned, nil, fmt.Errorf("%w; %v", err, rollbackErr)
		}
		return openedStreams, newlyAssigned, nil, err
	}

	for groupName, nextShards := range nextByGroup {
		previous := previousByGroup[groupName]
		if shardVUsersForTarget(previous, evacuatedTarget) <= 0 {
			continue
		}
		previousByTarget := make(map[string]int, len(previous))
		previousTargets := make(map[string]struct{}, len(previous))
		for _, shard := range previous {
			if shard.Target == evacuatedTarget {
				continue
			}
			previousByTarget[shard.Target] = shard.VUsers
			previousTargets[shard.Target] = struct{}{}
		}
		for _, shard := range nextShards {
			client := clients[shard.Target]
			if client == nil {
				return fail(fmt.Errorf("selected generator %q is not connected", shard.Target))
			}
			_, alreadyAssigned := previousTargets[shard.Target]
			if !alreadyAssigned {
				group, ok := groups[groupName]
				if !ok || group.script == nil {
					return fail(fmt.Errorf("group %q has no compiled script for evacuation", groupName))
				}
				scriptContent, contentHash, err := marshalDistributedScript(group.script)
				if err != nil {
					return fail(fmt.Errorf("serialize script %q for evacuation: %w", group.ScriptName, err))
				}
				ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
				_, err = client.DistributeScript(ctx, generatorrpc.Script{ID: group.ScriptID, Content: scriptContent, ContentHash: contentHash})
				cancel()
				if err != nil {
					return fail(fmt.Errorf("distribute evacuation script %q to %s: %w", group.ScriptName, shard.Target, err))
				}
				ctx, cancel = context.WithTimeout(context.Background(), b.timeout)
				// Record the attempted assignment before the RPC. A timeout can mean
				// the generator committed but its response was lost; rollback must then
				// conservatively stop/reset this candidate.
				changes = append(changes, appliedChange{target: shard.Target, group: groupName, newlyAssigned: true})
				assignment, assignErr := client.AssignWork(ctx, generatorrpc.Assignment{
					TestID:                testID,
					GroupName:             groupName,
					TargetDurationSeconds: int64(remainingDuration / time.Second),
					ScriptID:              group.ScriptID,
					VUserCount:            int32(shard.VUsers),
					RedactionRules:        cloneRedactionRules(group.redactionRules),
				})
				cancel()
				if assignErr != nil || !assignment.Accepted {
					if assignErr != nil {
						return fail(fmt.Errorf("assign evacuation shard %s on %s: %w", groupName, shard.Target, assignErr))
					}
					return fail(fmt.Errorf("generator %s rejected evacuation shard %s: %s", shard.Target, groupName, assignment.ErrorMessage))
				}
				if newlyAssigned[shard.Target] == nil {
					newlyAssigned[shard.Target] = make(map[string]struct{})
				}
				newlyAssigned[shard.Target][groupName] = struct{}{}
				if streams[shard.Target] == nil && openedStreams[shard.Target] == nil {
					stream, streamErr := b.openMetricsStream(client)
					if streamErr != nil {
						return fail(fmt.Errorf("open evacuation metrics stream on %s: %w", shard.Target, streamErr))
					}
					openedStreams[shard.Target] = stream
				}
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
			adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
				TestID:       testID,
				GroupName:    groupName,
				TargetVUsers: int32(shard.VUsers),
			})
			cancel()
			if err != nil || !adjustment.Accepted {
				if err != nil {
					return fail(fmt.Errorf("evacuate generator %s: adjust %s on %s: %w", evacuatedTarget, groupName, shard.Target, err))
				}
				return fail(fmt.Errorf("evacuate generator %s: generator %s rejected %s adjustment", evacuatedTarget, shard.Target, groupName))
			}
			changes = append(changes, appliedChange{target: shard.Target, group: groupName, previous: previousByTarget[shard.Target]})
		}
	}
	return openedStreams, newlyAssigned, rollback, nil
}

func (b *distributedExecutionBackend) tolerateMetricsStreamLoss(tr *TestRunner, test *ManagedTest, target string, err error) bool {
	tr.mu.RLock()
	tolerate := assignmentToleratesGeneratorLoss(test.Assignment)
	status := test.Status
	tr.mu.RUnlock()
	if !tolerate || (status != "RUNNING" && status != "INITIALIZING") {
		return false
	}
	impacts, evacuateErr := b.EvacuateGenerator(tr, test, target, false, "generator_loss")
	if evacuateErr != nil {
		slog.Warn("generator loss tolerance failed; falling back to run failure", "test_id", test.ID, "target", target, "err", evacuateErr)
		return false
	}
	b.broadcastFleetDegraded(tr, test, target, err, impacts)
	return true
}

func (b *distributedExecutionBackend) broadcastFleetDegraded(tr *TestRunner, test *ManagedTest, target string, streamErr error, impacts []GeneratorEvacuationImpact) {
	if tr == nil || tr.hub == nil || test == nil {
		return
	}
	tr.hub.BroadcastJSON(map[string]any{
		"type":      "fleet_event",
		"event":     "fleet_degraded",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data": map[string]any{
			"test_id":         test.ID,
			"test_name":       test.Name,
			"target":          target,
			"error":           streamErr.Error(),
			"impacted_groups": len(impacts),
		},
		"broadcast_all": true,
	})
}
