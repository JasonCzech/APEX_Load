package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
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

// distributedEvacuationUncertainError means an evacuation mutation may still
// be live on one or more generators after compensation. The participant map is
// deliberately retained so the caller can fail closed without losing a newly
// dialed generator that has not yet been committed to the controller topology.
type distributedEvacuationUncertainError struct {
	testID             string
	participantClients map[string]generatorClient
	touchedTargets     []string
	err                error
}

func (e *distributedEvacuationUncertainError) Error() string { return e.err.Error() }
func (e *distributedEvacuationUncertainError) Unwrap() error { return e.err }

func assignmentToleratesGeneratorLoss(assignment *GeneratorAssignmentSettings) bool {
	return assignment != nil && assignment.TolerateGeneratorLoss
}

func groupAllowsGeneratorLossReshard(group TestGroup) (bool, string) {
	protocol := strings.ToLower(strings.TrimSpace(group.Protocol))
	switch protocol {
	case "browser", "java_vuser", "sap_gui":
		return false, fmt.Sprintf("%s VUsers are stateful and are not eligible for live generator-loss resharding", firstNonEmpty(protocol, "stateful"))
	default:
		// Continue with script-state checks below.
	}
	if group.script == nil {
		return true, ""
	}
	runLogic := group.script.Config.RunLogic
	if runLogic.Iterations > 0 || runLogic.GlobalIterations > 0 || runLogic.MaxTransactions > 0 {
		return false, "bounded iteration/transaction goals cannot be live-evacuated without a progress checkpoint"
	}
	for _, parameter := range group.script.Parameters {
		parameterType := dsl.CanonicalParameterType(parameter.Type)
		update := strings.ToLower(strings.TrimSpace(parameter.Update))
		if update == "" {
			update = "each_iteration"
		}
		switch parameterType {
		case "counter", "current_iteration":
			if update != "once" {
				return false, fmt.Sprintf("parameter %q advances %s state and cannot be live-evacuated without a progress checkpoint", parameter.Name, parameterType)
			}
		case "file", "list":
			fixedPerVUser := parameter.ValueForEachVUser || parameter.BlockSize == 1 || len(parameter.Values) == 1
			if update != "once" && !fixedPerVUser {
				return false, fmt.Sprintf("parameter %q advances row-selection state and cannot be live-evacuated without a progress checkpoint", parameter.Name)
			}
			if parameter.Seed != nil && update != "once" {
				return false, fmt.Sprintf("parameter %q has a replayable random sequence that cannot be live-evacuated without a progress checkpoint", parameter.Name)
			}
		}
	}
	return true, ""
}

func groupAllowsStatelessDataReshard(group TestGroup) (bool, string) {
	if group.script == nil {
		return true, ""
	}
	for _, source := range group.script.DataSources {
		if strings.EqualFold(strings.TrimSpace(source.Type), "file") {
			continue
		}
		strategy := strings.ToLower(strings.TrimSpace(source.Strategy))
		if strategy == "" {
			strategy = "sequential"
		}
		if strategy == "sequential" || strategy == "pop" {
			return false, fmt.Sprintf("data source %q uses cursor-stateful %s selection and cannot be live-evacuated without a cursor checkpoint", source.Name, strategy)
		}
	}
	return true, ""
}

