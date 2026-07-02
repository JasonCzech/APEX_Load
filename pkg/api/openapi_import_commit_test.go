package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func openAPIKeyedAction(name, operationKey string) dsl.Action {
	return dsl.Action{
		Name: name,
		Type: "request",
		Metadata: map[string]any{
			"openapi": map[string]any{"operation_key": operationKey},
		},
	}
}

func csvDataSource(name, inline string, columns ...string) dsl.DataSource {
	return dsl.DataSource{
		Name:           name,
		Type:           "csv",
		InlineData:     inline,
		Strategy:       "sequential",
		Delimiter:      ",",
		FirstRowHeader: true,
		Columns:        columns,
	}
}

func TestDiffOpenAPIImportFullReplace(t *testing.T) {
	existing := &dsl.Script{Actions: []dsl.Action{
		openAPIKeyedAction("GetA", "GET /a"),
		openAPIKeyedAction("GetB", "GET /b"),
	}}
	imported := &dsl.Script{Actions: []dsl.Action{
		openAPIKeyedAction("GetA", "GET /a"),
		openAPIKeyedAction("GetB", "GET /b"),
		openAPIKeyedAction("GetC", "GET /c"),
	}}
	diff := diffOpenAPIImport(existing, imported, "full")
	if diff.Added != 3 || diff.Removed != 2 {
		t.Fatalf("full diff = %#v, want Added=3 Removed=2", diff)
	}
	if diff.Updated != 0 {
		t.Fatalf("full diff Updated = %d, want 0", diff.Updated)
	}
}

func TestDiffOpenAPIImportMergeByOperationID(t *testing.T) {
	existing := &dsl.Script{
		Actions: []dsl.Action{
			openAPIKeyedAction("GetA", "GET /a"),
			openAPIKeyedAction("PostB", "POST /b"),
			{Name: "ManualStep", Type: "request"}, // no openapi key -> preserved
		},
		DataSources: []dsl.DataSource{csvDataSource("openapi", "id\nord-1\n", "id")},
		Parameters:  []dsl.Parameter{{Name: "p1", Type: "list"}},
	}
	imported := &dsl.Script{Actions: []dsl.Action{
		openAPIKeyedAction("PostB", "POST /b"), // updated
		openAPIKeyedAction("GetC", "GET /c"),   // added
	}}
	diff := diffOpenAPIImport(existing, imported, "merge_by_operation_id")
	if diff.Updated != 1 || diff.Added != 1 || diff.Removed != 1 {
		t.Fatalf("merge diff = %#v, want Updated=1 Added=1 Removed=1", diff)
	}
	if diff.Preserved != 1 {
		t.Fatalf("merge diff Preserved = %d, want 1 (manual action)", diff.Preserved)
	}
	if diff.PreservedDataSources != 1 || diff.PreservedParameters != 1 {
		t.Fatalf("merge diff data preservation = %#v, want 1 data source + 1 parameter preserved", diff)
	}
	joined := strings.Join(diff.Items, "|")
	if !strings.Contains(joined, "Preserved 1 existing data source") || !strings.Contains(joined, "Preserved 1 existing parameter") {
		t.Fatalf("merge diff items missing preservation notes: %#v", diff.Items)
	}
}

