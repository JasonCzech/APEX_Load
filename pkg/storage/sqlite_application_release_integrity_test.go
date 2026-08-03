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
)

func TestSaveProjectAtomicallyProvisionsIdempotentLegacyHierarchy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-hierarchy.db")
	project := &Project{ID: "project-hierarchy", Name: "Hierarchy", CreatedAt: time.Now().UTC()}

	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first): %v", err)
	}
	if err := first.CreateProjectWithLegacyHierarchy(project); err != nil {
		first.Close()
		t.Fatalf("SaveProject(create): %v", err)
	}
	if err := first.CreateProjectWithLegacyHierarchy(project); err != nil {
		first.Close()
		t.Fatalf("SaveProject(idempotent update): %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(second): %v", err)
	}
	defer second.Close()
	applicationID := legacyApplicationID(project.ID)
	releaseID := legacyReleaseID(project.ID)
	application, err := second.GetApplication(applicationID)
	if err != nil || application.ProjectID != project.ID || application.Name != "Legacy" {
		t.Fatalf("canonical application after restart = %#v, err=%v", application, err)
	}
	release, err := second.GetRelease(releaseID)
	if err != nil || release.ProjectID != project.ID || release.ApplicationID != applicationID || release.Name != "Imported Legacy" {
		t.Fatalf("canonical release after restart = %#v, err=%v", release, err)
	}
	if err := second.DeleteApplication(applicationID); err == nil {
		t.Fatal("DeleteApplication removed a project-scoped canonical Legacy application")
	}
	if err := second.ArchiveRelease(releaseID); err == nil {
		t.Fatal("ArchiveRelease archived a project-scoped canonical Legacy release")
	}
	if err := second.DeleteRelease(releaseID); err == nil {
		t.Fatal("DeleteRelease removed a project-scoped canonical Legacy release")
	}
	applications, err := second.ListApplications()
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}
	count := 0
	for _, candidate := range applications {
		if candidate.ProjectID == project.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("project applications after restart = %d, want one canonical application", count)
	}
}

func TestSaveProjectRollsBackWhenLegacyHierarchyCannotBeCreated(t *testing.T) {
	store := newProjectEntityCASStore(t)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_atomic_project_hierarchy
		BEFORE INSERT ON applications
		WHEN NEW.project_id = 'atomic-project'
		BEGIN
			SELECT RAISE(ABORT, 'injected hierarchy failure');
		END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	err := store.CreateProjectWithLegacyHierarchy(&Project{ID: "atomic-project", Name: "Atomic", CreatedAt: time.Now().UTC()})
	if err == nil || !strings.Contains(err.Error(), "injected hierarchy failure") {
		t.Fatalf("SaveProject error = %v, want injected hierarchy failure", err)
	}
	if _, err := store.GetProject("atomic-project"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("project survived failed hierarchy transaction: %v", err)
	}
}

func TestCreateProjectWithLegacyHierarchyRejectsConflictingCanonicalApplication(t *testing.T) {
	store := newProjectEntityCASStore(t)
	const projectID = "canonical-application-conflict"
	applicationID := legacyApplicationID(projectID)
	if err := store.SaveApplication(&Application{ID: applicationID, ProjectID: "project-a", Name: "Existing application"}); err != nil {
		t.Fatalf("seed conflicting application: %v", err)
	}

	err := store.CreateProjectWithLegacyHierarchy(&Project{ID: projectID, Name: "Must roll back", CreatedAt: time.Now().UTC()})
	if err == nil || !strings.Contains(err.Error(), "belongs to project") {
		t.Fatalf("CreateProjectWithLegacyHierarchy error = %v, want application placement conflict", err)
	}
	if _, err := store.GetProject(projectID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("project survived conflicting application transaction: %v", err)
	}
	application, err := store.GetApplication(applicationID)
	if err != nil || application.ProjectID != "project-a" || application.Name != "Existing application" {
		t.Fatalf("conflicting application changed = %#v, err=%v", application, err)
	}
	if _, err := store.GetRelease(legacyReleaseID(projectID)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("release survived conflicting application transaction: %v", err)
	}
}

func TestCreateProjectWithLegacyHierarchyRejectsConflictingCanonicalRelease(t *testing.T) {
	store := newProjectEntityCASStore(t)
	const projectID = "canonical-release-conflict"
	owner := &Application{ID: "release-conflict-owner", ProjectID: "project-a", Name: "Existing application"}
	if err := store.SaveApplication(owner); err != nil {
		t.Fatalf("seed release owner: %v", err)
	}
	releaseID := legacyReleaseID(projectID)
	if err := store.SaveRelease(&Release{ID: releaseID, ApplicationID: owner.ID, ProjectID: owner.ProjectID, Name: "Existing release"}); err != nil {
		t.Fatalf("seed conflicting release: %v", err)
	}

	err := store.CreateProjectWithLegacyHierarchy(&Project{ID: projectID, Name: "Must roll back", CreatedAt: time.Now().UTC()})
	if err == nil || !strings.Contains(err.Error(), "belongs to application") {
		t.Fatalf("CreateProjectWithLegacyHierarchy error = %v, want release placement conflict", err)
	}
	if _, err := store.GetProject(projectID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("project survived conflicting release transaction: %v", err)
	}
	if _, err := store.GetApplication(legacyApplicationID(projectID)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("canonical application survived conflicting release transaction: %v", err)
	}
	release, err := store.GetRelease(releaseID)
	if err != nil || release.ApplicationID != owner.ID || release.ProjectID != owner.ProjectID || release.Name != "Existing release" {
		t.Fatalf("conflicting release changed = %#v, err=%v", release, err)
	}
}

