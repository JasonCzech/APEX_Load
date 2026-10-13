package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSharedAssetUsageListsEachProjectsBindingsAndReferences(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	for _, projectID := range []string{"usage-a", "usage-b"} {
		if err := store.SaveProject(&Project{ID: projectID, Name: projectID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", projectID, err)
		}
	}
	for _, repo := range []*ObjectRepository{
		{ID: "object-repo-1", ProjectID: "usage-a", Name: "Pages", Objects: []RepositoryObject{{LogicalName: "Login"}, {LogicalName: "Logout"}}},
		{ID: "object-repo-2", ProjectID: "usage-a", Name: "Forms", Objects: []RepositoryObject{{LogicalName: "Submit"}}},
		{ID: "object-repo-3", ProjectID: "usage-b", Name: "Other pages", Objects: []RepositoryObject{{LogicalName: "Login"}}},
	} {
		if err := store.SaveObjectRepository(repo); err != nil {
			t.Fatalf("SaveObjectRepository(%s): %v", repo.ID, err)
		}
	}
	nested := dsl.Action{Name: "Branch", Type: "if", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Name: "Nested", Type: "browser_click", ObjectRef: "object-repo-2:Submit"}}}}
	for _, script := range []*dsl.Script{
		{ID: "usage-script-a", Name: "Checkout", Protocol: "browser", ProjectID: "usage-a", CreatedAt: now, Actions: []dsl.Action{
			{Name: "Login", Type: "browser_click", ObjectRef: "object-repo-1:Login"},
			{Name: "Again", Type: "browser_click", ObjectRef: "object-repo-1:Logout"},
			nested,
			{Name: "Plain", Type: "browser_click", ObjectRef: "Login"},
		}},
		{ID: "usage-script-a2", Name: "Browse", Protocol: "http", ProjectID: "usage-a", CreatedAt: now},
		{ID: "usage-script-b", Name: "Other", Protocol: "browser", ProjectID: "usage-b", CreatedAt: now, Actions: []dsl.Action{{Name: "Login", Type: "browser_click", ObjectRef: "object-repo-3:Login"}}},
	} {
		if err := store.SaveScript(script); err != nil {
			t.Fatalf("SaveScript(%s): %v", script.ID, err)
		}
	}
	for _, pool := range []*SharedDataPool{
		{ID: "usage-pool-a", Name: "Customers", ProjectID: "usage-a", CreatedAt: now},
		{ID: "usage-pool-b", Name: "Elsewhere", ProjectID: "usage-b", CreatedAt: now},
	} {
		if err := store.SaveDataPool(pool); err != nil {
			t.Fatalf("SaveDataPool(%s): %v", pool.ID, err)
		}
	}
	for _, scenario := range []struct {
		id, name, script, pool, alias string
	}{
		{"usage-scenario-z", "Zeta run", "usage-script-a", "usage-pool-a", "users"},
		{"usage-scenario-a", "Alpha run", "usage-script-a2", "usage-pool-a", "customers"},
		{"usage-scenario-b", "Other run", "usage-script-b", "usage-pool-b", "users"},
	} {
		if err := store.SaveScenarioWithDataPoolBindings(&Scenario{ID: scenario.id, Name: scenario.name, Duration: "1m", CreatedAt: now},
			[]ScenarioScript{{ScenarioID: scenario.id, ScriptID: scenario.script}},
			[]ScenarioDataPoolBinding{{ID: scenario.id + "-binding", PoolID: scenario.pool, Alias: scenario.alias, AccessMode: "read_only", SelectionStrategy: "sequential"}}); err != nil {
			t.Fatalf("SaveScenarioWithDataPoolBindings(%s): %v", scenario.id, err)
		}
	}
	zeta, _, err := store.GetScenario("usage-scenario-z")
	if err != nil {
		t.Fatalf("GetScenario: %v", err)
	}
	if ok, err := store.SetObjectArchived("scenario", zeta.ID, zeta.UpdatedAt, true); err != nil || !ok {
		t.Fatalf("SetObjectArchived(scenario) = %v, %v", ok, err)
	}
	browse, err := store.GetScript("usage-script-a2")
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	if ok, err := store.SetObjectArchived("script", browse.ID, browse.UpdatedAt, true); err != nil || !ok {
		t.Fatalf("SetObjectArchived(script) = %v, %v", ok, err)
	}

	bindings, err := store.ListProjectSharedAssetBindings("usage-a")
	if err != nil {
		t.Fatalf("ListProjectSharedAssetBindings: %v", err)
	}
	// Ordered by scenario name; another project's bindings never appear; an
	// archived scenario still binds the pool (and still blocks its deletion).
	wantBindings := []SharedAssetScenarioBinding{
		{ScenarioID: "usage-scenario-a", ScenarioName: "Alpha run", PoolID: "usage-pool-a", Alias: "customers"},
		{ScenarioID: "usage-scenario-z", ScenarioName: "Zeta run", Archived: true, PoolID: "usage-pool-a", Alias: "users"},
	}
	if !reflect.DeepEqual(bindings, wantBindings) {
		t.Fatalf("bindings = %#v, want %#v", bindings, wantBindings)
	}

	scripts, err := store.ListProjectSharedAssetScripts("usage-a", true)
	if err != nil {
		t.Fatalf("ListProjectSharedAssetScripts: %v", err)
	}
	// Each repository once per script, nested branches included, and a plain
	// (unqualified) object name is not a repository reference.
	wantScripts := []SharedAssetScript{
		{ID: "usage-script-a2", Name: "Browse", Archived: true, ObjectRepositoryIDs: []string{}},
		{ID: "usage-script-a", Name: "Checkout", ObjectRepositoryIDs: []string{"object-repo-1", "object-repo-2"}},
	}
	if !reflect.DeepEqual(scripts, wantScripts) {
		t.Fatalf("scripts = %#v, want %#v", scripts, wantScripts)
	}
	// Names alone skip decoding the actions.
	names, err := store.ListProjectSharedAssetScripts("usage-a", false)
	if err != nil {
		t.Fatalf("ListProjectSharedAssetScripts(names): %v", err)
	}
	for index := range wantScripts {
		wantScripts[index].ObjectRepositoryIDs = []string{}
	}
	if !reflect.DeepEqual(names, wantScripts) {
		t.Fatalf("names = %#v, want %#v", names, wantScripts)
	}
}

