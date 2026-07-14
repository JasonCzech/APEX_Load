package api

import (
	"context"
	"errors"
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

type setupTimeoutMetricsClient interface {
	OpenMetricsStreamWithSetupTimeout(parent context.Context, setupTimeout time.Duration) (*generatorrpc.MetricsStream, error)
}

// distributedStopFanoutError reports that a remote stop failed after at least
// one StopTest RPC was dispatched. A transport failure cannot prove that the
// generator did not apply the request, so callers must keep the controller in
// its fail-closed STOPPING state instead of restoring RUNNING.
type distributedStopFanoutError struct {
	attemptedTargets    []string
	acknowledgedTargets []string
	err                 error
}

func (e *distributedStopFanoutError) Error() string {
	return e.err.Error()
}

func (e *distributedStopFanoutError) Unwrap() error {
	return e.err
}

func (e *distributedStopFanoutError) MayHaveApplied() bool {
	return len(e.attemptedTargets) > 0
}

func (e *distributedStopFanoutError) AttemptedTargets() []string {
	return append([]string(nil), e.attemptedTargets...)
}

func (e *distributedStopFanoutError) AcknowledgedTargets() []string {
	return append([]string(nil), e.acknowledgedTargets...)
}

func distributedStopMayHaveApplied(err error) bool {
	var mutationErr interface{ MayHaveApplied() bool }
	return errors.As(err, &mutationErr) && mutationErr.MayHaveApplied()
}

func (b *distributedExecutionBackend) openMetricsStream(client generatorClient) (*generatorrpc.MetricsStream, error) {
	if bounded, ok := client.(setupTimeoutMetricsClient); ok {
		return bounded.OpenMetricsStreamWithSetupTimeout(context.Background(), b.timeout)
	}
	// Test adapters and legacy clients do not expose the bounded setup method.
	// Production generatorrpc clients always take the bounded path above.
	return client.OpenMetricsStream(context.Background())
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
	targetsMu    sync.RWMutex
	targets      []generatorTargetSpec
	timeout      time.Duration
	dial         generatorDial
	drainMu      sync.RWMutex
	drainStates  map[string]string
	identityMu   sync.RWMutex
	targetIDs    map[string]string
	idTargets    map[string]string
	evacuationMu sync.Mutex
	// onIdentityRecorded, if set, is invoked (outside all backend locks) the first
	// time a generator's id↔target mapping is established or changes — i.e. when a
	// generator (re)connects. The Server uses it to apply a persisted metadata
	// cordon to the now-live drain state.
	onIdentityRecorded func(generatorID string)
}

// setOnIdentityRecorded registers a hook fired when a generator (re)connects.
func (b *distributedExecutionBackend) setOnIdentityRecorded(hook func(generatorID string)) {
	b.identityMu.Lock()
	b.onIdentityRecorded = hook
	b.identityMu.Unlock()
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
		targetIDs:   make(map[string]string),
		idTargets:   make(map[string]string),
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
	configured := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		spec := parseGeneratorTargetSpec(target)
		if spec.Address == "" {
			continue
		}
		specs = append(specs, spec)
		configured[spec.Address] = struct{}{}
	}
	b.targetsMu.Lock()
	b.targets = specs
	b.targetsMu.Unlock()

	b.identityMu.Lock()
	for target, id := range b.targetIDs {
		if _, ok := configured[target]; !ok {
			delete(b.targetIDs, target)
			if b.idTargets[id] == target {
				delete(b.idTargets, id)
			}
		}
	}
	b.identityMu.Unlock()
}

