package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type admissionEvidenceStore struct {
	storage.Store
	reservationCalls             atomic.Int32
	occurrenceCalls              atomic.Int32
	projectCalls                 atomic.Int32
	projectReadWhileRunnerLocked atomic.Bool
	releaseCalls                 atomic.Int32
	releaseReadWhileRunnerLocked atomic.Bool
	scheduleIOCalls              atomic.Int32
	scheduleIOWhileRunnerLocked  atomic.Bool
	applicationCalls             atomic.Int32
	applicationReadWhileLocked   atomic.Bool
	testCreateReadWhileLocked    atomic.Bool
	blockReservations            atomic.Bool
	blockTestCreate              atomic.Bool
	blockTestCreateReturn        atomic.Bool
	blockLeaseRead               atomic.Bool
	leaseReadWhileRunnerLocked   atomic.Bool
	reservationEntered           chan struct{}
	releaseReservation           chan struct{}
	testCreateEntered            chan struct{}
	releaseTestCreate            chan struct{}
	testCreateCommitted          chan struct{}
	releaseTestCreateReturn      chan struct{}
	leaseReadEntered             chan struct{}
	releaseLeaseRead             chan struct{}
	enterOnce                    sync.Once
	testCreateOnce               sync.Once
	testCreateCommitOnce         sync.Once
	leaseReadOnce                sync.Once
	occurrenceErr                error
	occurrenceFailures           atomic.Int32
	occurrenceByTestErr          error
	occurrenceByTestFailures     atomic.Int32
	projectErr                   error
	releaseErr                   error
	applicationErr               error
	runner                       *TestRunner
}

type blockingPendingTransitionStore struct {
	storage.Store
	target interface {
		TransitionTestToPending(storage.TestPendingTransition) (storage.TestStatusTransitionResult, error)
	}
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type committedPendingResponseLossStore struct {
	storage.Store
	target interface {
		TransitionTestToPending(storage.TestPendingTransition) (storage.TestStatusTransitionResult, error)
	}
	remainingLosses atomic.Int32
	err             error
}

func (s *committedPendingResponseLossStore) TransitionTestToPending(transition storage.TestPendingTransition) (storage.TestStatusTransitionResult, error) {
	result, err := s.target.TransitionTestToPending(transition)
	if err == nil && (result == storage.TestStatusTransitionUpdated || result == storage.TestStatusTransitionUnchanged) {
		for {
			remaining := s.remainingLosses.Load()
			if remaining <= 0 {
				break
			}
			if s.remainingLosses.CompareAndSwap(remaining, remaining-1) {
				return "", s.err
			}
		}
	}
	return result, err
}

func (s *blockingPendingTransitionStore) TransitionTestToPending(transition storage.TestPendingTransition) (storage.TestStatusTransitionResult, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.target.TransitionTestToPending(transition)
}

func (s *admissionEvidenceStore) TransitionTestToPending(transition storage.TestPendingTransition) (storage.TestStatusTransitionResult, error) {
	store, ok := s.Store.(interface {
		TransitionTestToPending(storage.TestPendingTransition) (storage.TestStatusTransitionResult, error)
	})
	if !ok {
		return storage.TestStatusTransitionFenced, errors.New("wrapped store does not support pending transitions")
	}
	return store.TransitionTestToPending(transition)
}

func (s *admissionEvidenceStore) GetSchedulerLease(name string) (*storage.SchedulerLease, error) {
	if s.runner != nil {
		if s.runner.mu.TryLock() {
			s.runner.mu.Unlock()
		} else {
			s.leaseReadWhileRunnerLocked.Store(true)
		}
	}
	if s.blockLeaseRead.Load() {
		s.leaseReadOnce.Do(func() { close(s.leaseReadEntered) })
		<-s.releaseLeaseRead
	}
	return s.Store.GetSchedulerLease(name)
}

func (s *admissionEvidenceStore) StartAdmissionRevision() (int64, error) {
	return s.Store.(interface {
		StartAdmissionRevision() (int64, error)
	}).StartAdmissionRevision()
}

func (s *admissionEvidenceStore) SaveTestStartIfNoDeploymentLease(test *storage.TestRecord, leaseName, ownerID, tokenHash string) error {
	return s.Store.(interface {
		SaveTestStartIfNoDeploymentLease(*storage.TestRecord, string, string, string) error
	}).SaveTestStartIfNoDeploymentLease(test, leaseName, ownerID, tokenHash)
}

func (s *admissionEvidenceStore) SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *storage.TestRecord, executionLeaseName, executionOwnerID, deploymentLeaseName, deploymentOwnerID, deploymentTokenHash string) (bool, error) {
	return s.Store.(interface {
		SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(*storage.TestRecord, string, string, string, string, string) (bool, error)
	}).SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test, executionLeaseName, executionOwnerID, deploymentLeaseName, deploymentOwnerID, deploymentTokenHash)
}

func (s *admissionEvidenceStore) observeScheduleIO() {
	s.scheduleIOCalls.Add(1)
	if s.runner == nil {
		return
	}
	if s.runner.mu.TryLock() {
		s.runner.mu.Unlock()
		return
	}
	s.scheduleIOWhileRunnerLocked.Store(true)
}

func (s *admissionEvidenceStore) SaveScenarioSchedule(schedule *storage.ScenarioSchedule) error {
	s.observeScheduleIO()
	return s.Store.SaveScenarioSchedule(schedule)
}

func (s *admissionEvidenceStore) DeleteScenarioSchedule(id string) error {
	s.observeScheduleIO()
	return s.Store.DeleteScenarioSchedule(id)
}

func (s *admissionEvidenceStore) ListScenarioScheduleOccurrences(scheduleID string) ([]*storage.ScenarioScheduleOccurrence, error) {
	s.observeScheduleIO()
	return s.Store.ListScenarioScheduleOccurrences(scheduleID)
}

func (s *admissionEvidenceStore) GetScenarioScheduleOccurrence(id string) (*storage.ScenarioScheduleOccurrence, error) {
	s.observeScheduleIO()
	return s.Store.GetScenarioScheduleOccurrence(id)
}

func (s *admissionEvidenceStore) DeleteScenarioScheduleOccurrence(id string) error {
	s.observeScheduleIO()
	return s.Store.DeleteScenarioScheduleOccurrence(id)
}

func (s *admissionEvidenceStore) SaveScenarioScheduleApproval(approval *storage.ScenarioScheduleApproval) error {
	s.observeScheduleIO()
	return s.Store.SaveScenarioScheduleApproval(approval)
}

func (s *admissionEvidenceStore) ResolveScenarioScheduleApproval(approval *storage.ScenarioScheduleApproval, schedule *storage.ScenarioSchedule, expectedRevision int) (bool, error) {
	s.observeScheduleIO()
	return s.Store.(interface {
		ResolveScenarioScheduleApproval(*storage.ScenarioScheduleApproval, *storage.ScenarioSchedule, int) (bool, error)
	}).ResolveScenarioScheduleApproval(approval, schedule, expectedRevision)
}

func (s *admissionEvidenceStore) GetScenarioScheduleApproval(id string) (*storage.ScenarioScheduleApproval, error) {
	s.observeScheduleIO()
	return s.Store.GetScenarioScheduleApproval(id)
}

