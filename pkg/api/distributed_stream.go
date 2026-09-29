package api

// distributed_stream.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

func distributedMonitoringArtifacts(tr *TestRunner, test *ManagedTest) *runArtifacts {
	if tr == nil || test == nil || (test.ELKCapture == nil && test.DynatraceCapture == nil) {
		return nil
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	current := tr.tests[test.ID]
	if current == nil {
		return nil
	}
	artifacts := current.elkArtifacts
	if artifacts == nil {
		artifacts = current.dynatraceArtifacts
	}
	if artifacts == nil {
		artifacts = &runArtifacts{runner: tr, test: current}
	}
	current.elkArtifacts = artifacts
	current.dynatraceArtifacts = artifacts
	return artifacts
}

func (b *distributedExecutionBackend) startDistributedTestRun(tr *TestRunner, test *ManagedTest, groupShards map[string][]GeneratorShardSummary, clientsByTarget map[string]generatorClient, streamsByTarget map[string]*generatorrpc.MetricsStream) (bool, error) {
	started, activate, err := b.prepareDistributedTestRun(tr, test, groupShards, clientsByTarget, streamsByTarget)
	if err != nil || !started {
		return started, err
	}
	activate()
	return true, nil
}

// prepareDistributedTestRun durably commits the controller-side RUNNING state
// but deliberately does not start metrics consumers or stage schedules. The
// distributed backend uses the returned activation only after every generator
// acknowledges CommitWork, closing the assignment/startup race without exposing
// a half-committed run to controller schedulers.
func (b *distributedExecutionBackend) prepareDistributedTestRun(tr *TestRunner, test *ManagedTest, groupShards map[string][]GeneratorShardSummary, clientsByTarget map[string]generatorClient, streamsByTarget map[string]*generatorrpc.MetricsStream, scheduledStart ...time.Time) (bool, func(), error) {
	now := time.Now()
	if len(scheduledStart) > 0 && scheduledStart[0].After(now) {
		now = scheduledStart[0]
	}
	lifecycleCtx, lifecycleCancel := tr.executionOperationContext(context.Background())
	return b.prepareDistributedTestRunWithLifecycle(tr, test, groupShards, clientsByTarget, streamsByTarget, lifecycleCtx, lifecycleCancel, now)
}

// prepareDistributedTestRunWithLifecycle installs the same lifecycle context
// that owns the metrics streams. The run execution context is canceled as soon
// as a graceful stop is accepted, while this context intentionally remains live
// until terminal metrics are consumed (or leadership/shutdown fences it).
func (b *distributedExecutionBackend) prepareDistributedTestRunWithLifecycle(tr *TestRunner, test *ManagedTest, groupShards map[string][]GeneratorShardSummary, clientsByTarget map[string]generatorClient, streamsByTarget map[string]*generatorrpc.MetricsStream, lifecycleCtx context.Context, lifecycleCancel context.CancelFunc, now time.Time) (bool, func(), error) {
	if lifecycleCtx == nil {
		lifecycleCtx = context.Background()
	}
	if lifecycleCancel == nil {
		lifecycleCtx, lifecycleCancel = context.WithCancel(lifecycleCtx)
	}
	orderedTargets := orderedGeneratorTargets(clientsByTarget, b.configuredTargets())
	firstTarget := ""
	if len(orderedTargets) > 0 {
		firstTarget = orderedTargets[0]
	}

	tr.mu.Lock()
	ctx := test.executionCtx
	if tr.shuttingDown || tr.tests[test.ID] != test || test.Status != "INITIALIZING" || ctx == nil || ctx.Err() != nil || lifecycleCtx.Err() != nil || !tr.isExecutionLeader() {
		tr.mu.Unlock()
		lifecycleCancel()
		return false, nil, nil
	}
	previousGoalControl := test.goalControl
	previousControlGeneration := test.remoteControlGeneration
	previousRemoteActivated := test.remoteActivated
	previousRemoteActivationPending := test.remoteActivationPending
	previousLifecycleCtx := test.remoteLifecycleCtx
	previousLifecycleCancel := test.remoteLifecycleCancel
	test.multiEngine = nil
	test.executionBackend = b
	test.remoteTarget = firstTarget
	test.remoteClient = clientsByTarget[firstTarget]
	test.remoteMetricsStream = streamsByTarget[firstTarget]
	test.remoteGroupTargets = primaryTargetsFromShards(groupShards)
	test.remoteGroupShards = cloneGroupShardMap(groupShards)
	test.GeneratorShards = flattenGroupShards(groupShards)
	test.remoteClientsByTarget = cloneClientMap(clientsByTarget)
	test.remoteArchiveClientsByTarget = nil
	test.remoteMetricsStreamsByTarget = cloneMetricsStreamMap(streamsByTarget)
	test.remoteMetricsBatchesByTarget = make(map[string]generatorrpc.MetricsBatch)
	test.remoteTargetStates = make(map[string]string)
	test.remoteFinalizing = false
	test.remoteLifecycleCtx = lifecycleCtx
	test.remoteLifecycleCancel = lifecycleCancel
	test.remoteStopFinalizerRunning = false
	test.remoteWorkloadCompleted = false
	test.remoteMetricsLastPublishedAt = time.Time{}
	test.remoteMetricsFlushScheduled = false
	test.remoteMetricsAcceptedRevision = 0
	test.remoteMetricsPublishedRevision = 0
	test.remoteMetricsPublishGate = make(chan struct{}, 1)
	test.remoteMetricsPublishGate <- struct{}{}
	test.remoteGoalAcceptedErrorWindows = make(map[string]distributedGoalErrorAccumulator)
	test.remoteGoalPublishedErrorWindows = make(map[string]distributedGoalErrorAccumulator)
	test.remoteGoalUpdateRunning = false
	test.remoteGoalUpdateRevision = 0
	test.remoteActivated = false
	test.remoteActivationPending = true
	test.remoteControlGeneration++
	// Goal-oriented distributed runs evaluate the goal controller from the
	// per-target consumeMetrics goroutines (applyRemoteMetricsBatch). Initialize
	// the controller state here, mirroring the local path (startManagedTestRun),
	// so the first scale decision does not dereference a nil pointer and crash
	// the controller.
	if test.goalControl == nil {
		test.goalControl = &goalControllerState{}
	}
	if test.remoteClient == nil && len(clientsByTarget) == 1 {
		for target, client := range clientsByTarget {
			test.remoteTarget = target
			test.remoteClient = client
			test.remoteMetricsStream = streamsByTarget[target]
			break
		}
	}
	test.StartedAt = &now
	test.EndedAt = nil
	test.Status = "RUNNING"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	if err := tr.saveManagedTestRecord(test); err != nil {
		// No metrics consumer has been launched yet. Restore the durable startup
		// state and detach every remote handle so the caller can stop/close the
		// accepted work before failTestBeforeRun persists FAILED.
		test.multiEngine = nil
		test.executionBackend = nil
		test.remoteTarget = ""
		test.remoteClient = nil
		test.remoteMetricsStream = nil
		test.remoteGroupTargets = nil
		test.remoteGroupShards = nil
		test.GeneratorShards = nil
		test.remoteClientsByTarget = nil
		test.remoteArchiveClientsByTarget = nil
		test.remoteMetricsStreamsByTarget = nil
		test.remoteMetricsBatchesByTarget = nil
		test.remoteTransactionResponseTimeSLA = nil
		test.remoteTransactionResponseTimeSLALegacyByTarget = nil
		test.remoteTargetStates = nil
		test.remoteFinalizing = false
		test.remoteLifecycleCtx = previousLifecycleCtx
		test.remoteLifecycleCancel = previousLifecycleCancel
		test.remoteStopFinalizerRunning = false
		test.remoteWorkloadCompleted = false
		test.remoteMetricsLastPublishedAt = time.Time{}
		test.remoteMetricsFlushScheduled = false
		test.remoteMetricsAcceptedRevision = 0
		test.remoteMetricsPublishedRevision = 0
		test.remoteMetricsPublishGate = nil
		test.remoteGoalAcceptedErrorWindows = nil
		test.remoteGoalPublishedErrorWindows = nil
		test.remoteGoalUpdateRunning = false
		test.remoteGoalUpdateRevision = 0
		test.remoteActivated = previousRemoteActivated
		test.remoteActivationPending = previousRemoteActivationPending
		test.goalControl = previousGoalControl
		test.remoteControlGeneration = previousControlGeneration
		test.StartedAt = nil
		test.EndedAt = nil
		test.Status = "INITIALIZING"
		tr.mu.Unlock()
		lifecycleCancel()
		return false, nil, fmt.Errorf("persist distributed RUNNING transition: %w", err)
	}
	tr.mu.Unlock()

	var activateOnce sync.Once
	activate := func() {
		activateOnce.Do(func() {
			tr.mu.Lock()
			// Stop is deliberately allowed while CommitWork is in flight. If it
			// won the race, do not resurrect metrics/stage activity after the
			// final commit acknowledgement arrives.
			if tr.tests[test.ID] != test || test.Status != "RUNNING" || test.remoteFinalizing {
				tr.mu.Unlock()
				return
			}
			test.remoteActivated = true
			test.remoteActivationPending = false
			eventSnapshot := cloneManagedTestFieldsLocked(test)
			snapshotTime := time.Now()
			if test.StartedAt != nil {
				snapshotTime = *test.StartedAt
			}
			snapshot := buildRunLiveStateSnapshotLocked(test, snapshotTime)
			tr.mu.Unlock()
			tr.cacheRunSnapshot(snapshot)
			tr.broadcastEvent("test_started", eventSnapshot)
			for _, target := range orderedMetricsTargets(streamsByTarget, b.configuredTargets()) {
				go b.consumeMetrics(lifecycleCtx, tr, test, target, streamsByTarget[target])
			}
			preBaseline, _ := testexec.ResourceBaselineWindows(test.ResourceBaseline)
			preBaseline += testexec.StartDelayWindow(test.StartDelay)
			go tr.startDistributedStageSchedulesAfterBaseline(ctx, test, preBaseline)
		})
	}
	return true, activate, nil
}

func persistFreshDistributedScheduledStart(tr *TestRunner, test *ManagedTest, rpcTimeout time.Duration) (time.Time, error) {
	if tr == nil || test == nil {
		return time.Time{}, fmt.Errorf("coordinated start is unavailable")
	}
	ackWindow := distributedCommitAckTimeout(rpcTimeout)
	const maxBarrierPersistenceAttempts = 3
	for attempt := 1; attempt <= maxBarrierPersistenceAttempts; attempt++ {
		startAt := time.Now().Add(distributedCommitStartLead(rpcTimeout))
		tr.mu.Lock()
		if tr.tests[test.ID] != test || test.Status != "RUNNING" || test.remoteFinalizing || test.executionCtx == nil || test.executionCtx.Err() != nil {
			tr.mu.Unlock()
			return time.Time{}, fmt.Errorf("test is no longer awaiting coordinated activation")
		}
		coordinated := startAt
		test.StartedAt = &coordinated
		err := tr.saveManagedTestRecord(test)
		tr.mu.Unlock()
		if err != nil {
			return time.Time{}, err
		}
		// Leave a small dispatch margin beyond the complete ACK deadline. If a
		// slow save consumed it, persist a new exact barrier before fanout.
		if time.Until(startAt) > ackWindow+250*time.Millisecond {
			return startAt, nil
		}
	}
	return time.Time{}, fmt.Errorf("coordinated start persistence repeatedly consumed the commit safety window")
}

func (tr *TestRunner) startDistributedStageSchedulesAfterBaseline(ctx context.Context, test *ManagedTest, preBaseline time.Duration) {
	if test == nil {
		return
	}
	// StartedAt is a coordinated future barrier. A zero-baseline schedule still
	// waits for that epoch so its first hold/ramp cannot run before load starts.
	if !tr.waitTestActiveRuntime(ctx, test.ID, preBaseline) {
		return
	}
	if !tr.waitTestRunning(ctx, test.ID) {
		return
	}
	tr.startStageSchedules(ctx, test, nil)
	tr.startTrafficProfileSchedules(ctx, test, nil)
}

// waitTestActiveRuntime waits until the run has consumed required active time
// since its coordinated StartedAt. Unlike a delay started after CommitWork ACKs,
// this remains correct when activation is late, and it freezes across pauses.
func (tr *TestRunner) waitTestActiveRuntime(ctx context.Context, testID string, required time.Duration) bool {
	poll := executionClockPollInterval
	if required > 0 {
		poll = min(required, executionClockPollInterval)
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		tr.mu.RLock()
		test := tr.tests[testID]
		status := ""
		active := time.Duration(0)
		if test != nil {
			status = test.Status
			active = testActiveRuntimeLocked(test, time.Now().UTC())
		}
		tr.mu.RUnlock()
		if test == nil || isTerminalTestStatus(status) || status == "STOPPING" {
			return false
		}
		if active >= required {
			return status == "RUNNING" || tr.waitTestRunning(ctx, testID)
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// snapshotRemoteClients copies, under tr.mu, the fields clientForTest reads.
func (b *distributedExecutionBackend) snapshotRemoteClients(tr *TestRunner, test *ManagedTest) (map[string]generatorClient, generatorClient, string) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return cloneClientMap(test.remoteClientsByTarget), test.remoteClient, test.remoteTarget
}

// clientFromSnapshot mirrors clientForTest on a lock-free snapshot; dials unlocked.
func (b *distributedExecutionBackend) clientFromSnapshot(clientsByTarget map[string]generatorClient, single generatorClient, target string) (generatorClient, error) {
	if len(clientsByTarget) == 1 {
		for _, client := range clientsByTarget {
			return client, nil
		}
	}
	if single != nil {
		return single, nil
	}
	if target == "" {
		return nil, errDistributedGeneratorTargetUnknown
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	client, err := b.dial(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("connect generator: %w", err)
	}
	return client, nil
}

// clientsFromSnapshot mirrors clientsForTest on a lock-free snapshot.
func (b *distributedExecutionBackend) clientsFromSnapshot(clientsByTarget map[string]generatorClient, single generatorClient, target string) (map[string]generatorClient, error) {
	if len(clientsByTarget) > 0 {
		return clientsByTarget, nil
	}
	client, err := b.clientFromSnapshot(clientsByTarget, single, target)
	if err != nil {
		return nil, err
	}
	return map[string]generatorClient{target: client}, nil
}

// resolveClientsLocked is snapshotRemoteClients + clientsFromSnapshot — the lock-safe
// replacement for clientsForTest(test).
func (b *distributedExecutionBackend) resolveClientsLocked(tr *TestRunner, test *ManagedTest) (map[string]generatorClient, error) {
	clientsByTarget, single, target := b.snapshotRemoteClients(tr, test)
	return b.clientsFromSnapshot(clientsByTarget, single, target)
}

// groupClientFromSnapshot mirrors clientForGroup on a lock-free snapshot.
func (b *distributedExecutionBackend) groupClientFromSnapshot(groupTarget string, groupTargetOK bool, clientsByTarget map[string]generatorClient, single generatorClient, target string) (generatorClient, error) {
	if groupTargetOK {
		if client := clientsByTarget[groupTarget]; client != nil {
			return client, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		defer cancel()
		client, err := b.dial(ctx, groupTarget)
		if err != nil {
			return nil, fmt.Errorf("connect generator: %w", err)
		}
		return client, nil
	}
	return b.clientFromSnapshot(clientsByTarget, single, target)
}

// snapshotMetricsStreams mirrors metricsStreamsForTest but reads under tr.mu.
func (b *distributedExecutionBackend) snapshotMetricsStreams(tr *TestRunner, test *ManagedTest) map[string]*generatorrpc.MetricsStream {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	if len(test.remoteMetricsStreamsByTarget) > 0 {
		return cloneMetricsStreamMap(test.remoteMetricsStreamsByTarget)
	}
	if test.remoteMetricsStream == nil || test.remoteTarget == "" {
		return nil
	}
	return map[string]*generatorrpc.MetricsStream{test.remoteTarget: test.remoteMetricsStream}
}

func (b *distributedExecutionBackend) rollbackShardVUsers(_ context.Context, testID string, executionGeneration uint64, groupName string, clients map[string]generatorClient, attempted []GeneratorShardSummary, previousByTarget map[string]int, runSupervisionToken []byte) error {
	rollbackErrs := make([]error, 0)
	for _, shard := range attempted {
		client := clients[shard.Target]
		if client == nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("selected generator %q is not connected", shard.Target))
			continue
		}
		if err := b.rollbackSingleShardVUsers(testID, executionGeneration, groupName, client, previousByTarget[shard.Target], runSupervisionToken); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("adjust rollback on %s: %w", shard.Target, err))
		}
	}
	return errors.Join(rollbackErrs...)
}

// rollbackSingleShardVUsers retries an idempotent absolute-target adjustment.
// It deliberately uses fresh bounded contexts: the forward request's context
// may have expired after the generator applied it but before returning its ACK.
func (b *distributedExecutionBackend) rollbackSingleShardVUsers(testID string, executionGeneration uint64, groupName string, client generatorClient, previousVUsers int, runSupervisionToken []byte) error {
	if previousVUsers < 0 || previousVUsers > maxManagedTestVUsers {
		return fmt.Errorf("rollback VUser count must be between 0 and %d", maxManagedTestVUsers)
	}
	var rollbackErr error
	for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
			TestID:              testID,
			ExecutionGeneration: executionGeneration,
			GroupName:           groupName,
			TargetVUsers:        int32(previousVUsers),
			RunSupervisionToken: runSupervisionToken,
		})
		cancel()
		if err == nil && adjustment.Accepted && int(adjustment.CurrentVUsers) == previousVUsers {
			return nil
		}
		if err != nil {
			rollbackErr = err
		} else if !adjustment.Accepted {
			rollbackErr = fmt.Errorf("generator did not accept rollback for %s", testID)
		} else {
			rollbackErr = fmt.Errorf("generator reported %d VUsers after rollback, want %d", adjustment.CurrentVUsers, previousVUsers)
		}
		if attempt+1 < durableTransitionAttempts {
			time.Sleep(durableTransitionRetryDelay)
		}
	}
	return rollbackErr
}

