package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Key-service failures are retried by acquisition reconciliation. They do not
// prove a checkpoint is malformed or that a generator has stopped.
var errRunAdoptionKeyAccess = errors.New("run adoption key access failed")

// An intent preserves a proposed token before any generator can accept it. A
// successor can prove either the last fully checkpointed generation or one of
// these interrupted rotations, without restarting an existing assignment.
type runAdoptionIntent struct {
	Generation uint64             `json:"generation"`
	OwnerID    string             `json:"owner_id"`
	Token      keywrap.WrappedKey `json:"token"`
	PreparedAt time.Time          `json:"prepared_at"`
}

func (tr *TestRunner) planRunAdoptionIntent(ctx context.Context, testID string, checkpoint runContinuityCheckpoint, generation uint64, owner string) (runContinuityCheckpoint, generatorrpc.RunAdoption, error) {
	adoption := generatorrpc.RunAdoption{TestID: testID, PriorGeneration: checkpoint.ExecutionGeneration}
	fail := func(err error) (runContinuityCheckpoint, generatorrpc.RunAdoption, error) {
		return checkpoint, generatorrpc.RunAdoption{}, err
	}
	if generation == 0 || generation <= checkpoint.ExecutionGeneration || strings.TrimSpace(owner) == "" || len(checkpoint.PendingAdoptions) > generatorrpc.MaxRunAdoptionPriorProofs {
		return fail(fmt.Errorf("invalid or oversized adoption intent history"))
	}
	prior, err := tr.openRunAdoptionToken(ctx, testID, checkpoint.ExecutionGeneration, checkpoint.RunSupervisionToken)
	if err != nil {
		return fail(err)
	}
	adoption.PriorToken = prior
	last := checkpoint.ExecutionGeneration
	for _, intent := range checkpoint.PendingAdoptions {
		if intent.Generation <= last || intent.Generation > generation || strings.TrimSpace(intent.OwnerID) == "" || intent.PreparedAt.IsZero() {
			return fail(fmt.Errorf("invalid adoption intent ordering or ownership"))
		}
		token, err := tr.openRunAdoptionToken(ctx, testID, intent.Generation, intent.Token)
		if err != nil {
			return fail(err)
		}
		if intent.Generation == generation {
			if intent.OwnerID != owner {
				return fail(fmt.Errorf("adoption intent belongs to another lease owner"))
			}
			adoption.NewToken = token
		} else {
			adoption.PriorProofs = append(adoption.PriorProofs, generatorrpc.RunAdoptionProof{Generation: intent.Generation, Token: token})
		}
		last = intent.Generation
	}
	if len(adoption.NewToken) != 0 {
		return checkpoint, adoption, nil
	}
	if len(checkpoint.PendingAdoptions) >= generatorrpc.MaxRunAdoptionPriorProofs {
		return fail(fmt.Errorf("too many interrupted controller adoption attempts"))
	}
	adoption.NewToken = make([]byte, generatorrpc.RunSupervisionTokenSize)
	if _, err := rand.Read(adoption.NewToken); err != nil {
		return fail(err)
	}
	sealed, err := sealRunSupervisionToken(ctx, tr.continuityKeyWrapper, testID, generation, adoption.NewToken)
	if err != nil {
		return fail(fmt.Errorf("%w: seal successor token: %w", errRunAdoptionKeyAccess, err))
	}
	checkpoint.PendingAdoptions = append(append([]runAdoptionIntent(nil), checkpoint.PendingAdoptions...), runAdoptionIntent{Generation: generation, OwnerID: owner, Token: sealed, PreparedAt: time.Now().UTC()})
	return checkpoint, adoption, nil
}

func (tr *TestRunner) openRunAdoptionToken(ctx context.Context, testID string, generation uint64, wrapped keywrap.WrappedKey) ([]byte, error) {
	if tr == nil || tr.continuityKeyWrapper == nil {
		return nil, errors.New("run continuity key wrapper is not configured")
	}
	if strings.TrimSpace(wrapped.Ciphertext) == "" {
		return nil, errors.New("run adoption token ciphertext is missing")
	}
	token, err := tr.continuityKeyWrapper.UnwrapKey(ctx, wrapped, runSupervisionTokenAAD(testID, generation))
	if err != nil {
		return nil, fmt.Errorf("%w: unwrap prior token: %w", errRunAdoptionKeyAccess, err)
	}
	if len(token) != generatorrpc.RunSupervisionTokenSize {
		return nil, fmt.Errorf("unsealed run supervision token has %d bytes, want %d", len(token), generatorrpc.RunSupervisionTokenSize)
	}
	return token, nil
}

