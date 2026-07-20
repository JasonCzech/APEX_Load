package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type noScenarioLookupRevisionResolverStore struct {
	*metricsStoreSpy
	source           *storage.TestRecord
	getScenarioCalls int
}

func (s *noScenarioLookupRevisionResolverStore) GetTest(id string) (*storage.TestRecord, error) {
	if s.source != nil && s.source.ID == id {
		return s.source, nil
	}
	return nil, errors.New("test not found")
}

func (s *noScenarioLookupRevisionResolverStore) GetScenario(string) (*storage.Scenario, []storage.ScenarioScript, error) {
	s.getScenarioCalls++
	return nil, nil, errors.New("unexpected live scenario lookup")
}

func TestResolveTestCreateProjectAllowsDeletedRevisionSourceScript(t *testing.T) {
	store, _, sourceID := deletedRevisionSourceFixture(t)
	sourceIndex := 0
	body := revisionResolverRequestBody(t, CreateTestRequest{
		RevisionSourceTestID: sourceID,
		Groups: []CreateGroupEntry{{
			Name: "Renamed source", ScriptID: "script-project-b", RevisionSourceGroupIndex: &sourceIndex, VUsers: 1,
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	projectID, status, message := resolveTestCreateProjectID(req, store)
	if status != 0 || projectID != "project-b" {
		t.Fatalf("resolve deleted source = project %q status %d message %q, want project-b success", projectID, status, message)
	}
}

func TestResolveTestCreateProjectUsesRevisionSnapshotWithoutScenarioLookup(t *testing.T) {
	sourceIndex := 0
	store := &noScenarioLookupRevisionResolverStore{
		metricsStoreSpy: &metricsStoreSpy{},
		source: &storage.TestRecord{
			ID: "snapshot-scoped-scenario-source", Status: "QUEUED", ProjectID: "project-a", ScenarioID: "source-scenario",
			Groups: encodeStoredGroups([]TestGroup{{Name: "Primary", ScriptID: "source-script", VUsers: 1}}),
		},
	}
	body := revisionResolverRequestBody(t, CreateTestRequest{
		RevisionSourceTestID: store.source.ID,
		ScenarioID:           store.source.ScenarioID,
		Groups: []CreateGroupEntry{{
			Name: "Primary", ScriptID: "source-script", RevisionSourceGroupIndex: &sourceIndex, VUsers: 1,
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	projectID, status, message := resolveTestCreateProjectID(req, store)
	if status != 0 || projectID != "project-a" {
		t.Fatalf("resolve = project %q status %d message %q, want project-a success", projectID, status, message)
	}
	if store.getScenarioCalls != 0 {
		t.Fatalf("resolver dereferenced missing live scenario %d times", store.getScenarioCalls)
	}

	mismatched := revisionResolverRequestBody(t, CreateTestRequest{
		RevisionSourceTestID: store.source.ID,
		ScenarioID:           "different-scenario",
		Groups: []CreateGroupEntry{{
			Name: "Primary", ScriptID: "source-script", RevisionSourceGroupIndex: &sourceIndex, VUsers: 1,
		}},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(mismatched))
	_, status, message = resolveTestCreateProjectID(req, store)
	if status != http.StatusBadRequest || !strings.Contains(message, "requested scenario") {
		t.Fatalf("mismatched scenario = status %d message %q, want 400", status, message)
	}
}

func TestResolveTestCreateProjectValidatesDurableRevisionSourceGroupIdentity(t *testing.T) {
	store, _, sourceID := deletedRevisionSourceFixture(t)
	tests := []struct {
		name        string
		group       CreateGroupEntry
		wantMessage string
	}{
		{
			name:        "source index out of range",
			group:       CreateGroupEntry{Name: "Bad index", ScriptID: "script-project-b", RevisionSourceGroupIndex: intPointer(1), VUsers: 1},
			wantMessage: "out of range",
		},
		{
			name:        "source script mismatch",
			group:       CreateGroupEntry{Name: "Swapped script", ScriptID: "script-project-a", RevisionSourceGroupIndex: intPointer(0), VUsers: 1},
			wantMessage: "does not match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := revisionResolverRequestBody(t, CreateTestRequest{
				RevisionSourceTestID: sourceID,
				Groups:               []CreateGroupEntry{test.group},
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
			_, status, message := resolveTestCreateProjectID(req, store)
			if status != http.StatusBadRequest || !strings.Contains(message, test.wantMessage) {
				t.Fatalf("resolve = status %d message %q, want 400 containing %q", status, message, test.wantMessage)
			}
		})
	}
}

func TestResolveTestCreateProjectRequiresLiveScriptForRevisionNewGroup(t *testing.T) {
	store, _, sourceID := deletedRevisionSourceFixture(t)
	body := revisionResolverRequestBody(t, CreateTestRequest{
		RevisionSourceTestID: sourceID,
		Groups: []CreateGroupEntry{{
			Name: "Explicit new group", ScriptID: "script-project-b", RevisionNewGroup: true, VUsers: 1,
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	_, status, message := resolveTestCreateProjectID(req, store)
	if status != http.StatusNotFound || message != "script not found" {
		t.Fatalf("resolve deleted new-group script = status %d message %q, want 404 script not found", status, message)
	}
}

func TestCreateTestAuthMiddlewareScopesDeletedRevisionSourceBeforeLiveScripts(t *testing.T) {
	store, server, sourceID := deletedRevisionSourceFixture(t)
	settings := enableTrustedHeaderAuth(t, server)
	sourceIndex := 0
	body := revisionResolverRequestBody(t, CreateTestRequest{
		RevisionSourceTestID: sourceID,
		Groups: []CreateGroupEntry{{
			Name: "Renamed source", ScriptID: "script-project-b", RevisionSourceGroupIndex: &sourceIndex, VUsers: 1,
		}},
	})

	called := false
	handler := authMiddleware(func() AuthSettings { return settings.Auth }, store, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if projectID, ok := authorizedProjectFromRequest(r); !ok || projectID != "project-b" {
			t.Fatalf("authorized project = %q, %t; want project-b", projectID, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	authorized := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	authorized.Header.Set("Content-Type", "application/json")
	authorized.Header.Set("X-Auth-Request-User", "revision-project-b-editor")
	authorized.Header.Set("X-APEXLoad-Project-Roles", "project-b="+RoleEditor)
	authorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(authorizedResponse, authorized)
	if authorizedResponse.Code != http.StatusNoContent || !called {
		t.Fatalf("authorized middleware response = %d called=%t body=%s", authorizedResponse.Code, called, authorizedResponse.Body.String())
	}

	called = false
	forbidden := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	forbidden.Header.Set("Content-Type", "application/json")
	forbidden.Header.Set("X-Auth-Request-User", "revision-project-a-editor")
	forbidden.Header.Set("X-APEXLoad-Project-Roles", "project-a="+RoleEditor)
	forbiddenResponse := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusForbidden || called {
		t.Fatalf("foreign middleware response = %d called=%t body=%s", forbiddenResponse.Code, called, forbiddenResponse.Body.String())
	}
}

func TestCreateQueuedRevisionAuthorizesImplicitLegacyContextInSourceProject(t *testing.T) {
	_, server := newCreateTestProjectIsolationServer(t)
	enableTrustedHeaderAuth(t, server)

	source, err := server.runner.CreateTest(CreateTestRequest{
		Name:      "Standalone project A source",
		ProjectID: "project-a",
		Duration:  "1m",
		Groups: []CreateGroupEntry{{
			Name: "Primary", ScriptID: "script-project-a", VUsers: 1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest(source): %v", err)
	}
	if source.ApplicationID != storage.LegacyApplicationID || source.ReleaseID != storage.LegacyReleaseID {
		t.Fatalf("source fallback context = application %q release %q, want public legacy IDs", source.ApplicationID, source.ReleaseID)
	}
	if deleted, deleteErr := server.runner.DeleteScriptDurableInProject("script-project-a", "project-a"); deleteErr != nil || !deleted {
		t.Fatalf("DeleteScriptDurableInProject(source) = %t, %v", deleted, deleteErr)
	}

	body := revisionResolverRequestBody(t, CreateTestRequest{
		Name:                 "Authorized standalone revision",
		RevisionSourceTestID: source.ID,
		ProjectID:            "project-a",
		ApplicationID:        source.ApplicationID,
		ReleaseID:            source.ReleaseID,
		Groups:               createGroupEntriesFromManagedTest(source),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Request-User", "revision-project-a-editor")
	req.Header.Set("X-APEXLoad-Project-Roles", "project-a="+RoleEditor)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("revision response = %d body=%s, want 201", resp.Code, resp.Body.String())
	}
	var revision ManagedTest
	if err := json.NewDecoder(resp.Body).Decode(&revision); err != nil {
		t.Fatalf("decode revision response: %v", err)
	}
	if revision.ProjectID != "project-a" || revision.Status != "QUEUED" {
		t.Fatalf("revision context = project %q status %q, want project-a QUEUED", revision.ProjectID, revision.Status)
	}
}

func TestResolveQueuedRevisionStillValidatesExplicitReleaseContext(t *testing.T) {
	store, _ := newCreateTestProjectIsolationServer(t)
	now := time.Now().UTC()
	sourceID := "queued-explicit-release-source"
	if err := store.SaveTest(&storage.TestRecord{
		ID: sourceID, Name: "Explicit release source", Status: "QUEUED", ProjectID: "project-a",
		ApplicationID: "application-project-a", ReleaseID: "release-project-a",
		Duration: int64(time.Minute), StartTime: now,
		Groups: encodeStoredGroups([]TestGroup{{Name: "Primary", ScriptID: "script-project-a", VUsers: 1}}),
	}); err != nil {
		t.Fatalf("SaveTest(source): %v", err)
	}
	sourceIndex := 0
	body := revisionResolverRequestBody(t, CreateTestRequest{
		RevisionSourceTestID: sourceID,
		ApplicationID:        storage.LegacyApplicationID,
		ReleaseID:            storage.LegacyReleaseID,
		Groups: []CreateGroupEntry{{
			Name: "Primary", ScriptID: "script-project-a", RevisionSourceGroupIndex: &sourceIndex, VUsers: 1,
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(body))
	_, status, message := resolveTestCreateProjectID(req, store)
	if status != http.StatusBadRequest || !strings.Contains(message, "release project") {
		t.Fatalf("explicit release mismatch = status %d message %q, want 400 release project mismatch", status, message)
	}
}

func deletedRevisionSourceFixture(t *testing.T) (*storage.SQLiteStore, *Server, string) {
	t.Helper()
	store, server := newCreateTestProjectIsolationServer(t)
	now := time.Now().UTC()
	sourceID := "queued-deleted-script-revision-source"
	if err := store.SaveTest(&storage.TestRecord{
		ID: sourceID, Name: "Queued deleted-script source", Status: "QUEUED", ProjectID: "project-b",
		Duration: int64(time.Minute), StartTime: now,
		Groups: encodeStoredGroups([]TestGroup{{Name: "Source group", ScriptID: "script-project-b", VUsers: 1}}),
	}); err != nil {
		t.Fatalf("SaveTest(source): %v", err)
	}
	if err := store.DeleteScript("script-project-b"); err != nil {
		t.Fatalf("DeleteScript(source): %v", err)
	}
	return store, server, sourceID
}

func revisionResolverRequestBody(t *testing.T, req CreateTestRequest) []byte {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal revision request: %v", err)
	}
	return payload
}

func intPointer(value int) *int { return &value }
