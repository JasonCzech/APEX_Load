package api

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
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
	Labels    map[string]string
}

type distributedExecutionBackend struct {
	targetsMu   sync.RWMutex
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
	specs := b.configuredTargetSpecs()
	targets := make([]string, 0, len(specs))
	for _, target := range specs {
		if b.generatorDrainState(target.Address) != "active" {
			continue
		}
		targets = append(targets, target.Address)
	}
	return targets
}

func (b *distributedExecutionBackend) configuredTargetSpecs() []generatorTargetSpec {
	b.targetsMu.RLock()
	defer b.targetsMu.RUnlock()
	return append([]generatorTargetSpec(nil), b.targets...)
}

// SetTargets replaces the generator fleet with the provided raw target entries
// (same syntax as APEXLOAD_GENERATORS). Used by dynamic discovery (e.g. dns://
// headless-service entries) to track generators as autoscaling adds or removes
// pods. Drain states keyed by address are preserved across updates.
func (b *distributedExecutionBackend) SetTargets(targets []string) {
	specs := make([]generatorTargetSpec, 0, len(targets))
	for _, target := range targets {
		spec := parseGeneratorTargetSpec(target)
		if spec.Address == "" {
			continue
		}
		specs = append(specs, spec)
	}
	b.targetsMu.Lock()
	b.targets = specs
	b.targetsMu.Unlock()
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
	for _, configured := range b.configuredTargetSpecs() {
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

func (b *distributedExecutionBackend) allowedTargetsForTest(test *ManagedTest) ([]string, error) {
	assignment, err := normalizeGeneratorAssignmentSettings(test.Assignment)
	if err != nil {
		return nil, err
	}
	if assignment == nil || assignment.Mode != generatorAssignmentModeSpecificHosts {
		return b.configuredTargets(), nil
	}
	specs := b.configuredTargetSpecs()
	configured := make(map[string]struct{}, len(specs))
	for _, target := range specs {
		configured[target.Address] = struct{}{}
	}
	// Pool-scoped selections (assignment.PoolID set) are enforced leniently: a pool
	// member that is offline/draining on this controller is skipped rather than
	// failing the run. Explicit specific_hosts selections stay strict.
	poolScoped := strings.TrimSpace(assignment.PoolID) != ""
	allowed := make([]string, 0, len(assignment.SelectedTargets))
	for _, target := range assignment.SelectedTargets {
		if _, ok := configured[target]; !ok {
			if poolScoped {
				continue
			}
			return nil, fmt.Errorf("assignment target %q is not configured on this controller", target)
		}
		if drainState := b.generatorDrainState(target); drainState != "active" {
			if poolScoped {
				continue
			}
			return nil, fmt.Errorf("assignment target %q is %s and cannot accept new work", target, drainState)
		}
		allowed = append(allowed, target)
	}
	if len(allowed) == 0 {
		if poolScoped {
			return nil, fmt.Errorf("fleet pool has no active generators configured on this controller")
		}
		return nil, fmt.Errorf("no explicit assignment targets are configured for this controller")
	}
	return allowed, nil
}

func (b *distributedExecutionBackend) explicitGroupTargetsForTest(test *ManagedTest) ([]string, error) {
	if test == nil || len(test.Groups) == 0 {
		return nil, nil
	}
	specs := b.configuredTargetSpecs()
	configured := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		configured[spec.Address] = struct{}{}
	}
	targets := make([]string, 0)
	for _, group := range test.Groups {
		assignment := group.Assignment
		if assignment == nil || assignment.Mode != generatorAssignmentModeSpecificHosts {
			continue
		}
		for _, target := range assignment.SelectedTargets {
			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}
			if _, ok := configured[target]; !ok {
				return nil, fmt.Errorf("group %q assignment target %q is not configured on this controller", group.Name, target)
			}
			if drainState := b.generatorDrainState(target); drainState != "active" {
				return nil, fmt.Errorf("group %q assignment target %q is %s and cannot accept new work", group.Name, target, drainState)
			}
			targets = append(targets, target)
		}
	}
	return orderedUniqueTargets(targets), nil
}