func (b *distributedExecutionBackend) SetGeneratorDrainState(target string, state string) error {
	target = strings.TrimSpace(target)
	state, err := normalizeGeneratorDrainState(state)
	if err != nil {
		return err
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

	id := b.GeneratorIDForTarget(target)
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	if state == "active" {
		delete(b.drainStates, target)
		if id != "" {
			delete(b.drainStates, id)
		}
		return nil
	}
	key := target
	if id != "" {
		key = id
		delete(b.drainStates, target)
	}
	b.drainStates[key] = state
	return nil
}

func (b *distributedExecutionBackend) SetGeneratorDrainStateByID(generatorID string, state string) (string, error) {
	generatorID = strings.TrimSpace(generatorID)
	if generatorID == "" {
		return "", fmt.Errorf("generator_id is required")
	}
	state, err := normalizeGeneratorDrainState(state)
	if err != nil {
		return "", err
	}
	target := b.GeneratorTargetForID(generatorID)
	if target == "" {
		return "", fmt.Errorf("generator_id %q is not configured on this controller", generatorID)
	}
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	if state == "active" {
		delete(b.drainStates, generatorID)
		if target != "" {
			delete(b.drainStates, target)
		}
		return target, nil
	}
	if target != "" {
		delete(b.drainStates, target)
	}
	b.drainStates[generatorID] = state
	return target, nil
}

func (b *distributedExecutionBackend) ApplyGeneratorDrainStates(states map[string]string) {
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	b.drainStates = make(map[string]string)
	for key, state := range states {
		key = strings.TrimSpace(key)
		state = strings.ToLower(strings.TrimSpace(state))
		if key == "" || state == "" || state == "active" {
			continue
		}
		switch state {
		case "cordoned", "draining":
			b.drainStates[key] = state
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
	target = strings.TrimSpace(target)
	id := b.GeneratorIDForTarget(target)
	b.drainMu.RLock()
	defer b.drainMu.RUnlock()
	if id != "" {
		if state := b.drainStates[id]; state != "" {
			return state
		}
	}
	if state := b.drainStates[target]; state != "" {
		return state
	}
	return "active"
}

func normalizeGeneratorDrainState(state string) (string, error) {
	state = strings.ToLower(strings.TrimSpace(state))
	switch state {
	case "", "active":
		return "active", nil
	case "cordoned", "draining":
		return state, nil
	default:
		return "", fmt.Errorf("unsupported drain_state %q", state)
	}
}

func (b *distributedExecutionBackend) recordGeneratorIdentity(target string, generatorID string) {
	target = strings.TrimSpace(target)
	generatorID = strings.TrimSpace(generatorID)
	if target == "" || generatorID == "" {
		return
	}
	b.identityMu.Lock()
	changed := b.idTargets[generatorID] != target
	b.targetIDs[target] = generatorID
	b.idTargets[generatorID] = target
	hook := b.onIdentityRecorded
	b.identityMu.Unlock()

	if target != generatorID {
		b.drainMu.Lock()
		if state := b.drainStates[target]; state != "" {
			if b.drainStates[generatorID] == "" {
				b.drainStates[generatorID] = state
			}
			delete(b.drainStates, target)
		}
		b.drainMu.Unlock()
	}

	// Only fire on a genuine (re)connect (new/changed mapping), not on every
	// steady-state re-probe, and outside all backend locks so the hook can safely
	// call back into the backend (SetGeneratorDrainStateByID) and the store.
	if changed && hook != nil {
		hook(generatorID)
	}
}

func (b *distributedExecutionBackend) GeneratorIDForTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	b.identityMu.RLock()
	defer b.identityMu.RUnlock()
	return b.targetIDs[target]
}

func (b *distributedExecutionBackend) GeneratorTargetForID(generatorID string) string {
	generatorID = strings.TrimSpace(generatorID)
	if generatorID == "" {
		return ""
	}
	b.identityMu.RLock()
	defer b.identityMu.RUnlock()
	return b.idTargets[generatorID]
}

// GeneratorDrainStateByID returns a generator's current drain state ("active" if
// unset), resolving both the id-keyed and target-keyed drain entries. The target
// is resolved before taking drainMu so the two locks are never held together.
func (b *distributedExecutionBackend) GeneratorDrainStateByID(generatorID string) string {
	generatorID = strings.TrimSpace(generatorID)
	if generatorID == "" {
		return "active"
	}
	target := b.GeneratorTargetForID(generatorID)
	b.drainMu.RLock()
	defer b.drainMu.RUnlock()
	if state := b.drainStates[generatorID]; state != "" {
		return state
	}
	if target != "" {
		if state := b.drainStates[target]; state != "" {
			return state
		}
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
		tr.failTestBeforeRunAndLog(test, err.Error())
		return
	}
	test.Assignment = assignment
	tr.mu.Unlock()
	targetSpecs := tr.enrichGeneratorTargetSpecsWithLabels(b.configuredTargetSpecs())
	if len(targetSpecs) == 0 {
		tr.failTestBeforeRunAndLog(test, "no generator targets configured")
		return
	}
	allowedTargets, err := b.allowedTargetsForTest(test)
	if err != nil {
		tr.failTestBeforeRunAndLog(test, err.Error())
		return
	}
	groupTargets, err := b.explicitGroupTargetsForTest(test)
	if err != nil {
		tr.failTestBeforeRunAndLog(test, err.Error())
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
	startedTargets := make(map[string]struct{})
	cleanup := func(stopStarted bool) {
		if stopStarted {
			for target := range startedTargets {
				client := clientsByTarget[target]
				if client == nil {
					continue
				}
				stopCtx, stopCancel := context.WithTimeout(context.Background(), b.timeout)
				_, _ = client.StopTest(stopCtx, test.ID, false)
				stopCancel()
			}
		}
		for _, stream := range streamsByTarget {
			_ = stream.CloseSend()
		}
		for _, client := range clientsByTarget {
			_ = client.Close()
		}
	}
	failStartup := func(message string) {
		cleanup(true)
		tr.failTestBeforeRunAndLog(test, message)
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
		tr.failTestBeforeRunAndLog(test, "no reachable generator targets configured")
		return
	}
	defaultLoadByTarget := loadSnapshotsForTargets(loadByTarget, allowedTargets)
	peakGroupShards := planDistributedGroupShardsForCandidates(test.Groups, loadByTarget, defaultLoadByTarget, allTargets, allowedTargets, assignment, targetSpecs)
	activeGroupShards := make(map[string][]GeneratorShardSummary, len(peakGroupShards))
	for _, group := range test.Groups {
		if group.script == nil {
			failStartup(fmt.Sprintf("group %q has no compiled script", group.Name))
			return
		}

		peakShards := peakGroupShards[group.Name]
		if len(peakShards) == 0 {
			failStartup(generatorPlacementRejectionMessage(group, loadByTarget, targetSpecs, assignment))
			return
		}
		shards := initialGroupShardsForRun(group, peakShards, loadByTarget)
		activeGroupShards[group.Name] = cloneGeneratorShardSummaries(shards)

		scriptContent, contentHash, err := marshalDistributedScript(group.script)
		if err != nil {
			failStartup(fmt.Sprintf("serialize script %q: %v", group.ScriptName, err))
			return
		}

		for _, shard := range shards {
			if shard.Target == "" {
				continue
			}
			if !distributedRunStartable(tr, test) {
				cleanup(true)
				return
			}
			target := shard.Target
			client := clientsByTarget[target]
			if client == nil {
				failStartup(fmt.Sprintf("selected generator %q is not connected", target))
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
					failStartup(fmt.Sprintf("distribute script %q: %v", group.ScriptName, err))
					return
				}
				distributedScripts[target][group.ScriptID] = struct{}{}
			}
			if !distributedRunStartable(tr, test) {
				cleanup(true)
				return
			}

			// AssignWork has uncertain commit semantics: a deadline or lost response
			// can occur after the generator accepted the shard. Track every attempted
			// target before the RPC so startup cleanup always issues an idempotent
			// StopTest and cannot orphan load on an ambiguous result.
			startedTargets[target] = struct{}{}
			assignment, err := client.AssignWork(ctx, generatorrpc.Assignment{
				TestID:                test.ID,
				GroupName:             group.Name,
				TargetDurationSeconds: int64(test.Duration / time.Second),
				ScriptID:              group.ScriptID,
				VUserCount:            int32(shard.VUsers),
				RampUpSeconds:         int32(group.rampUpDur / time.Second),
				RedactionRules:        cloneRedactionRules(group.redactionRules),
			})
			if err != nil {
				failStartup(fmt.Sprintf("assign work for group %q on %q: %v", group.Name, target, err))
				return
			}
			if !assignment.Accepted {
				failStartup(fmt.Sprintf("generator rejected group %q on %q: %s", group.Name, target, assignment.ErrorMessage))
				return
			}
			snapshot := loadByTarget[target]
			snapshot.AvailableCapacity = assignment.AvailableCapacity
			snapshot.Stats.ActiveVUsers += int32(shard.VUsers)
			loadByTarget[target] = snapshot
		}
	}

	assignedClients := make(map[string]generatorClient, len(startedTargets))
	for target, client := range clientsByTarget {
		if _, assigned := startedTargets[target]; assigned {
			assignedClients[target] = client
			continue
		}
		// Placement probes are not run participants. Do not retain them in the
		// completion/archive client set or wait forever for a lifecycle batch they
		// will never emit.
		_ = client.Close()
		delete(clientsByTarget, target)
	}

	for _, target := range orderedGeneratorTargets(assignedClients, allowedTargets) {
		if _, ok := streamsByTarget[target]; ok {
			continue
		}
		stream, err := b.openMetricsStream(assignedClients[target])
		if err != nil {
			failStartup(fmt.Sprintf("open generator metrics stream for %q: %v", target, err))
			return
		}
		streamsByTarget[target] = stream
	}

	started, startErr := b.startDistributedTestRun(tr, test, activeGroupShards, assignedClients, streamsByTarget)
	if startErr != nil {
		failStartup(startErr.Error())
		return
	}
	if !started {
		cleanup(true)
	}
}

func distributedRunStartable(tr *TestRunner, test *ManagedTest) bool {
	if tr == nil || test == nil || !tr.isExecutionLeader() {
		return false
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	current := tr.tests[test.ID]
	return !tr.shuttingDown && current == test && test.Status == "INITIALIZING" && test.executionCtx != nil && test.executionCtx.Err() == nil
}

func (b *distributedExecutionBackend) StopTest(tr *TestRunner, test *ManagedTest) error {
	return b.StopTestContext(context.Background(), tr, test)
}

func (b *distributedExecutionBackend) StopTestContext(parent context.Context, tr *TestRunner, test *ManagedTest) error {
	if parent == nil {
		parent = context.Background()
	}
	tr.mu.RLock()
	clientsByTarget, single, remoteTarget := cloneClientMap(test.remoteClientsByTarget), test.remoteClient, test.remoteTarget
	status := test.Status
	tr.mu.RUnlock()
	clients := clientsByTarget
	var dialed generatorClient
	if len(clients) == 0 {
		if single != nil {
			clients = map[string]generatorClient{remoteTarget: single}
		} else {
			if remoteTarget == "" {
				return fmt.Errorf("distributed generator target is not recorded")
			}
			ctx, cancel := context.WithTimeout(parent, b.timeout)
			client, err := b.dial(ctx, remoteTarget)
			cancel()
			if err != nil {
				return fmt.Errorf("connect generator: %w", err)
			}
			dialed = client
			defer dialed.Close()
			clients = map[string]generatorClient{remoteTarget: client}
		}
	}
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	type stopResult struct {
		target string
		err    error
	}
	results := make(chan stopResult, len(targets))
	for _, target := range targets {
		target := target
		go func() {
			ctx, cancel := context.WithTimeout(parent, b.timeout)
			defer cancel()
			stop, err := clients[target].StopTest(ctx, test.ID, true)
			if err == nil && !stop.Acknowledged {
				err = fmt.Errorf("generator did not acknowledge stop for %s", test.ID)
			}
			results <- stopResult{target: target, err: err}
		}()
	}
	var stopErrs []error
	acknowledgedTargets := make([]string, 0, len(targets))
	for range targets {
		result := <-results
		err := result.err
		if err != nil {
			code := grpcStatus.Code(err)
			if code == grpcCodes.Canceled || code == grpcCodes.Unavailable || code == grpcCodes.NotFound {
				if status == "COMPLETE" || status == "FAILED" {
					continue
				}
			}
			stopErrs = append(stopErrs, fmt.Errorf("stop remote test on %s: %w", result.target, err))
			continue
		}
		acknowledgedTargets = append(acknowledgedTargets, result.target)
	}
	stopErr := errors.Join(stopErrs...)
	if stopErr == nil {
		return nil
	}
	return &distributedStopFanoutError{
		attemptedTargets:    append([]string(nil), targets...),
		acknowledgedTargets: acknowledgedTargets,
		err:                 stopErr,
	}
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
	if tr.tests[test.ID] != test || test.Status != "RUNNING" || test.remoteFinalizing {
		status := test.Status
		tr.mu.RUnlock()
		return fmt.Errorf("distributed test is %s and cannot be scaled", status)
	}
	controlGeneration := test.remoteControlGeneration
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
		tr.mu.Lock()
		current := distributedControlCommitCurrentLocked(tr, test, "RUNNING", controlGeneration)
		tr.mu.Unlock()
		if !current {
			rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), b.timeout)
			rollback, rollbackErr := client.AdjustVUsers(rollbackCtx, generatorrpc.VUserAdjustment{
				TestID:       test.ID,
				GroupName:    groupName,
				TargetVUsers: int32(currentShardVUsers),
			})
			rollbackCancel()
			if rollbackErr != nil {
				return fmt.Errorf("distributed scale became stale; rollback vusers: %w", rollbackErr)
			}
			if !rollback.Accepted {
				return fmt.Errorf("distributed scale became stale; generator did not accept vuser rollback for %s", test.ID)
			}
			return fmt.Errorf("distributed scale became stale before commit")
		}
		return nil
	}

	clients, err := b.clientsFromSnapshot(clientsByTarget, single, remoteTarget)
	if err != nil {
		return err
	}
	targets := shardTargets(shards)
	loadByTarget := make(map[string]generatorLoadSnapshot, len(targets))
	type loadProbeResult struct {
		target   string
		snapshot generatorLoadSnapshot
		err      error
	}
	loadCh := make(chan loadProbeResult, len(targets))
	for _, target := range targets {
		target := target
		client := clients[target]
		if client == nil {
			return fmt.Errorf("selected generator %q is not connected", target)
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
			defer cancel()
			snapshot := b.probeGeneratorLoad(ctx, target, client)
			snapshot.AvailableCapacity += int32(shardVUsersForTarget(shards, target))
			loadCh <- loadProbeResult{target: target, snapshot: snapshot}
		}()
	}
	for range targets {
		result := <-loadCh
		if result.err != nil {
			return result.err
		}
		loadByTarget[result.target] = result.snapshot
	}
	nextShards := capacityWeightedShardsIncludingZero(groupName, targetVUsers, targets, loadByTarget, shardStrategyForGroupShards(shards))
	previousByTarget := make(map[string]int, len(shards))
	for _, shard := range shards {
		previousByTarget[shard.Target] = shard.VUsers
	}
	type adjustResult struct {
		shard GeneratorShardSummary
		err   error
	}
	adjustCh := make(chan adjustResult, len(nextShards))
	for _, shard := range nextShards {
		shard := shard
		client := clients[shard.Target]
		if client == nil {
			adjustCh <- adjustResult{shard: shard, err: fmt.Errorf("selected generator %q is not connected", shard.Target)}
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
			defer cancel()
			adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
				TestID:       test.ID,
				GroupName:    groupName,
				TargetVUsers: int32(shard.VUsers),
			})
			if err != nil {
				adjustCh <- adjustResult{shard: shard, err: fmt.Errorf("adjust remote vusers on %s: %w", shard.Target, err)}
				return
			}
			if !adjustment.Accepted {
				adjustCh <- adjustResult{shard: shard, err: fmt.Errorf("generator did not accept vuser adjustment for %s on %s", test.ID, shard.Target)}
				return
			}
			adjustCh <- adjustResult{shard: shard}
		}()
	}
	successes := make([]GeneratorShardSummary, 0, len(nextShards))
	var adjustErr error
	for range nextShards {
		result := <-adjustCh
		if result.err != nil && adjustErr == nil {
			adjustErr = result.err
			continue
		}
		if result.err == nil {
			successes = append(successes, result.shard)
		}
	}
	if adjustErr != nil {
		if rollbackErr := b.rollbackShardVUsers(test.ID, groupName, clients, successes, previousByTarget); rollbackErr != nil {
			return fmt.Errorf("%w; rollback shard vusers: %v", adjustErr, rollbackErr)
		}
		return adjustErr
	}
	tr.mu.Lock()
	if !distributedControlCommitCurrentLocked(tr, test, "RUNNING", controlGeneration) {
		tr.mu.Unlock()
		if rollbackErr := b.rollbackShardVUsers(test.ID, groupName, clients, successes, previousByTarget); rollbackErr != nil {
			return fmt.Errorf("distributed scale became stale before commit; rollback shard vusers: %v", rollbackErr)
		}
		return fmt.Errorf("distributed scale became stale before commit")
	}
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
	tr.mu.RLock()
	previousDuration := test.Duration
	tr.mu.RUnlock()
	clients, err := b.resolveClientsLocked(tr, test)
	if err != nil {
		return err
	}
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	attempted := make([]string, 0, len(targets))
	for _, target := range targets {
		attempted = append(attempted, target)
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		result, err := clients[target].AdjustDuration(ctx, generatorrpc.DurationAdjustment{
			TestID:                test.ID,
			TargetDurationSeconds: int64(newDuration / time.Second),
		})
		cancel()
		if err != nil {
			forwardErr := fmt.Errorf("adjust remote duration on %s: %w", target, err)
			return errors.Join(append([]error{forwardErr}, b.rollbackDurations(test.ID, clients, attempted, previousDuration)...)...)
		}
		if !result.Accepted {
			forwardErr := fmt.Errorf("generator %s did not accept duration adjustment for %s", target, test.ID)
			return errors.Join(append([]error{forwardErr}, b.rollbackDurations(test.ID, clients, attempted, previousDuration)...)...)
		}
	}
	return nil
}