func (s *admissionEvidenceStore) ListScenarioScheduleApprovals(scheduleID string) ([]*storage.ScenarioScheduleApproval, error) {
	s.observeScheduleIO()
	return s.Store.ListScenarioScheduleApprovals(scheduleID)
}

func (s *admissionEvidenceStore) GetProject(id string) (*storage.Project, error) {
	s.projectCalls.Add(1)
	if s.runner != nil {
		if s.runner.mu.TryLock() {
			s.runner.mu.Unlock()
		} else {
			s.projectReadWhileRunnerLocked.Store(true)
		}
	}
	if s.projectErr != nil {
		return nil, s.projectErr
	}
	return s.Store.GetProject(id)
}

func (s *admissionEvidenceStore) GetRelease(id string) (*storage.Release, error) {
	s.releaseCalls.Add(1)
	if s.runner != nil {
		if s.runner.mu.TryLock() {
			s.runner.mu.Unlock()
		} else {
			s.releaseReadWhileRunnerLocked.Store(true)
		}
	}
	if s.releaseErr != nil {
		return nil, s.releaseErr
	}
	return s.Store.GetRelease(id)
}

func (s *admissionEvidenceStore) GetApplication(id string) (*storage.Application, error) {
	s.applicationCalls.Add(1)
	if s.runner != nil {
		if s.runner.mu.TryLock() {
			s.runner.mu.Unlock()
		} else {
			s.applicationReadWhileLocked.Store(true)
		}
	}
	if s.applicationErr != nil {
		return nil, s.applicationErr
	}
	return s.Store.GetApplication(id)
}

func (s *admissionEvidenceStore) ListScenarioScheduleOccurrencesByTimeRange(projectID string, from, to time.Time) ([]*storage.ScenarioScheduleOccurrence, error) {
	s.occurrenceCalls.Add(1)
	if s.occurrenceErr != nil {
		for {
			remaining := s.occurrenceFailures.Load()
			if remaining < 0 {
				return nil, s.occurrenceErr
			}
			if remaining == 0 {
				break
			}
			if s.occurrenceFailures.CompareAndSwap(remaining, remaining-1) {
				return nil, s.occurrenceErr
			}
		}
	}
	windowStore := s.Store.(interface {
		ListScenarioScheduleOccurrencesByTimeRange(string, time.Time, time.Time) ([]*storage.ScenarioScheduleOccurrence, error)
	})
	return windowStore.ListScenarioScheduleOccurrencesByTimeRange(projectID, from, to)
}

func (s *admissionEvidenceStore) GetScenarioScheduleOccurrenceByTestID(testID string) (*storage.ScenarioScheduleOccurrence, error) {
	for {
		remaining := s.occurrenceByTestFailures.Load()
		if remaining <= 0 {
			return s.Store.(interface {
				GetScenarioScheduleOccurrenceByTestID(string) (*storage.ScenarioScheduleOccurrence, error)
			}).GetScenarioScheduleOccurrenceByTestID(testID)
		}
		if s.occurrenceByTestFailures.CompareAndSwap(remaining, remaining-1) {
			return nil, s.occurrenceByTestErr
		}
	}
}

func TestPreflightLaunchBlocksWhenScheduleOccurrenceEvidenceIsUnavailable(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	store.occurrenceErr = errors.New("occurrence store unavailable")
	store.occurrenceFailures.Store(-1)
	response, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name: "unknown occurrences", Duration: "5m", ScriptID: "storage-evidence-script", VUsers: 1,
	}})
	if err != nil {
		t.Fatalf("PreflightLaunch: %v", err)
	}
	if response == nil || len(response.BlockingConflicts) == 0 {
		t.Fatalf("preflight response = %#v, want a transient blocking conflict", response)
	}
	conflict := response.BlockingConflicts[0]
	if conflict.Code != "launch_occurrence_evidence_unavailable" || conflict.Severity != "blocking" {
		t.Fatalf("occurrence evidence conflict = %#v", conflict)
	}
}

type oneShotReservationOutage struct {
	base      reservationPersistence
	remaining atomic.Int32
	err       error
}

func (s *oneShotReservationOutage) GetCapacityReservation(id string) (*storage.CapacityReservation, error) {
	return s.base.GetCapacityReservation(id)
}

func (s *oneShotReservationOutage) UpdateCapacityReservationBinding(id string, testID *string, status *string, updatedAt time.Time) (bool, error) {
	return s.base.UpdateCapacityReservationBinding(id, testID, status, updatedAt)
}

func (s *oneShotReservationOutage) ClearCapacityReservationBinding(id, testID string, updatedAt time.Time) (bool, error) {
	return s.base.ClearCapacityReservationBinding(id, testID, updatedAt)
}

func (s *oneShotReservationOutage) ListCapacityReservationsByTimeRange(projectID string, from, to time.Time) ([]*storage.CapacityReservation, error) {
	for {
		remaining := s.remaining.Load()
		if remaining <= 0 {
			return s.base.ListCapacityReservationsByTimeRange(projectID, from, to)
		}
		if s.remaining.CompareAndSwap(remaining, remaining-1) {
			return nil, s.err
		}
	}
}

