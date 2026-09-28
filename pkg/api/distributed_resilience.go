package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
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
	// unreachableTargets names participants that are already known to be gone
	// (the stream-loss source). They can never acknowledge an exact-generation
	// stop, so fail-closed cleanup must not hold the run in STOPPING waiting for
	// one; their generator-side execution lease expires on its own and force-
	// stops the assignment. Only populated for loss-tolerant runs, which are the
	// only ones that hold such a lease.
	unreachableTargets []string
	err                error
}

func (e *distributedEvacuationUncertainError) Error() string { return e.err.Error() }
func (e *distributedEvacuationUncertainError) Unwrap() error { return e.err }

func assignmentToleratesGeneratorLoss(assignment *GeneratorAssignmentSettings) bool {
	return assignment != nil && assignment.TolerateGeneratorLoss
}

func groupAllowsGeneratorLossReshard(group TestGroup) (bool, string) {
	if dsl.RequiresJMeterMessaging(group.script) || (group.Assignment != nil && group.Assignment.SourceAffinity == "single_generator") {
		return false, "JMeter source state cannot be restarted or replayed after generator loss"
	}
	if engine.HasAllocatedParameters(group.script) {
		return false, "unique parameter block progress cannot be recovered on a replacement generator"
	}
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
	return b.evacuateGenerator(tr, test, target, dryRun, false)
}

