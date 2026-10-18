package api

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Implementations adopt the owned record's authoritative InitialProvenance only
// after a successful INITIALIZING commit; a failed write leaves it unchanged.
type deploymentLeaseTestStartPersistence interface {
	SaveTestStartIfNoDeploymentLease(test *storage.TestRecord, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) error
	SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *storage.TestRecord, executionLeaseName, executionLeaseOwnerID, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) (bool, error)
}

type managedTestStartPersistenceSnapshot struct {
	record                        *storage.TestRecord
	testID                        string
	executionFence                string
	executionGeneration           uint64
	authorizedDeploymentOwnerID   string
	authorizedDeploymentTokenHash string
}

func newManagedTestStartPersistenceSnapshot(test *ManagedTest) managedTestStartPersistenceSnapshot {
	ownerID, tokenHash := deploymentLeaseAuthorizationForCITest(test)
	snapshot := managedTestStartPersistenceSnapshot{
		record:                        buildManagedTestRecord(test),
		authorizedDeploymentOwnerID:   ownerID,
		authorizedDeploymentTokenHash: tokenHash,
	}
	if test != nil {
		snapshot.testID = test.ID
		snapshot.executionFence = test.executionFence
		snapshot.executionGeneration = test.executionGeneration
		if snapshot.record != nil {
			// The fenced Start commit stamps this term on the durable row.
			snapshot.record.ExecutionGeneration = test.executionGeneration
		}
	}
	return snapshot
}

// classifyStartTransitionPersistenceError marks an unexpected durable write
// failure as retryable after the caller has rolled its in-memory transition
// back. Semantic fences remain distinct so maintenance conflicts, missing rows,
// and lost leadership are not hidden behind an outage retry.
func classifyStartTransitionPersistenceError(err error) error {
	if err == nil || errors.Is(err, errStartAdmissionEvidenceStale) ||
		errors.Is(err, errNotExecutionLeader) || errors.Is(err, errRunnerShuttingDown) ||
		errors.Is(err, storage.ErrDeploymentLeaseActive) ||
		errors.Is(err, storage.ErrTestStartIdentityChanged) ||
		errors.Is(err, storage.ErrCapacityReservationStartInvalid) || errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return errors.Join(errStartAdmissionEvidenceStale, err)
}

// saveManagedTestStartRecord is the only persistence path for entering a state
// that may begin execution. It combines the durable test write with the shared
// deployment lease guard, and (under HA) with the execution-leader fence.
func (tr *TestRunner) saveManagedTestStartRecord(test *ManagedTest) error {
	snapshot := newManagedTestStartPersistenceSnapshot(test)
	if err := tr.saveManagedTestStartSnapshot(snapshot); err != nil {
		return err
	}
	if test != nil && tr.durableExecutionFence {
		test.durableExecutionGeneration = snapshot.record.ExecutionGeneration
	}
	return nil
}

// saveManagedTestStartSnapshot persists an immutable start transition without
// reading the live ManagedTest pointer. Callers can therefore release tr.mu
// while the database waits on lease, reservation, or admission-revision locks.
func (tr *TestRunner) saveManagedTestStartSnapshot(snapshot managedTestStartPersistenceSnapshot) error {
	if tr.testStore == nil {
		// In-memory runners are used by local/unit execution and have no durable
		// state to race with another controller replica.
		return nil
	}
	if snapshot.record == nil {
		return fmt.Errorf("test record is required")
	}
	if tr.testOnlyAllowUnfencedDeploymentLeaseStart {
		// This branch is reachable only through a test-only option compiled from
		// a _test.go file. It preserves deterministic SaveTest fault injection
		// without making capability detection a production bypass.
		return classifyStartTransitionPersistenceError(tr.testStore.SaveTest(snapshot.record))
	}
	store, ok := tr.testStore.(deploymentLeaseTestStartPersistence)
	if !ok {
		return fmt.Errorf("deployment lease start fence is unavailable for the configured test store")
	}
	if tr.durableExecutionFence {
		if snapshot.executionFence == "" {
			return fmt.Errorf("%w: test has no claimed execution epoch", errNotExecutionLeader)
		}
		saved, err := store.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(
			snapshot.record, executionLeaseName, snapshot.executionFence, storage.DefaultDeploymentLeaseName,
			snapshot.authorizedDeploymentOwnerID, snapshot.authorizedDeploymentTokenHash,
		)
		if err != nil {
			if errors.Is(err, storage.ErrTestProjectRevisionChanged) || errors.Is(err, storage.ErrTestStartAdmissionRevisionChanged) {
				return errStartAdmissionEvidenceStale
			}
			return classifyStartTransitionPersistenceError(err)
		}
		if !saved {
			return fmt.Errorf("%w: test %s belongs to an expired execution epoch", errNotExecutionLeader, snapshot.testID)
		}
		return nil
	}
	err := store.SaveTestStartIfNoDeploymentLease(
		snapshot.record, storage.DefaultDeploymentLeaseName,
		snapshot.authorizedDeploymentOwnerID, snapshot.authorizedDeploymentTokenHash,
	)
	if errors.Is(err, storage.ErrTestProjectRevisionChanged) || errors.Is(err, storage.ErrTestStartAdmissionRevisionChanged) {
		return errStartAdmissionEvidenceStale
	}
	return classifyStartTransitionPersistenceError(err)
}

// A deployment's own cryptographically verified controller-backed CI run is
// the sole exception to the maintenance fence. This permits the protected
// distributed smoke gate while the lease remains held through rollback. The
// identity is encoded by the CI API only after OIDC/API-key source validation.
func deploymentLeaseAuthorizationForCITest(test *ManagedTest) (string, string) {
	if test == nil {
		return "", ""
	}
	metadata, err := decodeCIRunMetadata(test.MaterializationKey)
	if err != nil {
		return "", ""
	}
	repository := strings.TrimSpace(metadata.Source.Repository)
	runID := strings.TrimSpace(metadata.Source.RunID)
	runAttempt := strings.TrimSpace(metadata.Source.RunAttempt)
	if repository == "" || runID == "" || runAttempt == "" || strings.TrimSpace(metadata.DeploymentLeaseTokenSHA) == "" {
		return "", ""
	}
	return fmt.Sprintf("github:%s:%s:%s", repository, runID, runAttempt), strings.TrimSpace(metadata.DeploymentLeaseTokenSHA)
}