func TestPendingPromotionRetriesUnavailableLaunchEvidence(t *testing.T) {
	for _, source := range []string{"reservation", "occurrence", "occurrence correlation"} {
		t.Run(source, func(t *testing.T) {
			runner, store := newAdmissionEvidenceRunner(t)
			backend := &executionBackendSpy{called: make(chan string, 1)}
			runner.setExecutionBackend(backend)
			test, err := runner.CreateTest(CreateTestRequest{
				Name: "pending evidence retry", ScriptID: "storage-evidence-script", VUsers: 1, Duration: "1m",
			})
			if err != nil {
				t.Fatalf("CreateTest: %v", err)
			}
			runner.mu.Lock()
			if err := runner.markTestPendingLocked(runner.tests[test.ID], "waiting for capacity"); err != nil {
				runner.mu.Unlock()
				t.Fatalf("mark pending: %v", err)
			}
			runner.mu.Unlock()

			outage := errors.New("temporary " + source + " evidence outage")
			if source == "occurrence" {
				store.occurrenceErr = outage
				store.occurrenceFailures.Store(1)
			} else if source == "occurrence correlation" {
				store.occurrenceByTestErr = outage
				store.occurrenceByTestFailures.Store(1)
			} else {
				flaky := &oneShotReservationOutage{base: store, err: outage}
				flaky.remaining.Store(1)
				runner.reservationStore = flaky
			}
			runner.promotePendingTests()
			select {
			case startedID := <-backend.called:
				if startedID != test.ID {
					t.Fatalf("backend started %q, want %q", startedID, test.ID)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("pending retry worker did not launch after evidence recovered")
			}
			current, ok := runner.GetTest(test.ID)
			if !ok || current.Status == "FAILED" || current.Error != "" {
				t.Fatalf("recovered pending test = %#v", current)
			}
		})
	}
}

func (s *admissionEvidenceStore) ListCapacityReservationsByTimeRange(projectID string, from, to time.Time) ([]*storage.CapacityReservation, error) {
	s.reservationCalls.Add(1)
	if s.blockReservations.Load() {
		s.enterOnce.Do(func() { close(s.reservationEntered) })
		<-s.releaseReservation
	}
	return s.Store.ListCapacityReservationsByTimeRange(projectID, from, to)
}

func (s *admissionEvidenceStore) CreateTestIfAbsent(test *storage.TestRecord) (bool, error) {
	if s.runner != nil {
		if s.runner.mu.TryLock() {
			s.runner.mu.Unlock()
		} else {
			s.testCreateReadWhileLocked.Store(true)
		}
	}
	if s.blockTestCreate.Load() {
		s.testCreateOnce.Do(func() { close(s.testCreateEntered) })
		<-s.releaseTestCreate
	}
	store := s.Store.(interface {
		CreateTestIfAbsent(*storage.TestRecord) (bool, error)
	})
	created, err := store.CreateTestIfAbsent(test)
	s.afterTestCreateCommit(created, err)
	return created, err
}

func (s *admissionEvidenceStore) afterTestCreateCommit(created bool, err error) {
	if !s.blockTestCreateReturn.Load() || err != nil || !created {
		return
	}
	s.testCreateCommitOnce.Do(func() { close(s.testCreateCommitted) })
	<-s.releaseTestCreateReturn
}

func (s *admissionEvidenceStore) CreateTestIfAbsentIfLeaseHeld(test *storage.TestRecord, leaseName, leaseOwnerID string) (bool, error) {
	store := s.Store.(interface {
		CreateTestIfAbsentIfLeaseHeld(*storage.TestRecord, string, string) (bool, error)
	})
	return store.CreateTestIfAbsentIfLeaseHeld(test, leaseName, leaseOwnerID)
}

func (s *admissionEvidenceStore) CreateTestIfAbsentIfLeaseTermHeld(test *storage.TestRecord, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error) {
	store := s.Store.(interface {
		CreateTestIfAbsentIfLeaseTermHeld(*storage.TestRecord, string, string, uint64) (bool, error)
	})
	created, err := store.CreateTestIfAbsentIfLeaseTermHeld(test, leaseName, leaseOwnerID, leaseGeneration)
	s.afterTestCreateCommit(created, err)
	return created, err
}

func (s *admissionEvidenceStore) TryAcquireSchedulerLeaseTerm(name, ownerID string, now time.Time, ttl time.Duration) (bool, *storage.SchedulerLease, error) {
	store := s.Store.(interface {
		TryAcquireSchedulerLeaseTerm(string, string, time.Time, time.Duration) (bool, *storage.SchedulerLease, error)
	})
	return store.TryAcquireSchedulerLeaseTerm(name, ownerID, now, ttl)
}

func newAdmissionEvidenceRunner(t *testing.T) (*TestRunner, *admissionEvidenceStore) {
	t.Helper()
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &admissionEvidenceStore{
		Store: base, reservationEntered: make(chan struct{}), releaseReservation: make(chan struct{}),
		testCreateEntered: make(chan struct{}), releaseTestCreate: make(chan struct{}),
		testCreateCommitted: make(chan struct{}), releaseTestCreateReturn: make(chan struct{}),
		leaseReadEntered: make(chan struct{}), releaseLeaseRead: make(chan struct{}),
	}
	runner := NewTestRunner(10, NewHub(), store)
	store.runner = runner
	storePreflightScript(runner, "storage-evidence-script", storage.DefaultProjectID)
	store.reservationCalls.Store(0)
	store.occurrenceCalls.Store(0)
	return runner, store
}

func TestSameStoredTestIdentityUsesPostgresTimestampPrecision(t *testing.T) {
	createdAt := time.Date(2026, time.August, 5, 12, 0, 0, 123456789, time.FixedZone("offset", -4*60*60))
	test := &ManagedTest{ID: "postgres-precision", ProjectID: "project-a", CreatedAt: createdAt}
	tests := []struct {
		name     string
		record   *storage.TestRecord
		wantSame bool
	}{
		{
			name: "postgres microsecond round trip",
			record: &storage.TestRecord{
				ID: "postgres-precision", ProjectID: "project-a",
				StartTime: createdAt.UTC().Truncate(time.Microsecond),
			},
			wantSame: true,
		},
		{
			name: "different creation microsecond",
			record: &storage.TestRecord{
				ID: "postgres-precision", ProjectID: "project-a",
				StartTime: createdAt.UTC().Truncate(time.Microsecond).Add(time.Microsecond),
			},
		},
		{
			name: "different project",
			record: &storage.TestRecord{
				ID: "postgres-precision", ProjectID: "project-b",
				StartTime: createdAt.UTC().Truncate(time.Microsecond),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameStoredTestIdentity(test, tt.record); got != tt.wantSame {
				t.Fatalf("sameStoredTestIdentity() = %v, want %v", got, tt.wantSame)
			}
		})
	}
}

func TestRuntimeCapacityReservationStatusesIncludeExecutionTransitions(t *testing.T) {
	for _, status := range []string{"SHAKEOUT", "STARTING", "INITIALIZING", "RUNNING", "PAUSED", "STOPPING"} {
		t.Run(status, func(t *testing.T) {
			if !testReservesRuntimeCapacity(&ManagedTest{Status: status}) {
				t.Fatalf("status %q did not reserve runtime capacity", status)
			}
		})
	}
	for _, status := range []string{"QUEUED", "PENDING", "COMPLETE", "FAILED", "SHAKEOUT_REVIEW"} {
		t.Run(status, func(t *testing.T) {
			if testReservesRuntimeCapacity(&ManagedTest{Status: status}) {
				t.Fatalf("status %q unexpectedly reserved runtime capacity", status)
			}
		})
	}
}

func TestDeletedSystemSettingsConvergeAcrossConnectionsBeforeFreshStart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "settings-delete-convergence.db")
	writer, err := storage.NewSQLiteStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	reader, err := storage.NewSQLiteStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	settings := defaultSystemSettings(3)
	settings.General.MaxConcurrentTests = 1
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.SaveSetting(systemSettingsStoreKey, string(encoded)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}

	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner := NewTestRunner(3, NewHub(), reader, WithExecutionBackend(backend))
	if runner.maxTests != 1 {
		t.Fatalf("initial max tests = %d, want durable override 1", runner.maxTests)
	}
	storePreflightScript(runner, "settings-delete-script", storage.DefaultProjectID)
	runner.mu.Lock()
	runner.tests["existing-active-run"] = &ManagedTest{
		ID: "existing-active-run", Name: "existing active run", Status: "RUNNING",
		ProjectID: storage.DefaultProjectID, CreatedAt: time.Now().UTC(), TotalVUsers: 1,
	}
	runner.mu.Unlock()
	candidate, err := runner.CreateTest(CreateTestRequest{
		Name: "fresh start after settings delete", ScriptID: "settings-delete-script", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if err := writer.DeleteSetting(systemSettingsStoreKey); err != nil {
		t.Fatalf("DeleteSetting: %v", err)
	}
	if err := runner.StartTest(candidate.ID); err != nil {
		t.Fatalf("StartTest: %v", err)
	}
	select {
	case startedID := <-backend.called:
		if startedID != candidate.ID {
			t.Fatalf("backend started %q, want %q", startedID, candidate.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fresh start remained blocked by deleted settings override")
	}
	runner.mu.RLock()
	maxTests := runner.maxTests
	runner.mu.RUnlock()
	if maxTests != 3 {
		t.Fatalf("converged max tests = %d, want constructor default 3", maxTests)
	}
}

func TestPendingTransitionCannotOverwriteConcurrentDurableMutationOrResurrectDeletion(t *testing.T) {
	for _, mutation := range []string{"reject reservation and edit groups", "delete test"} {
		t.Run(mutation, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "pending-transition-cas.db")
			writer, err := storage.NewSQLiteStore(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Close() })
			reader, err := storage.NewSQLiteStore(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reader.Close() })

			now := time.Now().UTC()
			projectID := "pending-transition-project"
			if err := writer.SaveProject(&storage.Project{ID: projectID, Name: "Pending transition", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatalf("SaveProject: %v", err)
			}
			timeslotID := "pending-transition-slot"
			vusers := 10
			if err := writer.SaveCapacityReservation(&storage.CapacityReservation{
				ID: timeslotID, ProjectID: projectID, StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour),
				ApprovalState: "approved", RequestedCapacity: &storage.CapacityReservationRequest{VUsers: &vusers},
				CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("SaveCapacityReservation: %v", err)
			}
			runner := NewTestRunner(2, NewHub(), reader)
			storePreflightScript(runner, "pending-transition-script", projectID)
			candidate, err := runner.CreateTest(CreateTestRequest{
				Name: "pending transition candidate", ProjectID: projectID, ScriptID: "pending-transition-script",
				VUsers: 1, Duration: "1m", TimeslotID: &timeslotID,
			})
			if err != nil {
				t.Fatalf("CreateTest: %v", err)
			}
			revision, err := reader.StartAdmissionRevision()
			if err != nil {
				t.Fatalf("StartAdmissionRevision: %v", err)
			}
			runner.mu.Lock()
			runner.tests[candidate.ID].expectedStartAdmissionRevision = revision
			runner.mu.Unlock()
			blocker := &blockingPendingTransitionStore{
				Store: reader, target: reader, entered: make(chan struct{}), release: make(chan struct{}),
			}
			runner.testStore = blocker

			result := make(chan error, 1)
			go func() {
				runner.mu.Lock()
				err := runner.markTestPendingLocked(runner.tests[candidate.ID], "waiting for capacity")
				runner.mu.Unlock()
				result <- err
			}()
			select {
			case <-blocker.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("pending transition did not reach blocked targeted persistence")
			}
			lockAcquired := make(chan struct{})
			go func() {
				runner.mu.Lock()
				close(lockAcquired)
				runner.mu.Unlock()
			}()
			select {
			case <-lockAcquired:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("runner mutex remained held during pending persistence")
			}

			if mutation == "delete test" {
				if err := writer.DeleteTest(candidate.ID); err != nil {
					t.Fatalf("DeleteTest: %v", err)
				}
			} else {
				changed, err := writer.UpdateCapacityReservationApprovalState(
					timeslotID, []string{"approved"}, "rejected", time.Now().UTC(),
				)
				if err != nil || !changed {
					t.Fatalf("reject reservation = %t, %v", changed, err)
				}
				durable, err := writer.GetTest(candidate.ID)
				if err != nil {
					t.Fatalf("GetTest after reject: %v", err)
				}
				durable.Groups = []any{map[string]any{"name": "concurrent durable group edit"}}
				if err := writer.SaveTest(durable); err != nil {
					t.Fatalf("SaveTest concurrent group edit: %v", err)
				}
			}
			close(blocker.release)
			select {
			case err := <-result:
				if mutation == "delete test" {
					if !errors.Is(err, errStoredTestMissing) {
						t.Fatalf("pending transition error = %v, want missing identity", err)
					}
				} else if !errors.Is(err, errStartAdmissionEvidenceStale) {
					t.Fatalf("pending transition error = %v, want stale admission evidence", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("pending transition did not finish")
			}

			if mutation == "delete test" {
				if _, err := writer.GetTest(candidate.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("deleted test lookup error = %v, want sql.ErrNoRows", err)
				}
				if ghost, ok := runner.GetTest(candidate.ID); ok || ghost != nil {
					t.Fatalf("deleted test was resurrected process-locally: %#v", ghost)
				}
				return
			}
			durable, err := writer.GetTest(candidate.ID)
			if err != nil {
				t.Fatalf("GetTest after pending race: %v", err)
			}
			if durable.Status != "QUEUED" || durable.TimeslotID != nil {
				t.Fatalf("durable lifecycle/binding = status %q timeslot %#v, want QUEUED/unbound", durable.Status, durable.TimeslotID)
			}
			encodedGroups, err := json.Marshal(durable.Groups)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encodedGroups), "concurrent durable group edit") {
				t.Fatalf("durable groups were overwritten by stale pending snapshot: %s", encodedGroups)
			}
		})
	}
}