func testGroupCurrentShardVUsers(test *ManagedTest, groupName string) int {
	vusers, _ := testGroupVUsers(test, groupName)
	return vusers
}

func (b *distributedExecutionBackend) closeRemoteResources(tr *TestRunner, test *ManagedTest) {
	unlockControl := tr.lockDistributedTopologyControl(test.ID)
	defer unlockControl()
	// Snapshot the streams/clients and clear the fields under the lock, then do
	// the blocking gRPC closes outside the lock (they must not be held under mu).
	tr.mu.Lock()
	streamsByTarget := test.remoteMetricsStreamsByTarget
	singleStream := test.remoteMetricsStream
	clientsByTarget := test.remoteClientsByTarget
	archiveClientsByTarget := test.remoteArchiveClientsByTarget
	singleClient := test.remoteClient
	lifecycleCancel := test.remoteLifecycleCancel
	runSupervisionKeeper := detachRunSupervisionKeeperLocked(test)
	test.remoteMetricsStream = nil
	test.remoteMetricsStreamsByTarget = nil
	test.remoteClient = nil
	test.remoteClientsByTarget = nil
	test.remoteArchiveClientsByTarget = nil
	test.remoteGroupTargets = nil
	test.remoteGroupShards = nil
	test.remoteMetricsBatchesByTarget = nil
	test.remoteTransactionResponseTimeSLA = nil
	test.remoteTransactionResponseTimeSLALegacyByTarget = nil
	test.remoteTargetStates = nil
	test.remoteGoalAcceptedErrorWindows = nil
	test.remoteGoalPublishedErrorWindows = nil
	test.remoteGoalUpdateRunning = false
	test.remoteGoalUpdateRevision = 0
	test.remoteLifecycleCtx = nil
	test.remoteLifecycleCancel = nil
	test.remoteRunSupervisionFencedTargets = nil
	clear(test.remoteRunSupervisionToken[:])
	test.remoteStopFinalizerRunning = false
	test.remoteWorkloadCompleted = false
	test.remoteActivated = false
	test.remoteActivationPending = false
	clearDistributedRunSecretSnapshotsLocked(test)
	test.remoteControlGeneration++
	tr.mu.Unlock()
	b.clearTestExecutionLeaseFences(test)
	if lifecycleCancel != nil {
		lifecycleCancel()
	}
	runSupervisionJoinCtx, runSupervisionJoinCancel := context.WithTimeout(context.Background(), b.runSupervisionKeeperJoinTimeout())
	if err := stopAndWaitRunSupervisionKeeper(runSupervisionJoinCtx, runSupervisionKeeper); err != nil {
		slog.Warn("timed out joining generator run supervision during remote cleanup", "test_id", test.ID, "error", err)
	}
	runSupervisionJoinCancel()

	if len(streamsByTarget) > 0 {
		for _, stream := range streamsByTarget {
			_ = stream.CloseSend()
		}
	} else if singleStream != nil {
		_ = singleStream.CloseSend()
	}
	if len(clientsByTarget) > 0 {
		for _, client := range clientsByTarget {
			_ = client.Close()
		}
	} else if singleClient != nil {
		_ = singleClient.Close()
	}
	for _, client := range archiveClientsByTarget {
		if client != nil {
			_ = client.Close()
		}
	}
}

