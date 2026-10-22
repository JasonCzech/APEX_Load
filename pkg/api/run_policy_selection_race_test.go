package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Only the real admission write is failed; policy and run reads use SQLite.
type runPolicyAdmissionFailureStore struct {
	SQLiteStore *storage.SQLiteStore
	fail        atomic.Bool
}

func (s *runPolicyAdmissionFailureStore) SaveTestStartIfNoDeploymentLease(record *storage.TestRecord, name, owner, token string) error {
	if s.fail.Load() {
		return errTelemetryPolicyStartFixture
	}
	return s.SQLiteStore.SaveTestStartIfNoDeploymentLease(record, name, owner, token)
}
func (s *runPolicyAdmissionFailureStore) SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(record *storage.TestRecord, name, owner, deploymentName, deploymentOwner, token string) (bool, error) {
	if s.fail.Load() {
		return false, errTelemetryPolicyStartFixture
	}
	return s.SQLiteStore.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(record, name, owner, deploymentName, deploymentOwner, token)
}

func TestRunAnalysisPolicySelectionSurvivesFailedQueuedAndPendingAdmission(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			t.Setenv(config.EnvArchiveDir, t.TempDir())
			runner, store, run := newPassiveTransitionTestRunner(t, fmt.Sprintf("selection-retry-%t", pending))
			policy := storage.AnalysisDefinition{ID: "chosen", ProjectID: normalizeProjectID(run.ProjectID), Kind: storage.AnalysisDefinitionKindBrowserPolicy, Name: "Chosen", Definition: json.RawMessage(selectedBrowserPolicyJSON)}
			if _, err := store.SaveAnalysisDefinition(policy, 0); err != nil {
				t.Fatal(err)
			}
			backend := newTelemetryPolicyBarrierBackend(t)
			runner.setExecutionBackend(backend)
			runner.mu.Lock()
			run.AnalysisPolicySelections = &RunAnalysisPolicySelections{Browser: &RunAnalysisPolicySelection{ID: policy.ID, Revision: 1}}
			if pending {
				run.Status = "PENDING"
				now := time.Now().UTC()
				run.PendingSince = &now
				run.PendingReason = "controlled policy retry"
			}
			err := runner.saveManagedTestRecord(run)
			runner.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			wrapper := &runPolicyAdmissionFailureStore{SQLiteStore: store}
			wrapper.fail.Store(true)
			runner.testStore = sqliteOverlay(wrapper)
			start := func() error {
				if pending {
					term, err := runner.captureExecutionLeaseTerm()
					if err != nil {
						return err
					}
					return runner.promotePendingTestsWithTerm(term)
				}
				return runner.StartTest(run.ID)
			}
			if err := start(); !errors.Is(err, errTelemetryPolicyStartFixture) {
				t.Fatal("did not reach controlled write", err)
			}
			record, err := store.GetTest(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if decodeStoredInitialProvenance(record.InitialProvenance) != nil {
				t.Fatal("failed admission exposed a policy receipt")
			}
			policy.Name = "Later revision"
			if _, err := store.SaveAnalysisDefinition(policy, 1); err != nil {
				t.Fatal(err)
			}
			wrapper.fail.Store(false)
			if err := start(); err != nil {
				t.Fatal(err)
			}
			backend.expected.Store(true)
			waitTelemetryBackend(t, backend)
			record, err = store.GetTest(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			initial := decodeStoredInitialProvenance(record.InitialProvenance)
			if initial == nil || len(initial.PolicyPins) != 1 || initial.PolicyPins[0].ID != policy.ID || initial.PolicyPins[0].Revision != 1 || initial.PolicyPins[0].Source != "run_selection" {
				t.Fatal("retry changed immutable selection", initial)
			}
			finishTelemetryBackend(t, backend)
		})
	}
}

type runPolicySelectionReadBarrier struct {
	SQLiteStore      *storage.SQLiteStore
	entered, release chan struct{}
	once             sync.Once
}

