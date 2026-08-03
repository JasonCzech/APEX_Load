package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The debug/replay prep hook must resolve object_ref bindings and block a run
// whose binding cannot be resolved and has no inline fallback — before any
// replay is launched.
func TestStartScriptDebugBlocksUnresolvedObjectRef(t *testing.T) {
	server := recordingTestServer(t)
	script := &dsl.Script{
		ID:       "s1",
		Name:     "x",
		Protocol: "browser",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []dsl.Action{{Name: "go", Type: "browser_click", ObjectRef: "missing"}},
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/s1/replay", strings.NewReader("{}"))
	w := httptest.NewRecorder()

	server.handleStartScriptDebugRequest(w, r, "s1", script, ScriptDebugStartRequest{})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "object_ref") {
		t.Fatalf("expected an object_ref issue, got: %s", w.Body.String())
	}
}

// from-script with bind_script:true seeds a repository AND rewrites the source
// script's actions to reference it by object_ref (keeping the inline selector as
// a fallback).
func TestObjectRepositoryFromScriptBindsScript(t *testing.T) {
	server := recordingTestServer(t)
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Login Flow",
		Protocol: "browser",
		Actions: []dsl.Action{{
			Name: "Search", Type: "browser_click", Selector: "#old-search",
			Metadata: map[string]any{selector.MetadataCandidatesKey: []selector.Candidate{
				{Kind: "css", Value: "#old-search", Confidence: 88, Reason: "captured css"},
			}},
		}},
	})

	body := `{"project_id":"` + stored.ProjectID + `","script_id":"` + stored.ID + `","bind_script":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/object-repositories/from-script", strings.NewReader(body))
	resp := httptest.NewRecorder()
	server.handleObjectRepositoryFromScript(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("from-script status = %d body=%s", resp.Code, resp.Body.String())
	}
	var repo storage.ObjectRepository
	json.NewDecoder(resp.Body).Decode(&repo)

	reloaded, ok := server.runner.GetScript(stored.ID)
	if !ok {
		t.Fatal("script missing after bind")
	}
	gotRef := reloaded.Script.Actions[0].ObjectRef
	wantRef := repo.ID + ":Search"
	if gotRef != wantRef {
		t.Fatalf("object_ref = %q, want %q", gotRef, wantRef)
	}
	if reloaded.Script.Actions[0].Selector != "#old-search" {
		t.Fatalf("inline selector should be preserved as a fallback, got %q", reloaded.Script.Actions[0].Selector)
	}
}