// startDistributedStopFinalizer keeps the remote lifecycle channel alive after
// the controller cancels executionCtx. A graceful generator ACK only means
// GracefulStop was requested; VUsers may still be finishing an action. Terminal
// metrics win normally. If they do not arrive within the bounded grace window,
// the finalizer escalates to an exact-generation forced stop and terminalizes
// only after every generator confirms that forced teardown completed.
func (b *distributedExecutionBackend) startDistributedStopFinalizer(tr *TestRunner, test *ManagedTest, executionGeneration uint64) {
	if b == nil || tr == nil || test == nil {
		return
	}
	tr.mu.Lock()
	if tr.tests[test.ID] != test || test.Status != "STOPPING" || test.remoteStopFinalizerRunning {
		tr.mu.Unlock()
		return
	}
	test.remoteStopFinalizerRunning = true
	lifecycleCtx := test.remoteLifecycleCtx
	tr.mu.Unlock()
	if lifecycleCtx == nil {
		lifecycleCtx = context.Background()
	}
	grace := b.stopGrace
	if grace <= 0 {
		grace = max(2*b.timeout, 5*time.Second)
	}
	go func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-lifecycleCtx.Done():
			return
		case <-timer.C:
		}

		retryDelay := min(max(b.timeout, 25*time.Millisecond), time.Second)
		for {
			tr.mu.RLock()
			current := tr.tests[test.ID]
			status := ""
			finalizing := false
			if current != nil {
				status = current.Status
				finalizing = current.remoteFinalizing
			}
			tr.mu.RUnlock()
			if current != test || isTerminalTestStatus(status) || finalizing {
				return
			}

			clients, err := b.resolveClientsLocked(tr, test)
			if err == nil {
				err = b.forceStopDistributedTargets(lifecycleCtx, test.ID, executionGeneration, clients, runSupervisionTokenForTest(tr, test))
			}
			if err == nil {
				tr.mu.Lock()
				if tr.tests[test.ID] != test || test.Status != "STOPPING" || test.remoteFinalizing {
					tr.mu.Unlock()
					return
				}
				test.remoteFinalizing = true
				tr.mu.Unlock()
				b.completeDistributedTestRun(tr, test, "ABORTED", "")
				return
			}
			slog.Warn("forced distributed stop retry failed", "test_id", test.ID, "error", err)
			retryTimer := time.NewTimer(retryDelay)
			select {
			case <-lifecycleCtx.Done():
				retryTimer.Stop()
				return
			case <-retryTimer.C:
			}
		}
	}()
}

// finalizeAcknowledgedForcedStop runs only after every target acknowledged a
// graceful=false Stop RPC. At that point remote execution teardown is complete,
// so retain the durable STOPPING fence while the normal archive/terminal path
// finishes, then publish ABORTED. A concurrent graceful finalizer observes
// remoteFinalizing and exits without issuing a later duplicate escalation.
func (b *distributedExecutionBackend) finalizeAcknowledgedForcedStop(tr *TestRunner, test *ManagedTest) {
	if b == nil || tr == nil || test == nil {
		return
	}
	tr.mu.Lock()
	if tr.tests[test.ID] != test || test.Status != "STOPPING" || test.remoteFinalizing {
		tr.mu.Unlock()
		return
	}
	test.remoteFinalizing = true
	tr.mu.Unlock()
	go b.completeDistributedTestRun(tr, test, "ABORTED", "")
}

func (b *distributedExecutionBackend) forceStopDistributedTargets(parent context.Context, testID string, executionGeneration uint64, clients map[string]generatorClient, runSupervisionToken []byte) error {
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	if len(targets) == 0 {
		return fmt.Errorf("distributed generator targets are unavailable")
	}
	type result struct {
		target string
		err    error
	}
	results := make(chan result, len(targets))
	for _, target := range targets {
		target := target
		go func() {
			client := clients[target]
			if client == nil {
				results <- result{target: target, err: fmt.Errorf("generator client is unavailable")}
				return
			}
			ctx, cancel := context.WithTimeout(parent, b.timeout)
			ack, err := stopGeneratorTestWithToken(ctx, client, testID, false, executionGeneration, runSupervisionToken)
			cancel()
			if err == nil && !ack.Acknowledged {
				err = fmt.Errorf("generator did not acknowledge forced stop")
			}
			results <- result{target: target, err: err}
		}()
	}
	var errs []error
	for range targets {
		result := <-results
		if result.err != nil {
			errs = append(errs, fmt.Errorf("force stop remote test on %s: %w", result.target, result.err))
		}
	}
	return errors.Join(errs...)
}

func (b *distributedExecutionBackend) consumeMetrics(ctx context.Context, tr *TestRunner, test *ManagedTest, target string, stream *generatorrpc.MetricsStream) {
	if ctx == nil {
		ctx = context.Background()
	}
	tr.mu.RLock()
	executionGeneration := test.executionGeneration
	tr.mu.RUnlock()
receiveLoop:
	for {
		batch, err := stream.Receive()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("distributed metrics stream stopped", "test_id", test.ID, "error", err)
			b.failDistributedTestOnStreamError(tr, test, target, err)
			return
		}
		// A gRPC implementation may deliver an already-buffered message while
		// cancellation races Receive. Never let that message mutate controller
		// state after test stop or synchronous leadership-epoch cancellation.
		if ctx.Err() != nil {
			return
		}
		if batch.TestID != test.ID {
			continue
		}
		terminal := isTerminalRemoteLifecycle(batch.LifecycleState)
		fence, current := captureDistributedMetricsConsumerFence(tr, test, target, stream, ctx, executionGeneration, terminal)
		if !current {
			return
		}
		if batch.ExecutionGeneration != executionGeneration {
			b.failDistributedTestOnStreamError(tr, test, target, fmt.Errorf("metrics generation %d does not match test generation %d", batch.ExecutionGeneration, executionGeneration))
			return
		}
		if batch.Telemetry == nil {
			batch.Telemetry = &generatorrpc.MetricsTelemetry{}
		}
		receivedAtUnixMs := time.Now().UnixMilli()
		batch.Telemetry.ControllerReceivedAtUnixMs = receivedAtUnixMs
		if batch.Telemetry.PublishedAtUnixMs > 0 {
			batch.Telemetry.TransportLagMs = float64(receivedAtUnixMs - batch.Telemetry.PublishedAtUnixMs)
		}
		var (
			live        *LiveMetrics
			finalStatus string
			finalError  string
			fresh       bool
			publication distributedMetricsPublication
		)
		if !terminal {
			// Reject duplicate/stale windows before they reserve a delayed flush
			// or enter the per-sample alert/stop pipeline.
			for !b.storeRemoteMetricsBatchOnly(tr, test, target, batch, fence) {
				if distributedMetricsConsumerFenceCurrent(tr, test, target, fence, false) {
					continue receiveLoop
				}
				if !distributedMetricsConsumerStillOwned(tr, test, target, fence, false) {
					return
				}
				fence, current = captureDistributedMetricsConsumerFence(tr, test, target, stream, ctx, executionGeneration, false)
				if !current {
					return
				}
			}
			var (
				publish    bool
				flushDelay time.Duration
				flushFrom  time.Time
			)
			for {
				publish, flushDelay, flushFrom = b.shouldPublishDistributedMetricsBatch(tr, test, time.Now(), fence)
				if publish || flushDelay >= 0 || distributedMetricsConsumerFenceCurrent(tr, test, target, fence, false) {
					break
				}
				if !distributedMetricsConsumerStillOwned(tr, test, target, fence, false) {
					return
				}
				fence, current = captureDistributedMetricsConsumerFence(tr, test, target, stream, ctx, executionGeneration, false)
				if !current {
					return
				}
			}
			if !publish {
				if flushDelay >= 0 {
					go b.flushStoredDistributedMetricsAfterDelay(ctx, tr, test, target, flushDelay, flushFrom, fence)
				}
				continue receiveLoop
			}
			for {
				live, finalStatus, finalError, fresh, publication = b.applyStoredRemoteMetricsBatch(tr, test, target, fence)
				if fresh {
					break
				}
				if distributedMetricsConsumerFenceCurrent(tr, test, target, fence, false) {
					continue receiveLoop
				}
				if !distributedMetricsConsumerStillOwned(tr, test, target, fence, false) {
					return
				}
				fence, current = captureDistributedMetricsConsumerFence(tr, test, target, stream, ctx, executionGeneration, false)
				if !current {
					return
				}
			}
		} else {
			savedFinalStatus := ""
			savedFinalError := ""
			for {
				live, finalStatus, finalError, fresh, publication = b.applyRemoteMetricsBatchWithFreshness(tr, test, target, batch, false, fence)
				if finalStatus != "" {
					savedFinalStatus = finalStatus
					savedFinalError = finalError
				}
				if !fresh {
					if distributedMetricsConsumerFenceCurrent(tr, test, target, fence, true) ||
						!distributedMetricsConsumerStillOwned(tr, test, target, fence, true) {
						return
					}
					fence, current = captureDistributedMetricsConsumerFence(tr, test, target, stream, ctx, executionGeneration, true)
					if !current {
						return
					}
					continue
				}
				publicationResult := b.publishDistributedMetricsRevision(ctx, tr, test, live, publication)
				if publicationResult.lifecycleCurrent {
					finalStatus = savedFinalStatus
					finalError = savedFinalError
					break
				}
				if !distributedMetricsConsumerStillOwned(tr, test, target, fence, true) {
					return
				}
				fence, current = captureDistributedMetricsConsumerFence(tr, test, target, stream, ctx, executionGeneration, true)
				if !current {
					return
				}
			}
			switch finalStatus {
			case "COMPLETE":
				b.completeDistributedTestRun(tr, test, "COMPLETE", "")
			case "ABORTED":
				b.completeDistributedTestRun(tr, test, "ABORTED", finalError)
			case "FAILED":
				b.completeDistributedTestRun(tr, test, "FAILED", finalError)
			}
			return
		}

		publicationResult := b.publishDistributedMetricsRevision(ctx, tr, test, live, publication)
		if !publicationResult.lifecycleCurrent && !distributedMetricsConsumerStillOwned(tr, test, target, fence, false) {
			return
		}
	}
}

