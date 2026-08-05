package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScenarioScheduleOccurrenceProjectionUnchangedForTerminalBlocks(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name              string
		status            string
		reason            string
		action            string
		pendingApproval   string
		occurrenceApprove string
	}{
		{name: "schedule conflict", status: "blocked", reason: "schedule_conflict", action: "resolve_conflict"},
		{name: "environment unavailable", status: "blocked", reason: "environment_unavailable", action: "resolve_conflict"},
		{name: "timeslot unavailable", status: "blocked", reason: "timeslot_unavailable", action: "resolve_conflict"},
		{name: "missed run", status: "blocked", reason: "missed_run_requires_decision", action: "operator_decision_required"},
		{name: "approval required", status: "blocked", reason: "approval_required", action: "approval_required", pendingApproval: "approval-1", occurrenceApprove: "approval-1"},
		{name: "approval rejected", status: "blocked", reason: "approval_rejected", action: "approval_rejected"},
		{name: "failed occurrence", status: "failed", reason: "materialization_failed", action: "operator_decision_required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			occurrence := &storage.ScenarioScheduleOccurrence{
				ID: "occurrence-1", Revision: 7, Status: tc.status, ReasonCode: tc.reason,
				ConflictSummary: "durable conflict", ApprovalID: tc.occurrenceApprove, UpdatedAt: now,
			}
			schedule := &storage.ScenarioSchedule{
				LastOccurrence: cloneScenarioScheduleOccurrence(occurrence), LastError: occurrence.ConflictSummary,
				NextRequiredAction: tc.action, PendingApprovalID: tc.pendingApproval,
			}
			if !scenarioScheduleOccurrenceProjectionUnchanged(schedule, occurrence, tc.status) {
				t.Fatalf("unchanged projection was not recognized: schedule=%#v occurrence=%#v", schedule, occurrence)
			}
			schedule.LastError = "different"
			if scenarioScheduleOccurrenceProjectionUnchanged(schedule, occurrence, tc.status) {
				t.Fatal("changed projection was incorrectly treated as idempotent")
			}
		})
	}
}

func TestMissedRunOperatorDecisionProjectionIsIdempotentAcrossSweeps(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "missed-run-idempotency.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	project := &storage.Project{ID: "missed-run-project", Name: "Missed Run", CreatedAt: base.Add(-time.Hour), UpdatedAt: base.Add(-time.Hour)}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	script, err := runner.StoreScriptDurable(&dsl.Script{
		ID: "missed-run-script", ProjectID: project.ID, Name: "Missed Run", Protocol: "http", Version: "1",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{
		Name: "Missed Run", Duration: "1m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plannedAt := base.Add(-2 * time.Minute)
	schedule := &storage.ScenarioSchedule{
		ID: "missed-run-schedule", ScenarioID: scenario.ID, ProjectID: project.ID, Revision: 1,
		Name: "Missed Run", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true,
		MissedRunPolicy: "require_operator_decision", NextRunAt: &plannedAt,
		CreatedAt: base.Add(-time.Hour), UpdatedAt: base.Add(-time.Hour),
	}
	runner.mu.Lock()
	err = runner.saveScenarioScheduleLocked(schedule)
	runner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	scheduler := newScenarioScheduler(runner)
	sweepAt := base
	scheduler.now = func() time.Time { return sweepAt }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatal(err)
	}
	firstSchedule, err := store.GetScenarioSchedule(schedule.ID)
	if err != nil || firstSchedule == nil {
		t.Fatalf("first schedule projection = %#v, err=%v", firstSchedule, err)
	}
	firstOccurrence, err := store.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil || firstOccurrence == nil {
		t.Fatalf("first missed occurrence = %#v, err=%v", firstOccurrence, err)
	}
	const summary = "Missed scheduled occurrence requires operator decision."
	if firstOccurrence.Status != "blocked" || firstOccurrence.ReasonCode != "missed_run_requires_decision" || firstOccurrence.ConflictSummary != summary {
		t.Fatalf("first missed occurrence projection = %#v", firstOccurrence)
	}
	if firstSchedule.LastError != summary || firstSchedule.NextRequiredAction != "operator_decision_required" {
		t.Fatalf("first schedule projection = %#v", firstSchedule)
	}

	sweepAt = base.Add(30 * time.Second)
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatal(err)
	}
	secondSchedule, err := store.GetScenarioSchedule(schedule.ID)
	if err != nil || secondSchedule == nil {
		t.Fatalf("second schedule projection = %#v, err=%v", secondSchedule, err)
	}
	secondOccurrence, err := store.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil || secondOccurrence == nil {
		t.Fatalf("second missed occurrence = %#v, err=%v", secondOccurrence, err)
	}
	if secondSchedule.Revision != firstSchedule.Revision || !secondSchedule.UpdatedAt.Equal(firstSchedule.UpdatedAt) {
		t.Fatalf("unchanged schedule was rewritten: first revision=%d updated=%s, second revision=%d updated=%s", firstSchedule.Revision, firstSchedule.UpdatedAt, secondSchedule.Revision, secondSchedule.UpdatedAt)
	}
	if secondOccurrence.Revision != firstOccurrence.Revision || !secondOccurrence.UpdatedAt.Equal(firstOccurrence.UpdatedAt) {
		t.Fatalf("unchanged occurrence was rewritten: first revision=%d updated=%s, second revision=%d updated=%s", firstOccurrence.Revision, firstOccurrence.UpdatedAt, secondOccurrence.Revision, secondOccurrence.UpdatedAt)
	}
}