func TestPendingTransitionLostResponseConvergesOnlyForOriginalAuthorization(t *testing.T) {
	runner, store := storeBackedRunner(t)
	settings := runner.GetSystemSettings()
	settings.General.MaxConcurrentTests = 1
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10_000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	active, err := runner.CreateTest(CreateTestRequest{
		Name: "pending response-loss holder", ProjectID: "proj-a", ScriptID: "script-1", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := runner.CreateTest(CreateTestRequest{
		Name: "pending response-loss candidate", ProjectID: "proj-a", ScriptID: "script-1", VUsers: 15_000, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()
	lostResponse := errors.New("pending transition response lost after commit")
	wrapper := &committedPendingResponseLossStore{Store: store, target: store, err: lostResponse}
	wrapper.remainingLosses.Store(startAdmissionPhaseAttempts)
	runner.testStore = wrapper

	originalActor := testCreationActor{Subject: "user:original-approver", DisplayName: "Original Approver"}
	opts := StartTestOptions{OverrideCapacityShortfall: true}
	if firstErr := runner.StartTestWithActor(candidate.ID, opts, originalActor); !errors.Is(firstErr, lostResponse) {
		t.Fatalf("first StartTestWithActor error = %v, want committed-response loss", firstErr)
	}
	durableAfterCommit, err := store.GetTest(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durableAfterCommit.Status != "PENDING" || durableAfterCommit.PendingSince == nil ||
		durableAfterCommit.StartedBySubject != originalActor.Subject || durableAfterCommit.StartAuthorization == nil {
		t.Fatalf("committed pending contract = %#v", durableAfterCommit)
	}
	committedPendingSince := *durableAfterCommit.PendingSince
	runner.mu.RLock()
	localAfterLoss := runner.tests[candidate.ID]
	if localAfterLoss.Status != "QUEUED" || localAfterLoss.PendingSince != nil || localAfterLoss.StartAuthorization != nil || localAfterLoss.StartedBySubject != "" {
		runner.mu.RUnlock()
		t.Fatalf("local rollback after lost response = %#v", localAfterLoss)
	}
	runner.mu.RUnlock()

	changedActor := testCreationActor{Subject: "user:different-approver", DisplayName: "Different Approver"}
	if changedErr := runner.StartTestWithActor(candidate.ID, opts, changedActor); !errors.Is(changedErr, errStartAdmissionEvidenceStale) {
		t.Fatalf("changed-intent retry error = %v, want stale/fenced", changedErr)
	}
	durableAfterChangedRetry, err := store.GetTest(candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durableAfterChangedRetry.StartedBySubject != originalActor.Subject {
		t.Fatalf("changed retry replaced durable actor with %q", durableAfterChangedRetry.StartedBySubject)
	}

	if retryErr := runner.StartTestWithActor(candidate.ID, opts, originalActor); retryErr != nil {
		t.Fatalf("original-intent retry: %v", retryErr)
	}
	runner.mu.RLock()
	converged := runner.tests[candidate.ID]
	if converged.Status != "PENDING" || converged.PendingSince == nil || !converged.PendingSince.Equal(committedPendingSince) ||
		converged.StartedBySubject != originalActor.Subject || converged.StartAuthorization == nil {
		runner.mu.RUnlock()
		t.Fatalf("local pending state did not adopt durable commit: %#v", converged)
	}
	runner.mu.RUnlock()
}

func TestStartLeaseValidationDoesNotHoldRunnerLock(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "off-lock-start-lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &admissionEvidenceStore{
		Store: base, reservationEntered: make(chan struct{}), releaseReservation: make(chan struct{}),
		testCreateEntered: make(chan struct{}), releaseTestCreate: make(chan struct{}),
		testCreateCommitted: make(chan struct{}), releaseTestCreateReturn: make(chan struct{}),
		leaseReadEntered: make(chan struct{}), releaseLeaseRead: make(chan struct{}),
	}
	leadership := newExecutionLeadership(store, "off-lock-start-lease")
	leadership.tick()
	t.Cleanup(leadership.relinquish)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership), WithExecutionBackend(backend))
	storePreflightScript(runner, "off-lock-start-lease-script", storage.DefaultProjectID)
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "off-lock start lease", ScriptID: "off-lock-start-lease-script", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	// Scope the lock probe to StartTest. CreateTest has its own off-lock storage
	// regression coverage and can legitimately overlap unrelated runner-lock use.
	store.runner = runner
	store.blockLeaseRead.Store(true)
	startDone := make(chan error, 1)
	go func() { startDone <- runner.StartTest(test.ID) }()
	select {
	case <-store.leaseReadEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("start did not reach durable lease validation")
	}
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("durable lease validation held the global runner mutex")
	}
	close(store.releaseLeaseRead)
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("StartTest: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not finish after lease read resumed")
	}
	if store.leaseReadWhileRunnerLocked.Load() {
		t.Fatal("GetSchedulerLease observed tr.mu held")
	}
}

func TestCreateTestDurableWriteDoesNotHoldRunnerLock(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	store.blockTestCreate.Store(true)
	result := make(chan error, 1)
	go func() {
		_, err := runner.CreateTest(CreateTestRequest{
			Name: "unlocked durable create", Duration: "1m", ScriptID: "storage-evidence-script", VUsers: 1,
		})
		result <- err
	}()

	select {
	case <-store.testCreateEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not reach durable create-only write")
	}
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner lock was held while durable test creation was blocked")
	}
	close(store.releaseTestCreate)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("CreateTest: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not complete")
	}
	if store.testCreateReadWhileLocked.Load() {
		t.Fatal("durable test creation began while runner.mu was held")
	}
}

