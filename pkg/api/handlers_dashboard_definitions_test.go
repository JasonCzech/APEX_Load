package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDashboardDefinitionsCreateListAndRevisionConflict(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	body := map[string]any{
		"project_id": projectID,
		"name":       "Shared operations",
		"visibility": "project",
		"tabs": []map[string]any{{
			"id": "overview", "title": "Overview",
			"panels": []map[string]any{{
				"id": "latency", "title": "Latency", "kind": "line",
				"x": 0, "y": 0, "w": 12, "h": 8,
				"query": map[string]any{"metric": "response_time", "aggregation": "p95", "dimensions": []string{"region"}, "max_series": 20},
			}},
		}},
	}
	created := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", body)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	var definition storage.DashboardDefinition
	if err := json.Unmarshal(created.Body.Bytes(), &definition); err != nil {
		t.Fatal(err)
	}
	if definition.ID == "" || definition.Revision != 1 || definition.ProjectID != projectID {
		t.Fatalf("created definition = %#v", definition)
	}

	listed := dashboardAPIRequest(t, server, http.MethodGet, "/api/v1/monitoring/dashboard-definitions?project_id="+projectID, nil)
	if listed.Code != http.StatusOK || !json.Valid(listed.Body.Bytes()) {
		t.Fatalf("list status = %d body=%s", listed.Code, listed.Body.String())
	}

	patch := dashboardAPIRequest(t, server, http.MethodPatch, "/api/v1/monitoring/dashboard-definitions/"+definition.ID, map[string]any{"revision": 1, "name": "Shared operations v2"})
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status = %d body=%s", patch.Code, patch.Body.String())
	}
	stale := dashboardAPIRequest(t, server, http.MethodPatch, "/api/v1/monitoring/dashboard-definitions/"+definition.ID, map[string]any{"revision": 1, "name": "stale"})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale patch status = %d body=%s", stale.Code, stale.Body.String())
	}
}

