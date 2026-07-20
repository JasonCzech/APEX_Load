package api

// distributed_stream.go — split from distributed_execution_backend.go (same package, behavior-preserving).

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
	"github.com/jasonczech/apexload/pkg/storage"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

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
	orderedTargets := orderedGeneratorTargets(clientsByTarget, b.configuredTargets())
	firstTarget := ""
	if len(orderedTargets) > 0 {
		firstTarget = orderedTargets[0]
	}

	tr.mu.Lock()
	ctx := test.executionCtx
	if tr.shuttingDown || tr.tests[test.ID] != test || test.Status != "INITIALIZING" || ctx == nil || ctx.Err() != nil || !tr.isExecutionLeader() {
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
	test.remoteMetricsLastPublishedAt = time.Time{}
	test.remoteMetricsFlushScheduled = false
	test.remoteGoalUpdateRunning = false
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
		test.remoteTargetStates = nil
		test.remoteFinalizing = false
		test.remoteLifecycleCtx = previousLifecycleCtx
		test.remoteLifecycleCancel = previousLifecycleCancel
		test.remoteStopFinalizerRunning = false
		test.remoteMetricsLastPublishedAt = time.Time{}
		test.remoteMetricsFlushScheduled = false
		test.remoteGoalUpdateRunning = false
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
			snapshotTime := time.Now()
			if test.StartedAt != nil {
				snapshotTime = *test.StartedAt
			}
			snapshot := buildRunLiveStateSnapshotLocked(test, snapshotTime)
			tr.mu.Unlock()
			tr.cacheRunSnapshot(snapshot)
			tr.broadcastEvent("test_started", test)
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
	if preBaseline > 0 && !tr.waitTestActiveRuntime(ctx, test.ID, preBaseline) {
		return
	}
	if !tr.waitTestRunning(ctx, test.ID) {
		return
	}
	tr.startStageSchedules(ctx, test, nil)
}

// waitTestActiveRuntime waits until the run has consumed required active time
// since its coordinated StartedAt. Unlike a delay started after CommitWork ACKs,
// this remains correct when activation is late, and it freezes across pauses.
func (tr *TestRunner) waitTestActiveRuntime(ctx context.Context, testID string, required time.Duration) bool {
	if required <= 0 {
		return true
	}
	ticker := time.NewTicker(min(required, executionClockPollInterval))
	defer ticker.Stop()
	for {
		tr.mu.RLock()
		test := tr.tests[testID]
		status := ""
		active := time.Duration(0)
		if test != nil {
			status = test.Status
			if test.StartedAt != nil {
				now := time.Now().UTC()
				active = now.Sub(test.StartedAt.UTC()) - test.CumulativePause
				if test.PausedAt != nil && !test.PausedAt.IsZero() && now.After(*test.PausedAt) {
					pauseStart := test.PausedAt.UTC()
					if pauseStart.Before(test.StartedAt.UTC()) {
						pauseStart = test.StartedAt.UTC()
					}
					active -= now.Sub(pauseStart)
				}
			}
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
		return nil, fmt.Errorf("distributed generator target is not recorded")
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

func (b *distributedExecutionBackend) rollbackShardVUsers(_ context.Context, testID string, executionGeneration uint64, groupName string, clients map[string]generatorClient, attempted []GeneratorShardSummary, previousByTarget map[string]int) error {
	rollbackErrs := make([]error, 0)
	for _, shard := range attempted {
		client := clients[shard.Target]
		if client == nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("selected generator %q is not connected", shard.Target))
			continue
		}
		if err := b.rollbackSingleShardVUsers(testID, executionGeneration, groupName, client, previousByTarget[shard.Target]); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("adjust rollback on %s: %w", shard.Target, err))
		}
	}
	return errors.Join(rollbackErrs...)
}

