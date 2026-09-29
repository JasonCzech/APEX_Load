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

// Version 2 is deliberately not adoptable by the version-1 reader, including
// older controllers. It is a durable interruption guard, not a replay protocol:
// until replacement progress can be reconciled, a successor must fail closed.
const runEvacuationCheckpointVersion = 2

const (
	runEvacuationPhasePrepared     = "prepared"
	runEvacuationPhaseStaged       = "staged"
	runEvacuationPhaseSourceFenced = "source_fenced"
)

type runEvacuationCheckpoint struct {
	Version        int                                `json:"version"`
	TestID         string                             `json:"test_id"`
	Source         string                             `json:"source"`
	Generation     uint64                             `json:"generation"`
	OwnerID        string                             `json:"owner_id"`
	PreparedAt     time.Time                          `json:"prepared_at"`
	Phase          string                             `json:"phase"`
	StagedAt       time.Time                          `json:"staged_at,omitempty"`
	StagedTargets  []string                           `json:"staged_targets,omitempty"`
	SourceFencedAt time.Time                          `json:"source_fenced_at,omitempty"`
	BeforeShards   map[string][]GeneratorShardSummary `json:"before_shards"`
	Prior          runContinuityCheckpoint            `json:"prior"`
	NextShards     map[string][]GeneratorShardSummary `json:"next_shards"`
}

// beginRunEvacuationCheckpoint runs before AssignWork can stage a replacement.
// The opaque returned guard binds completion to this exact durable intent. An
// unsuccessful evacuation leaves it in place, conservatively disabling adoption.
func (tr *TestRunner) beginRunEvacuationCheckpoint(ctx context.Context, test *ManagedTest, generation uint64, owner, source string, token []byte, previous, next map[string][]GeneratorShardSummary) (string, error) {
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
		if !guard.StagedAt.IsZero() || len(guard.StagedTargets) != 0 || !guard.SourceFencedAt.IsZero() {
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
		if guard.StagedAt.IsZero() || len(guard.StagedTargets) == 0 || !guard.SourceFencedAt.IsZero() {
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
	if guard.Version != runEvacuationCheckpointVersion || guard.TestID != test.ID || guard.Generation != generation || guard.OwnerID != owner || guard.Source == "" || guard.PreparedAt.IsZero() || guard.Phase != phase {
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
	if guard.Version != runEvacuationCheckpointVersion || guard.TestID != testID || guard.Generation != generation || guard.OwnerID != owner || guard.Source == "" || guard.PreparedAt.IsZero() || guard.Phase != runEvacuationPhaseSourceFenced || guard.StagedAt.IsZero() || len(guard.StagedTargets) == 0 || guard.SourceFencedAt.IsZero() || !reflect.DeepEqual(guard.NextShards, shards) {
		return errors.New("evacuation completion does not match durable intent")
	}
	expected, err := runEvacuationReplacementTargets(guard)
	if err != nil || !slices.Equal(guard.StagedTargets, expected) {
		return errors.New("evacuation completion lacks exact staged replacement targets")
	}
	return nil
}

func interruptedRunEvacuation(raw string) bool {
	var guard runEvacuationCheckpoint
	return json.Unmarshal([]byte(raw), &guard) == nil && guard.Version == runEvacuationCheckpointVersion && guard.Source != ""
}
