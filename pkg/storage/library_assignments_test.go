package storage

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func seedLibraryAssignments(t *testing.T, store *SQLiteStore) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"la-project", "la-foreign"} {
		must(store.SaveProject(&Project{ID: p, Name: p}))
	}
	for _, a := range []Application{{ID: "la-app", Name: "App", ProjectID: "la-project"}, {ID: "la-other", Name: "Other", ProjectID: "la-project"}, {ID: "la-foreign", Name: "Foreign", ProjectID: "la-foreign"}} {
		must(store.SaveApplication(&a))
	}
	for _, r := range []Release{{ID: "la-r1", GroupID: "la-family", Name: "Release One", ApplicationID: "la-app"}, {ID: "la-r2", Name: "Release Two", ApplicationID: "la-app"}, {ID: "la-other-r", Name: "Other", ApplicationID: "la-other"}} {
		must(store.SaveRelease(&r))
	}
	must(store.SaveScript(&dsl.Script{ID: "la-script", Name: "Script", Protocol: "http", ProjectID: "la-project"}))
	must(store.SaveScenario(&Scenario{ID: "la-scenario", Name: "Scenario", Duration: "1m"}, []ScenarioScript{{ScriptID: "la-script", Order: 1, VUsers: 1}}))
}
func checkLibraryAssignments(t *testing.T, store *SQLiteStore) {
	t.Helper()
	seedLibraryAssignments(t, store)
	app := "la-app"
	for _, kind := range []string{"script", "scenario"} {
		t.Run(kind, func(t *testing.T) {
			id := "la-" + kind
			empty, err := store.ListLibraryAssignments(kind, []string{id})
			if err != nil || empty[id].Revision != 0 || empty[id].Application != nil {
				t.Fatalf("empty: %#v %v", empty, err)
			}
			before, assigned, err := store.ReplaceLibraryAssignment(kind, id, "la-project", &app, []string{"la-family", "la-r2", "la-r2"}, 0)
			if err != nil || before.Revision != 0 || assigned.Revision != 1 || len(assigned.Releases) != 2 {
				t.Fatalf("assign: %#v %v", assigned, err)
			}
			for _, tc := range []struct {
				name, project, app string
				releases           []string
				revision           int64
				want               error
			}{
				{"stale", "la-project", app, nil, 0, ErrLibraryAssignmentConflict},
				{"foreign item", "la-foreign", "la-foreign", nil, 1, ErrLibraryAssignmentInvalid},
				{"foreign application", "la-project", "la-foreign", nil, 1, ErrLibraryAssignmentInvalid},
				{"wrong release", "la-project", app, []string{"la-other-r"}, 1, ErrLibraryAssignmentInvalid},
				{"missing release", "la-project", app, []string{"absent"}, 1, ErrLibraryAssignmentInvalid},
			} {
				t.Run(tc.name, func(t *testing.T) {
					_, _, err := store.ReplaceLibraryAssignment(kind, id, tc.project, &tc.app, tc.releases, tc.revision)
					if !errors.Is(err, tc.want) {
						t.Fatalf("got %v want %v", err, tc.want)
					}
				})
			}
			if err := store.DeleteApplication(app); err == nil {
				t.Fatal("deleted assigned application")
			}
			if err := store.DeleteRelease("la-r2"); err == nil {
				t.Fatal("deleted assigned release")
			}
			_, current, err := store.ReplaceLibraryAssignment(kind, id, "la-project", nil, nil, 1)
			if err != nil || current.Revision != 2 || current.Application != nil || len(current.Releases) != 0 {
				t.Fatalf("clear: %#v %v", current, err)
			}
			// Simultaneous editors cannot both commit the same revision.
			var wg sync.WaitGroup
			outcomes := make(chan error, 2)
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _, err := store.ReplaceLibraryAssignment(kind, id, "la-project", &app, nil, 2)
					outcomes <- err
				}()
			}
			wg.Wait()
			close(outcomes)
			successes, conflicts := 0, 0
			for err := range outcomes {
				if err == nil {
					successes++
				} else if errors.Is(err, ErrLibraryAssignmentConflict) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
			}
		})
	}
	// Assignment changes leave content edits independent.
	script, err := store.GetScript("la-script")
	if err != nil {
		t.Fatal(err)
	}
	script.Name = "Edited"
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	values, err := store.ListLibraryAssignments("script", []string{"la-script"})
	if err != nil || values["la-script"].Revision != 3 {
		t.Fatalf("content edit changed assignment: %#v %v", values, err)
	}
	// Retain archived associations, but do not permit a new assignment to one.
	if _, _, err := store.ReplaceLibraryAssignment("script", "la-script", "la-project", &app, []string{"la-r1"}, 3); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveRelease("la-r1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReplaceLibraryAssignment("script", "la-script", "la-project", &app, []string{"la-r1", "la-r2"}, 4); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReplaceLibraryAssignment("scenario", "la-scenario", "la-project", &app, []string{"la-r1"}, 3); !errors.Is(err, ErrLibraryAssignmentInvalid) {
		t.Fatalf("new archived assignment: %v", err)
	}
}
func TestLibraryAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assignments.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	checkLibraryAssignments(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assignments, err := reopened.ListLibraryAssignments("script", []string{"la-script"})
	if err != nil || assignments["la-script"].Revision != 5 || len(assignments["la-script"].Releases) != 2 {
		t.Fatalf("restart: %#v %v", assignments, err)
	}
	if err := reopened.DeleteProject("la-project"); err != nil {
		t.Fatalf("delete whole project: %v", err)
	}
}