func isTerminalRemoteLifecycle(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

// remoteMetricsBatchSupersedes reports whether next is allowed to replace the
// latest snapshot for one generator. Millisecond timestamps can collide during
// the staged-to-committed handoff: AssignWork's STAGED snapshot and CommitWork's
// RUNNING snapshot are commonly produced in the same millisecond. Treat that
// lifecycle transition as authoritative while continuing to reject ordinary
// duplicate/stale windows and any non-terminal update after a terminal state.
func remoteMetricsBatchSupersedes(prior, next generatorrpc.MetricsBatch) bool {
	if next.BatchSequence > 0 && prior.BatchSequence > 0 {
		if next.BatchSequence > prior.BatchSequence {
			// Continue below so lifecycle validity is still enforced.
		} else {
			return false
		}
	}
	if isTerminalRemoteLifecycle(next.LifecycleState) {
		return true
	}
	if isTerminalRemoteLifecycle(prior.LifecycleState) {
		return false
	}
	if next.BatchSequence > 0 && prior.BatchSequence > 0 {
		return true
	}
	if next.TimestampUnixMs <= 0 || prior.TimestampUnixMs <= 0 {
		return true
	}
	if next.TimestampUnixMs > prior.TimestampUnixMs {
		return true
	}
	if next.TimestampUnixMs < prior.TimestampUnixMs {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(prior.LifecycleState), "STAGED") &&
		!strings.EqualFold(strings.TrimSpace(next.LifecycleState), "STAGED")
}

type distributedGoalErrorAccumulator struct {
	timestamp        time.Time
	cumulativeErrors int64
}

// distributedMetricsPublication binds a computed aggregate to the execution
// and topology snapshots from which it was built. errorWindows carries the
// latest non-terminal window for every target in that aggregate; the goal
// controller's existing source/timestamp dedupe makes replay bounded and safe.
type distributedMetricsPublication struct {
	revision            uint64
	executionGeneration uint64
	topologyGeneration  uint64
	terminal            bool
	target              string
	stream              *generatorrpc.MetricsStream
	streamBound         bool
	errorWindows        map[string]distributedGoalErrorAccumulator
}

type distributedMetricsPublicationResult struct {
	published        bool
	lifecycleCurrent bool
}

var errStaleDistributedGoalScale = errors.New("distributed goal scale became stale before admission")

type distributedMetricsConsumerFence struct {
	ctx                 context.Context
	executionGeneration uint64
	topologyGeneration  uint64
	target              string
	stream              *generatorrpc.MetricsStream
	enforced            bool
}

func remoteMetricsStreamOwnedLocked(test *ManagedTest, target string, stream *generatorrpc.MetricsStream) bool {
	if test == nil || stream == nil {
		return false
	}
	// Once the per-target map exists it is authoritative. Falling through to
	// the legacy single-target fields when the map omits/replaces a target would
	// let an evacuated primary stream retain ownership and repopulate state.
	if test.remoteMetricsStreamsByTarget != nil {
		return test.remoteMetricsStreamsByTarget[target] == stream
	}
	return test.remoteTarget == target && test.remoteMetricsStream == stream
}

func distributedMetricsConsumerStillOwnedLocked(tr *TestRunner, test *ManagedTest, target string, fence distributedMetricsConsumerFence, terminal bool) bool {
	if !fence.enforced {
		return true
	}
	if fence.ctx == nil || fence.ctx.Err() != nil ||
		tr.tests[test.ID] != test ||
		!tr.isExecutionLeader() ||
		test.executionGeneration != fence.executionGeneration ||
		!remoteMetricsStreamOwnedLocked(test, target, fence.stream) ||
		isTerminalTestStatus(test.Status) {
		return false
	}
	if terminal {
		switch test.Status {
		case "RUNNING", "PAUSED", "STOPPING":
			return true
		default:
			return false
		}
	}
	return (test.Status == "RUNNING" || test.Status == "PAUSED") && !test.remoteFinalizing
}

func distributedMetricsConsumerFenceCurrentLocked(tr *TestRunner, test *ManagedTest, target string, fence distributedMetricsConsumerFence, terminal bool) bool {
	return distributedMetricsConsumerStillOwnedLocked(tr, test, target, fence, terminal) &&
		(!fence.enforced || test.remoteControlGeneration == fence.topologyGeneration)
}

func distributedMetricsConsumerFenceCurrent(tr *TestRunner, test *ManagedTest, target string, fence distributedMetricsConsumerFence, terminal bool) bool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return distributedMetricsConsumerFenceCurrentLocked(tr, test, target, fence, terminal)
}

func distributedMetricsConsumerStillOwned(tr *TestRunner, test *ManagedTest, target string, fence distributedMetricsConsumerFence, terminal bool) bool {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return distributedMetricsConsumerStillOwnedLocked(tr, test, target, fence, terminal)
}

func captureDistributedMetricsConsumerFence(tr *TestRunner, test *ManagedTest, target string, stream *generatorrpc.MetricsStream, ctx context.Context, executionGeneration uint64, terminal bool) (distributedMetricsConsumerFence, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	fence := distributedMetricsConsumerFence{
		ctx:                 ctx,
		executionGeneration: executionGeneration,
		topologyGeneration:  test.remoteControlGeneration,
		target:              target,
		stream:              stream,
		enforced:            true,
	}
	return fence, distributedMetricsConsumerFenceCurrentLocked(tr, test, target, fence, terminal)
}

func (b *distributedExecutionBackend) shouldPublishDistributedMetricsBatch(tr *TestRunner, test *ManagedTest, now time.Time, fences ...distributedMetricsConsumerFence) (bool, time.Duration, time.Time) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(fences) > 0 && !distributedMetricsConsumerFenceCurrentLocked(tr, test, fences[0].target, fences[0], false) {
		return false, -1, time.Time{}
	}
	if test.remoteMetricsLastPublishedAt.IsZero() || now.Sub(test.remoteMetricsLastPublishedAt) >= time.Second {
		test.remoteMetricsLastPublishedAt = now
		test.remoteMetricsFlushScheduled = false
		return true, 0, time.Time{}
	}
	if !test.remoteMetricsFlushScheduled {
		test.remoteMetricsFlushScheduled = true
		return false, time.Second - now.Sub(test.remoteMetricsLastPublishedAt), test.remoteMetricsLastPublishedAt
	}
	return false, -1, time.Time{}
}

func (b *distributedExecutionBackend) flushStoredDistributedMetricsAfterDelay(ctx context.Context, tr *TestRunner, test *ManagedTest, target string, delay time.Duration, flushFrom time.Time, fences ...distributedMetricsConsumerFence) {
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	} else {
		select {
		case <-ctx.Done():
			return
		default:
		}
	}

	tr.mu.Lock()
	if len(fences) > 0 && !distributedMetricsConsumerFenceCurrentLocked(tr, test, target, fences[0], false) {
		tr.mu.Unlock()
		return
	}
	if ctx.Err() != nil || isTerminalTestStatus(test.Status) || test.remoteMetricsBatchesByTarget == nil {
		if test.remoteMetricsFlushScheduled && test.remoteMetricsLastPublishedAt.Equal(flushFrom) {
			test.remoteMetricsFlushScheduled = false
		}
		tr.mu.Unlock()
		return
	}
	// A newer immediate publication invalidates this timer. The equality token
	// also prevents an old timer from consuming a newer scheduled flush.
	if !test.remoteMetricsFlushScheduled || !test.remoteMetricsLastPublishedAt.Equal(flushFrom) {
		tr.mu.Unlock()
		return
	}
	test.remoteMetricsLastPublishedAt = time.Now()
	test.remoteMetricsFlushScheduled = false
	tr.mu.Unlock()

	live, _, _, fresh, publication := b.applyStoredRemoteMetricsBatch(tr, test, target, fences...)
	if live != nil && fresh {
		b.publishDistributedMetricsRevision(ctx, tr, test, live, publication)
	}
}

// distributedMetricsPublicationLifecycleValidLocked is evaluated inside
// publishLiveMetrics' installation critical section. Non-terminal samples may
// flow while running, paused, or draining after a graceful stop, but never once
// remote finalization has started. A terminal batch is allowed to install its
// final snapshot while remoteFinalizing is true, before completeTestRun commits
// the terminal test status.
func distributedMetricsPublicationLifecycleValidLocked(test *ManagedTest, publication distributedMetricsPublication) bool {
	if test == nil || isTerminalTestStatus(test.Status) {
		return false
	}
	if publication.terminal {
		switch test.Status {
		case "RUNNING", "PAUSED", "STOPPING":
			return true
		default:
			return false
		}
	}
	return (test.Status == "RUNNING" || test.Status == "PAUSED") && !test.remoteFinalizing
}

