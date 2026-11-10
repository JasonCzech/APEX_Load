package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func scriptLoadRunsTestServer(t *testing.T) *Server {
	t.Helper()
	server := recordingTestServer(t)
	storeRecordingDigestTestScript(t, server, "s1")
	storeRecordingDigestTestScript(t, server, "s2")
	at := func(minute int) *time.Time {
		value := time.Date(2026, 11, 1, 10, minute, 0, 0, time.UTC)
		return &value
	}
	archive := &storage.RunArchiveRecord{TestID: "archived"}
	server.runner.mu.Lock()
	server.runner.tests = map[string]*ManagedTest{
		"old": {ID: "old", Name: "Old run", ProjectID: "p1", Status: "COMPLETE", StartedAt: at(1), EndedAt: at(2), Archive: archive,
			Groups: []TestGroup{{Name: "Shop", ScriptID: "s1", VUsers: 20}, {Name: "Other", ScriptID: "s2", VUsers: 5}, {Name: "Shop B", ScriptID: "s1", VUsers: 10}}},
		"new": {ID: "new", Name: "New run", ProjectID: "p1", Status: "COMPLETE", StartedAt: at(30), Groups: []TestGroup{{Name: "Shop", ScriptID: "s1", VUsers: 50}}},
		"stopped-archived": {ID: "stopped-archived", Name: "Stopped", ProjectID: "p1", Status: "ABORTED", StartedAt: at(20), Archive: archive,
			Groups: []TestGroup{{Name: "Shop", ScriptID: "s1"}}},
		"failed-no-archive": {ID: "failed-no-archive", ProjectID: "p1", Status: "FAILED", StartedAt: at(25), Groups: []TestGroup{{Name: "Shop", ScriptID: "s1"}}},
		"running":           {ID: "running", ProjectID: "p1", Status: "RUNNING", StartedAt: at(40), Groups: []TestGroup{{Name: "Shop", ScriptID: "s1"}}},
		"other-script":      {ID: "other-script", ProjectID: "p1", Status: "COMPLETE", StartedAt: at(35), Groups: []TestGroup{{Name: "Other", ScriptID: "s2"}}},
		"other-project":     {ID: "other-project", ProjectID: "p2", Status: "COMPLETE", StartedAt: at(45), Archive: archive, Groups: []TestGroup{{Name: "Shop", ScriptID: "s1"}}},
		"legacy":            {ID: "legacy", ProjectID: "p1", Status: "COMPLETE", StartedAt: at(5), ScriptID: "s1"},
	}
	server.runner.mu.Unlock()
	return server
}

func getScriptLoadRuns(t *testing.T, server *Server, query string) (*httptest.ResponseRecorder, ScriptLoadRunsResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/load-runs"+query, nil)
	req.SetPathValue("id", "s1")
	resp := httptest.NewRecorder()
	server.handleListScriptLoadRuns(resp, req)
	var out ScriptLoadRunsResponse
	if resp.Code == http.StatusOK {
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return resp, out
}

func TestListScriptLoadRunsFiltersAndOrders(t *testing.T) {
	server := scriptLoadRunsTestServer(t)
	resp, out := getScriptLoadRuns(t, server, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var ids []string
	for _, run := range out.Runs {
		ids = append(ids, run.TestID)
	}
	want := []string{"new", "stopped-archived", "legacy", "old"}
	if len(ids) != len(want) {
		t.Fatalf("runs = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("runs = %v, want %v", ids, want)
		}
	}
	old := out.Runs[3]
	if !old.ArchiveAvailable || old.VUsers != 30 || len(old.Groups) != 2 || old.Groups[0] != "Shop" || old.Groups[1] != "Shop B" {
		t.Fatalf("old run = %+v", old)
	}
	if out.Runs[0].ArchiveAvailable || out.Limit != defaultScriptLoadRunLimit || out.Count != 4 || out.ScriptID != "s1" {
		t.Fatalf("response = %+v", out)
	}
	if out.Runs[2].Groups == nil {
		t.Fatal("legacy run must encode groups as an empty list")
	}
}

func TestListScriptLoadRunsLimit(t *testing.T) {
	server := scriptLoadRunsTestServer(t)
	_, out := getScriptLoadRuns(t, server, "?limit=2")
	if len(out.Runs) != 2 || out.Runs[0].TestID != "new" || out.Limit != 2 {
		t.Fatalf("limited = %+v", out)
	}
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=x"} {
		if resp, _ := getScriptLoadRuns(t, server, query); resp.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", query, resp.Code)
		}
	}
}

