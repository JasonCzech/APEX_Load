package api

import (
	"bytes"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/storage"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLibraryAssignmentAPI(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	if err := store.SaveProject(&storage.Project{ID: "assignment-project", Name: "Project"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplication(&storage.Application{ID: "assignment-app", Name: "Application", ProjectID: "assignment-project"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRelease(&storage.Release{ID: "assignment-release", Name: "Release", ApplicationID: "assignment-app"}); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string, status int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	var script StoredScript
	json.Unmarshal(call("POST", "/scripts", `{"project_id":"assignment-project","script":{"name":"Assignment script","protocol":"http","version":"1.0","config":{"base_url":"https://example.com"},"actions":[{"type":"request","name":"request","method":"GET","target":"https://example.com"}]}}`, http.StatusCreated), &script)
	var scenario StoredScenario
	json.Unmarshal(call("POST", "/scenarios", `{"name":"Assignment scenario","duration":"1m","groups":[{"script_id":"`+script.ID+`","vusers":1}]}`, http.StatusCreated), &scenario)
	for _, tc := range []struct{ kind, id string }{{"scripts", script.ID}, {"scenarios", scenario.ID}} {
		t.Run(tc.kind, func(t *testing.T) {
			path := "/" + tc.kind + "/" + tc.id
			call("PUT", path+"/library-assignment", `{"application_id":"assignment-app","release_ids":["assignment-release"],"expected_revision":0}`, 200)
			call("PUT", path+"/library-assignment", `{"application_id":null,"release_ids":[],"expected_revision":0}`, 409)
			call("PUT", path+"/library-assignment", `{"application_id":null}`, 400)
			var read struct {
				Assignment storage.LibraryAssignment `json:"library_assignment"`
			}
			json.Unmarshal(call("GET", path, "", 200), &read)
			if read.Assignment.Application == nil || read.Assignment.Revision != 1 || len(read.Assignment.Releases) != 1 {
				t.Fatalf("read assignment: %+v", read)
			}
			if !bytes.Contains(call("GET", "/"+tc.kind+"?project_id=assignment-project", "", 200), []byte(`"library_assignment"`)) {
				t.Fatal("list missing assignments")
			}
			call("PUT", path+"/library-assignment", `{"application_id":null,"release_ids":[],"expected_revision":1}`, 200)
		})
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{ProjectID: "assignment-project", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Action == "script.library_assignment.update" || event.Action == "scenario.library_assignment.update" {
			count++
		}
	}
	if count != 4 {
		t.Fatalf("assignment audit count %d", count)
	}
}
