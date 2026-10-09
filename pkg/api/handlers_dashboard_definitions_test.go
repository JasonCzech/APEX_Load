package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// dashboardSaveFailingStore is a store whose definition saves fail with err.
type dashboardSaveFailingStore struct {
	*storage.SQLiteStore
	err error
}

func (s dashboardSaveFailingStore) UpdateDashboardDefinitionIfRevision(*storage.DashboardDefinition, int64) (bool, error) {
	return false, s.err
}

func (s dashboardSaveFailingStore) SaveDashboardDefinition(*storage.DashboardDefinition) error {
	return s.err
}

// dashboardRevisionRaceStore answers the revision list as if an edit
// committed after the definition was read: one revision newer than it.
type dashboardRevisionRaceStore struct {
	*storage.SQLiteStore
}

func (s dashboardRevisionRaceStore) ListDashboardDefinitionRevisions(id string) ([]storage.DashboardDefinitionRevision, error) {
	retained, err := s.SQLiteStore.ListDashboardDefinitionRevisions(id)
	if err != nil || len(retained) == 0 {
		return retained, err
	}
	newer := retained[0]
	newer.Revision++
	return append([]storage.DashboardDefinitionRevision{newer}, retained...), nil
}

// An edit that commits between the definition read and the revision list
// never makes a read inconsistent: a plain read lists no revision newer than
// the definition it returns, and a revision read names the newer current one.
func TestDashboardDefinitionReadsStayConsistentAcrossAConcurrentEdit(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	created := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{
		"project_id": projectID, "name": "Operations", "visibility": "project", "tabs": []map[string]any{{"id": "overview", "title": "Overview", "panels": []map[string]any{}}},
	})
	var definition storage.DashboardDefinition
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &definition) != nil {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	server.store = dashboardRevisionRaceStore{SQLiteStore: server.store.(*storage.SQLiteStore)}
	item := "/api/v1/monitoring/dashboard-definitions/" + definition.ID
	read := func(path string) storage.DashboardDefinition {
		t.Helper()
		response := dashboardAPIRequest(t, server, http.MethodGet, path, nil)
		var got storage.DashboardDefinition
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &got) != nil {
			t.Fatalf("%s = %d %s", path, response.Code, response.Body.String())
		}
		return got
	}
	if plain := read(item); plain.Revision != 1 || plain.CurrentRevision != 0 || len(plain.RetainedRevisions) != 1 || plain.RetainedRevisions[0].Revision != 1 {
		t.Fatalf("plain read = %#v", plain)
	}
	if named := read(item + "?revision=1"); named.Revision != 1 || named.CurrentRevision != 2 {
		t.Fatalf("revision read = %#v", named)
	}
}

// A definition refused for its content is the caller's 400; a store that
// fails to save it is the server's 500, whose response carries no cause.
func TestDashboardDefinitionSaveFailureIsTheServers(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	created := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{
		"project_id": projectID, "name": "Operations", "visibility": "project", "tabs": []map[string]any{{"id": "overview", "title": "Overview", "panels": []map[string]any{}}},
	})
	var definition storage.DashboardDefinition
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &definition) != nil {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	item := "/api/v1/monitoring/dashboard-definitions/" + definition.ID
	if invalid := dashboardAPIRequest(t, server, http.MethodPatch, item, map[string]any{"revision": 1, "visibility": "everyone"}); invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid edit = %d %s", invalid.Code, invalid.Body.String())
	}
	// The project's definition limit is the caller's to fix.
	for i := 1; i < storage.MaxDashboardDefinitionsPerProject; i++ {
		if created := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{"project_id": projectID, "name": fmt.Sprintf("Operations %d", i), "visibility": "project", "tabs": []map[string]any{{"id": "overview", "title": "Overview", "panels": []map[string]any{}}}}); created.Code != http.StatusCreated {
			t.Fatalf("create %d = %d %s", i, created.Code, created.Body.String())
		}
	}
	if limited := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{"project_id": projectID, "name": "One too many", "visibility": "project", "tabs": []map[string]any{{"id": "overview", "title": "Overview", "panels": []map[string]any{}}}}); limited.Code != http.StatusBadRequest || !strings.Contains(limited.Body.String(), "maximum") {
		t.Fatalf("over the limit = %d %s", limited.Code, limited.Body.String())
	}
	server.store = dashboardSaveFailingStore{SQLiteStore: server.store.(*storage.SQLiteStore), err: errors.New("database is locked")}
	failed := dashboardAPIRequest(t, server, http.MethodPatch, item, map[string]any{"revision": 1, "name": "Operations v2"})
	if failed.Code != http.StatusInternalServerError || strings.Contains(failed.Body.String(), "locked") {
		t.Fatalf("store failure = %d %s", failed.Code, failed.Body.String())
	}
	failed = dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{"project_id": projectID, "name": "Operations new", "visibility": "project", "tabs": []map[string]any{{"id": "overview", "title": "Overview", "panels": []map[string]any{}}}})
	if failed.Code != http.StatusInternalServerError || strings.Contains(failed.Body.String(), "locked") {
		t.Fatalf("create store failure = %d %s", failed.Code, failed.Body.String())
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
	// A read of the current revision by number still says it is current.
	if status, named := read(item + "?revision=2"); status != http.StatusOK || named.Revision != 2 || named.CurrentRevision != 2 {
		t.Fatalf("current revision by number = %d %#v", status, named)
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

// A saved revision link is readable by a viewer of its project and refused,
// behind real authentication, to a principal of another project.
func TestDashboardDefinitionRevisionDeniesAnotherProject(t *testing.T) {
	server, projectID := newGrafanaDashboardAPIServer(t)
	created := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", map[string]any{
		"project_id": projectID, "name": "Shared operations", "visibility": "project",
		"tabs": []map[string]any{{"id": "overview", "title": "Overview", "panels": []map[string]any{{"id": "latency", "title": "Latency", "kind": "line", "x": 0, "y": 0, "w": 12, "h": 8,
			"query": map[string]any{"query_version": storage.AnalyticsQueryVersion, "metric": "response_time", "aggregation": "p95", "max_series": 20}}}}},
	})
	var definition storage.DashboardDefinition
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &definition) != nil {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	enableTrustedHeaderAuth(t, server)
	path := "/api/v1/monitoring/dashboard-definitions/" + definition.ID + "?revision=1"
	if reply := harDownloadRequest(t, server, path, "own-viewer", projectID+"="+RoleViewer); reply.Code != http.StatusOK {
		t.Fatalf("own project's viewer = %d %s", reply.Code, reply.Body.String())
	}
	for _, roles := range []string{"another-project=" + RoleViewer, "another-project=" + RoleEditor} {
		if reply := harDownloadRequest(t, server, path+"&project_id=another-project", "foreign", roles); reply.Code != http.StatusForbidden {
			t.Fatalf("%s read another project's revision: %d %s", roles, reply.Code, reply.Body.String())
		}
	}
}