// publishDistributedMetricsRevision serializes the final publication step and
// rejects a computed aggregate when a newer revision has already won. The
// ownership/generation/lifecycle guard runs under the exact tr.mu acquisition
// that installs LiveMetrics and History, closing replacement/finalization races.
// Goal-controller mutation and scale reservation also occur in that admitted
// critical section, so a losing revision is observationally inert.
func (b *distributedExecutionBackend) publishDistributedMetricsRevision(ctx context.Context, tr *TestRunner, test *ManagedTest, live *LiveMetrics, publication distributedMetricsPublication) distributedMetricsPublicationResult {
	result := distributedMetricsPublicationResult{}
	if tr == nil || test == nil || live == nil || publication.revision == 0 {
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tr.mu.Lock()
	gate := test.remoteMetricsPublishGate
	if gate == nil {
		gate = make(chan struct{}, 1)
		gate <- struct{}{}
		test.remoteMetricsPublishGate = gate
	}
	tr.mu.Unlock()

	select {
	case <-ctx.Done():
		return result
	case <-gate:
	}
	defer func() { gate <- struct{}{} }()
	if ctx.Err() != nil {
		return result
	}

	var (
		runGoalUpdate   bool
		goalGroupVUsers map[string]int
		goalUpdate      goalControllerUpdate
	)
	installed := tr.publishLiveMetrics(test, live, distributedMonitoringArtifacts(tr, test), func() bool {
		if ctx.Err() != nil ||
			tr.tests[test.ID] != test ||
			!tr.isExecutionLeader() ||
			test.executionGeneration != publication.executionGeneration ||
			test.remoteControlGeneration != publication.topologyGeneration ||
			(publication.streamBound && !remoteMetricsStreamOwnedLocked(test, publication.target, publication.stream)) ||
			!distributedMetricsPublicationLifecycleValidLocked(test, publication) {
			return false
		}
		result.lifecycleCurrent = true
		if publication.revision <= test.remoteMetricsPublishedRevision {
			return false
		}

		applyRuntimeGoalTargetsToLiveMetrics(test, live)
		if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil && tr.testLoadReadyLocked(test, time.Now().UTC()) {
			if test.goalControl == nil {
				test.goalControl = &goalControllerState{}
			}
			if test.remoteGoalPublishedErrorWindows == nil {
				test.remoteGoalPublishedErrorWindows = make(map[string]distributedGoalErrorAccumulator)
			}
			for source, acceptedWindow := range publication.errorWindows {
				publishedWindow := test.remoteGoalPublishedErrorWindows[source]
				if !acceptedWindow.timestamp.After(publishedWindow.timestamp) ||
					acceptedWindow.cumulativeErrors < publishedWindow.cumulativeErrors {
					continue
				}
				recordGoalErrorWindow(
					test.goalControl,
					source,
					acceptedWindow.timestamp,
					acceptedWindow.cumulativeErrors-publishedWindow.cumulativeErrors,
				)
				test.remoteGoalPublishedErrorWindows[source] = acceptedWindow
			}
			goalGroupVUsers = liveGroupVUsers(test, live)
			currentTotalVUsers := totalGroupVUsers(goalGroupVUsers)
			goalUpdate = evaluateGoalControllerForTest(test, live.Timestamp, currentTotalVUsers, live)
			live.GoalStatus = goalUpdate.Status
			if !publication.terminal && goalUpdate.ShouldScale && !test.remoteGoalUpdateRunning {
				test.remoteGoalUpdateRunning = true
				test.remoteGoalUpdateRevision = publication.revision
				runGoalUpdate = true
			}
		}
		test.remoteMetricsPublishedRevision = publication.revision
		return true
	})
	if !installed {
		return result
	}
	result.published = true

	// Keep thresholds inside the per-test publication gate so an older
	// aggregate cannot evaluate after a newer revision.
	tr.evaluateThresholdAlertsAndStops(test, live, !publication.terminal)
	if runGoalUpdate {
		goalLive := *live
		if live.GoalStatus != nil {
			statusCopy := *live.GoalStatus
			goalLive.GoalStatus = &statusCopy
		}
		go func() {
			defer func() {
				tr.mu.Lock()
				if test.remoteGoalUpdateRevision == publication.revision {
					test.remoteGoalUpdateRunning = false
					test.remoteGoalUpdateRevision = 0
				}
				tr.mu.Unlock()
			}()
			tr.mu.RLock()
			updateCurrent := tr.tests[test.ID] == test &&
				tr.isExecutionLeader() &&
				test.executionGeneration == publication.executionGeneration &&
				test.remoteControlGeneration == publication.topologyGeneration &&
				test.remoteMetricsPublishedRevision == publication.revision &&
				test.remoteGoalUpdateRevision == publication.revision &&
				test.Status == "RUNNING" &&
				!test.remoteFinalizing
			tr.mu.RUnlock()
			if !updateCurrent {
				return
			}
			applyGoalControllerUpdateWithManagedScaler(tr, test, &goalLive, goalGroupVUsers, goalUpdate, func(groupName string, target int) error {
				return tr.scaleDistributedGoalVUsersNow(test, groupName, target, publication)
			})
		}()
	}
	return result
}

// scaleDistributedGoalVUsersNow carries publication ownership through the
// group/topology control gate into the exact tr.mu critical section that
// reserves the scale. A pre-check in the publisher goroutine is insufficient:
// a newer metrics revision, topology, or same-ID run can win while that
// goroutine waits to enter scaleVUsersNow.
func (tr *TestRunner) scaleDistributedGoalVUsersNow(test *ManagedTest, groupName string, target int, publication distributedMetricsPublication) error {
	if tr == nil || test == nil {
		return fmt.Errorf("%w: managed test is unavailable", errStaleDistributedGoalScale)
	}
	return tr.scaleVUsersNowWithAdmissionGuard(test.ID, groupName, target, false, func(current *ManagedTest) error {
		if current != test ||
			!tr.isExecutionLeader() ||
			test.executionGeneration != publication.executionGeneration ||
			test.remoteControlGeneration != publication.topologyGeneration ||
			test.remoteMetricsPublishedRevision != publication.revision ||
			test.remoteGoalUpdateRevision != publication.revision ||
			test.Status != "RUNNING" ||
			test.remoteFinalizing {
			return fmt.Errorf("%w: publication revision %d no longer owns run %s", errStaleDistributedGoalScale, publication.revision, test.ID)
		}
		return nil
	})
}

func stageDistributedGoalErrorWindowLocked(tr *TestRunner, test *ManagedTest, target string, batch generatorrpc.MetricsBatch) {
	if tr == nil || test == nil ||
		test.ScenarioMode != "goal_oriented" ||
		test.GoalConfig == nil ||
		isTerminalRemoteLifecycle(batch.LifecycleState) ||
		batch.TimestampUnixMs <= 0 ||
		!tr.testLoadReadyLocked(test, time.Now().UTC()) {
		return
	}
	timestamp := time.UnixMilli(batch.TimestampUnixMs)
	if test.remoteGoalAcceptedErrorWindows == nil {
		test.remoteGoalAcceptedErrorWindows = make(map[string]distributedGoalErrorAccumulator)
	}
	window := test.remoteGoalAcceptedErrorWindows[target]
	if !timestamp.After(window.timestamp) {
		return
	}
	window.timestamp = timestamp
	window.cumulativeErrors += distributedBatchErrorCount(batch)
	test.remoteGoalAcceptedErrorWindows[target] = window
}

func (b *distributedExecutionBackend) storeRemoteMetricsBatchOnly(tr *TestRunner, test *ManagedTest, target string, batch generatorrpc.MetricsBatch, fences ...distributedMetricsConsumerFence) bool {
	batch.RegionMetrics = controllerApprovedRegionMetrics(batch.RegionMetrics, b.regionForTarget(target))
	freshTransactions := append([]generatorrpc.TransactionMetric(nil), batch.Transactions...)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(fences) > 0 && !distributedMetricsConsumerFenceCurrentLocked(tr, test, target, fences[0], isTerminalRemoteLifecycle(batch.LifecycleState)) {
		return false
	}
	if test.remoteMetricsBatchesByTarget == nil {
		test.remoteMetricsBatchesByTarget = make(map[string]generatorrpc.MetricsBatch)
	}
	if isTerminalRemoteLifecycle(batch.LifecycleState) {
		if prior, ok := test.remoteMetricsBatchesByTarget[target]; ok {
			if len(batch.Transactions) == 0 {
				batch.Transactions = prior.Transactions
			}
			if batch.FleetMetrics == nil {
				batch.FleetMetrics = prior.FleetMetrics
			}
			if len(batch.TransactionMetrics) == 0 {
				batch.TransactionMetrics = prior.TransactionMetrics
			}
			if len(batch.DependencyMetrics) == 0 {
				batch.DependencyMetrics = prior.DependencyMetrics
			}
			if len(batch.SOAPMetrics) == 0 {
				batch.SOAPMetrics = prior.SOAPMetrics
			}
			if len(batch.NetworkProfileMetrics) == 0 {
				batch.NetworkProfileMetrics = prior.NetworkProfileMetrics
			}
			if len(batch.RegionMetrics) == 0 {
				batch.RegionMetrics = prior.RegionMetrics
			}
			if batch.Browser == nil {
				batch.Browser = prior.Browser
			}
			if len(batch.BrowserByDevice) == 0 {
				batch.BrowserByDevice = prior.BrowserByDevice
			}
			if len(batch.Groups) == 0 {
				batch.Groups = prior.Groups
			}
			if len(batch.TransactionResponseTimeSLA) == 0 {
				batch.TransactionResponseTimeSLA = prior.TransactionResponseTimeSLA
			}
		}
	}
	if prior, ok := test.remoteMetricsBatchesByTarget[target]; ok {
		if !remoteMetricsBatchSupersedes(prior, batch) {
			return false
		}
		missing := int64(0)
		if prior.Telemetry != nil {
			missing = prior.Telemetry.MissingMetricWindows
		}
		if prior.BatchSequence > 0 && batch.BatchSequence > prior.BatchSequence && batch.BatchSequence-prior.BatchSequence > 1 {
			gap := batch.BatchSequence - prior.BatchSequence - 1
			if gap > math.MaxInt64 {
				missing = math.MaxInt64
			} else if missing <= math.MaxInt64-int64(gap) {
				missing += int64(gap)
			} else {
				missing = math.MaxInt64
			}
		}
		if missing > 0 {
			if batch.Telemetry == nil {
				batch.Telemetry = &generatorrpc.MetricsTelemetry{}
			}
			batch.Telemetry.MissingMetricWindows = missing
		}
	}
	test.remoteMetricsBatchesByTarget[target] = batch
	if len(batch.TransactionResponseTimeSLA) > 0 {
		delete(test.remoteTransactionResponseTimeSLALegacyByTarget, target)
	} else {
		accumulateRemoteTransactionResponseTimeSLALegacy(test, target, freshTransactions)
	}
	rebuildRemoteTransactionResponseTimeSLA(test)
	test.remoteMetricsAcceptedRevision++
	stageDistributedGoalErrorWindowLocked(tr, test, target, batch)
	return true
}

func rebuildRemoteTransactionResponseTimeSLA(test *ManagedTest) {
	if test == nil {
		return
	}
	test.remoteTransactionResponseTimeSLA = make(map[string]*transactionResponseTimeSLAAccumulator)
	targets := make([]string, 0, len(test.remoteMetricsBatchesByTarget))
	for target := range test.remoteMetricsBatchesByTarget {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		batch := test.remoteMetricsBatchesByTarget[target]
		rows := append([]generatorrpc.TransactionResponseTimeSLACompliance(nil), batch.TransactionResponseTimeSLA...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Group == rows[j].Group {
				return rows[i].Transaction < rows[j].Transaction
			}
			return rows[i].Group < rows[j].Group
		})
		if len(rows) == 0 {
			mergeRemoteTransactionResponseTimeSLALegacyTarget(test, target)
			continue
		}
		for _, row := range rows {
			group, transaction := strings.TrimSpace(row.Group), strings.TrimSpace(row.Transaction)
			threshold, configured := effectiveTransactionResponseTimeSLAForTestGroup(test, group)
			if !configured || transaction == "" || math.IsNaN(row.ThresholdMs) || math.IsInf(row.ThresholdMs, 0) || math.Abs(row.ThresholdMs-threshold) > 0.000001 {
				continue
			}
			if row.TotalCount < 0 || row.EligibleSuccessCount < 0 || row.ExcludedErrorCount < 0 || row.MissedCount < 0 ||
				row.MissedCount > row.EligibleSuccessCount || row.EligibleSuccessCount > row.TotalCount ||
				row.ExcludedErrorCount != row.TotalCount-row.EligibleSuccessCount {
				continue
			}
			key := group + "\x00" + transaction
			acc := test.remoteTransactionResponseTimeSLA[key]
			if acc == nil {
				if len(test.remoteTransactionResponseTimeSLA) >= maxDistributedMetricSeries {
					continue
				}
				acc = &transactionResponseTimeSLAAccumulator{}
				test.remoteTransactionResponseTimeSLA[key] = acc
			}
			acc.total = saturatingMetricCountAdd(acc.total, row.TotalCount)
			acc.eligible = saturatingMetricCountAdd(acc.eligible, row.EligibleSuccessCount)
			acc.excluded = saturatingMetricCountAdd(acc.excluded, row.ExcludedErrorCount)
			acc.missed = saturatingMetricCountAdd(acc.missed, row.MissedCount)
		}
	}
}

