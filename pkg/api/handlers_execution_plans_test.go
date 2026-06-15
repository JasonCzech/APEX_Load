package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestExecutionPlanCRUD(t *testing.T) {
	server, _ := newTimeslotServer(t)

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing name", map[string]any{"project_id": "p", "entries": []any{}}},
		{"bad run_mode", map[string]any{"project_id": "p", "name": "x", "run_mode": "weird"}},
		{"entry missing scenario", map[string]any{"project_id": "p", "name": "x", "entries": []map[string]any{{"order": 0}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
		"project_id": "proj-a", "name": "nightly", "run_mode": "sequential",
		"entries": []map[string]any{{"order": 0, "scenario_id": "scn-1"}},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var plan storage.ExecutionPlan
	_ = json.Unmarshal(create.Body.Bytes(), &plan)
	if plan.ID == "" || plan.Status != "draft" || plan.RunMode != "sequential" {
		t.Fatalf("created plan = %+v", plan)
	}

	list := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans?project_id=proj-a", nil)
	var listResp struct {
		ExecutionPlans []storage.ExecutionPlan `json:"execution_plans"`
		Count          int                     `json:"count"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &listResp)
	if listResp.Count != 1 {
		t.Fatalf("list count = %d, want 1", listResp.Count)
	}

	upd := timeslotRequest(t, server, http.MethodPut, "/api/v1/execution-plans/"+plan.ID, map[string]any{
		"name": "nightly-v2", "run_mode": "parallel",
		"entries": []map[string]any{{"order": 0, "scenario_id": "scn-1"}, {"order": 1, "scenario_id": "scn-2"}},
	})
	if upd.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", upd.Code, upd.Body.String())
	}
	var updated storage.ExecutionPlan
	_ = json.Unmarshal(upd.Body.Bytes(), &updated)
	if updated.RunMode != "parallel" || len(updated.Entries) != 2 {
		t.Fatalf("update not applied: %+v", updated)
	}

	del := timeslotRequest(t, server, http.MethodDelete, "/api/v1/execution-plans/"+plan.ID, nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete status = %d", del.Code)
	}
	if rec := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans/"+plan.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", rec.Code)
	}
}

// TestExecutionPlanMaterializeOrchestration exercises the run orchestration:
// disabled entries are skipped, and a sequential plan halts after a failing
// entry (scenarios are absent, so test creation fails — that's the failure path
// the orchestration must report honestly).
func TestExecutionPlanMaterializeOrchestration(t *testing.T) {
	server, _ := newTimeslotServer(t)

	mkPlan := func(runMode string, entries []map[string]any) string {
		rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
			"project_id": "proj-a", "name": "plan", "run_mode": runMode, "entries": entries,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create plan status = %d body=%s", rec.Code, rec.Body.String())
		}
		var p storage.ExecutionPlan
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return p.ID
	}
	run := func(id string) map[string]any {
		rec := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans/"+id+"/runs", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("run status = %d body=%s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	statuses := func(out map[string]any) []string {
		raw, _ := out["results"].([]any)
		got := make([]string, 0, len(raw))
		for _, r := range raw {
			m, _ := r.(map[string]any)
			got = append(got, m["status"].(string))
		}
		return got
	}

	// Sequential: first entry fails (no scenario) → second is halted/skipped.
	seq := run(mkPlan("sequential", []map[string]any{
		{"order": 0, "scenario_id": "missing-1"},
		{"order": 1, "scenario_id": "missing-2"},
	}))
	if got := statuses(seq); len(got) != 2 || got[0] != "failed" || got[1] != "skipped" {
		t.Fatalf("sequential statuses = %v, want [failed skipped]", got)
	}

	// Parallel: both entries attempted (both fail, none halted).
	par := run(mkPlan("parallel", []map[string]any{
		{"order": 0, "scenario_id": "missing-1"},
		{"order": 1, "scenario_id": "missing-2"},
	}))
	if got := statuses(par); len(got) != 2 || got[0] != "failed" || got[1] != "failed" {
		t.Fatalf("parallel statuses = %v, want [failed failed]", got)
	}

	// Disabled entries are skipped regardless of mode.
	dis := run(mkPlan("parallel", []map[string]any{
		{"order": 0, "scenario_id": "missing-1", "enabled": false},
	}))
	if got := statuses(dis); len(got) != 1 || got[0] != "skipped" {
		t.Fatalf("disabled statuses = %v, want [skipped]", got)
	}
}

func TestExecutionPlanRouteAccessRequirements(t *testing.T) {
	for _, tc := range []struct {
		method    string
		path      string
		wantRoles []string
	}{
		{http.MethodPost, "/api/v1/execution-plans", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/execution-plans", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodGet, "/api/v1/execution-plans/p1", []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPut, "/api/v1/execution-plans/p1", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodDelete, "/api/v1/execution-plans/p1", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
		{http.MethodPost, "/api/v1/execution-plans/p1/runs", []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin}},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			requirement, ok := accessRequirementForRequest(req)
			if !ok {
				t.Fatalf("no access requirement for %s %s", tc.method, tc.path)
			}
			if len(requirement.GlobalRoles) != len(tc.wantRoles) {
				t.Fatalf("GlobalRoles = %#v, want %#v", requirement.GlobalRoles, tc.wantRoles)
			}
		})
	}
}
