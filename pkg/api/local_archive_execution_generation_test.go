package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Orphan recovery identifies a failed-over local archive by the execution term
// in its final provenance. The write-once initial provenance never carries the
// term, so only the refresh before Finalize can record it.
func TestLocalRunArchiveFinalizeRecordsExecutingTerm(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "local-term.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := NewTestRunner(1, NewHub(), store)
	runner.StoreScript(&dsl.Script{
		ID: "local-term-script", Name: "Local Term", Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "Local term", Duration: "5m",
		Groups: []CreateGroupEntry{{Name: "buyers", ScriptID: "local-term-script", VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const executingTerm = 7
	runner.mu.Lock()
	test := runner.tests[created.ID]
	test.Status = "RUNNING"
	test.executionGeneration = executingTerm
	if err := runner.saveManagedTestRecord(test); err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	runner.mu.Unlock()
	artifacts, err := newRunArtifacts(runner, test, runner.saveRunArchiveRecord)
	if err != nil {
		t.Fatalf("newRunArtifacts: %v", err)
	}
	// Production callers hold the archive commit fence across finalize.
	unlockArchiveCommit := runner.lockArchiveCommitFence(test.ID)
	record, _, _ := artifacts.finalize(test, "FAILED", time.Now().UTC(), "execution owner relinquished leadership")
	unlockArchiveCommit()
	if record == nil {
		t.Fatal("local finalize produced no archive record")
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Provenance.ExecutionGeneration != executingTerm {
		t.Fatalf("final provenance execution generation = %d, want %d", manifest.Provenance.ExecutionGeneration, executingTerm)
	}
	if manifest.InitialProvenance != nil && manifest.InitialProvenance.ExecutionGeneration != 0 {
		t.Fatalf("manifest initial provenance carries execution generation %d", manifest.InitialProvenance.ExecutionGeneration)
	}
	stored, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if initial := decodeStoredInitialProvenance(stored.InitialProvenance); initial == nil || initial.ExecutionGeneration != 0 {
		t.Fatalf("stored initial provenance = %#v, want one without an execution term", initial)
	}
}