// evacuateGenerator's allowLostSourceLeaseFence bit is intentionally private:
// the public API's free-text reason must never grant authority to treat lease
// expiry as proof that an unreachable source stopped. Only the internal stream-
// loss path may opt into that recovery after establishing a per-test fence.
func (b *distributedExecutionBackend) evacuateGenerator(tr *TestRunner, test *ManagedTest, target string, dryRun bool, allowLostSourceLeaseFence bool) ([]GeneratorEvacuationImpact, error) {
	b.evacuationMu.Lock()
	defer b.evacuationMu.Unlock()
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("generator target is required")
	}
	if tr == nil || test == nil {
		return nil, fmt.Errorf("test runner state is unavailable")
	}
	if test.TerminationMode == "iterations" {
		return nil, fmt.Errorf("iteration work is incomplete after generator loss; replay or evacuation requires a progress checkpoint and is unsupported")
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
	tr.podCapacityMutationMu.Lock()
	defer tr.podCapacityMutationMu.Unlock()

	tr.mu.RLock()
	currentTest := tr.tests[test.ID] == test
	status := test.Status
	controlGeneration := test.remoteControlGeneration
	remoteFinalizing := test.remoteFinalizing
	executionCtx := test.executionCtx
	remoteLifecycleCtx := test.remoteLifecycleCtx
	executionGeneration := test.executionGeneration
	runSupervisionToken := cloneRunSupervisionToken(test.remoteRunSupervisionToken)
	requireTestExecutionLease := assignmentToleratesGeneratorLoss(test.Assignment)
	sourceSupervisionTTL := continuitySupervisionFenceTTL(b.runSupervisionTTL, tr.runAdoptionGrace(test))
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
	defer clear(runSupervisionToken)
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	if remoteLifecycleCtx == nil {
		remoteLifecycleCtx = executionCtx
	}
	// The stream-loss source is presumed gone: the same transport that lost its
	// metrics stream also carries the cleanup stop. Record it so any fail-closed
	// exit can terminalize on the surviving participants instead of retrying a
	// stop the source can never acknowledge.
	var unreachableTargets []string
	if allowLostSourceLeaseFence && requireTestExecutionLease {
		unreachableTargets = []string{target}
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
	standbys, standbyErr := tr.standbyEvacuationReservations(testID, executionGeneration)
	if standbyErr != nil {
		return nil, fmt.Errorf("read standby capacity before evacuation: %w", standbyErr)
	}
	for candidate, standby := range standbys {
		proof := standby.RecoveryPlacement
		if (recoverySpareVUsers(test) > 0 && proof == nil) || (proof != nil && (!slices.Contains(proof.ReplacementSources, target) || !tr.recoveryStandbyIdentityCurrent(standby))) {
			delete(standbys, candidate)
		}
	}
	dialedClients := b.connectEvacuationCandidates(executionCtx, test, target, clientsByTarget, states, executionGeneration, executionLeaseOwnerID, claimDeadline, standbys)
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
			if recoverySpareVUsers(test) > 0 && standbys[candidate].RecoveryPlacement == nil {
				continue
			}
			if _, alreadyParticipates := runParticipantTargets[candidate]; alreadyParticipates {
				continue
			}
			snapshot := standbyEvacuationLoad(loadByTarget[candidate], standbys[candidate], testID, executionGeneration)
			if !snapshot.HeartbeatOK || snapshot.AvailableCapacity < int32(evacuated) {
				continue
			}
			if groupRequiresJavaVuserPhaseLifecycle(group) &&
				(!stringSliceContainsEqualFold(snapshot.SupportedProtocols, "java_vuser") ||
					!protocol.RuntimeFeaturesSatisfy(snapshot.CompatibleFeatures, protocol.FeatureJavaVuserPersistentLifecycleV1)) {
				continue
			}
			if !runtimeCapacityAllowsGroup(group, snapshot, evacuated) {
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
	podClaims, podErr := tr.reservePodEvacuation(executionCtx, b, testID, target, nextByGroup, clientsByTarget, dryRun, standbys)
	podCommitted := false
	defer func() {
		if !podCommitted && len(podClaims) > 0 {
			if err := tr.rollbackEvacuationPodClaims(b, podClaims, standbys); err != nil {
				slog.Warn("evacuation capacity rollback remains reserved", "test_id", testID, "error", err)
			}
		}
	}()
	if podErr != nil {
		closeDialedClients()
		return impacts, podErr
	}
	if dryRun {
		closeDialedClients()
		return impacts, nil
	}
	if len(refused) > 0 {
		closeDialedClients()
		return impacts, fmt.Errorf("generator evacuation refused: %s", strings.Join(refused, "; "))
	}

	openedStreams, newlyAssigned, touchedTargets, rollback, err := b.applyEvacuationShardAdjustments(executionCtx, remoteLifecycleCtx, tr, test, testID, executionGeneration, requireTestExecutionLease, remainingDuration, steadyElapsed, startPaused, groupByName, clientsByTarget, streamsByTarget, shardsByGroup, nextByGroup, target, runSupervisionToken)
	if err != nil {
		var uncertain *distributedEvacuationUncertainError
		if errors.As(err, &uncertain) {
			uncertainTouched := targetSetFromList(uncertain.touchedTargets)
			uncertain.participantClients = evacuationParticipantClients(runClientsByTarget, dialedClients, uncertainTouched)
			uncertain.unreachableTargets = unreachableTargets
			closeUntouchedDialedClients(uncertainTouched)
			return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
		}
		for _, stream := range openedStreams {
			_ = stream.CloseSend()
		}
		closeDialedClients()
		return impacts, err
	}
	replacementClients := make(map[string]generatorClient, len(newlyAssigned))
	for replacementTarget := range newlyAssigned {
		if client := clientsByTarget[replacementTarget]; client != nil {
			replacementClients[replacementTarget] = client
		}
	}
	// As with the initial test-start path (distributed_execution_backend.go),
	// the test-execution lease must be established on a replacement target
	// before run supervision is claimed there: a generator with
	// RequireTestExecutionLease set rejects a run supervision claim unless it
	// already holds a current lease for this generation.
	var stopStagedLeaseKeeper context.CancelFunc
	if requireTestExecutionLease {
		currentGeneration, currentOwnerID, currentDeadline, leaseErr := tr.generatorExecutionClaim()
		if leaseErr != nil || currentGeneration != executionGeneration || currentOwnerID != executionLeaseOwnerID {
			for _, stream := range openedStreams {
				_ = stream.CloseSend()
			}
			rollbackErr := rollback()
			closeDialedClients()
			return impacts, errors.Join(fmt.Errorf("execution leadership changed before replacement lease establishment"), rollbackErr)
		}
		if leaseErr := b.renewGeneratorTestExecutionClients(executionCtx, testID, replacementClients, executionGeneration, executionLeaseOwnerID, currentDeadline); leaseErr != nil {
			for _, stream := range openedStreams {
				_ = stream.CloseSend()
			}
			rollbackErr := rollback()
			closeDialedClients()
			return impacts, errors.Join(fmt.Errorf("establish replacement assignment leases: %w", leaseErr), rollbackErr)
		}
		stagedLeaseCtx, cancelStagedLeaseKeeper := context.WithCancel(executionCtx)
		stopStagedLeaseKeeper = cancelStagedLeaseKeeper
		go b.maintainStagedGeneratorTestExecutionLeases(stagedLeaseCtx, tr, test, executionGeneration, replacementClients)
	}
	if stopStagedLeaseKeeper != nil {
		defer stopStagedLeaseKeeper()
	}
	var stopReplacementSupervision func()
	if b.requireRunSupervision && len(replacementClients) > 0 {
		if err := b.claimRunSupervisionClients(executionCtx, testID, replacementClients, executionGeneration, executionLeaseOwnerID, runSupervisionToken); err != nil {
			rollbackErr := rollback()
			closeDialedClients()
			return impacts, errors.Join(fmt.Errorf("claim replacement run supervision: %w", err), rollbackErr)
		}
		stopReplacementSupervision = b.startRunSupervisionTargetKeeper(executionCtx, testID, replacementClients, executionGeneration, executionLeaseOwnerID, runSupervisionToken)
		defer func() {
			if stopReplacementSupervision != nil {
				stopReplacementSupervision()
			}
		}()
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
				unreachableTargets: unreachableTargets,
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
	var sourceRenewalsJoinedAt time.Time
	if b.requireRunSupervision {
		joinCtx, joinCancel := context.WithTimeout(executionCtx, b.runSupervisionKeeperJoinTimeout())
		joinErr := b.fenceRunSupervisionTarget(joinCtx, tr, test, target, executionGeneration, executionLeaseOwnerID)
		joinCancel()
		if joinErr != nil {
			uncertain := &distributedEvacuationUncertainError{
				testID:             testID,
				participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
				touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
				unreachableTargets: unreachableTargets,
				err:                fmt.Errorf("fence source run-supervision renewals on %s: %w", target, joinErr),
			}
			closeUntouchedDialedClients(touchedTargets)
			return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
		}
		sourceRenewalsJoinedAt = time.Now()
	}
	if requireTestExecutionLease {
		// Suppress only this assignment's renewal before attempting StopTest. A
		// claim already in flight can finish, so the controller deadline sampled
		// after the stop attempt remains the conservative positive fence.
		b.fenceTestExecutionLease(test, target)
	}

	// The source must be positively fenced before its replacement topology is
	// advertised. A timeout is ambiguous: the stop may have applied, so neither
	// rolling back nor committing the reshard can prove the active VUser count.
	if source := clientsByTarget[target]; source != nil {
		if err := b.stopEvacuationClient(testID, executionGeneration, source, runSupervisionToken); err != nil {
			if allowLostSourceLeaseFence && requireTestExecutionLease && !sourceRenewalsJoinedAt.IsZero() {
				if expiryErr := waitForRunSupervisionProcessFence(executionCtx, tr, test, status, controlGeneration, executionGeneration, runSupervisionProcessFenceDeadline(sourceRenewalsJoinedAt, sourceSupervisionTTL)); expiryErr == nil {
					goto sourceFenced
				}
			}
			if allowLostSourceLeaseFence && requireTestExecutionLease {
				if expiryErr := waitForLostGeneratorEpochFence(executionCtx, tr, test, status, controlGeneration, executionGeneration); expiryErr == nil {
					goto sourceFenced
				}
			}
			uncertain := &distributedEvacuationUncertainError{
				testID:             testID,
				participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
				touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
				unreachableTargets: unreachableTargets,
				err:                fmt.Errorf("stop evacuated source %s: %w", target, err),
			}
			closeUntouchedDialedClients(touchedTargets)
			return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
		}
	} else {
		if allowLostSourceLeaseFence && requireTestExecutionLease && !sourceRenewalsJoinedAt.IsZero() {
			if expiryErr := waitForRunSupervisionProcessFence(executionCtx, tr, test, status, controlGeneration, executionGeneration, runSupervisionProcessFenceDeadline(sourceRenewalsJoinedAt, sourceSupervisionTTL)); expiryErr == nil {
				goto sourceFenced
			}
		}
		if allowLostSourceLeaseFence && requireTestExecutionLease {
			if expiryErr := waitForLostGeneratorEpochFence(executionCtx, tr, test, status, controlGeneration, executionGeneration); expiryErr == nil {
				goto sourceFenced
			}
		}
		uncertain := &distributedEvacuationUncertainError{
			testID:             testID,
			participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
			touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
			unreachableTargets: unreachableTargets,
			err:                fmt.Errorf("evacuated source %s is not connected and cannot be fenced", target),
		}
		closeUntouchedDialedClients(touchedTargets)
		return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
	}

sourceFenced:
	if len(podClaims) > 0 {
		if store, ok := tr.testStore.(podCapacityStore); ok {
			if rows, err := store.ListPodCapacityReservations(); err == nil {
				for _, r := range rows {
					if r.TestID == testID && r.Target == target {
						_ = tr.transitionPodLedger(store, r.Target, r.TestID, r.Incarnation, r.ReservationID, "releasing", executionLeaseName, executionLeaseOwnerID, executionGeneration)
					}
				}
			}
		}
	}

	// AssignWork above is a staging boundary: replacement VUsers must remain
	// inert until the source's exact-generation StopTest has positively fenced
	// the old logical shards. Only then may the same shard coordinates and data
	// partitions be released on their replacement generators.
	if requireTestExecutionLease {
		currentGeneration, currentOwnerID, currentDeadline, leaseErr := tr.generatorExecutionClaim()
		if leaseErr != nil || currentGeneration != executionGeneration || currentOwnerID != executionLeaseOwnerID {
			closeUntouchedDialedClients(touchedTargets)
			return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, &distributedEvacuationUncertainError{
				testID:             testID,
				participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
				touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
				unreachableTargets: unreachableTargets,
				err:                fmt.Errorf("execution leadership changed before replacement commit"),
			}, streamsByTarget, openedStreams, releaseTopology)
		}
		// Re-assert the process-scoped epoch before the assignment lease. The lost-
		// source fence above waits past the deadline these replacements were dialled
		// under, so this is the point where a missing epoch renewal becomes visible.
		// A generator never revives a lapsed session at its current generation, so
		// that fault is rejected HERE, by the claim, and can never reach the
		// assignment renewal below.
		if epochErr := b.claimGeneratorProcessExecutionEpochClients(executionCtx, replacementClients, executionGeneration, executionLeaseOwnerID, currentDeadline); epochErr != nil {
			reclaimErr := fmt.Errorf("re-establish replacement process execution epoch after the source fence: %w", epochErr)
			if generatorProcessExecutionEpochExpired(epochErr) {
				// The generator still holds this generation but let its session lapse,
				// so it force-stopped and deleted the staged assignment during the
				// fence. Nothing renewed the replacement's process epoch while it was
				// staged: a keeper fault, not a lease race.
				reclaimErr = fmt.Errorf("re-establish replacement process execution epoch after the source fence: staged replacement lost its execution generation session during the source fence; its process-scoped epoch was not renewed while staged: %w", epochErr)
			}
			closeUntouchedDialedClients(touchedTargets)
			return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, &distributedEvacuationUncertainError{
				testID:             testID,
				participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
				touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
				unreachableTargets: unreachableTargets,
				err:                reclaimErr,
			}, streamsByTarget, openedStreams, releaseTopology)
		}
		if leaseErr := b.renewGeneratorTestExecutionClients(executionCtx, testID, replacementClients, executionGeneration, executionLeaseOwnerID, currentDeadline); leaseErr != nil {
			renewErr := fmt.Errorf("renew replacement assignment leases: %w", leaseErr)
			if generatorExecutionEpochSessionExpired(leaseErr) {
				// The re-claim above just succeeded, so the epoch lapsed between the
				// two round trips rather than while the replacement was staged:
				// claimGeneratorExecutionEpoch pins the generator's expiry one transit
				// budget inside the controller's own lease deadline, so only a nearly
				// exhausted controller lease (or a stall between the calls) leaves too
				// little room for this claim. Do not blame the staged lease keeper.
				renewErr = fmt.Errorf("renew replacement assignment leases: the replacement's execution generation session lapsed between the post-fence epoch re-claim and this assignment claim; the controller execution lease was too close to expiry to cover both round trips: %w", leaseErr)
			}
			closeUntouchedDialedClients(touchedTargets)
			return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, &distributedEvacuationUncertainError{
				testID:             testID,
				participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
				touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
				unreachableTargets: unreachableTargets,
				err:                renewErr,
			}, streamsByTarget, openedStreams, releaseTopology)
		}
	}
	// Staging, stream attachment, and the lost-source fence can consume a
	// material part (or all) of the duration sampled at evacuation entry. Refresh
	// the active-time budget immediately before commit so a replacement never
	// receives a stale full remainder and extends load past the fleet deadline.
	tr.mu.RLock()
	commitCurrent := tr.tests[testID] == test && distributedControlCommitCurrentLocked(tr, test, status, controlGeneration)
	refreshedRemaining, _, _ := distributedEvacuationTiming(test, time.Now())
	tr.mu.RUnlock()
	if !commitCurrent {
		closeUntouchedDialedClients(touchedTargets)
		return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, &distributedEvacuationUncertainError{
			testID:             testID,
			participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
			touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
			unreachableTargets: unreachableTargets,
			err:                fmt.Errorf("generator evacuation became stale before replacement commit"),
		}, streamsByTarget, openedStreams, releaseTopology)
	}
	if refreshedRemaining <= 0 {
		closeUntouchedDialedClients(touchedTargets)
		return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, &distributedEvacuationUncertainError{
			testID:             testID,
			participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
			touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
			unreachableTargets: unreachableTargets,
			err:                fmt.Errorf("steady-state duration expired before replacement commit"),
		}, streamsByTarget, openedStreams, releaseTopology)
	}
	replacementStartAt := time.Now().Add(distributedCommitStartLead(b.timeout))
	if err := b.commitDistributedWork(executionCtx, testID, executionGeneration, replacementStartAt, replacementClients, runSupervisionToken); err != nil {
		uncertain := &distributedEvacuationUncertainError{
			testID:             testID,
			participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
			touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
			unreachableTargets: unreachableTargets,
			err:                fmt.Errorf("commit replacement shards after source fence: %w", err),
		}
		closeUntouchedDialedClients(touchedTargets)
		return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, uncertain, streamsByTarget, openedStreams, releaseTopology)
	}
	if refreshedRemaining < remainingDuration {
		// Duration control is intentionally after CommitWork: generators reject
		// ordinary runtime controls while an assignment is still staged. The
		// coordinated start barrier keeps replacement traffic fenced while this
		// shortening is applied, and any failure remains a post-source-fence
		// uncertainty that stops the exact-generation fleet before publication.
		refreshedLogicalDuration := steadyElapsed + refreshedRemaining
		for _, client := range replacementClients {
			adjustCtx, cancel := context.WithTimeout(executionCtx, b.timeout)
			result, adjustErr := client.AdjustDuration(adjustCtx, generatorrpc.DurationAdjustment{
				TestID:                testID,
				ExecutionGeneration:   executionGeneration,
				TargetDuration:        refreshedLogicalDuration,
				TargetDurationSeconds: int64(refreshedLogicalDuration / time.Second),
				RunSupervisionToken:   runSupervisionToken,
			})
			cancel()
			if adjustErr == nil && !result.Accepted {
				adjustErr = fmt.Errorf("generator rejected duration adjustment")
			}
			if adjustErr != nil {
				closeUntouchedDialedClients(touchedTargets)
				return impacts, b.failClosedUncertainEvacuationWithTopologyRelease(tr, test, &distributedEvacuationUncertainError{
					testID:             testID,
					participantClients: evacuationParticipantClients(runClientsByTarget, dialedClients, touchedTargets),
					touchedTargets:     orderedTargetList(touchedTargets, b.configuredTargets()),
					unreachableTargets: unreachableTargets,
					err:                fmt.Errorf("shorten committed replacement duration: %w", adjustErr),
				}, streamsByTarget, openedStreams, releaseTopology)
			}
		}
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
			unreachableTargets: unreachableTargets,
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
	shardsSnapshot := cloneGroupShardMap(test.remoteGroupShards)
	tr.mu.Unlock()
	tr.armRunContinuity(executionCtx, test, executionGeneration, executionLeaseOwnerID, runSupervisionToken, shardsSnapshot)
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
	// The replacement is now part of the published topology. The existing
	// topology-aware run keeper takes ownership when this method returns, while
	// the deferred cancellation prevents duplicate steady-state renewals.

	if stream := streamsByTarget[target]; stream != nil {
		_ = stream.CloseSend()
	}
	podCommitted = true
	if store, ok := tr.testStore.(podCapacityStore); ok {
		for _, r := range podClaims {
			_ = tr.transitionPodLedger(store, r.Target, r.TestID, r.Incarnation, r.ReservationID, "committed", executionLeaseName, executionLeaseOwnerID, executionGeneration)
		}
	}
	return impacts, nil
}

