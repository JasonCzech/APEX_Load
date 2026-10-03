package api

import (
	"context"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const localPauseQuiescenceTimeout = 30 * time.Second

// Called under tr.mu. Admission claims are authoritative for modern runs;
// immutable phase plans cover older assignments without resource claims.
func processorPauseTargetsLocked(test *ManagedTest) (map[string]bool, error) {
	required := make(map[string]bool)
	if test == nil {
		return required, nil
	}
	shards := append([]GeneratorShardSummary(nil), test.GeneratorShards...)
	for _, groupShards := range test.remoteGroupShards {
		shards = append(shards, groupShards...)
	}
	claims := make(map[string]map[string]bool)
	assignedGroups := make(map[string]bool)
	for _, shard := range shards {
		assignedGroups[shard.Group] = true
		if remoteTargetStateIntentionallyRemoved(test.remoteTargetStates[shard.Target]) || isTerminalRemoteLifecycle(test.remoteTargetStates[shard.Target]) {
			continue
		}
		if shard.RuntimeResourceClaim != nil && !generatorrpc.ValidRuntimeResourceClaim(shard.RuntimeResourceClaim) {
			return nil, fmt.Errorf("processor pause claim for group %q on %s is invalid", shard.Group, shard.Target)
		}
		if claims[shard.Group] == nil {
			claims[shard.Group] = make(map[string]bool)
		}
		claims[shard.Group][shard.Target] = claims[shard.Group][shard.Target] || (shard.RuntimeResourceClaim != nil && shard.RuntimeResourceClaim.Requested != nil)
	}
	consuming := make(map[string]bool)
	for _, group := range test.Groups {
		if group.Protocol == "eventhubs" && group.script == nil && !group.runtimeResourcePlanKnown {
			covered := len(claims[group.Name]) > 0 || assignedGroups[group.Name]
			for _, known := range claims[group.Name] {
				covered = covered && known
			}
			if covered {
				continue
			}
			return nil, fmt.Errorf("processor pause scope for group %q is unavailable", group.Name)
		}
		plan, err := runtimeResourcePlanForGroup(group)
		if err != nil {
			return nil, fmt.Errorf("cannot establish processor pause scope for group %q", group.Name)
		}
		consuming[group.Name] = plan.RequiresReservation()
	}
	knownGroups := make(map[string]bool)
	for _, shard := range shards {
		knownGroups[shard.Group] = true
		if remoteTargetStateIntentionallyRemoved(test.remoteTargetStates[shard.Target]) || isTerminalRemoteLifecycle(test.remoteTargetStates[shard.Target]) {
			continue
		}
		claim := shard.RuntimeResourceClaim
		if claim != nil && claim.Requested != nil {
			if claim.Requested.Processors > 0 {
				required[shard.Target] = true
			}
		} else if !claims[shard.Group][shard.Target] && consuming[shard.Group] {
			required[shard.Target] = true
		}
	}
	for group, consumes := range consuming {
		if consumes && !knownGroups[group] {
			return nil, fmt.Errorf("processor group %q has no confirmed generator assignment for pause", group)
		}
	}
	return required, nil
}

func processorPauseCapabilityError(snapshot generatorLoadSnapshot) error {
	if !snapshot.HeartbeatOK {
		return fmt.Errorf("generator readiness for processor pause is unavailable")
	}
	if !protocol.RuntimeFeaturesSatisfy(snapshot.CompatibleFeatures, protocol.FeatureEventHubsProcessorPauseV1) || !protocol.RuntimeFeaturesSatisfy(snapshot.CompatibleFeatures, generatorrpc.FeatureMessagingRuntimeQuiescenceV1) {
		return fmt.Errorf("generator cannot prove processor pause; updated runtime quiescence capabilities are required")
	}
	return nil
}

// Retained terminal clients are archive transports, not control participants.
// Called under tr.mu; preserve the chosen placement order.
func activePauseTargetsLocked(test *ManagedTest, targets []string) []string {
	active := make([]string, 0, len(targets))
	for _, target := range targets {
		state := test.remoteTargetStates[target]
		if !remoteTargetStateIntentionallyRemoved(state) && !isTerminalRemoteLifecycle(state) {
			active = append(active, target)
		}
	}
	return active
}

func (tr *TestRunner) pauseTargetActive(test *ManagedTest, target string) bool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	state := test.remoteTargetStates[target]
	return !remoteTargetStateIntentionallyRemoved(state) && !isTerminalRemoteLifecycle(state)
}

// Probe only approved control-plane peers, before any participant is mutated.
// One shared deadline bounds the entire preflight independently of peer count.
func (b *distributedExecutionBackend) checkProcessorPauseCapabilities(parent context.Context, tr *TestRunner, test *ManagedTest, clients map[string]generatorClient, targets []string, required map[string]bool) error {
	timeout := b.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	for target := range required {
		if tr.pauseTargetActive(test, target) && clients[target] == nil {
			return fmt.Errorf("processor pause peer %s is unavailable", target)
		}
	}
	probed := make(map[string]bool)
	for _, target := range targets {
		if !required[target] || !tr.pauseTargetActive(test, target) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("processor pause readiness deadline: %w", err)
		}
		if err := processorPauseCapabilityError(b.probeGeneratorLoad(ctx, target, clients[target])); err != nil {
			if !tr.pauseTargetActive(test, target) {
				continue
			}
			return fmt.Errorf("processor pause on %s: %w", target, err)
		}
		probed[target] = true
	}
	for target := range required {
		if tr.pauseTargetActive(test, target) && !probed[target] {
			return fmt.Errorf("processor pause peer %s was not checked", target)
		}
	}
	return nil
}
