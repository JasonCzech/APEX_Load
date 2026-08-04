package api

import (
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
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

func governedCSVAsset(t *testing.T, projectID, content string) string {
	t.Helper()
	root := t.TempDir()
	if err := fileasset.RegisterProjectRoot(root); err != nil {
		t.Fatalf("register project asset root: %v", err)
	}
	projectDir, err := fileasset.ProjectAssetDir(root, projectID)
	if err != nil {
		t.Fatalf("resolve project asset directory: %v", err)
	}
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatalf("create project asset directory: %v", err)
	}
	path := filepath.Join(projectDir, "data.csv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write project asset: %v", err)
	}
	return path
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

func TestMergeCSVDataSourcePreservesExistingExternalAssetConfiguration(t *testing.T) {
	existing := dsl.DataSource{
		Name: "users", Type: "csv", Path: "/operator/users.tsv", StorageRef: "asset://users-v2",
		Strategy: "unique", Delimiter: "tab", FirstRowHeader: true, Columns: []string{"id", "name"},
	}
	imported := dsl.DataSource{
		Name: "users", Type: "csv", Strategy: "sequential", Delimiter: "comma",
		InlineData: "id,email\n1,a@example.test\n", Columns: []string{"id", "email"},
	}

	merged, added := mergeCSVDataSource(existing, imported)
	if merged.Path != existing.Path || merged.StorageRef != existing.StorageRef || merged.Strategy != "unique" || merged.Delimiter != "tab" || !merged.FirstRowHeader {
		t.Fatalf("external source configuration was replaced: %#v", merged)
	}
	if merged.InlineData != "" {
		t.Fatalf("external source received imported inline data: %q", merged.InlineData)
	}
	if !slices.Equal(merged.Columns, []string{"id", "name"}) || len(added) != 0 {
		t.Fatalf("external asset advertised unavailable columns: columns=%#v added=%#v", merged.Columns, added)
	}
}

func TestMergeCSVDataSourceHonorsHeaderlessTabSources(t *testing.T) {
	existing := dsl.DataSource{
		Name:           "users",
		Type:           "csv",
		InlineData:     "1\tAlice\n2\tBob\n",
		Strategy:       "sequential",
		Delimiter:      "auto",
		FirstRowHeader: false,
		Columns:        []string{"id", "name"},
	}
	imported := dsl.DataSource{
		Name:           "users",
		Type:           "csv",
		InlineData:     "sample-id\tuser@example.test\n",
		Strategy:       "sequential",
		Delimiter:      "tab",
		FirstRowHeader: false,
		Columns:        []string{"id", "email"},
	}

	merged, added := mergeCSVDataSource(existing, imported)
	if !slices.Equal(added, []string{"email"}) {
		t.Fatalf("added columns = %#v, want email", added)
	}
	if merged.FirstRowHeader {
		t.Fatal("headerless source was changed to first_row_header=true")
	}
	if merged.Delimiter != "tab" {
		t.Fatalf("normalized delimiter = %q, want tab", merged.Delimiter)
	}
	if strings.HasPrefix(merged.InlineData, "id\t") || strings.Contains(merged.InlineData, ",") {
		t.Fatalf("headerless tab data was rendered with the wrong shape: %q", merged.InlineData)
	}
	header, rows := parseCSVDataSource(merged)
	if !slices.Equal(header, []string{"id", "name", "email"}) {
		t.Fatalf("runtime columns = %#v", header)
	}
	wantRows := [][]string{{"1", "Alice", "user@example.test"}, {"2", "Bob", "user@example.test"}}
	if !slices.EqualFunc(rows, wantRows, func(left, right []string) bool { return slices.Equal(left, right) }) {
		t.Fatalf("runtime rows = %#v, want %#v", rows, wantRows)
	}
}

