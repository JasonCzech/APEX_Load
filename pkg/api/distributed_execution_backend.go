package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

type generatorDial func(ctx context.Context, target string) (generatorClient, error)

type generatorClient interface {
	DistributeScript(ctx context.Context, script generatorrpc.Script) (generatorrpc.ScriptResult, error)
	AssignWork(ctx context.Context, assignment generatorrpc.Assignment) (generatorrpc.AssignmentResult, error)
	AdjustVUsers(ctx context.Context, adjustment generatorrpc.VUserAdjustment) (generatorrpc.AdjustmentResult, error)
	AdjustDuration(ctx context.Context, adjustment generatorrpc.DurationAdjustment) (generatorrpc.DurationResult, error)
	OpenMetricsStream(ctx context.Context) (*generatorrpc.MetricsStream, error)
	OpenHeartbeat(ctx context.Context) (*generatorrpc.HeartbeatStream, error)
	StopTest(ctx context.Context, testID string, graceful bool) (generatorrpc.StopResult, error)
	GetArchiveArtifact(ctx context.Context, request generatorrpc.ArchiveArtifactRequest) ([]byte, error)
	Close() error
}

type generatorLoadSnapshot struct {
	Target             string
	GeneratorID        string
	AvailableCapacity  int32
	Stats              generatorrpc.GeneratorStats
	HeartbeatOK        bool
	CPULimitCores      float64
	MemoryLimitBytes   int64
	MaxVUsers          int32
	Version            string
	SupportedProtocols []string
	CompatibleFeatures []string
}

type generatorTargetSpec struct {
	Address   string
	Region    string
	Version   string
	Protocols []string
}

type distributedExecutionBackend struct {
	targets     []generatorTargetSpec
	timeout     time.Duration
	dial        generatorDial
	drainMu     sync.RWMutex
	drainStates map[string]string
}

func newDistributedExecutionBackend(targets []string, timeout time.Duration, dial generatorDial) *distributedExecutionBackend {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if dial == nil {
		dial = func(ctx context.Context, target string) (generatorClient, error) {
			return generatorrpc.Dial(ctx, target)
		}
	}

	clonedTargets := make([]generatorTargetSpec, 0, len(targets))
	for _, target := range targets {
		spec := parseGeneratorTargetSpec(target)
		if spec.Address == "" {
			continue
		}
		clonedTargets = append(clonedTargets, spec)
	}
	return &distributedExecutionBackend{
		targets:     clonedTargets,
		timeout:     timeout,
		dial:        dial,
		drainStates: make(map[string]string),
	}
}

func (b *distributedExecutionBackend) configuredTargets() []string {
	targets := make([]string, 0, len(b.targets))
	for _, target := range b.targets {
		if b.generatorDrainState(target.Address) != "active" {
			continue
		}
		targets = append(targets, target.Address)
	}
	return targets
}

func (b *distributedExecutionBackend) configuredTargetSpecs() []generatorTargetSpec {
	return append([]generatorTargetSpec(nil), b.targets...)
}

func (b *distributedExecutionBackend) SetGeneratorDrainState(target string, state string) error {
	target = strings.TrimSpace(target)
	state = strings.TrimSpace(state)
	switch state {
	case "", "active":
		state = "active"
	case "cordoned", "draining":
	default:
		return fmt.Errorf("unsupported drain_state %q", state)
	}
	found := false
	for _, configured := range b.targets {
		if configured.Address == target {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("generator target %q is not configured on this controller", target)
	}

	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	if state == "active" {
		delete(b.drainStates, target)
		return nil
	}
	b.drainStates[target] = state
	return nil
}

func (b *distributedExecutionBackend) ApplyGeneratorDrainStates(states map[string]string) {
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	b.drainStates = make(map[string]string)
	for target, state := range states {
		target = strings.TrimSpace(target)
		state = strings.ToLower(strings.TrimSpace(state))
		if target == "" || state == "" || state == "active" {
			continue
		}
		switch state {
		case "cordoned", "draining":
			b.drainStates[target] = state
		}
	}
}

func (b *distributedExecutionBackend) GeneratorDrainStates() map[string]string {
	b.drainMu.RLock()
	defer b.drainMu.RUnlock()
	out := make(map[string]string, len(b.drainStates))
	for target, state := range b.drainStates {
		out[target] = state
	}
	return out
}

func (b *distributedExecutionBackend) generatorDrainState(target string) string {
	b.drainMu.RLock()
	defer b.drainMu.RUnlock()
	if state := b.drainStates[target]; state != "" {
		return state
	}
	return "active"
}

func (b *distributedExecutionBackend) SystemInventory(ctx context.Context) []GeneratorInventoryEntry {
	entries := make([]GeneratorInventoryEntry, 0, len(b.targets))
	for _, target := range b.targets {
		entry := GeneratorInventoryEntry{
			Target:              target.Address,
			GeneratorID:         target.Address,
			Region:              target.Region,
			Status:              "unreachable",
			Version:             generatorSpecVersion(target),
			SupportedProtocols:  generatorSpecProtocols(target),
			DrainState:          b.generatorDrainState(target.Address),
			CapacityState:       generatorCapacityState(0, 0, false),
			HealthScore:         generatorHealthScore(false, "unavailable"),
			CompatibilityState:  generatorCompatibilityState(target),
			CompatibilityIssues: generatorCompatibilityIssues(target),
		}

		probeCtx, cancel := context.WithTimeout(ctx, b.timeout)
		client, err := b.dial(probeCtx, target.Address)
		if err != nil {
			cancel()
			entries = append(entries, entry)
			continue
		}

		snapshot := b.probeGeneratorLoad(probeCtx, target.Address, client)
		_ = client.Close()
		cancel()

		if snapshot.GeneratorID != "" {
			entry.GeneratorID = snapshot.GeneratorID
		}
		if snapshot.HeartbeatOK {
			effectiveSpec := generatorEffectiveSpec(target, snapshot)
			entry.Version = generatorSpecVersion(effectiveSpec)
			entry.SupportedProtocols = generatorSpecProtocols(effectiveSpec)
			entry.CompatibleFeatures = snapshot.CompatibleFeatures
			if len(entry.CompatibleFeatures) == 0 {
				entry.CompatibleFeatures = append([]string(nil), entry.SupportedProtocols...)
			}
			entry.CompatibilityState = generatorCompatibilityState(effectiveSpec)
			entry.CompatibilityIssues = generatorCompatibilityIssues(effectiveSpec)
			entry.Status = "connected"
			entry.AvailableCapacity = snapshot.AvailableCapacity
			applyGeneratorStatsToInventory(&entry, snapshot.Stats)
			entry.CPULimitCores = snapshot.CPULimitCores
			entry.MemoryLimitBytes = snapshot.MemoryLimitBytes
			entry.MaxVUsers = snapshot.MaxVUsers
			entry.CapacityState = generatorCapacityState(entry.AvailableCapacity, entry.MaxVUsers, true)
			entry.HealthScore = generatorHealthScore(true, entry.CapacityState)
		}

		entries = append(entries, entry)
	}
	return entries
}

func (b *distributedExecutionBackend) allowedTargetsForTest(test *ManagedTest) ([]string, error) {
	assignment, err := normalizeGeneratorAssignmentSettings(test.Assignment)
	if err != nil {
		return nil, err
	}
	if assignment == nil || assignment.Mode != generatorAssignmentModeSpecificHosts {
		return b.configuredTargets(), nil
	}
	configured := make(map[string]struct{}, len(b.targets))
	for _, target := range b.targets {
		configured[target.Address] = struct{}{}
	}
	allowed := make([]string, 0, len(assignment.SelectedTargets))
	for _, target := range assignment.SelectedTargets {
		if _, ok := configured[target]; !ok {
			return nil, fmt.Errorf("assignment target %q is not configured on this controller", target)
		}
		if drainState := b.generatorDrainState(target); drainState != "active" {
			return nil, fmt.Errorf("assignment target %q is %s and cannot accept new work", target, drainState)
		}
		allowed = append(allowed, target)
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("no explicit assignment targets are configured for this controller")
	}
	return allowed, nil
}

func (b *distributedExecutionBackend) Run(tr *TestRunner, test *ManagedTest) {
	assignment, err := normalizeGeneratorAssignmentSettings(test.Assignment)
	if err != nil {
		tr.failTestBeforeRun(test, err.Error())
		return
	}
	test.Assignment = assignment
	if len(b.targets) == 0 {
		tr.failTestBeforeRun(test, "no generator targets configured")
		return
	}
	allowedTargets, err := b.allowedTargetsForTest(test)
	if err != nil {
		tr.failTestBeforeRun(test, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()

	clientsByTarget := make(map[string]generatorClient)
	streamsByTarget := make(map[string]*generatorrpc.MetricsStream)
	distributedScripts := make(map[string]map[string]struct{})
	loadByTarget := make(map[string]generatorLoadSnapshot)
	cleanup := func() {
		for _, stream := range streamsByTarget {
			_ = stream.CloseSend()
		}
		for _, client := range clientsByTarget {
			_ = client.Close()
		}
	}
	for _, target := range allowedTargets {
		client, err := b.dial(ctx, target)
		if err != nil {
			slog.Warn("distributed generator unavailable during placement", "target", target, "error", err)
			continue
		}
		clientsByTarget[target] = client
		loadByTarget[target] = b.probeGeneratorLoad(ctx, target, client)
	}
	if len(clientsByTarget) == 0 {
		tr.failTestBeforeRun(test, "no reachable generator targets configured")
		return
	}
	groupShards := planDistributedGroupShards(test.Groups, loadByTarget, allowedTargets, assignment, b.targets)
	for _, group := range test.Groups {
		if group.script == nil {
			cleanup()
			tr.failTestBeforeRun(test, fmt.Sprintf("group %q has no compiled script", group.Name))
			return
		}

		shards := groupShards[group.Name]
		if len(shards) == 0 {
			cleanup()
			tr.failTestBeforeRun(test, generatorPlacementRejectionMessage(group, loadByTarget, b.targets))
			return
		}

		scriptContent, contentHash, err := marshalDistributedScript(group.script)
		if err != nil {
			cleanup()
			tr.failTestBeforeRun(test, fmt.Sprintf("serialize script %q: %v", group.ScriptName, err))
			return
		}

		for _, shard := range shards {
			if shard.VUsers <= 0 {
				continue
			}
			target := shard.Target
			client := clientsByTarget[target]
			if client == nil {
				cleanup()
				tr.failTestBeforeRun(test, fmt.Sprintf("selected generator %q is not connected", target))
				return
			}
			if distributedScripts[target] == nil {
				distributedScripts[target] = make(map[string]struct{})
			}

			if _, ok := distributedScripts[target][group.ScriptID]; !ok {
				if _, err := client.DistributeScript(ctx, generatorrpc.Script{
					ID:          group.ScriptID,
					Content:     scriptContent,
					ContentHash: contentHash,
				}); err != nil {
					cleanup()
					tr.failTestBeforeRun(test, fmt.Sprintf("distribute script %q: %v", group.ScriptName, err))
					return
				}
				distributedScripts[target][group.ScriptID] = struct{}{}
			}

			assignment, err := client.AssignWork(ctx, generatorrpc.Assignment{
				TestID:                test.ID,
				GroupName:             group.Name,
				TargetDurationSeconds: int64(test.Duration / time.Second),
				ScriptID:              group.ScriptID,
				VUserCount:            int32(shard.VUsers),
				RampUpSeconds:         int32(group.rampUpDur / time.Second),
			})
			if err != nil {
				cleanup()
				tr.failTestBeforeRun(test, fmt.Sprintf("assign work for group %q on %q: %v", group.Name, target, err))
				return
			}
			if !assignment.Accepted {
				cleanup()
				tr.failTestBeforeRun(test, fmt.Sprintf("generator rejected group %q on %q: %s", group.Name, target, assignment.ErrorMessage))
				return
			}
			snapshot := loadByTarget[target]
			snapshot.AvailableCapacity = assignment.AvailableCapacity
			snapshot.Stats.ActiveVUsers += int32(shard.VUsers)
			loadByTarget[target] = snapshot
		}
	}

	for _, target := range orderedAssignedShardTargets(groupShards, allowedTargets) {
		if _, ok := streamsByTarget[target]; ok {
			continue
		}
		stream, err := clientsByTarget[target].OpenMetricsStream(context.Background())
		if err != nil {
			cleanup()
			tr.failTestBeforeRun(test, fmt.Sprintf("open generator metrics stream for %q: %v", target, err))
			return
		}
		streamsByTarget[target] = stream
	}

	b.startDistributedTestRun(tr, test, groupShards, clientsByTarget, streamsByTarget)
}

func (b *distributedExecutionBackend) StopTest(tr *TestRunner, test *ManagedTest) error {
	clients, err := b.clientsForTest(test)
	if err != nil {
		return err
	}
	var stopErr error
	for _, target := range orderedGeneratorTargets(clients, b.configuredTargets()) {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		stop, err := clients[target].StopTest(ctx, test.ID, true)
		cancel()
		if err != nil {
			code := grpcStatus.Code(err)
			if code == grpcCodes.Canceled || code == grpcCodes.Unavailable || code == grpcCodes.NotFound {
				if test.Status == "COMPLETE" || test.Status == "FAILED" {
					continue
				}
			}
			stopErr = fmt.Errorf("stop remote test on %s: %w", target, err)
			continue
		}
		if !stop.Acknowledged {
			stopErr = fmt.Errorf("generator %s did not acknowledge stop for %s", target, test.ID)
		}
	}
	if stopErr != nil {
		return stopErr
	}
	return nil
}

func (b *distributedExecutionBackend) ScaleVUsers(tr *TestRunner, test *ManagedTest, groupName string, targetVUsers int) error {
	groupExists := false
	for _, group := range test.Groups {
		if group.Name == groupName {
			groupExists = true
			break
		}
	}
	if !groupExists {
		return fmt.Errorf("test group not found: %s", groupName)
	}
	shards := test.remoteGroupShards[groupName]
	if len(shards) == 0 {
		if target, ok := test.remoteGroupTargets[groupName]; ok && target != "" {
			shards = []GeneratorShardSummary{{Group: groupName, Target: target, VUsers: testGroupCurrentShardVUsers(test, groupName)}}
		}
	}
	if len(shards) == 0 {
		client, err := b.clientForGroup(test, groupName)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		defer cancel()
		adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
			TestID:       test.ID,
			GroupName:    groupName,
			TargetVUsers: int32(targetVUsers),
		})
		if err != nil {
			return fmt.Errorf("adjust remote vusers: %w", err)
		}
		if !adjustment.Accepted {
			return fmt.Errorf("generator did not accept vuser adjustment for %s", test.ID)
		}
		return nil
	}

	clients, err := b.clientsForTest(test)
	if err != nil {
		return err
	}
	targets := shardTargets(shards)
	loadByTarget := make(map[string]generatorLoadSnapshot, len(targets))
	for _, target := range targets {
		client := clients[target]
		if client == nil {
			return fmt.Errorf("selected generator %q is not connected", target)
		}
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		snapshot := b.probeGeneratorLoad(ctx, target, client)
		cancel()
		snapshot.AvailableCapacity += int32(shardVUsersForTarget(shards, target))
		loadByTarget[target] = snapshot
	}
	nextShards := capacityWeightedShardsIncludingZero(groupName, targetVUsers, targets, loadByTarget, shardStrategyForGroupShards(shards))
	previousByTarget := make(map[string]int, len(shards))
	for _, shard := range shards {
		previousByTarget[shard.Target] = shard.VUsers
	}
	successes := make([]GeneratorShardSummary, 0, len(nextShards))
	for _, shard := range nextShards {
		client := clients[shard.Target]
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
			TestID:       test.ID,
			GroupName:    groupName,
			TargetVUsers: int32(shard.VUsers),
		})
		cancel()
		if err != nil || !adjustment.Accepted {
			rollbackErr := b.rollbackShardVUsers(test.ID, groupName, clients, successes, previousByTarget)
			if err != nil {
				if rollbackErr != nil {
					return fmt.Errorf("adjust remote vusers on %s: %w; rollback shard vusers: %v", shard.Target, err, rollbackErr)
				}
				return fmt.Errorf("adjust remote vusers on %s: %w", shard.Target, err)
			}
			if rollbackErr != nil {
				return fmt.Errorf("generator did not accept vuser adjustment for %s on %s; rollback shard vusers: %v", test.ID, shard.Target, rollbackErr)
			}
			return fmt.Errorf("generator did not accept vuser adjustment for %s on %s", test.ID, shard.Target)
		}
		successes = append(successes, shard)
	}
	tr.mu.Lock()
	if test.remoteGroupShards == nil {
		test.remoteGroupShards = make(map[string][]GeneratorShardSummary)
	}
	test.remoteGroupShards[groupName] = cloneGeneratorShardSummaries(nextShards)
	test.remoteGroupTargets = primaryTargetsFromShards(test.remoteGroupShards)
	test.GeneratorShards = flattenGroupShards(test.remoteGroupShards)
	tr.mu.Unlock()
	return nil
}

