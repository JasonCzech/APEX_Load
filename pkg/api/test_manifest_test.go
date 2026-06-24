package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestGetTestManifestBuildsLiveConfigurationAndRuntimeChanges(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	createdAt := time.Date(2026, 6, 7, 14, 0, 0, 0, time.UTC)
	initialGroups := []TestGroup{{
		Name:       "buyers",
		ScriptID:   "script-1",
		ScriptName: "Checkout Flow",
		Protocol:   "http",
		VUsers:     10,
		RampUp:     "1m",
		RampDown:   "30s",
	}}
	if err := store.SaveTest(&storage.TestRecord{
		ID:          "test-live-manifest",
		RefKey:      "RUN-20260607-001",
		Name:        "Checkout Run",
		Status:      "RUNNING",
		ProjectID:   "project-1",
		TotalVUsers: 10,
		Duration:    int64(5 * time.Minute),
		StartTime:   createdAt,
		Groups:      initialGroups,
		Assignment:  &GeneratorAssignmentSettings{Mode: "specific_hosts", SelectedTargets: []string{"generator-a"}},
	}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}

	runner := NewTestRunner(2, NewHub(), store)
	runner.mu.Lock()
	test := runner.tests["test-live-manifest"]
	test.Status = "RUNNING"
	test.Groups[0].VUsers = 25
	test.TotalVUsers = 25
	test.LiveMetrics = &LiveMetrics{ActiveVUsers: 22}
	test.GeneratorShards = []GeneratorShardSummary{{Group: "buyers", Target: "generator-a", VUsers: 25, ShardStrategy: "capacity_weighted"}}
	runner.mu.Unlock()

	saveAuditEvent(t, store, &storage.AuditEvent{
		ID:               "evt-duration",
		Action:           "test.duration.adjust",
		ActorType:        "user",
		ActorSubject:     "ops@example.com",
		ActorDisplayName: "Ops User",
		ProjectID:        "project-1",
		ResourceType:     "test",
		ResourceID:       "test-live-manifest",
		Status:           "success",
		Details:          map[string]any{"duration": "10m", "api_key": "do-not-copy"},
		CreatedAt:        createdAt.Add(2 * time.Minute),
	})
	saveAuditEvent(t, store, &storage.AuditEvent{
		ID:               "evt-start",
		Action:           "test.start",
		ActorType:        "user",
		ActorSubject:     "ops@example.com",
		ActorDisplayName: "Ops User",
		ProjectID:        "project-1",
		ResourceType:     "test",
		ResourceID:       "RUN-20260607-001",
		Status:           "success",
		Details:          map[string]any{"status": "RUNNING"},
		CreatedAt:        createdAt.Add(time.Minute),
	})

	manifest, err := runner.GetTestManifest("test-live-manifest")
	if err != nil {
		t.Fatalf("GetTestManifest returned error: %v", err)
	}
	if manifest.Source != "live" {
		t.Fatalf("manifest source = %q, want live", manifest.Source)
	}
	if manifest.InitialConfiguration.TotalVUsers != 10 {
		t.Fatalf("initial total vusers = %d, want 10", manifest.InitialConfiguration.TotalVUsers)
	}
	if manifest.CurrentConfiguration.TotalVUsers != 25 || manifest.CurrentConfiguration.ActiveVUsers != 22 {
		t.Fatalf("current config = %#v, want total 25 active 22", manifest.CurrentConfiguration)
	}
	if len(manifest.Scripts) != 1 || manifest.Scripts[0].VUsers != 25 {
		t.Fatalf("scripts = %#v, want current script VUsers 25", manifest.Scripts)
	}
	if len(manifest.LoadGenerators.GeneratorShards) != 1 || manifest.LoadGenerators.GeneratorShards[0].Target != "generator-a" {
		t.Fatalf("generator shards = %#v, want generator-a", manifest.LoadGenerators.GeneratorShards)
	}
	if len(manifest.RuntimeChanges) != 2 {
		t.Fatalf("runtime changes = %#v, want 2", manifest.RuntimeChanges)
	}
	if manifest.RuntimeChanges[0].Action != "test.start" || manifest.RuntimeChanges[1].Action != "test.duration.adjust" {
		t.Fatalf("runtime changes not chronological: %#v", manifest.RuntimeChanges)
	}
	if _, leaked := manifest.RuntimeChanges[1].Details["api_key"]; leaked {
		t.Fatalf("unsafe audit detail leaked into manifest: %#v", manifest.RuntimeChanges[1].Details)
	}
}

