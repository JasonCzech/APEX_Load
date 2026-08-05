package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSchedulerContentVersionChangesWithSchedulerInputs(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	versions := make(map[string]struct{})
	assertNewVersion := func(label string) {
		t.Helper()
		version, err := store.SchedulerContentVersion()
		if err != nil {
			t.Fatalf("%s version: %v", label, err)
		}
		if _, duplicate := versions[version]; duplicate {
			t.Fatalf("%s version did not change: %q", label, version)
		}
		versions[version] = struct{}{}
	}
	assertNewVersion("empty")
	if err := store.SaveProject(&Project{ID: "project", Name: "Project", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	script := &dsl.Script{ID: "script", Name: "Script", Protocol: "http", ProjectID: "project"}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	assertNewVersion("script")
	scenario := &Scenario{ID: "scenario", Name: "Scenario", Duration: "1m", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.SaveScenario(scenario, []ScenarioScript{{ScriptID: script.ID, VUsers: 1}}); err != nil {
		t.Fatal(err)
	}
	assertNewVersion("scenario")
	if _, err := store.db.Exec(`UPDATE scenario_scripts SET vusers = vusers + 1 WHERE scenario_id = ?`, scenario.ID); err != nil {
		t.Fatal(err)
	}
	assertNewVersion("scenario group")
	if err := store.SaveScenarioSchedule(&ScenarioSchedule{
		ID:         "schedule",
		ScenarioID: scenario.ID,
		ProjectID:  "project",
		Name:       "Schedule",
		CronExpr:   "0 * * * *",
		Timezone:   "UTC",
		Enabled:    true,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	assertNewVersion("schedule")
}

func TestPostgresSchedulerContentTriggersDeferRevisionUntilContentLocksComplete(t *testing.T) {
	for _, table := range schedulerContentRevisionTables() {
		trigger := "apexload_scheduler_content_" + table
		query := postgresSchedulerContentTriggerSQL(trigger, table)
		for _, required := range []string{
			"CREATE CONSTRAINT TRIGGER " + trigger,
			" AFTER INSERT OR UPDATE OR DELETE ON " + table,
			" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW ",
		} {
			if !strings.Contains(query, required) {
				t.Fatalf("PostgreSQL trigger for %s is missing %q: %s", table, required, query)
			}
		}
		if strings.Contains(query, " BEFORE ") || strings.Contains(query, "FOR EACH STATEMENT") {
			t.Fatalf("PostgreSQL trigger for %s retained eager revision locking: %s", table, query)
		}
	}
	function := postgresSchedulerContentRevisionFunctionSQL()
	for _, required := range []string{
		"pg_current_xact_id()::text",
		"current_setting('apexload.scheduler_content_revision_xid', true) IS DISTINCT FROM current_xid",
		"set_config('apexload.scheduler_content_revision_xid', current_xid, true)",
	} {
		if !strings.Contains(function, required) {
			t.Fatalf("PostgreSQL revision function is missing transaction coalescing %q: %s", required, function)
		}
	}
}

func TestPostgresSchedulerContentTriggerProbeRejectsMalformedSameNameTrigger(t *testing.T) {
	query := postgresSchedulerContentTriggerProbeSQL()
	for _, required := range []string{
		"tgrelid = ?::regclass",
		"tgname = ?",
		"NOT tgisinternal",
		"tgparentid = 0",
		"tgconstraint <> 0",
		"tgfoid = to_regprocedure('apexload_bump_scheduler_content_revision()')",
		"tgtype = ?",
		"tgenabled = 'O'",
		"tgdeferrable AND tginitdeferred",
		"tgnargs = 0",
		"tgattr = ''::int2vector",
		"tgqual IS NULL",
		"tgoldtable IS NULL",
		"tgnewtable IS NULL",
	} {
		if !strings.Contains(query, required) {
			t.Errorf("PostgreSQL trigger probe is missing %q: %s", required, query)
		}
	}
	rewritten := rewriteSQL(query, storeDialectPostgres)
	for _, required := range []string{"$1::regclass", "tgname = $2", "tgtype = $3"} {
		if !strings.Contains(rewritten, required) {
			t.Errorf("rewritten PostgreSQL trigger probe is missing %q: %s", required, rewritten)
		}
	}
	if strings.Contains(rewritten, "?") {
		t.Fatalf("rewritten PostgreSQL trigger probe retained SQLite placeholders: %s", rewritten)
	}
	if postgresSchedulerContentTriggerType != 29 {
		t.Fatalf("PostgreSQL ROW/INSERT/DELETE/UPDATE trigger mask = %d, want 29", postgresSchedulerContentTriggerType)
	}
}

func TestSchedulerContentVersionChangesWhenOlderRowIsEditedWithClockBehind(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.SaveScript(&dsl.Script{ID: "older", Name: "Older", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveScript(&dsl.Script{ID: "newest", Name: "Newest", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	before, err := store.SchedulerContentVersion()
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce a controller whose clock is behind the row holding MAX(updated_at).
	// The old COUNT+MAX token remained unchanged even though scheduler content did.
	if _, err := store.db.Exec(`UPDATE scripts SET name = ?, updated_at = ? WHERE id = ?`, "Edited", time.Unix(1, 0).UTC(), "older"); err != nil {
		t.Fatal(err)
	}
	after, err := store.SchedulerContentVersion()
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatalf("scheduler content revision stayed %q after an older row changed", before)
	}
}