func (b *distributedExecutionBackend) AdjustDuration(tr *TestRunner, test *ManagedTest, newDuration time.Duration) error {
	clients, err := b.clientsForTest(test)
	if err != nil {
		return err
	}
	for _, target := range orderedGeneratorTargets(clients, b.configuredTargets()) {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		result, err := clients[target].AdjustDuration(ctx, generatorrpc.DurationAdjustment{
			TestID:                test.ID,
			TargetDurationSeconds: int64(newDuration / time.Second),
		})
		cancel()
		if err != nil {
			return fmt.Errorf("adjust remote duration on %s: %w", target, err)
		}
		if !result.Accepted {
			return fmt.Errorf("generator %s did not accept duration adjustment for %s", target, test.ID)
		}
	}
	return nil
}

func (b *distributedExecutionBackend) PauseTest(tr *TestRunner, test *ManagedTest) error {
	streams := b.metricsStreamsForTest(test)
	if len(streams) == 0 {
		return fmt.Errorf("distributed metrics stream is not initialized")
	}
	for _, target := range orderedMetricsTargets(streams, b.configuredTargets()) {
		if err := streams[target].SendPause(generatorrpc.PauseControl{TestID: test.ID, Pause: true}); err != nil {
			return fmt.Errorf("send remote pause control to %s: %w", target, err)
		}
	}
	return nil
}

func (b *distributedExecutionBackend) ResumeTest(tr *TestRunner, test *ManagedTest) error {
	streams := b.metricsStreamsForTest(test)
	if len(streams) == 0 {
		return fmt.Errorf("distributed metrics stream is not initialized")
	}
	for _, target := range orderedMetricsTargets(streams, b.configuredTargets()) {
		if err := streams[target].SendPause(generatorrpc.PauseControl{TestID: test.ID, Pause: false}); err != nil {
			return fmt.Errorf("send remote pause control to %s: %w", target, err)
		}
	}
	return nil
}

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
	tr.mu.Unlock()

	tr.broadcastEvent("test_started", test)
	for _, target := range orderedMetricsTargets(streamsByTarget, b.configuredTargets()) {
		go b.consumeMetrics(ctx, tr, test, target, streamsByTarget[target])
	}
}

