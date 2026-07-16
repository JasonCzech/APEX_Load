package api

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

type deploymentLeaseTestStartPersistence interface {
	SaveTestStartIfNoDeploymentLease(test *storage.TestRecord, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) error
	SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *storage.TestRecord, executionLeaseName, executionLeaseOwnerID, deploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash string) (bool, error)
}

// saveManagedTestStartRecord is the only persistence path for entering a state
// that may begin execution. It combines the durable test write with the shared
// deployment lease guard, and (under HA) with the execution-leader fence.
func (tr *TestRunner) saveManagedTestStartRecord(test *ManagedTest) error {
	if tr.testStore == nil {
		// In-memory runners are used by local/unit execution and have no durable
		// state to race with another controller replica.
		return nil
	}
	if tr.testOnlyAllowUnfencedDeploymentLeaseStart {
		// This branch is reachable only through a test-only option compiled from
		// a _test.go file. It preserves deterministic SaveTest fault injection
		// without making capability detection a production bypass.
		return tr.saveManagedTestRecord(test)
	}
	store, ok := tr.testStore.(deploymentLeaseTestStartPersistence)
	if !ok {
		return fmt.Errorf("deployment lease start fence is unavailable for the configured test store")
	}
	record := buildManagedTestRecord(test)
	authorizedDeploymentOwnerID, authorizedDeploymentTokenHash := deploymentLeaseAuthorizationForCITest(test)
	if tr.durableExecutionFence {
		if test == nil || test.executionFence == "" {
			return fmt.Errorf("%w: test has no claimed execution epoch", errNotExecutionLeader)
		}
		saved, err := store.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(record, executionLeaseName, test.executionFence, storage.DefaultDeploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash)
		if err != nil {
			return err
		}
		if !saved {
			return fmt.Errorf("%w: test %s belongs to an expired execution epoch", errNotExecutionLeader, test.ID)
		}
		return nil
	}
	return store.SaveTestStartIfNoDeploymentLease(record, storage.DefaultDeploymentLeaseName, authorizedDeploymentOwnerID, authorizedDeploymentTokenHash)
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