func TestNextCronRunDoesNotRepeatFallbackWallClockMinute(t *testing.T) {
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	allows := func(time.Time) (bool, error) { return true, nil }
	first, err := nextCronRun("30 1 * * *", "America/Chicago", time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC), allows)
	if err != nil || first == nil {
		t.Fatalf("first occurrence = %v, err=%v", first, err)
	}
	second, err := nextCronRun("30 1 * * *", "America/Chicago", *first, allows)
	if err != nil || second == nil {
		t.Fatalf("second occurrence = %v, err=%v", second, err)
	}
	firstLocal, secondLocal := first.In(location), second.In(location)
	if sameLocalCalendarMinute(firstLocal, secondLocal) {
		t.Fatalf("fall-back wall-clock minute repeated: first=%s second=%s", firstLocal, secondLocal)
	}
	want := time.Date(2026, time.November, 2, 7, 30, 0, 0, time.UTC)
	if !second.Equal(want) {
		t.Fatalf("second occurrence = %s, want next day's 01:30 at %s", second, want)
	}

	// Reconciliation commonly advances from wall-clock now rather than the exact
	// planned instant. Even after 01:30 CDT has passed, 01:30 CST is not a new
	// daily occurrence.
	delayedFrom := time.Date(2026, time.November, 1, 6, 35, 0, 0, time.UTC)
	delayedNext, err := nextCronRun("30 1 * * *", "America/Chicago", delayedFrom, allows)
	if err != nil || delayedNext == nil || !delayedNext.Equal(want) {
		t.Fatalf("delayed fall-back advance = %v, err=%v, want %s", delayedNext, err, want)
	}

	// A schedule with several matching minutes in the repeated hour must not
	// replay the already-observed earlier wall-clock minutes either.
	multiMinuteFrom := time.Date(2026, time.November, 1, 6, 45, 0, 0, time.UTC)
	multiMinuteNext, err := nextCronRun("0,15,30,45 1 * * *", "America/Chicago", multiMinuteFrom, allows)
	multiMinuteWant := time.Date(2026, time.November, 2, 7, 0, 0, 0, time.UTC)
	if err != nil || multiMinuteNext == nil || !multiMinuteNext.Equal(multiMinuteWant) {
		t.Fatalf("multi-minute fall-back advance = %v, err=%v, want %s", multiMinuteNext, err, multiMinuteWant)
	}

	// A restart can first evaluate the schedule from inside the second repeated
	// hour. There is no first-fold previousLocal marker in that process, but the
	// 01:15 CST candidate must still be rejected because 01:15 CDT was the day's
	// selected wall-clock occurrence.
	secondFoldFrom := time.Date(2026, time.November, 1, 7, 10, 0, 0, time.UTC)
	secondFoldNext, err := nextCronRun("15 1 * * *", "America/Chicago", secondFoldFrom, allows)
	secondFoldWant := time.Date(2026, time.November, 2, 7, 15, 0, 0, time.UTC)
	if err != nil || secondFoldNext == nil || !secondFoldNext.Equal(secondFoldWant) {
		t.Fatalf("second-fold restart advance = %v, err=%v, want %s", secondFoldNext, err, secondFoldWant)
	}
}
