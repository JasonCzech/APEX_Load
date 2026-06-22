package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func postmanCommitTestAction(name, folder, item, method, target string) dsl.Action {
	meta := map[string]any{"postman_item": item}
	if folder != "" {
		meta["postman_folder"] = folder
	}
	return dsl.Action{Name: name, Type: "request", Method: method, Target: target, Metadata: meta}
}

func postmanCommitTestScript(name, projectID string, actions []dsl.Action) dsl.Script {
	return dsl.Script{
		Name:      name,
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions:   actions,
	}
}

func TestPostmanActionKeyUsesFolderAndItem(t *testing.T) {
	if got := postmanActionKey(postmanCommitTestAction("A", "Orders", "Get Order", "GET", "/o")); got != "Orders / Get Order" {
		t.Fatalf("folder+item key = %q", got)
	}
	if got := postmanActionKey(postmanCommitTestAction("A", "", "Ping", "GET", "/p")); got != "Ping" {
		t.Fatalf("item-only key = %q", got)
	}
	if got := postmanActionKey(dsl.Action{Name: "Manual"}); got != "" {
		t.Fatalf("manual action key = %q, want empty", got)
	}
}

func TestMergePostmanImportScriptPreservesManualAndData(t *testing.T) {
	existing := postmanCommitTestScript("Target", "proj", []dsl.Action{
		postmanCommitTestAction("GetOrder", "Orders", "Get Order", "GET", "/orders/1"),
		{Name: "ManualStep", Type: "request", Method: "GET", Target: "/manual"},
	})
	existing.DataSources = []dsl.DataSource{csvDataSource("postman", "id\nord-1\n", "id")}
	existing.Parameters = []dsl.Parameter{{Name: "p1", Type: "list"}}
	imported := postmanCommitTestScript("Reimport", "other", []dsl.Action{
		postmanCommitTestAction("GetOrderUpdated", "Orders", "Get Order", "GET", "/orders/{{var.x}}"),
		postmanCommitTestAction("CreateOrder", "Orders", "Create Order", "POST", "/orders"),
	})

	merged := mergePostmanImportScript(&existing, &imported)

	names := map[string]bool{}
	for _, a := range merged.Actions {
		names[a.Name] = true
	}
	if !names["ManualStep"] || !names["GetOrderUpdated"] || !names["CreateOrder"] {
		t.Fatalf("merged actions = %#v", names)
	}
	if names["GetOrder"] {
		t.Fatalf("old 'Get Order' should be replaced by the re-imported version")
	}
	if len(merged.DataSources) != 1 || len(merged.Parameters) != 1 {
		t.Fatalf("data/params not preserved: dataSources=%d params=%d", len(merged.DataSources), len(merged.Parameters))
	}
}

func TestHandleSavePostmanImportCreateAndMerge(t *testing.T) {
	server := NewServer(ServerConfig{})

	createBody, err := json.Marshal(map[string]any{
		"script": postmanCommitTestScript("Created PM", "proj-pm", []dsl.Action{
			postmanCommitTestAction("GetOrder", "Orders", "Get Order", "GET", "/orders/1"),
		}),
	})
	if err != nil {
		t.Fatalf("marshal create: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var created struct {
		StoredScript StoredScript      `json:"stored_script"`
		Diff         openAPIImportDiff `json:"diff"`
		Preflight    *ReplayPreflight  `json:"preflight"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.StoredScript.ID == "" || created.Diff.Added != 1 || created.Preflight == nil {
		t.Fatalf("create response = %+v", created)
	}

	// Merge into an existing script that has a hand-added action; the merge must
	// replace the re-imported request, keep the manual action, and preserve the
	// target's project (not relocate it to the posted body's project).
	existing := postmanCommitTestScript("Merge Target", "proj-pm", []dsl.Action{
		postmanCommitTestAction("GetOrder", "Orders", "Get Order", "GET", "/orders/1"),
		{Name: "CustomAudit", Type: "request", Method: "GET", Target: "/custom-audit"},
	})
	storedExisting := server.runner.StoreScript(&existing)

	mergeBody, err := json.Marshal(map[string]any{
		"mode":             "replace",
		"target_script_id": storedExisting.ID,
		"replace_mode":     "merge_by_item",
		"script": postmanCommitTestScript("Merged PM", "attacker-proj", []dsl.Action{
			postmanCommitTestAction("GetOrderUpdated", "Orders", "Get Order", "GET", "/orders/{{var.x}}"),
			postmanCommitTestAction("CreateOrder", "Orders", "Create Order", "POST", "/orders"),
		}),
	})
	if err != nil {
		t.Fatalf("marshal merge: %v", err)
	}
	mergeReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(mergeBody))
	mergeReq.Header.Set("Content-Type", "application/json")
	mergeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(mergeResp, mergeReq)
	if mergeResp.Code != http.StatusOK {
		t.Fatalf("merge status = %d body=%s", mergeResp.Code, mergeResp.Body.String())
	}
	var merged struct {
		StoredScript StoredScript      `json:"stored_script"`
		Replaced     bool              `json:"replaced"`
		Diff         openAPIImportDiff `json:"diff"`
	}
	if err := json.Unmarshal(mergeResp.Body.Bytes(), &merged); err != nil {
		t.Fatalf("unmarshal merge: %v", err)
	}
	if !merged.Replaced || merged.Diff.Updated != 1 || merged.Diff.Added != 1 || merged.Diff.Preserved != 1 {
		t.Fatalf("merge diff = %+v, want updated=1 added=1 preserved=1", merged.Diff)
	}
	if merged.StoredScript.ID != storedExisting.ID {
		t.Fatalf("merge changed stored ID: %q != %q", merged.StoredScript.ID, storedExisting.ID)
	}
	if merged.StoredScript.Script.ProjectID != "proj-pm" {
		t.Fatalf("merge relocated project: %q, want proj-pm preserved", merged.StoredScript.Script.ProjectID)
	}
	if len(merged.StoredScript.Script.Actions) != 3 || merged.StoredScript.Script.Actions[2].Name != "CustomAudit" {
		t.Fatalf("merged actions = %+v, want manual action preserved after imported requests", merged.StoredScript.Script.Actions)
	}
}

func TestHandleSavePostmanImportRejectsBadReplaceMode(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, _ := json.Marshal(map[string]any{
		"mode":             "replace",
		"target_script_id": "x",
		"replace_mode":     "bogus",
		"script":           postmanCommitTestScript("X", "p", []dsl.Action{postmanCommitTestAction("A", "", "Get", "GET", "/a")}),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid replace_mode body=%s", resp.Code, resp.Body.String())
	}
}

func TestHandlePostmanSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/postman/support-matrix", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("support matrix status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		SupportMatrix []postman.SupportMatrixEntry `json:"support_matrix"`
		Notes         []string                     `json:"notes"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal support matrix: %v", err)
	}
	if len(payload.SupportMatrix) == 0 || len(payload.Notes) == 0 {
		t.Fatalf("support matrix empty: %+v", payload)
	}
}