func (b *distributedExecutionBackend) clientForTest(test *ManagedTest) (generatorClient, error) {
	if len(test.remoteClientsByTarget) == 1 {
		for _, client := range test.remoteClientsByTarget {
			return client, nil
		}
	}
	if test.remoteClient != nil {
		return test.remoteClient, nil
	}
	if test.remoteTarget == "" {
		return nil, fmt.Errorf("distributed generator target is not recorded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	client, err := b.dial(ctx, test.remoteTarget)
	if err != nil {
		return nil, fmt.Errorf("connect generator: %w", err)
	}
	return client, nil
}

func (b *distributedExecutionBackend) clientsForTest(test *ManagedTest) (map[string]generatorClient, error) {
	if len(test.remoteClientsByTarget) > 0 {
		return cloneClientMap(test.remoteClientsByTarget), nil
	}
	client, err := b.clientForTest(test)
	if err != nil {
		return nil, err
	}
	return map[string]generatorClient{test.remoteTarget: client}, nil
}

func (b *distributedExecutionBackend) metricsStreamsForTest(test *ManagedTest) map[string]*generatorrpc.MetricsStream {
	if len(test.remoteMetricsStreamsByTarget) > 0 {
		return cloneMetricsStreamMap(test.remoteMetricsStreamsByTarget)
	}
	if test.remoteMetricsStream == nil || test.remoteTarget == "" {
		return nil
	}
	return map[string]*generatorrpc.MetricsStream{test.remoteTarget: test.remoteMetricsStream}
}

func (b *distributedExecutionBackend) clientForGroup(test *ManagedTest, groupName string) (generatorClient, error) {
	if target, ok := test.remoteGroupTargets[groupName]; ok {
		if client := test.remoteClientsByTarget[target]; client != nil {
			return client, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		defer cancel()
		client, err := b.dial(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("connect generator: %w", err)
		}
		return client, nil
	}
	return b.clientForTest(test)
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

func (b *distributedExecutionBackend) closeRemoteResources(test *ManagedTest) {
	if len(test.remoteMetricsStreamsByTarget) > 0 {
		for _, stream := range test.remoteMetricsStreamsByTarget {
			_ = stream.CloseSend()
		}
	} else if test.remoteMetricsStream != nil {
		_ = test.remoteMetricsStream.CloseSend()
	}
	test.remoteMetricsStream = nil
	test.remoteMetricsStreamsByTarget = nil
	if len(test.remoteClientsByTarget) > 0 {
		for _, client := range test.remoteClientsByTarget {
			_ = client.Close()
		}
	} else if test.remoteClient != nil {
		_ = test.remoteClient.Close()
	}
	test.remoteClient = nil
	test.remoteClientsByTarget = nil
	test.remoteGroupTargets = nil
	test.remoteGroupShards = nil
	test.remoteMetricsBatchesByTarget = nil
	test.remoteTargetStates = nil
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

	aggregated := aggregateDistributedMetricsBatches(test.ID, batches)
	live := buildLiveMetricsFromDistributedBatch(test, aggregated)
	live.GeneratorResources = b.buildGeneratorResourceSnapshots(test, batches)
	return live, finalStatus, finalError
}

func (b *distributedExecutionBackend) completeDistributedTestRun(tr *TestRunner, test *ManagedTest, finalStatus string, finalError string) {
	if finalStatus == "FAILED" {
		if err := b.stopRemainingRemoteTargets(test); err != nil {
			finalError = appendRunError(finalError, "stop remote targets", err)
		}
	}
	archiveRecord, archiveErr := b.stageRemoteArchive(test, finalStatus == "COMPLETE")
	if archiveErr != nil {
		finalStatus = "FAILED"
		finalError = appendRunError(finalError, "remote archive stage failed", archiveErr)
	}
	b.closeRemoteResources(test)
	if test.cancel != nil {
		test.cancel()
	}
	if archiveRecord != nil {
		if err := tr.saveRunArchiveRecord(archiveRecord); err != nil {
			finalStatus = "FAILED"
			finalError = appendRunError(finalError, "archive metadata save failed", err)
			archiveRecord = nil
		}
	}
	tr.completeTestRun(test, finalStatus, time.Now(), finalError, archiveRecord)
}

func (b *distributedExecutionBackend) stageRemoteArchive(test *ManagedTest, requireAll bool) (*storage.RunArchiveRecord, error) {
	bundles, err := b.fetchRemoteArchiveBundles(test, requireAll)
	if err != nil {
		return nil, err
	}
	if len(bundles) == 0 {
		return nil, nil
	}
	manifest := mergeRemoteArchiveManifest(test, bundles)
	archiveDir := filepath.Join(resolveArchiveRoot(), "remote", test.ID)
	if err := os.RemoveAll(archiveDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, err
	}
	if err := writeMergedRemoteArchiveFiles(archiveDir, &manifest, bundles); err != nil {
		return nil, err
	}
	if err := writeArchiveParquetFiles(archiveDir, &manifest); err != nil {
		return nil, err
	}
	manifestContent, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "manifest.json"), manifestContent, 0o644); err != nil {
		return nil, err
	}
	if err := uploadArchiveDirectory(context.Background(), archiveDir, manifest.TestID); err != nil {
		return nil, err
	}
	archiveBackend := currentArchiveBackend()
	return &storage.RunArchiveRecord{
		TestID:               manifest.TestID,
		SchemaVersion:        manifest.SchemaVersion,
		ArchiveBackend:       archiveBackend.Backend,
		ArchivePath:          archiveDir,
		ManifestPath:         filepath.Join(archiveDir, "manifest.json"),
		ArchiveURI:           archiveBackend.runURI(manifest.TestID),
		ManifestURI:          archiveBackend.manifestURI(manifest.TestID),
		TransactionCount:     manifest.TransactionCount,
		ResourceSampleCount:  manifest.ResourceSampleCount,
		IntervalSummaryCount: manifest.IntervalSummaryCount,
		CreatedAt:            manifest.CreatedAt,
		CompletedAt:          manifest.CompletedAt,
	}, nil
}

type remoteArchiveBundle struct {
	target       string
	manifest     RunArchiveManifest
	transactions []byte
	resources    []byte
	summaries    []byte
}

func (b *distributedExecutionBackend) fetchRemoteArchiveBundles(test *ManagedTest, requireAll bool) ([]remoteArchiveBundle, error) {
	clients, err := b.clientsForTest(test)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	bundles := make([]remoteArchiveBundle, 0, len(clients))
	missingTargets := make([]string, 0)
	for _, target := range orderedGeneratorTargets(clients, b.configuredTargets()) {
		client := clients[target]
		manifestContent, err := client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: test.ID, Name: "manifest.json"})
		if err != nil {
			code := grpcStatus.Code(err)
			if code == grpcCodes.NotFound || code == grpcCodes.Unimplemented || (!requireAll && (code == grpcCodes.Canceled || code == grpcCodes.Unavailable)) {
				missingTargets = append(missingTargets, target)
				continue
			}
			return nil, fmt.Errorf("fetch archive manifest from %s: %w", target, err)
		}
		var manifest RunArchiveManifest
		if err := json.Unmarshal(manifestContent, &manifest); err != nil {
			return nil, fmt.Errorf("decode archive manifest from %s: %w", target, err)
		}
		transactions, err := client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: test.ID, Name: manifest.TransactionsFile})
		if err != nil {
			return nil, fmt.Errorf("fetch transaction archive from %s: %w", target, err)
		}
		resources, err := client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: test.ID, Name: manifest.ResourceSamplesFile})
		if err != nil {
			return nil, fmt.Errorf("fetch resource archive from %s: %w", target, err)
		}
		summaries, err := client.GetArchiveArtifact(ctx, generatorrpc.ArchiveArtifactRequest{TestID: test.ID, Name: manifest.IntervalSummariesFile})
		if err != nil {
			return nil, fmt.Errorf("fetch interval summaries archive from %s: %w", target, err)
		}
		bundles = append(bundles, remoteArchiveBundle{
			target:       target,
			manifest:     manifest,
			transactions: transactions,
			resources:    resources,
			summaries:    summaries,
		})
	}
	if len(bundles) == 0 {
		return nil, nil
	}
	if requireAll && len(missingTargets) > 0 {
		return nil, fmt.Errorf("archive not available from targets: %s", strings.Join(missingTargets, ", "))
	}
	return bundles, nil
}

func mergeRemoteArchiveManifest(test *ManagedTest, bundles []remoteArchiveBundle) RunArchiveManifest {
	manifest := RunArchiveManifest{
		SchemaVersion:                archiveSchemaVersion,
		TestID:                       test.ID,
		TestName:                     test.Name,
		CreatedAt:                    test.CreatedAt,
		TransactionsFile:             "transactions.jsonl",
		TransactionsParquetFile:      "transactions.parquet",
		ResourceSamplesFile:          "resources.jsonl",
		ResourceSamplesParquetFile:   "resources.parquet",
		IntervalSummariesFile:        "interval_summaries.jsonl",
		IntervalSummariesParquetFile: "interval_summaries.parquet",
	}
	errors := make([]string, 0)
	for index, bundle := range bundles {
		if index == 0 || bundle.manifest.CreatedAt.Before(manifest.CreatedAt) {
			manifest.CreatedAt = bundle.manifest.CreatedAt
		}
		if bundle.manifest.CompletedAt.After(manifest.CompletedAt) {
			manifest.CompletedAt = bundle.manifest.CompletedAt
		}
		manifest.TransactionCount += bundle.manifest.TransactionCount
		manifest.ResourceSampleCount += bundle.manifest.ResourceSampleCount
		manifest.IntervalSummaryCount += bundle.manifest.IntervalSummaryCount
		if bundle.manifest.Status == "FAILED" {
			manifest.Status = "FAILED"
		}
		if bundle.manifest.Error != "" {
			errors = append(errors, bundle.manifest.Error)
		}
	}
	if manifest.Status == "" {
		manifest.Status = "COMPLETE"
	}
	manifest.Error = strings.Join(errors, "; ")
	return manifest
}

func writeMergedRemoteArchiveFiles(rootDir string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle) error {
	transactionsFile, err := os.Create(filepath.Join(rootDir, manifest.TransactionsFile))
	if err != nil {
		return err
	}
	defer transactionsFile.Close()
	resourcesFile, err := os.Create(filepath.Join(rootDir, manifest.ResourceSamplesFile))
	if err != nil {
		return err
	}
	defer resourcesFile.Close()
	summariesFile, err := os.Create(filepath.Join(rootDir, manifest.IntervalSummariesFile))
	if err != nil {
		return err
	}
	defer summariesFile.Close()
	for _, bundle := range bundles {
		if err := appendArchiveContent(transactionsFile, bundle.transactions); err != nil {
			return err
		}
		if err := appendArchiveContent(resourcesFile, bundle.resources); err != nil {
			return err
		}
		if err := appendArchiveContent(summariesFile, bundle.summaries); err != nil {
			return err
		}
	}
	return nil
}

func appendArchiveContent(file *os.File, content []byte) error {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return nil
	}
	if _, err := file.Write(trimmed); err != nil {
		return err
	}
	if _, err := file.Write([]byte("\n")); err != nil {
		return err
	}
	return nil
}

