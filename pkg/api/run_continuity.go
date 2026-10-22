package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

// HA run continuity (docs/HA_RUN_CONTINUITY_PLAN.md). This file holds the P1
// persistence layer: the per-run policy and the adoption checkpoint a successor
// leader needs. Nothing here changes what happens on failover yet.
const (
	failoverPolicyFail     = "fail"
	failoverPolicyContinue = "continue"

	runContinuityCheckpointVersion = 1
	// runContinuityArmTimeout bounds sealing and persisting one checkpoint.
	runContinuityArmTimeout = 10 * time.Second

	// runAdoptionImplemented gates every user-visible claim that a
	// continue-policy run survives failover. It flips when the successor
	// adoption path (plan P3) lands; until then the checkpoint is recorded but
	// a failover still fails the run, and preflight says so.
	runAdoptionImplemented = true
)

// runContinuityCheckpoint is everything a successor leader needs to adopt a
// distributed run that it cannot rebuild from the stored test row. Generator
// placement is not part of the test row, so it is captured here.
type runContinuityCheckpoint struct {
	Version               int    `json:"version"`
	ExecutionGeneration   uint64 `json:"execution_generation"`
	ExecutionLeaseOwnerID string `json:"execution_lease_owner_id"`
	// RunSupervisionToken is the generator deadman token, wrapped by the
	// controller key wrapper and bound (AAD) to this run and generation. The
	// generator stores only its hash; presenting it proves run lineage.
	RunSupervisionToken    keywrap.WrappedKey                 `json:"run_supervision_token"`
	PinnedSecretCiphertext string                             `json:"pinned_secret_ciphertext,omitempty"`
	PinnedSecretKey        *keywrap.WrappedKey                `json:"pinned_secret_key,omitempty"`
	Shards                 map[string][]GeneratorShardSummary `json:"shards"`
	CompletedTargets       []string                           `json:"completed_targets,omitempty"`
	// StoppedTargets are successor-confirmed force-stop ACKs or retained
	// terminal outcomes, durably recorded per target during failed adoption
	// cleanup. They are proof only for a STOPPING run; an ordinary RUNNING
	// adoption must still inspect the target.
	StoppedTargets []string  `json:"stopped_targets,omitempty"`
	ArchiveTargets []string  `json:"archive_targets,omitempty"`
	ArmedAt        time.Time `json:"armed_at"`
	// Failovers is the run's adoption history, carried forward on every re-arm
	// so it survives further failovers and is available after the run ends.
	Failovers        []RunFailover       `json:"failovers,omitempty"`
	PendingAdoptions []runAdoptionIntent `json:"pending_adoptions,omitempty"`
}

// RunFailover records one controller failover the run survived.
type RunFailover struct {
	PriorGeneration uint64    `json:"prior_generation"`
	Generation      uint64    `json:"generation"`
	AdoptedAt       time.Time `json:"adopted_at"`
	// LastObservedAt is the newest live-metrics sample persisted before the
	// failover; the controller has no observations between it and AdoptedAt.
	LastObservedAt      *time.Time `json:"last_observed_at,omitempty"`
	GapSeconds          float64    `json:"gap_seconds,omitempty"`
	AdoptedGenerators   int        `json:"adopted_generators"`
	CompletedGenerators int        `json:"completed_generators,omitempty"`
}

// RunContinuityStatus is the public HA continuity state of a run.
type RunContinuityStatus struct {
	// Policy is fail or continue.
	Policy string `json:"policy"`
	// Armed means a successor controller holds what it needs to adopt the run.
	Armed               bool          `json:"armed"`
	ExecutionGeneration uint64        `json:"execution_generation,omitempty"`
	Failovers           []RunFailover `json:"failovers,omitempty"`
}

// runContinuityStatusLocked derives the public status from the policy and the
// persisted checkpoint (never exposing the sealed token). It returns nil for a
// fail-policy run that never failed over. The caller holds tr.mu.
func runContinuityStatusLocked(test *ManagedTest) *RunContinuityStatus {
	if test == nil {
		return nil
	}
	policy := effectiveFailoverPolicy(test.Assignment)
	if policy != failoverPolicyContinue && len(test.continuityFailovers) == 0 {
		return nil
	}
	armed := test.continuityArmedGeneration > 0
	return &RunContinuityStatus{
		Policy:              policy,
		Armed:               armed && !isTerminalTestStatus(test.Status),
		ExecutionGeneration: test.continuityArmedGeneration,
		Failovers:           append([]RunFailover(nil), test.continuityFailovers...),
	}
}