// rollbackSingleShardVUsers retries an idempotent absolute-target adjustment.
// It deliberately uses fresh bounded contexts: the forward request's context
// may have expired after the generator applied it but before returning its ACK.
func (b *distributedExecutionBackend) rollbackSingleShardVUsers(testID string, executionGeneration uint64, groupName string, client generatorClient, previousVUsers int) error {
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
	test.remoteMetricsStream = nil
	test.remoteMetricsStreamsByTarget = nil
	test.remoteClient = nil
	test.remoteClientsByTarget = nil
	test.remoteArchiveClientsByTarget = nil
	test.remoteGroupTargets = nil
	test.remoteGroupShards = nil
	test.remoteMetricsBatchesByTarget = nil
	test.remoteTargetStates = nil
	test.remoteLifecycleCtx = nil
	test.remoteLifecycleCancel = nil
	test.remoteStopFinalizerRunning = false
	test.remoteActivated = false
	test.remoteActivationPending = false
	clearDistributedRunSecretSnapshotsLocked(test)
	test.remoteControlGeneration++
	tr.mu.Unlock()
	if lifecycleCancel != nil {
		lifecycleCancel()
	}

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
				err = b.forceStopDistributedTargets(lifecycleCtx, test.ID, executionGeneration, clients)
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

func (b *distributedExecutionBackend) forceStopDistributedTargets(parent context.Context, testID string, executionGeneration uint64, clients map[string]generatorClient) error {
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
			ack, err := stopGeneratorTest(ctx, client, testID, false, executionGeneration)
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
		tr.mu.RLock()
		expectedGeneration := test.executionGeneration
		tr.mu.RUnlock()
		if batch.ExecutionGeneration != expectedGeneration {
			b.failDistributedTestOnStreamError(tr, test, target, fmt.Errorf("metrics generation %d does not match test generation %d", batch.ExecutionGeneration, expectedGeneration))
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
		if !isTerminalRemoteLifecycle(batch.LifecycleState) {
			publish, flushDelay := b.shouldPublishDistributedMetricsBatch(tr, test, time.Now())
			if !publish {
				b.storeRemoteMetricsBatchOnly(tr, test, target, batch)
				if flushDelay >= 0 {
					go b.flushStoredDistributedMetricsAfterDelay(ctx, tr, test, target, batch, flushDelay)
				}
				continue
			}
		}
		live, finalStatus, finalError := b.applyRemoteMetricsBatch(tr, test, target, batch)
		if live != nil {
			tr.publishLiveMetrics(test, live, nil)
		}
		switch finalStatus {
		case "COMPLETE":
			b.completeDistributedTestRun(tr, test, "COMPLETE", "")
			return
		case "ABORTED":
			b.completeDistributedTestRun(tr, test, "ABORTED", "")
			return
		case "FAILED":
			b.completeDistributedTestRun(tr, test, "FAILED", finalError)
			return
		}
	}
}

func isTerminalRemoteLifecycle(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "COMPLETE", "FAILED":
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
	if isTerminalRemoteLifecycle(next.LifecycleState) {
		return true
	}
	if isTerminalRemoteLifecycle(prior.LifecycleState) {
		return false
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

func (b *distributedExecutionBackend) shouldPublishDistributedMetricsBatch(tr *TestRunner, test *ManagedTest, now time.Time) (bool, time.Duration) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.remoteMetricsLastPublishedAt.IsZero() || now.Sub(test.remoteMetricsLastPublishedAt) >= time.Second {
		test.remoteMetricsLastPublishedAt = now
		test.remoteMetricsFlushScheduled = false
		return true, 0
	}
	if !test.remoteMetricsFlushScheduled {
		test.remoteMetricsFlushScheduled = true
		return false, time.Second - now.Sub(test.remoteMetricsLastPublishedAt)
	}
	return false, -1
}

func (b *distributedExecutionBackend) flushStoredDistributedMetricsAfterDelay(ctx context.Context, tr *TestRunner, test *ManagedTest, target string, batch generatorrpc.MetricsBatch, delay time.Duration) {
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
	if ctx.Err() != nil || test.Status == "COMPLETE" || test.Status == "FAILED" || test.remoteMetricsBatchesByTarget == nil {
		test.remoteMetricsFlushScheduled = false
		tr.mu.Unlock()
		return
	}
	test.remoteMetricsLastPublishedAt = time.Now()
	test.remoteMetricsFlushScheduled = false
	tr.mu.Unlock()

	live, _, _ := b.applyRemoteMetricsBatch(tr, test, target, batch)
	if live != nil {
		tr.publishLiveMetrics(test, live, nil)
	}
}

func (b *distributedExecutionBackend) storeRemoteMetricsBatchOnly(tr *TestRunner, test *ManagedTest, target string, batch generatorrpc.MetricsBatch) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
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
	if prior, ok := test.remoteMetricsBatchesByTarget[target]; ok {
		if !remoteMetricsBatchSupersedes(prior, batch) {
			return
		}
	}
	test.remoteMetricsBatchesByTarget[target] = batch
	if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil && tr.testLoadReadyLocked(test, time.Now().UTC()) && !isTerminalRemoteLifecycle(batch.LifecycleState) && batch.TimestampUnixMs > 0 {
		recordGoalErrorWindow(test.goalControl, target, time.UnixMilli(batch.TimestampUnixMs), distributedBatchErrorCount(batch))
	}
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
	if test.remoteFinalizing || test.Status == "COMPLETE" || test.Status == "FAILED" {
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
	case "complete", "failed":
		return true
	default:
		return remoteTargetStateIntentionallyRemoved(state)
	}
}

func (b *distributedExecutionBackend) applyRemoteMetricsBatch(tr *TestRunner, test *ManagedTest, target string, batch generatorrpc.MetricsBatch) (*LiveMetrics, string, string) {
	tr.mu.Lock()
	acceptedErrorWindow := false
	if test.remoteMetricsBatchesByTarget == nil {
		test.remoteMetricsBatchesByTarget = make(map[string]generatorrpc.MetricsBatch)
	}
	// A terminal lifecycle batch (COMPLETE/FAILED) is a state signal that the
	// generator emits with no transaction window (metricsBatchForState). Don't let
	// it erase the transactions/groups already reported for this target: otherwise
	// live metrics reset to zero at completion. Under load the controller can drain
	// the last metrics batch and this terminal batch from the stream in a single
	// burst, so a poller (and the dashboard) could otherwise never observe the
	// run's transactions before they are wiped. Carry the prior snapshot forward.
	if batch.LifecycleState == "COMPLETE" || batch.LifecycleState == "FAILED" {
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
		batch = prior
	} else {
		test.remoteMetricsBatchesByTarget[target] = batch
		acceptedErrorWindow = batch.LifecycleState != "COMPLETE" && batch.LifecycleState != "FAILED" && batch.TimestampUnixMs > 0
	}
	batches := make(map[string]generatorrpc.MetricsBatch, len(test.remoteMetricsBatchesByTarget))
	for key, value := range test.remoteMetricsBatchesByTarget {
		batches[key] = value
	}
	if test.remoteTargetStates == nil {
		test.remoteTargetStates = make(map[string]string)
	}
	ignoreTargetTerminal := remoteTargetStateIntentionallyRemoved(test.remoteTargetStates[target])
	finalStatus := ""
	finalError := ""
	if !test.remoteFinalizing && !ignoreTargetTerminal {
		switch batch.LifecycleState {
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
				}
				test.remoteFinalizing = true
			}
		}
	}
	tr.mu.Unlock()

	// aggregateDistributedMetricsBatches only reads test.ID + the batches snapshot,
	// so it is safe to run without the lock. Everything below reads mutable
	// ManagedTest state (Groups, GoalConfig/GoalTargets/ScenarioMode) and the
	// goalControl state machine (breachStartedAt), which is concurrently written
	// under tr.mu by UpdateGoalTargetTPS/applyGoalControllerUpdate and by sibling
	// per-target consumeMetrics goroutines — so it must hold tr.mu. The lock is
	// released BEFORE applyGoalControllerUpdate, which re-locks internally and
	// issues blocking gRPC scale calls.
	aggregated, fleetTPS, groupTPS := aggregateDistributedMetricsBatches(test.ID, batches)
	tr.mu.Lock()
	live := buildLiveMetricsFromDistributedBatch(test, aggregated, fleetTPS, groupTPS)
	applyRuntimeGoalTargetsToLiveMetrics(test, live)
	var (
		runGoalUpdate   bool
		goalGroupVUsers map[string]int
		goalUpdate      goalControllerUpdate
	)
	if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil && tr.testLoadReadyLocked(test, time.Now().UTC()) {
		if acceptedErrorWindow {
			recordGoalErrorWindow(test.goalControl, target, time.UnixMilli(batch.TimestampUnixMs), distributedBatchErrorCount(batch))
		}
		goalGroupVUsers = liveGroupVUsers(test, live)
		currentTotalVUsers := totalGroupVUsers(goalGroupVUsers)
		goalUpdate = evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
		live.GoalStatus = goalUpdate.Status
		if goalUpdate.ShouldScale {
			if test.remoteGoalUpdateRunning {
				runGoalUpdate = false
			} else {
				test.remoteGoalUpdateRunning = true
				runGoalUpdate = true
			}
		}
	}
	tr.mu.Unlock()

	if runGoalUpdate {
		goalLive := *live
		if live.GoalStatus != nil {
			statusCopy := *live.GoalStatus
			goalLive.GoalStatus = &statusCopy
		}
		go func() {
			defer func() {
				tr.mu.Lock()
				test.remoteGoalUpdateRunning = false
				tr.mu.Unlock()
			}()
			applyGoalControllerUpdate(tr, test, &goalLive, goalGroupVUsers, goalUpdate, func(groupName string, target int) error {
				return tr.scaleVUsersNow(test.ID, groupName, target, false)
			})
		}()
	}
	live.GeneratorResources = b.buildGeneratorResourceSnapshots(test, batches)
	return live, finalStatus, finalError
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
	if finalStatus == "FAILED" {
		// Generator-loss cleanup and archive transfer are best effort and may be
		// slow when a target is unreachable. Publish the already-derived FAILED
		// outcome durably before attempting any remote cleanup so the run cannot
		// remain visibly RUNNING behind transport or archive timeouts.
		if test.cancel != nil {
			test.cancel()
		}
		if err := tr.completeTestRun(test, finalStatus, completedAt, finalError, nil); err != nil {
			slog.Error("distributed terminal transition remains retryable after bounded persistence retries", "test_id", test.ID, "status", finalStatus, "error", err)
		}
		if err := b.stopRemainingRemoteTargets(tr, test); err != nil {
			slog.Warn("failed to stop remaining remote targets after distributed run failure", "test_id", test.ID, "error", err)
		}
		unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
		tr.mu.RLock()
		owned := tr.tests[test.ID] == test
		tr.mu.RUnlock()
		var archiveRecord *storage.RunArchiveRecord
		var archiveErr error
		if owned {
			archiveRecord, archiveErr = b.stageRemoteArchive(tr, test, finalStatus, finalError, completedAt, false)
		}
		b.closeRemoteResources(tr, test)
		if archiveErr != nil {
			unlockArchiveCommit()
			slog.Error("failed to collect best-effort archive after distributed run failure", "test_id", test.ID, "error", archiveErr)
			return
		}
		if archiveRecord != nil {
			tr.persistAndAttachRunArchiveUnderFence(test, archiveRecord)
		}
		unlockArchiveCommit()
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
	if archiveErr != nil {
		finalStatus = "FAILED"
		finalError = testexec.AppendRunError(finalError, "remote archive stage failed", archiveErr)
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
	unlockArchiveCommit()
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

func (b *distributedExecutionBackend) stopRemainingRemoteTargets(tr *TestRunner, test *ManagedTest) error {
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
	states := make(map[string]string, len(test.remoteTargetStates))
	for k, v := range test.remoteTargetStates {
		states[k] = v
	}
	tr.mu.RUnlock()
	clients, err := b.clientsFromSnapshot(clientsByTarget, single, remoteTarget)
	if err != nil {
		return err
	}
	var stopErr error
	for _, target := range orderedGeneratorTargets(clients, b.configuredTargets()) {
		if state := states[target]; state == "COMPLETE" || state == "FAILED" {
			continue
		}
		if err := cleanupCtx.Err(); err != nil {
			return errors.Join(stopErr, fmt.Errorf("remote stop cleanup deadline: %w", err))
		}
		_, err := stopGeneratorTest(cleanupCtx, clients[target], test.ID, true, executionGeneration)
		if err != nil {
			code := grpcStatus.Code(err)
			if code == grpcCodes.Canceled || code == grpcCodes.Unavailable || code == grpcCodes.NotFound {
				continue
			}
			stopErr = fmt.Errorf("stop remote target %s: %w", target, err)
		}
	}
	return stopErr
}

func (b *distributedExecutionBackend) probeGeneratorLoad(ctx context.Context, target string, client generatorClient) generatorLoadSnapshot {
	snapshot := generatorLoadSnapshot{
		Target:            target,
		GeneratorID:       target,
		AvailableCapacity: int32(^uint32(0) >> 1),
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
	snapshot.HeartbeatOK = true
	return snapshot
}
