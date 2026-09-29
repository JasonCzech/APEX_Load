package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Version 2 is deliberately not adoptable by the version-1 reader, including
// older controllers. It is a durable interruption guard, not a replay protocol:
// until replacement progress can be reconciled, a successor must fail closed.
const runEvacuationCheckpointVersion = 2

type runEvacuationCheckpoint struct {
	Version      int                                `json:"version"`
	TestID       string                             `json:"test_id"`
	Source       string                             `json:"source"`
	Generation   uint64                             `json:"generation"`
	OwnerID      string                             `json:"owner_id"`
	PreparedAt   time.Time                          `json:"prepared_at"`
	BeforeShards map[string][]GeneratorShardSummary `json:"before_shards"`
	Prior        runContinuityCheckpoint            `json:"prior"`
	NextShards   map[string][]GeneratorShardSummary `json:"next_shards"`
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

func validateRunEvacuationCompletion(raw, testID string, generation uint64, owner string, shards map[string][]GeneratorShardSummary) error {
	var guard runEvacuationCheckpoint
	if err := json.Unmarshal([]byte(raw), &guard); err != nil {
		return fmt.Errorf("decode evacuation checkpoint: %w", err)
	}
	if guard.Version != runEvacuationCheckpointVersion || guard.TestID != testID || guard.Generation != generation || guard.OwnerID != owner || guard.Source == "" || guard.PreparedAt.IsZero() || !reflect.DeepEqual(guard.NextShards, shards) {
		return errors.New("evacuation completion does not match durable intent")
	}
	return nil
}

func interruptedRunEvacuation(raw string) bool {
	var guard runEvacuationCheckpoint
	return json.Unmarshal([]byte(raw), &guard) == nil && guard.Version == runEvacuationCheckpointVersion && guard.Source != ""
}