func (b *distributedExecutionBackend) stopRemainingRemoteTargets(test *ManagedTest) error {
	clients, err := b.clientsForTest(test)
	if err != nil {
		return err
	}
	var stopErr error
	for _, target := range orderedGeneratorTargets(clients, b.configuredTargets()) {
		if state := test.remoteTargetStates[target]; state == "COMPLETE" || state == "FAILED" {
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

func parseGeneratorTargetSpec(raw string) generatorTargetSpec {
	parts := strings.Split(raw, "|")
	spec := generatorTargetSpec{Address: strings.TrimSpace(parts[0])}
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(key)) {
		case "region":
			spec.Region = strings.TrimSpace(value)
		case "version":
			spec.Version = strings.TrimSpace(value)
		case "protocols":
			for _, protocol := range strings.Split(value, ",") {
				protocol = strings.TrimSpace(protocol)
				if protocol != "" {
					spec.Protocols = append(spec.Protocols, protocol)
				}
			}
		}
	}
	return spec
}

func generatorSpecVersion(spec generatorTargetSpec) string {
	if strings.TrimSpace(spec.Version) == "" {
		return "unknown"
	}
	return strings.TrimSpace(spec.Version)
}

func generatorSpecProtocols(spec generatorTargetSpec) []string {
	if len(spec.Protocols) == 0 {
		return defaultSupportedProtocols()
	}
	protocols := normalizedProtocolList(spec.Protocols)
	sort.Strings(protocols)
	return protocols
}

func normalizedProtocolList(protocols []string) []string {
	out := make([]string, 0, len(protocols))
	seen := make(map[string]struct{}, len(protocols))
	for _, protocol := range protocols {
		protocol = strings.TrimSpace(protocol)
		if protocol == "" {
			continue
		}
		if _, ok := seen[protocol]; ok {
			continue
		}
		seen[protocol] = struct{}{}
		out = append(out, protocol)
	}
	sort.Strings(out)
	return out
}

func generatorEffectiveSpec(spec generatorTargetSpec, snapshot generatorLoadSnapshot) generatorTargetSpec {
	effective := spec
	if version := strings.TrimSpace(snapshot.Version); version != "" {
		effective.Version = version
	}
	if len(snapshot.SupportedProtocols) > 0 {
		effective.Protocols = normalizedProtocolList(snapshot.SupportedProtocols)
	}
	return effective
}

func generatorCompatibilityState(spec generatorTargetSpec) string {
	if len(generatorCompatibilityIssues(spec)) > 0 {
		return "incompatible"
	}
	return "compatible"
}

func generatorCompatibilityIssues(spec generatorTargetSpec) []string {
	issues := []string{}
	version := generatorSpecVersion(spec)
	if version != "unknown" && !compatibleMajorVersion(version, "0.1.0") {
		issues = append(issues, fmt.Sprintf("generator version %s is incompatible with controller version 0.1.0", version))
	}
	return issues
}

func compatibleMajorVersion(candidate string, required string) bool {
	candidateMajor, candidateOK := leadingVersionPart(candidate)
	requiredMajor, requiredOK := leadingVersionPart(required)
	if !candidateOK || !requiredOK {
		return true
	}
	return candidateMajor == requiredMajor
}

func leadingVersionPart(version string) (string, bool) {
	version = strings.TrimSpace(version)
	if version == "" || version == "unknown" {
		return "", false
	}
	part, _, _ := strings.Cut(version, ".")
	part = strings.TrimSpace(part)
	if part == "" {
		return "", false
	}
	return part, true
}

func targetSupportsProtocol(spec generatorTargetSpec, protocolName string) bool {
	protocolName = strings.TrimSpace(protocolName)
	if protocolName == "" {
		return true
	}
	for _, supported := range generatorSpecProtocols(spec) {
		if supported == protocolName {
			return true
		}
	}
	return false
}

type generatorCompatibilityIndex struct {
	byTarget map[string]generatorTargetSpec
}

func newGeneratorCompatibilityIndex(specs []generatorTargetSpec, loadByTarget map[string]generatorLoadSnapshot) *generatorCompatibilityIndex {
	index := &generatorCompatibilityIndex{byTarget: make(map[string]generatorTargetSpec, len(specs))}
	for _, spec := range specs {
		if snapshot, ok := loadByTarget[spec.Address]; ok {
			spec = generatorEffectiveSpec(spec, snapshot)
		}
		index.byTarget[spec.Address] = spec
	}
	return index
}

func (i *generatorCompatibilityIndex) targetCanRun(target string, protocolName string) bool {
	if i == nil {
		return true
	}
	spec, ok := i.byTarget[target]
	if !ok {
		return true
	}
	return generatorCompatibilityState(spec) == "compatible" && targetSupportsProtocol(spec, protocolName)
}

func generatorPlacementRejectionMessage(group TestGroup, loadByTarget map[string]generatorLoadSnapshot, specs []generatorTargetSpec) string {
	protocolName := usageKey(group.Protocol, "unknown")
	reasons := make([]string, 0)
	for _, spec := range specs {
		if _, reachable := loadByTarget[spec.Address]; !reachable {
			continue
		}
		if snapshot, ok := loadByTarget[spec.Address]; ok {
			spec = generatorEffectiveSpec(spec, snapshot)
		}
		if issues := generatorCompatibilityIssues(spec); len(issues) > 0 {
			reasons = append(reasons, fmt.Sprintf("%s incompatible: %s", spec.Address, strings.Join(issues, "; ")))
			continue
		}
		if !targetSupportsProtocol(spec, group.Protocol) {
			reasons = append(reasons, fmt.Sprintf("%s does not support protocol %s", spec.Address, protocolName))
		}
	}
	if len(reasons) == 0 {
		return fmt.Sprintf("no compatible generator target is available for group %q using protocol %s", group.Name, protocolName)
	}
	sort.Strings(reasons)
	return fmt.Sprintf("no compatible generator target is available for group %q using protocol %s: %s", group.Name, protocolName, strings.Join(reasons, "; "))
}

type regionPlacementState struct {
	remaining      map[string]float64
	targetRegion   map[string]string
	weightedRegion map[string]struct{}
}

func newRegionPlacementState(groups []TestGroup, assignment *GeneratorAssignmentSettings, targetSpecs []generatorTargetSpec) *regionPlacementState {
	weights := effectiveRegionWeights(assignment)
	if assignment == nil || (assignment.Mode != generatorAssignmentModeRegionWeighted && assignment.Mode != generatorAssignmentModeLoadZoneWeighted) || len(weights) == 0 {
		return nil
	}
	available := make(map[string]struct{})
	targetRegion := make(map[string]string, len(targetSpecs))
	for _, spec := range targetSpecs {
		targetRegion[spec.Address] = spec.Region
		if spec.Region == "" {
			continue
		}
		available[spec.Region] = struct{}{}
	}
	totalWeight := 0
	remaining := make(map[string]float64)
	weightedRegions := make(map[string]struct{})
	totalVUsers := 0
	for _, group := range groups {
		totalVUsers += group.VUsers
	}
	for region, weight := range weights {
		if weight <= 0 {
			continue
		}
		if _, ok := available[region]; !ok {
			continue
		}
		totalWeight += weight
		weightedRegions[region] = struct{}{}
	}
	if totalWeight == 0 || totalVUsers == 0 {
		return nil
	}
	for region, weight := range weights {
		if _, ok := weightedRegions[region]; !ok {
			continue
		}
		remaining[region] = float64(totalVUsers) * float64(weight) / float64(totalWeight)
	}
	return &regionPlacementState{remaining: remaining, targetRegion: targetRegion, weightedRegion: weightedRegions}
}

func (s *regionPlacementState) preference(target string) (float64, bool) {
	if s == nil {
		return 0, false
	}
	region, ok := s.targetRegion[target]
	if !ok || region == "" {
		return 0, false
	}
	_, weighted := s.weightedRegion[region]
	if !weighted {
		return 0, false
	}
	return s.remaining[region], true
}

func (s *regionPlacementState) consume(target string, vusers int) {
	if s == nil {
		return
	}
	region, ok := s.targetRegion[target]
	if !ok || region == "" {
		return
	}
	if _, weighted := s.weightedRegion[region]; !weighted {
		return
	}
	s.remaining[region] -= float64(vusers)
}

func planDistributedGroupTargets(groups []TestGroup, loadByTarget map[string]generatorLoadSnapshot, preferred []string, assignment *GeneratorAssignmentSettings, targetSpecs []generatorTargetSpec) map[string]string {
	return primaryTargetsFromShards(planDistributedGroupShards(groups, loadByTarget, preferred, assignment, targetSpecs))
}

func planDistributedGroupShards(groups []TestGroup, loadByTarget map[string]generatorLoadSnapshot, preferred []string, assignment *GeneratorAssignmentSettings, targetSpecs []generatorTargetSpec) map[string][]GeneratorShardSummary {
	if len(loadByTarget) == 0 {
		return nil
	}
	projected := make(map[string]generatorLoadSnapshot, len(loadByTarget))
	for key, value := range loadByTarget {
		projected[key] = value
	}
	regionState := newRegionPlacementState(groups, assignment, targetSpecs)
	compatibility := newGeneratorCompatibilityIndex(targetSpecs, loadByTarget)
	shardsByGroup := make(map[string][]GeneratorShardSummary, len(groups))
	for _, group := range groups {
		shards := chooseGeneratorShardsForGroup(group, projected, preferred, regionState, compatibility)
		shardsByGroup[group.Name] = shards
		for _, shard := range shards {
			if shard.Target == "" || shard.VUsers <= 0 {
				continue
			}
			snapshot := projected[shard.Target]
			snapshot.AvailableCapacity -= int32(shard.VUsers)
			snapshot.Stats.ActiveVUsers += int32(shard.VUsers)
			projected[shard.Target] = snapshot
			regionState.consume(shard.Target, shard.VUsers)
		}
	}
	return shardsByGroup
}

func chooseGeneratorShardsForGroup(group TestGroup, loadByTarget map[string]generatorLoadSnapshot, preferred []string, regionState *regionPlacementState, compatibility *generatorCompatibilityIndex) []GeneratorShardSummary {
	if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeSpecificHosts {
		targets := make([]string, 0, len(group.Assignment.SelectedTargets))
		for _, target := range group.Assignment.SelectedTargets {
			if _, ok := loadByTarget[target]; !ok {
				return nil
			}
			if compatibility != nil && !compatibility.targetCanRun(target, group.Protocol) {
				return nil
			}
			targets = append(targets, target)
		}
		return capacityWeightedShards(group.Name, group.VUsers, targets, loadByTarget, group.Assignment.ShardStrategy)
	}

	target := chooseGeneratorTargetForGroup(group, loadByTarget, preferred, regionState, compatibility)
	if target == "" {
		return nil
	}
	return []GeneratorShardSummary{{Group: group.Name, Target: target, VUsers: group.VUsers}}
}

func capacityWeightedShards(groupName string, vusers int, targets []string, loadByTarget map[string]generatorLoadSnapshot, strategy string) []GeneratorShardSummary {
	return buildCapacityWeightedShards(groupName, vusers, targets, loadByTarget, strategy, false)
}

func capacityWeightedShardsIncludingZero(groupName string, vusers int, targets []string, loadByTarget map[string]generatorLoadSnapshot, strategy string) []GeneratorShardSummary {
	return buildCapacityWeightedShards(groupName, vusers, targets, loadByTarget, strategy, true)
}

func buildCapacityWeightedShards(groupName string, vusers int, targets []string, loadByTarget map[string]generatorLoadSnapshot, strategy string, includeZero bool) []GeneratorShardSummary {
	if len(targets) == 0 || vusers <= 0 {
		if !includeZero {
			return nil
		}
		shards := make([]GeneratorShardSummary, 0, len(targets))
		for _, target := range targets {
			shards = append(shards, GeneratorShardSummary{
				Group:         groupName,
				Target:        target,
				VUsers:        0,
				ShardStrategy: firstNonEmpty(strategy, generatorShardStrategyCapacityWeighted),
			})
		}
		return shards
	}
	if len(targets) == 1 {
		return []GeneratorShardSummary{{Group: groupName, Target: targets[0], VUsers: vusers}}
	}
	if strategy == "" {
		strategy = generatorShardStrategyCapacityWeighted
	}
	allocations := allocateCapacityWeightedVUsers(vusers, targets, loadByTarget)
	shards := make([]GeneratorShardSummary, 0, len(targets))
	for _, target := range targets {
		if allocations[target] <= 0 && !includeZero {
			continue
		}
		shards = append(shards, GeneratorShardSummary{
			Group:         groupName,
			Target:        target,
			VUsers:        allocations[target],
			ShardStrategy: strategy,
		})
	}
	return shards
}

func allocateCapacityWeightedVUsers(total int, targets []string, loadByTarget map[string]generatorLoadSnapshot) map[string]int {
	allocations := make(map[string]int, len(targets))
	if total <= 0 || len(targets) == 0 {
		return allocations
	}
	remaining := total
	if total >= len(targets) {
		for _, target := range targets {
			allocations[target] = 1
		}
		remaining -= len(targets)
	}
	if remaining <= 0 {
		return allocations
	}

	type weightedAllocation struct {
		target    string
		weight    float64
		base      int
		remainder float64
		index     int
	}
	weighted := make([]weightedAllocation, 0, len(targets))
	totalWeight := 0.0
	for index, target := range targets {
		weight := float64(loadByTarget[target].AvailableCapacity)
		if weight < 0 {
			weight = 0
		}
		weighted = append(weighted, weightedAllocation{target: target, weight: weight, index: index})
		totalWeight += weight
	}
	if totalWeight <= 0 {
		totalWeight = float64(len(weighted))
		for index := range weighted {
			weighted[index].weight = 1
		}
	}

	assigned := 0
	for index := range weighted {
		exact := float64(remaining) * weighted[index].weight / totalWeight
		base := int(math.Floor(exact))
		weighted[index].base = base
		weighted[index].remainder = exact - float64(base)
		allocations[weighted[index].target] += base
		assigned += base
	}
	sort.SliceStable(weighted, func(left int, right int) bool {
		if weighted[left].remainder != weighted[right].remainder {
			return weighted[left].remainder > weighted[right].remainder
		}
		if weighted[left].weight != weighted[right].weight {
			return weighted[left].weight > weighted[right].weight
		}
		return weighted[left].index < weighted[right].index
	})
	for index, remainingAssigned := 0, remaining-assigned; remainingAssigned > 0; index, remainingAssigned = index+1, remainingAssigned-1 {
		next := weighted[index%len(weighted)]
		allocations[next.target]++
	}
	return allocations
}

func chooseGeneratorTargetForGroup(group TestGroup, loadByTarget map[string]generatorLoadSnapshot, preferred []string, regionState *regionPlacementState, compatibility *generatorCompatibilityIndex) string {
	ordered := orderedLoadTargets(loadByTarget, preferred, regionState)
	if len(ordered) == 0 {
		return ""
	}
	for _, target := range ordered {
		if compatibility != nil && !compatibility.targetCanRun(target, group.Protocol) {
			continue
		}
		if loadByTarget[target].AvailableCapacity >= int32(group.VUsers) {
			return target
		}
	}
	for _, target := range ordered {
		if compatibility == nil || compatibility.targetCanRun(target, group.Protocol) {
			return target
		}
	}
	return ""
}

func orderedLoadTargets(loadByTarget map[string]generatorLoadSnapshot, preferred []string, regionState *regionPlacementState) []string {
	ordered := make([]string, 0, len(loadByTarget))
	for target := range loadByTarget {
		ordered = append(ordered, target)
	}
	preferenceIndex := make(map[string]int, len(preferred))
	for index, target := range preferred {
		preferenceIndex[target] = index
	}
	sort.SliceStable(ordered, func(left int, right int) bool {
		leftSnapshot := loadByTarget[ordered[left]]
		rightSnapshot := loadByTarget[ordered[right]]
		if regionState != nil {
			leftQuota, leftWeighted := regionState.preference(ordered[left])
			rightQuota, rightWeighted := regionState.preference(ordered[right])
			if leftWeighted != rightWeighted {
				return leftWeighted
			}
			if leftWeighted && leftQuota != rightQuota {
				return leftQuota > rightQuota
			}
		}
		if leftSnapshot.AvailableCapacity != rightSnapshot.AvailableCapacity {
			return leftSnapshot.AvailableCapacity > rightSnapshot.AvailableCapacity
		}
		if leftSnapshot.Stats.ActiveVUsers != rightSnapshot.Stats.ActiveVUsers {
			return leftSnapshot.Stats.ActiveVUsers < rightSnapshot.Stats.ActiveVUsers
		}
		leftIndex, leftOK := preferenceIndex[ordered[left]]
		rightIndex, rightOK := preferenceIndex[ordered[right]]
		if leftOK && rightOK && leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		if leftOK != rightOK {
			return leftOK
		}
		return ordered[left] < ordered[right]
	})
	return ordered
}

func orderedAssignedTargets(groupTargets map[string]string, preferred []string) []string {
	set := make(map[string]struct{})
	for _, target := range groupTargets {
		set[target] = struct{}{}
	}
	return orderedTargetList(set, preferred)
}

func orderedAssignedShardTargets(groupShards map[string][]GeneratorShardSummary, preferred []string) []string {
	set := make(map[string]struct{})
	for _, shards := range groupShards {
		for _, shard := range shards {
			if shard.Target == "" || shard.VUsers <= 0 {
				continue
			}
			set[shard.Target] = struct{}{}
		}
	}
	return orderedTargetList(set, preferred)
}

func orderedGeneratorTargets(clients map[string]generatorClient, preferred []string) []string {
	set := make(map[string]struct{}, len(clients))
	for target := range clients {
		set[target] = struct{}{}
	}
	return orderedTargetList(set, preferred)
}

func orderedMetricsTargets(streams map[string]*generatorrpc.MetricsStream, preferred []string) []string {
	set := make(map[string]struct{}, len(streams))
	for target := range streams {
		set[target] = struct{}{}
	}
	return orderedTargetList(set, preferred)
}

func orderedTargetList(targetSet map[string]struct{}, preferred []string) []string {
	ordered := make([]string, 0, len(targetSet))
	seen := make(map[string]struct{}, len(targetSet))
	for _, target := range preferred {
		if _, ok := targetSet[target]; ok {
			ordered = append(ordered, target)
			seen[target] = struct{}{}
		}
	}
	extras := make([]string, 0)
	for target := range targetSet {
		if _, ok := seen[target]; ok {
			continue
		}
		extras = append(extras, target)
	}
	sort.Strings(extras)
	return append(ordered, extras...)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func primaryTargetsFromShards(groupShards map[string][]GeneratorShardSummary) map[string]string {
	if len(groupShards) == 0 {
		return nil
	}
	primary := make(map[string]string, len(groupShards))
	for group, shards := range groupShards {
		for _, shard := range shards {
			if shard.Target == "" || shard.VUsers <= 0 {
				continue
			}
			primary[group] = shard.Target
			break
		}
	}
	return primary
}

func cloneGroupShardMap(values map[string][]GeneratorShardSummary) map[string][]GeneratorShardSummary {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string][]GeneratorShardSummary, len(values))
	for key, shards := range values {
		cloned[key] = cloneGeneratorShardSummaries(shards)
	}
	return cloned
}