func (b *distributedExecutionBackend) EvacuateGenerator(tr *TestRunner, test *ManagedTest, target string, dryRun bool, _ string) ([]GeneratorEvacuationImpact, error) {
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
	topologyLocked := true
	releaseTopology := func() {
		if topologyLocked {
			topologyLocked = false
			unlockControl()
		}
	}
	defer releaseTopology()

	tr.mu.RLock()
	currentTest := tr.tests[test.ID] == test
	status := test.Status
	controlGeneration := test.remoteControlGeneration
	remoteFinalizing := test.remoteFinalizing
	executionCtx := test.executionCtx
	remoteLifecycleCtx := test.remoteLifecycleCtx
	executionGeneration := test.executionGeneration
	testName := test.Name
	testID := test.ID
	startPaused := status == "PAUSED" || test.PausedAt != nil
	evacuationNow := time.Now()
	remainingDuration, steadyElapsed, preBaselineRemaining := distributedEvacuationTiming(test, evacuationNow)
	groups := append([]TestGroup(nil), test.Groups...)
	shardsByGroup := cloneGroupShardMap(test.remoteGroupShards)
	clientsByTarget := cloneClientMap(test.remoteClientsByTarget)
	runClientsByTarget := cloneClientMap(test.remoteClientsByTarget)
	streamsByTarget := cloneMetricsStreamMap(test.remoteMetricsStreamsByTarget)
	states := make(map[string]string, len(test.remoteTargetStates))
	for key, value := range test.remoteTargetStates {
		states[key] = value
	}
	tr.mu.RUnlock()
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	if remoteLifecycleCtx == nil {
		remoteLifecycleCtx = executionCtx
	}

	if !currentTest {
		return nil, fmt.Errorf("test %s is no longer the active distributed run", test.ID)
	}
	if (status != "RUNNING" && status != "INITIALIZING" && status != "PAUSED") || remoteFinalizing {
		return nil, nil
	}
	if len(shardsByGroup) == 0 {
		return nil, nil
	}
	if len(clientsByTarget) == 0 {
		return nil, fmt.Errorf("distributed remote clients are not initialized")
	}
	currentGeneration, executionLeaseOwnerID, claimDeadline, claimErr := tr.generatorExecutionClaim()
	if claimErr != nil || currentGeneration != executionGeneration {
		return nil, fmt.Errorf("execution leadership changed before generator evacuation")
	}
	dialedClients := b.connectEvacuationCandidates(executionCtx, test, target, clientsByTarget, states, executionGeneration, executionLeaseOwnerID, claimDeadline)
	closeDialedClients := func() {
		for candidate, client := range dialedClients {
			if stream := streamsByTarget[candidate]; stream != nil {
				_ = stream.CloseSend()
			}
			_ = client.Close()
		}
	}
	closeUntouchedDialedClients := func(touched map[string]struct{}) {
		for candidate, client := range dialedClients {
			if _, wasTouched := touched[candidate]; wasTouched {
				continue
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
	runParticipantTargets := make(map[string]struct{})
	// The original controller client set is the conservative participation
	// boundary. Shards normally name the same targets, but excluding both also
	// protects restored or partially reconciled runtimes whose client and shard
	// snapshots have not yet converged.
	for participant := range runClientsByTarget {
		if participant != target {
			runParticipantTargets[participant] = struct{}{}
		}
	}
	for _, shards := range shardsByGroup {
		for _, shard := range shards {
			if shard.Target != target {
				runParticipantTargets[shard.Target] = struct{}{}
			}
		}
	}

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
		if ok, why := groupAllowsStatelessDataReshard(group); !ok {
			impact.Refused = true
			impact.RefusalReason = why
			refused = append(refused, fmt.Sprintf("%s: %s", groupName, why))
			impacts = append(impacts, impact)
			continue
		}
		if preBaselineRemaining > 0 {
			impact.Refused = true
			impact.RefusalReason = fmt.Sprintf("replacement is unavailable until the start-delay/resource pre-baseline completes (%s remaining)", preBaselineRemaining)
			refused = append(refused, fmt.Sprintf("%s: %s", groupName, impact.RefusalReason))
			impacts = append(impacts, impact)
			continue
		}
		if remainingDuration <= 0 {
			impact.Refused = true
			impact.RefusalReason = "steady-state duration is exhausted; positive load must not be restarted during post-baseline/finalization"
			refused = append(refused, fmt.Sprintf("%s: %s", groupName, impact.RefusalReason))
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

		loadByTarget := b.snapshotSurvivorLoad(executionCtx, survivors, clientsByTarget, shards)
		var evacuatedShard GeneratorShardSummary
		for _, shard := range shards {
			if shard.Target == target {
				evacuatedShard = shard
			}
		}
		replacementTarget := ""
		bestCapacity := int32(-1)
		for _, candidate := range survivors {
			if _, alreadyParticipates := runParticipantTargets[candidate]; alreadyParticipates {
				continue
			}
			snapshot := loadByTarget[candidate]
			if !snapshot.HeartbeatOK || snapshot.AvailableCapacity < int32(evacuated) {
				continue
			}
			if replacementTarget == "" || snapshot.AvailableCapacity > bestCapacity {
				replacementTarget = candidate
				bestCapacity = snapshot.AvailableCapacity
			}
		}
		if replacementTarget == "" {
			impact.Refused = true
			impact.RefusalReason = "no healthy generator with sufficient capacity is available that does not already participate in this run"
			refused = append(refused, fmt.Sprintf("%s: %s", groupName, impact.RefusalReason))
			impacts = append(impacts, impact)
			continue
		}
		nextShards := make([]GeneratorShardSummary, 0, len(shards))
		for _, shard := range shards {
			if shard.Target != target {
				nextShards = append(nextShards, shard)
			}
		}
		evacuatedShard.Target = replacementTarget
		nextShards = append(nextShards, evacuatedShard)
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

	openedStreams, newlyAssigned, touchedTargets, rollback, err := b.applyEvacuationShardAdjustments(executionCtx, remoteLifecycleCtx, test, testID, executionGeneration, remainingDuration, steadyElapsed, startPaused, groupByName, clientsByTarget, streamsByTarget, shardsByGroup, nextByGroup, target)
	if err != nil {
		var uncertain *distributedEvacuationUncertainError
		if errors.As(err, &uncertain) {
			uncertainTouched := targetSetFromList(uncertain.touchedTargets)
			uncertain.participantClients = evacuationParticipantClients(runClientsByTarget, dialedClients, uncertainTouched)
			closeUntouchedDialedClients(uncertainTouched)
			return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
		}
		for _, stream := range openedStreams {
			_ = stream.CloseSend()
		}
		closeDialedClients()
		return impacts, err
	}

	// Mark the source before issuing StopTest. Generator StopTest broadcasts a
	// terminal lifecycle batch before returning its ACK, and its stream can close
	// in the same window. The transitional state makes both signals intentional
	// removal rather than whole-run completion/failure. If another lifecycle
	// operation already won, the source is still live and the staged replacement
	// can be cleanly rolled back.
	tr.mu.Lock()
	if !distributedControlCommitCurrentLocked(tr, test, status, controlGeneration) {
		tr.mu.Unlock()
		for _, stream := range openedStreams {
			_ = stream.CloseSend()
		}
		rollbackErr := rollback()
		if rollbackErr != nil {
			uncertain := &distributedEvacuationUncertainError{
				testID:             testID,
				participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
				touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
				err:                fmt.Errorf("evacuation became stale before source fence and rollback was uncertain: %w", rollbackErr),
			}
			closeUntouchedDialedClients(touchedTargets)
			return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
		}
		closeDialedClients()
		return impacts, fmt.Errorf("generator evacuation became stale before the source fence")
	}
	if test.remoteTargetStates == nil {
		test.remoteTargetStates = make(map[string]string)
	}
	test.remoteTargetStates[target] = remoteTargetStateEvacuating
	tr.mu.Unlock()

	// The source must be positively fenced before its replacement topology is
	// advertised. A timeout is ambiguous: the stop may have applied, so neither
	// rolling back nor committing the reshard can prove the active VUser count.
	if source := clientsByTarget[target]; source != nil {
		if err := b.stopEvacuationClient(testID, executionGeneration, source); err != nil {
			uncertain := &distributedEvacuationUncertainError{
				testID:             testID,
				participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
				touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
				err:                fmt.Errorf("stop evacuated source %s: %w", target, err),
			}
			closeUntouchedDialedClients(touchedTargets)
			return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
		}
	} else {
		uncertain := &distributedEvacuationUncertainError{
			testID:             testID,
			participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
			touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
			err:                fmt.Errorf("evacuated source %s is not connected and cannot be fenced", target),
		}
		closeUntouchedDialedClients(touchedTargets)
		return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
	}

	// AssignWork above is a staging boundary: replacement VUsers must remain
	// inert until the source's exact-generation StopTest has positively fenced
	// the old logical shards. Only then may the same shard coordinates and data
	// partitions be released on their replacement generators.
	replacementClients := make(map[string]generatorClient, len(newlyAssigned))
	for replacementTarget := range newlyAssigned {
		if client := clientsByTarget[replacementTarget]; client != nil {
			replacementClients[replacementTarget] = client
		}
	}
	replacementStartAt := time.Now().Add(distributedCommitStartLead(b.timeout))
	if err := b.commitDistributedWork(executionCtx, testID, executionGeneration, replacementStartAt, replacementClients); err != nil {
		uncertain := &distributedEvacuationUncertainError{
			testID:             testID,
			participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
			touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
			err:                fmt.Errorf("commit replacement shards after source fence: %w", err),
		}
		closeUntouchedDialedClients(touchedTargets)
		return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
	}
	tr.mu.Lock()
	if !distributedControlCommitCurrentLocked(tr, test, status, controlGeneration) {
		tr.mu.Unlock()
		// The source has already acknowledged its stop. Restoring only the
		// replacement shards would leave the run under-provisioned, while a lost
		// acknowledgement on any later cleanup could leave it over-provisioned.
		// Stop the whole exact-generation fleet instead.
		uncertain := &distributedEvacuationUncertainError{
			testID:             testID,
			participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
			touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
			err:                fmt.Errorf("generator evacuation became stale after the source stopped"),
		}
		closeUntouchedDialedClients(touchedTargets)
		return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
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
	if sourceClient := clientsByTarget[target]; sourceClient != nil {
		if test.remoteArchiveClientsByTarget == nil {
			test.remoteArchiveClientsByTarget = make(map[string]generatorClient)
		}
		// StopTest above finalized this source's partial archive. Preserve only
		// its transport for the eventual evidence merge; keeping it out of
		// remoteClientsByTarget prevents pause/duration/stop controls from being
		// fanned back to a deliberately retired assignment.
		test.remoteArchiveClientsByTarget[target] = sourceClient
	}
	delete(test.remoteClientsByTarget, target)
	delete(test.remoteMetricsStreamsByTarget, target)
	delete(test.remoteMetricsBatchesByTarget, target)
	test.remoteGroupTargets = primaryTargetsFromShards(test.remoteGroupShards)
	test.GeneratorShards = flattenGroupShards(test.remoteGroupShards)
	test.remoteControlGeneration++
	// Use the normalized lifecycle snapshot captured at the start of the
	// operation. Restored/legacy runs may not yet have remoteLifecycleCtx stored
	// on the managed test, but their execution context must still own the newly
	// opened stream and its consumer.
	runCtx := remoteLifecycleCtx
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
	return impacts, nil
}

// distributedEvacuationTiming separates controller wall time into the
// pre-load window and active steady-state time. StartedAt precedes configured
// start delay/resource baseline, and completed or current pauses must not
// consume the steady-state target. A non-positive remaining duration means the
// run is in post-baseline/finalization and must not be restarted on a replacement.
func distributedEvacuationTiming(test *ManagedTest, now time.Time) (remaining, steadyElapsed, preBaselineRemaining time.Duration) {
	if test == nil {
		return 0, 0, 0
	}
	preBaseline, _ := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	preBaseline += testexec.StartDelayWindow(test.StartDelay)
	remaining = max(test.Duration, time.Duration(0))
	if test.StartedAt == nil || test.StartedAt.IsZero() {
		return remaining, 0, preBaseline
	}

	wallElapsed := max(now.Sub(*test.StartedAt), time.Duration(0))
	preBaselineRemaining = max(preBaseline-wallElapsed, time.Duration(0))
	pauseElapsed := max(test.CumulativePause, time.Duration(0))
	if test.PausedAt != nil && !test.PausedAt.IsZero() {
		pauseElapsed += max(now.Sub(*test.PausedAt), time.Duration(0))
	}
	steadyElapsed = max(wallElapsed-preBaseline-pauseElapsed, time.Duration(0))
	remaining = max(test.Duration-steadyElapsed, time.Duration(0))
	return remaining, steadyElapsed, preBaselineRemaining
}

func (b *distributedExecutionBackend) connectEvacuationCandidates(parent context.Context, test *ManagedTest, evacuatedTarget string, clients map[string]generatorClient, states map[string]string, executionGeneration uint64, executionLeaseOwnerID string, claimDeadline time.Time) map[string]generatorClient {
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
		ctx, cancel := context.WithTimeout(parent, b.timeout)
		client, err := b.dial(ctx, candidate)
		if err != nil {
			cancel()
			continue
		}
		if err := claimGeneratorExecutionEpoch(ctx, client, executionGeneration, executionLeaseOwnerID, claimDeadline, b.timeout); err != nil {
			cancel()
			_ = client.Close()
			continue
		}
		snapshot := b.probeGeneratorLoad(ctx, candidate, client)
		cancel()
		if !generatorSupportsEvacuationAssignmentState(snapshot) || snapshot.AvailableCapacity <= 0 {
			_ = client.Close()
			continue
		}
		clients[candidate] = client
		dialed[candidate] = client
	}
	return dialed
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

func (b *distributedExecutionBackend) snapshotSurvivorLoad(parent context.Context, survivors []string, clientsByTarget map[string]generatorClient, currentShards []GeneratorShardSummary) map[string]generatorLoadSnapshot {
	loadByTarget := make(map[string]generatorLoadSnapshot, len(survivors))
	for _, target := range survivors {
		snapshot := generatorLoadSnapshot{
			Target:            target,
			GeneratorID:       firstNonEmpty(b.GeneratorIDForTarget(target), target),
			AvailableCapacity: int32(^uint32(0) >> 1),
		}
		if client := clientsByTarget[target]; client != nil {
			ctx, cancel := context.WithTimeout(parent, b.timeout)
			snapshot = b.probeGeneratorLoad(ctx, target, client)
			cancel()
		}
		if !generatorSupportsEvacuationAssignmentState(snapshot) {
			// Unknown protobuf fields are intentionally ignored. Treat a generator
			// without the explicit capability as ineligible instead of assuming it
			// honored the paused-state and logical-duration continuation fields.
			snapshot.HeartbeatOK = false
			snapshot.AvailableCapacity = 0
		}
		snapshot.AvailableCapacity += int32(shardVUsersForTarget(currentShards, target))
		loadByTarget[target] = snapshot
	}
	return loadByTarget
}

func generatorSupportsEvacuationAssignmentState(snapshot generatorLoadSnapshot) bool {
	return snapshot.HeartbeatOK && stringSliceContainsEqualFold(snapshot.CompatibleFeatures, generatorrpc.FeatureEvacuationAssignmentStateV1)
}

func (b *distributedExecutionBackend) applyEvacuationShardAdjustments(parent context.Context, metricsParent context.Context, managedTest *ManagedTest, testID string, executionGeneration uint64, remainingDuration time.Duration, steadyElapsed time.Duration, startPaused bool, groups map[string]TestGroup, clients map[string]generatorClient, streams map[string]*generatorrpc.MetricsStream, previousByGroup map[string][]GeneratorShardSummary, nextByGroup map[string][]GeneratorShardSummary, evacuatedTarget string) (map[string]*generatorrpc.MetricsStream, map[string]map[string]struct{}, map[string]struct{}, func() error, error) {
	if parent == nil {
		parent = context.Background()
	}
	_, postBaseline := testexec.ResourceBaselineWindows(managedTest != nil && managedTest.ResourceBaseline)
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
	touchedTargets := make(map[string]struct{})
	rollback := func() error {
		// A concurrent StopTest cancels the run context before this rollback can
		// observe that its topology commit lost. Cleanup must still remove an
		// ambiguously committed replacement assignment. Detach cancellation but
		// keep every exact-generation cleanup RPC bounded by b.timeout.
		var rollbackErrs []error
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
					if err := b.stopEvacuationClient(testID, executionGeneration, client); err != nil {
						rollbackErrs = append(rollbackErrs, fmt.Errorf("stop newly provisioned test on %s: %w", change.target, err))
					}
					stoppedTargets[change.target] = struct{}{}
					continue
				}
			}
			if err := b.restoreEvacuationVUsers(testID, executionGeneration, change.group, change.previous, client); err != nil {
				rollbackErrs = append(rollbackErrs, fmt.Errorf("rollback %s on %s: %w", change.group, change.target, err))
			}
		}
		return errors.Join(rollbackErrs...)
	}
	fail := func(err error) (map[string]*generatorrpc.MetricsStream, map[string]map[string]struct{}, map[string]struct{}, func() error, error) {
		if rollbackErr := rollback(); rollbackErr != nil {
			targets := orderedTargetList(touchedTargets, b.configuredTargets())
			return openedStreams, newlyAssigned, touchedTargets, nil, &distributedEvacuationUncertainError{
				testID:         testID,
				touchedTargets: targets,
				err:            errors.Join(err, fmt.Errorf("evacuation rollback remained uncertain: %w", rollbackErr)),
			}
		}
		return openedStreams, newlyAssigned, touchedTargets, nil, err
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
			if alreadyAssigned && previousByTarget[shard.Target] == shard.VUsers {
				continue
			}
			if !alreadyAssigned {
				group, ok := groups[groupName]
				if !ok || group.script == nil {
					return fail(fmt.Errorf("group %q has no compiled script for evacuation", groupName))
				}
				if _, ok := client.(coordinatedWorkCommitClient); !ok {
					return fail(fmt.Errorf("generator %q does not support staged evacuation commit", shard.Target))
				}
				scriptContent, contentHash, dataFiles, err := generatorrpc.PrepareDistributedScript(group.script)
				if err != nil {
					return fail(fmt.Errorf("stage script %q for evacuation: %w", group.ScriptName, err))
				}
				rampUp, rampDown, initStrategy := distributedGroupRuntimeConfig(managedTest, group)
				rampUp = distributedEvacuationRemainingRamp(rampUp, steadyElapsed)
				ctx, cancel := context.WithTimeout(parent, b.timeout)
				_, err = client.DistributeScript(ctx, generatorrpc.Script{ID: group.ScriptID, ExecutionGeneration: executionGeneration, Content: scriptContent, ContentHash: contentHash})
				cancel()
				if err != nil {
					return fail(fmt.Errorf("distribute evacuation script %q to %s: %w", group.ScriptName, shard.Target, err))
				}
				ctx, cancel = context.WithTimeout(parent, b.timeout)
				// Record the attempted assignment before the RPC. A timeout can mean
				// the generator committed but its response was lost; rollback must then
				// conservatively stop/reset this candidate.
				changes = append(changes, appliedChange{target: shard.Target, group: groupName, newlyAssigned: true})
				touchedTargets[shard.Target] = struct{}{}
				assignment, assignErr := client.AssignWork(ctx, generatorrpc.Assignment{
					TestID:                testID,
					ExecutionGeneration:   executionGeneration,
					GroupName:             groupName,
					TargetDuration:        remainingDuration,
					TargetDurationSeconds: distributedCompatibilityDurationSeconds(remainingDuration),
					DurationOffset:        steadyElapsed,
					ScriptID:              group.ScriptID,
					ExpectedScriptHash:    contentHash,
					VUserCount:            int32(shard.VUsers),
					RampUp:                rampUp,
					RampDown:              rampDown,
					InitStrategy:          initStrategy,
					PostBaseline:          postBaseline,
					ShardOrdinal:          shard.ShardOrdinal,
					ShardCount:            shard.ShardCount,
					VUserIDOffset:         shard.VUserIDOffset,
					StartPaused:           startPaused,
					DataFiles:             dataFiles,
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
				continue
			}
			// One-for-one evacuation must never resize a surviving logical shard.
			// Such an adjustment would begin executing before the source is fenced
			// and could duplicate VUser IDs and partitioned data consumption.
			return fail(fmt.Errorf("evacuation topology attempted an in-place adjustment for group %s on %s", groupName, shard.Target))
		}
	}
	stagedStreams, streamErr := b.openEvacuationMetricsStreams(metricsParent, testID, executionGeneration, newlyAssigned, clients, streams)
	for target, stream := range stagedStreams {
		openedStreams[target] = stream
	}
	if streamErr != nil {
		for _, stream := range openedStreams {
			_ = stream.CloseSend()
		}
		return fail(fmt.Errorf("open staged replacement metrics streams: %w", streamErr))
	}
	return openedStreams, newlyAssigned, touchedTargets, rollback, nil
}

func (b *distributedExecutionBackend) openEvacuationMetricsStreams(parent context.Context, testID string, executionGeneration uint64, newlyAssigned map[string]map[string]struct{}, clients map[string]generatorClient, existing map[string]*generatorrpc.MetricsStream) (map[string]*generatorrpc.MetricsStream, error) {
	if parent == nil {
		parent = context.Background()
	}
	targets := make([]string, 0, len(newlyAssigned))
	for target := range newlyAssigned {
		if existing[target] == nil {
			targets = append(targets, target)
		}
	}
	targets = orderedTargetList(targetSetFromList(targets), b.configuredTargets())
	opened := make(map[string]*generatorrpc.MetricsStream, len(targets))
	if len(targets) == 0 {
		return opened, nil
	}
	// Replacement streams must be attached before the coordinated commit
	// barrier releases load. Keep this as a setup-only timeout: the stream itself
	// continues to inherit parent for the run's full lifecycle.
	setupWindow := min(b.timeout, 200*time.Millisecond)
	if setupWindow <= 0 {
		setupWindow = 200 * time.Millisecond
	}
	type result struct {
		target string
		stream *generatorrpc.MetricsStream
		err    error
	}
	results := make(chan result, len(targets))
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := clients[target]
			if client == nil {
				results <- result{target: target, err: fmt.Errorf("replacement client is unavailable")}
				return
			}
			stream, err := b.openMetricsStreamWithSetupTimeout(parent, client, testID, executionGeneration, setupWindow)
			results <- result{target: target, stream: stream, err: err}
		}()
	}
	wg.Wait()
	close(results)
	var errs []error
	for result := range results {
		if result.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", result.target, result.err))
			continue
		}
		opened[result.target] = result.stream
	}
	return opened, errors.Join(errs...)
}