// setContinuityCheckpointLocked stores a checkpoint and decodes it once, so
// reads (every API clone) never parse JSON. The stored failover history is
// adopted only when it is at least as long as the in-memory one: a failover
// recorded by a re-arm that could not be persisted is never discarded. The
// caller holds tr.mu or owns test exclusively.
func setContinuityCheckpointLocked(test *ManagedTest, raw string) {
	test.continuityCheckpoint = raw
	test.continuityArmedGeneration = 0
	checkpoint, present, err := decodeRunContinuityCheckpoint(raw)
	if err != nil || !present {
		var guard runEvacuationCheckpoint
		if json.Unmarshal([]byte(raw), &guard) == nil && (guard.Version == 2 || supportedRunEvacuationCheckpointVersion(guard.Version)) && len(guard.Prior.Failovers) >= len(test.continuityFailovers) {
			test.continuityFailovers = append([]RunFailover(nil), guard.Prior.Failovers...)
		}
		return
	}
	test.continuityArmedGeneration = checkpoint.ExecutionGeneration
	if len(checkpoint.Failovers) >= len(test.continuityFailovers) {
		test.continuityFailovers = append([]RunFailover(nil), checkpoint.Failovers...)
	}
}

// WithRunContinuityKeyWrapper supplies the key wrapper used to seal the
// run-supervision token into the adoption checkpoint. Without one, a
// continue-policy run is never armed.
func WithRunContinuityKeyWrapper(wrapper keywrap.KeyWrapper) TestRunnerOption {
	return func(tr *TestRunner) {
		tr.continuityKeyWrapper = wrapper
	}
}

func effectiveFailoverPolicy(assignment *GeneratorAssignmentSettings) string {
	if assignment != nil && strings.EqualFold(strings.TrimSpace(assignment.FailoverPolicy), failoverPolicyContinue) {
		return failoverPolicyContinue
	}
	return failoverPolicyFail
}

// runSupervisionTokenAAD binds a sealed token to one run and one controller
// term, so a checkpoint copied onto another run (or replayed from an older
// term) cannot be unsealed.
func runSupervisionTokenAAD(testID string, generation uint64) []byte {
	return []byte("apexload:run-supervision:" + testID + ":" + strconv.FormatUint(generation, 10))
}

func sealRunSupervisionToken(ctx context.Context, wrapper keywrap.KeyWrapper, testID string, generation uint64, token []byte) (keywrap.WrappedKey, error) {
	if wrapper == nil {
		return keywrap.WrappedKey{}, errors.New("run continuity key wrapper is not configured")
	}
	if len(token) != generatorrpc.RunSupervisionTokenSize {
		return keywrap.WrappedKey{}, fmt.Errorf("run supervision token must be %d bytes", generatorrpc.RunSupervisionTokenSize)
	}
	return wrapper.WrapKey(ctx, token, runSupervisionTokenAAD(testID, generation))
}

func openRunSupervisionToken(ctx context.Context, wrapper keywrap.KeyWrapper, testID string, checkpoint runContinuityCheckpoint) ([]byte, error) {
	if wrapper == nil {
		return nil, errors.New("run continuity key wrapper is not configured")
	}
	token, err := wrapper.UnwrapKey(ctx, checkpoint.RunSupervisionToken, runSupervisionTokenAAD(testID, checkpoint.ExecutionGeneration))
	if err != nil {
		return nil, err
	}
	if len(token) != generatorrpc.RunSupervisionTokenSize {
		return nil, fmt.Errorf("unsealed run supervision token has %d bytes, want %d", len(token), generatorrpc.RunSupervisionTokenSize)
	}
	return token, nil
}

func decodeRunContinuityCheckpoint(raw string) (runContinuityCheckpoint, bool, error) {
	var checkpoint runContinuityCheckpoint
	if strings.TrimSpace(raw) == "" {
		return checkpoint, false, nil
	}
	if err := json.Unmarshal([]byte(raw), &checkpoint); err != nil {
		return checkpoint, false, fmt.Errorf("decode run continuity checkpoint: %w", err)
	}
	if checkpoint.Version != runContinuityCheckpointVersion {
		return checkpoint, false, fmt.Errorf("unsupported run continuity checkpoint version %d", checkpoint.Version)
	}
	return checkpoint, true, nil
}