func mergeRemoteTransactionResponseTimeSLALegacyTarget(test *ManagedTest, target string) {
	legacy := test.remoteTransactionResponseTimeSLALegacyByTarget[target]
	keys := make([]string, 0, len(legacy))
	for key := range legacy {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		if _, configured := effectiveTransactionResponseTimeSLAForTestGroup(test, parts[0]); !configured {
			continue
		}
		acc := test.remoteTransactionResponseTimeSLA[key]
		if acc == nil {
			if len(test.remoteTransactionResponseTimeSLA) >= maxDistributedMetricSeries {
				continue
			}
			acc = &transactionResponseTimeSLAAccumulator{}
			test.remoteTransactionResponseTimeSLA[key] = acc
		}
		source := legacy[key]
		acc.total = saturatingMetricCountAdd(acc.total, source.total)
		acc.eligible = saturatingMetricCountAdd(acc.eligible, source.eligible)
		acc.excluded = saturatingMetricCountAdd(acc.excluded, source.excluded)
		acc.missed = saturatingMetricCountAdd(acc.missed, source.missed)
	}
}

func effectiveTransactionResponseTimeSLAForTestGroup(test *ManagedTest, groupName string) (float64, bool) {
	if test == nil {
		return 0, false
	}
	var defaultMs float64
	if test.GoalConfig != nil && test.GoalConfig.TransactionResponseTimeSLAMs != nil {
		defaultMs = *test.GoalConfig.TransactionResponseTimeSLAMs
	}
	for index := range test.Groups {
		if strings.TrimSpace(test.Groups[index].Name) != strings.TrimSpace(groupName) {
			continue
		}
		if test.Groups[index].TransactionResponseTimeSLAMs != nil {
			return *test.Groups[index].TransactionResponseTimeSLAMs, true
		}
		return defaultMs, defaultMs > 0
	}
	return 0, false
}

func accumulateRemoteTransactionResponseTimeSLALegacy(test *ManagedTest, target string, transactions []generatorrpc.TransactionMetric) {
	if test == nil || len(transactions) == 0 {
		return
	}
	if test.remoteTransactionResponseTimeSLALegacyByTarget == nil {
		test.remoteTransactionResponseTimeSLALegacyByTarget = make(map[string]map[string]*transactionResponseTimeSLAAccumulator)
	}
	legacy := test.remoteTransactionResponseTimeSLALegacyByTarget[target]
	if legacy == nil {
		legacy = make(map[string]*transactionResponseTimeSLAAccumulator)
		test.remoteTransactionResponseTimeSLALegacyByTarget[target] = legacy
	}
	for _, transaction := range transactions {
		threshold, configured := effectiveTransactionResponseTimeSLAForTestGroup(test, transaction.Group)
		name := strings.TrimSpace(transaction.Name)
		group := strings.TrimSpace(transaction.Group)
		if !configured || name == "" {
			continue
		}
		key := group + "\x00" + name
		acc := legacy[key]
		if acc == nil {
			if len(legacy) >= maxDistributedMetricSeries {
				continue
			}
			acc = &transactionResponseTimeSLAAccumulator{}
			legacy[key] = acc
		}
		acc.total++
		if !transaction.Success {
			acc.excluded++
			continue
		}
		acc.eligible++
		if transaction.DurationMs > threshold {
			acc.missed++
		}
	}
}

func saturatingMetricCountAdd(left, right int64) int64 {
	if right > 0 && left > math.MaxInt64-right {
		return math.MaxInt64
	}
	return left + right
}

func remoteTransactionResponseTimeSLAResults(test *ManagedTest) []TransactionResponseTimeSLAResult {
	if test == nil {
		return nil
	}
	keys := make([]string, 0, len(test.remoteTransactionResponseTimeSLA))
	for key := range test.remoteTransactionResponseTimeSLA {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	results := make([]TransactionResponseTimeSLAResult, 0, len(keys))
	observedGroups := make(map[string]bool)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		threshold, configured := effectiveTransactionResponseTimeSLAForTestGroup(test, parts[0])
		if !configured {
			continue
		}
		observedGroups[parts[0]] = true
		results = append(results, buildTransactionResponseTimeSLAResult(parts[0], parts[1], threshold, test.remoteTransactionResponseTimeSLA[key]))
	}
	for _, group := range test.Groups {
		name := strings.TrimSpace(group.Name)
		threshold, configured := effectiveTransactionResponseTimeSLAForTestGroup(test, name)
		if configured && !observedGroups[name] {
			results = append(results, buildTransactionResponseTimeSLAResult(name, "", threshold, nil))
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Group == results[j].Group {
			return results[i].Transaction < results[j].Transaction
		}
		return results[i].Group < results[j].Group
	})
	return results
}

func (b *distributedExecutionBackend) failDistributedTestOnStreamError(tr *TestRunner, test *ManagedTest, target string, err error) {
	tr.mu.RLock()
	intentionalRemoval := remoteTargetStateIntentionallyRemoved(test.remoteTargetStates[target])
	tr.mu.RUnlock()
	if intentionalRemoval {
		return
	}
	if b.tolerateMetricsStreamLoss(tr, test, target, err) {
		return
	}
	finalError := fmt.Sprintf("remote target %s metrics stream stopped: %v", target, err)

	tr.mu.Lock()
	if test.remoteFinalizing || isTerminalTestStatus(test.Status) {
		tr.mu.Unlock()
		return
	}
	if test.remoteTargetStates == nil {
		test.remoteTargetStates = make(map[string]string)
	}
	if remoteTargetStateDone(test.remoteTargetStates[target]) {
		tr.mu.Unlock()
		return
	}
	test.remoteTargetStates[target] = "FAILED"
	test.remoteFinalizing = true
	tr.mu.Unlock()

	b.completeDistributedTestRun(tr, test, "FAILED", finalError)
}

const (
	// remoteTargetStateEvacuating fences lifecycle messages and stream closure
	// before the source StopTest RPC. A real generator emits COMPLETE before that
	// RPC returns, so waiting until topology commit to mark the source can
	// prematurely finalize a sole-source run.
	remoteTargetStateEvacuating = "evacuating"
	// remoteTargetStateEvacuated is the fixed sentinel stored after topology
	// commit. It is independent of any free-text operator reason.
	remoteTargetStateEvacuated = "evacuated"
)

func remoteTargetStateIntentionallyRemoved(state string) bool {
	normalized := strings.ToLower(strings.TrimSpace(state))
	switch normalized {
	case remoteTargetStateEvacuating, remoteTargetStateEvacuated, "operator_evacuate", "generator_loss":
		return true
	default:
		return strings.Contains(normalized, "evacuat")
	}
}

func remoteTargetStateDone(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "complete", "failed", "aborted":
		return true
	default:
		return remoteTargetStateIntentionallyRemoved(state)
	}
}

func (b *distributedExecutionBackend) applyRemoteMetricsBatch(tr *TestRunner, test *ManagedTest, target string, batch generatorrpc.MetricsBatch) (*LiveMetrics, string, string) {
	live, finalStatus, finalError, _, _ := b.applyRemoteMetricsBatchWithFreshness(tr, test, target, batch, false)
	return live, finalStatus, finalError
}

// applyStoredRemoteMetricsBatch publishes the newest snapshot already accepted
// for target. Callers are responsible for ensuring one accepted window maps to
// at most one call (the immediate path or the token-fenced delayed flush).
func (b *distributedExecutionBackend) applyStoredRemoteMetricsBatch(tr *TestRunner, test *ManagedTest, target string, fences ...distributedMetricsConsumerFence) (*LiveMetrics, string, string, bool, distributedMetricsPublication) {
	return b.applyRemoteMetricsBatchWithFreshness(tr, test, target, generatorrpc.MetricsBatch{}, true, fences...)
}