func (b *distributedExecutionBackend) rollbackDurations(testID string, clients map[string]generatorClient, attempted []string, previousDuration time.Duration) []error {
	rollbackErrs := make([]error, 0)
	for i := len(attempted) - 1; i >= 0; i-- {
		target := attempted[i]
		client := clients[target]
		if client == nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restore remote duration on %s: generator is not connected", target))
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		result, err := client.AdjustDuration(ctx, generatorrpc.DurationAdjustment{
			TestID:                testID,
			TargetDurationSeconds: int64(previousDuration / time.Second),
		})
		cancel()
		if err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restore remote duration on %s: %w", target, err))
			continue
		}
		if !result.Accepted {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("generator %s did not accept duration rollback for %s", target, testID))
		}
	}
	return rollbackErrs
}

func (b *distributedExecutionBackend) PauseTest(tr *TestRunner, test *ManagedTest) error {
	streams := b.snapshotMetricsStreams(tr, test)
	if len(streams) == 0 {
		return fmt.Errorf("distributed metrics stream is not initialized")
	}
	targets := orderedMetricsTargets(streams, b.configuredTargets())
	return applyPauseControlFanout(test.ID, true, targets, func(target string, pause bool) error {
		return streams[target].SendPause(generatorrpc.PauseControl{TestID: test.ID, Pause: pause})
	})
}