// runContinuityUnavailableReasons lists why a continue-policy run would still
// fail on controller failover. Empty means the run can be armed.
func (tr *TestRunner) runContinuityUnavailableReasons(distributed bool) []string {
	var reasons []string
	if tr == nil || tr.leadership == nil || !tr.durableExecutionFence {
		reasons = append(reasons, "the controller is not running in HA mode (APEXLOAD_CONTROLLER_HA with a shared PostgreSQL store)")
	}
	if !distributed {
		reasons = append(reasons, "the run executes on the controller's local backend, whose virtual users stop with the controller")
	}
	if tr == nil || tr.continuityKeyWrapper == nil {
		reasons = append(reasons, "no key wrapper is configured to protect the adoption credential (set APEXLOAD_WRAPPING_MASTER_KEY or a Key Vault/KMS key)")
	}
	if !runAdoptionImplemented {
		reasons = append(reasons, "run adoption by a successor controller is not implemented in this release")
	}
	return reasons
}

// runContinuityLaunchWarning surfaces, at preflight, that a run asking to
// continue across failover will not.
func (tr *TestRunner) runContinuityLaunchWarning(test *ManagedTest) *LaunchConflict {
	if test == nil || effectiveFailoverPolicy(test.Assignment) != failoverPolicyContinue {
		return nil
	}
	_, distributed := tr.resolveBackend(test).(*distributedExecutionBackend)
	reasons := tr.runContinuityUnavailableReasons(distributed)
	if len(reasons) == 0 {
		return nil
	}
	return &LaunchConflict{
		Code:     "failover_continuity_unavailable",
		Severity: "warning",
		Source:   "controller",
		Message:  "failover_policy=continue will not keep this run alive if the controller fails over: " + strings.Join(reasons, "; ") + ". The run will be marked FAILED instead.",
	}
}

// armRunContinuity records the adoption checkpoint for a continue-policy
// distributed run once its generators hold the run's supervision lease. It
// never fails the run: an unarmable run simply keeps today's fail-on-failover
// behaviour, which is logged.
func (tr *TestRunner) armRunContinuity(ctx context.Context, test *ManagedTest, generation uint64, leaseOwnerID string, token []byte, shards map[string][]GeneratorShardSummary) {
	if err := tr.writeRunContinuity(ctx, test, generation, leaseOwnerID, token, shards, ""); err != nil {
		slog.Warn("run continuity not armed", "test_id", test.ID, "error", err)
	}
}

