package api

// distributed_stream.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

func marshalDistributedScript(script any) ([]byte, string, error) {
	content, err := json.Marshal(script)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(content)
	return content, hex.EncodeToString(hash[:]), nil
}

func (b *distributedExecutionBackend) startDistributedTestRun(tr *TestRunner, test *ManagedTest, groupShards map[string][]GeneratorShardSummary, clientsByTarget map[string]generatorClient, streamsByTarget map[string]*generatorrpc.MetricsStream) {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	orderedTargets := orderedGeneratorTargets(clientsByTarget, b.configuredTargets())
	firstTarget := ""
	if len(orderedTargets) > 0 {
		firstTarget = orderedTargets[0]
	}

	tr.mu.Lock()
	test.cancel = cancel
	test.multiEngine = nil
	test.executionBackend = b
	test.remoteTarget = firstTarget
	test.remoteClient = clientsByTarget[firstTarget]
	test.remoteMetricsStream = streamsByTarget[firstTarget]
	test.remoteGroupTargets = primaryTargetsFromShards(groupShards)
	test.remoteGroupShards = cloneGroupShardMap(groupShards)
	test.GeneratorShards = flattenGroupShards(groupShards)
	test.remoteClientsByTarget = cloneClientMap(clientsByTarget)
	test.remoteMetricsStreamsByTarget = cloneMetricsStreamMap(streamsByTarget)
	test.remoteMetricsBatchesByTarget = make(map[string]generatorrpc.MetricsBatch)
	test.remoteTargetStates = make(map[string]string)
	test.remoteFinalizing = false
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
	tr.saveManagedTestRecord(test)
	snapshot := buildRunLiveStateSnapshotLocked(test, now)
	tr.mu.Unlock()

	tr.cacheRunSnapshot(snapshot)
	tr.broadcastEvent("test_started", test)
	for _, target := range orderedMetricsTargets(streamsByTarget, b.configuredTargets()) {
		go b.consumeMetrics(ctx, tr, test, target, streamsByTarget[target])
	}
	tr.startStageSchedules(ctx, test, nil)
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

func (b *distributedExecutionBackend) rollbackShardVUsers(testID string, groupName string, clients map[string]generatorClient, successes []GeneratorShardSummary, previousByTarget map[string]int) error {
	var rollbackErr error
	for _, shard := range successes {
		client := clients[shard.Target]
		if client == nil {
			rollbackErr = fmt.Errorf("selected generator %q is not connected", shard.Target)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
			TestID:       testID,
			GroupName:    groupName,
			TargetVUsers: int32(previousByTarget[shard.Target]),
		})
		cancel()
		if err != nil {
			rollbackErr = fmt.Errorf("adjust rollback on %s: %w", shard.Target, err)
			continue
		}
		if !adjustment.Accepted {
			rollbackErr = fmt.Errorf("generator %s did not accept rollback for %s", shard.Target, testID)
		}
	}
	return rollbackErr
}

func testGroupCurrentShardVUsers(test *ManagedTest, groupName string) int {
	vusers, _ := testGroupVUsers(test, groupName)
	return vusers
}

func (b *distributedExecutionBackend) closeRemoteResources(tr *TestRunner, test *ManagedTest) {
	// Snapshot the streams/clients and clear the fields under the lock, then do
	// the blocking gRPC closes outside the lock (they must not be held under mu).
	tr.mu.Lock()
	streamsByTarget := test.remoteMetricsStreamsByTarget
	singleStream := test.remoteMetricsStream
	clientsByTarget := test.remoteClientsByTarget
	singleClient := test.remoteClient
	test.remoteMetricsStream = nil
	test.remoteMetricsStreamsByTarget = nil
	test.remoteClient = nil
	test.remoteClientsByTarget = nil
	test.remoteGroupTargets = nil
	test.remoteGroupShards = nil
	test.remoteMetricsBatchesByTarget = nil
	test.remoteTargetStates = nil
	tr.mu.Unlock()

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
		if batch.TestID != test.ID {
			continue
		}
		if batch.Telemetry == nil {
			batch.Telemetry = &generatorrpc.MetricsTelemetry{}
		}
		batch.Telemetry.ControllerReceivedAtUnixMs = time.Now().UnixMilli()
		live, finalStatus, finalError := b.applyRemoteMetricsBatch(tr, test, target, batch)
		tr.publishLiveMetrics(test, live, nil)
		switch finalStatus {
		case "COMPLETE":
			b.completeDistributedTestRun(tr, test, "COMPLETE", "")
			return
		case "FAILED":
			b.completeDistributedTestRun(tr, test, "FAILED", finalError)
			return
		}
	}
}

