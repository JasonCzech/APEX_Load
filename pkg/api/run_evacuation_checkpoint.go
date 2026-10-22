package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Version 3 commits only after the replacement's duration adjustment is
// acknowledged. A v2 replacement_committed record may have been written before
// that RPC, so successors must keep all v2 guards fail-closed instead of
// adopting work that could exceed the run's remaining duration.
const runEvacuationCheckpointVersion = 3

// Version 4 adds the replacement capacity claims and the standby pre-images a
// successor needs to roll a prepared or staged evacuation back to its prior
// topology. It is written only when that recovery is enabled: an older peer
// cannot decode it and keeps the run STOPPING instead of guessing.
const runEvacuationRollbackCheckpointVersion = 4

const (
	runEvacuationPhasePrepared             = "prepared"
	runEvacuationPhaseStaged               = "staged"
	runEvacuationPhaseSourceFenced         = "source_fenced"
	runEvacuationPhaseReplacementCommitted = "replacement_committed"
	// runEvacuationPhaseRollingBack records a successor's adoption intent for
	// the prior topology before any generator sees it. Until the replacement is
	// fenced and its capacity restored, every later reader keeps the journal.
	runEvacuationPhaseRollingBack = "rolling_back"
)

func supportedRunEvacuationCheckpointVersion(version int) bool {
	return version == runEvacuationCheckpointVersion || version == runEvacuationRollbackCheckpointVersion
}

type runEvacuationCheckpoint struct {
	Version                int                                `json:"version"`
	TestID                 string                             `json:"test_id"`
	Source                 string                             `json:"source"`
	Generation             uint64                             `json:"generation"`
	OwnerID                string                             `json:"owner_id"`
	PreparedAt             time.Time                          `json:"prepared_at"`
	Phase                  string                             `json:"phase"`
	StagedAt               time.Time                          `json:"staged_at,omitempty"`
	StagedTargets          []string                           `json:"staged_targets,omitempty"`
	SourceFencedAt         time.Time                          `json:"source_fenced_at,omitempty"`
	ReplacementCommittedAt time.Time                          `json:"replacement_committed_at,omitempty"`
	BeforeShards           map[string][]GeneratorShardSummary `json:"before_shards"`
	Prior                  runContinuityCheckpoint            `json:"prior"`
	NextShards             map[string][]GeneratorShardSummary `json:"next_shards"`
	// CapacityClaims are the replacement reservations written before staging,
	// and StandbyPreImages the idle standby rows they activated (version 4).
	CapacityClaims   []storage.PodCapacityReservation `json:"capacity_claims,omitempty"`
	StandbyPreImages []storage.PodCapacityReservation `json:"standby_pre_images,omitempty"`
	// RollbackGeneration is the newest successor term that may have adopted
	// the prior topology under this journal (phase rolling_back).
	RollbackGeneration uint64    `json:"rollback_generation,omitempty"`
	RollbackOwnerID    string    `json:"rollback_owner_id,omitempty"`
	RollbackStartedAt  time.Time `json:"rollback_started_at,omitempty"`
}

// runEvacuationCapacity is the capacity journal of one evacuation attempt.
type runEvacuationCapacity struct {
	claims    []storage.PodCapacityReservation
	preImages []storage.PodCapacityReservation
}

