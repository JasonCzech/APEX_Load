package storage

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
)

func TestSQLiteStoreSaveScriptPreservesMetadata(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID:       "script-1",
		Name:     "Imported Script",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
		},
		Actions: []dsl.Action{{
			Name:   "Homepage",
			Type:   "GET",
			Target: "/",
		}},
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Source:           "loadrunner",
				SourceScriptName: "Demo",
				Preserved: map[string]any{
					"runtime_files": map[string]any{
						"default.cfg": map[string]any{
							"HTTP": map[string]any{
								"ConnectTimeout": "45",
							},
						},
					},
				},
			},
		},
	}

	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	roundTrip, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript returned error: %v", err)
	}
	if roundTrip.Metadata == nil || roundTrip.Metadata.Import == nil {
		t.Fatal("expected metadata to round-trip through sqlite")
	}
	payload, err := json.Marshal(roundTrip.Metadata.Import)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	text := string(payload)
	for _, want := range []string{"\"source\":\"loadrunner\"", "default.cfg", "\"ConnectTimeout\":\"45\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected metadata JSON to contain %q, got %s", want, text)
		}
	}
}

func TestSQLiteStoreProjectsFoldersAndFiltering(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	projects, err := store.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects returned error: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != DefaultProjectID {
		t.Fatalf("expected default project, got %#v", projects)
	}

	project := &Project{
		ID:   "project-1",
		Name: "Payments",
		Settings: ProjectSettings{
			RedactionRules: []engine.RedactionRule{{Name: "customer id", Pattern: `customer-[0-9]+`}},
		},
		CreatedAt: time.Now(),
	}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	roundTripProject, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject returned error: %v", err)
	}
	if len(roundTripProject.Settings.RedactionRules) != 1 || roundTripProject.Settings.RedactionRules[0].Pattern != `customer-[0-9]+` {
		t.Fatalf("expected persisted project settings, got %#v", roundTripProject.Settings)
	}
	folder := &Folder{ID: "folder-1", ProjectID: project.ID, Name: "Checkout", Category: "scripts", CreatedAt: time.Now()}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatalf("SaveFolder returned error: %v", err)
	}

	script := &dsl.Script{
		ID:        "script-1",
		Name:      "Checkout API",
		Protocol:  "http",
		Version:   "1.0",
		ProjectID: project.ID,
		FolderID:  &folder.ID,
		Config:    dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:   []dsl.Action{{Name: "Cart", Type: "request", Method: "GET", Target: "/cart"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	filtered, err := store.ListScripts(project.ID, &folder.ID)
	if err != nil {
		t.Fatalf("ListScripts returned error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ProjectID != project.ID || filtered[0].FolderID == nil || *filtered[0].FolderID != folder.ID {
		t.Fatalf("unexpected filtered scripts: %#v", filtered)
	}

	if err := store.DeleteFolder(folder.ID); err == nil {
		t.Fatal("expected non-empty folder delete to fail")
	}
	if err := store.MoveScript(script.ID, nil); err != nil {
		t.Fatalf("MoveScript returned error: %v", err)
	}
	if err := store.DeleteFolder(folder.ID); err != nil {
		t.Fatalf("DeleteFolder returned error after moving script: %v", err)
	}
}

func TestSQLiteStoreBackfillsDefaultProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stormrunner.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	script := &dsl.Script{
		ID:       "legacy-script",
		Name:     "Legacy",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Home", Type: "request", Method: "GET", Target: "/"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	store.Close()

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen NewSQLiteStore returned error: %v", err)
	}
	defer reopened.Close()
	roundTrip, err := reopened.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript returned error: %v", err)
	}
	if roundTrip.ProjectID != DefaultProjectID {
		t.Fatalf("project id = %q, want %q", roundTrip.ProjectID, DefaultProjectID)
	}
	pool := &SharedDataPool{ID: "legacy-pool", Name: "Legacy Pool", CreatedAt: time.Now()}
	if err := reopened.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}
	poolRoundTrip, err := reopened.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool returned error: %v", err)
	}
	if poolRoundTrip.ProjectID != DefaultProjectID {
		t.Fatalf("data pool project id = %q, want %q", poolRoundTrip.ProjectID, DefaultProjectID)
	}
}