func (s *runPolicySelectionReadBarrier) GetAnalysisPolicyRevision(project, id string, revision int64) (*storage.AnalysisDefinition, *storage.AnalysisDefinition, error) {
	s.once.Do(func() { close(s.entered); <-s.release })
	return s.SQLiteStore.GetAnalysisPolicyRevision(project, id, revision)
}

func TestRunAnalysisPolicySelectionDeletionBeforeResolutionRefusesTraffic(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, run := newPassiveTransitionTestRunner(t, "selection-deleted")
	policy := storage.AnalysisDefinition{ID: "chosen", ProjectID: normalizeProjectID(run.ProjectID), Kind: storage.AnalysisDefinitionKindBrowserPolicy, Name: "Chosen", Definition: json.RawMessage(selectedBrowserPolicyJSON)}
	if _, err := store.SaveAnalysisDefinition(policy, 0); err != nil {
		t.Fatal(err)
	}
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	runner.mu.Lock()
	run.AnalysisPolicySelections = &RunAnalysisPolicySelections{Browser: &RunAnalysisPolicySelection{ID: policy.ID, Revision: 1}}
	err := runner.saveManagedTestRecord(run)
	runner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	barrier := &runPolicySelectionReadBarrier{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{})}
	runner.testStore = sqliteOverlay(barrier)
	done := make(chan error, 1)
	go func() { done <- runner.StartTest(run.ID) }()
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		close(barrier.release)
		t.Fatal("resolution did not reach barrier")
	}
	policy.Deleted = true
	if _, err := store.SaveAnalysisDefinition(policy, 1); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	close(barrier.release)
	select {
	case err := <-done:
		if !errors.Is(err, errRunPolicyConfiguration) {
			t.Fatal("deleted selection was admitted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not finish")
	}
	select {
	case <-backend.entered:
		backend.expected.Store(true)
		t.Fatal("refused policy reached execution")
	default:
	}
	record, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "QUEUED" || decodeStoredInitialProvenance(record.InitialProvenance) != nil {
		t.Fatal("refused selection published admission", record.Status)
	}
}

func TestRunAnalysisPolicySelectionDoesNotAdmitReplacedRun(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, run := newPassiveTransitionTestRunner(t, "selection-replaced")
	policy := storage.AnalysisDefinition{ID: "chosen", ProjectID: normalizeProjectID(run.ProjectID), Kind: storage.AnalysisDefinitionKindBrowserPolicy, Name: "Chosen", Definition: json.RawMessage(selectedBrowserPolicyJSON)}
	if _, err := store.SaveAnalysisDefinition(policy, 0); err != nil {
		t.Fatal(err)
	}
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	runner.mu.Lock()
	run.AnalysisPolicySelections = &RunAnalysisPolicySelections{Browser: &RunAnalysisPolicySelection{ID: policy.ID, Revision: 1}}
	err := runner.saveManagedTestRecord(run)
	runner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	barrier := &runPolicySelectionReadBarrier{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{})}
	runner.testStore = sqliteOverlay(barrier)
	var released sync.Once
	release := func() { released.Do(func() { close(barrier.release) }) }
	defer release()
	done := make(chan error, 1)
	go func() { done <- runner.StartTest(run.ID) }()
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("selection resolution did not reach barrier")
	}
	replacement, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTest(run.ID); err != nil {
		t.Fatal(err)
	}
	replacement.RefKey += "-replacement"
	replacement.StartTime = replacement.StartTime.Add(time.Microsecond)
	replacement.AnalysisPolicySelections = nil
	if err := store.SaveTest(replacement); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-done:
		if !errors.Is(err, storage.ErrTestStartIdentityChanged) && !errors.Is(err, errStartAdmissionEvidenceStale) {
			t.Fatal("replacement was not fenced", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not leave resolution")
	}
	record, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.RefKey != replacement.RefKey || record.Status != "QUEUED" || record.AnalysisPolicySelections != nil || decodeStoredInitialProvenance(record.InitialProvenance) != nil {
		t.Fatal("old selection contaminated replacement", record)
	}
	select {
	case <-backend.entered:
		backend.expected.Store(true)
		t.Fatal("stale selection reached execution")
	default:
	}
}