// beginRunEvacuationCheckpoint runs before AssignWork can stage a replacement.
// The opaque returned guard binds completion to this exact durable intent. An
// unsuccessful evacuation leaves it in place, conservatively disabling adoption.
func (tr *TestRunner) beginRunEvacuationCheckpoint(ctx context.Context, test *ManagedTest, generation uint64, owner, source string, token []byte, previous, next map[string][]GeneratorShardSummary, capacity runEvacuationCapacity) (string, error) {
	tr.mu.RLock()
	policy := effectiveFailoverPolicy(test.Assignment)
	expected := test.continuityCheckpoint
	failovers := append([]RunFailover(nil), test.continuityFailovers...)
	tr.mu.RUnlock()
	if policy != failoverPolicyContinue || (!tr.runAdoptionEnabled() && expected == "") {
		return "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, runContinuityArmTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prior, present, err := decodeRunContinuityCheckpoint(expected)
	if err != nil {
		return "", fmt.Errorf("unresolved continuity checkpoint: %w", err)
	}
	if generation == 0 || owner == "" || source == "" || len(previous) == 0 || len(next) == 0 {
		return "", errors.New("invalid evacuation checkpoint identity or topology")
	}
	if present && (prior.ExecutionGeneration != generation || prior.ExecutionLeaseOwnerID != owner) {
		return "", errors.New("evacuation checkpoint belongs to a different execution term")
	}
	if !present {
		sealed, err := sealRunSupervisionToken(ctx, tr.continuityKeyWrapper, test.ID, generation, token)
		if err != nil {
			return "", err
		}
		prior = runContinuityCheckpoint{
			Version:               runContinuityCheckpointVersion,
			ExecutionGeneration:   generation,
			ExecutionLeaseOwnerID: owner,
			RunSupervisionToken:   sealed,
			Shards:                cloneGroupShardMap(previous),
			Failovers:             failovers,
		}
	}
	if len(failovers) >= len(prior.Failovers) {
		prior.Failovers = failovers
	}
	guard := runEvacuationCheckpoint{
		Version:      runEvacuationCheckpointVersion,
		TestID:       test.ID,
		Source:       source,
		Generation:   generation,
		OwnerID:      owner,
		PreparedAt:   time.Now().UTC(),
		Phase:        runEvacuationPhasePrepared,
		Prior:        prior,
		BeforeShards: cloneGroupShardMap(previous),
		NextShards:   cloneGroupShardMap(next),
	}
	if tr.evacuationRollbackRecovery {
		guard.Version = runEvacuationRollbackCheckpointVersion
		guard.CapacityClaims = clonePodCapacityReservations(capacity.claims)
		guard.StandbyPreImages = clonePodCapacityReservations(capacity.preImages)
	}
	encoded, err := json.Marshal(guard)
	if err != nil {
		return "", err
	}
	store, ok := tr.testStore.(interface {
		CompareAndSwapTestContinuityCheckpointIfLeaseHeld(string, string, string, string, string) (storage.TestStatusTransitionResult, error)
	})
	if !ok {
		return "", errors.New("test store cannot persist evacuation checkpoint")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var result storage.TestStatusTransitionResult
	err = tr.trackDurableWrite(func() error {
		var e error
		result, e = store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(test.ID, expected, string(encoded), executionLeaseName, owner)
		return e
	})
	if err != nil {
		return "", err
	}
	if result != storage.TestStatusTransitionUpdated {
		return "", fmt.Errorf("evacuation checkpoint write not applied: %s", result)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.continuityCheckpoint != expected {
		return "", errors.New("continuity checkpoint changed while preparing evacuation")
	}
	setContinuityCheckpointLocked(test, string(encoded))
	return string(encoded), nil
}

// markRunEvacuationStaged records the exact replacement targets after staging
// returns successfully, before the source is stopped. An incomplete or
// ambiguous stage cannot be promoted into a source-fence checkpoint.
func (tr *TestRunner) markRunEvacuationStaged(ctx context.Context, test *ManagedTest, raw string, generation uint64, owner string, targets []string) (string, error) {
	return tr.advanceRunEvacuationCheckpoint(ctx, test, raw, generation, owner, runEvacuationPhasePrepared, func(guard *runEvacuationCheckpoint) error {
		if !guard.StagedAt.IsZero() || len(guard.StagedTargets) != 0 || !guard.SourceFencedAt.IsZero() || !guard.ReplacementCommittedAt.IsZero() {
			return errors.New("evacuation staging progress already exists")
		}
		actual := append([]string(nil), targets...)
		slices.Sort(actual)
		if len(actual) == 0 {
			return errors.New("evacuation staged no replacement targets")
		}
		for _, target := range actual {
			if target == "" || strings.TrimSpace(target) != target {
				return errors.New("evacuation staged an invalid replacement target")
			}
		}
		for i := 1; i < len(actual); i++ {
			if actual[i] == actual[i-1] {
				return errors.New("evacuation staged duplicate replacement targets")
			}
		}
		expected, err := runEvacuationReplacementTargets(*guard)
		if err != nil {
			return err
		}
		if !slices.Equal(actual, expected) {
			return errors.New("staged replacement targets do not match the durable topology")
		}
		guard.Phase = runEvacuationPhaseStaged
		guard.StagedAt = time.Now().UTC()
		guard.StagedTargets = actual
		return nil
	})
}

func runEvacuationReplacementTargets(guard runEvacuationCheckpoint) ([]string, error) {
	before := make(map[string]struct{})
	for _, shards := range guard.BeforeShards {
		for _, shard := range shards {
			if shard.Target == "" || strings.TrimSpace(shard.Target) != shard.Target {
				return nil, errors.New("evacuation prior topology has an invalid target")
			}
			before[shard.Target] = struct{}{}
		}
	}
	if _, present := before[guard.Source]; !present {
		return nil, errors.New("evacuation source is absent from the prior topology")
	}
	expected := make([]string, 0)
	seen := make(map[string]struct{})
	for _, shards := range guard.NextShards {
		for _, shard := range shards {
			if shard.Target == "" || strings.TrimSpace(shard.Target) != shard.Target {
				return nil, errors.New("evacuation next topology has an invalid target")
			}
			if shard.Target == guard.Source {
				return nil, errors.New("evacuation source remains in the next topology")
			}
			if _, previous := before[shard.Target]; !previous {
				if _, duplicate := seen[shard.Target]; !duplicate {
					seen[shard.Target] = struct{}{}
					expected = append(expected, shard.Target)
				}
			}
		}
	}
	slices.Sort(expected)
	if len(expected) == 0 {
		return nil, errors.New("evacuation topology has no new replacement target")
	}
	return expected, nil
}

// markRunEvacuationSourceFenced records the positive source fence before any
// replacement commit. A failed write leaves the guard unresolved and the
// caller must stop the staged replacement through the uncertain path.
func (tr *TestRunner) markRunEvacuationSourceFenced(ctx context.Context, test *ManagedTest, raw string, generation uint64, owner string) (string, error) {
	return tr.advanceRunEvacuationCheckpoint(ctx, test, raw, generation, owner, runEvacuationPhaseStaged, func(guard *runEvacuationCheckpoint) error {
		if guard.StagedAt.IsZero() || len(guard.StagedTargets) == 0 || !guard.SourceFencedAt.IsZero() || !guard.ReplacementCommittedAt.IsZero() {
			return errors.New("evacuation source fence lacks durable staging proof")
		}
		expected, err := runEvacuationReplacementTargets(*guard)
		if err != nil || !slices.Equal(guard.StagedTargets, expected) {
			return errors.New("evacuation source fence has mismatched staged targets")
		}
		guard.Phase = runEvacuationPhaseSourceFenced
		guard.SourceFencedAt = time.Now().UTC()
		return nil
	})
}

// markRunEvacuationReplacementCommitted records acknowledged CommitWork only
// after the positive source fence and durable replacement capacity commit.
// A successor can then adopt the existing replacement without replaying work.
func (tr *TestRunner) markRunEvacuationReplacementCommitted(ctx context.Context, test *ManagedTest, raw string, generation uint64, owner string) (string, error) {
	return tr.advanceRunEvacuationCheckpoint(ctx, test, raw, generation, owner, runEvacuationPhaseSourceFenced, func(guard *runEvacuationCheckpoint) error {
		if guard.StagedAt.IsZero() || len(guard.StagedTargets) == 0 || guard.SourceFencedAt.IsZero() || !guard.ReplacementCommittedAt.IsZero() {
			return errors.New("evacuation replacement commit lacks durable source fence")
		}
		expected, err := runEvacuationReplacementTargets(*guard)
		if err != nil || !slices.Equal(guard.StagedTargets, expected) {
			return errors.New("evacuation replacement commit has mismatched staged targets")
		}
		if store, ok := tr.testStore.(podCapacityStore); ok {
			rows, err := store.ListPodCapacityReservations()
			if err != nil {
				return fmt.Errorf("verify committed replacement capacity: %w", err)
			}
			byTarget := make(map[string]storage.PodCapacityReservation)
			capacityPresent := false
			for _, row := range rows {
				if row.TestID != test.ID {
					continue
				}
				if row.Target == guard.Source || slices.Contains(expected, row.Target) {
					capacityPresent = true
					byTarget[row.Target] = row
				}
			}
			if capacityPresent {
				for _, target := range expected {
					claim, present := byTarget[target]
					if !present || claim.State != "committed" || claim.Generation != generation {
						return fmt.Errorf("replacement %s has no committed capacity reservation", target)
					}
				}
			}
		}
		guard.Phase = runEvacuationPhaseReplacementCommitted
		guard.ReplacementCommittedAt = time.Now().UTC()
		return nil
	})
}

func (tr *TestRunner) advanceRunEvacuationCheckpoint(ctx context.Context, test *ManagedTest, raw string, generation uint64, owner, phase string, update func(*runEvacuationCheckpoint) error) (string, error) {
	if tr == nil || test == nil {
		return "", errors.New("evacuation progress requires a managed run")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, runContinuityArmTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var guard runEvacuationCheckpoint
	if err := json.Unmarshal([]byte(raw), &guard); err != nil {
		return "", fmt.Errorf("decode evacuation checkpoint: %w", err)
	}
	if !supportedRunEvacuationCheckpointVersion(guard.Version) || guard.TestID != test.ID || guard.Generation != generation || guard.OwnerID != owner || guard.Source == "" || guard.PreparedAt.IsZero() || guard.Phase != phase {
		return "", errors.New("evacuation progress does not match durable intent")
	}
	tr.mu.RLock()
	current := test.continuityCheckpoint
	tr.mu.RUnlock()
	if current != raw {
		return "", errors.New("evacuation checkpoint changed before progress write")
	}
	if err := update(&guard); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(guard)
	if err != nil {
		return "", err
	}
	store, ok := tr.testStore.(interface {
		CompareAndSwapTestContinuityCheckpointIfLeaseHeld(string, string, string, string, string) (storage.TestStatusTransitionResult, error)
	})
	if !ok {
		return "", errors.New("test store cannot persist evacuation checkpoint")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var result storage.TestStatusTransitionResult
	err = tr.trackDurableWrite(func() error {
		var e error
		result, e = store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(test.ID, raw, string(encoded), executionLeaseName, owner)
		return e
	})
	if err != nil {
		return "", err
	}
	if result != storage.TestStatusTransitionUpdated {
		return "", fmt.Errorf("evacuation progress write not applied: %s", result)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.continuityCheckpoint != raw {
		return "", errors.New("evacuation checkpoint changed while recording progress")
	}
	setContinuityCheckpointLocked(test, string(encoded))
	return string(encoded), nil
}

func validateRunEvacuationCompletion(raw, testID string, generation uint64, owner string, shards map[string][]GeneratorShardSummary) error {
	var guard runEvacuationCheckpoint
	if err := json.Unmarshal([]byte(raw), &guard); err != nil {
		return fmt.Errorf("decode evacuation checkpoint: %w", err)
	}
	if !supportedRunEvacuationCheckpointVersion(guard.Version) || guard.TestID != testID || guard.Generation != generation || guard.OwnerID != owner || guard.Source == "" || guard.PreparedAt.IsZero() || guard.Phase != runEvacuationPhaseReplacementCommitted || guard.StagedAt.IsZero() || len(guard.StagedTargets) == 0 || guard.SourceFencedAt.IsZero() || guard.ReplacementCommittedAt.IsZero() || !reflect.DeepEqual(guard.NextShards, shards) {
		return errors.New("evacuation completion does not match durable intent")
	}
	expected, err := runEvacuationReplacementTargets(guard)
	if err != nil || !slices.Equal(guard.StagedTargets, expected) {
		return errors.New("evacuation completion lacks exact staged replacement targets")
	}
	return nil
}

// committedRunEvacuationCheckpoint accepts only a complete positive-fence and
// replacement-commit journal whose prior placement matches its sealed lineage.
// It is safe to adopt the existing replacement, but never to replay its work.
func committedRunEvacuationCheckpoint(raw, testID string) (runEvacuationCheckpoint, bool) {
	var guard runEvacuationCheckpoint
	if json.Unmarshal([]byte(raw), &guard) != nil || guard.TestID != testID || guard.Generation == 0 || guard.OwnerID == "" || guard.Prior.Version != runContinuityCheckpointVersion || guard.Prior.ExecutionGeneration != guard.Generation || guard.Prior.ExecutionLeaseOwnerID != guard.OwnerID || guard.Prior.ArmedAt.IsZero() || !reflect.DeepEqual(guard.Prior.Shards, guard.BeforeShards) || slices.Contains(guard.Prior.CompletedTargets, guard.Source) {
		return runEvacuationCheckpoint{}, false
	}
	if validateRunEvacuationCompletion(raw, testID, guard.Generation, guard.OwnerID, guard.NextShards) != nil {
		return runEvacuationCheckpoint{}, false
	}
	return guard, true
}

// promoteCommittedRunEvacuationCheckpoint converts an acknowledged replacement
// into the ordinary adoption checkpoint under the successor's execution lease.
// A second successor sees either the original guard or the completed topology.
func (tr *TestRunner) promoteCommittedRunEvacuationCheckpoint(ctx context.Context, test *ManagedTest, raw string, generation uint64, owner string) (string, error) {
	if tr == nil || test == nil || generation == 0 || owner == "" {
		return "", errors.New("evacuation promotion requires a successor lease")
	}
	guard, ok := committedRunEvacuationCheckpoint(raw, test.ID)
	if !ok || guard.Generation >= generation {
		return "", errors.New("evacuation promotion lacks committed predecessor proof")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	tr.mu.RLock()
	current := test.continuityCheckpoint
	tr.mu.RUnlock()
	if current != raw {
		return "", errors.New("evacuation checkpoint changed before promotion")
	}
	prior := guard.Prior
	prior.Shards = cloneGroupShardMap(guard.NextShards)
	prior.CompletedTargets = append([]string(nil), prior.CompletedTargets...)
	prior.ArchiveTargets = append([]string(nil), prior.ArchiveTargets...)
	if !slices.Contains(prior.ArchiveTargets, guard.Source) {
		prior.ArchiveTargets = append(prior.ArchiveTargets, guard.Source)
		slices.Sort(prior.ArchiveTargets)
	}
	prior.ArmedAt = time.Now().UTC()
	encoded, err := json.Marshal(prior)
	if err != nil {
		return "", err
	}
	store, ok := tr.testStore.(interface {
		CompareAndSwapTestContinuityCheckpointIfLeaseHeld(string, string, string, string, string) (storage.TestStatusTransitionResult, error)
	})
	if !ok {
		return "", errors.New("test store cannot promote evacuation checkpoint")
	}
	var result storage.TestStatusTransitionResult
	if err := tr.trackDurableWrite(func() error {
		var writeErr error
		result, writeErr = store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(test.ID, raw, string(encoded), executionLeaseName, owner)
		return writeErr
	}); err != nil {
		return "", err
	}
	if result != storage.TestStatusTransitionUpdated {
		return "", fmt.Errorf("evacuation promotion write not applied: %s", result)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.continuityCheckpoint != raw {
		return "", errors.New("evacuation checkpoint changed while promoting")
	}
	setContinuityCheckpointLocked(test, string(encoded))
	return string(encoded), nil
}

func interruptedRunEvacuation(raw string) bool {
	var guard runEvacuationCheckpoint
	return json.Unmarshal([]byte(raw), &guard) == nil && (guard.Version == 2 || supportedRunEvacuationCheckpointVersion(guard.Version)) && guard.Source != ""
}

func clonePodCapacityReservations(rows []storage.PodCapacityReservation) []storage.PodCapacityReservation {
	if len(rows) == 0 {
		return nil
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return nil
	}
	var cloned []storage.PodCapacityReservation
	if json.Unmarshal(encoded, &cloned) != nil {
		return nil
	}
	return cloned
}
