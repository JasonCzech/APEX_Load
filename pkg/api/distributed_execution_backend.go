package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

const maxDistributedTargetsPerRun = 256

func validateDistributedRunTargetCount(targets []string) error {
	if len(targets) > maxDistributedTargetsPerRun {
		return fmt.Errorf("distributed run targets %d generators; limit is %d", len(targets), maxDistributedTargetsPerRun)
	}
	return nil
}

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

type coordinatedWorkCommitClient interface {
	CommitWorkAt(ctx context.Context, testID string, executionGeneration uint64, startAt time.Time) (generatorrpc.WorkCommitResult, error)
}

// acknowledgedPauseClient is implemented by the production generator RPC
// client. It is optional so older in-process test adapters can continue to use
// the legacy stream signal; real distributed clients always take the unary ACK
// path below.
type acknowledgedPauseClient interface {
	SetPaused(context.Context, generatorrpc.PauseControl) (generatorrpc.PauseResult, error)
}

type distributedPauseUncertainError struct {
	testID            string
	unresolvedTargets []string
	err               error
}

func (e *distributedPauseUncertainError) Error() string { return e.err.Error() }
func (e *distributedPauseUncertainError) Unwrap() error { return e.err }

type distributedDurationUncertainError struct {
	testID            string
	unresolvedTargets []string
	err               error
}

func (e *distributedDurationUncertainError) Error() string { return e.err.Error() }
func (e *distributedDurationUncertainError) Unwrap() error { return e.err }

type distributedVUserUncertainError struct {
	testID            string
	unresolvedTargets []string
	err               error
}

func (e *distributedVUserUncertainError) Error() string { return e.err.Error() }
func (e *distributedVUserUncertainError) Unwrap() error { return e.err }

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

func (b *distributedExecutionBackend) openMetricsStream(ctx context.Context, client generatorClient, testID string, executionGeneration uint64) (*generatorrpc.MetricsStream, error) {
	return b.openMetricsStreamWithSetupTimeout(ctx, client, testID, executionGeneration, b.timeout)
}

func (b *distributedExecutionBackend) openMetricsStreamWithSetupTimeout(ctx context.Context, client generatorClient, testID string, executionGeneration uint64, setupTimeout time.Duration) (*generatorrpc.MetricsStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if setupTimeout <= 0 {
		setupTimeout = b.timeout
	}
	if bounded, ok := client.(executionTestSetupMetricsClient); ok {
		return bounded.OpenMetricsStreamWithSetupTimeoutForTestGeneration(ctx, setupTimeout, testID, executionGeneration)
	}
	if scoped, ok := client.(executionTestMetricsClient); ok {
		return scoped.OpenMetricsStreamForTestGeneration(ctx, testID, executionGeneration)
	}
	if executionGeneration > 0 {
		if bounded, ok := client.(executionGenerationSetupMetricsClient); ok {
			return bounded.OpenMetricsStreamWithSetupTimeoutForGeneration(ctx, setupTimeout, executionGeneration)
		}
		if generationClient, ok := client.(executionGenerationMetricsClient); ok {
			return generationClient.OpenMetricsStreamForGeneration(ctx, executionGeneration)
		}
		return nil, fmt.Errorf("generator client does not support execution-fenced metrics streams")
	}
	if bounded, ok := client.(setupTimeoutMetricsClient); ok {
		return bounded.OpenMetricsStreamWithSetupTimeout(ctx, setupTimeout)
	}
	// Test adapters and legacy clients do not expose the bounded setup method.
	// Production generatorrpc clients always take the bounded path above.
	return client.OpenMetricsStream(ctx)
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
	RuntimeCapacities  []generatorrpc.ProtocolRuntimeCapacity
}

func cloneProtocolRuntimeCapacities(values []generatorrpc.ProtocolRuntimeCapacity) []generatorrpc.ProtocolRuntimeCapacity {
	if len(values) == 0 {
		return nil
	}
	out := make([]generatorrpc.ProtocolRuntimeCapacity, len(values))
	copy(out, values)
	for index := range out {
		out[index].Features = append([]string(nil), values[index].Features...)
	}
	return out
}

type generatorTargetSpec struct {
	Address   string
	Region    string
	Version   string
	Protocols []string
	Features  []string
	Labels    map[string]string
}

func validateDistributedShardRunGoals(group TestGroup, peakShards []GeneratorShardSummary) error {
	if len(peakShards) <= 1 {
		return nil
	}
	var globalIterations int
	var maxTransactions int64
	if group.RunLogicGoals != nil {
		globalIterations = group.RunLogicGoals.GlobalIterations
		maxTransactions = group.RunLogicGoals.MaxTransactions
	}
	if group.script != nil {
		if globalIterations <= 0 {
			globalIterations = group.script.Config.RunLogic.GlobalIterations
		}
		if maxTransactions <= 0 {
			maxTransactions = group.script.Config.RunLogic.MaxTransactions
		}
	}
	if globalIterations > 0 || maxTransactions > 0 {
		return fmt.Errorf("global iteration/transaction goals require a single generator shard because cross-generator goal coordination is unavailable")
	}
	return nil
}

