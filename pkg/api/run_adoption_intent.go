package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
)

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
	prior, err := openRunSupervisionToken(ctx, tr.continuityKeyWrapper, testID, checkpoint)
	if err != nil {
		return fail(err)
	}
	adoption.PriorToken = prior
	last := checkpoint.ExecutionGeneration
	for _, intent := range checkpoint.PendingAdoptions {
		if intent.Generation <= last || intent.Generation > generation || strings.TrimSpace(intent.OwnerID) == "" || intent.PreparedAt.IsZero() {
			return fail(fmt.Errorf("invalid adoption intent ordering or ownership"))
		}
		token, err := openRunSupervisionToken(ctx, tr.continuityKeyWrapper, testID, runContinuityCheckpoint{ExecutionGeneration: intent.Generation, RunSupervisionToken: intent.Token})
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
		return fail(err)
	}
	checkpoint.PendingAdoptions = append(append([]runAdoptionIntent(nil), checkpoint.PendingAdoptions...), runAdoptionIntent{Generation: generation, OwnerID: owner, Token: sealed, PreparedAt: time.Now().UTC()})
	return checkpoint, adoption, nil
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