func (tr *TestRunner) writeRunContinuity(ctx context.Context, test *ManagedTest, generation uint64, leaseOwnerID string, token []byte, shards map[string][]GeneratorShardSummary, evacuationGuard string) error {
	if tr == nil || test == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// A remote key wrapper (Key Vault/KMS) must not be able to stall run start
	// or acquisition reconcile indefinitely.
	ctx, cancel := context.WithTimeout(ctx, runContinuityArmTimeout)
	defer cancel()
	tr.mu.RLock()
	policy := effectiveFailoverPolicy(test.Assignment)
	testID := test.ID
	expected := test.continuityCheckpoint
	failovers := append([]RunFailover(nil), test.continuityFailovers...)
	var completedTargets, archiveTargets []string
	for target, state := range test.remoteTargetStates {
		if strings.EqualFold(state, "complete") || strings.EqualFold(state, "completed") {
			completedTargets = append(completedTargets, target)
		}
	}
	for target := range test.remoteArchiveClientsByTarget {
		archiveTargets = append(archiveTargets, target)
	}
	pinnedSecrets := test.pinnedSecrets.snapshot()
	tr.mu.RUnlock()
	if policy != failoverPolicyContinue {
		if evacuationGuard != "" {
			return errors.New("continuity policy changed during evacuation")
		}
		return nil
	}
	if evacuationGuard != "" {
		if expected != evacuationGuard {
			return errors.New("evacuation checkpoint changed before completion")
		}
		if err := validateRunEvacuationCompletion(evacuationGuard, testID, generation, leaseOwnerID, shards); err != nil {
			return err
		}
	} else if _, _, err := decodeRunContinuityCheckpoint(expected); err != nil {
		return fmt.Errorf("cannot overwrite unresolved continuity checkpoint: %w", err)
	}
	if !tr.durableExecutionFence || tr.leadership == nil || tr.continuityKeyWrapper == nil {
		return fmt.Errorf("run continuity unavailable: %s", strings.Join(tr.runContinuityUnavailableReasons(true), "; "))
	}
	if len(token) == 0 {
		return errors.New("generator run supervision is disabled")
	}
	store, ok := tr.testStore.(interface {
		CompareAndSwapTestContinuityCheckpointIfLeaseHeld(string, string, string, string, string) (storage.TestStatusTransitionResult, error)
	})
	if !ok {
		return errors.New("test store cannot persist checkpoints")
	}
	sealed, err := sealRunSupervisionToken(ctx, tr.continuityKeyWrapper, testID, generation, token)
	if err != nil {
		return fmt.Errorf("seal run supervision token: %w", err)
	}
	var pinnedCiphertext string
	var pinnedKey *keywrap.WrappedKey
	if pinnedSecrets != nil {
		pinnedCiphertext, pinnedKey, err = sealRunPinnedSecrets(ctx, tr.continuityKeyWrapper, testID, pinnedSecrets)
		if err != nil {
			return fmt.Errorf("seal run secrets: %w", err)
		}
	} else if previous, present, decodeErr := decodeRunContinuityCheckpoint(expected); decodeErr == nil && present {
		pinnedCiphertext, pinnedKey = previous.PinnedSecretCiphertext, previous.PinnedSecretKey
	}
	checkpoint := runContinuityCheckpoint{
		Version:                runContinuityCheckpointVersion,
		ExecutionGeneration:    generation,
		ExecutionLeaseOwnerID:  leaseOwnerID,
		RunSupervisionToken:    sealed,
		PinnedSecretCiphertext: pinnedCiphertext,
		PinnedSecretKey:        pinnedKey,
		Shards:                 make(map[string][]GeneratorShardSummary, len(shards)),
		ArmedAt:                time.Now().UTC(),
		Failovers:              failovers,
		CompletedTargets:       completedTargets,
		ArchiveTargets:         archiveTargets,
	}
	for group, groupShards := range shards {
		checkpoint.Shards[group] = cloneGeneratorShardSummaries(groupShards)
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var result storage.TestStatusTransitionResult
	if err := tr.trackDurableWrite(func() error {
		var writeErr error
		result, writeErr = store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(testID, expected, string(encoded), executionLeaseName, leaseOwnerID)
		return writeErr
	}); err != nil {
		return fmt.Errorf("persist checkpoint: %w", err)
	}
	if result != storage.TestStatusTransitionUpdated {
		return fmt.Errorf("checkpoint write was not applied: %s", result)
	}
	tr.mu.Lock()
	if test.continuityCheckpoint != expected {
		tr.mu.Unlock()
		return errors.New("checkpoint changed during persistence")
	}
	setContinuityCheckpointLocked(test, string(encoded))
	tr.mu.Unlock()
	slog.Info("run continuity armed", "test_id", testID, "execution_generation", generation, "adoption_implemented", runAdoptionImplemented)
	return nil
}

// archivedRunContinuity is the continuity record written into a finished
// run's archive: only runs that actually failed over carry one. The caller
// holds tr.mu.
func archivedRunContinuity(test *ManagedTest) *RunContinuityStatus {
	status := runContinuityStatusLocked(test)
	if status == nil || len(status.Failovers) == 0 {
		return nil
	}
	status.Armed = false
	return status
}

// failoverGapTotalSeconds sums the unobserved windows of a continuity record.
func failoverGapTotalSeconds(status *RunContinuityStatus) float64 {
	if status == nil {
		return 0
	}
	total := 0.0
	for _, failover := range status.Failovers {
		total += failover.GapSeconds
	}
	return total
}

// controllerFailoverComparisonWarning is the evidence caveat a baseline
// comparison attaches to a run that survived controller failover: its
// archive completeness must be checked separately; controller-driven shapes held
// during each gap.
func controllerFailoverComparisonWarning(status *RunContinuityStatus) string {
	if status == nil || len(status.Failovers) == 0 {
		return ""
	}
	return fmt.Sprintf("Survived %d controller failover(s) with %.0f s unobserved by the controller; archive completeness requires separate verification, and stage, traffic-profile and goal control held their last value during each gap",
		len(status.Failovers), failoverGapTotalSeconds(status))
}
