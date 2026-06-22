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

func TestObjectRepositoryCRUDHandlers(t *testing.T) {
	server := recordingTestServer(t)

	// Create.
	createBody := `{"project_id":"p1","name":"Checkout","objects":[{"logical_name":"go","css":"#go"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/object-repositories", strings.NewReader(createBody))
	resp := httptest.NewRecorder()
	server.handleCreateObjectRepository(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", resp.Code, resp.Body.String())
	}
	var created storage.ObjectRepository
	json.NewDecoder(resp.Body).Decode(&created)
	if created.ID == "" || len(created.Objects) != 1 {
		t.Fatalf("unexpected created repo: %#v", created)
	}

	// Reject an object with no logical name.
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/object-repositories", strings.NewReader(`{"project_id":"p1","name":"x","objects":[{"css":"#a"}]}`))
	badResp := httptest.NewRecorder()
	server.handleCreateObjectRepository(badResp, bad)
	if badResp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing logical_name, got %d", badResp.Code)
	}

	// List (project-scoped).
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/object-repositories?project_id=p1", nil)
	listResp := httptest.NewRecorder()
	server.handleListObjectRepositories(listResp, listReq)
	var list struct {
		Count int `json:"count"`
	}
	json.NewDecoder(listResp.Body).Decode(&list)
	if list.Count != 1 {
		t.Fatalf("list count = %d, want 1", list.Count)
	}

	// Get.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/object-repositories/"+created.ID, nil)
	getReq.SetPathValue("id", created.ID)
	getResp := httptest.NewRecorder()
	server.handleGetObjectRepository(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get status = %d", getResp.Code)
	}

	// Update (replace objects).
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/object-repositories/"+created.ID,
		strings.NewReader(`{"name":"Checkout v2","objects":[{"logical_name":"go","css":"#go"},{"logical_name":"cancel","css":"#cancel"}]}`))
	putReq.SetPathValue("id", created.ID)
	putResp := httptest.NewRecorder()
	server.handleUpdateObjectRepository(putResp, putReq)
	var updated storage.ObjectRepository
	json.NewDecoder(putResp.Body).Decode(&updated)
	if updated.Name != "Checkout v2" || len(updated.Objects) != 2 || updated.ProjectID != "p1" {
		t.Fatalf("update did not apply / preserve project: %#v", updated)
	}

	// Delete.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/object-repositories/"+created.ID, nil)
	delReq.SetPathValue("id", created.ID)
	delResp := httptest.NewRecorder()
	server.handleDeleteObjectRepository(delResp, delReq)
	if delResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d", delResp.Code)
	}
}

func TestUpdateObjectRepositoryPreservesOmittedFields(t *testing.T) {
	server := recordingTestServer(t)

	// Seed a script-sourced repo that has opted into auto-promote-on-heal.
	seed := &storage.ObjectRepository{
		ID:                "repo-merge",
		ProjectID:         "p1",
		Name:              "Checkout",
		Source:            "script:script-7",
		AutoPromoteOnHeal: true,
		Objects:           []storage.RepositoryObject{{LogicalName: "go", CSS: "#go"}},
	}
	if err := server.store.SaveObjectRepository(seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	// A partial update (e.g. a candidate promotion) that omits source and
	// auto_promote_on_heal must NOT wipe them.
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/object-repositories/"+seed.ID,
		strings.NewReader(`{"name":"Checkout","description":"","objects":[{"logical_name":"go","css":"#go2"}]}`))
	putReq.SetPathValue("id", seed.ID)
	putResp := httptest.NewRecorder()
	server.handleUpdateObjectRepository(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", putResp.Code, putResp.Body.String())
	}
	var updated storage.ObjectRepository
	json.NewDecoder(putResp.Body).Decode(&updated)
	if updated.Source != "script:script-7" {
		t.Fatalf("source dropped on partial update: %q", updated.Source)
	}
	if !updated.AutoPromoteOnHeal {
		t.Fatal("auto_promote_on_heal wiped on partial update that omitted it")
	}

	// An explicit toggle that DOES send auto_promote_on_heal:false must still apply.
	offReq := httptest.NewRequest(http.MethodPut, "/api/v1/object-repositories/"+seed.ID,
		strings.NewReader(`{"name":"Checkout","objects":[{"logical_name":"go","css":"#go2"}],"auto_promote_on_heal":false}`))
	offReq.SetPathValue("id", seed.ID)
	offResp := httptest.NewRecorder()
	server.handleUpdateObjectRepository(offResp, offReq)
	if offResp.Code != http.StatusOK {
		t.Fatalf("toggle-off status = %d body=%s", offResp.Code, offResp.Body.String())
	}
	var toggled storage.ObjectRepository
	json.NewDecoder(offResp.Body).Decode(&toggled)
	if toggled.AutoPromoteOnHeal {
		t.Fatal("explicit auto_promote_on_heal:false did not take effect")
	}
	if toggled.Source != "script:script-7" {
		t.Fatalf("source dropped on explicit toggle: %q", toggled.Source)
	}
}

func TestObjectRepositoryObjectsFromScriptExcludesAmbiguousNames(t *testing.T) {
	script := &dsl.Script{
		Actions: []dsl.Action{
			// Two "Continue" buttons with DIFFERENT selectors → ambiguous → excluded,
			// so neither is silently re-targeted at the other's locator.
			{Name: "Continue", Type: "browser_click", Selector: "#page1-continue"},
			{Name: "Continue", Type: "browser_click", Selector: "#page2-continue"},
			// A uniquely-named element is still collected.
			{Name: "Submit", Type: "browser_click", Selector: "#submit"},
			// Same name + SAME selector is the same element → one object, both bind.
			{Name: "Logout", Type: "browser_click", Selector: "#logout"},
			{Name: "Logout", Type: "browser_click", Selector: "#logout"},
		},
	}
	objects := objectRepositoryObjectsFromScript(script, "script-x")
	names := map[string]bool{}
	for _, o := range objects {
		names[o.LogicalName] = true
	}
	if names["Continue"] {
		t.Error("ambiguous 'Continue' (two different selectors) must be excluded from the repository")
	}
	if !names["Submit"] {
		t.Error("uniquely-named 'Submit' must be collected")
	}
	if !names["Logout"] {
		t.Error("same-name same-selector 'Logout' must still be collected")
	}
}

func TestObjectRepositoryFromScript(t *testing.T) {
	server := recordingTestServer(t)

	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Login Flow",
		Protocol: "browser",
		Actions: []dsl.Action{
			{Name: "Search", Type: "browser_click", Selector: "#old-search", Metadata: map[string]any{
				selector.MetadataCandidatesKey: []selector.Candidate{
					{Kind: "css", Value: "#old-search", Confidence: 88, Reason: "captured css"},
					{Kind: "role", Value: "role=searchbox", Confidence: 72, Reason: "role"},
				},
			}},
			{Name: "Think", Type: "think_time"}, // no selector → skipped
		},
	})

	body := `{"script_id":"` + stored.ID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/object-repositories/from-script", strings.NewReader(body))
	resp := httptest.NewRecorder()
	server.handleObjectRepositoryFromScript(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("from-script status = %d body=%s", resp.Code, resp.Body.String())
	}
	var repo storage.ObjectRepository
	json.NewDecoder(resp.Body).Decode(&repo)
	if len(repo.Objects) != 1 {
		t.Fatalf("expected 1 seeded object (think_time skipped), got %d: %#v", len(repo.Objects), repo.Objects)
	}
	object := repo.Objects[0]
	if object.LogicalName != "Search" || object.CSS != "#old-search" || object.Role != "role=searchbox" {
		t.Errorf("object not seeded from candidates: %#v", object)
	}
	if object.BestConfidence != 88 || len(object.Candidates) != 2 || object.SourceScriptID != stored.ID {
		t.Errorf("ranking/source not carried: best=%d candidates=%d source=%q", object.BestConfidence, len(object.Candidates), object.SourceScriptID)
	}

	// Unknown script → 404.
	missing := httptest.NewRequest(http.MethodPost, "/api/v1/object-repositories/from-script", strings.NewReader(`{"script_id":"nope"}`))
	missingResp := httptest.NewRecorder()
	server.handleObjectRepositoryFromScript(missingResp, missing)
	if missingResp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown script, got %d", missingResp.Code)
	}
}