const (
	// The generator accepts at most a 10-second abandonment drain. After hard
	// cancellation, replacement must also remain fenced for whichever containment
	// path is longer: the generator's one-second hard-stop plus two-second process
	// exit, or an external runner's five-second owner lease plus two-second cleanup
	// exit. Waiting for this maximum contract (rather than the five-second
	// deployment default) keeps evacuation safe for every accepted generator and
	// Java/Bruno sidecar configuration.
	maxRunSupervisionAbandonDrainContract = 10 * time.Second
	runSupervisionHardStopContract        = time.Second
	runSupervisionSafetyExitContract      = 2 * time.Second
	externalRunnerOwnerLeaseContract      = 5 * time.Second
	externalRunnerCleanupExitContract     = 2 * time.Second
	runSupervisionProcessFenceJitter      = 250 * time.Millisecond
)

func runSupervisionProcessFenceDeadline(renewalsJoinedAt time.Time, ttl time.Duration) time.Time {
	postDrainFence := max(
		runSupervisionHardStopContract+runSupervisionSafetyExitContract,
		externalRunnerOwnerLeaseContract+externalRunnerCleanupExitContract,
	)
	return renewalsJoinedAt.Add(ttl + maxRunSupervisionAbandonDrainContract + postDrainFence + runSupervisionProcessFenceJitter)
}