func TestPreflightLaunchLoadsDurableEvidenceOutsideRunnerLockOnce(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	store.blockReservations.Store(true)
	result := make(chan error, 1)
	go func() {
		_, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
			Name: "evidence", Duration: "5m", ScriptID: "storage-evidence-script", VUsers: 1,
		}})
		result <- err
	}()

	select {
	case <-store.reservationEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("preflight did not reach reservation evidence query")
	}
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner lock was held while reservation query was blocked")
	}
	close(store.releaseReservation)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("PreflightLaunch: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preflight did not complete")
	}
	if got := store.reservationCalls.Load(); got != 1 {
		t.Fatalf("reservation range queries = %d, want one request-scoped query", got)
	}
	if got := store.occurrenceCalls.Load(); got != 1 {
		t.Fatalf("occurrence range queries = %d, want one request-scoped query", got)
	}
}

type blockingLoadModelDataPoolStore struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingLoadModelDataPoolStore) GetDataPool(id string) (*storage.SharedDataPool, error) {
	s.once.Do(func() {
		close(s.entered)
		<-s.release
	})
	return &storage.SharedDataPool{ID: id, ProjectID: "project-a", Name: "Customers"}, nil
}

func (s *blockingLoadModelDataPoolStore) ListDataPoolRows(string) ([]*storage.SharedDataPoolRow, error) {
	return []*storage.SharedDataPoolRow{{ID: "row-1", Data: map[string]string{"email": "user@example.test"}}}, nil
}

func TestLoadModelValidationReadsDataPoolsOutsideRunnerLock(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	storePreflightScript(runner, "load-model-lock-script", "project-a")
	poolStore := &blockingLoadModelDataPoolStore{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-poolStore.release:
		default:
			close(poolStore.release)
		}
	})
	runner.dataPoolStore = poolStore

	result := make(chan error, 1)
	go func() {
		_, err := runner.ValidateScenarioLoadModel(CreateScenarioRequest{
			Name: "load model lock", Duration: "1m",
			Groups:    []CreateGroupEntry{{ScriptID: "load-model-lock-script", VUsers: 1}},
			DataPools: []storage.ScenarioDataPoolBinding{{PoolID: "customers", Alias: "customers"}},
		})
		result <- err
	}()
	select {
	case <-poolStore.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("load-model validation did not reach data-pool lookup")
	}
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner lock was held while data-pool lookup was blocked")
	}
	close(poolStore.release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("ValidateScenarioLoadModel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("load-model validation did not complete")
	}
}

func TestScaleAdmissionLoadsDurableEvidenceOutsideRunnerLockOnce(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	now := time.Now().UTC()
	runner.tests["scale-evidence"] = &ManagedTest{
		ID:          "scale-evidence",
		Name:        "scale evidence",
		Status:      "RUNNING",
		ProjectID:   storage.DefaultProjectID,
		CreatedAt:   now.Add(-time.Minute),
		StartedAt:   cloneTimePtr(&now),
		Duration:    5 * time.Minute,
		DurationStr: "5m",
		TotalVUsers: 1,
		Groups:      []TestGroup{{Name: "buyers", VUsers: 1}},
	}
	backend := &scalableExecutionBackendSpy{calls: make(chan scaleBackendCall, 1)}
	runner.setExecutionBackend(backend)
	store.blockReservations.Store(true)
	result := make(chan error, 1)
	go func() { result <- runner.ScaleVUsers("scale-evidence", "buyers", 2) }()

	select {
	case <-store.reservationEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("scale did not reach reservation evidence query")
	}
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner lock was held while scale evidence query was blocked")
	}
	close(store.releaseReservation)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("ScaleVUsers: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scale did not complete")
	}
	if got := store.reservationCalls.Load(); got != 1 {
		t.Fatalf("reservation range queries = %d, want one request-scoped query", got)
	}
	if got := store.occurrenceCalls.Load(); got != 1 {
		t.Fatalf("occurrence range queries = %d, want one request-scoped query", got)
	}
}