func TestCreateProjectWithLegacyHierarchyRejectsArchivedCanonicalRelease(t *testing.T) {
	store := newProjectEntityCASStore(t)
	const projectID = "archived-canonical-release"
	if err := store.SaveProject(&Project{ID: projectID, Name: "Original project"}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	application := &Application{ID: legacyApplicationID(projectID), ProjectID: projectID, Name: "Renamed legacy application"}
	if err := store.SaveApplication(application); err != nil {
		t.Fatalf("seed canonical application: %v", err)
	}
	release := &Release{ID: legacyReleaseID(projectID), ApplicationID: application.ID, ProjectID: projectID, Name: "Renamed legacy release"}
	if err := store.SaveRelease(release); err != nil {
		t.Fatalf("seed canonical release: %v", err)
	}
	archivedAt := time.Now().UTC()
	if _, err := store.db.Exec(`UPDATE releases SET archived_at = ? WHERE id = ?`, archivedAt, release.ID); err != nil {
		t.Fatalf("archive canonical release fixture: %v", err)
	}

	err := store.CreateProjectWithLegacyHierarchy(&Project{ID: projectID, Name: "Updated project"})
	if err == nil || !strings.Contains(err.Error(), "is archived") {
		t.Fatalf("CreateProjectWithLegacyHierarchy error = %v, want archived release conflict", err)
	}
	project, err := store.GetProject(projectID)
	if err != nil || project.Name != "Original project" {
		t.Fatalf("project update was not rolled back = %#v, err=%v", project, err)
	}
	persisted, err := store.GetRelease(release.ID)
	if err != nil || persisted.ArchivedAt == nil || persisted.Name != release.Name {
		t.Fatalf("archived release changed = %#v, err=%v", persisted, err)
	}
}

func TestApplicationAndReleaseWritesRequireImmutableDurableParents(t *testing.T) {
	store := newProjectEntityCASStore(t)
	if err := store.SaveApplication(nil); err == nil {
		t.Fatal("SaveApplication(nil) succeeded")
	}
	if err := store.SaveApplication(&Application{ID: "missing-project-app", ProjectID: "missing-project", Name: "missing"}); err == nil {
		t.Fatal("SaveApplication accepted a missing project")
	}
	if err := store.SaveApplication(&Application{ID: "placed-app", ProjectID: "project-a", Name: "placed"}); err != nil {
		t.Fatalf("SaveApplication(create): %v", err)
	}
	if err := store.SaveApplication(&Application{ID: "placed-app", ProjectID: "project-b", Name: "moved"}); err == nil || !strings.Contains(err.Error(), "already belongs") {
		t.Fatalf("SaveApplication relocation error = %v", err)
	}
	application, err := store.GetApplication("placed-app")
	if err != nil || application.ProjectID != "project-a" || application.Name != "placed" {
		t.Fatalf("application after rejected relocation = %#v, err=%v", application, err)
	}

	if err := store.SaveRelease(nil); err == nil {
		t.Fatal("SaveRelease(nil) succeeded")
	}
	if err := store.SaveRelease(&Release{ID: "missing-app-release", ApplicationID: "missing-app", ProjectID: "project-a", Name: "missing"}); err == nil {
		t.Fatal("SaveRelease accepted a missing application")
	}
	if err := store.SaveRelease(&Release{ID: "placed-release", ApplicationID: "placed-app", ProjectID: "project-a", Name: "placed"}); err != nil {
		t.Fatalf("SaveRelease(create): %v", err)
	}
	if err := store.SaveApplication(&Application{ID: "other-app", ProjectID: "project-b", Name: "other"}); err != nil {
		t.Fatalf("SaveApplication(other): %v", err)
	}
	if err := store.SaveRelease(&Release{ID: "placed-release", ApplicationID: "other-app", ProjectID: "project-b", Name: "moved"}); err == nil || !strings.Contains(err.Error(), "already belongs") {
		t.Fatalf("SaveRelease relocation error = %v", err)
	}
	release, err := store.GetRelease("placed-release")
	if err != nil || release.ApplicationID != "placed-app" || release.ProjectID != "project-a" || release.Name != "placed" {
		t.Fatalf("release after rejected relocation = %#v, err=%v", release, err)
	}
}

func TestReleaseAndApplicationDeletionRejectEveryDurableReference(t *testing.T) {
	for _, tc := range []struct {
		name   string
		insert func(t *testing.T, store *SQLiteStore, releaseID string)
	}{
		{
			name: "external result import",
			insert: func(t *testing.T, store *SQLiteStore, releaseID string) {
				t.Helper()
				if _, err := store.db.Exec(`INSERT INTO external_result_imports (id, provider, name, project_id, release_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`, "external-"+releaseID, "jmeter", "import", "project-a", releaseID, time.Now().UTC()); err != nil {
					t.Fatalf("insert external result import: %v", err)
				}
			},
		},
		{
			name: "scenario schedule",
			insert: func(t *testing.T, store *SQLiteStore, releaseID string) {
				t.Helper()
				now := time.Now().UTC()
				if _, err := store.db.Exec(`INSERT INTO scenarios (id, name, duration, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, "scenario-"+releaseID, "scenario", "1m", now, now); err != nil {
					t.Fatalf("insert scenario: %v", err)
				}
				if _, err := store.db.Exec(`INSERT INTO scenario_schedules (id, scenario_id, project_id, name, cron_expr, release_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "schedule-"+releaseID, "scenario-"+releaseID, "project-a", "schedule", "0 * * * *", releaseID, now, now); err != nil {
					t.Fatalf("insert scenario schedule: %v", err)
				}
			},
		},
		{
			name: "SLA waiver",
			insert: func(t *testing.T, store *SQLiteStore, releaseID string) {
				t.Helper()
				now := time.Now().UTC()
				if _, err := store.db.Exec(`INSERT INTO release_sla_waivers (id, release_id, project_id, scope, reason, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "waiver-"+releaseID, releaseID, "project-a", "release", "accepted", "active", now, now); err != nil {
					t.Fatalf("insert SLA waiver: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newProjectEntityCASStore(t)
			applicationID := "application-reference"
			releaseID := "release-reference"
			if err := store.SaveApplication(&Application{ID: applicationID, ProjectID: "project-a", Name: "app"}); err != nil {
				t.Fatalf("SaveApplication: %v", err)
			}
			if err := store.SaveRelease(&Release{ID: releaseID, ApplicationID: applicationID, ProjectID: "project-a", Name: "release"}); err != nil {
				t.Fatalf("SaveRelease: %v", err)
			}
			tc.insert(t, store, releaseID)

			if err := store.DeleteRelease(releaseID); err == nil {
				t.Fatal("DeleteRelease removed a referenced release")
			}
			if tc.name != "SLA waiver" {
				if err := store.ArchiveRelease(releaseID); err == nil {
					t.Fatal("ArchiveRelease archived a release referenced by a result or schedule")
				}
			}
			if err := store.DeleteApplication(applicationID); err == nil {
				t.Fatal("DeleteApplication removed a hierarchy with durable references")
			}
			if _, err := store.GetRelease(releaseID); err != nil {
				t.Fatalf("referenced release disappeared after rejected deletes: %v", err)
			}
		})
	}
}

func TestDeleteApplicationRejectsDirectExternalResultReference(t *testing.T) {
	store := newProjectEntityCASStore(t)
	now := time.Now().UTC()
	applicationID := "application-direct-result"
	if err := store.SaveApplication(&Application{ID: applicationID, ProjectID: "project-a", Name: "app"}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO external_result_imports
		(id, provider, name, project_id, application_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, "direct-result", "jmeter", "result", "project-a", applicationID, now); err != nil {
		t.Fatalf("insert direct external result: %v", err)
	}

	if err := store.DeleteApplication(applicationID); err == nil || !strings.Contains(err.Error(), "associated results") {
		t.Fatalf("DeleteApplication error = %v, want governed dependency conflict", err)
	}
	if _, err := store.GetApplication(applicationID); err != nil {
		t.Fatalf("referenced application disappeared: %v", err)
	}
}

func TestConcurrentReleaseCreateAndApplicationDeleteCannotLeaveOrphan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application-release-race.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first): %v", err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(second): %v", err)
	}
	defer second.Close()
	now := time.Now().UTC()
	if err := first.SaveProject(&Project{ID: "race-project", Name: "Race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := first.SaveApplication(&Application{ID: "race-application", ProjectID: "race-project", Name: "Race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		<-start
		errs <- first.SaveRelease(&Release{ID: "race-release", ApplicationID: "race-application", ProjectID: "race-project", Name: "Race", CreatedAt: now, UpdatedAt: now})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- second.DeleteApplication("race-application")
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		message := strings.ToLower(fmt.Sprint(err))
		if err != nil && !strings.Contains(message, "not found") && !strings.Contains(message, "no rows") && !strings.Contains(message, "locked") && !strings.Contains(message, "busy") {
			t.Fatalf("concurrent hierarchy mutation error: %v", err)
		}
	}

	release, releaseErr := first.GetRelease("race-release")
	application, applicationErr := first.GetApplication("race-application")
	if releaseErr == nil && release != nil && (applicationErr != nil || application == nil) {
		t.Fatalf("orphan release survived concurrent create/delete: release=%#v application=%#v applicationErr=%v", release, application, applicationErr)
	}
	if releaseErr != nil && !strings.Contains(strings.ToLower(releaseErr.Error()), "no rows") && releaseErr != sql.ErrNoRows {
		t.Fatalf("GetRelease: %v", releaseErr)
	}
}