func (b *distributedExecutionBackend) restoreEvacuationVUsers(testID string, executionGeneration uint64, group string, previous int, client generatorClient) error {
	var lastErr error
	for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		result, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
			TestID:              testID,
			ExecutionGeneration: executionGeneration,
			GroupName:           group,
			TargetVUsers:        int32(previous),
		})
		cancel()
		switch {
		case err != nil:
			lastErr = err
		case !result.Accepted:
			lastErr = fmt.Errorf("generator rejected VUser rollback")
		case result.CurrentVUsers != int32(previous):
			lastErr = fmt.Errorf("generator reported %d VUsers after rollback, want %d", result.CurrentVUsers, previous)
		default:
			return nil
		}
		if attempt+1 < durableTransitionAttempts {
			time.Sleep(durableTransitionRetryDelay)
		}
	}
	return lastErr
}

func (b *distributedExecutionBackend) stopEvacuationClient(testID string, executionGeneration uint64, client generatorClient) error {
	if client == nil {
		return fmt.Errorf("generator client is unavailable")
	}
	var lastErr error
	for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		result, err := stopGeneratorTest(ctx, client, testID, false, executionGeneration)
		cancel()
		switch {
		case err != nil:
			lastErr = err
		case !result.Acknowledged:
			lastErr = fmt.Errorf("generator did not acknowledge exact-generation stop")
		default:
			return nil
		}
		if attempt+1 < durableTransitionAttempts {
			time.Sleep(durableTransitionRetryDelay)
		}
	}
	return lastErr
}