func TestSharedAssetUsageListsProjectLiveTableBindings(t *testing.T) {
	s := newHAFencingStore(t)
	input, term := projectTableFixture(t, s)
	table, err := s.CreateProjectLiveTable(context.Background(), input, term)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveScript(&dsl.Script{ID: "table-script", ProjectID: DefaultProjectID, Name: "Native", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	binding := ScenarioDataPoolBinding{ID: "table-ref", Scope: "project", ProjectTableID: table.ID, Alias: "orders", AccessMode: "live", SelectionStrategy: "manual", LiveOptions: &LiveTableOptions{EmptyPolicy: "error", OperationTimeoutMS: 120000}}
	if _, err := s.CreateScenarioAuthoringIfAbsent(&Scenario{ID: "table-scenario", Name: "Workers", Duration: "1m"}, []ScenarioScript{{ScriptID: "table-script", Name: "Workers", VUsers: 1}}, []ScenarioDataPoolBinding{binding}); err != nil {
		t.Fatal(err)
	}
	bindings, err := s.ListProjectSharedAssetBindings(DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	want := []SharedAssetScenarioBinding{{ScenarioID: "table-scenario", ScenarioName: "Workers", ProjectTableID: table.ID, Alias: "orders"}}
	if !reflect.DeepEqual(bindings, want) {
		t.Fatalf("bindings = %#v, want %#v", bindings, want)
	}
	// A project-table binding has no pool, and must not read as the project's
	// default-pool binding in another project.
	other, err := s.ListProjectSharedAssetBindings("some-other-project")
	if err != nil || len(other) != 0 {
		t.Fatalf("other project bindings = %#v, %v", other, err)
	}
}