func TestFairShareSortPrecomputesReservationRanks(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	now := time.Now().UTC()
	pending := make([]*ManagedTest, 0, 40)
	for index := 39; index >= 0; index-- {
		pending = append(pending, &ManagedTest{ID: string(rune('a' + index)), ProjectID: storage.DefaultProjectID, CreatedAt: now})
	}
	store.reservationCalls.Store(0)
	activeSlots, err := runner.loadBoundTimeslotsActiveAt(now)
	if err != nil {
		t.Fatalf("load reservation evidence: %v", err)
	}
	runner.mu.Lock()
	runner.sortPendingTestsForFairShareWithTimeslotsLocked(pending, now, activeSlots)
	runner.mu.Unlock()
	if got := store.reservationCalls.Load(); got != 1 {
		t.Fatalf("reservation queries during sort = %d, want one", got)
	}
	for index := 1; index < len(pending); index++ {
		if pending[index-1].ID >= pending[index].ID {
			t.Fatalf("sort order is not deterministic at %d: %q >= %q", index, pending[index-1].ID, pending[index].ID)
		}
	}
}

func TestPendingMetadataRefreshSharesReservationEvidenceAcrossQueue(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	now := time.Now().UTC()
	for index := 0; index < 40; index++ {
		id := string(rune('a' + index))
		runner.tests[id] = &ManagedTest{
			ID: id, Status: "PENDING", ProjectID: storage.DefaultProjectID,
			CreatedAt: now, PendingSince: cloneTimePtr(&now), TotalVUsers: 1,
		}
	}
	store.reservationCalls.Store(0)
	runner.refreshPendingQueueMetadata()
	if got := store.reservationCalls.Load(); got != 1 {
		t.Fatalf("reservation queries during queue refresh = %d, want one shared lookup", got)
	}
}

func TestPendingMetadataLoadsProjectQuotaEvidenceOutsideRunnerLockOncePerProject(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	now := time.Now().UTC()
	projects := []*storage.Project{
		{
			ID: "project-quota", Name: "Project quota", CreatedAt: now, UpdatedAt: now,
			Settings: storage.ProjectSettings{MaxConcurrentTests: 1},
		},
		{
			ID: "pool-quota", Name: "Pool quota", CreatedAt: now, UpdatedAt: now,
			Settings: storage.ProjectSettings{PoolQuotas: map[string]storage.PoolProjectQuota{
				"pool-b": {MaxConcurrentTests: 1},
			}},
		},
	}
	for _, project := range projects {
		if err := store.Store.SaveProject(project); err != nil {
			t.Fatalf("SaveProject(%s): %v", project.ID, err)
		}
	}
	poolAssignment := &GeneratorAssignmentSettings{PoolID: "pool-b"}
	runner.mu.Lock()
	runner.tests["active-project"] = &ManagedTest{
		ID: "active-project", Status: "RUNNING", ProjectID: "project-quota", CreatedAt: now, TotalVUsers: 1,
	}
	runner.tests["pending-project"] = &ManagedTest{
		ID: "pending-project", Status: "PENDING", ProjectID: "project-quota", CreatedAt: now, PendingSince: cloneTimePtr(&now), TotalVUsers: 1,
	}
	runner.tests["active-pool"] = &ManagedTest{
		ID: "active-pool", Status: "RUNNING", ProjectID: "pool-quota", CreatedAt: now, TotalVUsers: 1, Assignment: poolAssignment,
	}
	runner.tests["pending-pool"] = &ManagedTest{
		ID: "pending-pool", Status: "PENDING", ProjectID: "pool-quota", CreatedAt: now, PendingSince: cloneTimePtr(&now), TotalVUsers: 1, Assignment: poolAssignment,
	}
	runner.mu.Unlock()

	store.projectCalls.Store(0)
	store.projectReadWhileRunnerLocked.Store(false)
	runner.refreshPendingQueueMetadata()

	if store.projectReadWhileRunnerLocked.Load() {
		t.Fatal("pending metadata refresh read project settings while runner.mu was held")
	}
	if got := store.projectCalls.Load(); got != 2 {
		t.Fatalf("project reads during queue refresh = %d, want one per distinct pending project", got)
	}
	if got := runner.tests["pending-project"].PendingReason; got != "waiting for a project test slot (1 max concurrent tests)" {
		t.Fatalf("project quota pending reason = %q", got)
	}
	if got := runner.tests["pending-pool"].PendingReason; got != "waiting for a pool test slot on \"pool-b\" (1 max concurrent tests)" {
		t.Fatalf("pool quota pending reason = %q", got)
	}

	store.projectErr = errors.New("project store unavailable")
	store.projectCalls.Store(0)
	store.projectReadWhileRunnerLocked.Store(false)
	runner.refreshPendingQueueMetadata()
	if store.projectReadWhileRunnerLocked.Load() {
		t.Fatal("failed project settings read occurred while runner.mu was held")
	}
	if got := store.projectCalls.Load(); got != 2 {
		t.Fatalf("failed project reads during queue refresh = %d, want one per distinct pending project", got)
	}
	for _, id := range []string{"pending-project", "pending-pool"} {
		if got := runner.tests[id].PendingReason; got != "waiting for project quota evidence to become available" {
			t.Fatalf("%s pending reason after project-store failure = %q", id, got)
		}
	}
}

func TestPreflightFailsClosedWhenProjectQuotaEvidenceIsUnavailable(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	store.projectErr = errors.New("project store unavailable")
	store.projectCalls.Store(0)
	store.projectReadWhileRunnerLocked.Store(false)

	response, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name: "quota evidence unavailable", Duration: "1m", ScriptID: "storage-evidence-script", VUsers: 1,
	}})
	if err != nil {
		t.Fatalf("PreflightLaunch: %v", err)
	}
	if response == nil || response.Decision != "blocked" || response.StartNowAllowed || response.QueueAllowed {
		t.Fatalf("preflight response = %#v, want fail-closed blocked decision", response)
	}
	found := false
	for _, conflict := range response.BlockingConflicts {
		if conflict.Code == "project_quota_evidence_unavailable" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("blocking conflicts = %#v, want project_quota_evidence_unavailable", response.BlockingConflicts)
	}
	if store.projectReadWhileRunnerLocked.Load() {
		t.Fatal("preflight read project settings while runner.mu was held")
	}
}