func evacuationParticipantClients(runClients, dialedClients map[string]generatorClient, touchedTargets map[string]struct{}) map[string]generatorClient {
	participants := cloneClientMap(runClients)
	for target := range touchedTargets {
		if client := dialedClients[target]; client != nil {
			participants[target] = client
		}
	}
	return participants
}

func (b *distributedExecutionBackend) stopEvacuationParticipants(testID string, executionGeneration uint64, participants map[string]generatorClient) error {
	var stopErrs []error
	for _, target := range orderedGeneratorTargets(participants, b.configuredTargets()) {
		if err := b.stopEvacuationClient(testID, executionGeneration, participants[target]); err != nil {
			stopErrs = append(stopErrs, fmt.Errorf("stop evacuation participant %s: %w", target, err))
		}
	}
	return errors.Join(stopErrs...)
}

// failClosedUncertainEvacuation keeps every possibly-live generator attached to
// the run until an exact-generation stop is acknowledged. Persistence failure
// never suppresses the cleanup attempt; that is the failure mode this path is
// designed to contain.
func (b *distributedExecutionBackend) failClosedUncertainEvacuation(tr *TestRunner, test *ManagedTest, uncertain *distributedEvacuationUncertainError, existingStreams, openedStreams map[string]*generatorrpc.MetricsStream) error {
	return b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, existingStreams, openedStreams, nil)
}

