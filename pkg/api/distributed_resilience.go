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
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("generator target is required")
	}
	if tr == nil || test == nil {
		return nil, fmt.Errorf("test runner state is unavailable")
	}

	tr.mu.RLock()
	status := test.Status
	testName := test.Name
	groups := append([]TestGroup(nil), test.Groups...)
	shardsByGroup := cloneGroupShardMap(test.remoteGroupShards)
	clientsByTarget := cloneClientMap(test.remoteClientsByTarget)
	streamsByTarget := cloneMetricsStreamMap(test.remoteMetricsStreamsByTarget)
	states := make(map[string]string, len(test.remoteTargetStates))
	for key, value := range test.remoteTargetStates {
		states[key] = value
	}
	tr.mu.RUnlock()

	if status != "RUNNING" && status != "INITIALIZING" {
		return nil, nil
	}
	if len(shardsByGroup) == 0 {
		return nil, nil
	}
	if len(clientsByTarget) == 0 {
		return nil, fmt.Errorf("distributed remote clients are not initialized")
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
		impact.ToShards = cloneGeneratorShardSummaries(nextShards)
		nextByGroup[groupName] = nextShards
		impacts = append(impacts, impact)
	}
	if len(impacts) == 0 {
		return nil, nil
	}
	if dryRun {
		return impacts, nil
	}
	if len(refused) > 0 {
		return impacts, fmt.Errorf("generator evacuation refused: %s", strings.Join(refused, "; "))
	}

	if err := b.applyEvacuationShardAdjustments(test.ID, clientsByTarget, shardsByGroup, nextByGroup, target); err != nil {
		return impacts, err
	}

	tr.mu.Lock()
	if test.remoteGroupShards == nil {
		test.remoteGroupShards = make(map[string][]GeneratorShardSummary)
	}
	for groupName, shards := range nextByGroup {
		test.remoteGroupShards[groupName] = cloneGeneratorShardSummaries(shards)
	}
	if test.remoteTargetStates == nil {
		test.remoteTargetStates = make(map[string]string)
	}
	test.remoteTargetStates[target] = firstNonEmpty(strings.TrimSpace(reason), "evacuated")
	delete(test.remoteClientsByTarget, target)
	delete(test.remoteMetricsStreamsByTarget, target)
	delete(test.remoteMetricsBatchesByTarget, target)
	test.remoteGroupTargets = primaryTargetsFromShards(test.remoteGroupShards)
	test.GeneratorShards = flattenGroupShards(test.remoteGroupShards)
	tr.mu.Unlock()

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

func (b *distributedExecutionBackend) applyEvacuationShardAdjustments(testID string, clients map[string]generatorClient, previousByGroup map[string][]GeneratorShardSummary, nextByGroup map[string][]GeneratorShardSummary, evacuatedTarget string) error {
	for groupName, nextShards := range nextByGroup {
		previous := previousByGroup[groupName]
		if shardVUsersForTarget(previous, evacuatedTarget) <= 0 {
			continue
		}
		previousByTarget := make(map[string]int, len(previous))
		for _, shard := range previous {
			if shard.Target == evacuatedTarget {
				continue
			}
			previousByTarget[shard.Target] = shard.VUsers
		}
		successes := make([]GeneratorShardSummary, 0, len(nextShards))
		for _, shard := range nextShards {
			client := clients[shard.Target]
			if client == nil {
				return fmt.Errorf("selected generator %q is not connected", shard.Target)
			}
			ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
			adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
				TestID:       testID,
				GroupName:    groupName,
				TargetVUsers: int32(shard.VUsers),
			})
			cancel()
			if err != nil || !adjustment.Accepted {
				rollbackErr := b.rollbackShardVUsers(testID, groupName, clients, successes, previousByTarget)
				if err != nil {
					if rollbackErr != nil {
						return fmt.Errorf("evacuate generator %s: adjust %s on %s: %w; rollback shard vusers: %v", evacuatedTarget, groupName, shard.Target, err, rollbackErr)
					}
					return fmt.Errorf("evacuate generator %s: adjust %s on %s: %w", evacuatedTarget, groupName, shard.Target, err)
				}
				if rollbackErr != nil {
					return fmt.Errorf("evacuate generator %s: generator %s rejected %s adjustment; rollback shard vusers: %v", evacuatedTarget, shard.Target, groupName, rollbackErr)
				}
				return fmt.Errorf("evacuate generator %s: generator %s rejected %s adjustment", evacuatedTarget, shard.Target, groupName)
			}
			successes = append(successes, shard)
		}
	}
	return nil
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