type distributedExecutionBackend struct {
	targetsMu          sync.RWMutex
	targets            []generatorTargetSpec
	timeout            time.Duration
	stopGrace          time.Duration
	dial               generatorDial
	drainMu            sync.RWMutex
	drainStates        map[string]string
	testLeaseFences    map[distributedTestLeaseFenceKey]uint64
	nextTestLeaseFence uint64
	identityMu         sync.RWMutex
	targetIDs          map[string]string
	idTargets          map[string]string
	evacuationMu       sync.Mutex
	inventoryProbeOnce sync.Once
	inventoryProbeGate chan struct{}
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
		targets:         clonedTargets,
		timeout:         timeout,
		stopGrace:       max(2*timeout, 5*time.Second),
		dial:            dial,
		drainStates:     make(map[string]string),
		testLeaseFences: make(map[distributedTestLeaseFenceKey]uint64),
		targetIDs:       make(map[string]string),
		idTargets:       make(map[string]string),
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

type distributedTestLeaseFenceKey struct {
	test   *ManagedTest
	target string
}

// fenceTestExecutionLease stops renewal only for one logical assignment. The
// returned token is incarnation-scoped; a stale cleanup cannot clear a newer
// fence for the same test and target.
func (b *distributedExecutionBackend) fenceTestExecutionLease(test *ManagedTest, target string) uint64 {
	key := distributedTestLeaseFenceKey{test: test, target: strings.TrimSpace(target)}
	if key.test == nil || key.target == "" {
		return 0
	}
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	if b.testLeaseFences == nil {
		b.testLeaseFences = make(map[distributedTestLeaseFenceKey]uint64)
	}
	b.nextTestLeaseFence++
	if b.nextTestLeaseFence == 0 {
		b.nextTestLeaseFence++
	}
	b.testLeaseFences[key] = b.nextTestLeaseFence
	return b.nextTestLeaseFence
}

func (b *distributedExecutionBackend) testExecutionLeaseFenced(test *ManagedTest, target string) bool {
	b.drainMu.RLock()
	defer b.drainMu.RUnlock()
	return b.testLeaseFences[distributedTestLeaseFenceKey{test: test, target: strings.TrimSpace(target)}] != 0
}

func (b *distributedExecutionBackend) clearTestExecutionLeaseFences(test *ManagedTest) {
	if test == nil {
		return
	}
	b.drainMu.Lock()
	for key := range b.testLeaseFences {
		if key.test == test {
			delete(b.testLeaseFences, key)
		}
	}
	b.drainMu.Unlock()
}

func (b *distributedExecutionBackend) filterTestPlacementTargets(test *ManagedTest, targets []string) []string {
	filtered := make([]string, 0, len(targets))
	for _, target := range targets {
		if b.testExecutionLeaseFenced(test, target) {
			continue
		}
		filtered = append(filtered, target)
	}
	return filtered
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
	executionGeneration, executionLeaseOwnerID, _, termErr := tr.generatorExecutionClaim()
	if termErr != nil {
		tr.failTestBeforeRunAndLog(test, fmt.Sprintf("capture generator execution term: %v", termErr))
		return
	}
	tr.mu.Lock()
	if err := validateManagedTestWorkloadBounds(test); err != nil {
		tr.mu.Unlock()
		tr.failTestBeforeRunAndLog(test, fmt.Sprintf("invalid workload: %v", err))
		return
	}
	if test.executionGeneration > 0 && test.executionGeneration != executionGeneration {
		expected := test.executionGeneration
		tr.mu.Unlock()
		tr.failTestBeforeRunAndLog(test, fmt.Sprintf("generator execution term changed before launch (expected %d, current %d)", expected, executionGeneration))
		return
	}
	assignment, err := normalizeGeneratorAssignmentSettings(test.Assignment)
	if err != nil {
		tr.mu.Unlock()
		tr.failTestBeforeRunAndLog(test, err.Error())
		return
	}
	test.Assignment = assignment
	requireTestExecutionLease := assignmentToleratesGeneratorLoss(assignment)
	test.executionGeneration = executionGeneration
	clearDistributedRunSecretSnapshotsLocked(test)
	tr.mu.Unlock()

	// Capture and persist the immutable configuration before any generator work
	// is staged. The stored snapshot survives controller restart/failover and is
	// never replaced by later scale, duration, or runtime-control mutations.
	initialProvenance := maskRunArchiveProvenance(buildRunArchiveProvenance(tr, test))
	if persistInitialErr := tr.persistInitialArchiveProvenance(test, &initialProvenance); persistInitialErr != nil {
		tr.failTestBeforeRunAndLog(test, fmt.Sprintf("persist initial archive provenance: %v", persistInitialErr))
		return
	}
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
	if err := validateDistributedRunTargetCount(allTargets); err != nil {
		tr.failTestBeforeRunAndLog(test, err.Error())
		return
	}

	tr.mu.RLock()
	executionCtx := test.executionCtx
	tr.mu.RUnlock()
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	clientsByTarget := make(map[string]generatorClient)
	streamsByTarget := make(map[string]*generatorrpc.MetricsStream)
	distributedScripts := make(map[string]map[string]struct{})
	loadByTarget := make(map[string]generatorLoadSnapshot)
	startedTargets := make(map[string]struct{})
	var stopInitialLeaseKeeper context.CancelFunc
	cleanup := func(stopStarted bool) {
		if stopInitialLeaseKeeper != nil {
			stopInitialLeaseKeeper()
			stopInitialLeaseKeeper = nil
		}
		if stopStarted {
			for target := range startedTargets {
				client := clientsByTarget[target]
				if client == nil {
					continue
				}
				stopCtx, stopCancel := context.WithTimeout(context.Background(), b.timeout)
				_, _ = stopGeneratorTest(stopCtx, client, test.ID, false, executionGeneration)
				stopCancel()
			}
		}
		for _, stream := range streamsByTarget {
			_ = stream.CloseSend()
		}
		for _, client := range clientsByTarget {
			_ = client.Close()
		}
		tr.clearDistributedRunSecretSnapshots(test)
	}
	failStartup := func(message string) {
		cleanup(true)
		tr.failTestBeforeRunAndLog(test, message)
	}
	type discoveryResult struct {
		target   string
		client   generatorClient
		load     generatorLoadSnapshot
		err      error
		fatalErr error
	}
	discoveryCtx, discoveryCancel := context.WithCancel(executionCtx)
	jobs := make(chan string, len(allTargets))
	results := make(chan discoveryResult, len(allTargets))
	workerCount := min(len(allTargets), maxGeneratorEpochKeeperWorkers)
	var discoveryWorkers sync.WaitGroup
	discoveryWorkers.Add(workerCount)
	for range workerCount {
		go func() {
			defer discoveryWorkers.Done()
			for target := range jobs {
				targetCtx, targetCancel := context.WithTimeout(discoveryCtx, b.timeout)
				client, dialErr := b.dial(targetCtx, target)
				if dialErr != nil {
					targetCancel()
					results <- discoveryResult{target: target, err: dialErr}
					continue
				}
				currentGeneration, currentLeaseOwnerID, claimDeadline, claimErr := tr.generatorExecutionClaim()
				if claimErr != nil || currentGeneration != executionGeneration || currentLeaseOwnerID != executionLeaseOwnerID {
					targetCancel()
					_ = client.Close()
					results <- discoveryResult{target: target, fatalErr: fmt.Errorf("execution leadership changed during generator discovery")}
					continue
				}
				if claimErr := claimGeneratorExecutionEpoch(targetCtx, client, executionGeneration, executionLeaseOwnerID, claimDeadline, b.timeout); claimErr != nil {
					targetCancel()
					_ = client.Close()
					results <- discoveryResult{target: target, err: claimErr}
					continue
				}
				load := b.probeGeneratorLoad(targetCtx, target, client)
				targetCancel()
				if !load.HeartbeatOK {
					_ = client.Close()
					results <- discoveryResult{target: target, err: fmt.Errorf("generator heartbeat probe failed")}
					continue
				}
				results <- discoveryResult{target: target, client: client, load: load}
			}
		}()
	}
	for _, target := range allTargets {
		jobs <- target
	}
	close(jobs)
	go func() {
		discoveryWorkers.Wait()
		close(results)
	}()
	var discoveryFatal error
	for result := range results {
		if result.fatalErr != nil && discoveryFatal == nil {
			discoveryFatal = result.fatalErr
		}
		if result.err != nil {
			slog.Warn("distributed generator unavailable during placement", "target", result.target, "error", result.err)
			continue
		}
		if result.client != nil {
			clientsByTarget[result.target] = result.client
			loadByTarget[result.target] = result.load
		}
	}
	discoveryCancel()
	if discoveryFatal != nil {
		failStartup(discoveryFatal.Error())
		return
	}
	if len(clientsByTarget) == 0 {
		tr.failTestBeforeRunAndLog(test, "no reachable generator targets configured")
		return
	}
	defaultLoadByTarget := loadSnapshotsForTargets(loadByTarget, allowedTargets)
	peakGroupShards := planDistributedGroupShardsForCandidates(test.Groups, loadByTarget, defaultLoadByTarget, allTargets, allowedTargets, assignment, targetSpecs)
	preBaseline, postBaseline := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	preBaseline += testexec.StartDelayWindow(test.StartDelay)
	activeGroupShards := make(map[string][]GeneratorShardSummary, len(peakGroupShards))
	initialLoadByTarget := make(map[string]generatorLoadSnapshot, len(loadByTarget))
	for target, snapshot := range loadByTarget {
		snapshot.RuntimeCapacities = cloneProtocolRuntimeCapacities(snapshot.RuntimeCapacities)
		initialLoadByTarget[target] = snapshot
	}
	projectSecretResolvers := make(map[string]engine.SecretResolver)
	var shardNamespaceBase uint64
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
		if err := validateDistributedShardRunGoals(group, peakShards); err != nil {
			failStartup(fmt.Sprintf("prepare distributed group %q: %v", group.Name, err))
			return
		}
		shards := initialGroupShardsForRun(group, peakShards, initialLoadByTarget)
		if groupCurrentVUsers(group) > 0 && len(shards) == 0 {
			failStartup(fmt.Sprintf("prepare distributed group %q: insufficient runtime capacity for initial VUsers", group.Name))
			return
		}
		for _, shard := range shards {
			if shard.Target == "" {
				continue
			}
			snapshot := initialLoadByTarget[shard.Target]
			if shard.VUsers > 0 {
				snapshot.AvailableCapacity -= int32(shard.VUsers)
				snapshot.Stats.ActiveVUsers += int32(shard.VUsers)
			}
			consumeGroupRuntimeCapacity(&snapshot, group, shard.VUsers)
			initialLoadByTarget[shard.Target] = snapshot
		}
		if uint64(len(peakShards)) > ^uint64(0)-shardNamespaceBase {
			failStartup(fmt.Sprintf("prepare shard coordinates for group %q: namespace index overflow", group.Name))
			return
		}
		shards, err = decorateDistributedShardCoordinates(shards, peakShards, shardNamespaceBase)
		if err != nil {
			failStartup(fmt.Sprintf("prepare shard coordinates for group %q: %v", group.Name, err))
			return
		}
		shardNamespaceBase += uint64(len(peakShards))
		activeGroupShards[group.Name] = cloneGeneratorShardSummaries(shards)

		secretProjectID := group.script.ProjectID
		if strings.TrimSpace(secretProjectID) == "" {
			secretProjectID = test.ProjectID
		}
		secretResolver := projectSecretResolvers[secretProjectID]
		if secretResolver == nil {
			secretResolver = tr.secretResolverForProject(secretProjectID)
			projectSecretResolvers[secretProjectID] = secretResolver
		}
		secretResolver = overlayRuntimeSecretResolver(group.runtimeSecretValues, secretResolver)
		secretBundle, err := resolveDistributedSecretBundle(group.script, secretResolver)
		if err != nil {
			failStartup(fmt.Sprintf("prepare secrets for group %q: %v", group.Name, err))
			return
		}
		if err := validateDistributedSecretTransport(secretBundle); err != nil {
			failStartup(fmt.Sprintf("prepare secrets for group %q: %v", group.Name, err))
			return
		}
		secretBundleID := ""
		if len(secretBundle) > 0 {
			secretBundleID = uuid.NewString()
		}
		for _, shard := range shards {
			if strings.TrimSpace(shard.Target) == "" {
				continue
			}
			if err := validateDistributedSecretTarget(shard.Target, loadByTarget[shard.Target], secretBundle); err != nil {
				failStartup(fmt.Sprintf("prepare secrets for group %q: %v", group.Name, err))
				return
			}
		}
		if err := tr.storeDistributedRunSecretSnapshot(test, group.Name, secretBundleID, secretBundle); err != nil {
			failStartup(fmt.Sprintf("retain run secret snapshot for group %q: %v", group.Name, err))
			return
		}

		scriptContent, contentHash, dataFiles, err := generatorrpc.PrepareDistributedScript(group.script)
		if err != nil {
			failStartup(fmt.Sprintf("stage script %q: %v", group.ScriptName, err))
			return
		}
		rampUp, rampDown, initStrategy := distributedGroupRuntimeConfig(test, group)

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
				rpcCtx, rpcCancel := context.WithTimeout(executionCtx, b.timeout)
				_, distributeErr := client.DistributeScript(rpcCtx, generatorrpc.Script{
					ID:                  group.ScriptID,
					ExecutionGeneration: executionGeneration,
					Content:             scriptContent,
					ContentHash:         contentHash,
				})
				rpcCancel()
				if distributeErr != nil {
					failStartup(fmt.Sprintf("distribute script %q: %v", group.ScriptName, distributeErr))
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
			rpcCtx, rpcCancel := context.WithTimeout(executionCtx, b.timeout)
			assignment, err := client.AssignWork(rpcCtx, generatorrpc.Assignment{
				TestID:                    test.ID,
				ExecutionGeneration:       executionGeneration,
				ScenarioID:                test.ScenarioID,
				RequireTestExecutionLease: requireTestExecutionLease,
				GroupName:                 group.Name,
				TargetDuration:            test.Duration,
				ScriptID:                  group.ScriptID,
				ExpectedScriptHash:        contentHash,
				VUserCount:                int32(shard.VUsers),
				RampUp:                    rampUp,
				RampDown:                  rampDown,
				InitStrategy:              initStrategy,
				PreBaseline:               preBaseline,
				PostBaseline:              postBaseline,
				ShardOrdinal:              shard.ShardOrdinal,
				ShardCount:                shard.ShardCount,
				VUserIDOffset:             shard.VUserIDOffset,
				DataFiles:                 dataFiles,
				RedactionRules:            cloneRedactionRules(group.redactionRules),
				SecretBundle:              secretBundle,
				SecretBundleID:            secretBundleID,
			})
			rpcCancel()
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

	if requireTestExecutionLease {
		currentGeneration, currentOwnerID, leaseDeadline, claimErr := tr.generatorExecutionClaim()
		if claimErr != nil || currentGeneration != executionGeneration || currentOwnerID != executionLeaseOwnerID {
			failStartup("execution leadership changed before assignment lease establishment")
			return
		}
		if claimErr := b.renewGeneratorTestExecutionClients(executionCtx, test.ID, assignedClients, executionGeneration, executionLeaseOwnerID, leaseDeadline); claimErr != nil {
			failStartup(fmt.Sprintf("establish loss-tolerance assignment leases: %v", claimErr))
			return
		}
		initialLeaseCtx, cancelInitialLeaseKeeper := context.WithCancel(executionCtx)
		stopInitialLeaseKeeper = cancelInitialLeaseKeeper
		go b.maintainStagedGeneratorTestExecutionLeases(initialLeaseCtx, tr, test, executionGeneration, assignedClients)
	}

	for _, target := range orderedGeneratorTargets(assignedClients, allowedTargets) {
		if _, ok := streamsByTarget[target]; ok {
			continue
		}
		stream, err := b.openMetricsStream(executionCtx, assignedClients[target], test.ID, executionGeneration)
		if err != nil {
			failStartup(fmt.Sprintf("open generator metrics stream for %q: %v", target, err))
			return
		}
		streamsByTarget[target] = stream
	}

	started, activate, startErr := b.prepareDistributedTestRun(tr, test, activeGroupShards, assignedClients, streamsByTarget)
	if startErr != nil {
		failStartup(startErr.Error())
		return
	}
	if !started {
		cleanup(true)
		return
	}
	// Choose and durably persist the exact barrier only after the initial RUNNING
	// transition. If persistence consumes its safety window, regenerate a later
	// barrier before any generator is allowed to commit.
	commitStartAt, barrierErr := persistFreshDistributedScheduledStart(tr, test, b.timeout)
	if barrierErr != nil {
		message := fmt.Sprintf("persist coordinated distributed start: %v", barrierErr)
		if terminalErr := b.failClosedDistributedCommit(tr, test, message); terminalErr != nil {
			slog.Error("distributed start persistence failure teardown remains retryable", "test_id", test.ID, "error", terminalErr)
		}
		return
	}
	if err := b.commitDistributedWork(executionCtx, test.ID, executionGeneration, commitStartAt, assignedClients); err != nil {
		message := fmt.Sprintf("commit distributed work: %v", err)
		if terminalErr := b.failClosedDistributedCommit(tr, test, message); terminalErr != nil {
			slog.Error("distributed commit failure teardown remains retryable", "test_id", test.ID, "error", terminalErr)
		}
		return
	}
	activate()
	if requireTestExecutionLease {
		// The staged keeper protects the assignment through metrics setup and
		// coordinated commit. Once the live topology is published, start its
		// topology-aware successor before cancelling the staged owner so there is
		// no unowned handoff window and no duplicate steady-state renewal loop.
		b.handoffStagedGeneratorTestExecutionLeaseKeeper(executionCtx, tr, test, executionGeneration, stopInitialLeaseKeeper)
		stopInitialLeaseKeeper = nil
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

func decorateDistributedShardCoordinates(active []GeneratorShardSummary, peak []GeneratorShardSummary, namespaceBaseOption ...uint64) ([]GeneratorShardSummary, error) {
	namespaceBase := uint64(0)
	if len(namespaceBaseOption) > 0 {
		namespaceBase = namespaceBaseOption[0]
	}
	if len(active) == 0 {
		return nil, nil
	}
	if len(peak) == 0 || uint64(len(peak)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("invalid peak shard count %d", len(peak))
	}
	for index, shard := range peak {
		if shard.VUsers < 0 {
			return nil, fmt.Errorf("peak shard %d has negative vuser count", index)
		}
	}
	used := make([]bool, len(peak))
	decorated := cloneGeneratorShardSummaries(active)
	for activeIndex := range decorated {
		peakIndex := -1
		for candidate := range peak {
			if !used[candidate] && peak[candidate].Target == decorated[activeIndex].Target {
				peakIndex = candidate
				break
			}
		}
		if peakIndex < 0 {
			return nil, fmt.Errorf("active target %q is absent from peak placement", decorated[activeIndex].Target)
		}
		used[peakIndex] = true
		namespaceIndex := namespaceBase + uint64(peakIndex)
		if namespaceIndex > ^uint64(0)/generatorrpc.DistributedVUserIDNamespaceSize {
			return nil, fmt.Errorf("shard vuser id namespace offset overflows uint64")
		}
		decorated[activeIndex].ShardOrdinal = uint32(peakIndex)
		decorated[activeIndex].ShardCount = uint32(len(peak))
		decorated[activeIndex].VUserIDOffset = namespaceIndex * generatorrpc.DistributedVUserIDNamespaceSize
	}
	return decorated, nil
}

func distributedGroupRuntimeConfig(test *ManagedTest, group TestGroup) (time.Duration, time.Duration, string) {
	rampUp := group.rampUpDur
	rampDown := time.Duration(0)
	initStrategy := strings.TrimSpace(group.Initialization)
	if test != nil {
		for _, spec := range test.groupSpecs {
			if spec.Name != group.Name {
				continue
			}
			rampUp = spec.RampUp
			rampDown = spec.RampDown
			if strings.TrimSpace(spec.InitStrategy) != "" {
				initStrategy = spec.InitStrategy
			}
			break
		}
	}
	if initStrategy == "" {
		initStrategy = "at_runtime"
	}
	return rampUp, rampDown, initStrategy
}

func distributedEvacuationRemainingRamp(configuredRamp, steadyElapsed time.Duration) time.Duration {
	return max(configuredRamp-steadyElapsed, time.Duration(0))
}

func distributedCommitStartLead(rpcTimeout time.Duration) time.Duration {
	// Commit is a local staged-state transition and gets a dedicated bounded ACK
	// window. The common barrier must be strictly later than that entire window:
	// otherwise a fast generator can start while a slow peer is still able to
	// time out or reject, producing partial fleet load before fail-closed cleanup.
	ackWindow := distributedCommitAckTimeout(rpcTimeout)
	margin := ackWindow / 10
	if margin < 500*time.Millisecond {
		margin = 500 * time.Millisecond
	}
	if margin > time.Second {
		margin = time.Second
	}
	return ackWindow + margin
}

func distributedCommitAckTimeout(rpcTimeout time.Duration) time.Duration {
	if rpcTimeout <= 0 {
		rpcTimeout = 10 * time.Second
	}
	// A commit only flips already-validated staged state. Bounding this phase
	// avoids making every run wait the generic (often 10s) generator RPC timeout
	// while retaining a barrier that provably covers its complete failure window.
	return min(rpcTimeout, 2*time.Second)
}

func (b *distributedExecutionBackend) commitDistributedWork(parent context.Context, testID string, executionGeneration uint64, startAt time.Time, clients map[string]generatorClient) error {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return err
	}
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	if len(targets) == 0 {
		return fmt.Errorf("no assigned generator clients")
	}
	committers := make(map[string]coordinatedWorkCommitClient, len(targets))
	for _, target := range targets {
		client := clients[target]
		if client == nil {
			return fmt.Errorf("generator client %q is unavailable", target)
		}
		committer, ok := client.(coordinatedWorkCommitClient)
		if !ok {
			return fmt.Errorf("generator client %q does not support coordinated staged work commit", target)
		}
		committers[target] = committer
	}

	type commitResult struct {
		target string
		err    error
	}
	results := make(chan commitResult, len(targets))
	for _, target := range targets {
		target := target
		go func() {
			ctx, cancel := context.WithTimeout(parent, distributedCommitAckTimeout(b.timeout))
			result, err := committers[target].CommitWorkAt(ctx, testID, executionGeneration, startAt)
			cancel()
			if err == nil && !result.Accepted {
				err = fmt.Errorf("generator rejected commit: %s", strings.TrimSpace(result.ErrorMessage))
			}
			results <- commitResult{target: target, err: err}
		}()
	}
	failures := make([]error, 0)
	for range targets {
		result := <-results
		if result.err != nil {
			failures = append(failures, fmt.Errorf("commit work on %s: %w", result.target, result.err))
		}
	}
	return errors.Join(failures...)
}

func (b *distributedExecutionBackend) failClosedDistributedCommit(tr *TestRunner, test *ManagedTest, message string) error {
	if tr == nil || test == nil {
		return fmt.Errorf("distributed commit failure has no managed test")
	}
	tr.mu.Lock()
	if tr.tests[test.ID] != test || isTerminalTestStatus(test.Status) {
		tr.mu.Unlock()
		return nil
	}
	test.Status = "STOPPING"
	test.Error = message
	test.remoteFinalizing = true
	persistErr := retryDurableTransition(func() error { return tr.saveManagedTestRecord(test) })
	tr.cancelAllVUserScaleRampsLocked(test)
	cancel := test.cancel
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()
	tr.broadcastEvent("test_stopping", eventSnapshot)

	stopAndFinalize := func() error {
		if err := b.StopTestContext(context.Background(), tr, test); err != nil {
			return err
		}
		b.closeRemoteResources(tr, test)
		return tr.completeTestRun(test, "FAILED", time.Now(), message, nil)
	}
	stopErr := stopAndFinalize()
	if cancel != nil {
		cancel()
	}
	if stopErr != nil {
		tr.enqueueTerminalRetry("commit-stop:"+test.ID, "distributed startup commit teardown", stopAndFinalize)
	}
	var persistWrapped error
	if persistErr != nil {
		persistWrapped = fmt.Errorf("persist distributed commit failure STOPPING state: %w", persistErr)
	}
	return errors.Join(persistWrapped, stopErr)
}

func (b *distributedExecutionBackend) StopTest(tr *TestRunner, test *ManagedTest) error {
	return b.stopTestContext(context.Background(), tr, test, true)
}

func (b *distributedExecutionBackend) StopTestContext(parent context.Context, tr *TestRunner, test *ManagedTest) error {
	return b.stopTestContext(parent, tr, test, true)
}

func (b *distributedExecutionBackend) AbortTest(tr *TestRunner, test *ManagedTest) error {
	return b.stopTestContext(context.Background(), tr, test, false)
}

func (b *distributedExecutionBackend) AbortTestContext(parent context.Context, tr *TestRunner, test *ManagedTest) error {
	return b.stopTestContext(parent, tr, test, false)
}

func (b *distributedExecutionBackend) stopTestContext(parent context.Context, tr *TestRunner, test *ManagedTest, graceful bool) error {
	if parent == nil {
		parent = context.Background()
	}
	tr.mu.RLock()
	clientsByTarget, single, remoteTarget := cloneClientMap(test.remoteClientsByTarget), test.remoteClient, test.remoteTarget
	status := test.Status
	executionGeneration := test.executionGeneration
	executionCtx := test.executionCtx
	tr.mu.RUnlock()
	// Stop work is canceled by either the caller's deadline (shutdown/API) or
	// the run/leadership epoch. Using executionCtx alone would silently ignore a
	// bounded parent whenever a live run context was present.
	stopBase, stopBaseCancel := context.WithCancel(parent)
	defer stopBaseCancel()
	// Once STOPPING is durable, executionCtx is intentionally canceled to halt
	// local run work. Teardown retries must nevertheless reach the exact remote
	// generation; binding them to that already-canceled context would make every
	// retry fail before dialing or dispatching a Stop RPC. The caller supplies the
	// bounded teardown context and the generation fence prevents stopping a newer
	// incarnation of the same test.
	if executionCtx != nil && status != "STOPPING" {
		stopExecutionCancel := context.AfterFunc(executionCtx, stopBaseCancel)
		defer stopExecutionCancel()
	}
	clients := clientsByTarget
	var dialed generatorClient
	if len(clients) == 0 {
		if single != nil {
			clients = map[string]generatorClient{remoteTarget: single}
		} else {
			if remoteTarget == "" {
				return fmt.Errorf("distributed generator target is not recorded")
			}
			ctx, cancel := context.WithTimeout(stopBase, b.timeout)
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
		target    string
		attempted bool
		pending   int32
		err       error
	}
	results := make(chan stopResult, len(targets))
	for _, target := range targets {
		target := target
		go func() {
			if err := stopBase.Err(); err != nil {
				results <- stopResult{target: target, err: err}
				return
			}
			client := clients[target]
			if client == nil {
				results <- stopResult{target: target, err: fmt.Errorf("generator client is unavailable")}
				return
			}
			ctx, cancel := context.WithTimeout(stopBase, b.timeout)
			stop, err := stopGeneratorTest(ctx, client, test.ID, graceful, executionGeneration)
			cancel()
			if err == nil && !stop.Acknowledged {
				err = fmt.Errorf("generator did not acknowledge stop for %s", test.ID)
			}
			results <- stopResult{target: target, attempted: true, pending: stop.PendingVUsers, err: err}
		}()
	}
	var stopErrs []error
	attemptedTargets := make([]string, 0, len(targets))
	acknowledgedTargets := make([]string, 0, len(targets))
	for range targets {
		result := <-results
		if result.attempted {
			attemptedTargets = append(attemptedTargets, result.target)
		}
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
	sort.Strings(attemptedTargets)
	sort.Strings(acknowledgedTargets)
	stopErr := errors.Join(stopErrs...)
	if stopErr == nil {
		if graceful {
			b.startDistributedStopFinalizer(tr, test, executionGeneration)
		} else {
			b.finalizeAcknowledgedForcedStop(tr, test)
		}
		return nil
	}
	return &distributedStopFanoutError{
		attemptedTargets:    attemptedTargets,
		acknowledgedTargets: acknowledgedTargets,
		err:                 stopErr,
	}
}

func initialGroupShardsForRun(group TestGroup, peakShards []GeneratorShardSummary, loadByTarget map[string]generatorLoadSnapshot) []GeneratorShardSummary {
	if !group.stageDriven {
		return cloneGeneratorShardSummaries(peakShards)
	}
	targets := shardTargets(peakShards)
	group.VUsers = groupCurrentVUsers(group)
	return capacityWeightedShardsForGroupIncludingZero(group, targets, loadByTarget, shardStrategyForGroupShards(peakShards))
}

func (b *distributedExecutionBackend) ScaleVUsers(tr *TestRunner, test *ManagedTest, groupName string, targetVUsers int) error {
	if targetVUsers < 0 || targetVUsers > maxManagedTestVUsers {
		return fmt.Errorf("target vusers must be between 0 and %d", maxManagedTestVUsers)
	}
	tr.mu.RLock()
	if tr.tests[test.ID] != test || test.Status != "RUNNING" || test.remoteFinalizing {
		status := test.Status
		tr.mu.RUnlock()
		return fmt.Errorf("distributed test is %s and cannot be scaled", status)
	}
	controlGeneration := test.remoteControlGeneration
	executionGeneration := test.executionGeneration
	executionCtx := test.executionCtx
	groupExists := false
	var scaleGroup TestGroup
	for _, group := range test.Groups {
		if group.Name == groupName {
			groupExists = true
			scaleGroup = group
			break
		}
	}
	shards := cloneGeneratorShardSummaries(test.remoteGroupShards[groupName])
	groupTarget, groupTargetOK := test.remoteGroupTargets[groupName]
	previousGroupVUsers, hasPreviousGroupVUsers := test.scaleAdjustmentPrevious[groupName]
	if !hasPreviousGroupVUsers {
		previousGroupVUsers = testGroupCurrentShardVUsers(test, groupName)
	}
	clientsByTarget, single, remoteTarget := cloneClientMap(test.remoteClientsByTarget), test.remoteClient, test.remoteTarget
	tr.mu.RUnlock()
	if previousGroupVUsers < 0 || previousGroupVUsers > maxManagedTestVUsers {
		return fmt.Errorf("stored group VUser count must be between 0 and %d", maxManagedTestVUsers)
	}
	for _, shard := range shards {
		if shard.VUsers < 0 || shard.VUsers > maxManagedTestVUsers {
			return fmt.Errorf("stored shard VUser count must be between 0 and %d", maxManagedTestVUsers)
		}
	}
	if executionCtx == nil {
		executionCtx = context.Background()
	}

	if !groupExists {
		return fmt.Errorf("test group not found: %s", groupName)
	}
	if len(shards) == 0 {
		if groupTargetOK && groupTarget != "" {
			shards = []GeneratorShardSummary{{Group: groupName, Target: groupTarget, VUsers: previousGroupVUsers}}
		}
	}
	if len(shards) == 0 {
		client, err := b.groupClientFromSnapshot(groupTarget, groupTargetOK, clientsByTarget, single, remoteTarget)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(executionCtx, b.timeout)
		defer cancel()
		adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
			TestID:              test.ID,
			ExecutionGeneration: executionGeneration,
			GroupName:           groupName,
			TargetVUsers:        int32(targetVUsers),
		})
		if err != nil || !adjustment.Accepted {
			forwardErr := err
			if forwardErr != nil {
				forwardErr = fmt.Errorf("adjust remote vusers: %w", forwardErr)
			} else {
				forwardErr = fmt.Errorf("generator did not accept vuser adjustment for %s", test.ID)
			}
			rollbackErr := b.rollbackSingleShardVUsers(test.ID, executionGeneration, groupName, client, previousGroupVUsers)
			if rollbackErr != nil {
				return &distributedVUserUncertainError{
					testID:            test.ID,
					unresolvedTargets: []string{firstNonEmpty(groupTarget, remoteTarget, "generator")},
					err:               errors.Join(forwardErr, fmt.Errorf("restore remote vusers after ambiguous adjustment: %w", rollbackErr)),
				}
			}
			return forwardErr
		}
		tr.mu.Lock()
		current := distributedControlCommitCurrentLocked(tr, test, "RUNNING", controlGeneration)
		tr.mu.Unlock()
		if !current {
			rollbackErr := b.rollbackSingleShardVUsers(test.ID, executionGeneration, groupName, client, previousGroupVUsers)
			if rollbackErr != nil {
				return &distributedVUserUncertainError{
					testID:            test.ID,
					unresolvedTargets: []string{firstNonEmpty(groupTarget, remoteTarget, "generator")},
					err:               fmt.Errorf("distributed scale became stale; rollback vusers: %w", rollbackErr),
				}
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
			ctx, cancel := context.WithTimeout(executionCtx, b.timeout)
			defer cancel()
			snapshot := b.probeGeneratorLoad(ctx, target, client)
			currentShardVUsers := shardVUsersForTarget(shards, target)
			snapshot.AvailableCapacity += int32(currentShardVUsers)
			restoreGroupRuntimeCapacity(&snapshot, scaleGroup, currentShardVUsers)
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
	scaleGroup.VUsers = targetVUsers
	nextShards := capacityWeightedShardsForGroupIncludingZero(scaleGroup, targets, loadByTarget, shardStrategyForGroupShards(shards))
	if len(nextShards) == 0 {
		return fmt.Errorf("no selected generator has sufficient java_vuser runtime capacity for %d VUsers", targetVUsers)
	}
	nextShards, err = preserveShardCoordinatesByTarget(nextShards, shards)
	if err != nil {
		return fmt.Errorf("preserve distributed shard coordinates: %w", err)
	}
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
			ctx, cancel := context.WithTimeout(executionCtx, b.timeout)
			defer cancel()
			adjustment, err := client.AdjustVUsers(ctx, generatorrpc.VUserAdjustment{
				TestID:              test.ID,
				ExecutionGeneration: executionGeneration,
				GroupName:           groupName,
				TargetVUsers:        int32(shard.VUsers),
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
	attempted := make([]GeneratorShardSummary, 0, len(nextShards))
	var adjustErr error
	for range nextShards {
		result := <-adjustCh
		if clients[result.shard.Target] != nil {
			// A transport error is ambiguous: the generator may have applied the
			// adjustment before its response was lost. Compensate every dispatched
			// RPC, not merely the requests whose acknowledgements arrived.
			attempted = append(attempted, result.shard)
		}
		if result.err != nil && adjustErr == nil {
			adjustErr = result.err
			continue
		}
	}
	if adjustErr != nil {
		if rollbackErr := b.rollbackShardVUsers(executionCtx, test.ID, executionGeneration, groupName, clients, attempted, previousByTarget); rollbackErr != nil {
			return &distributedVUserUncertainError{
				testID:            test.ID,
				unresolvedTargets: shardTargets(attempted),
				err:               errors.Join(adjustErr, fmt.Errorf("rollback shard vusers: %w", rollbackErr)),
			}
		}
		return adjustErr
	}
	tr.mu.Lock()
	if !distributedControlCommitCurrentLocked(tr, test, "RUNNING", controlGeneration) {
		tr.mu.Unlock()
		if rollbackErr := b.rollbackShardVUsers(executionCtx, test.ID, executionGeneration, groupName, clients, attempted, previousByTarget); rollbackErr != nil {
			return &distributedVUserUncertainError{
				testID:            test.ID,
				unresolvedTargets: shardTargets(attempted),
				err:               fmt.Errorf("distributed scale became stale before commit; rollback shard vusers: %w", rollbackErr),
			}
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

func preserveShardCoordinatesByTarget(next []GeneratorShardSummary, previous []GeneratorShardSummary) ([]GeneratorShardSummary, error) {
	previousByTarget := make(map[string]GeneratorShardSummary, len(previous))
	for _, shard := range previous {
		if _, duplicate := previousByTarget[shard.Target]; duplicate {
			return nil, fmt.Errorf("duplicate previous shard target %q", shard.Target)
		}
		previousByTarget[shard.Target] = shard
	}
	out := cloneGeneratorShardSummaries(next)
	for index := range out {
		prior, ok := previousByTarget[out[index].Target]
		if !ok {
			return nil, fmt.Errorf("new shard target %q has no stable coordinate", out[index].Target)
		}
		out[index].ShardOrdinal = prior.ShardOrdinal
		out[index].ShardCount = prior.ShardCount
		out[index].VUserIDOffset = prior.VUserIDOffset
	}
	return out, nil
}

func (b *distributedExecutionBackend) AdjustDuration(tr *TestRunner, test *ManagedTest, newDuration time.Duration) error {
	tr.mu.RLock()
	previousDuration := test.durationAdjustmentPrevious
	if previousDuration <= 0 {
		previousDuration = test.Duration
	}
	executionGeneration := test.executionGeneration
	executionCtx := test.executionCtx
	tr.mu.RUnlock()
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	clients, err := b.resolveClientsLocked(tr, test)
	if err != nil {
		return err
	}
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	attempted := make([]string, 0, len(targets))
	for _, target := range targets {
		attempted = append(attempted, target)
		ctx, cancel := context.WithTimeout(executionCtx, b.timeout)
		result, err := clients[target].AdjustDuration(ctx, generatorrpc.DurationAdjustment{
			TestID:                test.ID,
			ExecutionGeneration:   executionGeneration,
			TargetDuration:        newDuration,
			TargetDurationSeconds: distributedCompatibilityDurationSeconds(newDuration),
		})
		cancel()
		if err != nil {
			forwardErr := fmt.Errorf("adjust remote duration on %s: %w", target, err)
			rollbackErrs, unresolved := b.rollbackDurations(test.ID, executionGeneration, clients, attempted, previousDuration)
			joined := errors.Join(append([]error{forwardErr}, rollbackErrs...)...)
			if len(unresolved) > 0 {
				return &distributedDurationUncertainError{testID: test.ID, unresolvedTargets: unresolved, err: joined}
			}
			return joined
		}
		if !result.Accepted {
			forwardErr := fmt.Errorf("generator %s did not accept duration adjustment for %s", target, test.ID)
			rollbackErrs, unresolved := b.rollbackDurations(test.ID, executionGeneration, clients, attempted, previousDuration)
			joined := errors.Join(append([]error{forwardErr}, rollbackErrs...)...)
			if len(unresolved) > 0 {
				return &distributedDurationUncertainError{testID: test.ID, unresolvedTargets: unresolved, err: joined}
			}
			return joined
		}
	}
	return nil
}

func (b *distributedExecutionBackend) rollbackDurations(testID string, executionGeneration uint64, clients map[string]generatorClient, attempted []string, previousDuration time.Duration) ([]error, []string) {
	rollbackErrs := make([]error, 0)
	unresolved := make([]string, 0)
	for i := len(attempted) - 1; i >= 0; i-- {
		target := attempted[i]
		client := clients[target]
		if client == nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restore remote duration on %s: generator is not connected", target))
			unresolved = append(unresolved, target)
			continue
		}
		var rollbackErr error
		for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
			result, err := client.AdjustDuration(ctx, generatorrpc.DurationAdjustment{
				TestID:                testID,
				ExecutionGeneration:   executionGeneration,
				TargetDuration:        previousDuration,
				TargetDurationSeconds: distributedCompatibilityDurationSeconds(previousDuration),
			})
			cancel()
			if err == nil && result.Accepted {
				rollbackErr = nil
				break
			}
			if err != nil {
				rollbackErr = err
			} else {
				rollbackErr = fmt.Errorf("generator did not accept duration rollback for %s", testID)
			}
			if attempt+1 < durableTransitionAttempts {
				time.Sleep(durableTransitionRetryDelay)
			}
		}
		if rollbackErr != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restore remote duration on %s: %w", target, rollbackErr))
			unresolved = append(unresolved, target)
		}
	}
	sort.Strings(unresolved)
	return rollbackErrs, unresolved
}

func distributedCompatibilityDurationSeconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	seconds := int64(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	return seconds
}

func (b *distributedExecutionBackend) PauseTest(tr *TestRunner, test *ManagedTest) error {
	if used, err := b.applyAcknowledgedPauseControl(tr, test, true); used {
		return err
	}
	streams := b.snapshotMetricsStreams(tr, test)
	if len(streams) == 0 {
		return fmt.Errorf("distributed metrics stream is not initialized")
	}
	targets := orderedMetricsTargets(streams, b.configuredTargets())
	tr.mu.RLock()
	executionGeneration := test.executionGeneration
	tr.mu.RUnlock()
	return applyPauseControlFanout(test.ID, true, targets, func(target string, pause bool) error {
		return streams[target].SendPause(generatorrpc.PauseControl{TestID: test.ID, ExecutionGeneration: executionGeneration, Pause: pause})
	})
}

func (b *distributedExecutionBackend) ResumeTest(tr *TestRunner, test *ManagedTest) error {
	if used, err := b.applyAcknowledgedPauseControl(tr, test, false); used {
		return err
	}
	streams := b.snapshotMetricsStreams(tr, test)
	if len(streams) == 0 {
		return fmt.Errorf("distributed metrics stream is not initialized")
	}
	targets := orderedMetricsTargets(streams, b.configuredTargets())
	tr.mu.RLock()
	executionGeneration := test.executionGeneration
	tr.mu.RUnlock()
	return applyPauseControlFanout(test.ID, false, targets, func(target string, pause bool) error {
		return streams[target].SendPause(generatorrpc.PauseControl{TestID: test.ID, ExecutionGeneration: executionGeneration, Pause: pause})
	})
}

func (b *distributedExecutionBackend) applyAcknowledgedPauseControl(tr *TestRunner, test *ManagedTest, pause bool) (bool, error) {
	clients, err := b.resolveClientsLocked(tr, test)
	if err != nil {
		return true, err
	}
	targets := orderedGeneratorTargets(clients, b.configuredTargets())
	if len(targets) == 0 {
		return true, fmt.Errorf("distributed generator clients are not initialized")
	}
	for _, target := range targets {
		if _, ok := clients[target].(acknowledgedPauseClient); !ok {
			// Legacy/unit-test adapters predate the acknowledged unary method.
			return false, nil
		}
	}

	tr.mu.Lock()
	executionGeneration := test.executionGeneration
	executionCtx := test.executionCtx
	test.remotePauseOperationSequence++
	if test.remotePauseOperationSequence == 0 {
		test.remotePauseOperationSequence++
	}
	operationSequence := test.remotePauseOperationSequence
	tr.mu.Unlock()
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	operationID := uuid.NewString()
	attempted := make([]string, 0, len(targets))
	for _, target := range targets {
		attempted = append(attempted, target)
		client := clients[target].(acknowledgedPauseClient)
		if err := b.applyPauseControlWithRetry(executionCtx, client, generatorrpc.PauseControl{
			TestID:              test.ID,
			ExecutionGeneration: executionGeneration,
			Pause:               pause,
			OperationID:         operationID,
			OperationSequence:   operationSequence,
		}); err != nil {
			forwardErr := fmt.Errorf("apply acknowledged remote %s on %s: %w", pauseOperationName(pause), target, err)
			compensationID := uuid.NewString()
			tr.mu.Lock()
			test.remotePauseOperationSequence++
			if test.remotePauseOperationSequence == 0 {
				test.remotePauseOperationSequence++
			}
			compensationSequence := test.remotePauseOperationSequence
			tr.mu.Unlock()
			unresolved := make([]string, 0)
			compensationErrs := []error{forwardErr}
			for i := len(attempted) - 1; i >= 0; i-- {
				rollbackTarget := attempted[i]
				rollbackClient := clients[rollbackTarget].(acknowledgedPauseClient)
				rollbackErr := b.applyPauseControlWithRetry(context.Background(), rollbackClient, generatorrpc.PauseControl{
					TestID:              test.ID,
					ExecutionGeneration: executionGeneration,
					Pause:               !pause,
					OperationID:         compensationID,
					OperationSequence:   compensationSequence,
				})
				if rollbackErr != nil {
					unresolved = append(unresolved, rollbackTarget)
					compensationErrs = append(compensationErrs, fmt.Errorf("compensate remote %s on %s: %w", pauseOperationName(!pause), rollbackTarget, rollbackErr))
				}
			}
			joined := errors.Join(compensationErrs...)
			if len(unresolved) > 0 {
				sort.Strings(unresolved)
				return true, &distributedPauseUncertainError{testID: test.ID, unresolvedTargets: unresolved, err: joined}
			}
			return true, joined
		}
	}
	return true, nil
}

func pauseOperationName(pause bool) string {
	if pause {
		return "pause"
	}
	return "resume"
}

func (b *distributedExecutionBackend) applyPauseControlWithRetry(parent context.Context, client acknowledgedPauseClient, control generatorrpc.PauseControl) error {
	var lastErr error
	for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(parent, b.timeout)
		result, err := client.SetPaused(ctx, control)
		cancel()
		if err == nil && result.Accepted && result.CurrentPaused == control.Pause && result.AppliedOperationID == control.OperationID && result.AppliedOperationSequence == control.OperationSequence {
			return nil
		}
		switch {
		case err != nil:
			lastErr = err
		case !result.Accepted:
			lastErr = fmt.Errorf("generator rejected operation %s: %s", control.OperationID, result.ErrorMessage)
		case result.AppliedOperationID != control.OperationID:
			lastErr = fmt.Errorf("generator acknowledged operation %q, want %q", result.AppliedOperationID, control.OperationID)
		case result.AppliedOperationSequence != control.OperationSequence:
			lastErr = fmt.Errorf("generator acknowledged operation sequence %d, want %d", result.AppliedOperationSequence, control.OperationSequence)
		default:
			lastErr = fmt.Errorf("generator pause state is %t, want %t", result.CurrentPaused, control.Pause)
		}
		if attempt+1 < durableTransitionAttempts {
			time.Sleep(durableTransitionRetryDelay)
		}
	}
	return lastErr
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
