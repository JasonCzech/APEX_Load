package api

import (
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// failingScheduleApprovalStore intentionally exposes only storage.Store's
// schedule contract, exercising the compatibility path where schedule and
// approval writes cannot be committed in one transaction.
type failingScheduleApprovalStore struct {
	storage.Store
	runner          *TestRunner
	ioCalls         atomic.Int32
	ioUnderLock     atomic.Bool
	approvalCalls   atomic.Int32
	approvalEntered chan struct{}
	releaseApproval chan struct{}
}

type nonAtomicScheduleApprovalStore struct{ storage.Store }

func (s *failingScheduleApprovalStore) observeScheduleIO() {
	s.ioCalls.Add(1)
	if s.runner == nil {
		return
	}
	if s.runner.mu.TryLock() {
		s.runner.mu.Unlock()
		return
	}
	s.ioUnderLock.Store(true)
}

func (s *failingScheduleApprovalStore) SaveScenarioSchedule(schedule *storage.ScenarioSchedule) error {
	s.observeScheduleIO()
	return s.Store.SaveScenarioSchedule(schedule)
}

func (s *failingScheduleApprovalStore) SaveScenarioScheduleApproval(*storage.ScenarioScheduleApproval) error {
	s.observeScheduleIO()
	if s.approvalCalls.Add(1) == 1 && s.approvalEntered != nil {
		close(s.approvalEntered)
		if s.releaseApproval != nil {
			<-s.releaseApproval
		}
	}
	return errors.New("approval persistence failed")
}

func (s *failingScheduleApprovalStore) CreateScenarioScheduleIfAbsent(schedule *storage.ScenarioSchedule) (bool, error) {
	s.observeScheduleIO()
	return s.Store.(interface {
		CreateScenarioScheduleIfAbsent(*storage.ScenarioSchedule) (bool, error)
	}).CreateScenarioScheduleIfAbsent(schedule)
}

func (s *failingScheduleApprovalStore) UpdateScenarioScheduleIfRevision(schedule *storage.ScenarioSchedule, expectedRevision int) (bool, error) {
	s.observeScheduleIO()
	return s.Store.(interface {
		UpdateScenarioScheduleIfRevision(*storage.ScenarioSchedule, int) (bool, error)
	}).UpdateScenarioScheduleIfRevision(schedule, expectedRevision)
}

func (s *failingScheduleApprovalStore) DeleteScenarioScheduleGuardedIfRevision(id string, expectedRevision int) (storage.ScheduleDeleteResult, error) {
	s.observeScheduleIO()
	return s.Store.(interface {
		DeleteScenarioScheduleGuardedIfRevision(string, int) (storage.ScheduleDeleteResult, error)
	}).DeleteScenarioScheduleGuardedIfRevision(id, expectedRevision)
}

func (s *failingScheduleApprovalStore) DeleteScenarioSchedule(id string) error {
	s.observeScheduleIO()
	return s.Store.DeleteScenarioSchedule(id)
}

func TestScenarioScheduleCreateCompensationDoesNotDeleteConcurrentUpdate(t *testing.T) {
	runner, store, scenario := newFailingScheduleApprovalRunner(t)
	store.approvalEntered = make(chan struct{})
	store.releaseApproval = make(chan struct{})
	enabled := true
	createErr := make(chan error, 1)
	go func() {
		_, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
			Name: "pending manual schedule", CronExpr: "0 * * * *", Timezone: "UTC",
			Enabled: &enabled, ApprovalPolicy: "manual",
		})
		createErr <- err
	}()
	<-store.approvalEntered
	schedules, ok := runner.ListScenarioSchedules(scenario.ID)
	if !ok || len(schedules) != 1 {
		t.Fatalf("schedule visible during approval write = %#v, %t", schedules, ok)
	}
	expected := schedules[0].Revision
	updated, found, err := runner.UpdateScenarioSchedule(scenario.ID, schedules[0].ID, CreateScenarioScheduleRequest{
		Name: "concurrent winning update", CronExpr: "5 * * * *", Timezone: "UTC",
		Enabled: &enabled, ApprovalPolicy: "auto", ExpectedRevision: &expected,
	})
	if err != nil || !found || updated == nil {
		t.Fatalf("concurrent UpdateScenarioSchedule = (%#v, %t, %v)", updated, found, err)
	}
	close(store.releaseApproval)
	err = <-createErr
	if !strings.Contains(errString(err), "approval persistence failed") || !errors.Is(err, errScheduleConcurrentChange) {
		t.Fatalf("CreateScenarioSchedule error = %v, want approval failure joined with concurrent-change fence", err)
	}
	assertOnlyScheduleNamed(t, runner, store.Store, scenario.ID, "concurrent winning update")
}

