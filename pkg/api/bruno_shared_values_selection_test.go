package api

import (
	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
)

func TestBrunoSharedValuesRebaseRetainsIdentityAndSelectedLocations(t *testing.T) {
	makeScript := func(name string) *dsl.Script {
		return &dsl.Script{Name: name, Actions: []dsl.Action{{Type: "http_request", Method: "GET", Target: "https://example.test", Headers: map[string]string{"X-Tenant": "original"}}}}
	}
	original := []*dsl.Script{makeScript("first"), makeScript("second")}
	trusted := trustedSharedValueCandidates(original)
	var id string
	for _, candidate := range trusted {
		if candidate.Name == "X_Tenant" {
			id = candidate.ID
		}
	}
	if id == "" {
		t.Fatal("expected repeated header candidate")
	}
	selected := []*dsl.Script{makeScript("second")}
	rebased := rebaseBrunoSharedValues(trusted, map[int]int{1: 0})
	if _, err := sharedvalue.Apply(selected, rebased, map[string]string{id: "replacement"}); err != nil {
		t.Fatal(err)
	}
	if selected[0].Actions[0].Headers["X-Tenant"] != "{{param.X_Tenant}}" {
		t.Fatalf("selected header = %q", selected[0].Actions[0].Headers["X-Tenant"])
	}
	if len(selected[0].Parameters) != 1 || selected[0].Parameters[0].Values[0] != "replacement" {
		t.Fatal("selected parameter missing")
	}
	if original[0].Actions[0].Headers["X-Tenant"] != "original" || original[1].Actions[0].Headers["X-Tenant"] != "original" {
		t.Fatal("inspection was mutated")
	}
	for _, candidate := range trusted {
		if candidate.ID == id && len(candidate.ScriptIndexes) != 2 {
			t.Fatal("original locations were mutated")
		}
	}
	empty := rebaseBrunoSharedValues(trusted, map[int]int{})
	if diffs, err := sharedvalue.Apply([]*dsl.Script{makeScript("unrelated")}, empty, map[string]string{id: "ignored"}); err != nil || len(diffs) != 0 {
		t.Fatalf("deselected edit = %v, %v", diffs, err)
	}
}

func TestBrunoSharedValuesPartialBatchCommit(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{Path: "demo/opencollection.yml", Content: "opencollection: 1.0.0\ninfo: {name: Selected values, uid: selected-values}\n"},
		{Path: "demo/first.yml", Content: "info: {name: First, uid: first, type: http, seq: 1}\nhttp:\n  method: GET\n  url: https://example.test/first\n  headers:\n    - {name: X-Tenant, value: original}\n"},
		{Path: "demo/second.yml", Content: "info: {name: Second, uid: second, type: http, seq: 2}\nhttp:\n  method: GET\n  url: https://example.test/second\n  headers:\n    - {name: X-Tenant, value: original}\n"},
	})
	var id string
	for _, value := range inspection.SharedValues.Candidates {
		if value.Name == "X_Tenant" {
			id = value.ID
		}
	}
	if id == "" {
		t.Fatal("inspection omitted repeated header")
	}
	folder := saveBrunoNativeScriptFolder(t, store, brunoImportTestProject, "selected-values")
	selected := inspection.Result.Scripts[1].CandidateID
	request := brunoCommitRequest{ProjectID: brunoImportTestProject, ImportSessionID: inspection.ImportSessionID, Mode: "create_native_batch", FolderID: &folder, CandidateIDs: []string{selected}, SharedValues: map[string]string{"unknown-id": "rejected"}}
	if response := commitBrunoImportWithStatus(t, server, request); response.Code != 409 {
		t.Fatalf("unknown ID status = %d", response.Code)
	}
	request.SharedValues = map[string]string{id: "selected-tenant"}
	response := commitBrunoImportWithStatus(t, server, request)
	if response.Code != 201 {
		t.Fatalf("partial commit = %d %s", response.Code, response.Body.String())
	}
	result := decodeBrunoNativeBatchTestResponse(t, response)
	if len(result.Scripts) != 1 || result.Scripts[0].Name != "Second" {
		t.Fatal("unexpected saved selection")
	}
	if len(result.Scripts[0].Parameters) != 1 || result.Scripts[0].Parameters[0].Values[0] != "selected-tenant" {
		t.Fatalf("substitution missing: %#v", result.Scripts[0].Parameters)
	}
	replay := commitBrunoImportWithStatus(t, server, request)
	if replay.Code != 200 {
		t.Fatalf("retry = %d %s", replay.Code, replay.Body.String())
	}
}