func TestTestManifestArchiveArtifactAndFallback(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", filepath.Join(t.TempDir(), "archives"))

	runner := NewTestRunner(2, NewHub(), store)
	completedAt := time.Date(2026, 6, 7, 15, 0, 0, 0, time.UTC)
	test := &ManagedTest{
		ID:          "test-archived-manifest",
		Name:        "Archived Run",
		Status:      "COMPLETE",
		ProjectID:   "project-1",
		TotalVUsers: 20,
		Duration:    5 * time.Minute,
		DurationStr: "5m0s",
		CreatedAt:   completedAt.Add(-5 * time.Minute),
		EndedAt:     &completedAt,
		Groups: []TestGroup{{
			Name:       "buyers",
			ScriptID:   "script-1",
			ScriptName: "Checkout Flow",
			Protocol:   "http",
			VUsers:     20,
		}},
	}
	saveStoredManifestTest(t, store, test)
	if err := store.CreateTestNote(&storage.TestNote{
		ID:        "manifest-note-1",
		TestID:    test.ID,
		Body:      "# During Run\n\nLatency elevated during ramp.",
		CreatedBy: "operator",
		UpdatedBy: "operator",
	}); err != nil {
		t.Fatalf("CreateTestNote returned error: %v", err)
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	writer, err := newRunArchiveWriter(test, buildRunArchiveProvenance(runner, test))
	if err != nil {
		t.Fatalf("newRunArchiveWriter returned error: %v", err)
	}
	record, err := writer.Finalize("COMPLETE", completedAt, "", false, nil)
	if err != nil {
		t.Fatalf("Finalize returned error: %v", err)
	}
	if err := runner.persistTestManifestArtifact(test, record); err != nil {
		t.Fatalf("persistTestManifestArtifact returned error: %v", err)
	}
	runner.mu.Lock()
	runner.archives[test.ID] = record
	runner.mu.Unlock()

	archiveManifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		t.Fatalf("loadRunArchiveManifest returned error: %v", err)
	}
	if archiveManifest.TestManifestFile != testRunManifestFileName {
		t.Fatalf("archive manifest test_manifest_file = %q, want %q", archiveManifest.TestManifestFile, testRunManifestFileName)
	}
	if archiveManifest.TestNotesFile != testNotesArchiveFileName {
		t.Fatalf("archive manifest test_notes_file = %q, want %q", archiveManifest.TestNotesFile, testNotesArchiveFileName)
	}
	artifacts, err := archiveManifestArtifactNames(archiveManifest)
	if err != nil {
		t.Fatalf("archiveManifestArtifactNames returned error: %v", err)
	}
	if _, ok := artifacts[testNotesArchiveFileName]; !ok {
		t.Fatalf("artifact allowlist does not include %q: %#v", testNotesArchiveFileName, artifacts)
	}
	var notesArtifact testNotesArchiveArtifact
	notesData, err := os.ReadFile(filepath.Join(record.ArchivePath, testNotesArchiveFileName))
	if err != nil {
		t.Fatalf("read test notes artifact: %v", err)
	}
	if err := json.Unmarshal(notesData, &notesArtifact); err != nil {
		t.Fatalf("decode test notes artifact: %v", err)
	}
	if notesArtifact.TestID != test.ID || len(notesArtifact.Notes) != 1 || notesArtifact.Notes[0].Body != "# During Run\n\nLatency elevated during ramp." {
		t.Fatalf("test notes artifact = %#v", notesArtifact)
	}
	persisted, err := runner.GetTestManifest(test.ID)
	if err != nil {
		t.Fatalf("GetTestManifest persisted returned error: %v", err)
	}
	if persisted.Source != "archive" {
		t.Fatalf("persisted manifest source = %q, want archive", persisted.Source)
	}
	if persisted.Archive == nil || persisted.Archive.TestManifestFile != testRunManifestFileName {
		t.Fatalf("persisted archive info = %#v, want test manifest file", persisted.Archive)
	}
	if persisted.Archive.TestNotesFile != testNotesArchiveFileName {
		t.Fatalf("persisted archive test_notes_file = %q, want %q", persisted.Archive.TestNotesFile, testNotesArchiveFileName)
	}

	fallbackTest := &ManagedTest{
		ID:          "test-archive-fallback",
		Name:        "Fallback Run",
		Status:      "COMPLETE",
		ProjectID:   "project-1",
		TotalVUsers: 5,
		Duration:    time.Minute,
		DurationStr: "1m0s",
		CreatedAt:   completedAt.Add(-time.Minute),
		EndedAt:     &completedAt,
		Groups:      []TestGroup{{Name: "api", ScriptID: "script-2", ScriptName: "API Flow", Protocol: "http", VUsers: 5}},
	}
	saveStoredManifestTest(t, store, fallbackTest)
	fallbackDir := filepath.Join(t.TempDir(), "old-archive")
	if err := os.MkdirAll(fallbackDir, 0o755); err != nil {
		t.Fatalf("create fallback archive dir: %v", err)
	}
	fallbackManifestPath := filepath.Join(fallbackDir, "manifest.json")
	if err := writeIndentedJSONFile(fallbackManifestPath, RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion,
		TestID:        fallbackTest.ID,
		TestName:      fallbackTest.Name,
		Status:        "COMPLETE",
		CompletedAt:   completedAt,
		Provenance:    buildRunArchiveProvenance(runner, fallbackTest),
	}); err != nil {
		t.Fatalf("write fallback archive manifest: %v", err)
	}
	fallbackRecord := &storage.RunArchiveRecord{
		TestID:        fallbackTest.ID,
		SchemaVersion: archiveSchemaVersion,
		ArchivePath:   fallbackDir,
		ManifestPath:  fallbackManifestPath,
		CreatedAt:     fallbackTest.CreatedAt,
		CompletedAt:   completedAt,
	}
	runner.mu.Lock()
	runner.tests[fallbackTest.ID] = fallbackTest
	runner.archives[fallbackTest.ID] = fallbackRecord
	runner.mu.Unlock()

	fallback, err := runner.GetTestManifest(fallbackTest.ID)
	if err != nil {
		t.Fatalf("GetTestManifest fallback returned error: %v", err)
	}
	if fallback.Source != "archive_fallback" {
		t.Fatalf("fallback source = %q, want archive_fallback", fallback.Source)
	}
	if !containsWarning(fallback.Warnings, "archive does not contain a persisted test_manifest.json") {
		t.Fatalf("fallback warnings = %#v, want old archive warning", fallback.Warnings)
	}
}

func saveAuditEvent(t *testing.T, store *storage.SQLiteStore, event *storage.AuditEvent) {
	t.Helper()
	if err := store.SaveAuditEvent(event); err != nil {
		t.Fatalf("SaveAuditEvent returned error: %v", err)
	}
}

func saveStoredManifestTest(t *testing.T, store *storage.SQLiteStore, test *ManagedTest) {
	t.Helper()
	if err := store.SaveTest(buildManagedTestRecord(test)); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
}

func containsWarning(warnings []string, fragment string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, fragment) {
			return true
		}
	}
	return false
}