func hasExplicitAutoGroup(groups []TestGroup) bool {
	for _, group := range groups {
		if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeAuto {
			return true
		}
	}
	return false
}

func orderedUniqueTargets(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func loadSnapshotsForTargets(loadByTarget map[string]generatorLoadSnapshot, targets []string) map[string]generatorLoadSnapshot {
	if len(loadByTarget) == 0 || len(targets) == 0 {
		return nil
	}
	out := make(map[string]generatorLoadSnapshot, len(targets))
	for _, target := range targets {
		if snapshot, ok := loadByTarget[target]; ok {
			out[target] = snapshot
		}
	}
	return out
}

func (tr *TestRunner) enrichGeneratorTargetSpecsWithLabels(specs []generatorTargetSpec) []generatorTargetSpec {
	if len(specs) == 0 {
		return nil
	}
	out := make([]generatorTargetSpec, len(specs))
	copy(out, specs)
	if tr == nil || tr.fleetStore == nil {
		for i := range out {
			out[i].Labels = cloneStringMap(out[i].Labels)
		}
		return out
	}
	metadata, err := tr.fleetStore.ListGeneratorMetadata(0, 0)
	if err != nil || len(metadata) == 0 {
		for i := range out {
			out[i].Labels = cloneStringMap(out[i].Labels)
		}
		return out
	}
	// Index fleet tags under BOTH the metadata Target and GeneratorID so capability
	// labels attach whenever EITHER equals the generator's dial address (the only
	// join key available pre-probe). Tags recorded against a generator whose metadata
	// has neither Target nor GeneratorID equal to its configured dial address cannot
	// be correlated here — operators must record capability tags keyed by dial address.
	labelsByTarget := make(map[string]map[string]string, len(metadata))
	for _, item := range metadata {
		if item == nil || len(item.Tags) == 0 {
			continue
		}
		if target := strings.TrimSpace(item.Target); target != "" {
			labelsByTarget[target] = mergeStringMaps(labelsByTarget[target], item.Tags)
		}
		if id := strings.TrimSpace(item.GeneratorID); id != "" {
			labelsByTarget[id] = mergeStringMaps(labelsByTarget[id], item.Tags)
		}
	}
	for i := range out {
		out[i].Labels = mergeStringMaps(out[i].Labels, labelsByTarget[out[i].Address])
	}
	return out
}

func (b *distributedExecutionBackend) Run(tr *TestRunner, test *ManagedTest) {
	tr.mu.Lock()
	assignment, err := normalizeGeneratorAssignmentSettings(test.Assignment)
	if err != nil {
		tr.mu.Unlock()
		tr.failTestBeforeRun(test, err.Error())
		return
	}
	test.Assignment = assignment
	tr.mu.Unlock()
	targetSpecs := tr.enrichGeneratorTargetSpecsWithLabels(b.configuredTargetSpecs())
	if len(targetSpecs) == 0 {
		tr.failTestBeforeRun(test, "no generator targets configured")
		return
	}
	allowedTargets, err := b.allowedTargetsForTest(test)
	if err != nil {
		tr.failTestBeforeRun(test, err.Error())
		return
	}
	groupTargets, err := b.explicitGroupTargetsForTest(test)
	if err != nil {
		tr.failTestBeforeRun(test, err.Error())
		return
	}
	allTargets := append([]string(nil), allowedTargets...)
	if hasExplicitAutoGroup(test.Groups) {
		allTargets = append(allTargets, b.configuredTargets()...)
	}
	allTargets = append(allTargets, groupTargets...)
	allTargets = orderedUniqueTargets(allTargets)

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
	for _, target := range allTargets {
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
	defaultLoadByTarget := loadSnapshotsForTargets(loadByTarget, allowedTargets)
	peakGroupShards := planDistributedGroupShardsForCandidates(test.Groups, loadByTarget, defaultLoadByTarget, allTargets, allowedTargets, assignment, targetSpecs)
	activeGroupShards := make(map[string][]GeneratorShardSummary, len(peakGroupShards))
	for _, group := range test.Groups {
		if group.script == nil {
			cleanup()
			tr.failTestBeforeRun(test, fmt.Sprintf("group %q has no compiled script", group.Name))
			return
		}

		peakShards := peakGroupShards[group.Name]
		if len(peakShards) == 0 {
			cleanup()
			tr.failTestBeforeRun(test, generatorPlacementRejectionMessage(group, loadByTarget, targetSpecs, assignment))
			return
		}
		shards := initialGroupShardsForRun(group, peakShards, loadByTarget)
		activeGroupShards[group.Name] = cloneGeneratorShardSummaries(shards)

		scriptContent, contentHash, err := marshalDistributedScript(group.script)
		if err != nil {
			cleanup()
			tr.failTestBeforeRun(test, fmt.Sprintf("serialize script %q: %v", group.ScriptName, err))
			return
		}

		for _, shard := range shards {
			if shard.Target == "" {
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

	for _, target := range orderedAssignedShardTargets(activeGroupShards, allowedTargets) {
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

	b.startDistributedTestRun(tr, test, activeGroupShards, clientsByTarget, streamsByTarget)
}

func (b *distributedExecutionBackend) StopTest(tr *TestRunner, test *ManagedTest) error {
	tr.mu.RLock()
	clientsByTarget, single, remoteTarget := cloneClientMap(test.remoteClientsByTarget), test.remoteClient, test.remoteTarget
	status := test.Status
	tr.mu.RUnlock()
	clients, err := b.clientsFromSnapshot(clientsByTarget, single, remoteTarget)
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
				if status == "COMPLETE" || status == "FAILED" {
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

func initialGroupShardsForRun(group TestGroup, peakShards []GeneratorShardSummary, loadByTarget map[string]generatorLoadSnapshot) []GeneratorShardSummary {
	if !group.stageDriven {
		return cloneGeneratorShardSummaries(peakShards)
	}
	targets := shardTargets(peakShards)
	return capacityWeightedShardsIncludingZero(group.Name, groupCurrentVUsers(group), targets, loadByTarget, shardStrategyForGroupShards(peakShards))
}

func (b *distributedExecutionBackend) ScaleVUsers(tr *TestRunner, test *ManagedTest, groupName string, targetVUsers int) error {
	tr.mu.RLock()
	groupExists := false
	for _, group := range test.Groups {
		if group.Name == groupName {
			groupExists = true
			break
		}
	}
	shards := cloneGeneratorShardSummaries(test.remoteGroupShards[groupName])
	groupTarget, groupTargetOK := test.remoteGroupTargets[groupName]
	currentShardVUsers := testGroupCurrentShardVUsers(test, groupName)
	clientsByTarget, single, remoteTarget := cloneClientMap(test.remoteClientsByTarget), test.remoteClient, test.remoteTarget
	tr.mu.RUnlock()

	if !groupExists {
		return fmt.Errorf("test group not found: %s", groupName)
	}
	if len(shards) == 0 {
		if groupTargetOK && groupTarget != "" {
			shards = []GeneratorShardSummary{{Group: groupName, Target: groupTarget, VUsers: currentShardVUsers}}
		}
	}
	if len(shards) == 0 {
		client, err := b.groupClientFromSnapshot(groupTarget, groupTargetOK, clientsByTarget, single, remoteTarget)
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

	clients, err := b.clientsFromSnapshot(clientsByTarget, single, remoteTarget)
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
	clients, err := b.resolveClientsLocked(tr, test)
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
	streams := b.snapshotMetricsStreams(tr, test)
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
	streams := b.snapshotMetricsStreams(tr, test)
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

// The snapshot* helpers below read the mutable remote* fields under tr.mu and return
// plain locals (cloning map/slice headers) so the control methods can resolve clients,
// dial, and make blocking gRPC calls WITHOUT holding tr.mu — mirroring the read/clear
// pattern in closeRemoteResources. They replace the direct clientsForTest/clientForGroup/
// metricsStreamsForTest reads, which raced the teardown/finalize mutators.
