package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/monitorcontract"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetry"
)

func newRetirementFixtureServer(t *testing.T) (*Server, *storage.SQLiteStore, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	store, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); store.Close() })
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "Project A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	if server.providerMonitoringCancel != nil {
		t.Cleanup(server.providerMonitoringCancel)
	}
	return server, store, db
}

func execLegacyFixture(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}

func legacyRetiredMonitorSource() storage.MonitorSource {
	return storage.MonitorSource{ID: "legacy-appd", ProjectID: "proj-a", Name: "Old AppDynamics", Pack: "appdynamics", Enabled: true, Revision: 1, CredentialRef: "retained-secret", CollectorPublicKey: "private-key", AppDynamics: &monitorcontract.AppDynamicsSelection{ProjectID: "proj-a", Applications: []string{"1"}}}
}

func seedRetiredMonitorSource(t *testing.T, db *sql.DB, source storage.MonitorSource) {
	t.Helper()
	body, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	execLegacyFixture(t, db, `INSERT INTO monitor_sources(id,project_id,revision,payload) VALUES(?,?,?,?)`, source.ID, source.ProjectID, source.Revision, string(body))
}

func TestRetiredMonitoringBindingsBlockEveryLaunchSelection(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		required, enabled, inherited bool
	}{
		{"explicit required", true, true, false}, {"explicit optional disabled", false, false, false},
		{"inherited optional", false, true, true}, {"inherited disabled required", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
			server, store, db := newRetirementFixtureServer(t)
			source := legacyRetiredMonitorSource()
			source.Enabled = tc.enabled
			seedRetiredMonitorSource(t, db, source)
			bindings := &storage.MonitorBindings{ProjectID: "proj-a", ScenarioID: "scenario", Revision: 1, Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: tc.required}}}
			record := &storage.TestRecord{ID: "run", ProjectID: "proj-a", Name: "legacy", Status: "QUEUED", MonitorBindings: bindings}
			if tc.inherited {
				now := time.Now().UTC()
				if err := store.SaveScript(&dsl.Script{ID: "script", ProjectID: "proj-a", Name: "fixture", Protocol: "http", CreatedAt: now}); err != nil {
					t.Fatal(err)
				}
				if err := store.SaveScenario(&storage.Scenario{ID: "scenario", Name: "fixture", Duration: "1m", CreatedAt: now}, []storage.ScenarioScript{{ScriptID: "script"}}); err != nil {
					t.Fatal(err)
				}
				body, _ := json.Marshal(bindings)
				execLegacyFixture(t, db, `INSERT INTO monitor_bindings(scenario_id,project_id,revision,payload) VALUES(?,?,?,?)`, "scenario", "proj-a", 1, string(body))
				record.ScenarioID = "scenario"
				record.MonitorBindings = nil
			}
			if err := store.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			server.runner.tests[record.ID] = &ManagedTest{ID: record.ID, ProjectID: record.ProjectID, ScenarioID: record.ScenarioID, Status: "QUEUED", MonitorBindings: record.MonitorBindings}
			if err := server.runner.validateRetiredMonitoringRecord(record, ""); err == nil || !strings.Contains(err.Error(), "AppDynamics monitoring is retired") {
				t.Fatalf("gate: %v", err)
			}
			if err := server.runner.StartTest(record.ID); err == nil || !strings.Contains(err.Error(), "AppDynamics monitoring is retired") {
				t.Fatalf("launch: %v", err)
			}
			if server.runner.tests[record.ID].Status != "QUEUED" {
				t.Fatal("retirement mutated launch state")
			}
		})
	}
}

