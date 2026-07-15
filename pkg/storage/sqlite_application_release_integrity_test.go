package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

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