// failClosedUncertainEvacuationWithTopologyRelease is entered while evacuation
// owns the topology write gate. It persists STOPPING first, then releases that
// gate before taking the archive fence. This preserves the repository-wide
// archive-fence -> topology-gate order used by closeRemoteResources.
func (b *distributedExecutionBackend) failClosedUncertainEvacuationWithTopologyRelease(tr *TestRunner, test *ManagedTest, uncertain *distributedEvacuationUncertainError, existingStreams, openedStreams map[string]*generatorrpc.MetricsStream, releaseTopology func()) error {
	if uncertain == nil {
		return fmt.Errorf("uncertain evacuation state is unavailable")
	}
	participants := cloneClientMap(uncertain.participantClients)
	allStreams := cloneMetricsStreamMap(existingStreams)
	if allStreams == nil && len(openedStreams) > 0 {
		allStreams = make(map[string]*generatorrpc.MetricsStream, len(openedStreams))
	}
	for target, stream := range openedStreams {
		allStreams[target] = stream
	}

	tr.mu.Lock()
	owned := tr.tests[test.ID] == test
	active := owned && !isTerminalTestStatus(test.Status)
	var persistErr error
	var cancel context.CancelFunc
	if active {
		test.Status = "STOPPING"
		test.Error = "generator evacuation state is uncertain: " + uncertain.Error()
		test.remoteFinalizing = true
		if test.remoteClientsByTarget == nil {
			test.remoteClientsByTarget = make(map[string]generatorClient)
		}
		for target, client := range participants {
			test.remoteClientsByTarget[target] = client
			if test.remoteTargetStates == nil {
				test.remoteTargetStates = make(map[string]string)
			}
			test.remoteTargetStates[target] = "STOPPING"
		}
		if test.remoteMetricsStreamsByTarget == nil {
			test.remoteMetricsStreamsByTarget = make(map[string]*generatorrpc.MetricsStream)
		}
		for target, stream := range openedStreams {
			test.remoteMetricsStreamsByTarget[target] = stream
		}
		test.remoteControlGeneration++
		persistErr = retryDurableTransition(func() error { return tr.saveManagedTestRecord(test) })
		tr.cancelAllVUserScaleRampsLocked(test)
		cancel = test.cancel
	}
	tr.mu.Unlock()
	if releaseTopology != nil {
		releaseTopology()
	}

	stopErr := b.stopEvacuationParticipants(test.ID, test.executionGeneration, participants)
	if cancel != nil {
		cancel()
	}
	if active {
		tr.broadcastEvent("test_stopping", test)
	}
	if stopErr == nil {
		if active {
			finalizeErr := b.finalizeFailClosedEvacuation(tr, test)
			return errors.Join(uncertain, wrapEvacuationPersistenceError(persistErr), finalizeErr)
		}
		b.detachEvacuationResourcesUnderTopologyControl(tr, test, participants, allStreams)
		return uncertain
	}

	// Retain all clients in the STOPPING topology and keep retrying with fresh
	// contexts. Generator epoch expiry is an additional fence, not a substitute
	// for explicit cleanup acknowledgement.
	if owned {
		tr.enqueueTerminalRetry("evacuation:"+test.ID, "uncertain evacuation cleanup", func() error {
			unlock := tr.lockDistributedTopologyControl(test.ID)
			if err := b.stopEvacuationParticipants(test.ID, test.executionGeneration, participants); err != nil {
				unlock()
				return err
			}
			unlock()
			if active {
				return b.finalizeFailClosedEvacuation(tr, test)
			}
			b.detachEvacuationResourcesUnderTopologyControl(tr, test, participants, allStreams)
			return nil
		})
	}
	return errors.Join(uncertain, wrapEvacuationPersistenceError(persistErr), stopErr)
}