// advanceAdoptedRunExecutionGeneration records the adopting term on the durable
// run before any generator sees it. An archive the previous term finalized for
// this run then no longer identifies as the run's outcome in orphan recovery.
func (tr *TestRunner) advanceAdoptedRunExecutionGeneration(test *ManagedTest, generation uint64, owner string) error {
	store, ok := tr.testStore.(interface {
		AdvanceTestExecutionGenerationIfLeaseTermHeld(string, string, string, uint64) (bool, error)
	})
	if !ok {
		return fmt.Errorf("test store cannot record the adopting execution term")
	}
	var advanced bool
	err := tr.trackDurableWrite(func() error {
		var writeErr error
		advanced, writeErr = store.AdvanceTestExecutionGenerationIfLeaseTermHeld(test.ID, executionLeaseName, owner, generation)
		return writeErr
	})
	if err != nil {
		return err
	}
	if !advanced {
		return fmt.Errorf("adoption execution term write not applied")
	}
	return nil
}

func (tr *TestRunner) persistRunAdoptionIntent(test *ManagedTest, expected string, checkpoint runContinuityCheckpoint, owner string) error {
	store, ok := tr.testStore.(interface {
		CompareAndSwapTestContinuityCheckpointIfLeaseHeld(string, string, string, string, string) (storage.TestStatusTransitionResult, error)
	})
	if !ok {
		return fmt.Errorf("test store cannot persist atomic adoption intent")
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	var result storage.TestStatusTransitionResult
	err = tr.trackDurableWrite(func() error {
		var writeErr error
		result, writeErr = store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(test.ID, expected, string(encoded), executionLeaseName, owner)
		return writeErr
	})
	if err != nil {
		return err
	}
	if result != storage.TestStatusTransitionUpdated {
		return fmt.Errorf("adoption intent write not applied: %s", result)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.continuityCheckpoint != expected {
		return fmt.Errorf("adoption checkpoint changed during intent persistence")
	}
	setContinuityCheckpointLocked(test, string(encoded))
	return nil
}

// persistRunAdoptionStoppedTarget records one force-stop ACK before the next
// target is attempted. A later retry or controller can then rely on this
// terminal proof even if the stopped generator pod has already disappeared.
func (tr *TestRunner) persistRunAdoptionStoppedTarget(test *ManagedTest, target string, generation uint64, owner string) error {
	currentGeneration, currentOwner, _, err := tr.generatorExecutionClaim()
	if err != nil || currentGeneration != generation || currentOwner != owner {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	current := tr.tests[test.ID] == test && test.Status == "STOPPING"
	raw := test.continuityCheckpoint
	tr.mu.RUnlock()
	if !current {
		return errNotExecutionLeader
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(raw)
	if err != nil || !present || !slices.Contains(checkpointTargets(checkpoint), target) || checkpoint.ExecutionGeneration > generation {
		return fmt.Errorf("cannot record force-stop proof against the current adoption topology: %v", err)
	}
	if checkpoint.ExecutionGeneration == generation {
		if checkpoint.ExecutionLeaseOwnerID != owner {
			return errNotExecutionLeader
		}
	} else {
		found := false
		for _, intent := range checkpoint.PendingAdoptions {
			if intent.Generation == generation && intent.OwnerID == owner {
				found = true
				break
			}
		}
		if !found {
			return errors.New("force-stop proof has no durable successor adoption intent")
		}
	}
	if slices.Contains(checkpoint.StoppedTargets, target) {
		return nil
	}
	checkpoint.StoppedTargets = append(checkpoint.StoppedTargets, target)
	slices.Sort(checkpoint.StoppedTargets)
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	store, ok := tr.testStore.(interface {
		CompareAndSwapTestContinuityCheckpointIfLeaseHeld(string, string, string, string, string) (storage.TestStatusTransitionResult, error)
	})
	if !ok {
		return errors.New("test store cannot persist force-stop proof")
	}
	var result storage.TestStatusTransitionResult
	if err := tr.trackDurableWrite(func() error {
		var writeErr error
		result, writeErr = store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(test.ID, raw, string(encoded), executionLeaseName, owner)
		return writeErr
	}); err != nil {
		return err
	}
	if result != storage.TestStatusTransitionUpdated {
		return fmt.Errorf("force-stop proof write not applied: %s", result)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[test.ID] != test || test.continuityCheckpoint != raw || test.Status != "STOPPING" {
		return errors.New("adoption checkpoint changed while recording force-stop proof")
	}
	setContinuityCheckpointLocked(test, string(encoded))
	return nil
}
