package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func postImportJSON(t *testing.T, server *Server, route string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

// A save into a project that does not exist is the operator's to fix, so
// every import save answers 404 naming it, before anything is written.
func TestImportSavesRefuseAnUnknownProject(t *testing.T) {
	server := newReimportTestServer(t)
	withFolder := func(script map[string]any, folderID string) map[string]any {
		copied := make(map[string]any, len(script)+1)
		for key, value := range script {
			copied[key] = value
		}
		copied["folder_id"] = folderID
		return copied
	}
	script := map[string]any{
		"name": "Orders", "version": "1.0.0", "protocol": "http", "project_id": "no-such-project",
		"config":  map[string]any{"base_url": "https://api.example.test"},
		"actions": []any{map[string]any{"name": "Get", "type": "request", "method": "GET", "target": "/"}},
	}
	for route, payload := range map[string]map[string]any{
		"/api/v1/scripts/import/commit-batch":                   {"scripts": []any{script}, "operation_id": "unknown-project-1", "source_profile": "har", "folder_id": "folder-in-another-project"},
		"/api/v1/scripts/import/asyncapi/commit-batch":          {"scripts": []any{script}, "operation_id": "unknown-project-2"},
		"/api/v1/scripts/import/enterprise-script-archive/save": {"scripts": []any{script}, "operation_id": "unknown-project-3"},
		"/api/v1/scripts/import/jmeter/save":                    {"scripts": []any{withFolder(script, "folder-in-another-project")}},
		"/api/v1/scripts/import/openapi/commit":                 {"script": script},
		"/api/v1/scripts/import/postman/save":                   {"script": script},
	} {
		response := postImportJSON(t, server, route, payload)
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "choose an existing project") {
			t.Errorf("%s: %d %.300s, want 404 naming the project", route, response.Code, response.Body.String())
		}
	}
	if scripts, err := server.store.ListScripts("no-such-project", nil); err == nil && len(scripts) != 0 {
		t.Fatalf("a refused save wrote %d scripts", len(scripts))
	}
}

// A commit of a script that is not valid says so in the operator's terms,
// not as an internal invariant.
func TestImportCommitNamesAnInvalidScript(t *testing.T) {
	server := newReimportTestServer(t)
	response := postImportJSON(t, server, "/api/v1/scripts/import/openapi/commit?project_id="+storage.DefaultProjectID,
		map[string]any{"script": map[string]any{"project_id": storage.DefaultProjectID}})
	body := response.Body.String()
	if response.Code != http.StatusBadRequest || !strings.Contains(body, "the script is not valid") || strings.Contains(body, "invariant") {
		t.Fatalf("invalid commit = %d %.300s, want 400 naming what is wrong", response.Code, body)
	}
}

// A repair request that does not decode says why.
func TestImportRepairNamesAnUndecodableRequest(t *testing.T) {
	server := newReimportTestServer(t)
	response := postImportJSON(t, server, "/api/v1/scripts/import/repairs/preview", map[string]any{"unknown_field": true})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unknown field") {
		t.Fatalf("repair preview = %d %.300s, want 400 naming the problem", response.Code, response.Body.String())
	}
}

func importJMeterWithScenario(t *testing.T, server *Server, path string) map[string]any {
	t.Helper()
	jmx := `<jmeterTestPlan version="1.2"><hashTree>
  <TestPlan testname="Checkout plan"/><hashTree>
    <ThreadGroup testname="Buyers">
      <stringProp name="ThreadGroup.num_threads">2</stringProp>
    </ThreadGroup><hashTree>
      <HTTPSamplerProxy testname="Home">
        <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
        <stringProp name="HTTPSampler.protocol">https</stringProp>
        <stringProp name="HTTPSampler.path">` + path + `</stringProp>
        <stringProp name="HTTPSampler.method">GET</stringProp>
      </HTTPSamplerProxy><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter?scenario_draft=true&filename=checkout.jmx&project_id="+storage.DefaultProjectID, strings.NewReader(jmx))
	request.Header.Set("Content-Type", "application/xml")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("JMeter import status = %d body=%.400s", response.Code, response.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// A JMeter scenario draft whose script still needs review is refused with
// what to do, and the scripts save without it.
func TestJMeterSaveRefusesAScenarioOfScriptsThatNeedReview(t *testing.T) {
	server := newReimportTestServer(t)
	imported := importJMeterWithScenario(t, server, "/orders/${orderId}")
	if imported["scenario_draft"] == nil {
		t.Fatal("the import made no scenario draft")
	}
	refused := postImportJSON(t, server, "/api/v1/scripts/import/jmeter/save", map[string]any{
		"scripts": imported["scripts"], "scenario_draft": imported["scenario_draft"],
	})
	if refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), importScenarioScriptsBlockedCode) ||
		!strings.Contains(refused.Body.String(), "save the scripts without the scenario draft") {
		t.Fatalf("scenario of a script needing review = %d %.400s, want 422 %s", refused.Code, refused.Body.String(), importScenarioScriptsBlockedCode)
	}
	if scripts, _ := server.store.ListScripts(storage.DefaultProjectID, nil); len(scripts) != 0 {
		t.Fatalf("a refused save wrote %d scripts", len(scripts))
	}
	saved := postImportJSON(t, server, "/api/v1/scripts/import/jmeter/save", map[string]any{"scripts": imported["scripts"]})
	if saved.Code != http.StatusCreated {
		t.Fatalf("save without the scenario = %d %.400s", saved.Code, saved.Body.String())
	}
}

type nilProjectStore struct {
	*libraryAtomicForwardingStore
}

func (s *nilProjectStore) GetProject(string) (*storage.Project, error) { return nil, nil }

// A store that answers a missing project with no error and no project still
// gets the 404, not a server error later.
func TestRequireImportProjectRefusesAProjectTheStoreDoesNotReturn(t *testing.T) {
	base := newReimportTestServer(t).store
	server := NewServer(ServerConfig{Store: &nilProjectStore{libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base}}})
	response := httptest.NewRecorder()
	if server.requireImportProject(response, "missing") || response.Code != http.StatusNotFound {
		t.Fatalf("missing project = %d %s, want 404", response.Code, response.Body.String())
	}
}