func TestDashboardDefinitionRejectsArbitraryPanelKind(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	response := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{
		"project_id": projectID, "name": "Unsafe", "visibility": "project",
		"tabs": []map[string]any{{"id": "one", "title": "One", "panels": []map[string]any{{
			"id": "raw", "title": "Raw", "kind": "iframe", "x": 0, "y": 0, "w": 24, "h": 8, "card_id": "raw",
		}}}},
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestDashboardDefinitionReopensAnExactSavedRevision(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	panel := func(aggregation string) map[string]any {
		return map[string]any{"id": "latency", "title": "Latency", "kind": "line", "x": 0, "y": 0, "w": 12, "h": 8,
			"query": map[string]any{"query_version": storage.AnalyticsQueryVersion, "metric": "response_time", "aggregation": aggregation, "dimensions": []string{"region"}, "max_series": 20}}
	}
	created := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{
		"project_id": projectID, "name": "Shared operations", "visibility": "project",
		"tabs": []map[string]any{{"id": "overview", "title": "Overview", "panels": []map[string]any{panel("p95")}}},
	})
	var definition storage.DashboardDefinition
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &definition) != nil {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	item := "/api/v1/monitoring/dashboard-definitions/" + definition.ID
	if patched := dashboardAPIRequest(t, server, http.MethodPatch, item, map[string]any{"revision": 1, "name": "Shared operations v2",
		"tabs": []map[string]any{{"id": "overview", "title": "Overview", "panels": []map[string]any{panel("p99")}}}}); patched.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", patched.Code, patched.Body.String())
	}
	read := func(path string) (int, storage.DashboardDefinition) {
		t.Helper()
		response := dashboardAPIRequest(t, server, http.MethodGet, path, nil)
		var got storage.DashboardDefinition
		_ = json.Unmarshal(response.Body.Bytes(), &got)
		return response.Code, got
	}
	// A second viewer opening the link to revision 1 sees exactly what was
	// saved then, with the current revision beside it.
	status, first := read(item + "?revision=1")
	if status != http.StatusOK || first.Revision != 1 || first.CurrentRevision != 2 || first.Name != "Shared operations" || first.Tabs[0].Panels[0].Query.Aggregation != "p95" || first.Tabs[0].Panels[0].Query.QueryVersion != storage.AnalyticsQueryVersion {
		t.Fatalf("revision 1 = %d %#v", status, first)
	}
	if len(first.RetainedRevisions) != 2 || first.RetainedRevisions[0].Revision != 2 {
		t.Fatalf("retained revisions = %#v", first.RetainedRevisions)
	}
	status, current := read(item)
	if status != http.StatusOK || current.Revision != 2 || current.CurrentRevision != 0 || current.Tabs[0].Panels[0].Query.Aggregation != "p99" {
		t.Fatalf("current = %d %#v", status, current)
	}
	if status, _ := read(item + "?revision=2"); status != http.StatusOK {
		t.Fatalf("current revision by number = %d", status)
	}
	if status, _ := read(item + "?revision=7"); status != http.StatusNotFound {
		t.Fatalf("unsaved revision = %d", status)
	}
	if status, _ := read(item + "?revision=zero"); status != http.StatusBadRequest {
		t.Fatalf("malformed revision = %d", status)
	}
	// The route's project comes from the stored definition, never from the
	// request, so a revision link cannot be read through another project.
	request := httptest.NewRequest(http.MethodGet, item+"?revision=1&project_id=proj-b", nil)
	request.SetPathValue("id", definition.ID)
	if project, status, _ := resolveDashboardDefinitionProjectID(request, server.store); project != projectID || status != 0 {
		t.Fatalf("resolved project = %q %d", project, status)
	}
	// Deletion races: a stale delete and an edit of a deleted definition are
	// refused, and its revisions go with it.
	if stale := dashboardAPIRequest(t, server, http.MethodDelete, item+"?revision=1", nil); stale.Code != http.StatusConflict {
		t.Fatalf("stale delete = %d", stale.Code)
	}
	if deleted := dashboardAPIRequest(t, server, http.MethodDelete, item+"?revision=2", nil); deleted.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
	if late := dashboardAPIRequest(t, server, http.MethodPatch, item, map[string]any{"revision": 2, "name": "late"}); late.Code != http.StatusNotFound {
		t.Fatalf("edit after delete = %d", late.Code)
	}
	if status, _ := read(item + "?revision=1"); status != http.StatusNotFound {
		t.Fatalf("revision of a deleted definition = %d", status)
	}
}

func TestDashboardDefinitionsRaceUpdateAndDelete(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	created := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{
		"project_id": projectID, "name": "Race", "visibility": "project",
		"tabs": []map[string]any{{"id": "one", "title": "One", "panels": []map[string]any{{"id": "c", "title": "C", "kind": "stat", "x": 0, "y": 0, "w": 6, "h": 4, "card_id": "throughput"}}}},
	})
	var definition storage.DashboardDefinition
	if json.Unmarshal(created.Body.Bytes(), &definition) != nil {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	item := "/api/v1/monitoring/dashboard-definitions/" + definition.ID
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, request := range []func() int{
		func() int {
			return dashboardAPIRequest(t, server, http.MethodPatch, item, map[string]any{"revision": 1, "name": "Race v2"}).Code
		},
		func() int { return dashboardAPIRequest(t, server, http.MethodDelete, item+"?revision=1", nil).Code },
	} {
		wg.Add(1)
		go func() { defer wg.Done(); results <- request() }()
	}
	wg.Wait()
	close(results)
	won := 0
	for code := range results {
		if code == http.StatusOK {
			won++
		} else if code != http.StatusConflict && code != http.StatusNotFound {
			t.Fatalf("unexpected status %d", code)
		}
	}
	if won != 1 {
		t.Fatalf("%d of an update and a delete at revision 1 succeeded", won)
	}
}