func TestSQLiteStoreApplicationsReleasesScenariosAndContext(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	app := &Application{ID: "app-1", Name: "Payments", CreatedAt: time.Now()}
	if err := store.SaveApplication(app); err != nil {
		t.Fatalf("SaveApplication returned error: %v", err)
	}
	release := &Release{ID: "rel-1", ApplicationID: app.ID, Name: "2026.05", CreatedAt: time.Now()}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("SaveRelease returned error: %v", err)
	}

	script := &dsl.Script{
		ID:       "script-context",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Checkout", Type: "request", Method: "GET", Target: "/checkout"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}

	scenario := &Scenario{
		ID:           "scenario-1",
		Name:         "Checkout Smoke",
		Duration:     "2m",
		ScenarioMode: "vuser_driven",
		CreatedAt:    time.Now(),
	}
	groups := []ScenarioScript{{Order: 0, ScriptID: script.ID, VUsers: 25, Initialization: "at_runtime"}}
	if err := store.SaveScenario(scenario, groups); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}
	roundTrip, scripts, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatalf("GetScenario returned error: %v", err)
	}
	if roundTrip.Name != scenario.Name || len(scripts) != 1 || scripts[0].ScriptID != script.ID {
		t.Fatalf("unexpected scenario round trip: %#v scripts=%#v", roundTrip, scripts)
	}

	record := &TestRecord{
		ID:            "test-1",
		Name:          scenario.Name,
		Status:        "COMPLETE",
		ApplicationID: app.ID,
		ReleaseID:     release.ID,
		ScenarioID:    scenario.ID,
		TotalVUsers:   25,
		Duration:      int64(2 * time.Minute),
		StartTime:     time.Now(),
		Groups:        []map[string]any{{"script_id": script.ID, "vusers": 25}},
	}
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	filtered, err := store.ListTestsByContext(app.ID, release.ID, scenario.ID)
	if err != nil {
		t.Fatalf("ListTestsByContext returned error: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ReleaseID != release.ID || filtered[0].ScenarioID != scenario.ID {
		t.Fatalf("unexpected filtered tests: %#v", filtered)
	}

	if err := store.DeleteRelease(release.ID); err == nil {
		t.Fatal("expected DeleteRelease to fail while results exist")
	}
	if err := store.ArchiveRelease(release.ID); err == nil {
		t.Fatal("expected ArchiveRelease to fail while results exist")
	}
}