func TestCreateTestLoadsReleaseAndApplicationEvidenceOutsideRunnerLock(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	now := time.Now().UTC()
	if err := store.SaveApplication(&storage.Application{
		ID: "create-evidence-app", ProjectID: storage.DefaultProjectID, Name: "Create Evidence", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	if err := store.SaveRelease(&storage.Release{
		ID: "create-evidence-release", ApplicationID: "create-evidence-app", ProjectID: storage.DefaultProjectID,
		Name: "Create Evidence", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	store.releaseCalls.Store(0)
	store.applicationCalls.Store(0)
	store.releaseReadWhileRunnerLocked.Store(false)
	store.applicationReadWhileLocked.Store(false)

	created, err := runner.CreateTest(CreateTestRequest{
		Name: "release evidence", Duration: "1m", ScriptID: "storage-evidence-script", VUsers: 1,
		ProjectID: storage.DefaultProjectID, ApplicationID: "create-evidence-app", ReleaseID: "create-evidence-release",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if created.ApplicationID != "create-evidence-app" || created.ReleaseID != "create-evidence-release" {
		t.Fatalf("created release context = %q/%q", created.ApplicationID, created.ReleaseID)
	}
	if store.releaseReadWhileRunnerLocked.Load() || store.applicationReadWhileLocked.Load() {
		t.Fatal("test creation read release/application evidence while runner.mu was held")
	}
	if got := store.releaseCalls.Load(); got != 1 {
		t.Fatalf("release reads during create = %d, want one", got)
	}
	if got := store.applicationCalls.Load(); got != 1 {
		t.Fatalf("application reads during create = %d, want one", got)
	}

	store.releaseErr = errors.New("release store unavailable")
	store.releaseReadWhileRunnerLocked.Store(false)
	if _, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{
		Name: "release unavailable", Duration: "1m", ScriptID: "storage-evidence-script", VUsers: 1,
		ProjectID: storage.DefaultProjectID, ApplicationID: "create-evidence-app", ReleaseID: "create-evidence-release",
	}}); err == nil || !strings.Contains(err.Error(), "release not found") {
		t.Fatalf("PreflightLaunch release-store error = %v", err)
	}
	if store.releaseReadWhileRunnerLocked.Load() {
		t.Fatal("failed release evidence lookup occurred while runner.mu was held")
	}
}

func TestScenarioScheduleLoadsProjectAndReleaseEvidenceOutsideRunnerLock(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "schedule evidence", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: "storage-evidence-script", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	now := time.Now().UTC()
	if err := store.SaveApplication(&storage.Application{
		ID: "schedule-evidence-app", ProjectID: scenario.ProjectID, Name: "Schedule Evidence", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	if err := store.SaveRelease(&storage.Release{
		ID: "schedule-evidence-release", ApplicationID: "schedule-evidence-app", ProjectID: scenario.ProjectID,
		Name: "Schedule Evidence", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	store.projectCalls.Store(0)
	store.releaseCalls.Store(0)
	store.projectReadWhileRunnerLocked.Store(false)
	store.releaseReadWhileRunnerLocked.Store(false)
	enabled := true
	schedule, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
		Name: "schedule evidence", CronExpr: "0 * * * *", Timezone: "UTC", Enabled: &enabled,
		ReleaseID: "schedule-evidence-release",
	})
	if err != nil {
		t.Fatalf("CreateScenarioSchedule: %v", err)
	}
	if schedule == nil || schedule.ReleaseID != "schedule-evidence-release" {
		t.Fatalf("schedule = %#v, want release identity preserved from immutable evidence", schedule)
	}
	if store.projectReadWhileRunnerLocked.Load() || store.releaseReadWhileRunnerLocked.Load() {
		t.Fatal("scenario schedule read project or release evidence while runner.mu was held")
	}
	if got := store.projectCalls.Load(); got != 1 {
		t.Fatalf("project reads during schedule create = %d, want one", got)
	}
	if got := store.releaseCalls.Load(); got != 1 {
		t.Fatalf("release reads during schedule create = %d, want one", got)
	}
	store.projectErr = errors.New("project store unavailable")
	store.projectReadWhileRunnerLocked.Store(false)
	if _, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
		Name: "schedule blocked", CronExpr: "5 * * * *", Timezone: "UTC", Enabled: &enabled,
	}); err == nil || !strings.Contains(err.Error(), "quota evidence") {
		t.Fatalf("CreateScenarioSchedule with unavailable project store error = %v", err)
	}
	if store.projectReadWhileRunnerLocked.Load() {
		t.Fatal("failed schedule quota read occurred while runner.mu was held")
	}
}

func TestScenarioScheduleCRUDAndApprovalStorageRunsOutsideRunnerLock(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "schedule CRUD lock evidence", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: "storage-evidence-script", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	store.scheduleIOCalls.Store(0)
	store.scheduleIOWhileRunnerLocked.Store(false)
	enabled := true
	schedule, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
		Name: "manual schedule", CronExpr: "0 * * * *", Timezone: "UTC", Enabled: &enabled,
		ApprovalPolicy: "manual",
	})
	if err != nil {
		t.Fatalf("CreateScenarioSchedule: %v", err)
	}
	if schedule.PendingApprovalID == "" {
		t.Fatalf("manual schedule = %#v, want a pending approval", schedule)
	}
	if schedules, ok := runner.ListScenarioSchedules(scenario.ID); !ok || len(schedules) != 1 {
		t.Fatalf("ListScenarioSchedules = %#v, %t", schedules, ok)
	}
	approvals, ok := runner.ListScenarioScheduleApprovals(scenario.ID, schedule.ID)
	if !ok || len(approvals) != 1 || approvals[0].ID != schedule.PendingApprovalID {
		t.Fatalf("ListScenarioScheduleApprovals = %#v, %t", approvals, ok)
	}
	_, resolved, found, err := runner.ResolveScenarioScheduleApproval(
		scenario.ID, schedule.ID, schedule.PendingApprovalID, true, "reviewer", "approved",
	)
	if err != nil || !found || resolved == nil || resolved.PendingApprovalID != "" {
		t.Fatalf("ResolveScenarioScheduleApproval = %#v, %t, %v", resolved, found, err)
	}
	updated, found, err := runner.UpdateScenarioSchedule(scenario.ID, schedule.ID, CreateScenarioScheduleRequest{
		Name: "updated schedule", CronExpr: "5 * * * *", Timezone: "UTC", Enabled: &enabled,
	})
	if err != nil || !found || updated == nil {
		t.Fatalf("UpdateScenarioSchedule = %#v, %t, %v", updated, found, err)
	}
	plannedAt := time.Now().UTC().Add(-time.Minute)
	occurrence := &storage.ScenarioScheduleOccurrence{
		ID: "schedule-crud-occurrence", ScheduleID: updated.ID, ScenarioID: scenario.ID,
		ProjectID: scenario.ProjectID, ScheduleRevision: updated.Revision, PlannedAt: plannedAt,
		Status: "blocked", ReasonCode: "test", CreatedAt: plannedAt, UpdatedAt: plannedAt,
	}
	if err := store.Store.SaveScenarioScheduleOccurrence(occurrence); err != nil {
		t.Fatalf("SaveScenarioScheduleOccurrence: %v", err)
	}
	if occurrences, ok := runner.ListScenarioScheduleOccurrences(scenario.ID, updated.ID); !ok || len(occurrences) != 1 {
		t.Fatalf("ListScenarioScheduleOccurrences = %#v, %t", occurrences, ok)
	}
	if deleted, err := runner.DeleteScenarioScheduleOccurrence(scenario.ID, updated.ID, occurrence.ID); err != nil || !deleted {
		t.Fatalf("DeleteScenarioScheduleOccurrence = %t, %v", deleted, err)
	}
	if deleted, err := runner.DeleteScenarioSchedule(scenario.ID, updated.ID); err != nil || !deleted {
		t.Fatalf("DeleteScenarioSchedule = %t, %v", deleted, err)
	}
	if store.scheduleIOWhileRunnerLocked.Load() {
		t.Fatal("scenario schedule CRUD or approval persistence ran while runner.mu was held")
	}
	if got := store.scheduleIOCalls.Load(); got < 9 {
		t.Fatalf("observed schedule storage calls = %d, want CRUD, history, and approval paths exercised", got)
	}
}

func TestRuntimeAdmissionFailsClosedOnUnlockedProjectQuotaReadFailure(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	now := time.Now().UTC()
	startedAt := now.Add(-time.Minute)
	runner.mu.Lock()
	runner.tests["runtime-quota-evidence"] = &ManagedTest{
		ID: "runtime-quota-evidence", Name: "runtime quota evidence", Status: "RUNNING",
		ProjectID: storage.DefaultProjectID, CreatedAt: startedAt, StartedAt: &startedAt,
		Duration: 5 * time.Minute, DurationStr: "5m", TotalVUsers: 1,
		Groups: []TestGroup{{Name: "buyers", VUsers: 1}},
	}
	runner.mu.Unlock()
	store.projectErr = errors.New("project store unavailable")
	store.projectCalls.Store(0)
	store.projectReadWhileRunnerLocked.Store(false)

	if err := runner.ScaleVUsers("runtime-quota-evidence", "buyers", 2); err == nil || !strings.Contains(err.Error(), "project quota evidence") {
		t.Fatalf("ScaleVUsers error = %v, want unavailable project quota evidence", err)
	}
	if err := runner.AdjustDuration("runtime-quota-evidence", 6*time.Minute); err == nil || !strings.Contains(err.Error(), "project quota evidence") {
		t.Fatalf("AdjustDuration error = %v, want unavailable project quota evidence", err)
	}
	runner.mu.Lock()
	runner.tests["runtime-quota-evidence"].Status = "PAUSED"
	pausedAt := now
	runner.tests["runtime-quota-evidence"].PausedAt = &pausedAt
	runner.mu.Unlock()
	if err := runner.ResumeTest("runtime-quota-evidence"); err == nil || !strings.Contains(err.Error(), "project quota evidence") {
		t.Fatalf("ResumeTest error = %v, want unavailable project quota evidence", err)
	}
	if store.projectReadWhileRunnerLocked.Load() {
		t.Fatal("runtime admission read project quota evidence while runner.mu was held")
	}
	if got := store.projectCalls.Load(); got != 3 {
		t.Fatalf("runtime admission project reads = %d, want one per operation", got)
	}
}

func TestPendingMetadataLoadsReservationEvidenceOutsideRunnerLock(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	now := time.Now().UTC()
	runner.mu.Lock()
	runner.tests["pending-metadata-evidence"] = &ManagedTest{
		ID: "pending-metadata-evidence", Status: "PENDING", ProjectID: storage.DefaultProjectID,
		CreatedAt: now, PendingSince: cloneTimePtr(&now), TotalVUsers: 1,
	}
	runner.mu.Unlock()
	store.blockReservations.Store(true)
	done := make(chan struct{})
	go func() {
		runner.refreshPendingQueueMetadata()
		close(done)
	}()
	select {
	case <-store.reservationEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("pending metadata refresh did not reach reservation evidence query")
	}
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner lock was held while pending metadata loaded reservations")
	}
	close(store.releaseReservation)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pending metadata refresh did not finish")
	}
}

