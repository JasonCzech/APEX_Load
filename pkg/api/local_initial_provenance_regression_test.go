package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestNewLocalRunArtifactsPersistImmutableInitialProvenance(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "initial-local.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := NewTestRunner(1, NewHub(), store)
	runner.StoreScript(&dsl.Script{
		ID: "local-initial-script", Name: "Local Initial", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "Local initial", Duration: "5m",
		Groups: []CreateGroupEntry{{Name: "buyers", ScriptID: "local-initial-script", VUsers: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	runner.mu.Lock()
	test := runner.tests[created.ID]
	test.Status = "INITIALIZING"
	if err := runner.saveManagedTestRecord(test); err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	runner.mu.Unlock()
	artifacts, err := newRunArtifacts(runner, test, runner.saveRunArchiveRecord)
	if err != nil {
		t.Fatalf("newRunArtifacts: %v", err)
	}
	if _, err := artifacts.recorder.Finalize("ABORTED", now, "test cleanup", false, false, nil); err != nil {
		t.Fatalf("finalize archive writer: %v", err)
	}

	stored, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := decodeStoredInitialProvenance(stored.InitialProvenance)
	if initial == nil || initial.TotalVUsers != 10 || initial.Duration != "5m" {
		t.Fatalf("stored initial provenance = %#v", initial)
	}

	runner.mu.Lock()
	test.TotalVUsers = 30
	test.Groups[0].VUsers = 30
	test.Duration = 15 * time.Minute
	test.DurationStr = "15m"
	if err := runner.saveManagedTestRecord(test); err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	// Simulate a process-local cache miss/restart so the manifest must recover
	// the immutable snapshot from the durable initial_provenance column.
	test.initialArchiveProvenance = nil
	runner.mu.Unlock()
	manifest, err := runner.GetTestManifest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.InitialConfiguration.TotalVUsers != 10 || manifest.InitialConfiguration.Duration != "5m" {
		t.Fatalf("initial configuration = %#v, want immutable 10 VUsers / 5m", manifest.InitialConfiguration)
	}
	if manifest.CurrentConfiguration.TotalVUsers != 30 || manifest.CurrentConfiguration.Duration != "15m" {
		t.Fatalf("current configuration = %#v, want 30 VUsers / 15m", manifest.CurrentConfiguration)
	}
	if !manifest.InitialConfigurationProvenance.Immutable || manifest.InitialConfigurationProvenance.Source != testRunManifestInitialConfigurationStoredProvenance {
		t.Fatalf("initial provenance marker = %#v", manifest.InitialConfigurationProvenance)
	}
}

func TestNewLocalRunArtifactsUsesPreexistingAuthoritativeProvenance(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "resumed-local.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := NewTestRunner(1, NewHub(), store)
	runner.StoreScript(&dsl.Script{
		ID: "resumed-local-script", Name: "Resumed Local", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "Resumed local", Duration: "5m",
		Groups: []CreateGroupEntry{{Name: "buyers", ScriptID: "resumed-local-script", VUsers: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}

	runner.mu.Lock()
	test := runner.tests[created.ID]
	test.Status = "INITIALIZING"
	runner.mu.Unlock()
	preexisting := maskRunArchiveProvenance(buildRunArchiveProvenance(runner, test))
	preexisting.TotalVUsers = 7
	preexisting.Duration = "7m"
	runner.mu.Lock()
	test.initialArchiveProvenance = &preexisting
	if err := runner.saveManagedTestRecord(test); err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	// Reproduce a retried INITIALIZING callback after mutable run fields changed.
	test.TotalVUsers = 30
	test.Groups[0].VUsers = 30
	test.Duration = 15 * time.Minute
	test.DurationStr = "15m"
	if err := runner.saveManagedTestRecord(test); err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	runner.mu.Unlock()

	artifacts, err := newRunArtifacts(runner, test, runner.saveRunArchiveRecord)
	if err != nil {
		t.Fatalf("newRunArtifacts: %v", err)
	}
	now := time.Now().UTC()
	if _, err := artifacts.recorder.Finalize("ABORTED", now, "test cleanup", false, false, nil); err != nil {
		t.Fatalf("finalize archive writer: %v", err)
	}
	writer, ok := artifacts.recorder.(*runArchiveWriter)
	if !ok {
		t.Fatalf("local recorder type = %T, want *runArchiveWriter", artifacts.recorder)
	}
	manifest := writer.manifest
	if manifest.InitialProvenance == nil || manifest.InitialProvenance.TotalVUsers != 7 || manifest.InitialProvenance.Duration != "7m" {
		t.Fatalf("archive initial provenance = %#v, want preserved 7 VUsers / 7m", manifest.InitialProvenance)
	}
	if manifest.Provenance.TotalVUsers != 7 || manifest.Provenance.Duration != "7m" {
		t.Fatalf("archive provenance = %#v, want preserved initial snapshot", manifest.Provenance)
	}
	stored, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	durable := decodeStoredInitialProvenance(stored.InitialProvenance)
	if durable == nil || durable.TotalVUsers != 7 || durable.Duration != "7m" {
		t.Fatalf("durable initial provenance = %#v, want preserved 7 VUsers / 7m", durable)
	}
}