func flattenGroupShards(values map[string][]GeneratorShardSummary) []GeneratorShardSummary {
	if len(values) == 0 {
		return nil
	}
	groups := make([]string, 0, len(values))
	for group := range values {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	out := make([]GeneratorShardSummary, 0)
	for _, group := range groups {
		shards := cloneGeneratorShardSummaries(values[group])
		sort.SliceStable(shards, func(left int, right int) bool {
			return shards[left].Target < shards[right].Target
		})
		out = append(out, shards...)
	}
	return out
}

func shardTargets(shards []GeneratorShardSummary) []string {
	targets := make([]string, 0, len(shards))
	seen := make(map[string]struct{}, len(shards))
	for _, shard := range shards {
		if shard.Target == "" {
			continue
		}
		if _, ok := seen[shard.Target]; ok {
			continue
		}
		seen[shard.Target] = struct{}{}
		targets = append(targets, shard.Target)
	}
	return targets
}

func shardVUsersForTarget(shards []GeneratorShardSummary, target string) int {
	total := 0
	for _, shard := range shards {
		if shard.Target == target {
			total += shard.VUsers
		}
	}
	return total
}

func shardStrategyForGroupShards(shards []GeneratorShardSummary) string {
	for _, shard := range shards {
		if shard.ShardStrategy != "" {
			return shard.ShardStrategy
		}
	}
	return ""
}

func cloneClientMap(values map[string]generatorClient) map[string]generatorClient {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]generatorClient, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneMetricsStreamMap(values map[string]*generatorrpc.MetricsStream) map[string]*generatorrpc.MetricsStream {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]*generatorrpc.MetricsStream, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func aggregateDistributedMetricsBatches(testID string, batches map[string]generatorrpc.MetricsBatch) generatorrpc.MetricsBatch {
	combined := generatorrpc.MetricsBatch{TestID: testID, GeneratorID: "distributed"}
	targets := make([]string, 0, len(batches))
	hasCollectedAt := false
	hasPublishedAt := false
	hasControllerReceivedAt := false
	groupsByName := make(map[string]int32)
	for target := range batches {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		batch := batches[target]
		if batch.TimestampUnixMs > combined.TimestampUnixMs {
			combined.TimestampUnixMs = batch.TimestampUnixMs
		}
		combined.Stats = mergeDistributedGeneratorStats(combined.Stats, batch.Stats)
		combined.Transactions = append(combined.Transactions, batch.Transactions...)
		for _, group := range batch.Groups {
			if group.Name == "" {
				continue
			}
			groupsByName[group.Name] += group.ActiveVUsers
		}
		if batch.Telemetry != nil {
			if combined.Telemetry == nil {
				combined.Telemetry = &generatorrpc.MetricsTelemetry{}
			}
			combined.Telemetry.DroppedResults += batch.Telemetry.DroppedResults
			if batch.Telemetry.CollectedAtUnixMs > 0 && (!hasCollectedAt || batch.Telemetry.CollectedAtUnixMs < combined.Telemetry.CollectedAtUnixMs) {
				combined.Telemetry.CollectedAtUnixMs = batch.Telemetry.CollectedAtUnixMs
				hasCollectedAt = true
			}
			if batch.Telemetry.PublishedAtUnixMs > 0 && (!hasPublishedAt || batch.Telemetry.PublishedAtUnixMs > combined.Telemetry.PublishedAtUnixMs) {
				combined.Telemetry.PublishedAtUnixMs = batch.Telemetry.PublishedAtUnixMs
				hasPublishedAt = true
			}
			if batch.Telemetry.ControllerReceivedAtUnixMs > 0 && (!hasControllerReceivedAt || batch.Telemetry.ControllerReceivedAtUnixMs > combined.Telemetry.ControllerReceivedAtUnixMs) {
				combined.Telemetry.ControllerReceivedAtUnixMs = batch.Telemetry.ControllerReceivedAtUnixMs
				hasControllerReceivedAt = true
			}
			if batch.Telemetry.SampleAgeMs > combined.Telemetry.SampleAgeMs {
				combined.Telemetry.SampleAgeMs = batch.Telemetry.SampleAgeMs
			}
		}
	}
	groupNames := make([]string, 0, len(groupsByName))
	for name := range groupsByName {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	for _, name := range groupNames {
		combined.Groups = append(combined.Groups, generatorrpc.GroupMetric{Name: name, ActiveVUsers: groupsByName[name]})
	}
	return combined
}

func applyGeneratorStatsToInventory(entry *GeneratorInventoryEntry, stats generatorrpc.GeneratorStats) {
	if entry == nil {
		return
	}
	entry.ActiveVUsers = stats.ActiveVUsers
	entry.CPUPercent = stats.CPUPercent
	entry.MemoryBytes = stats.MemoryBytes
	entry.GoroutineCount = stats.GoroutineCount
	entry.CPUUserPct = stats.CPUUserPct
	entry.CPUSystemPct = stats.CPUSystemPct
	entry.CPUIOWaitPct = stats.CPUIOWaitPct
	entry.CPUStealPct = stats.CPUStealPct
	entry.ThreadCount = stats.ThreadCount
	entry.OpenFDCount = stats.OpenFDCount
	entry.HeapAllocMB = stats.HeapAllocMB
	entry.HeapInUseMB = stats.HeapInUseMB
	entry.GCCountDelta = stats.GCCountDelta
	entry.GCPauseMs = stats.GCPauseMs
	entry.ContainerCPUUsagePct = stats.ContainerCPUUsagePct
	entry.ContainerCPUThrottledPeriodsDelta = stats.ContainerCPUThrottledPeriodsDelta
	entry.ContainerCPUThrottledSecondsRate = stats.ContainerCPUThrottledSecondsRate
	entry.ContainerMemoryWorkingSetMB = stats.ContainerMemoryWorkingSetMB
	entry.ContainerMemoryUsagePct = stats.ContainerMemoryUsagePct
	entry.ContainerMemoryFailCountDelta = stats.ContainerMemoryFailCountDelta
	entry.ContainerOOMKillCountDelta = stats.ContainerOOMKillCountDelta
	entry.NetDropInPerSec = stats.NetDropInPerSec
	entry.NetDropOutPerSec = stats.NetDropOutPerSec
	entry.NetErrorInPerSec = stats.NetErrorInPerSec
	entry.NetErrorOutPerSec = stats.NetErrorOutPerSec
	entry.TCPRetransmitsPerSec = stats.TCPRetransmitsPerSec
	entry.TCPEstablished = int(stats.TCPEstablished)
	entry.TCPTimeWait = int(stats.TCPTimeWait)
	entry.DiskIOTimePct = stats.DiskIOTimePct
}

func resourcesFromGeneratorStats(stats generatorrpc.GeneratorStats) *engine.SystemResources {
	return &engine.SystemResources{
		CPUProcessPct:                     stats.CPUPercent,
		MemProcessMb:                      float64(stats.MemoryBytes) / (1024 * 1024),
		CPUUserPct:                        stats.CPUUserPct,
		CPUSystemPct:                      stats.CPUSystemPct,
		CPUIOWaitPct:                      stats.CPUIOWaitPct,
		CPUStealPct:                       stats.CPUStealPct,
		Load1:                             stats.Load1,
		Load5:                             stats.Load5,
		Load15:                            stats.Load15,
		ThreadCount:                       stats.ThreadCount,
		OpenFDCount:                       stats.OpenFDCount,
		GoroutineCount:                    stats.GoroutineCount,
		HeapAllocMB:                       stats.HeapAllocMB,
		HeapInUseMB:                       stats.HeapInUseMB,
		GCCountDelta:                      stats.GCCountDelta,
		GCPauseMs:                         stats.GCPauseMs,
		ContainerCPULimitCores:            stats.ContainerCPULimitCores,
		ContainerCPUUsagePct:              stats.ContainerCPUUsagePct,
		ContainerCPUThrottledPeriodsDelta: stats.ContainerCPUThrottledPeriodsDelta,
		ContainerCPUThrottledSecondsRate:  stats.ContainerCPUThrottledSecondsRate,
		ContainerMemoryWorkingSetMB:       stats.ContainerMemoryWorkingSetMB,
		ContainerMemoryLimitMB:            stats.ContainerMemoryLimitMB,
		ContainerMemoryUsagePct:           stats.ContainerMemoryUsagePct,
		ContainerMemoryFailCountDelta:     stats.ContainerMemoryFailCountDelta,
		ContainerOOMKillCountDelta:        stats.ContainerOOMKillCountDelta,
		NetDropInPerSec:                   stats.NetDropInPerSec,
		NetDropOutPerSec:                  stats.NetDropOutPerSec,
		NetErrorInPerSec:                  stats.NetErrorInPerSec,
		NetErrorOutPerSec:                 stats.NetErrorOutPerSec,
		TCPRetransmitsPerSec:              stats.TCPRetransmitsPerSec,
		TCPEstablished:                    int(stats.TCPEstablished),
		TCPTimeWait:                       int(stats.TCPTimeWait),
		EphemeralPortUtilizationPct:       stats.EphemeralPortUtilizationPct,
		DiskIOTimePct:                     stats.DiskIOTimePct,
		DiskWeightedIOTimePct:             stats.DiskWeightedIOTimePct,
		DiskInProgress:                    stats.DiskInProgress,
	}
}

func mergeDistributedGeneratorStats(left generatorrpc.GeneratorStats, right generatorrpc.GeneratorStats) generatorrpc.GeneratorStats {
	left.ActiveVUsers += right.ActiveVUsers
	left.CPUPercent += right.CPUPercent
	left.MemoryBytes += right.MemoryBytes
	left.GoroutineCount += right.GoroutineCount
	left.ThreadCount += right.ThreadCount
	left.OpenFDCount += right.OpenFDCount
	left.GCCountDelta += right.GCCountDelta
	left.GCPauseMs += right.GCPauseMs
	left.ContainerCPULimitCores += right.ContainerCPULimitCores
	left.ContainerCPUThrottledPeriodsDelta += right.ContainerCPUThrottledPeriodsDelta
	left.ContainerCPUThrottledSecondsRate += right.ContainerCPUThrottledSecondsRate
	left.ContainerMemoryWorkingSetMB += right.ContainerMemoryWorkingSetMB
	left.ContainerMemoryLimitMB += right.ContainerMemoryLimitMB
	left.ContainerMemoryFailCountDelta += right.ContainerMemoryFailCountDelta
	left.ContainerOOMKillCountDelta += right.ContainerOOMKillCountDelta
	left.NetDropInPerSec += right.NetDropInPerSec
	left.NetDropOutPerSec += right.NetDropOutPerSec
	left.NetErrorInPerSec += right.NetErrorInPerSec
	left.NetErrorOutPerSec += right.NetErrorOutPerSec
	left.TCPRetransmitsPerSec += right.TCPRetransmitsPerSec
	left.TCPEstablished += right.TCPEstablished
	left.TCPTimeWait += right.TCPTimeWait
	left.DiskInProgress += right.DiskInProgress
	left.CPUUserPct = maxFloat(left.CPUUserPct, right.CPUUserPct)
	left.CPUSystemPct = maxFloat(left.CPUSystemPct, right.CPUSystemPct)
	left.CPUIOWaitPct = maxFloat(left.CPUIOWaitPct, right.CPUIOWaitPct)
	left.CPUStealPct = maxFloat(left.CPUStealPct, right.CPUStealPct)
	left.Load1 = maxFloat(left.Load1, right.Load1)
	left.Load5 = maxFloat(left.Load5, right.Load5)
	left.Load15 = maxFloat(left.Load15, right.Load15)
	left.HeapAllocMB += right.HeapAllocMB
	left.HeapInUseMB += right.HeapInUseMB
	left.ContainerCPUUsagePct = maxFloat(left.ContainerCPUUsagePct, right.ContainerCPUUsagePct)
	left.ContainerMemoryUsagePct = maxFloat(left.ContainerMemoryUsagePct, right.ContainerMemoryUsagePct)
	left.EphemeralPortUtilizationPct = maxFloat(left.EphemeralPortUtilizationPct, right.EphemeralPortUtilizationPct)
	left.DiskIOTimePct = maxFloat(left.DiskIOTimePct, right.DiskIOTimePct)
	left.DiskWeightedIOTimePct = maxFloat(left.DiskWeightedIOTimePct, right.DiskWeightedIOTimePct)
	return left
}

func (b *distributedExecutionBackend) buildGeneratorResourceSnapshots(test *ManagedTest, batches map[string]generatorrpc.MetricsBatch) map[string]*GeneratorResourceSnapshot {
	if len(batches) == 0 {
		return nil
	}
	out := make(map[string]*GeneratorResourceSnapshot, len(batches))
	for target, batch := range batches {
		resources := resourcesFromGeneratorStats(batch.Stats)
		p95, p99, errorPct, tps := summarizeGeneratorBatchTransactions(batch.Transactions)
		state, reasons := classifyGeneratorImpact(resources, p95, errorPct)
		id := firstNonEmpty(batch.GeneratorID, target)
		timestamp := time.UnixMilli(batch.TimestampUnixMs)
		if batch.TimestampUnixMs == 0 {
			timestamp = time.Now()
		}
		out[id] = &GeneratorResourceSnapshot{
			GeneratorID:     id,
			Target:          target,
			Region:          b.regionForTarget(target),
			ActiveVUsers:    batch.Stats.ActiveVUsers,
			TPS:             tps,
			ErrorPct:        errorPct,
			P95Ms:           p95,
			P99Ms:           p99,
			SaturationState: state,
			ImpactReasons:   reasons,
			Resources:       resources,
			Timestamp:       timestamp,
		}
	}
	if len(out) == 0 || test == nil {
		return out
	}
	return out
}

func (b *distributedExecutionBackend) regionForTarget(target string) string {
	for _, spec := range b.targets {
		if spec.Address == target {
			return spec.Region
		}
	}
	return ""
}

func summarizeGeneratorBatchTransactions(transactions []generatorrpc.TransactionMetric) (float64, float64, float64, float64) {
	if len(transactions) == 0 {
		return 0, 0, 0, 0
	}
	durations := make([]float64, 0, len(transactions))
	errors := 0
	successes := 0
	for _, tx := range transactions {
		durations = append(durations, tx.DurationMs)
		if !tx.Success {
			errors++
		} else {
			successes++
		}
	}
	sort.Float64s(durations)
	errorPct := float64(errors) / float64(len(transactions)) * 100
	return percentileValue(durations, 0.95), percentileValue(durations, 0.99), errorPct, float64(successes)
}

func classifyGeneratorImpact(resources *engine.SystemResources, p95Ms float64, errorPct float64) (string, []string) {
	if resources == nil {
		return "unknown", nil
	}
	reasons := make([]string, 0, 6)
	appendReason := func(condition bool, reason string) {
		if condition {
			reasons = append(reasons, reason)
		}
	}
	appendReason(resources.ContainerCPUThrottledPeriodsDelta > 0 || resources.ContainerCPUThrottledSecondsRate > 0, "CPU throttling")
	appendReason(resources.ContainerCPUUsagePct >= 95 || resources.CPUProcessPct >= 95, "CPU saturation")
	appendReason(resources.ContainerMemoryUsagePct >= 90, "Memory near limit")
	appendReason(resources.ContainerOOMKillCountDelta > 0, "OOM kill observed")
	appendReason(resources.GCPauseMs >= 100, "GC pause pressure")
	appendReason(resources.NetDropInPerSec > 0 || resources.NetDropOutPerSec > 0 || resources.NetErrorInPerSec > 0 || resources.NetErrorOutPerSec > 0 || resources.TCPRetransmitsPerSec > 0, "Network loss/retransmits")
	appendReason(resources.DiskIOTimePct >= 90 || resources.DiskWeightedIOTimePct >= 90, "Disk I/O pressure")
	appendReason(resources.CPUIOWaitPct >= 20, "CPU I/O wait")
	appendReason(resources.CPUStealPct >= 10, "CPU steal")
	if len(reasons) == 0 {
		return "clear", nil
	}
	if errorPct > 0 || p95Ms >= slowTransactionDiagnosticThresholdMs {
		return "danger", reasons
	}
	return "warning", reasons
}

func maxFloat(left float64, right float64) float64 {
	if right > left {
		return right
	}
	return left
}

func buildLiveMetricsFromDistributedBatch(test *ManagedTest, batch generatorrpc.MetricsBatch) *LiveMetrics {
	timestamp := time.UnixMilli(batch.TimestampUnixMs)
	if batch.TimestampUnixMs == 0 {
		timestamp = time.Now()
	}

	live := &LiveMetrics{
		Timestamp:        timestamp,
		ActiveVUsers:     int64(batch.Stats.ActiveVUsers),
		Transactions:     make(map[string]*TxMetrics),
		DependencyHealth: make(map[string]*TxMetrics),
		SOAPOperations:   make(map[string]*TxMetrics),
		GroupMetrics:     make(map[string]*GroupLiveMetrics),
		Telemetry:        &TelemetryHealth{},
		Resources: &engine.SystemResources{
			CPUProcessPct: batch.Stats.CPUPercent,
			MemProcessMb:  float64(batch.Stats.MemoryBytes) / (1024 * 1024),
		},
	}
	live.Resources = resourcesFromGeneratorStats(batch.Stats)
	if batch.Telemetry != nil {
		live.Telemetry.DroppedResults = batch.Telemetry.DroppedResults
		if batch.Telemetry.CollectedAtUnixMs > 0 {
			live.Telemetry.CollectedAt = time.UnixMilli(batch.Telemetry.CollectedAtUnixMs)
		}
		if batch.Telemetry.PublishedAtUnixMs > 0 {
			live.Telemetry.GeneratorPublishedAt = time.UnixMilli(batch.Telemetry.PublishedAtUnixMs)
		}
		if batch.Telemetry.ControllerReceivedAtUnixMs > 0 {
			live.Telemetry.ControllerReceivedAt = time.UnixMilli(batch.Telemetry.ControllerReceivedAtUnixMs)
		}
		if !live.Telemetry.CollectedAt.IsZero() && !live.Telemetry.GeneratorPublishedAt.IsZero() {
			live.Telemetry.GeneratorProcessingLagMs = float64(live.Telemetry.GeneratorPublishedAt.Sub(live.Telemetry.CollectedAt).Microseconds()) / 1000.0
		}
		if !live.Telemetry.GeneratorPublishedAt.IsZero() && !live.Telemetry.ControllerReceivedAt.IsZero() {
			live.Telemetry.TransportLagMs = float64(live.Telemetry.ControllerReceivedAt.Sub(live.Telemetry.GeneratorPublishedAt).Microseconds()) / 1000.0
		}
		live.Telemetry.SampleAgeMs = batch.Telemetry.SampleAgeMs
	}
	if live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.CollectedAt = timestamp
	}

	durationsByName := make(map[string][]float64)
	durationsByDependencyTarget := make(map[string][]float64)
	durationsBySOAPOperation := make(map[string][]float64)
	transactionAttempts := int64(0)
	for _, tx := range batch.Transactions {
		transactionAttempts++
		if !tx.Success {
			live.TotalErrors++
		} else {
			live.TotalTx++
		}
		live.AvgResponseMs += tx.DurationMs
		durationsByName[tx.Name] = append(durationsByName[tx.Name], tx.DurationMs)
		metrics := live.Transactions[tx.Name]
		if metrics == nil {
			metrics = &TxMetrics{Name: tx.Name}
			live.Transactions[tx.Name] = metrics
		}
		mergeDistributedTransactionMetrics(metrics, tx)

		if dependencyTarget := strings.TrimSpace(tx.DependencyTarget); dependencyTarget != "" {
			durationsByDependencyTarget[dependencyTarget] = append(durationsByDependencyTarget[dependencyTarget], tx.DurationMs)
			dependencyMetrics := live.DependencyHealth[dependencyTarget]
			if dependencyMetrics == nil {
				dependencyMetrics = &TxMetrics{Name: dependencyTarget}
				live.DependencyHealth[dependencyTarget] = dependencyMetrics
			}
			mergeDistributedTransactionMetrics(dependencyMetrics, tx)
		}

		if operation := strings.TrimSpace(tx.SOAPOperation); operation != "" {
			soapMetrics := live.SOAPOperations[operation]
			if soapMetrics == nil {
				soapMetrics = &TxMetrics{Name: operation}
				live.SOAPOperations[operation] = soapMetrics
			}
			durationsBySOAPOperation[operation] = append(durationsBySOAPOperation[operation], tx.DurationMs)
			mergeDistributedTransactionMetrics(soapMetrics, tx)
		}
	}

	if transactionAttempts > 0 {
		live.TPS = float64(live.TotalTx)
		live.ErrorPct = float64(live.TotalErrors) / float64(transactionAttempts) * 100
		live.AvgResponseMs /= float64(transactionAttempts)
	}

	names := make([]string, 0, len(durationsByName))
	allDurations := make([]float64, 0, len(batch.Transactions))
	for name, durations := range durationsByName {
		names = append(names, name)
		sort.Float64s(durations)
		allDurations = append(allDurations, durations...)
		metrics := live.Transactions[name]
		metrics.AvgDNSMs /= float64(metrics.Count)
		metrics.AvgConnectMs /= float64(metrics.Count)
		metrics.AvgTLSMs /= float64(metrics.Count)
		metrics.AvgTTFBMs /= float64(metrics.Count)
		metrics.AvgMs /= float64(metrics.Count)
		finalizeAPIServerTelemetrySummary(metrics.ServerTelemetry)
		metrics.P50Ms = percentileValue(durations, 0.50)
		metrics.P90Ms = percentileValue(durations, 0.90)
		metrics.P95Ms = percentileValue(durations, 0.95)
		metrics.P99Ms = percentileValue(durations, 0.99)
	}
	for dependencyTarget, durations := range durationsByDependencyTarget {
		sort.Float64s(durations)
		metrics := live.DependencyHealth[dependencyTarget]
		metrics.AvgDNSMs /= float64(metrics.Count)
		metrics.AvgConnectMs /= float64(metrics.Count)
		metrics.AvgTLSMs /= float64(metrics.Count)
		metrics.AvgTTFBMs /= float64(metrics.Count)
		metrics.AvgMs /= float64(metrics.Count)
		finalizeAPIServerTelemetrySummary(metrics.ServerTelemetry)
		metrics.P50Ms = percentileValue(durations, 0.50)
		metrics.P90Ms = percentileValue(durations, 0.90)
		metrics.P95Ms = percentileValue(durations, 0.95)
		metrics.P99Ms = percentileValue(durations, 0.99)
	}
	for operation, durations := range durationsBySOAPOperation {
		sort.Float64s(durations)
		metrics := live.SOAPOperations[operation]
		metrics.AvgDNSMs /= float64(metrics.Count)
		metrics.AvgConnectMs /= float64(metrics.Count)
		metrics.AvgTLSMs /= float64(metrics.Count)
		metrics.AvgTTFBMs /= float64(metrics.Count)
		metrics.AvgMs /= float64(metrics.Count)
		finalizeAPIServerTelemetrySummary(metrics.ServerTelemetry)
		metrics.P50Ms = percentileValue(durations, 0.50)
		metrics.P90Ms = percentileValue(durations, 0.90)
		metrics.P95Ms = percentileValue(durations, 0.95)
		metrics.P99Ms = percentileValue(durations, 0.99)
	}
	sort.Strings(names)
	if len(allDurations) > 0 {
		sort.Float64s(allDurations)
		live.P50Ms = percentileValue(allDurations, 0.50)
		live.P90Ms = percentileValue(allDurations, 0.90)
		live.P95Ms = percentileValue(allDurations, 0.95)
		live.P99Ms = percentileValue(allDurations, 0.99)
	}
	if len(batch.Groups) > 0 {
		for _, group := range batch.Groups {
			live.GroupMetrics[group.Name] = &GroupLiveMetrics{
				Name:          group.Name,
				VUsers:        int(group.ActiveVUsers),
				Transactions:  0,
				Errors:        0,
				TPS:           0,
				AvgResponseMs: 0,
				P50Ms:         0,
				P90Ms:         0,
				P99Ms:         0,
			}
		}
	} else if len(test.Groups) == 1 {
		groupName := test.Groups[0].Name
		live.GroupMetrics[groupName] = &GroupLiveMetrics{
			Name:          groupName,
			VUsers:        int(batch.Stats.ActiveVUsers),
			Transactions:  live.TotalTx,
			Errors:        live.TotalErrors,
			TPS:           live.TPS,
			AvgResponseMs: live.AvgResponseMs,
			P50Ms:         live.P50Ms,
			P90Ms:         live.P90Ms,
			P99Ms:         live.P99Ms,
		}
	}

	return live
}

func mergeDistributedTransactionMetrics(metrics *TxMetrics, tx generatorrpc.TransactionMetric) {
	if metrics == nil {
		return
	}
	metrics.Count++
	if !tx.Success {
		metrics.Errors++
	}
	mergeTransactionMetricMetadata(metrics, tx.DependencyType, tx.DependencyTarget, tx.CorrelationID)
	mergeDistributedServerTelemetry(metrics, tx.ServerTelemetry)
	metrics.AvgDNSMs += tx.DNSMs
	metrics.AvgConnectMs += tx.ConnectMs
	metrics.AvgTLSMs += tx.TLSMs
	metrics.AvgTTFBMs += tx.TTFBMs
	metrics.AvgMs += tx.DurationMs
}

func mergeTransactionMetricMetadata(metrics *TxMetrics, dependencyType string, dependencyTarget string, correlationID string) {
	if metrics == nil {
		return
	}
	mergeStableTxMetadata(&metrics.DependencyType, dependencyType)
	mergeStableTxMetadata(&metrics.DependencyTarget, dependencyTarget)
	if correlationID == "" {
		return
	}
	metrics.CorrelationCount++
	for _, existing := range metrics.CorrelationIDs {
		if existing == correlationID {
			return
		}
	}
	if len(metrics.CorrelationIDs) < 3 {
		metrics.CorrelationIDs = append(metrics.CorrelationIDs, correlationID)
	}
}

func mergeStableTxMetadata(current *string, next string) {
	if current == nil || next == "" {
		return
	}
	if *current == "" {
		*current = next
		return
	}
	if *current != next {
		*current = "multiple"
	}
}

func cloneEngineServerTelemetrySummary(summary *engine.ServerTelemetrySummary) *ServerTelemetrySummary {
	if summary == nil {
		return nil
	}
	return &ServerTelemetrySummary{
		Samples:                   summary.Samples,
		AvgServerProcessingMs:     summary.AvgServerProcessingMs,
		AvgTransferMs:             summary.AvgTransferMs,
		AvgRequestPayloadKB:       summary.AvgRequestPayloadKB,
		AvgResponsePayloadKB:      summary.AvgResponsePayloadKB,
		RequestPayloadHistogram:   cloneTelemetryClassCounts(summary.RequestPayloadHistogram),
		ResponsePayloadHistogram:  cloneTelemetryClassCounts(summary.ResponsePayloadHistogram),
		AvgMessageLatencyMs:       summary.AvgMessageLatencyMs,
		MaxMessageLatencyMs:       summary.MaxMessageLatencyMs,
		MessagesSent:              summary.MessagesSent,
		MessagesReceived:          summary.MessagesReceived,
		AvgMessagesSentPerSec:     summary.AvgMessagesSentPerSec,
		AvgMessagesReceivedPerSec: summary.AvgMessagesReceivedPerSec,
		AvgConsumerLagMs:          summary.AvgConsumerLagMs,
		MaxConsumerLagMs:          summary.MaxConsumerLagMs,
		RequestSizeClasses:        cloneTelemetryClassCounts(summary.RequestSizeClasses),
		ResponseSizeClasses:       cloneTelemetryClassCounts(summary.ResponseSizeClasses),
		ConnectionReusedCount:     summary.ConnectionReusedCount,
		ConnectionReusedPct:       summary.ConnectionReusedPct,
	}
}

func mergeDistributedServerTelemetry(metrics *TxMetrics, telemetry *generatorrpc.ServerTelemetry) {
	if metrics == nil || telemetry == nil {
		return
	}
	if metrics.ServerTelemetry == nil {
		metrics.ServerTelemetry = &ServerTelemetrySummary{}
	}
	metrics.ServerTelemetry.Samples++
	metrics.ServerTelemetry.AvgServerProcessingMs += telemetry.ServerProcessingMs
	metrics.ServerTelemetry.AvgTransferMs += telemetry.TransferMs
	metrics.ServerTelemetry.AvgRequestPayloadKB += telemetry.RequestPayloadKB
	metrics.ServerTelemetry.AvgResponsePayloadKB += telemetry.ResponsePayloadKB
	incrementTelemetryHistogramCount(&metrics.ServerTelemetry.RequestPayloadHistogram, telemetry.RequestPayloadKB)
	incrementTelemetryHistogramCount(&metrics.ServerTelemetry.ResponsePayloadHistogram, telemetry.ResponsePayloadKB)
	metrics.ServerTelemetry.AvgMessageLatencyMs += telemetry.MessageLatencyMs
	if telemetry.MessageLatencyMs > metrics.ServerTelemetry.MaxMessageLatencyMs {
		metrics.ServerTelemetry.MaxMessageLatencyMs = telemetry.MessageLatencyMs
	}
	metrics.ServerTelemetry.MessagesSent += int64(telemetry.MessagesSent)
	metrics.ServerTelemetry.MessagesReceived += int64(telemetry.MessagesReceived)
	metrics.ServerTelemetry.AvgMessagesSentPerSec += telemetry.MessagesSentPerSec
	metrics.ServerTelemetry.AvgMessagesReceivedPerSec += telemetry.MessagesReceivedPerSec
	metrics.ServerTelemetry.AvgConsumerLagMs += telemetry.ConsumerLagMs
	if telemetry.ConsumerLagMs > metrics.ServerTelemetry.MaxConsumerLagMs {
		metrics.ServerTelemetry.MaxConsumerLagMs = telemetry.ConsumerLagMs
	}
	incrementTelemetryClassCount(&metrics.ServerTelemetry.RequestSizeClasses, telemetry.RequestSizeClass)
	incrementTelemetryClassCount(&metrics.ServerTelemetry.ResponseSizeClasses, telemetry.ResponseSizeClass)
	if telemetry.ConnectionReused {
		metrics.ServerTelemetry.ConnectionReusedCount++
	}
}

func finalizeAPIServerTelemetrySummary(summary *ServerTelemetrySummary) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgServerProcessingMs /= divisor
	summary.AvgTransferMs /= divisor
	summary.AvgRequestPayloadKB /= divisor
	summary.AvgResponsePayloadKB /= divisor
	summary.AvgMessageLatencyMs /= divisor
	summary.AvgMessagesSentPerSec /= divisor
	summary.AvgMessagesReceivedPerSec /= divisor
	summary.AvgConsumerLagMs /= divisor
	summary.ConnectionReusedPct = float64(summary.ConnectionReusedCount) / divisor * 100
	if len(summary.RequestSizeClasses) == 0 {
		summary.RequestSizeClasses = nil
	}
	if len(summary.ResponseSizeClasses) == 0 {
		summary.ResponseSizeClasses = nil
	}
	if len(summary.RequestPayloadHistogram) == 0 {
		summary.RequestPayloadHistogram = nil
	}
	if len(summary.ResponsePayloadHistogram) == 0 {
		summary.ResponsePayloadHistogram = nil
	}
}

func cloneTelemetryClassCounts(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func incrementTelemetryHistogramCount(values *map[string]int64, payloadKB float64) {
	if values == nil {
		return
	}
	key := payloadHistogramBucket(payloadKB)
	if key == "" {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
	(*values)[key]++
}

func payloadHistogramBucket(payloadKB float64) string {
	switch {
	case payloadKB <= 0:
		return ""
	case payloadKB < 1:
		return "0-1 KB"
	case payloadKB < 4:
		return "1-4 KB"
	case payloadKB < 16:
		return "4-16 KB"
	case payloadKB < 64:
		return "16-64 KB"
	default:
		return "64+ KB"
	}
}

func incrementTelemetryClassCount(values *map[string]int64, key string) {
	if values == nil || key == "" {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
	(*values)[key]++
}

func percentileValue(sorted []float64, percentile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if percentile <= 0 {
		return sorted[0]
	}
	if percentile >= 1 {
		return sorted[len(sorted)-1]
	}
	index := int(float64(len(sorted)-1) * percentile)
	return sorted[index]
}
