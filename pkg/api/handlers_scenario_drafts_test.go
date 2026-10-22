package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScenarioDraftsSaveToTheProjectAndNeverLaunch(t *testing.T) {
	s, store := newAuditTestServer(t)
	defer store.Close()
	now := time.Now().UTC()
	for _, id := range []string{"draft-p", "draft-other"} {
		if err := store.SaveProject(&storage.Project{ID: id, Name: id, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject: %v", err)
		}
	}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path, reader)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder) ScenarioDraftResponse {
		t.Helper()
		var out ScenarioDraftResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", w.Body, err)
		}
		return out
	}
	document := map[string]any{"version": 3, "form": map[string]any{"name": "", "groups": []any{}}, "step": "scripts"}

	// An unnamed draft with no scripts still saves to the project.
	created := call(http.MethodPost, "/api/v1/projects/draft-p/scenario-drafts", map[string]any{"name": "  ", "document": document})
	if created.Code != http.StatusCreated {
		t.Fatalf("create %d %s", created.Code, created.Body)
	}
	draft := decode(created)
	if draft.Name != "Untitled scenario" || draft.Revision != 1 || draft.ProjectID != "draft-p" || !strings.HasPrefix(draft.ID, "scenario-draft-") || !strings.Contains(string(draft.Document), `"step":"scripts"`) {
		t.Fatalf("created = %+v", draft)
	}

	// The list carries no documents; another project sees nothing.
	var listed ScenarioDraftListResponse
	list := call(http.MethodGet, "/api/v1/projects/draft-p/scenario-drafts", nil)
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || listed.Count != 1 || listed.Drafts[0].ID != draft.ID || strings.Contains(list.Body.String(), `"document"`) {
		t.Fatalf("list %d %s", list.Code, list.Body)
	}
	if other := call(http.MethodGet, "/api/v1/projects/draft-other/scenario-drafts/"+draft.ID, nil); other.Code != http.StatusNotFound {
		t.Fatalf("other project read %d", other.Code)
	}

	// Saves need the revision they were made from.
	updated := call(http.MethodPut, "/api/v1/projects/draft-p/scenario-drafts/"+draft.ID, map[string]any{"name": "Checkout peak", "document": document, "revision": 1})
	if updated.Code != http.StatusOK || decode(updated).Revision != 2 {
		t.Fatalf("update %d %s", updated.Code, updated.Body)
	}
	if stale := call(http.MethodPut, "/api/v1/projects/draft-p/scenario-drafts/"+draft.ID, map[string]any{"name": "Lost", "document": document, "revision": 1}); stale.Code != http.StatusConflict {
		t.Fatalf("stale update %d %s", stale.Code, stale.Body)
	}
	if missing := call(http.MethodPut, "/api/v1/projects/draft-p/scenario-drafts/nope", map[string]any{"name": "x", "document": document, "revision": 1}); missing.Code != http.StatusNotFound {
		t.Fatalf("missing update %d", missing.Code)
	}
	if norev := call(http.MethodPut, "/api/v1/projects/draft-p/scenario-drafts/"+draft.ID, map[string]any{"name": "x", "document": document}); norev.Code != http.StatusBadRequest {
		t.Fatalf("update without revision %d", norev.Code)
	}

	// Drafts are visible to the project, so inline credentials are refused.
	for name, form := range map[string]any{
		"runtime env":    map[string]any{"runtime_settings": map[string]any{"env": map[string]any{"API_KEY": "plain"}}},
		"header":         map[string]any{"groups": []any{map[string]any{"runtimeSettings": map[string]any{"headers": map[string]any{"Authorization": "Bearer x"}}}}},
		"url user info":  map[string]any{"runtime_settings": map[string]any{"proxy": "http://user:pass@proxy:8080"}},
		"password field": map[string]any{"password": "hunter2"},
	} {
		refused := call(http.MethodPost, "/api/v1/projects/draft-p/scenario-drafts", map[string]any{"name": "x", "document": map[string]any{"version": 3, "form": form}})
		if refused.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, refused.Code, refused.Body)
		}
	}
	// Secret references and empty maps are fine.
	allowed := call(http.MethodPost, "/api/v1/projects/draft-p/scenario-drafts", map[string]any{"name": "Refs", "document": map[string]any{"version": 3, "form": map[string]any{"runtime_settings": map[string]any{"env": map[string]any{"API_KEY": "{{secret.API_KEY}}"}, "headers": map[string]any{}}}}})
	if allowed.Code != http.StatusCreated {
		t.Fatalf("secret references %d %s", allowed.Code, allowed.Body)
	}
	// With the spacing the runtime accepts, too.
	spaced := call(http.MethodPost, "/api/v1/projects/draft-p/scenario-drafts", map[string]any{"name": "Spaced refs", "document": map[string]any{"version": 3, "form": map[string]any{"runtime_settings": map[string]any{"env": map[string]any{"API_KEY": "{{ secret.API_KEY }}"}}}}})
	if spaced.Code != http.StatusCreated {
		t.Fatalf("spaced secret references %d %s", spaced.Code, spaced.Body)
	}
	// An edit draft names a scenario of its own project, never another's.
	scenarioIDs := map[string]string{}
	for _, projectID := range []string{"draft-p", "draft-other"} {
		s.runner.StoreScript(&dsl.Script{ID: projectID + "-script", Name: "S", Protocol: "http", Version: "1.0.0", ProjectID: projectID, Actions: []dsl.Action{{Name: "a", Type: "request", Target: "/"}}})
		scenario, err := s.runner.StoreScenario(CreateScenarioRequest{Name: projectID + " scenario", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: projectID + "-script", VUsers: 1}}})
		if err != nil {
			t.Fatalf("StoreScenario(%s): %v", projectID, err)
		}
		scenarioIDs[projectID] = scenario.ID
	}
	if edit := call(http.MethodPost, "/api/v1/projects/draft-p/scenario-drafts", map[string]any{"name": "Checkout changes", "scenario_id": scenarioIDs["draft-p"], "document": document}); edit.Code != http.StatusCreated || decode(edit).ScenarioID != scenarioIDs["draft-p"] || decode(edit).ScenarioName != "draft-p scenario" {
		t.Fatalf("edit draft %d %s", edit.Code, edit.Body)
	}
	if foreign := call(http.MethodPost, "/api/v1/projects/draft-p/scenario-drafts", map[string]any{"name": "x", "scenario_id": scenarioIDs["draft-other"], "document": document}); foreign.Code != http.StatusBadRequest || strings.Contains(foreign.Body.String(), "draft-other scenario") {
		t.Fatalf("draft of another project's scenario %d %s", foreign.Code, foreign.Body)
	}
	// A scenario deleted since the edit began leaves the work as a new scenario's draft.
	if orphan := call(http.MethodPost, "/api/v1/projects/draft-p/scenario-drafts", map[string]any{"name": "Orphan", "scenario_id": "no-such-scenario", "document": document}); orphan.Code != http.StatusCreated || decode(orphan).ScenarioID != "" {
		t.Fatalf("draft of a deleted scenario %d %s", orphan.Code, orphan.Body)
	}
	if missing := call(http.MethodPost, "/api/v1/projects/no-such-project/scenario-drafts", map[string]any{"name": "x", "document": document}); missing.Code < 400 || missing.Code >= 500 {
		t.Fatalf("draft in a missing project %d %s", missing.Code, missing.Body)
	}
	for name, body := range map[string]any{
		"not an object":       map[string]any{"name": "x", "document": []any{1}},
		"name over 200 runes": map[string]any{"name": strings.Repeat("é", 201), "document": document},
	} {
		if refused := call(http.MethodPost, "/api/v1/projects/draft-p/scenario-drafts", body); refused.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, refused.Code, refused.Body)
		}
	}

	// A delete at an older revision keeps the work saved since.
	if stale := call(http.MethodDelete, "/api/v1/projects/draft-p/scenario-drafts/"+draft.ID+"?revision=1", nil); stale.Code != http.StatusConflict {
		t.Fatalf("stale delete %d %s", stale.Code, stale.Body)
	}
	if bad := call(http.MethodDelete, "/api/v1/projects/draft-p/scenario-drafts/"+draft.ID+"?revision=x", nil); bad.Code != http.StatusBadRequest {
		t.Fatalf("delete with a bad revision %d", bad.Code)
	}
	if deleted := call(http.MethodDelete, "/api/v1/projects/draft-p/scenario-drafts/"+draft.ID+"?revision=2", nil); deleted.Code != http.StatusOK {
		t.Fatalf("delete %d %s", deleted.Code, deleted.Body)
	}
	if again := call(http.MethodDelete, "/api/v1/projects/draft-p/scenario-drafts/"+draft.ID, nil); again.Code != http.StatusNotFound {
		t.Fatalf("delete again %d", again.Code)
	}

	// A draft is not a scenario: no run can name it.
	if run := call(http.MethodPost, "/api/v1/tests", map[string]any{"scenario_id": draft.ID, "project_id": "draft-p"}); run.Code < 400 {
		t.Fatalf("a run started from a draft id: %d %s", run.Code, run.Body)
	}
}