func TestMergeImportUsesGovernedExternalCSVHeaderInsteadOfColumnsMetadata(t *testing.T) {
	const projectID = "project-a"
	actualWide := governedCSVAsset(t, projectID, "id\temail\n1\ta@example.test\n")
	actualNarrow := governedCSVAsset(t, projectID, "id\n1\n")
	imported := &dsl.Script{
		Actions: []dsl.Action{func() dsl.Action {
			action := openAPIKeyedAction("Get", "GET /users")
			action.Body = `{"email":"{{data.openapi.email}}"}`
			return action
		}()},
		DataSources: []dsl.DataSource{csvDataSource("openapi", "id,email\n1,a@example.test\n", "id", "email")},
	}

	t.Run("actual header prevents false gate", func(t *testing.T) {
		existing := &dsl.Script{
			ProjectID: projectID,
			Actions:   []dsl.Action{openAPIKeyedAction("Get", "GET /users")},
			DataSources: []dsl.DataSource{{
				Name: "openapi", Type: "csv", StorageRef: (&url.URL{Scheme: "file", Path: actualWide}).String(),
				Delimiter: "tab", FirstRowHeader: true, Columns: []string{"id"},
			}},
		}
		merged := mergeOpenAPIImportScript(existing, imported)
		if blocker := merged.Actions[0].Metadata["execution_blocker"]; blocker != nil {
			t.Fatalf("actual email header was falsely gated: blocker=%v metadata=%#v", blocker, merged.Actions[0].Metadata)
		}
	})

	t.Run("actual header catches stale advertised column", func(t *testing.T) {
		existing := &dsl.Script{
			ProjectID: projectID,
			Actions:   []dsl.Action{openAPIKeyedAction("Get", "GET /users")},
			DataSources: []dsl.DataSource{{
				Name: "openapi", Type: "csv", Path: actualNarrow,
				Delimiter: "comma", FirstRowHeader: true, Columns: []string{"id", "email"},
			}},
		}
		merged := mergeOpenAPIImportScript(existing, imported)
		if blocker := merged.Actions[0].Metadata["execution_blocker"]; blocker != "import_review_required" {
			t.Fatalf("missing actual email header was not gated: blocker=%v metadata=%#v", blocker, merged.Actions[0].Metadata)
		}
		if _, unknown := merged.Actions[0].Metadata["unknown_external_data_source_schemas"]; unknown {
			t.Fatalf("readable governed schema was reported unknown: %#v", merged.Actions[0].Metadata)
		}
	})
}

func TestMergeImportFailsClosedWhenExternalCSVHeaderIsUnknown(t *testing.T) {
	existing := &dsl.Script{
		ProjectID: "project-a",
		Actions:   []dsl.Action{openAPIKeyedAction("Get", "GET /users")},
		DataSources: []dsl.DataSource{{
			Name: "openapi", Type: "csv", Path: "/arbitrary/operator/users.csv",
			FirstRowHeader: true, Columns: []string{"id", "email"},
		}},
	}
	imported := &dsl.Script{
		Actions: []dsl.Action{func() dsl.Action {
			action := openAPIKeyedAction("Get", "GET /users")
			action.Body = `{"email":"{{data.openapi.email}}"}`
			return action
		}()},
		DataSources: []dsl.DataSource{csvDataSource("openapi", "id,email\n1,a@example.test\n", "id", "email")},
	}

	merged := mergeOpenAPIImportScript(existing, imported)
	if blocker := merged.Actions[0].Metadata["execution_blocker"]; blocker != "import_review_required" {
		t.Fatalf("unknown external schema trusted Columns metadata: blocker=%v metadata=%#v", blocker, merged.Actions[0].Metadata)
	}
	unknown, _ := merged.Actions[0].Metadata["unknown_external_data_source_schemas"].([]string)
	if !slices.Equal(unknown, []string{"openapi"}) {
		t.Fatalf("unknown schema disclosure = %#v", unknown)
	}
	review, _ := merged.Actions[0].Metadata["import_review"].(map[string]any)
	reasons, _ := review["reason_codes"].([]string)
	if !slices.Contains(reasons, "external_data_source_schema_unknown") {
		t.Fatalf("review reasons = %#v", reasons)
	}
}

func TestMergeImportPreservesJSONDecodedReviewBlockersWhenGatingCSV(t *testing.T) {
	existing := &dsl.Script{
		Actions: []dsl.Action{openAPIKeyedAction("Get", "GET /users")},
		DataSources: []dsl.DataSource{{
			Name: "openapi", Type: "csv", Path: "/arbitrary/operator/users.csv",
			FirstRowHeader: true, Columns: []string{"email"},
		}},
	}
	action := openAPIKeyedAction("Get", "GET /users")
	action.Body = `{{data.openapi.email}}`
	action.Metadata["execution_blockers"] = []any{"existing_blocker"}
	action.Metadata["import_review"] = map[string]any{"reason_codes": []any{"existing_reason"}}
	imported := &dsl.Script{
		Actions:     []dsl.Action{action},
		DataSources: []dsl.DataSource{csvDataSource("openapi", "email\na@example.test\n", "email")},
	}

	merged := mergeOpenAPIImportScript(existing, imported)
	blockers, _ := merged.Actions[0].Metadata["execution_blockers"].([]string)
	if !slices.Equal(blockers, []string{"existing_blocker", "import_review_required"}) {
		t.Fatalf("execution blockers = %#v", blockers)
	}
	review, _ := merged.Actions[0].Metadata["import_review"].(map[string]any)
	reasons, _ := review["reason_codes"].([]string)
	if !slices.Equal(reasons, []string{"existing_reason", "external_data_source_schema_unknown"}) {
		t.Fatalf("review reasons = %#v", reasons)
	}
}