func TestPendingPromotionLoadsReservationEvidenceOutsideRunnerLock(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	now := time.Now().UTC()
	runner.mu.Lock()
	runner.tests["pending-evidence"] = &ManagedTest{
		ID: "pending-evidence", Status: "PENDING", ProjectID: storage.DefaultProjectID,
		CreatedAt: now, PendingSince: cloneTimePtr(&now), TotalVUsers: 1,
	}
	runner.mu.Unlock()
	leaseTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	store.blockReservations.Store(true)
	done := make(chan error, 1)
	go func() { done <- runner.promotePendingTestsWithTerm(leaseTerm) }()
	select {
	case <-store.reservationEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("pending promotion did not reach reservation evidence query")
	}
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		runner.shuttingDown = true
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner lock was held while pending promotion loaded reservations")
	}
	close(store.releaseReservation)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("promotePendingTestsWithTerm: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending promotion did not finish")
	}
}

func TestPendingPromotionFailsClosedOnUnlockedProjectQuotaReadFailure(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	now := time.Now().UTC()
	runner.mu.Lock()
	runner.tests["pending-project-evidence"] = &ManagedTest{
		ID: "pending-project-evidence", Status: "PENDING", ProjectID: storage.DefaultProjectID,
		CreatedAt: now, PendingSince: cloneTimePtr(&now), TotalVUsers: 1,
	}
	runner.mu.Unlock()
	leaseTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	store.projectErr = errors.New("project store unavailable")
	store.projectCalls.Store(0)
	store.projectReadWhileRunnerLocked.Store(false)
	err = runner.promotePendingTestsWithTerm(leaseTerm)
	if !errors.Is(err, errStartAdmissionEvidenceStale) || !errors.Is(err, store.projectErr) {
		t.Fatalf("promotePendingTestsWithTerm error = %v, want retryable project-evidence failure", err)
	}
	if got := runner.tests["pending-project-evidence"].Status; got != "PENDING" {
		t.Fatalf("pending test status = %q, want PENDING while quota evidence is unavailable", got)
	}
	if store.projectReadWhileRunnerLocked.Load() {
		t.Fatal("pending promotion read project quota evidence while runner.mu was held")
	}
	if got := store.projectCalls.Load(); got != 1 {
		t.Fatalf("pending promotion project reads = %d, want one", got)
	}
}

func TestScheduleQuotaEvidenceRevalidatesAfterUnlockedLookup(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	leaseTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	store.blockReservations.Store(true)
	done := make(chan error, 1)
	go func() {
		runner.mu.Lock()
		_, _, _, _, err := runner.loadScheduleQuotaEvidenceForSweepLocked(leaseTerm, nil)
		runner.mu.Unlock()
		done <- err
	}()
	select {
	case <-store.reservationEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("schedule conflict evaluation did not reach reservation evidence query")
	}
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		runner.contentRevision++
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner lock was held while schedule conflict evaluation loaded reservations")
	}
	close(store.releaseReservation)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "content changed") {
			t.Fatalf("schedule evidence revalidation error = %v, want content-change retry", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("schedule conflict evaluation did not finish")
	}
}

type scheduleHealthCountStore struct {
	storage.Store
	countCalls atomic.Int32
	listCalls  atomic.Int32
}

func (s *scheduleHealthCountStore) CountScenarioScheduleOccurrencesByStatus() (map[string]int, error) {
	s.countCalls.Add(1)
	return map[string]int{"queued": 3, "materialized": 5, "completed": 8}, nil
}

func (s *scheduleHealthCountStore) ListAllScenarioScheduleOccurrences() ([]*storage.ScenarioScheduleOccurrence, error) {
	s.listCalls.Add(1)
	return s.Store.ListAllScenarioScheduleOccurrences()
}

func TestScenarioScheduleHealthUsesAggregatedOccurrenceCounts(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &scheduleHealthCountStore{Store: base}
	runner := NewTestRunner(1, NewHub(), store)
	store.countCalls.Store(0)
	store.listCalls.Store(0)
	health := runner.ScenarioScheduleHealth(time.Now().UTC())
	if health.Queued != 3 || health.Materialized != 5 || health.Completed != 8 {
		t.Fatalf("health counts = %#v", health)
	}
	if store.countCalls.Load() != 1 || store.listCalls.Load() != 0 {
		t.Fatalf("count/list calls = %d/%d, want 1/0", store.countCalls.Load(), store.listCalls.Load())
	}
}