func (b *distributedExecutionBackend) ResumeTest(tr *TestRunner, test *ManagedTest) error {
	streams := b.snapshotMetricsStreams(tr, test)
	if len(streams) == 0 {
		return fmt.Errorf("distributed metrics stream is not initialized")
	}
	targets := orderedMetricsTargets(streams, b.configuredTargets())
	return applyPauseControlFanout(test.ID, false, targets, func(target string, pause bool) error {
		return streams[target].SendPause(generatorrpc.PauseControl{TestID: test.ID, Pause: pause})
	})
}

func applyPauseControlFanout(testID string, pause bool, targets []string, send func(target string, pause bool) error) error {
	attempted := make([]string, 0, len(targets))
	operation := "pause"
	rollbackOperation := "resume"
	if !pause {
		operation = "resume"
		rollbackOperation = "pause"
	}
	for _, target := range targets {
		attempted = append(attempted, target)
		if err := send(target, pause); err != nil {
			errList := []error{fmt.Errorf("send remote %s control to %s: %w", operation, target, err)}
			for i := len(attempted) - 1; i >= 0; i-- {
				rollbackTarget := attempted[i]
				if rollbackErr := send(rollbackTarget, !pause); rollbackErr != nil {
					errList = append(errList, fmt.Errorf("send compensating remote %s control to %s: %w", rollbackOperation, rollbackTarget, rollbackErr))
				}
			}
			return errors.Join(errList...)
		}
	}
	return nil
}

// The snapshot* helpers below read the mutable remote* fields under tr.mu and return
// plain locals (cloning map/slice headers) so the control methods can resolve clients,
// dial, and make blocking gRPC calls WITHOUT holding tr.mu — mirroring the read/clear
// pattern in closeRemoteResources. They replace the direct clientsForTest/clientForGroup/
// metricsStreamsForTest reads, which raced the teardown/finalize mutators.
