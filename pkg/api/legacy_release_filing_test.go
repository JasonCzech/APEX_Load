package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// TestRunWithoutReleaseIsFiledInItsProjectsLegacyRelease: a run that names no
// release goes to its own project's legacy application and release. Outside
// the default project those are suffixed ("legacy-<hash>"); filing the run
// under the bare IDs left it in an application its workspace does not have,
// and Command Center refused to open it.
func TestRunWithoutReleaseIsFiledInItsProjectsLegacyRelease(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.CreateProjectWithLegacyHierarchy(&storage.Project{ID: "proj-legacy", Name: "With hierarchy", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateProjectWithLegacyHierarchy: %v", err)
	}
	// A project saved without the hierarchy (as an old store or an import may
	// leave it) keeps the bare IDs, so its runs still persist.
	if err := store.SaveProject(&storage.Project{ID: "proj-bare", Name: "Without hierarchy", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	runner := NewTestRunner(100, NewHub(), store)
	for _, project := range []string{"proj-legacy", "proj-bare"} {
		runner.StoreScript(&dsl.Script{
			ID: "script-" + project, Name: "S", Protocol: "http", Version: "1", ProjectID: project,
			Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
		})
	}

	scoped, err := runner.CreateTest(CreateTestRequest{Name: "scoped", Duration: "1m", ScriptID: "script-proj-legacy", VUsers: 1, ProjectID: "proj-legacy"})
	if err != nil {
		t.Fatalf("CreateTest(proj-legacy): %v", err)
	}
	if scoped.ApplicationID != storage.LegacyApplicationIDForProject("proj-legacy") || scoped.ReleaseID != storage.LegacyReleaseIDForProject("proj-legacy") {
		t.Fatalf("run filed under %q/%q, want the project's own legacy %q/%q", scoped.ApplicationID, scoped.ReleaseID, storage.LegacyApplicationIDForProject("proj-legacy"), storage.LegacyReleaseIDForProject("proj-legacy"))
	}
	if scoped.ApplicationID == storage.LegacyApplicationID {
		t.Fatal("a run outside the default project was filed under the bare legacy application")
	}

	bare, err := runner.CreateTest(CreateTestRequest{Name: "bare", Duration: "1m", ScriptID: "script-proj-bare", VUsers: 1, ProjectID: "proj-bare"})
	if err != nil {
		t.Fatalf("CreateTest(proj-bare): %v", err)
	}
	if bare.ApplicationID != storage.LegacyApplicationID || bare.ReleaseID != storage.LegacyReleaseID {
		t.Fatalf("run in a project without its legacy release filed under %q/%q, want the bare IDs", bare.ApplicationID, bare.ReleaseID)
	}
}