func TestListScriptLoadRunsMissingScript(t *testing.T) {
	server := scriptLoadRunsTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/missing/load-runs", nil)
	req.SetPathValue("id", "missing")
	resp := httptest.NewRecorder()
	server.handleListScriptLoadRuns(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
}

// The route admits readers of the script's project only, and the handler
// never returns another project's runs of the same script.
func TestListScriptLoadRunsProjectScoping(t *testing.T) {
	server := scriptLoadRunsTestServer(t)
	requirement, ok := accessRequirementForRequest(func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/load-runs", nil)
		req.SetPathValue("id", "s1")
		return req
	}())
	if !ok {
		t.Fatal("load-runs route has no access requirement")
	}
	authorize := func(project string) (*httptest.ResponseRecorder, *http.Request, bool) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/load-runs", nil)
		req.SetPathValue("id", "s1")
		resp := httptest.NewRecorder()
		user := &AuthenticatedUser{ID: "viewer", ProjectRoles: map[string]map[string]struct{}{project: {RoleViewer: {}}}}
		allowed := authorizeAccessRequirement(resp, req, server.store, authResolution{User: user}, requirement)
		return resp, req, allowed
	}
	if resp, _, allowed := authorize("p2"); allowed || resp.Code != http.StatusForbidden {
		t.Fatalf("p2 viewer: allowed=%t status=%d", allowed, resp.Code)
	}
	resp, req, allowed := authorize("p1")
	if !allowed {
		t.Fatalf("p1 viewer denied: status=%d body=%s", resp.Code, resp.Body.String())
	}
	if projectID, scoped := authorizedProjectFromRequest(req); !scoped || projectID != "p1" {
		t.Fatalf("authorized project = %q, %t", projectID, scoped)
	}
	resp = httptest.NewRecorder()
	server.handleListScriptLoadRuns(resp, req)
	var out ScriptLoadRunsResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, run := range out.Runs {
		if run.TestID == "other-project" {
			t.Fatalf("p1 reader saw a p2 run: %+v", out.Runs)
		}
	}
}

// The list is test-run metadata: a service account needs results:read, as
// for GET /api/v1/tests; the broad "read" scope (scripts) is not enough.
func TestListScriptLoadRunsServiceAccountNeedsResultsRead(t *testing.T) {
	newRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/s1/load-runs?limit=50", nil)
		req.SetPathValue("id", "s1")
		return req
	}
	if got := requiredServiceAccountScopes(newRequest()); len(got) != 1 || got[0] != "results:read" {
		t.Fatalf("required scopes = %v, want [results:read]", got)
	}
	if testsScope := requiredServiceAccountScopes(httptest.NewRequest(http.MethodGet, "/api/v1/tests", nil)); len(testsScope) != 1 || testsScope[0] != "results:read" {
		t.Fatalf("GET /api/v1/tests scopes = %v: the parity premise changed", testsScope)
	}
	for _, tc := range []struct {
		scopes  []string
		allowed bool
	}{
		{[]string{"read"}, false},
		{[]string{"scripts:write"}, false},
		{[]string{"results:read"}, true},
		{[]string{"read", "results:read"}, true},
	} {
		account := &AuthenticatedServiceAccount{ID: "ci", Scopes: tc.scopes}
		if got := serviceAccountAllowsRequest(account, newRequest()); got != tc.allowed {
			t.Fatalf("scopes %v: allowed = %t, want %t", tc.scopes, got, tc.allowed)
		}
	}
}