func (b *distributedExecutionBackend) finalizeFailClosedEvacuation(tr *TestRunner, test *ManagedTest) error {
	completedAt := time.Now()
	finalError := test.Error
	unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
	defer unlockArchiveCommit()

	// Commit the authoritative failure before potentially slow best-effort
	// evidence transfer, matching the ordinary distributed failure path.
	completionErr := tr.completeTestRunUnderArchiveFence(test, "FAILED", completedAt, finalError, nil)
	archiveRecord, archiveErr := b.stageRemoteArchive(tr, test, "FAILED", finalError, completedAt, false)
	// Clients remain attached until staging has snapshotted every stopped
	// participant. Closing after the fetch prevents partial-run evidence loss.
	b.closeRemoteResources(tr, test)
	if archiveRecord != nil {
		tr.persistAndAttachRunArchiveUnderFence(test, archiveRecord)
	}
	if archiveErr != nil {
		archiveErr = fmt.Errorf("collect fail-closed evacuation archive: %w", archiveErr)
	}
	return errors.Join(completionErr, archiveErr)
}

func (b *distributedExecutionBackend) detachEvacuationResourcesUnderTopologyControl(tr *TestRunner, test *ManagedTest, participants map[string]generatorClient, streams map[string]*generatorrpc.MetricsStream) {
	unlock := tr.lockDistributedTopologyControl(test.ID)
	b.detachEvacuationResources(tr, test, participants, streams)
	unlock()
}