func TestGovernedExternalCSVHeaderReadIsBounded(t *testing.T) {
	const projectID = "project-a"
	path := governedCSVAsset(t, projectID, strings.Repeat("x", int(maxImportExternalCSVHeaderBytes)+1)+"\nvalue\n")
	_, err := readGovernedExternalCSVFirstRecord(projectID, dsl.DataSource{
		Name: "openapi", Type: "csv", Path: path, FirstRowHeader: true, Delimiter: "comma",
	})
	if err == nil || !strings.Contains(err.Error(), "header exceeds") {
		t.Fatalf("oversized header error = %v", err)
	}
}

func TestMergeImportReadsShortHeaderFromLargeGovernedAutoCSV(t *testing.T) {
	const projectID = "project-a"
	content := "id,email\n" + strings.Repeat("1,a@example.test\n", 5_000)
	if len(content) <= int(maxImportExternalCSVHeaderBytes) {
		t.Fatalf("test fixture is only %d bytes", len(content))
	}
	path := governedCSVAsset(t, projectID, content)
	existing := &dsl.Script{
		ProjectID: projectID,
		Actions:   []dsl.Action{openAPIKeyedAction("Get", "GET /users")},
		DataSources: []dsl.DataSource{{
			Name: "openapi", Type: "csv", Path: path, Delimiter: "auto",
			FirstRowHeader: true, Columns: []string{"id"},
		}},
	}
	imported := &dsl.Script{
		Actions: []dsl.Action{func() dsl.Action {
			action := openAPIKeyedAction("Get", "GET /users")
			action.Body = `{{data.openapi.email}}`
			return action
		}()},
		DataSources: []dsl.DataSource{csvDataSource("openapi", "id,email\n1,a@example.test\n", "id", "email")},
	}

	merged := mergeOpenAPIImportScript(existing, imported)
	if blocker := merged.Actions[0].Metadata["execution_blocker"]; blocker != nil {
		t.Fatalf("complete short header in large governed CSV was treated as unknown: blocker=%v metadata=%#v", blocker, merged.Actions[0].Metadata)
	}
}

func TestMergeImportGatesColumnsUnavailableInPreservedExternalCSV(t *testing.T) {
	existing := &dsl.Script{
		Actions:     []dsl.Action{openAPIKeyedAction("Get", "GET /users")},
		DataSources: []dsl.DataSource{{Name: "openapi", Type: "csv", Path: "/operator/users.csv", FirstRowHeader: true, Columns: []string{"id"}}},
	}
	imported := &dsl.Script{
		Actions: []dsl.Action{
			func() dsl.Action {
				action := openAPIKeyedAction("Get", "GET /users")
				action.Body = `{"email":"{{data.openapi.email}}"}`
				return action
			}(),
			openAPIKeyedAction("Health", "GET /health"),
		},
		Init:        []dsl.Action{{Name: "Seed", Type: "request", Method: "POST", Target: "/seed", Body: `{{ data.openapi.email }}`}},
		DataSources: []dsl.DataSource{csvDataSource("openapi", "id,email\n1,a@example.test\n", "id", "email")},
	}
	merged := mergeOpenAPIImportScript(existing, imported)
	if !slices.Equal(merged.DataSources[0].Columns, []string{"id"}) {
		t.Fatalf("external schema changed: %#v", merged.DataSources[0])
	}
	if merged.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("action was not gated for unavailable external column: %#v", merged.Actions[0].Metadata)
	}
	blocked, _ := merged.Actions[0].Metadata["unavailable_external_data_columns"].(map[string][]string)
	if !slices.Equal(blocked["openapi"], []string{"email"}) {
		t.Fatalf("blocked columns = %#v", blocked)
	}
	if merged.Actions[1].Metadata["execution_blocker"] != nil {
		t.Fatalf("unrelated imported action was unnecessarily gated: %#v", merged.Actions[1].Metadata)
	}
	if len(merged.Init) != 1 || merged.Init[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("imported lifecycle binding was not gated: %#v", merged.Init)
	}
}