func continuitySupervisionFenceTTL(ttl, adoptionGrace time.Duration) time.Duration {
	if adoptionGrace > 0 {
		// Orphaning can extend the deadman past its original supervision TTL.
		// Include the last possible process lease plus grace before adding drain
		// and containment time in runSupervisionProcessFenceDeadline.
		return max(ttl, defaultExecutionLeaseTTL+adoptionGrace)
	}
	return ttl
}

func waitForRunSupervisionProcessFence(ctx context.Context, tr *TestRunner, test *ManagedTest, status string, controlGeneration, generation uint64, deadline time.Time) error {
	delay := time.Until(deadline)
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if lostGeneratorEpochFenceAbandoned(tr, test, status, controlGeneration) {
		return fmt.Errorf("evacuation became stale while waiting for run-supervision process fence")
	}
	current, _, _, err := tr.generatorExecutionClaim()
	if err != nil || current != generation {
		return fmt.Errorf("execution leadership changed while waiting for run-supervision process fence")
	}
	return nil
}

// lostGeneratorEpochFencePollInterval bounds how long the lost-generator fence
// keeps the evacuation mutex and the topology write gate after the run itself
// stops being commit-eligible.
const lostGeneratorEpochFencePollInterval = 25 * time.Millisecond

// waitForLostGeneratorEpochFence waits past the latest controller lease
// deadline that an already-in-flight assignment claim could have installed on
// the lost generator. With this test/target renewal suppressed, that one
// assignment is guaranteed to have stopped before replacement shards start;
// unrelated assignments retain their own leases and continue normally.
//
// The wait is polled rather than one uninterruptible sleep. It runs while the
// evacuation mutex and the topology write gate are held, for up to a whole
// execution-lease TTL, so it must hand them back the moment the evacuation can
// no longer commit. Every abandonment observed here (an operator stop reserving
// STOPPING, distributed finalization, leadership loss, a canceled execution
// context) is one the post-fence commit check already rejects; detecting it
// while waiting turns a TTL-long stall for whole-run controls into one poll
// interval, and the caller's fail-closed exit is reached sooner rather than
// differently.
//
// INVARIANT: this fence deliberately outlives the controller lease deadline
// sampled when the staged replacements were dialled, so their process-scoped
// execution epoch (Service.executionExpiresAt, installed by
// claimGeneratorExecutionEpoch in connectEvacuationCandidates) has necessarily
// lapsed by the time it returns unless something renewed it underneath. That
// renewal must keep running for the whole wait: a lapsed process epoch does not
// merely reject the next claim, it force-stops and deletes the staged
// assignment, which no post-fence re-claim can undo. maintainStagedGenerator-
// TestExecutionLeases owns that renewal for the staged set; the caller re-claims
// once more after this returns so a gap surfaces as a named error rather than an
// opaque FailedPrecondition on the assignment lease. Early abandonment exits
// above are safe against that invariant: they return an error, so the caller
// fails closed rather than committing on a staged set whose epoch may have gone.
func waitForLostGeneratorEpochFence(ctx context.Context, tr *TestRunner, test *ManagedTest, status string, controlGeneration uint64, generation uint64) error {
	current, _, deadline, err := tr.generatorExecutionClaim()
	if err != nil || current != generation || deadline.IsZero() {
		return fmt.Errorf("execution epoch deadline is unavailable while fencing lost generator")
	}
	deadline = lostGeneratorFenceDeadline(tr, test, deadline, time.Now())
	wait := time.Until(deadline) + 10*time.Millisecond
	if wait <= 0 {
		wait = time.Millisecond
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	poll := time.NewTicker(lostGeneratorEpochFencePollInterval)
	defer poll.Stop()
	for fenced := false; !fenced; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			fenced = true
		case <-poll.C:
			if lostGeneratorEpochFenceAbandoned(tr, test, status, controlGeneration) {
				return fmt.Errorf("generator evacuation was abandoned while fencing lost generator")
			}
		}
	}
	current, _, _, err = tr.generatorExecutionClaim()
	if err != nil || current != generation {
		return fmt.Errorf("execution leadership changed while fencing lost generator")
	}
	return nil
}

