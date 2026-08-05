package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRestartRecoversExactQueuedScheduledStartWithoutReplayingShakeout(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "scheduled-restart.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var shakeoutExecutions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "scheduled-restart-recovery", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "scheduled-restart-recovery", execute: func(context.Context) error {
			shakeoutExecutions.Add(1)
			return nil
		}}
	})
	bootstrap := NewTestRunner(2, NewHub(), store)
	script, err := bootstrap.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-restart-recovery-script", Name: "Scheduled restart recovery", Version: "1", Protocol: protocolName,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "one shot", Type: "request", Method: http.MethodGet, Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := bootstrap.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled restart recovery", Duration: "1m", ScriptShakeout: true,
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := bootstrap.CreateTest(CreateTestRequest{
		Name: "Scheduled restart recovery", ProjectID: scenario.ProjectID, ScenarioID: scenario.ID,
		ScriptID: script.ID, VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := bootstrap.scriptShakeoutExecutableFingerprint(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bootstrap.mu.Lock()
	managed := bootstrap.tests[created.ID]
	managed.Status = "QUEUED"
	managed.StartAuthorization = &QueuedStartAuthorization{ScriptShakeoutPass: &ScriptShakeoutPassEvidence{
		PassedAt: now.Add(-time.Minute), EvidenceSHA256: fingerprint.EvidenceSHA256, ReusableAfterRestart: true,
	}}
	record := buildManagedTestRecord(managed)
	bootstrap.mu.Unlock()
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}

	const scheduleID = "scheduled-restart-recovery-schedule"
	nextRun := now.Add(time.Hour)
	if err := store.SaveScenarioSchedule(&storage.ScenarioSchedule{
		ID: scheduleID, ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Revision: 1,
		Name: "Scheduled restart recovery", CronExpr: "0 0 * * *", Timezone: "UTC", Enabled: true,
		MissedRunPolicy: "catch_up_once", LastRunTestID: created.ID, NextRunAt: &nextRun,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
		ID: "scheduled-restart-recovery-occurrence", ScheduleID: scheduleID,
		ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, ScheduleRevision: 1,
		PlannedAt: now.Add(-time.Minute), Status: "queued", ReasonCode: "start_retrying", TestID: created.ID,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	identity, ok := queuedStartRecoveryIdentityForTest(managed)
	if !ok {
		t.Fatal("scheduled test identity was not capturable")
	}
	bootstrap.mu.Lock()
	bootstrap.shuttingDown = true
	bootstrap.mu.Unlock()
	if err := bootstrap.recoverQueuedStartForIdentityWithTerm(created.ID, identity, executionLeaseTerm{}); !errors.Is(err, errRunnerShuttingDown) {
		t.Fatalf("shutdown recovery error = %v, want errRunnerShuttingDown", err)
	}
	stillQueued, err := store.GetScenarioScheduleOccurrenceByTestID(created.ID)
	if err != nil || stillQueued == nil || stillQueued.Status != "queued" || stillQueued.ReasonCode != "start_retrying" {
		t.Fatalf("shutdown recovery terminalized occurrence: (%#v, %v)", stillQueued, err)
	}

	backend := &executionBackendSpy{called: make(chan string, 1)}
	recovered := NewTestRunner(2, NewHub(), store, WithExecutionBackend(backend))
	select {
	case startedID := <-backend.called:
		if startedID != created.ID {
			t.Fatalf("recovered backend started %q, want %q", startedID, created.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restart did not re-enqueue the exact queued/start_retrying occurrence")
	}
	if got := shakeoutExecutions.Load(); got != 0 {
		t.Fatalf("restart replayed one-shot shakeout %d times", got)
	}
	durable, err := store.GetTest(created.ID)
	if err != nil || durable.Status != "INITIALIZING" {
		t.Fatalf("recovered durable test = (%#v, %v), want INITIALIZING", durable, err)
	}
	occurrence, err := store.GetScenarioScheduleOccurrenceByTestID(created.ID)
	if err != nil || occurrence == nil || occurrence.Status != "materialized" || occurrence.ReasonCode != "started" {
		t.Fatalf("recovered occurrence = (%#v, %v), want materialized/started", occurrence, err)
	}
	if local, ok := recovered.GetTest(created.ID); !ok || local.Status != "INITIALIZING" {
		t.Fatalf("recovered local test = %#v, want INITIALIZING", local)
	}
}

func TestRecurringScheduleSweepRearmsExactQueuedStartRetry(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "scheduled-sweep.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	runner := NewTestRunner(2, NewHub(), store)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "scheduled-sweep-recovery-script", Name: "Scheduled sweep recovery", Version: "1", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: http.MethodGet, Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Scheduled sweep recovery", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "Scheduled sweep recovery", ProjectID: scenario.ProjectID, ScenarioID: scenario.ID,
		ScriptID: script.ID, VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	runner.mu.Lock()
	managed := runner.tests[created.ID]
	managed.Status = "QUEUED"
	record := buildManagedTestRecord(managed)
	runner.mu.Unlock()
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}

	const scheduleID = "scheduled-sweep-recovery-schedule"
	schedule := &storage.ScenarioSchedule{
		ID: scheduleID, ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Revision: 1,
		Name: "Scheduled sweep recovery", CronExpr: "*/5 * * * *", Timezone: "UTC", Enabled: true,
		MissedRunPolicy: "catch_up_once", LastRunTestID: created.ID, NextRunAt: &now,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}
	if err := store.SaveScenarioSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.scenarios[scenario.ID].Schedules = append(runner.scenarios[scenario.ID].Schedules, cloneScenarioSchedule(schedule))
	runner.mu.Unlock()
	if err := store.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
		ID: "scheduled-sweep-recovery-occurrence", ScheduleID: scheduleID,
		ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, ScheduleRevision: 1,
		PlannedAt: now, Status: "queued", ReasonCode: "start_retrying", TestID: created.ID,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner.setExecutionBackend(backend)
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatal(err)
	}
	select {
	case startedID := <-backend.called:
		if startedID != created.ID {
			t.Fatalf("sweep recovery started %q, want %q", startedID, created.ID)
		}
	case <-time.After(5 * time.Second):
		local, _ := runner.GetTest(created.ID)
		occurrence, occurrenceErr := store.GetScenarioScheduleOccurrenceByTestID(created.ID)
		durableSchedule, scheduleErr := store.GetScenarioSchedule(scheduleID)
		runner.mu.RLock()
		_, retryQueued := runner.ciRecoveryRetrying[created.ID]
		runner.mu.RUnlock()
		t.Fatalf("same-occurrence sweep did not re-arm queued start recovery: test=%#v occurrence=(%#v, %v) schedule=(%#v, %v) retry_queued=%t", local, occurrence, occurrenceErr, durableSchedule, scheduleErr, retryQueued)
	}
	occurrence, err := store.GetScenarioScheduleOccurrenceByTestID(created.ID)
	if err != nil || occurrence == nil || occurrence.Status != "materialized" || occurrence.ReasonCode != "started" {
		t.Fatalf("recovered occurrence = (%#v, %v), want materialized/started", occurrence, err)
	}
}
