package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type testPlacementFixture struct {
	store *SQLiteStore
}

func newTestPlacementFixture(t *testing.T, path string) testPlacementFixture {
	t.Helper()
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, projectID := range []string{"test-placement-project-a", "test-placement-project-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
		applicationID := "application-" + projectID
		if err := store.SaveApplication(&Application{ID: applicationID, ProjectID: projectID, Name: applicationID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveApplication(%s): %v", projectID, err)
		}
		releaseID := "release-" + projectID
		if err := store.SaveRelease(&Release{ID: releaseID, ApplicationID: applicationID, ProjectID: projectID, Name: releaseID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveRelease(%s): %v", projectID, err)
		}
		scriptID := "script-" + projectID
		if err := store.SaveScript(&dsl.Script{ID: scriptID, ProjectID: projectID, Name: scriptID, Protocol: "http", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveScript(%s): %v", projectID, err)
		}
		scenarioID := "scenario-" + projectID
		if err := store.SaveScenario(&Scenario{ID: scenarioID, Name: scenarioID, Duration: "1m", CreatedAt: now, UpdatedAt: now}, []ScenarioScript{{ScenarioID: scenarioID, ScriptID: scriptID, VUsers: 1}}); err != nil {
			t.Fatalf("SaveScenario(%s): %v", projectID, err)
		}
		folderID := "folder-" + projectID
		if err := store.SaveFolder(&Folder{ID: folderID, ProjectID: projectID, Name: folderID, Category: "general", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveFolder(%s): %v", projectID, err)
		}
	}
	if err := store.SaveApplication(&Application{
		ID: "application-test-placement-project-a-alt", ProjectID: "test-placement-project-a", Name: "alternate", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveApplication(alternate): %v", err)
	}
	if err := store.SaveRelease(&Release{
		ID: "release-test-placement-project-a-alt", ApplicationID: "application-test-placement-project-a-alt", ProjectID: "test-placement-project-a", Name: "alternate", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveRelease(alternate): %v", err)
	}
	return testPlacementFixture{store: store}
}

func (f testPlacementFixture) record(id, projectSuffix string) *TestRecord {
	projectID := "test-placement-project-" + projectSuffix
	folderID := "folder-" + projectID
	return &TestRecord{
		ID:            id,
		Name:          id,
		Status:        "QUEUED",
		ApplicationID: "application-" + projectID,
		ReleaseID:     "release-" + projectID,
		ScenarioID:    "scenario-" + projectID,
		ProjectID:     projectID,
		FolderID:      &folderID,
		StartTime:     time.Now().UTC(),
	}
}

type testPlacementWriter struct {
	name  string
	write func(*SQLiteStore, *TestRecord) (bool, error)
}

var testPlacementWriters = []testPlacementWriter{
	{
		name: "SaveTest",
		write: func(store *SQLiteStore, record *TestRecord) (bool, error) {
			err := store.SaveTest(record)
			return err == nil, err
		},
	},
	{
		name: "CreateTestIfAbsent",
		write: func(store *SQLiteStore, record *TestRecord) (bool, error) {
			return store.CreateTestIfAbsent(record)
		},
	},
}

func TestTestWritesRejectMissingAndCrossProjectParents(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*TestRecord)
		wantErrSub string
	}{
		{name: "missing project", mutate: func(record *TestRecord) { record.ProjectID = "missing-project" }, wantErrSub: "project not found"},
		{name: "missing application", mutate: func(record *TestRecord) { record.ApplicationID = "missing-application" }, wantErrSub: "application not found"},
		{name: "missing release", mutate: func(record *TestRecord) { record.ReleaseID = "missing-release" }, wantErrSub: "release not found"},
		{name: "missing scenario", mutate: func(record *TestRecord) { record.ScenarioID = "missing-scenario" }, wantErrSub: "scenario not found"},
		{name: "missing folder", mutate: func(record *TestRecord) { missing := "missing-folder"; record.FolderID = &missing }, wantErrSub: "folder not found"},
		{name: "foreign application", mutate: func(record *TestRecord) {
			record.ApplicationID = "application-test-placement-project-b"
			record.ReleaseID = "release-test-placement-project-b"
		}, wantErrSub: "belongs to project"},
		{name: "foreign release", mutate: func(record *TestRecord) { record.ReleaseID = "release-test-placement-project-b" }, wantErrSub: "belongs to application"},
		{name: "release from another application", mutate: func(record *TestRecord) { record.ReleaseID = "release-test-placement-project-a-alt" }, wantErrSub: "belongs to application"},
		{name: "foreign scenario", mutate: func(record *TestRecord) { record.ScenarioID = "scenario-test-placement-project-b" }, wantErrSub: "belongs to project"},
		{name: "foreign folder", mutate: func(record *TestRecord) {
			foreign := "folder-test-placement-project-b"
			record.FolderID = &foreign
		}, wantErrSub: "belongs to project"},
	}
	for _, writer := range testPlacementWriters {
		writer := writer
		for _, tc := range cases {
			tc := tc
			t.Run(writer.name+"/"+tc.name, func(t *testing.T) {
				fixture := newTestPlacementFixture(t, filepath.Join(t.TempDir(), "placement.db"))
				record := fixture.record("rejected-"+strings.ReplaceAll(tc.name, " ", "-"), "a")
				tc.mutate(record)
				created, err := writer.write(fixture.store, record)
				if err == nil || created || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("write = created %t, err %v; want error containing %q", created, err, tc.wantErrSub)
				}
				if _, err := fixture.store.GetTest(record.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("rejected test was persisted: %v", err)
				}
			})
		}
	}
}

func TestSaveTestRejectsProjectRelocation(t *testing.T) {
	fixture := newTestPlacementFixture(t, filepath.Join(t.TempDir(), "relocation.db"))
	original := fixture.record("immutable-test-project", "a")
	if err := fixture.store.SaveTest(original); err != nil {
		t.Fatalf("SaveTest(original): %v", err)
	}
	relocation := fixture.record(original.ID, "b")
	if err := fixture.store.SaveTest(relocation); err == nil || !strings.Contains(err.Error(), "belongs to project") {
		t.Fatalf("SaveTest(relocation) error = %v", err)
	}
	stored, err := fixture.store.GetTest(original.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if stored.ProjectID != original.ProjectID || stored.ApplicationID != original.ApplicationID || stored.ReleaseID != original.ReleaseID {
		t.Fatalf("relocation changed durable placement: %#v", stored)
	}
}

func TestCreateTestIfAbsentCollisionCannotRelocateProject(t *testing.T) {
	fixture := newTestPlacementFixture(t, filepath.Join(t.TempDir(), "create-collision.db"))
	original := fixture.record("create-only-immutable-project", "a")
	created, err := fixture.store.CreateTestIfAbsent(original)
	if err != nil || !created {
		t.Fatalf("CreateTestIfAbsent(original) = %t, %v", created, err)
	}
	relocation := fixture.record(original.ID, "b")
	created, err = fixture.store.CreateTestIfAbsent(relocation)
	if err != nil || created {
		t.Fatalf("CreateTestIfAbsent(relocation) = %t, %v", created, err)
	}
	stored, err := fixture.store.GetTest(original.ID)
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	if stored.ProjectID != original.ProjectID || stored.ApplicationID != original.ApplicationID || stored.ReleaseID != original.ReleaseID {
		t.Fatalf("create-only collision changed durable placement: %#v", stored)
	}
}

func TestConcurrentTestWritersCannotRelocateProject(t *testing.T) {
	for _, writer := range testPlacementWriters {
		writer := writer
		t.Run(writer.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "race.db")
			fixture := newTestPlacementFixture(t, path)
			peer, err := NewSQLiteStore(path)
			if err != nil {
				t.Fatalf("NewSQLiteStore(peer): %v", err)
			}
			defer peer.Close()

			for iteration := 0; iteration < 8; iteration++ {
				testID := fmt.Sprintf("concurrent-placement-%d", iteration)
				type outcome struct {
					projectID string
					created   bool
					err       error
				}
				start := make(chan struct{})
				results := make(chan outcome, 2)
				var wg sync.WaitGroup
				for _, candidate := range []struct {
					store   *SQLiteStore
					suffix  string
					project string
				}{
					{store: fixture.store, suffix: "a", project: "test-placement-project-a"},
					{store: peer, suffix: "b", project: "test-placement-project-b"},
				} {
					candidate := candidate
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						created, err := writer.write(candidate.store, fixture.record(testID, candidate.suffix))
						results <- outcome{projectID: candidate.project, created: created, err: err}
					}()
				}
				close(start)
				wg.Wait()
				close(results)

				var winner string
				createdCount := 0
				for result := range results {
					if result.created && result.err == nil {
						createdCount++
						winner = result.projectID
					}
				}
				if createdCount != 1 {
					t.Fatalf("iteration %d successful writers = %d, want 1", iteration, createdCount)
				}
				stored, err := fixture.store.GetTest(testID)
				if err != nil {
					t.Fatalf("GetTest(%s): %v", testID, err)
				}
				if stored.ProjectID != winner {
					t.Fatalf("iteration %d durable project = %q, successful writer = %q", iteration, stored.ProjectID, winner)
				}
			}
		})
	}
}

func TestSaveTestRejectsFirstInsertForArchivedRelease(t *testing.T) {
	fixture := newTestPlacementFixture(t, filepath.Join(t.TempDir(), "archived-release.db"))
	record := fixture.record("archived-release-save-test", "a")
	if err := fixture.store.ArchiveRelease(record.ReleaseID); err != nil {
		t.Fatalf("ArchiveRelease: %v", err)
	}
	if err := fixture.store.SaveTest(record); err == nil || !strings.Contains(err.Error(), "release is archived") {
		t.Fatalf("SaveTest archived release error = %v", err)
	}
	if _, err := fixture.store.GetTest(record.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("test against archived release was persisted: %v", err)
	}
}