func TestMergeImportGatesUnavailableExternalColumnThroughParameterAliases(t *testing.T) {
	existing := &dsl.Script{
		Actions:     []dsl.Action{openAPIKeyedAction("Get", "GET /users")},
		DataSources: []dsl.DataSource{{Name: "openapi", Type: "csv", Path: "/operator/users.csv", FirstRowHeader: true, Columns: []string{"id"}}},
	}
	imported := &dsl.Script{
		Actions: []dsl.Action{func() dsl.Action {
			action := openAPIKeyedAction("Get", "GET /users")
			action.Body = `{"email":"{{param.contact}}"}`
			return action
		}()},
		Parameters: []dsl.Parameter{
			{Name: "email", Type: "file", DataSource: "openapi", Column: "email"},
			{Name: "contact", Type: "list", Values: []string{"{{param.email}}"}},
		},
		DataSources: []dsl.DataSource{csvDataSource("openapi", "id,email\n1,a@example.test\n", "id", "email")},
	}

	merged := mergeOpenAPIImportScript(existing, imported)
	if merged.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("parameter-indirected unavailable column was not gated: %#v", merged.Actions[0].Metadata)
	}
	blocked, _ := merged.Actions[0].Metadata["unavailable_external_data_columns"].(map[string][]string)
	if !slices.Equal(blocked["openapi"], []string{"email"}) {
		t.Fatalf("blocked columns = %#v", blocked)
	}
}

func TestMergeImportUsesPreservedParameterDefinitionWhenEvaluatingExternalColumns(t *testing.T) {
	existing := &dsl.Script{
		Actions:     []dsl.Action{openAPIKeyedAction("Get", "GET /users")},
		DataSources: []dsl.DataSource{{Name: "openapi", Type: "csv", Path: "/operator/users.csv", FirstRowHeader: false, Columns: []string{"id"}}},
		Parameters:  []dsl.Parameter{{Name: "identity", Type: "file", DataSource: "openapi", Column: "id"}},
	}
	imported := &dsl.Script{
		Actions: []dsl.Action{func() dsl.Action {
			action := openAPIKeyedAction("Get", "GET /users")
			action.Target = `/users/{{param.identity}}`
			return action
		}()},
		Parameters:  []dsl.Parameter{{Name: "identity", Type: "file", DataSource: "openapi", Column: "email"}},
		DataSources: []dsl.DataSource{csvDataSource("openapi", "id,email\n1,a@example.test\n", "id", "email")},
	}

	merged := mergeOpenAPIImportScript(existing, imported)
	if merged.Actions[0].Metadata["execution_blocker"] != nil {
		t.Fatalf("preserved valid parameter was unnecessarily gated: %#v", merged.Actions[0].Metadata)
	}
}

func TestMergeCSVDataSourceRebuildsInlineHeaderForExistingOnlyColumns(t *testing.T) {
	existing := csvDataSource("openapi", "", "id", "name")
	imported := csvDataSource("openapi", "id,email\n1,a@example.test\n", "id", "email")
	merged, added := mergeCSVDataSource(existing, imported)
	if !slices.Equal(added, []string{"email"}) || !slices.Equal(merged.Columns, []string{"id", "name", "email"}) {
		t.Fatalf("columns=%#v added=%#v", merged.Columns, added)
	}
	header, rows := parseCSVDataSource(merged)
	if !slices.Equal(header, merged.Columns) || len(rows) != 1 || !slices.Equal(rows[0], []string{"1", "", "a@example.test"}) {
		t.Fatalf("rebuilt inline CSV header/rows = %#v / %#v", header, rows)
	}
}

func TestMergeImportScriptByKeyDoesNotDuplicateImportedUnkeyedActionsOnRetry(t *testing.T) {
	importedPlaceholder := dsl.Action{
		Name: "Review source script",
		Type: "review_required",
		Metadata: map[string]any{
			"execution_blocker": "import_review_required",
		},
	}
	manualDuplicate := importedPlaceholder
	existing := &dsl.Script{
		Actions: []dsl.Action{
			openAPIKeyedAction("GetA", "GET /a"),
			importedPlaceholder,
			manualDuplicate,
			{Name: "ManualStep", Type: "request", Method: "GET", Target: "/manual"},
		},
	}
	imported := &dsl.Script{
		Actions: []dsl.Action{
			openAPIKeyedAction("GetA", "GET /a"),
			importedPlaceholder,
		},
	}

	merged := mergeImportScriptByKey(existing, imported, openAPIActionKey)
	placeholderCount := 0
	manualCount := 0
	for _, action := range merged.Actions {
		switch action.Name {
		case importedPlaceholder.Name:
			placeholderCount++
		case "ManualStep":
			manualCount++
		}
	}
	if placeholderCount != 2 {
		t.Fatalf("matching imported occurrence should be deduplicated while the extra manual occurrence survives: %#v", merged.Actions)
	}
	if manualCount != 1 {
		t.Fatalf("manual unkeyed action was not preserved: %#v", merged.Actions)
	}

	retried := mergeImportScriptByKey(merged, imported, openAPIActionKey)
	if len(retried.Actions) != len(merged.Actions) {
		t.Fatalf("retry changed action count from %d to %d: %#v", len(merged.Actions), len(retried.Actions), retried.Actions)
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
	header, rows := parseCSVDataSource(merged)
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