func TestFrozenRetiredPreparationCannotBypassEditedDraft(t *testing.T) {
	server, store, db := newRetirementFixtureServer(t)
	record := &storage.TestRecord{ID: "run", ProjectID: "proj-a", Name: "legacy", Status: "INITIALIZING", MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{}}}
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	server.runner.tests[record.ID] = &ManagedTest{ID: record.ID, ProjectID: record.ProjectID, Status: record.Status, MonitorBindings: record.MonitorBindings, preparationID: "prep"}
	now := time.Now().UTC()
	prep := storage.Preparation{ID: "prep", ProjectID: "proj-a", TestID: "run", State: "ready", Revision: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour), Monitors: &storage.PreparationMonitorSnapshot{Sources: map[string]storage.MonitorSource{"legacy-appd": legacyRetiredMonitorSource()}}}
	body, _ := json.Marshal(prep)
	execLegacyFixture(t, db, `INSERT INTO preparations(id,project_id,test_id,revision,execution_revision,state,idempotency_key,owner_fence,created_at,expires_at,payload) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, prep.ID, prep.ProjectID, prep.TestID, 1, "revision", "ready", "fixture", "", now, prep.ExpiresAt, string(body))
	if err := server.runner.validateRetiredMonitoring("run", "prep"); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("frozen gate: %v", err)
	}
	if err := server.runner.consumePreparation("run", StartTestOptions{PreparationID: "prep"}); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("consume: %v", err)
	}
	preserved, err := store.GetPreparation("prep")
	if err != nil || preserved.State != "ready" {
		t.Fatalf("snapshot changed: %+v %v", preserved, err)
	}
}

func TestRetirementRoutesAndNewSourceUnavailable(t *testing.T) {
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	server, _, _ := newRetirementFixtureServer(t)
	for _, path := range []string{"/api/v1/projects/proj-a/monitoring/appdynamics-connections", "/api/v1/scripts/old/bruno-projection/preview"} {
		method := http.MethodGet
		if strings.Contains(path, "projection") {
			method = http.MethodPost
		}
		got := timeslotRequest(t, server, method, path, nil)
		if got.Code != http.StatusNotFound && got.Code != http.StatusMethodNotAllowed {
			t.Fatalf("removed route %s: %d %s", path, got.Code, got.Body.String())
		}
	}
	got := timeslotRequest(t, server, http.MethodPost, "/api/v1/projects/proj-a/monitor-sources", legacyRetiredMonitorSource())
	if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "retired") {
		t.Fatalf("new source: %d %s", got.Code, got.Body.String())
	}
	if _, ok := providerDescriptors[monitorprovider.PackAppDynamics]; ok {
		t.Fatal("retired provider executable")
	}
	if !nativeProviderPack(monitorprovider.PackAppDynamics) {
		t.Fatal("historical provider classification lost")
	}
	for _, profile := range buildAPMIntegrationProfiles(SystemSettings{}, telemetry.Status{}) {
		if profile.ID == "appdynamics" {
			t.Fatal("retired integration advertised")
		}
	}
}

func TestRetainedAppDynamicsEvidenceAndSecretProjection(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, db := newRetirementFixtureServer(t)
	now := time.Now().UTC().Add(-time.Hour)
	source := legacyRetiredMonitorSource()
	seedRetiredMonitorSource(t, db, source)
	record := &storage.TestRecord{ID: "old-run", Name: "old", ProjectID: "proj-a", Status: "COMPLETE", StartedAt: &now, StartTime: now}
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	job := storage.RunMonitorJob{ID: "old-job", ProjectID: "proj-a", RunID: record.ID, State: "stopped", Source: source, StartedAt: now, NextSequence: 1, ExpiresAt: now.Add(time.Hour)}
	body, _ := json.Marshal(job)
	execLegacyFixture(t, db, `INSERT INTO run_monitor_jobs(id,project_id,test_id,collector_id,state,next_sequence,expires_at,payload) VALUES(?,?,?,?,?,?,?,?)`, job.ID, job.ProjectID, job.RunID, "", "stopped", 1, job.ExpiresAt, string(body))
	evidence := monitorprovider.Diagnostic{ID: "event-1", Kind: "event", ApplicationID: "1", TimestampUTC: now, Summary: "historical event"}
	evidenceJSON, _ := json.Marshal(evidence)
	evidenceHash := sha256.Sum256(evidenceJSON)
	evidenceDigest := hex.EncodeToString(evidenceHash[:])
	batch := storage.ProviderMonitorBatch{Evidence: []monitorprovider.Diagnostic{evidence}}
	data, _ := json.Marshal(batch)
	key, digest := monitorArtifactKey(job.ProjectID, job.RunID, data)
	if err := writeMonitorArtifact(context.Background(), key, data); err != nil {
		t.Fatal(err)
	}
	execLegacyFixture(t, db, `INSERT INTO run_monitor_batches(job_id,sequence,digest,artifact_key,sample_count,received_at) VALUES(?,?,?,?,?,?)`, job.ID, 0, digest, key, 0, now)
	execLegacyFixture(t, db, `INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,content_digest,diagnostic_kind) VALUES(?,?,?,?,?,?,?)`, job.ID, storage.DiagnosticKeyForJob(job.ID, evidence), 0, 0, "diagnostic", evidenceDigest, "event")
	page, err := loadCanonicalMonitorData(context.Background(), store, job.ProjectID, job.RunID, "", "diagnostic", "", 100)
	if err != nil || len(page.Evidence) != 1 || page.Evidence[0].Summary != evidence.Summary || page.EvidenceSources[0] != source.ID {
		t.Fatalf("historical evidence: %+v %v", page, err)
	}
	safe := safeMonitorJob(job)
	raw, _ := json.Marshal(safe)
	if strings.Contains(string(raw), "retained-secret") || strings.Contains(string(raw), "private-key") {
		t.Fatal("legacy credentials disclosed")
	}
	jobs, err := store.ListRunMonitorJobs(job.ProjectID, job.RunID)
	if err != nil || len(jobs) != 1 || jobs[0].Source.CredentialRef != "retained-secret" {
		t.Fatalf("stored history lost: %+v %v", jobs, err)
	}
	_ = server
}

func TestBrunoRetirementPrecedesAPISecretsAndReplay(t *testing.T) {
	server, _, _ := newRetirementFixtureServer(t)
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "control_loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Type: "bruno_run"}}}}}}
	server.runner.tests["run"] = &ManagedTest{ID: "run", ProjectID: "proj-a", Status: "QUEUED", groupSpecs: []engine.GroupConfig{{Script: script}}}
	if err := server.runner.validateRetiredExecution("run", ""); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("launch gate: %v", err)
	}
	if _, err := server.runner.ReplayScriptDefinition(script, engine.ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("replay gate: %v", err)
	}
}

func TestRetiredPreparationRejectedBeforeCapacityAndSecrets(t *testing.T) {
	t.Setenv("APEXLOAD_PREPARATION_PROJECTS", "proj-a")
	server, store, db := newRetirementFixtureServer(t)
	source := legacyRetiredMonitorSource()
	seedRetiredMonitorSource(t, db, source)
	bindings := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID}}}
	record := &storage.TestRecord{ID: "old-prepare", ProjectID: "proj-a", Name: "legacy", Status: "QUEUED", MonitorBindings: bindings}
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	server.runner.tests[record.ID] = &ManagedTest{ID: record.ID, ProjectID: record.ProjectID, Status: record.Status, MonitorBindings: bindings, groupSpecs: []engine.GroupConfig{{Script: &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "request", Target: "{{secret.unavailable}}"}}}}}}
	// Without the early retirement gate, this reaches the unsupported ephemeral
	// capacity path or tries to resolve the deliberately missing script secret.
	for _, body := range []any{nil, map[string]any{"capacity": map[string]any{"selection": "ephemeral"}}} {
		got := timeslotRequest(t, server, http.MethodPost, "/api/v1/tests/"+record.ID+"/preparation", body)
		if got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "AppDynamics monitoring is retired") {
			t.Fatalf("preparation: %d %s", got.Code, got.Body.String())
		}
	}
	if p, _ := store.LatestPreparation(record.ID); p != nil {
		t.Fatal("retired preparation persisted state")
	}
}

func TestRetiredDeletedSourceBindingCannotBypassLaunch(t *testing.T) {
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
	server, store, db := newRetirementFixtureServer(t)
	source := legacyRetiredMonitorSource()
	seedRetiredMonitorSource(t, db, source)
	record := &storage.TestRecord{ID: "dangling-run", ProjectID: "proj-a", Name: "legacy", Status: "QUEUED", MonitorBindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID, Required: false}}}}
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	server.runner.tests[record.ID] = &ManagedTest{ID: record.ID, ProjectID: record.ProjectID, Status: record.Status, MonitorBindings: record.MonitorBindings}
	if err := store.DeleteMonitorSource(source.ProjectID, source.ID, source.Revision); err != nil {
		t.Fatal(err)
	}
	if err := server.runner.StartTest(record.ID); err == nil || !strings.Contains(err.Error(), "remove its binding") {
		t.Fatalf("dangling retired launch: %v", err)
	}
	if server.runner.tests[record.ID].Status != "QUEUED" {
		t.Fatal("dangling retirement mutated launch state")
	}
}

func TestRetiredMonitoringRulesBlockBeforeExecutionAdmission(t *testing.T) {
	server, _, _ := newRetirementFixtureServer(t)
	server.runner.tests["run"] = &ManagedTest{ID: "run", ProjectID: "proj-a", Status: "QUEUED", GoalConfig: &GoalConfig{MonitorRules: []analysis.MonitorRule{{Provider: monitorprovider.ProviderAppDynamics}}}}
	if err := server.runner.validateRetiredExecution("run", ""); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("retired criteria admitted: %v", err)
	}
	if err := validateRetiredMonitorRules([]analysis.MonitorRule{{Provider: monitorprovider.ProviderDynatrace}, {Provider: monitorprovider.ProviderPrometheus}}); err != nil {
		t.Fatalf("supported criteria rejected: %v", err)
	}
}