// applyRemoteMetricsBatchWithFreshness reports whether the supplied metrics
// state was accepted for publication. A rejected duplicate/stale batch still
// returns the current aggregate for compatibility with direct callers, but
// fresh=false keeps it out of threshold alert and stop evaluation.
func (b *distributedExecutionBackend) applyRemoteMetricsBatchWithFreshness(tr *TestRunner, test *ManagedTest, target string, batch generatorrpc.MetricsBatch, alreadyStored bool, fences ...distributedMetricsConsumerFence) (*LiveMetrics, string, string, bool, distributedMetricsPublication) {
	if !alreadyStored {
		batch.RegionMetrics = controllerApprovedRegionMetrics(batch.RegionMetrics, b.regionForTarget(target))
	}
	tr.mu.Lock()
	accepted := false
	revision := uint64(0)
	if alreadyStored {
		if test.remoteMetricsBatchesByTarget == nil {
			tr.mu.Unlock()
			return nil, "", "", false, distributedMetricsPublication{}
		}
		stored, ok := test.remoteMetricsBatchesByTarget[target]
		if !ok || isTerminalRemoteLifecycle(stored.LifecycleState) {
			tr.mu.Unlock()
			return nil, "", "", false, distributedMetricsPublication{}
		}
		if len(fences) > 0 && !distributedMetricsConsumerFenceCurrentLocked(tr, test, target, fences[0], false) {
			tr.mu.Unlock()
			return nil, "", "", false, distributedMetricsPublication{}
		}
		batch = stored
		accepted = true
		revision = test.remoteMetricsAcceptedRevision
	} else {
		if len(fences) > 0 && !distributedMetricsConsumerFenceCurrentLocked(tr, test, target, fences[0], isTerminalRemoteLifecycle(batch.LifecycleState)) {
			tr.mu.Unlock()
			return nil, "", "", false, distributedMetricsPublication{}
		}
		if test.remoteMetricsBatchesByTarget == nil {
			test.remoteMetricsBatchesByTarget = make(map[string]generatorrpc.MetricsBatch)
		}
		// A terminal lifecycle batch (COMPLETE/FAILED/ABORTED) is a state signal
		// that the generator emits with no transaction window
		// (metricsBatchForState). Don't let it erase the metrics already reported
		// for this target.
		if isTerminalRemoteLifecycle(batch.LifecycleState) {
			if prior, ok := test.remoteMetricsBatchesByTarget[target]; ok {
				if len(batch.Transactions) == 0 {
					batch.Transactions = prior.Transactions
				}
				if batch.FleetMetrics == nil {
					batch.FleetMetrics = prior.FleetMetrics
				}
				if len(batch.TransactionMetrics) == 0 {
					batch.TransactionMetrics = prior.TransactionMetrics
				}
				if len(batch.DependencyMetrics) == 0 {
					batch.DependencyMetrics = prior.DependencyMetrics
				}
				if len(batch.SOAPMetrics) == 0 {
					batch.SOAPMetrics = prior.SOAPMetrics
				}
				if len(batch.NetworkProfileMetrics) == 0 {
					batch.NetworkProfileMetrics = prior.NetworkProfileMetrics
				}
				if len(batch.RegionMetrics) == 0 {
					batch.RegionMetrics = prior.RegionMetrics
				}
				if batch.Browser == nil {
					batch.Browser = prior.Browser
				}
				if len(batch.BrowserByDevice) == 0 {
					batch.BrowserByDevice = prior.BrowserByDevice
				}
				if len(batch.Groups) == 0 {
					batch.Groups = prior.Groups
				}
			}
		}
		if prior, ok := test.remoteMetricsBatchesByTarget[target]; ok && !remoteMetricsBatchSupersedes(prior, batch) {
			// A terminal batch can win storage/target-lifecycle handling before an
			// unrelated topology generation changes, then lose guarded live
			// publication. Its consumer must be able to recompute and republish
			// that same terminal snapshot under a rebased, still-owned fence.
			// Production terminal retries are always stream-bound; ordinary
			// duplicate/direct callers remain stale.
			terminalRepublication := isTerminalRemoteLifecycle(batch.LifecycleState) &&
				len(fences) > 0 &&
				fences[0].enforced &&
				strings.EqualFold(strings.TrimSpace(test.remoteTargetStates[target]), strings.TrimSpace(batch.LifecycleState))
			if terminalRepublication {
				accepted = true
				test.remoteMetricsAcceptedRevision++
				revision = test.remoteMetricsAcceptedRevision
			} else {
				batch = prior
			}
		} else {
			test.remoteMetricsBatchesByTarget[target] = batch
			accepted = true
			test.remoteMetricsAcceptedRevision++
			revision = test.remoteMetricsAcceptedRevision
			stageDistributedGoalErrorWindowLocked(tr, test, target, batch)
		}
	}
	batches := make(map[string]generatorrpc.MetricsBatch, len(test.remoteMetricsBatchesByTarget))
	for key, value := range test.remoteMetricsBatchesByTarget {
		batches[key] = value
	}
	publication := distributedMetricsPublication{}
	if accepted {
		publication = distributedMetricsPublication{
			revision:            revision,
			executionGeneration: test.executionGeneration,
			topologyGeneration:  test.remoteControlGeneration,
			terminal:            isTerminalRemoteLifecycle(batch.LifecycleState),
			target:              target,
			errorWindows:        make(map[string]distributedGoalErrorAccumulator, len(test.remoteGoalAcceptedErrorWindows)),
		}
		if len(fences) > 0 && fences[0].enforced {
			publication.stream = fences[0].stream
			publication.streamBound = true
		}
		for source, window := range test.remoteGoalAcceptedErrorWindows {
			publication.errorWindows[source] = window
		}
	}
	if test.remoteTargetStates == nil {
		test.remoteTargetStates = make(map[string]string)
	}
	ignoreTargetTerminal := remoteTargetStateIntentionallyRemoved(test.remoteTargetStates[target])
	finalStatus := ""
	finalError := ""
	if !test.remoteFinalizing && !ignoreTargetTerminal {
		switch batch.LifecycleState {
		case "ABORTED":
			test.remoteTargetStates[target] = "ABORTED"
			finalStatus = "ABORTED"
			finalError = batch.LifecycleError
			test.remoteFinalizing = true
		case "FAILED":
			test.remoteTargetStates[target] = "FAILED"
			finalStatus = "FAILED"
			finalError = batch.LifecycleError
			if finalError == "" {
				finalError = "remote generator reported failure"
			}
			test.remoteFinalizing = true
		case "COMPLETE":
			test.remoteTargetStates[target] = "COMPLETE"
			allComplete := true
			for remoteTarget := range test.remoteClientsByTarget {
				if test.remoteTargetStates[remoteTarget] != "COMPLETE" {
					allComplete = false
					break
				}
			}
			if len(test.remoteClientsByTarget) == 0 {
				allComplete = test.remoteTargetStates[target] == "COMPLETE"
			}
			if allComplete {
				if test.Status == "STOPPING" {
					finalStatus = "ABORTED"
				} else {
					finalStatus = "COMPLETE"
					test.remoteWorkloadCompleted = true
				}
				test.remoteFinalizing = true
			}
		}
	}
	observationValidUntil := distributedObservationValidUntilLocked(test, batches, time.Now())
	tr.mu.Unlock()

	// aggregateDistributedMetricsBatches only reads test.ID + the batches
	// snapshot, so it is safe to run without the lock. Goal-controller mutation
	// is deliberately deferred until this publication wins guarded admission.
	aggregated, fleetTPS, groupTPS := aggregateDistributedMetricsBatches(test.ID, batches)
	tr.mu.Lock()
	live := buildLiveMetricsFromDistributedBatch(test, aggregated, fleetTPS, groupTPS)
	live.ObservationValidUntil = &observationValidUntil
	applyRuntimeGoalTargetsToLiveMetrics(test, live)
	tr.mu.Unlock()
	live.GeneratorResources = b.buildGeneratorResourceSnapshots(test, batches)
	if inventory, _ := tr.cachedGeneratorInventorySnapshot(); len(inventory) > 0 {
		for _, entry := range inventory {
			for _, resource := range live.GeneratorResources {
				if resource.Target == entry.Target {
					resource.PodCapacity = entry.PodCapacity
				}
			}
		}
	}
	return live, finalStatus, finalError, accepted, publication
}

func controllerApprovedRegionMetrics(in []generatorrpc.MetricAggregate, region string) []generatorrpc.MetricAggregate {
	region = strings.TrimSpace(region)
	if region == "" || len(in) == 0 {
		return nil
	}
	normalized := make([]generatorrpc.MetricAggregate, 0, len(in))
	for _, aggregate := range in {
		aggregate.Name = region
		normalized = append(normalized, aggregate)
	}
	return mergeGeneratorMetricAggregateSeries(normalized)
}

func distributedBatchErrorCount(batch generatorrpc.MetricsBatch) int64 {
	if batch.FleetMetrics != nil {
		return batch.FleetMetrics.Errors
	}
	var errors int64
	for _, tx := range batch.Transactions {
		if !tx.Success {
			errors++
		}
	}
	return errors
}