func TestMergeOpenAPIImportScriptPreservesUserCustomizations(t *testing.T) {
	folder := "folder-7"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	existing := &dsl.Script{
		ID:        "real-id",
		ProjectID: "owner-project",
		FolderID:  &folder,
		CreatedAt: created,
		Actions: []dsl.Action{
			openAPIKeyedAction("GetA", "GET /a"),
			{Name: "ManualStep", Type: "request"},
		},
		// Existing data source carries a user-seeded extra row + values.
		DataSources: []dsl.DataSource{csvDataSource("openapi", "id,name\nord-1,Acme\nord-2,Beta\n", "id", "name")},
		Parameters:  []dsl.Parameter{{Name: "p1", Type: "list"}},
		Config:      dsl.ScriptConfig{BaseURL: "https://kept.example"},
	}
	imported := &dsl.Script{
		ProjectID: "attacker-project",
		Actions: []dsl.Action{
			openAPIKeyedAction("GetA", "GET /a"), // updated version of A
			openAPIKeyedAction("PostB", "POST /b"),
		},
		DataSources: []dsl.DataSource{csvDataSource("openapi", "id,name,email\nsample-id,sample-name,user@example.test\n", "id", "name", "email")},
		Parameters:  []dsl.Parameter{{Name: "p2", Type: "list"}},
		Config:      dsl.ScriptConfig{BaseURL: "https://fresh.example"},
	}

	merged := mergeOpenAPIImportScript(existing, imported)

	// Manual action preserved; both imported operations present.
	names := map[string]bool{}
	for _, a := range merged.Actions {
		names[a.Name] = true
	}
	if !names["ManualStep"] || !names["GetA"] || !names["PostB"] {
		t.Fatalf("merged actions missing expected entries: %#v", names)
	}

	// Config is preserved (user's base URL kept, not clobbered by the import).
	if merged.Config.BaseURL != "https://kept.example" {
		t.Fatalf("merged base URL = %q, want existing kept", merged.Config.BaseURL)
	}

	// Parameters unioned by name.
	paramNames := map[string]bool{}
	for _, p := range merged.Parameters {
		paramNames[p.Name] = true
	}
	if !paramNames["p1"] || !paramNames["p2"] {
		t.Fatalf("merged parameters = %#v, want p1 and p2", paramNames)
	}

	// Data source: existing seed rows preserved, new column merged in.
	if len(merged.DataSources) != 1 {
		t.Fatalf("merged data sources = %d, want 1", len(merged.DataSources))
	}
	inline := merged.DataSources[0].InlineData
	for _, want := range []string{"ord-1", "Acme", "ord-2", "Beta", "email"} {
		if !strings.Contains(inline, want) {
			t.Fatalf("merged data source lost %q: %q", want, inline)
		}
	}
}

// TestMergeOpenAPIImportScriptPreservesInitTeardown guards B3: a merge re-import
// must not silently drop the user's manual init/teardown steps. The importer's
// re-synthesized lifecycle step (matched by type+name) wins, but everything the
// import does not re-emit survives.
func TestMergeOpenAPIImportScriptPreservesInitTeardown(t *testing.T) {
	existing := &dsl.Script{
		ID: "real-id",
		Init: []dsl.Action{
			{Name: "ManualLogin", Type: "request", Method: "POST", Target: "/login"},
			{Name: "TokenFetch", Type: "http_oauth2_token"},
		},
		Teardown: []dsl.Action{
			{Name: "ManualCleanup", Type: "request", Method: "DELETE", Target: "/session"},
		},
		Actions: []dsl.Action{openAPIKeyedAction("GetA", "GET /a")},
	}
	imported := &dsl.Script{
		// Fresh import re-synthesizes the OAuth token init (same type+name) and
		// carries no manual setup/cleanup of its own.
		Init:    []dsl.Action{{Name: "TokenFetch", Type: "http_oauth2_token"}},
		Actions: []dsl.Action{openAPIKeyedAction("GetA", "GET /a")},
	}

	merged := mergeOpenAPIImportScript(existing, imported)

	initNames := map[string]int{}
	for _, a := range merged.Init {
		initNames[a.Name]++
	}
	if initNames["ManualLogin"] != 1 {
		t.Fatalf("manual init step lost: %#v", merged.Init)
	}
	if initNames["TokenFetch"] != 1 {
		t.Fatalf("synthesized init step should appear exactly once (deduped), got %d: %#v", initNames["TokenFetch"], merged.Init)
	}
	if len(merged.Teardown) != 1 || merged.Teardown[0].Name != "ManualCleanup" {
		t.Fatalf("manual teardown step lost: %#v", merged.Teardown)
	}
	if len(merged.Init) != 2 || merged.Init[0].Name != "ManualLogin" || merged.Init[1].Name != "TokenFetch" {
		t.Fatalf("init order = %#v, want ManualLogin before refreshed TokenFetch", merged.Init)
	}

	// When the import emits no init of its own, the existing init is preserved wholesale.
	importedNoInit := &dsl.Script{Actions: []dsl.Action{openAPIKeyedAction("GetA", "GET /a")}}
	merged2 := mergeOpenAPIImportScript(existing, importedNoInit)
	if len(merged2.Init) != 2 {
		t.Fatalf("existing init dropped when import had none: %#v", merged2.Init)
	}
}

func TestDiffOpenAPIImportReportsLifecycleChanges(t *testing.T) {
	existing := &dsl.Script{
		Init:    []dsl.Action{{Name: "SeedTenant", Type: "request", Method: "POST", Target: "/session"}},
		Actions: []dsl.Action{openAPIKeyedAction("GetA", "GET /a")},
	}
	imported := &dsl.Script{
		Init:    []dsl.Action{{Name: "TokenFetch", Type: "http_oauth2_token"}},
		Actions: []dsl.Action{openAPIKeyedAction("GetA", "GET /a")},
	}
	diff := diffOpenAPIImport(existing, imported, "merge")
	if !containsString(diff.Items, "Updated init lifecycle (2 step(s))") {
		t.Fatalf("diff items = %#v, want lifecycle update disclosure", diff.Items)
	}
}

