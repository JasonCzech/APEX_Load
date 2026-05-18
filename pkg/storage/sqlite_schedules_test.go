package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStorePersistsScenarioSchedules(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	scenario := &Scenario{ID: "scenario-1", Name: "Checkout", Duration: "5m", StartDelay: "30s", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveScenario(scenario, nil); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}
	nextRun := time.Date(2026, time.May, 17, 1, 0, 0, 0, time.UTC)
	schedule := &ScenarioSchedule{
		ID:               "schedule-1",
		ScenarioID:       scenario.ID,
		ProjectID:        "checkout",
		Name:             "Nightly",
		CronExpr:         "0 1 * * *",
		Timezone:         "UTC",
		RunNameTemplate:  "Nightly Checkout",
		Enabled:          true,
		OwnerSubject:     "user:qa-lead",
		OwnerDisplayName: "QA Lead",
		Governance: &ScenarioScheduleGovernance{
			PolicyVersion: "schedule-governance.v1",
			RBACReview: ScenarioScheduleRBACReview{
				Decision:      "allowed",
				ReviewedAt:    time.Date(2026, time.May, 17, 0, 30, 0, 0, time.UTC),
				ReviewedBy:    "user:qa-lead",
				RequiredRoles: []string{"editor", "project_admin"},
				ReviewSource:  "api",
			},
			QuotaReview: ScenarioScheduleQuotaReview{
				Decision:            "allowed",
				ProjectMaxVUsers:    500,
				EstimatedPeakVUsers: 25,
				Findings:            []string{"Schedule mutation authorized for scenario project scope."},
			},
		},
		NextRunAt: &nextRun,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.SaveScenarioSchedule(schedule); err != nil {
		t.Fatalf("SaveScenarioSchedule returned error: %v", err)
	}

	roundTrip, err := store.GetScenarioSchedule(schedule.ID)
	if err != nil {
		t.Fatalf("GetScenarioSchedule returned error: %v", err)
	}
	if roundTrip == nil || roundTrip.CronExpr != schedule.CronExpr || roundTrip.NextRunAt == nil || !roundTrip.NextRunAt.Equal(nextRun) {
		t.Fatalf("round trip schedule = %#v, want cron %q next run %s", roundTrip, schedule.CronExpr, nextRun)
	}
	if roundTrip.ProjectID != "checkout" || roundTrip.OwnerSubject != "user:qa-lead" || roundTrip.Governance == nil || roundTrip.Governance.QuotaReview.ProjectMaxVUsers != 500 {
		t.Fatalf("round trip governance = %#v, want owner/project/quota metadata", roundTrip)
	}
	listed, err := store.ListScenarioSchedules(scenario.ID)
	if err != nil {
		t.Fatalf("ListScenarioSchedules returned error: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != schedule.ID {
		t.Fatalf("listed schedules = %#v, want one schedule %s", listed, schedule.ID)
	}
	all, err := store.ListAllScenarioSchedules()
	if err != nil {
		t.Fatalf("ListAllScenarioSchedules returned error: %v", err)
	}
	if len(all) != 1 || all[0].ScenarioID != scenario.ID {
		t.Fatalf("all schedules = %#v, want one schedule for %s", all, scenario.ID)
	}
	if err := store.DeleteScenarioSchedule(schedule.ID); err != nil {
		t.Fatalf("DeleteScenarioSchedule returned error: %v", err)
	}
	listed, err = store.ListScenarioSchedules(scenario.ID)
	if err != nil {
		t.Fatalf("ListScenarioSchedules after delete returned error: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("listed schedules after delete = %#v, want empty", listed)
	}
}