func (b *distributedExecutionBackend) completeDistributedTestRun(tr *TestRunner, test *ManagedTest, finalStatus string, finalError string) {
	completedAt := time.Now()
	tr.mu.RLock()
	elk, dynatrace := test.elkArtifacts, test.dynatraceArtifacts
	tr.mu.RUnlock()
	elk.stopMonitoring()
	dynatrace.stopMonitoring()
	if finalStatus == "FAILED" || finalStatus == "ABORTED" {
		// Failure/abort cleanup and archive transfer are best effort and may be
		// slow when a target is unreachable. Publish the already-derived terminal
		// outcome durably before attempting remote cleanup so the run cannot
		// remain visibly active behind transport or archive timeouts.
		// Snapshot controller-owned target/resource evidence before terminal
		// compaction reduces canonical history to its latest point.
		targetHistory := snapshotRemoteArchiveTargetHistory(tr, test)
		if test.cancel != nil {
			test.cancel()
		}
		if err := tr.completeTestRun(test, finalStatus, completedAt, finalError, nil); err != nil {
			slog.Error("distributed terminal transition remains retryable after bounded persistence retries", "test_id", test.ID, "status", finalStatus, "error", err)
		}
		// An ABORTED outcome represents forced/abrupt teardown. Once a graceful
		// stop has been upgraded to abort, cleanup retries must never downgrade
		// the generator intent back to graceful.
		gracefulCleanup := finalStatus != "ABORTED"
		if err := b.stopRemainingRemoteTargets(tr, test, gracefulCleanup); err != nil {
			slog.Warn("failed to stop remaining remote targets after distributed terminal outcome", "test_id", test.ID, "status", finalStatus, "error", err)
		}
		unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
		tr.mu.RLock()
		owned := tr.tests[test.ID] == test
		tr.mu.RUnlock()
		var archiveRecord *storage.RunArchiveRecord
		var archiveErr error
		if owned {
			archiveRecord, archiveErr = b.stageRemoteArchiveWithTargetHistory(tr, test, finalStatus, finalError, completedAt, false, targetHistory)
		}
		b.closeRemoteResources(tr, test)
		if archiveErr != nil {
			tr.recordRemoteArchiveFailure(test, storage.RunArchiveFailureRemoteCollection)
			unlockArchiveCommit()
			slog.Error("failed to collect best-effort archive after distributed terminal outcome", "test_id", test.ID, "status", finalStatus, "error", archiveErr)
			return
		}
		if archiveRecord != nil {
			tr.persistAndAttachRunArchiveUnderFence(test, archiveRecord)
		} else if owned {
			tr.recordRemoteArchiveFailure(test, storage.RunArchiveFailureRemoteUnavailable)
		}
		tr.mu.RLock()
		archiveTerminal := archiveRecord != nil && test.Archive != nil
		tr.mu.RUnlock()
		unlockArchiveCommit()
		if b.releaseRunCapacity != nil {
			b.releaseRunCapacity(test, archiveTerminal)
		}
		return
	}
	unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
	tr.mu.RLock()
	owned := tr.tests[test.ID] == test
	tr.mu.RUnlock()
	if !owned {
		unlockArchiveCommit()
		b.closeRemoteResources(tr, test)
		if test.cancel != nil {
			test.cancel()
		}
		return
	}
	archiveRecord, archiveErr := b.stageRemoteArchive(tr, test, finalStatus, finalError, completedAt, finalStatus == "COMPLETE")
	if archiveErr != nil && !isArchiveOutputAdmissionError(archiveErr) {
		finalStatus = "FAILED"
		finalError = testexec.AppendRunError(finalError, "remote archive stage failed", archiveErr)
	} else if archiveErr != nil {
		// The generators finished; controller output capacity only prevents
		// archive availability. The durable failure marker below and retained
		// source intent expose recovery without rewriting the execution result.
		slog.Error("distributed archive output admission failed without changing the execution outcome", "test_id", test.ID, "error", archiveErr)
	}
	b.closeRemoteResources(tr, test)
	if test.cancel != nil {
		test.cancel()
	}
	if archiveRecord != nil {
		// Commit terminality before creating/publishing the archive intent. If the
		// controller crashes after staging, startup orphan recovery now sees the
		// authoritative terminal row instead of reconciling the run as FAILED.
		err := tr.persistStoredTestStatus(test.ID, test.executionFence, finalStatus, &completedAt, finalError)
		if isStoredTestTransitionCancelled(err) {
			// The durable row was deleted by the current owner; this stale remote
			// completion must not publish an archive or enqueue publication retries.
			archiveRecord = nil
			err = nil
		}
		if err == nil && archiveRecord != nil {
			// The execution outcome is already durable and all generator data has
			// been collected. Publish terminal API/WS state before potentially slow
			// object publication; the catalog remains absent until its own commit.
			err = tr.completeTestRunUnderArchiveFence(test, finalStatus, completedAt, finalError, nil)
		}
		if err == nil {
			if archiveRecord != nil {
				publishCtx, cancel := context.WithTimeout(context.Background(), archiveFinalizationAttemptTimeout)
				err = tr.finalizeRunArchiveDurablyUnderFence(publishCtx, test, archiveRecord, true)
				cancel()
			}
		}
		if err != nil {
			// The archive manifest already contains the authoritative execution
			// outcome. Publication/catalog failures remain in the durable outbox and
			// must not rewrite that outcome.
			slog.Error("distributed archive publication remains durably retryable", "test_id", test.ID, "error", err)
			tr.enqueueRunArchiveFinalizationRetry(test, archiveRecord, true)
			archiveRecord = nil
		}
	}
	if err := tr.completeTestRunUnderArchiveFence(test, finalStatus, completedAt, finalError, archiveRecord); err != nil {
		slog.Error("distributed terminal transition remains retryable after bounded persistence retries", "test_id", test.ID, "status", finalStatus, "error", err)
	}
	if archiveErr != nil {
		tr.recordRemoteArchiveFailure(test, storage.RunArchiveFailureRemoteCollection)
	}
	unlockArchiveCommit()
	if b.releaseRunCapacity != nil {
		tr.mu.RLock()
		archiveTerminal := archiveRecord != nil && test.Archive != nil
		tr.mu.RUnlock()
		b.releaseRunCapacity(test, archiveTerminal)
	}
}

func (tr *TestRunner) persistAndAttachRunArchive(test *ManagedTest, archiveRecord *storage.RunArchiveRecord) {
	if tr == nil || test == nil || archiveRecord == nil {
		return
	}
	unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
	defer unlockArchiveCommit()
	tr.persistAndAttachRunArchiveUnderFence(test, archiveRecord)
}

func (tr *TestRunner) persistAndAttachRunArchiveUnderFence(test *ManagedTest, archiveRecord *storage.RunArchiveRecord) {
	tr.mu.RLock()
	owned := tr.tests[test.ID] == test
	tr.mu.RUnlock()
	if !owned {
		return
	}
	publishCtx, cancel := context.WithTimeout(context.Background(), archiveFinalizationAttemptTimeout)
	err := tr.finalizeRunArchiveDurablyUnderFence(publishCtx, test, archiveRecord, true)
	cancel()
	if err != nil {
		slog.Error("distributed archive publication remains durably retryable", "test_id", test.ID, "error", err)
		tr.enqueueRunArchiveFinalizationRetry(test, archiveRecord, true)
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[test.ID] != test {
		return
	}
	test.Archive = archiveRecord
	tr.archives[test.ID] = archiveRecord
}

const distributedFailureCleanupTimeout = time.Second

func (b *distributedExecutionBackend) stopRemainingRemoteTargets(tr *TestRunner, test *ManagedTest, graceful bool) error {
	cleanupTimeout := min(b.timeout, distributedFailureCleanupTimeout)
	if cleanupTimeout <= 0 {
		cleanupTimeout = distributedFailureCleanupTimeout
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cleanupCancel()
	unlockControl, err := tr.lockTestLifecycleControl(cleanupCtx, test.ID)
	if err != nil {
		return fmt.Errorf("acquire remote stop lifecycle control: %w", err)
	}
	defer unlockControl()

	tr.mu.RLock()
	clientsByTarget, single, remoteTarget := cloneClientMap(test.remoteClientsByTarget), test.remoteClient, test.remoteTarget
	executionGeneration := test.executionGeneration
	runSupervisionToken := cloneRunSupervisionToken(test.remoteRunSupervisionToken)
	states := make(map[string]string, len(test.remoteTargetStates))
	for k, v := range test.remoteTargetStates {
		states[k] = v
	}
	tr.mu.RUnlock()
	clients, err := b.clientsFromSnapshot(clientsByTarget, single, remoteTarget)
	if err != nil {
		return err
	}
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	type stopResult struct {
		target string
		err    error
	}
	results := make(chan stopResult, len(targets))
	pending := 0
	for _, target := range targets {
		if isTerminalRemoteLifecycle(states[target]) {
			continue
		}
		pending++
		target := target
		go func() {
			_, stopErr := stopGeneratorTestWithToken(cleanupCtx, clients[target], test.ID, graceful, executionGeneration, runSupervisionToken)
			results <- stopResult{target: target, err: stopErr}
		}()
	}

	stopErrs := make([]error, 0)
	for pending > 0 {
		select {
		case <-cleanupCtx.Done():
			return errors.Join(errors.Join(stopErrs...), fmt.Errorf("remote stop cleanup deadline: %w", cleanupCtx.Err()))
		case result := <-results:
			pending--
			if result.err == nil {
				continue
			}
			code := grpcStatus.Code(result.err)
			if code == grpcCodes.Canceled || code == grpcCodes.Unavailable || code == grpcCodes.NotFound {
				continue
			}
			stopErrs = append(stopErrs, fmt.Errorf("stop remote target %s: %w", result.target, result.err))
		}
	}
	return errors.Join(stopErrs...)
}

func (b *distributedExecutionBackend) probeGeneratorLoad(ctx context.Context, target string, client generatorClient) generatorLoadSnapshot {
	snapshot := generatorLoadSnapshot{
		Target:      target,
		GeneratorID: target,
		// Failed probes supply no capacity evidence. In particular, callers
		// must not mistake an unreachable generator for an unlimited one.
	}
	heartbeat, err := client.OpenHeartbeat(ctx)
	if err != nil {
		slog.Warn("open generator heartbeat failed during placement", "target", target, "error", err)
		return snapshot
	}
	defer heartbeat.CloseSend()
	if err := heartbeat.Send(generatorrpc.Heartbeat{GeneratorID: "controller", TimestampUnixMs: time.Now().UnixMilli()}); err != nil {
		slog.Warn("send generator heartbeat probe failed during placement", "target", target, "error", err)
		return snapshot
	}
	response, err := heartbeat.Receive()
	if err != nil {
		slog.Warn("receive generator heartbeat probe failed during placement", "target", target, "error", err)
		return snapshot
	}
	snapshot.GeneratorID = response.GeneratorID
	snapshot.PodCapacity = generatorrpc.PodCapacityFromProto(response.PodCapacity)
	if snapshot.PodCapacity != nil {
		snapshot.PodCapacity.ObservedAtUnixMS = time.Now().UnixMilli()
	}
	if snapshot.GeneratorID == "" {
		snapshot.GeneratorID = target
	}
	b.recordGeneratorIdentity(target, snapshot.GeneratorID)
	snapshot.AvailableCapacity = response.AvailableCapacity
	snapshot.Stats = response.Stats
	snapshot.CPULimitCores = response.CPULimitCores
	snapshot.MemoryLimitBytes = response.MemoryLimitBytes
	snapshot.MaxVUsers = response.MaxVUsers
	snapshot.Version = strings.TrimSpace(response.Version)
	snapshot.SupportedProtocols = normalizedProtocolList(response.SupportedProtocols)
	snapshot.CompatibleFeatures = normalizedProtocolList(response.CompatibleFeatures)
	snapshot.RuntimeCapacities = cloneProtocolRuntimeCapacities(response.RuntimeCapacities)
	snapshot.JavaHost = javaHostCapabilitySummary(response.JavaHost, response.CompatibleFeatures)
	snapshot.RunSupervisionExpirationsTotal = response.RunSupervisionExpirationsTotal
	snapshot.RunSupervisionEscalationsTotal = response.RunSupervisionEscalationsTotal
	snapshot.LastSafetyExit = cloneRemoteGeneratorSafetyExitEvidence(response.LastSafetyExit)
	snapshot.SafetyExits = cloneRemoteGeneratorSafetyExitEvidenceList(response.SafetyExits)
	snapshot.HeartbeatOK = true
	return snapshot
}

// testActiveRuntimeLocked is the run's pause-aware active time since its
// coordinated StartedAt. The caller holds tr.mu.
func testActiveRuntimeLocked(test *ManagedTest, now time.Time) time.Duration {
	if test == nil || test.StartedAt == nil {
		return 0
	}
	active := now.Sub(test.StartedAt.UTC()) - test.CumulativePause
	if test.PausedAt != nil && !test.PausedAt.IsZero() && now.After(*test.PausedAt) {
		pauseStart := test.PausedAt.UTC()
		if pauseStart.Before(test.StartedAt.UTC()) {
			pauseStart = test.StartedAt.UTC()
		}
		active -= now.Sub(pauseStart)
	}
	return active
}