func TestLibraryAssignmentsPreserveContentHistoryAndSchedules(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "preservation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedLibraryAssignments(t, store)
	if err := store.SaveScenarioSchedule(&ScenarioSchedule{ID: "existing-schedule", ScenarioID: "la-scenario", ProjectID: "la-project", ReleaseID: "la-r1", Name: "Scheduled", CronExpr: "0 * * * *", Timezone: "UTC", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&TestRecord{ID: "existing-run", Name: "History", Status: "COMPLETED", ProjectID: "la-project", ApplicationID: "la-app", ReleaseID: "la-r1", ScenarioID: "la-scenario"}); err != nil {
		t.Fatal(err)
	}
	beforeScript, err := store.GetScript("la-script")
	if err != nil {
		t.Fatal(err)
	}
	beforeScenario, beforeGroups, err := store.GetScenario("la-scenario")
	if err != nil {
		t.Fatal(err)
	}
	beforeSchedule, err := store.GetScenarioSchedule("existing-schedule")
	if err != nil {
		t.Fatal(err)
	}
	beforeRun, err := store.GetTest("existing-run")
	if err != nil {
		t.Fatal(err)
	}
	app := "la-other"
	for _, kind := range []string{"script", "scenario"} {
		if _, _, err := store.ReplaceLibraryAssignment(kind, "la-"+kind, "la-project", &app, []string{"la-other-r"}, 0); err != nil {
			t.Fatal(err)
		}
	}
	afterScript, _ := store.GetScript("la-script")
	afterScenario, afterGroups, _ := store.GetScenario("la-scenario")
	afterSchedule, _ := store.GetScenarioSchedule("existing-schedule")
	afterRun, _ := store.GetTest("existing-run")
	for _, pair := range []struct {
		name          string
		before, after any
	}{{"script", beforeScript, afterScript}, {"scenario", beforeScenario, afterScenario}, {"groups", beforeGroups, afterGroups}, {"schedule", beforeSchedule, afterSchedule}, {"history", beforeRun, afterRun}} {
		if !reflect.DeepEqual(pair.before, pair.after) {
			t.Fatalf("assignment changed %s", pair.name)
		}
	}
	beforeScenario.Name = "Ordinary edit"
	if err := store.SaveScenario(beforeScenario, beforeGroups); err != nil {
		t.Fatal(err)
	}
	assignment, err := store.ListLibraryAssignments("scenario", []string{"la-scenario"})
	if err != nil || assignment["la-scenario"].Application.ID != app {
		t.Fatalf("edit lost assignment: %v %v", assignment, err)
	}
	// A clone has a new identity and starts unassigned.
	beforeScript.ID = "cloned-script"
	if err := store.SaveScript(beforeScript); err != nil {
		t.Fatal(err)
	}
	assignment, err = store.ListLibraryAssignments("script", []string{"cloned-script"})
	if err != nil || assignment["cloned-script"].Application != nil {
		t.Fatalf("clone inherited assignment: %v %v", assignment, err)
	}
}

func TestLibraryAssignmentMigrationFromVersionNine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	seedLibraryAssignments(t, store)
	// Reconstruct the version-nine schema with pre-existing library content.
	for _, table := range []string{"script_library_assignments_releases", "scenario_library_assignments_releases", "script_library_assignments", "scenario_library_assignments"} {
		if _, err := store.db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec("DELETE FROM schema_migrations WHERE version >= 10"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	status, err := store.SchemaStatus()
	if err != nil || status.AppliedVersion != 12 {
		t.Fatalf("upgrade: %+v %v", status, err)
	}
	for _, kind := range []string{"script", "scenario"} {
		assignments, err := store.ListLibraryAssignments(kind, []string{"la-" + kind})
		if err != nil || assignments["la-"+kind].Application != nil || assignments["la-"+kind].Revision != 0 {
			t.Fatalf("legacy item assignment: %+v %v", assignments, err)
		}
	}
}