func TestScenarioScheduleUpdateCompensationDoesNotOverwriteConcurrentUpdate(t *testing.T) {
	runner, store, scenario := newFailingScheduleApprovalRunner(t)
	enabled := true
	initial, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
		Name: "initial schedule", CronExpr: "0 * * * *", Timezone: "UTC",
		Enabled: &enabled, ApprovalPolicy: "auto",
	})
	if err != nil {
		t.Fatalf("CreateScenarioSchedule: %v", err)
	}
	store.approvalEntered = make(chan struct{})
	store.releaseApproval = make(chan struct{})
	store.approvalCalls.Store(0)
	firstExpected := initial.Revision
	firstErr := make(chan error, 1)
	go func() {
		_, _, err := runner.UpdateScenarioSchedule(scenario.ID, initial.ID, CreateScenarioScheduleRequest{
			Name: "manual update", CronExpr: "5 * * * *", Timezone: "UTC",
			Enabled: &enabled, ApprovalPolicy: "manual", ExpectedRevision: &firstExpected,
		})
		firstErr <- err
	}()
	<-store.approvalEntered
	current, ok := runner.ListScenarioSchedules(scenario.ID)
	if !ok || len(current) != 1 {
		t.Fatalf("schedule visible during update approval = %#v, %t", current, ok)
	}
	secondExpected := current[0].Revision
	updated, found, err := runner.UpdateScenarioSchedule(scenario.ID, initial.ID, CreateScenarioScheduleRequest{
		Name: "newer winning update", CronExpr: "10 * * * *", Timezone: "UTC",
		Enabled: &enabled, ApprovalPolicy: "auto", ExpectedRevision: &secondExpected,
	})
	if err != nil || !found || updated == nil {
		t.Fatalf("second UpdateScenarioSchedule = (%#v, %t, %v)", updated, found, err)
	}
	close(store.releaseApproval)
	err = <-firstErr
	if !strings.Contains(errString(err), "approval persistence failed") || !errors.Is(err, errScheduleConcurrentChange) {
		t.Fatalf("first UpdateScenarioSchedule error = %v, want approval failure joined with concurrent-change fence", err)
	}
	assertOnlyScheduleNamed(t, runner, store.Store, scenario.ID, "newer winning update")
}

func newFailingScheduleApprovalRunner(t *testing.T) (*TestRunner, *failingScheduleApprovalStore, *StoredScenario) {
	t.Helper()
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &failingScheduleApprovalStore{Store: base}
	runner := NewTestRunner(10, NewHub(), store)
	store.runner = runner
	storePreflightScript(runner, "schedule-compensation-concurrent-script", storage.DefaultProjectID)
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "schedule compensation concurrent", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: "schedule-compensation-concurrent-script", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	return runner, store, scenario
}

func assertOnlyScheduleNamed(t *testing.T, runner *TestRunner, store storage.Store, scenarioID, wantName string) {
	t.Helper()
	live, ok := runner.ListScenarioSchedules(scenarioID)
	if !ok || len(live) != 1 || live[0].Name != wantName {
		t.Fatalf("live schedules = %#v, want one named %q", live, wantName)
	}
	durable, err := store.ListScenarioSchedules(scenarioID)
	if err != nil {
		t.Fatalf("ListScenarioSchedules: %v", err)
	}
	if len(durable) != 1 || durable[0].Name != wantName {
		t.Fatalf("durable schedules = %#v, want one named %q", durable, wantName)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestScenarioScheduleApprovalFailureCompensatesOutsideRunnerLock(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &failingScheduleApprovalStore{Store: base}
	runner := NewTestRunner(10, NewHub(), store)
	store.runner = runner
	storePreflightScript(runner, "schedule-compensation-script", storage.DefaultProjectID)
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "schedule compensation", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: "schedule-compensation-script", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	store.ioCalls.Store(0)
	store.ioUnderLock.Store(false)
	enabled := true
	if _, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
		Name: "manual schedule", CronExpr: "0 * * * *", Timezone: "UTC",
		Enabled: &enabled, ApprovalPolicy: "manual",
	}); err == nil || err.Error() != "approval persistence failed" {
		t.Fatalf("CreateScenarioSchedule error = %v, want approval persistence failure", err)
	}
	if store.ioUnderLock.Load() {
		t.Fatal("schedule approval failure or compensating delete ran while runner.mu was held")
	}
	if got := store.ioCalls.Load(); got != 3 {
		t.Fatalf("observed schedule storage calls = %d, want save, approval attempt, and compensating delete", got)
	}
	if schedules, ok := runner.ListScenarioSchedules(scenario.ID); !ok || len(schedules) != 0 {
		t.Fatalf("in-memory schedules after compensation = %#v, %t", schedules, ok)
	}
	durable, err := base.ListScenarioSchedules(scenario.ID)
	if err != nil {
		t.Fatalf("ListScenarioSchedules: %v", err)
	}
	if len(durable) != 0 {
		t.Fatalf("durable schedules after compensation = %#v", durable)
	}
}

func TestScenarioScheduleApprovalResolutionFailsBeforeSplitWrite(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })
	runner := NewTestRunner(10, NewHub(), &nonAtomicScheduleApprovalStore{Store: base})
	storePreflightScript(runner, "schedule-resolution-script", storage.DefaultProjectID)
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "schedule resolution", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: "schedule-resolution-script", VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	enabled := true
	schedule, err := runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{
		Name: "manual schedule", CronExpr: "0 * * * *", Timezone: "UTC",
		Enabled: &enabled, ApprovalPolicy: "manual",
	})
	if err != nil || schedule == nil || schedule.PendingApprovalID == "" {
		t.Fatalf("CreateScenarioSchedule = (%#v, %v), want pending approval", schedule, err)
	}
	_, _, found, err := runner.ResolveScenarioScheduleApproval(scenario.ID, schedule.ID, schedule.PendingApprovalID, true, "reviewer", "approved")
	if err == nil || !strings.Contains(err.Error(), "does not support atomic resolution") || found {
		t.Fatalf("ResolveScenarioScheduleApproval = (found=%t, err=%v), want fail-closed capability error", found, err)
	}
	approval, err := base.GetScenarioScheduleApproval(schedule.PendingApprovalID)
	if err != nil || approval == nil || approval.Status != "pending" {
		t.Fatalf("approval after rejected split resolution = (%#v, %v)", approval, err)
	}
	durable, err := base.GetScenarioSchedule(schedule.ID)
	if err != nil || durable == nil || durable.PendingApprovalID != schedule.PendingApprovalID {
		t.Fatalf("schedule after rejected split resolution = (%#v, %v)", durable, err)
	}
}