func TestPreserveReplacedScriptIdentity(t *testing.T) {
	folder := "owner-folder"
	created := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	existing := &dsl.Script{ID: "real-id", ProjectID: "owner", FolderID: &folder, CreatedAt: created}
	// A posted body attempting to relocate the script and reset its identity.
	next := &dsl.Script{ID: "spoofed", ProjectID: "attacker"}

	preserveReplacedScriptIdentity(next, existing)

	if next.ID != "real-id" || next.ProjectID != "owner" {
		t.Fatalf("identity not preserved: ID=%q ProjectID=%q", next.ID, next.ProjectID)
	}
	if next.FolderID == nil || *next.FolderID != "owner-folder" {
		t.Fatalf("folder not preserved: %#v", next.FolderID)
	}
	if !next.CreatedAt.Equal(created) {
		t.Fatalf("created-at not preserved: %v", next.CreatedAt)
	}
}

func TestMergeCSVDataSourceUnionsColumns(t *testing.T) {
	existing := csvDataSource("openapi", "id,name\nord-1,Acme\nord-2,Beta\n", "id", "name")
	imported := csvDataSource("openapi", "id,name,email\nsample-id,sample-name,user@example.test\n", "id", "name", "email")

	merged, newCols := mergeCSVDataSource(existing, imported)
	if len(newCols) != 1 || newCols[0] != "email" {
		t.Fatalf("new columns = %#v, want [email]", newCols)
	}
	header, rows := parseCSVInline(merged.InlineData)
	if strings.Join(header, ",") != "id,name,email" {
		t.Fatalf("merged header = %#v", header)
	}
	if len(rows) != 2 {
		t.Fatalf("merged rows = %d, want 2 (existing rows preserved)", len(rows))
	}
	// Existing values kept; new column seeded from the import sample.
	if rows[0][0] != "ord-1" || rows[0][1] != "Acme" || rows[0][2] != "user@example.test" {
		t.Fatalf("row 0 = %#v", rows[0])
	}
	if rows[1][0] != "ord-2" || rows[1][1] != "Beta" || rows[1][2] != "user@example.test" {
		t.Fatalf("row 1 = %#v", rows[1])
	}
}

func TestMergeCSVDataSourceNoNewColumnsKeepsExisting(t *testing.T) {
	existing := csvDataSource("openapi", "id\nord-1\nord-2\n", "id")
	imported := csvDataSource("openapi", "id\nsample-id\n", "id")
	merged, newCols := mergeCSVDataSource(existing, imported)
	if len(newCols) != 0 {
		t.Fatalf("new columns = %#v, want none", newCols)
	}
	if merged.InlineData != existing.InlineData {
		t.Fatalf("existing data source should be unchanged, got %q", merged.InlineData)
	}
}

func TestMergeOpenAPIParametersUnionByName(t *testing.T) {
	existing := []dsl.Parameter{{Name: "p1"}, {Name: "p2"}}
	imported := []dsl.Parameter{{Name: "p2"}, {Name: "p3"}}
	merged := mergeImportParameters(existing, imported)
	if len(merged) != 3 {
		t.Fatalf("merged params = %d, want 3 (p1,p2,p3 deduped)", len(merged))
	}
	names := []string{merged[0].Name, merged[1].Name, merged[2].Name}
	if strings.Join(names, ",") != "p1,p2,p3" {
		t.Fatalf("merged param order = %#v, want p1,p2,p3", names)
	}
}

func TestMergeOpenAPIConfigKeepsExistingBaseURL(t *testing.T) {
	kept := mergeImportConfig(dsl.ScriptConfig{BaseURL: "https://kept"}, dsl.ScriptConfig{BaseURL: "https://fresh"})
	if kept.BaseURL != "https://kept" {
		t.Fatalf("base URL = %q, want existing kept", kept.BaseURL)
	}
	filled := mergeImportConfig(dsl.ScriptConfig{}, dsl.ScriptConfig{BaseURL: "https://fresh"})
	if filled.BaseURL != "https://fresh" {
		t.Fatalf("empty existing base URL should fall back to import, got %q", filled.BaseURL)
	}
}