func TestSQLiteStoreSaveHistoryUsesTypedIntervalSummaries(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	timestamp := time.Date(2026, time.May, 13, 17, 0, 0, 0, time.UTC)
	metrics := map[string]any{
		"timestamp":          timestamp,
		"active_vusers":      12,
		"tps":                48.5,
		"avg_response_ms":    220.5,
		"error_pct":          1.25,
		"total_transactions": 120,
		"total_errors":       2,
		"p50_ms":             150.0,
		"p90_ms":             300.0,
		"p95_ms":             350.0,
		"p99_ms":             420.0,
		"browser": map[string]any{
			"samples":                2,
			"avg_fcp_ms":             120.0,
			"avg_lcp_ms":             240.0,
			"avg_cls_score":          0.031,
			"avg_inp_ms":             55.0,
			"avg_blocking_resources": 2.0,
			"avg_long_task_count":    1.5,
			"avg_js_heap_used_mb":    18.0,
		},
		"browser_by_device": map[string]any{
			"iphone_se": map[string]any{
				"device_profile":         "iphone_se",
				"device_tier":            "low",
				"samples":                2,
				"avg_fcp_ms":             120.0,
				"avg_lcp_ms":             240.0,
				"avg_cls_score":          0.031,
				"avg_inp_ms":             55.0,
				"avg_blocking_resources": 2.0,
				"avg_long_task_count":    1.5,
				"avg_js_heap_used_mb":    18.0,
			},
		},
		"telemetry": map[string]any{
			"dropped_results":             3,
			"websocket_clients":           2,
			"websocket_dropped_messages":  4,
			"websocket_dropped_events":    1,
			"generator_published_at":      timestamp.Add(-400 * time.Millisecond),
			"controller_received_at":      timestamp.Add(-200 * time.Millisecond),
			"collected_at":                timestamp.Add(-time.Second),
			"published_at":                timestamp,
			"generator_processing_lag_ms": 600.0,
			"transport_lag_ms":            200.0,
			"controller_fanout_lag_ms":    200.0,
			"sample_age_ms":               1000.0,
		},
	}

	if err := store.SaveHistory("test-1", timestamp, metrics); err != nil {
		t.Fatalf("SaveHistory returned error: %v", err)
	}

	var typedCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM test_interval_summaries WHERE test_id = ?`, "test-1").Scan(&typedCount); err != nil {
		t.Fatalf("count typed summaries: %v", err)
	}
	if typedCount != 1 {
		t.Fatalf("typed summary count = %d, want 1", typedCount)
	}

	var legacyCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM test_history WHERE test_id = ?`, "test-1").Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy history: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("legacy history count = %d, want 0", legacyCount)
	}

	history, err := store.GetHistory("test-1")
	if err != nil {
		t.Fatalf("GetHistory returned error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	record, ok := history[0].(IntervalSummaryRecord)
	if !ok {
		t.Fatalf("expected typed interval summary, got %#v", history[0])
	}
	if record.ActiveVUsers != 12 || record.TotalTx != 120 || record.DroppedResults != 3 || record.WebsocketDroppedMessages != 4 {
		t.Fatalf("unexpected typed interval summary: %#v", record)
	}
	if record.Browser == nil || record.Browser.Samples != 2 || record.Browser.AvgCLSScore != 0.031 {
		t.Fatalf("unexpected browser summary: %#v", record.Browser)
	}
	if record.BrowserByDevice == nil || record.BrowserByDevice["iphone_se"] == nil || record.BrowserByDevice["iphone_se"].DeviceTier != "low" {
		t.Fatalf("unexpected browser by device summary: %#v", record.BrowserByDevice)
	}
	if !record.GeneratorPublishedAt.Equal(timestamp.Add(-400 * time.Millisecond)) {
		t.Fatalf("generator_published_at = %s, want %s", record.GeneratorPublishedAt, timestamp.Add(-400*time.Millisecond))
	}
	if !record.ControllerReceivedAt.Equal(timestamp.Add(-200 * time.Millisecond)) {
		t.Fatalf("controller_received_at = %s, want %s", record.ControllerReceivedAt, timestamp.Add(-200*time.Millisecond))
	}
	if record.GeneratorProcessingLagMs != 600 || record.TransportLagMs != 200 || record.ControllerFanoutLagMs != 200 {
		t.Fatalf("unexpected lag breakdown values: %#v", record)
	}
	if record.SchemaVersion != intervalSummarySchemaVersion {
		t.Fatalf("schema version = %q, want %q", record.SchemaVersion, intervalSummarySchemaVersion)
	}

	var storedSchemaVersion string
	if err := store.db.QueryRow(`SELECT schema_version FROM test_interval_summaries WHERE test_id = ?`, "test-1").Scan(&storedSchemaVersion); err != nil {
		t.Fatalf("select typed schema version: %v", err)
	}
	if storedSchemaVersion != intervalSummarySchemaVersion {
		t.Fatalf("stored schema version = %q, want %q", storedSchemaVersion, intervalSummarySchemaVersion)
	}
}

func TestSQLiteStoreMigratesTelemetrySchemaVersionsForExistingRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stormrunner.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}

	statements := []string{
		`CREATE TABLE applications (id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE releases (id TEXT PRIMARY KEY, application_id TEXT NOT NULL, name TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE scenarios (id TEXT PRIMARY KEY, name TEXT NOT NULL, duration TEXT NOT NULL, start_delay TEXT, assignment_json TEXT, scenario_mode TEXT, shakeout_enabled INTEGER DEFAULT 0, resource_baseline_enabled INTEGER DEFAULT 0, goal_config_json TEXT, runtime_settings_json TEXT, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE scenario_scripts (scenario_id TEXT NOT NULL, script_id TEXT NOT NULL, script_order INTEGER NOT NULL, vusers INTEGER NOT NULL, ramp_up TEXT, ramp_down TEXT, iterations INTEGER, initialization TEXT, PRIMARY KEY (scenario_id, script_order))`,
		`CREATE TABLE shared_data_pools (id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, schema_json TEXT, archived_at DATETIME, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE shared_data_pool_rows (id TEXT PRIMARY KEY, pool_id TEXT NOT NULL, data_json TEXT NOT NULL, status TEXT, version INTEGER NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE scenario_data_pool_bindings (id TEXT PRIMARY KEY, scenario_id TEXT NOT NULL, pool_id TEXT NOT NULL, alias TEXT NOT NULL, access_mode TEXT NOT NULL, selection_strategy TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE folders (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, name TEXT NOT NULL, category TEXT NOT NULL, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE scripts (id TEXT PRIMARY KEY, name TEXT NOT NULL, protocol TEXT NOT NULL, version TEXT NOT NULL, script_json TEXT NOT NULL, metadata_json TEXT, created_at DATETIME)`,
		`CREATE TABLE tests (id TEXT PRIMARY KEY, name TEXT NOT NULL, status TEXT NOT NULL, script_id TEXT, total_vusers INTEGER, duration_ns INTEGER, start_time DATETIME, end_time DATETIME, error TEXT, groups_json TEXT, goal_config_json TEXT)`,
		`CREATE TABLE test_history (test_id TEXT, timestamp DATETIME, metrics_json TEXT, FOREIGN KEY(test_id) REFERENCES tests(id))`,
		`CREATE TABLE test_interval_summaries (test_id TEXT, timestamp DATETIME, active_vusers INTEGER, tps REAL, avg_response_ms REAL, error_pct REAL, total_transactions INTEGER, total_errors INTEGER, p50_ms REAL, p90_ms REAL, p95_ms REAL, p99_ms REAL, dropped_results INTEGER, websocket_clients INTEGER, websocket_dropped_messages INTEGER, websocket_dropped_events INTEGER, collected_at DATETIME, published_at DATETIME, sample_age_ms REAL, FOREIGN KEY(test_id) REFERENCES tests(id))`,
		`CREATE TABLE run_archives (test_id TEXT PRIMARY KEY, archive_path TEXT, manifest_path TEXT, transaction_count INTEGER, resource_sample_count INTEGER, interval_summary_count INTEGER, created_at DATETIME, completed_at DATETIME, FOREIGN KEY(test_id) REFERENCES tests(id))`,
		`CREATE TABLE app_settings (key TEXT PRIMARY KEY, value TEXT, updated_at DATETIME)`,
	}
	for _, statement := range statements {
		if _, err := rawDB.Exec(statement); err != nil {
			t.Fatalf("exec statement failed: %v", err)
		}
	}

	timestamp := time.Date(2026, time.May, 13, 17, 30, 0, 0, time.UTC)
	if _, err := rawDB.Exec(`INSERT INTO tests (id, name, status, total_vusers, duration_ns, start_time, groups_json, goal_config_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"test-legacy", "Legacy", "COMPLETE", 1, 0, timestamp, `[]`, `{}`); err != nil {
		t.Fatalf("insert legacy test failed: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO test_interval_summaries (test_id, timestamp, active_vusers, tps, avg_response_ms, error_pct, total_transactions, total_errors, p50_ms, p90_ms, p95_ms, p99_ms, dropped_results, websocket_clients, websocket_dropped_messages, websocket_dropped_events, collected_at, published_at, sample_age_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"test-legacy", timestamp, 7, 12.5, 180.0, 0.5, 50, 1, 120.0, 200.0, 250.0, 300.0, 2, 1, 3, 1, timestamp.Add(-time.Second), timestamp, 1000.0); err != nil {
		t.Fatalf("insert legacy typed summary failed: %v", err)
	}
	if _, err := rawDB.Exec(`INSERT INTO run_archives (test_id, archive_path, manifest_path, transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"test-legacy", "/tmp/archive", "/tmp/archive/manifest.json", 10, 2, 3, timestamp, timestamp); err != nil {
		t.Fatalf("insert legacy archive row failed: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw db failed: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	history, err := store.GetHistory("test-legacy")
	if err != nil {
		t.Fatalf("GetHistory returned error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	record, ok := history[0].(IntervalSummaryRecord)
	if !ok {
		t.Fatalf("expected typed interval summary, got %#v", history[0])
	}
	if record.SchemaVersion != intervalSummarySchemaVersionV1 {
		t.Fatalf("migrated summary schema version = %q, want %q", record.SchemaVersion, intervalSummarySchemaVersionV1)
	}

	archive, err := store.GetRunArchive("test-legacy")
	if err != nil {
		t.Fatalf("GetRunArchive returned error: %v", err)
	}
	if archive.SchemaVersion != archiveRecordSchemaVersion {
		t.Fatalf("archive schema version = %q, want %q", archive.SchemaVersion, archiveRecordSchemaVersion)
	}
}

func TestSQLiteStoreSharedDataPoolsAndScenarioBindings(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID:       "script-data-pool",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.com"},
		Actions:  []dsl.Action{{Name: "Checkout", Type: "request", Method: "GET", Target: "/checkout"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatalf("SaveScript returned error: %v", err)
	}
	scenario := &Scenario{ID: "scenario-data-pool", Name: "Checkout Flow", Duration: "5m", CreatedAt: time.Now()}
	if err := store.SaveScenario(scenario, []ScenarioScript{{Order: 0, ScriptID: script.ID, VUsers: 10}}); err != nil {
		t.Fatalf("SaveScenario returned error: %v", err)
	}

	pool := &SharedDataPool{
		ID:          "pool-1",
		Name:        "Customers",
		Description: "Reusable customer identities",
		Schema:      map[string]string{"customer_id": "string", "zip": "string"},
		ProjectID:   DefaultProjectID,
		CreatedAt:   time.Now(),
	}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool returned error: %v", err)
	}
	roundTripPool, err := store.GetDataPool(pool.ID)
	if err != nil {
		t.Fatalf("GetDataPool returned error: %v", err)
	}
	if roundTripPool.ProjectID != DefaultProjectID {
		t.Fatalf("data pool project id = %q, want %q", roundTripPool.ProjectID, DefaultProjectID)
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{
		ID:     "row-1",
		PoolID: pool.ID,
		Data:   map[string]string{"customer_id": "C-100", "zip": "10001"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow returned error: %v", err)
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{
		ID:     "row-1",
		PoolID: pool.ID,
		Data:   map[string]string{"customer_id": "C-101", "zip": "10002"},
	}); err != nil {
		t.Fatalf("SaveDataPoolRow update returned error: %v", err)
	}

	rows, err := store.ListDataPoolRows(pool.ID)
	if err != nil {
		t.Fatalf("ListDataPoolRows returned error: %v", err)
	}
	if len(rows) != 1 || rows[0].Data["customer_id"] != "C-101" || rows[0].Version != 2 {
		t.Fatalf("unexpected rows after update: %#v", rows)
	}

	bindings := []ScenarioDataPoolBinding{{
		ID:                "binding-1",
		PoolID:            pool.ID,
		Alias:             "customers",
		AccessMode:        "read_write",
		SelectionStrategy: "unique",
	}}
	if err := store.SaveScenarioDataPoolBindings(scenario.ID, bindings); err != nil {
		t.Fatalf("SaveScenarioDataPoolBindings returned error: %v", err)
	}
	roundTrip, err := store.ListScenarioDataPoolBindings(scenario.ID)
	if err != nil {
		t.Fatalf("ListScenarioDataPoolBindings returned error: %v", err)
	}
	if len(roundTrip) != 1 || roundTrip[0].PoolName != pool.Name || roundTrip[0].Alias != "customers" || roundTrip[0].SelectionStrategy != "unique" {
		t.Fatalf("unexpected scenario data pool bindings: %#v", roundTrip)
	}
	if err := store.DeleteDataPool(pool.ID); err == nil {
		t.Fatal("expected DeleteDataPool to fail while scenario binding exists")
	}
	if err := store.DeleteScenarioDataPoolBinding("binding-1"); err != nil {
		t.Fatalf("DeleteScenarioDataPoolBinding returned error: %v", err)
	}
	if err := store.DeleteDataPool(pool.ID); err != nil {
		t.Fatalf("DeleteDataPool returned error after unbinding: %v", err)
	}
}
