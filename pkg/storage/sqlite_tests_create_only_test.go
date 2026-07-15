package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCreateTestIfAbsentCannotReplaceLifecycleEvidence(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "create-only-tests.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, id := range []string{"project-a", "project-b"} {
		if err := store.SaveProject(&Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	original := &TestRecord{
		ID:        "fixed-test-id",
		Name:      "completed evidence",
		Status:    "COMPLETE",
		ProjectID: "project-a",
		StartTime: now,
		EndTime:   &now,
	}
	created, err := store.CreateTestIfAbsent(original)
	if err != nil || !created {
		t.Fatalf("first create = %t, %v", created, err)
	}
	replacement := &TestRecord{
		ID:        original.ID,
		Name:      "replacement",
		Status:    "QUEUED",
		ProjectID: "project-b",
		StartTime: now.Add(time.Hour),
	}
	created, err = store.CreateTestIfAbsent(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate deterministic test identity was inserted")
	}
	stored, err := store.GetTest(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != original.Name || stored.Status != original.Status || stored.ProjectID != original.ProjectID || stored.EndTime == nil {
		t.Fatalf("existing lifecycle evidence was replaced: %#v", stored)
	}
}

func TestCreateTestIfAbsentIfLeaseHeldRejectsStaleOwner(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "create-only-lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "project-a", Name: "project-a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	const leaseName = "execution"
	if acquired, _, err := store.TryAcquireSchedulerLeaseTerm(leaseName, "owner-old", time.Time{}, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire old = %t, %v", acquired, err)
	}
	if err := store.ReleaseSchedulerLease(leaseName, "owner-old"); err != nil {
		t.Fatal(err)
	}
	if acquired, _, err := store.TryAcquireSchedulerLeaseTerm(leaseName, "owner-new", time.Time{}, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire new = %t, %v", acquired, err)
	}
	record := &TestRecord{ID: "stale-create", Name: "stale", Status: "QUEUED", ProjectID: "project-a", StartTime: now}
	if created, err := store.CreateTestIfAbsentIfLeaseHeld(record, leaseName, "owner-old"); err != nil || created {
		t.Fatalf("stale create = %t, %v", created, err)
	}
	if _, err := store.GetTest(record.ID); err == nil {
		t.Fatal("stale lease inserted a test row")
	}
	if created, err := store.CreateTestIfAbsentIfLeaseHeld(record, leaseName, "owner-new"); err != nil || !created {
		t.Fatalf("current create = %t, %v", created, err)
	}
}

func TestExplicitReleaseCannotRaceArchiveAndTestCreation(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "release-create-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	project := &Project{ID: "release-race-project", Name: "release race", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	application := &Application{ID: "release-race-app", ProjectID: project.ID, Name: "app", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveApplication(application); err != nil {
		t.Fatal(err)
	}
	release := &Release{ID: "release-race", ApplicationID: application.ID, ProjectID: project.ID, Name: "release", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveRelease(release); err != nil {
		t.Fatal(err)
	}
	record := &TestRecord{ID: "release-race-test", Name: "test", Status: "DRAFT", ApplicationID: application.ID, ReleaseID: release.ID, ProjectID: project.ID, StartTime: now}

	start := make(chan struct{})
	archiveErr := make(chan error, 1)
	createResult := make(chan struct {
		created bool
		err     error
	}, 1)
	go func() {
		<-start
		archiveErr <- store.ArchiveRelease(release.ID)
	}()
	go func() {
		<-start
		created, err := store.CreateTestIfAbsent(record)
		createResult <- struct {
			created bool
			err     error
		}{created: created, err: err}
	}()
	close(start)
	archiveResult := <-archiveErr
	create := <-createResult
	if archiveResult == nil && create.err == nil && create.created {
		t.Fatal("archive and test creation both succeeded for the same release")
	}
	persistedRelease, err := store.GetRelease(release.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, testErr := store.GetTest(record.ID)
	if persistedRelease.ArchivedAt != nil && testErr == nil {
		t.Fatal("archived release has a concurrently created test")
	}
}

func TestCreateTestIfAbsentRejectsAlreadyArchivedRelease(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "archived-release-create.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	project := &Project{ID: "archived-project", Name: "archived", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	application := &Application{ID: "archived-app", ProjectID: project.ID, Name: "app", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveApplication(application); err != nil {
		t.Fatal(err)
	}
	release := &Release{ID: "archived-release", ApplicationID: application.ID, ProjectID: project.ID, Name: "release", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveRelease(release); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveRelease(release.ID); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateTestIfAbsent(&TestRecord{ID: "archived-test", Name: "test", Status: "DRAFT", ApplicationID: application.ID, ReleaseID: release.ID, ProjectID: project.ID, StartTime: now})
	if err == nil || created {
		t.Fatalf("create against archived release = %t, %v", created, err)
	}
	if _, err := store.GetTest("archived-test"); err == nil {
		t.Fatal("test against archived release was persisted")
	}
}