// lostGeneratorFenceDeadline is when a lost generator is certain to have
// stopped the run: its lease deadline (capped at one lease TTL; a later value is
// a corrupt sample, never a longer fence) plus, for a continue-policy run, the
// adoption grace. A generator partitioned from this live controller cannot tell
// that from controller loss, so it orphans the run and keeps generating load
// for up to the grace; the replacement must not start before then.
func lostGeneratorFenceDeadline(tr *TestRunner, test *ManagedTest, leaseDeadline, now time.Time) time.Time {
	if latest := now.Add(defaultExecutionLeaseTTL); leaseDeadline.After(latest) {
		leaseDeadline = latest
	}
	tr.mu.RLock()
	adoptionGrace := tr.runAdoptionGrace(test)
	tr.mu.RUnlock()
	return leaseDeadline.Add(adoptionGrace)
}

// lostGeneratorEpochFenceAbandoned evaluates the evacuation's own post-fence
// commit predicate while the fence is still waiting, so a run that can no longer
// commit releases the evacuation and topology locks immediately.
func lostGeneratorEpochFenceAbandoned(tr *TestRunner, test *ManagedTest, status string, controlGeneration uint64) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return !distributedControlCommitCurrentLocked(tr, test, status, controlGeneration)
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

func (b *distributedExecutionBackend) connectEvacuationCandidates(parent context.Context, test *ManagedTest, evacuatedTarget string, clients map[string]generatorClient, states map[string]string, executionGeneration uint64, executionLeaseOwnerID string, claimDeadline time.Time, standbys map[string]storage.PodCapacityReservation) map[string]generatorClient {
	candidates := b.configuredTargets()
	if allowed, err := b.allowedTargetsForTest(test); err == nil {
		candidates = allowed
	}
	if groupTargets, err := b.explicitGroupTargetsForTest(test); err == nil {
		candidates = append(candidates, groupTargets...)
	}
	candidates = orderedUniqueTargets(candidates)
	candidates = b.filterTestPlacementTargets(test, candidates)
	dialed := make(map[string]generatorClient)
	for _, candidate := range candidates {
		if recoverySpareVUsers(test) > 0 && standbys[candidate].RecoveryPlacement == nil {
			continue
		}
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
		snapshot = standbyEvacuationLoad(snapshot, standbys[candidate], test.ID, executionGeneration)
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

func (b *distributedExecutionBackend) applyEvacuationShardAdjustments(parent context.Context, metricsParent context.Context, tr *TestRunner, managedTest *ManagedTest, testID string, executionGeneration uint64, requireTestExecutionLease bool, remainingDuration time.Duration, steadyElapsed time.Duration, startPaused bool, groups map[string]TestGroup, clients map[string]generatorClient, streams map[string]*generatorrpc.MetricsStream, previousByGroup map[string][]GeneratorShardSummary, nextByGroup map[string][]GeneratorShardSummary, evacuatedTarget string, runSupervisionToken []byte) (map[string]*generatorrpc.MetricsStream, map[string]map[string]struct{}, map[string]struct{}, func() error, error) {
	if parent == nil {
		parent = context.Background()
	}
	_, postBaseline := testexec.ResourceBaselineWindows(managedTest != nil && managedTest.ResourceBaseline)
	scenarioID := ""
	if managedTest != nil {
		scenarioID = managedTest.ScenarioID
	}
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
					if err := b.stopEvacuationClient(testID, executionGeneration, client, runSupervisionToken); err != nil {
						rollbackErrs = append(rollbackErrs, fmt.Errorf("stop newly provisioned test on %s: %w", change.target, err))
					}
					stoppedTargets[change.target] = struct{}{}
					continue
				}
			}
			if err := b.restoreEvacuationVUsers(testID, executionGeneration, change.group, change.previous, client, runSupervisionToken); err != nil {
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
				runSecrets, found := tr.distributedRunSecretSnapshot(managedTest, groupName)
				if !found {
					names, inspectErr := engine.ReferencedSecretNames(group.script)
					if inspectErr != nil {
						return fail(fmt.Errorf("inspect evacuation secrets for group %q", groupName))
					}
					if len(names) > 0 {
						return fail(fmt.Errorf("run secret snapshot for group %q is unavailable; fail closed and restart the saved test", groupName))
					}
				}
				secretBundle := runSecrets.Values
				if err := validateDistributedSecretTransport(secretBundle); err != nil {
					return fail(fmt.Errorf("prepare evacuation secrets for group %q: %w", groupName, err))
				}
				secretBundleID := runSecrets.ID
				if len(secretBundle) > 0 {
					ctx, cancel := context.WithTimeout(parent, b.timeout)
					snapshot := b.probeGeneratorLoad(ctx, shard.Target, client)
					cancel()
					if err := validateDistributedSecretTarget(shard.Target, snapshot, secretBundle); err != nil {
						return fail(fmt.Errorf("prepare evacuation secrets for group %q: %w", groupName, err))
					}
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
				if err := tr.extendRunArchiveSources(managedTest, []string{shard.Target}, executionGeneration, runSupervisionToken); err != nil {
					return fail(err)
				}
				archiveSourceTarget, err := tr.archiveSourceTargetForRun(managedTest, shard.Target)
				if err != nil {
					return fail(err)
				}
				ctx, cancel = context.WithTimeout(parent, b.timeout)
				// Record the attempted assignment before the RPC. A timeout can mean
				// the generator committed but its response was lost; rollback must then
				// conservatively stop/reset this candidate.
				changes = append(changes, appliedChange{target: shard.Target, group: groupName, newlyAssigned: true})
				touchedTargets[shard.Target] = struct{}{}
				assignment, assignErr := client.AssignWork(ctx, generatorrpc.Assignment{
					ArchiveSourceTarget:        archiveSourceTarget,
					TestID:                     testID,
					ExecutionGeneration:        executionGeneration,
					ScenarioID:                 scenarioID,
					RequireTestExecutionLease:  requireTestExecutionLease,
					GroupName:                  groupName,
					TargetDuration:             remainingDuration,
					TargetDurationSeconds:      distributedCompatibilityDurationSeconds(remainingDuration),
					DurationOffset:             steadyElapsed,
					StartOffset:                remainingGroupStartOffset(groupStartOffsetDuration(group), steadyElapsed),
					ScriptID:                   group.ScriptID,
					ExpectedScriptHash:         contentHash,
					VUserCount:                 int32(shard.VUsers),
					RampUp:                     rampUp,
					RampDown:                   rampDown,
					InitStrategy:               initStrategy,
					PostBaseline:               postBaseline,
					ShardOrdinal:               shard.ShardOrdinal,
					ShardCount:                 shard.ShardCount,
					VUserIDOffset:              shard.VUserIDOffset,
					StartPaused:                startPaused,
					DataFiles:                  dataFiles,
					RedactionRules:             cloneRedactionRules(group.redactionRules),
					SecretBundle:               secretBundle,
					SecretBundleID:             secretBundleID,
					RunSupervisionToken:        runSupervisionToken,
					AdoptionGrace:              tr.runAdoptionGrace(managedTest),
					Region:                     b.regionForTarget(shard.Target),
					MessageDetailMaxItems:      authoredMessageDetailMaxItems(group.script),
					MessageDetailMaxItemBytes:  authoredMessageDetailMaxItemBytes(group.script),
					MessageDetailMaxTotalBytes: authoredMessageDetailMaxTotalBytes(group.script),
					TransactionResponseTimeSLAMs: func() float64 {
						threshold, _ := effectiveTransactionResponseTimeSLAForTestGroup(managedTest, groupName)
						return threshold
					}(),
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

func (b *distributedExecutionBackend) restoreEvacuationVUsers(testID string, executionGeneration uint64, group string, previous int, client generatorClient, runSupervisionToken []byte) error {
	var lastErr error
	for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		result, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
			TestID:              testID,
			ExecutionGeneration: executionGeneration,
			GroupName:           group,
			TargetVUsers:        int32(previous),
			RunSupervisionToken: runSupervisionToken,
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

func (b *distributedExecutionBackend) stopEvacuationClient(testID string, executionGeneration uint64, client generatorClient, runSupervisionToken []byte) error {
	if client == nil {
		return fmt.Errorf("generator client is unavailable")
	}
	var lastErr error
	for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		result, err := stopGeneratorTestWithToken(ctx, client, testID, false, executionGeneration, runSupervisionToken)
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

// stopEvacuationParticipants stops every participant and reports the failures of
// already-unreachable participants separately. Those generators are still asked
// to stop, because a source whose metrics stream broke may still answer RPCs,
// but a failure from one is a reconciliation note rather than a reason to hold
// the run non-terminal: it is fenced by its own execution-lease expiry.
func (b *distributedExecutionBackend) stopEvacuationParticipants(testID string, executionGeneration uint64, participants map[string]generatorClient, unreachable map[string]struct{}, runSupervisionToken []byte) (blocking error, unreachableFailures error) {
	var stopErrs []error
	var unreachableErrs []error
	for _, target := range orderedGeneratorTargets(participants, b.configuredTargets()) {
		if err := b.stopEvacuationClient(testID, executionGeneration, participants[target], runSupervisionToken); err != nil {
			stopErr := fmt.Errorf("stop evacuation participant %s: %w", target, err)
			if _, tolerated := unreachable[target]; tolerated {
				unreachableErrs = append(unreachableErrs, stopErr)
				continue
			}
			stopErrs = append(stopErrs, stopErr)
		}
	}
	return errors.Join(stopErrs...), errors.Join(unreachableErrs...)
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
	unreachable := targetSetFromList(uncertain.unreachableTargets)
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
	var eventSnapshot *ManagedTest
	runSupervisionToken := cloneRunSupervisionToken(test.remoteRunSupervisionToken)
	defer clear(runSupervisionToken)
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
		eventSnapshot = cloneManagedTestFieldsLocked(test)
	}
	tr.mu.Unlock()
	if releaseTopology != nil {
		releaseTopology()
	}
	if active {
		tr.broadcastEvent("test_stopping", eventSnapshot)
	}

	stopErr, unreachableErr := b.stopEvacuationParticipants(test.ID, test.executionGeneration, participants, unreachable, runSupervisionToken)
	if cancel != nil {
		cancel()
	}
	if stopErr == nil {
		if active {
			noteUnreachableEvacuationParticipants(tr, test, unreachableErr)
			finalizeErr := b.finalizeFailClosedEvacuation(tr, test)
			return errors.Join(uncertain, wrapEvacuationPersistenceError(persistErr), unreachableErr, finalizeErr)
		}
		b.detachEvacuationResourcesUnderTopologyControl(tr, test, participants, allStreams)
		return uncertain
	}

	// Retain all clients in the STOPPING topology and keep retrying with fresh
	// contexts. Generator epoch expiry is an additional fence, not a substitute
	// for explicit cleanup acknowledgement.
	if owned {
		retryRunSupervisionToken := append([]byte(nil), runSupervisionToken...)
		tr.enqueueTerminalRetry("evacuation:"+test.ID, "uncertain evacuation cleanup", func() error {
			unlock := tr.lockDistributedTopologyControl(test.ID)
			retryErr, retryUnreachableErr := b.stopEvacuationParticipants(test.ID, test.executionGeneration, participants, unreachable, retryRunSupervisionToken)
			if retryErr != nil {
				unlock()
				return retryErr
			}
			unlock()
			if active {
				noteUnreachableEvacuationParticipants(tr, test, retryUnreachableErr)
				finalizeErr := b.finalizeFailClosedEvacuation(tr, test)
				if finalizeErr == nil {
					clear(retryRunSupervisionToken)
					retryRunSupervisionToken = nil
				}
				return finalizeErr
			}
			b.detachEvacuationResourcesUnderTopologyControl(tr, test, participants, allStreams)
			clear(retryRunSupervisionToken)
			retryRunSupervisionToken = nil
			return nil
		})
	}
	return errors.Join(uncertain, wrapEvacuationPersistenceError(persistErr), stopErr)
}

// noteUnreachableEvacuationParticipants folds the participants that could not
// acknowledge their stop into the run's failure text before it is committed, so
// the operator can reconcile the generators the controller gave up on rather
// than the run silently reporting a clean fail-closed cleanup.
func noteUnreachableEvacuationParticipants(tr *TestRunner, test *ManagedTest, unreachableErr error) {
	if unreachableErr == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	note := "unreachable generators left to execution-lease expiry: " + unreachableErr.Error()
	if strings.TrimSpace(test.Error) == "" {
		test.Error = note
		return
	}
	test.Error = test.Error + "; " + note
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
	// Terminal and stale-incarnation callers may have no separately captured
	// participants. Allocate the local collectors before merging handles retained
	// on the managed test so cleanup remains safe and still closes those handles.
	if participants == nil {
		participants = make(map[string]generatorClient)
	}
	if streams == nil {
		streams = make(map[string]*generatorrpc.MetricsStream)
	}
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
		test.remoteTransactionResponseTimeSLA = nil
		test.remoteTransactionResponseTimeSLALegacyByTarget = nil
		test.remoteTargetStates = nil
		test.remoteActivated = false
		test.remoteActivationPending = false
		clearDistributedRunSecretSnapshotsLocked(test)
		test.remoteControlGeneration++
	}
	tr.mu.Unlock()
	// Fences are keyed by the ManagedTest pointer so stale-incarnation cleanup
	// must release the exact detached owner even when tr.tests now contains a
	// newer run with the same ID.
	b.clearTestExecutionLeaseFences(test)
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
	impacts, evacuateErr := b.evacuateGenerator(tr, test, target, false, true)
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