func wrapEvacuationPersistenceError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("persist fail-closed evacuation state: %w", err)
}

func (b *distributedExecutionBackend) detachEvacuationResources(tr *TestRunner, test *ManagedTest, participants map[string]generatorClient, streams map[string]*generatorrpc.MetricsStream) {
	var archivedClients map[string]generatorClient
	tr.mu.Lock()
	if tr.tests[test.ID] == test {
		for target, stream := range test.remoteMetricsStreamsByTarget {
			if streams[target] == nil {
				streams[target] = stream
			}
		}
		for target, client := range test.remoteClientsByTarget {
			if participants[target] == nil {
				participants[target] = client
			}
		}
		test.remoteClient = nil
		test.remoteClientsByTarget = nil
		archivedClients = test.remoteArchiveClientsByTarget
		test.remoteArchiveClientsByTarget = nil
		test.remoteMetricsStream = nil
		test.remoteMetricsStreamsByTarget = nil
		test.remoteGroupTargets = nil
		test.remoteGroupShards = nil
		test.remoteMetricsBatchesByTarget = nil
		test.remoteTargetStates = nil
		test.remoteActivated = false
		test.remoteActivationPending = false
		test.remoteControlGeneration++
	}
	tr.mu.Unlock()
	for _, stream := range streams {
		if stream != nil {
			_ = stream.CloseSend()
		}
	}
	for _, client := range participants {
		if client != nil {
			_ = client.Close()
		}
	}
	for _, client := range archivedClients {
		if client != nil {
			_ = client.Close()
		}
	}
}

func (b *distributedExecutionBackend) tolerateMetricsStreamLoss(tr *TestRunner, test *ManagedTest, target string, err error) bool {
	tr.mu.RLock()
	tolerate := assignmentToleratesGeneratorLoss(test.Assignment)
	status := test.Status
	intentionalRemoval := remoteTargetStateIntentionallyRemoved(test.remoteTargetStates[target])
	tr.mu.RUnlock()
	if intentionalRemoval {
		return true
	}
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
