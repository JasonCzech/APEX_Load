package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScheduleHistoryEndpointsArePaginated(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if err := store.SaveScenario(&storage.Scenario{ID: "api-history-scenario", Name: "History", Duration: "1m", CreatedAt: now, UpdatedAt: now}, nil); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	scenarioSchedule := &storage.ScenarioSchedule{
		ID: "api-history-scenario-schedule", ScenarioID: "api-history-scenario", ProjectID: storage.DefaultProjectID,
		Name: "History", CronExpr: "* * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveScenarioSchedule(scenarioSchedule); err != nil {
		t.Fatalf("SaveScenarioSchedule: %v", err)
	}
	plan := &storage.ExecutionPlan{ID: "api-history-plan", ProjectID: storage.DefaultProjectID, Name: "History", RunMode: "parallel", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	planSchedule := &storage.ExecutionPlanSchedule{
		ID: "api-history-plan-schedule", PlanID: plan.ID, ProjectID: storage.DefaultProjectID,
		Name: "History", CronExpr: "* * * * *", Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanSchedule(planSchedule); err != nil {
		t.Fatalf("SaveExecutionPlanSchedule: %v", err)
	}
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		if err := store.SaveScenarioScheduleOccurrence(&storage.ScenarioScheduleOccurrence{
			ID: fmt.Sprintf("api-history-scenario-occurrence-%d", i), ScheduleID: scenarioSchedule.ID,
			ScenarioID: scenarioSchedule.ScenarioID, ProjectID: storage.DefaultProjectID,
			PlannedAt: at, Status: "materialized", CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveScenarioScheduleApproval(&storage.ScenarioScheduleApproval{
			ID: fmt.Sprintf("api-history-scenario-approval-%d", i), ScheduleID: scenarioSchedule.ID,
			ScenarioID: scenarioSchedule.ScenarioID, ProjectID: storage.DefaultProjectID,
			Policy: "manual", Status: "approved", RequestedAt: at, CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveExecutionPlanScheduleOccurrence(&storage.ExecutionPlanScheduleOccurrence{
			ID: fmt.Sprintf("api-history-plan-occurrence-%d", i), ScheduleID: planSchedule.ID,
			PlanID: plan.ID, ProjectID: storage.DefaultProjectID,
			PlannedAt: at, Status: "materialized", CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveExecutionPlanApproval(&storage.ExecutionPlanApproval{
			ID: fmt.Sprintf("api-history-plan-approval-%d", i), PlanID: plan.ID,
			ProjectID: storage.DefaultProjectID, Policy: "manual", Status: "approved",
			RequestedAt: at, CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}

	server := NewServer(ServerConfig{Store: store})
	cases := []struct {
		path  string
		field string
		want  string
	}{
		{"/api/v1/scenarios/" + scenarioSchedule.ScenarioID + "/schedules/" + scenarioSchedule.ID + "/occurrences?limit=1&offset=1", "occurrences", "api-history-scenario-occurrence-1"},
		{"/api/v1/scenarios/" + scenarioSchedule.ScenarioID + "/schedules/" + scenarioSchedule.ID + "/approvals?limit=1&offset=1", "approvals", "api-history-scenario-approval-1"},
		{"/api/v1/execution-plans/" + plan.ID + "/schedules/" + planSchedule.ID + "/occurrences?limit=1&offset=1", "occurrences", "api-history-plan-occurrence-1"},
		{"/api/v1/execution-plans/" + plan.ID + "/approvals?limit=1&offset=1", "approvals", "api-history-plan-approval-1"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
			}
			var payload struct {
				Limit  int                        `json:"limit"`
				Offset int                        `json:"offset"`
				Items  map[string]json.RawMessage `json:"-"`
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(resp.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if err := json.Unmarshal(raw["limit"], &payload.Limit); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw["offset"], &payload.Offset); err != nil {
				t.Fatal(err)
			}
			var items []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw[tc.field], &items); err != nil {
				t.Fatalf("decode %s: %v", tc.field, err)
			}
			if payload.Limit != 1 || payload.Offset != 1 || len(items) != 1 || items[0].ID != tc.want {
				t.Fatalf("response = %s", resp.Body.String())
			}
		})
	}
}