func (b *distributedExecutionBackend) failDistributedTestOnStreamError(tr *TestRunner, test *ManagedTest, target string, err error) {
	finalError := fmt.Sprintf("remote target %s metrics stream stopped: %v", target, err)

	tr.mu.Lock()
	if test.remoteFinalizing || test.Status == "COMPLETE" || test.Status == "FAILED" {
		tr.mu.Unlock()
		return
	}
	if test.remoteTargetStates == nil {
		test.remoteTargetStates = make(map[string]string)
	}
	if state := test.remoteTargetStates[target]; state == "COMPLETE" || state == "FAILED" {
		tr.mu.Unlock()
		return
	}
	test.remoteTargetStates[target] = "FAILED"
	test.remoteFinalizing = true
	tr.mu.Unlock()

	b.completeDistributedTestRun(tr, test, "FAILED", finalError)
}

func (b *distributedExecutionBackend) applyRemoteMetricsBatch(tr *TestRunner, test *ManagedTest, target string, batch generatorrpc.MetricsBatch) (*LiveMetrics, string, string) {
	tr.mu.Lock()
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
			if len(batch.Groups) == 0 {
				batch.Groups = prior.Groups
			}
		}
	}
	test.remoteMetricsBatchesByTarget[target] = batch
	batches := make(map[string]generatorrpc.MetricsBatch, len(test.remoteMetricsBatchesByTarget))
	for key, value := range test.remoteMetricsBatchesByTarget {
		batches[key] = value
	}
	if test.remoteTargetStates == nil {
		test.remoteTargetStates = make(map[string]string)
	}
	finalStatus := ""
	finalError := ""
	if !test.remoteFinalizing {
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
				finalStatus = "COMPLETE"
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
	if test.ScenarioMode == "goal_oriented" && test.GoalConfig != nil {
		goalGroupVUsers = liveGroupVUsers(test, live)
		currentTotalVUsers := totalGroupVUsers(goalGroupVUsers)
		goalUpdate = evaluateGoalController(live.Timestamp, currentTotalVUsers, live, test.GoalConfig, test.goalControl)
		runGoalUpdate = true
	}
	tr.mu.Unlock()

	if runGoalUpdate {
		applyGoalControllerUpdate(tr, test, live, goalGroupVUsers, goalUpdate, func(groupName string, target int) error {
			return b.ScaleVUsers(tr, test, groupName, target)
		})
	}
	live.GeneratorResources = b.buildGeneratorResourceSnapshots(test, batches)
	return live, finalStatus, finalError
}

func (b *distributedExecutionBackend) completeDistributedTestRun(tr *TestRunner, test *ManagedTest, finalStatus string, finalError string) {
	if finalStatus == "FAILED" {
		if err := b.stopRemainingRemoteTargets(tr, test); err != nil {
			finalError = testexec.AppendRunError(finalError, "stop remote targets", err)
		}
	}
	archiveRecord, archiveErr := b.stageRemoteArchive(tr, test, finalStatus == "COMPLETE")
	if archiveErr != nil {
		finalStatus = "FAILED"
		finalError = testexec.AppendRunError(finalError, "remote archive stage failed", archiveErr)
	}
	b.closeRemoteResources(tr, test)
	if test.cancel != nil {
		test.cancel()
	}
	if archiveRecord != nil {
		if err := tr.saveRunArchiveRecord(archiveRecord); err != nil {
			finalStatus = "FAILED"
			finalError = testexec.AppendRunError(finalError, "archive metadata save failed", err)
			archiveRecord = nil
		}
	}
	tr.completeTestRun(test, finalStatus, time.Now(), finalError, archiveRecord)
}

func (b *distributedExecutionBackend) stopRemainingRemoteTargets(tr *TestRunner, test *ManagedTest) error {
	tr.mu.RLock()
	clientsByTarget, single, remoteTarget := cloneClientMap(test.remoteClientsByTarget), test.remoteClient, test.remoteTarget
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
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		_, err := clients[target].StopTest(ctx, test.ID, true)
		cancel()
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
