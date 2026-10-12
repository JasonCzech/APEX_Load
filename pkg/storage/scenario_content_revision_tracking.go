package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var scenarioAuthoredColumns = []string{"id", "ref_key", "name", "workload_shape_id", "duration", "start_delay", "scenario_mode", "termination_mode", "shakeout_enabled", "resource_baseline_enabled", "assignment_json", "goal_config_json", "runtime_settings_json", "traffic_profile_json", "created_at", "materialization_key"}
var scenarioGroupAuthoredColumns = []string{"scenario_id", "sort_order", "name", "script_id", "vusers", "ramp_up", "ramp_down", "start_offset", "stages_json", "iterations", "initialization", "role", "runtime_settings_json", "assignment_json", "transaction_response_time_sla_ms"}
var scenarioPoolAuthoredColumns = []string{"id", "scenario_id", "pool_id", "alias", "access_mode", "selection_strategy", "live_options_json"}
var scenarioMonitorAuthoredColumns = []string{"scenario_id", "project_id", "revision", "payload"}

func scenarioRevisionMintSQL(dialect storeDialect) string {
	if dialect == storeDialectPostgres {
		return `'scn-v1-' || replace(gen_random_uuid()::text, '-', '') || replace(gen_random_uuid()::text, '-', '')`
	}
	return `'scn-v1-' || lower(hex(randomblob(32)))`
}

func scenarioAuthoredDifferenceSQL(dialect storeDialect) string {
	return scenarioColumnsDifferenceSQL(dialect, scenarioAuthoredColumns)
}

func scenarioColumnsDifferenceSQL(dialect storeDialect, columns []string) string {
	operator := " IS NOT "
	if dialect == storeDialectPostgres {
		operator = " IS DISTINCT FROM "
	}
	predicates := make([]string, 0, len(columns))
	for _, column := range columns {
		predicates = append(predicates, "OLD."+column+operator+"NEW."+column)
	}
	return strings.Join(predicates, " OR ")
}

type scenarioRevisionTrigger struct{ name, table, sql string }

func sqliteScenarioRevisionTriggers() []scenarioRevisionTrigger {
	mint := scenarioRevisionMintSQL(storeDialectSQLite)
	bump := func(id string) string {
		return `UPDATE scenarios SET content_revision = ` + mint + ` WHERE id = ` + id + `;`
	}
	definitions := []scenarioRevisionTrigger{
		{"apexload_scenario_content_insert", "scenarios", `AFTER INSERT ON scenarios BEGIN ` + bump("NEW.id") + ` END`},
		{"apexload_scenario_content_update", "scenarios", `AFTER UPDATE OF ` + strings.Join(scenarioAuthoredColumns, ", ") + ` ON scenarios WHEN ` + scenarioAuthoredDifferenceSQL(storeDialectSQLite) + ` BEGIN ` + bump("NEW.id") + ` END`},
		{"apexload_scenario_placement_insert", "scenario_library_assignments", `AFTER INSERT ON scenario_library_assignments WHEN NEW.application_id IS NOT NULL BEGIN ` + bump("NEW.item_id") + ` END`},
		{"apexload_scenario_placement_delete", "scenario_library_assignments", `AFTER DELETE ON scenario_library_assignments WHEN OLD.application_id IS NOT NULL BEGIN ` + bump("OLD.item_id") + ` END`},
		{"apexload_scenario_placement_update", "scenario_library_assignments", `AFTER UPDATE ON scenario_library_assignments WHEN OLD.application_id IS NOT NEW.application_id OR (OLD.item_id IS NOT NEW.item_id AND (OLD.application_id IS NOT NULL OR NEW.application_id IS NOT NULL OR EXISTS (SELECT 1 FROM scenario_library_assignments_releases WHERE item_id IN (OLD.item_id, NEW.item_id)))) BEGIN UPDATE scenarios SET content_revision = ` + mint + ` WHERE id IN (OLD.item_id, NEW.item_id); END`},
		{"apexload_scenario_release_insert", "scenario_library_assignments_releases", `AFTER INSERT ON scenario_library_assignments_releases BEGIN ` + bump("NEW.item_id") + ` END`},
		{"apexload_scenario_release_delete", "scenario_library_assignments_releases", `AFTER DELETE ON scenario_library_assignments_releases BEGIN ` + bump("OLD.item_id") + ` END`},
		{"apexload_scenario_release_update", "scenario_library_assignments_releases", `AFTER UPDATE ON scenario_library_assignments_releases WHEN OLD.item_id IS NOT NEW.item_id OR OLD.release_id IS NOT NEW.release_id BEGIN UPDATE scenarios SET content_revision = ` + mint + ` WHERE id IN (OLD.item_id, NEW.item_id); END`},
	}
	for _, child := range []struct {
		name, table string
		columns     []string
	}{
		{"group", "scenario_scripts", scenarioGroupAuthoredColumns},
		{"pool", "scenario_data_pool_bindings", scenarioPoolAuthoredColumns},
		{"monitor", "monitor_bindings", scenarioMonitorAuthoredColumns},
	} {
		definitions = append(definitions,
			scenarioRevisionTrigger{"apexload_scenario_" + child.name + "_insert", child.table, "AFTER INSERT ON " + child.table + " BEGIN " + bump("NEW.scenario_id") + " END"},
			scenarioRevisionTrigger{"apexload_scenario_" + child.name + "_delete", child.table, "AFTER DELETE ON " + child.table + " BEGIN " + bump("OLD.scenario_id") + " END"},
			scenarioRevisionTrigger{"apexload_scenario_" + child.name + "_update", child.table, "AFTER UPDATE OF " + strings.Join(child.columns, ", ") + " ON " + child.table + " WHEN " + scenarioColumnsDifferenceSQL(storeDialectSQLite, child.columns) + " BEGIN UPDATE scenarios SET content_revision = " + mint + " WHERE id IN (OLD.scenario_id, NEW.scenario_id); END"},
		)
	}

	definitions = append(definitions, scenarioRevisionTrigger{"apexload_scenario_script_owner_update", "scripts", `AFTER UPDATE OF project_id ON scripts WHEN OLD.project_id IS NOT NEW.project_id BEGIN UPDATE scenarios SET content_revision = ` + mint + ` WHERE id IN (SELECT scenario_id FROM scenario_scripts WHERE script_id=NEW.id); END`})
	for i := range definitions {
		definitions[i].sql = `CREATE TRIGGER ` + definitions[i].name + ` ` + definitions[i].sql
	}
	return definitions
}

type scenarioRevisionFunction struct{ name, body string }

func postgresScenarioRevisionFunctions() []scenarioRevisionFunction {
	mint := scenarioRevisionMintSQL(storeDialectPostgres)
	functions := []scenarioRevisionFunction{
		{"apexload_mint_scenario_content_revision", `BEGIN
 IF TG_OP = 'INSERT' THEN
  NEW.content_revision := ` + mint + `;
 ELSIF ` + scenarioAuthoredDifferenceSQL(storeDialectPostgres) + ` THEN
  NEW.content_revision := ` + mint + `;
 END IF;
 RETURN NEW;
END;`},
		{"apexload_bump_scenario_placement_revision", `DECLARE
 old_id TEXT;
 new_id TEXT;
 changed BOOLEAN := FALSE;
BEGIN
 IF TG_OP = 'INSERT' THEN
  new_id := NEW.item_id;
  changed := NEW.application_id IS NOT NULL;
 ELSIF TG_OP = 'DELETE' THEN
  old_id := OLD.item_id;
  changed := OLD.application_id IS NOT NULL;
 ELSE
  old_id := OLD.item_id;
  new_id := NEW.item_id;
  changed := OLD.application_id IS DISTINCT FROM NEW.application_id OR
   (OLD.item_id IS DISTINCT FROM NEW.item_id AND (OLD.application_id IS NOT NULL OR NEW.application_id IS NOT NULL OR
    EXISTS (SELECT 1 FROM scenario_library_assignments_releases WHERE item_id IN (old_id, new_id))));
 END IF;
 IF changed THEN
  PERFORM id FROM scenarios WHERE id IN (old_id, new_id) ORDER BY id FOR UPDATE;
  UPDATE scenarios SET content_revision = ` + mint + ` WHERE id IN (old_id, new_id);
 END IF;
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;`},
		{"apexload_bump_scenario_release_revision", `DECLARE
 old_id TEXT;
 new_id TEXT;
BEGIN
 IF TG_OP = 'INSERT' THEN
  new_id := NEW.item_id;
 ELSIF TG_OP = 'DELETE' THEN
  old_id := OLD.item_id;
 ELSE
  IF OLD.item_id IS NOT DISTINCT FROM NEW.item_id AND OLD.release_id IS NOT DISTINCT FROM NEW.release_id THEN RETURN NEW; END IF;
  old_id := OLD.item_id;
  new_id := NEW.item_id;
 END IF;
 PERFORM id FROM scenarios WHERE id IN (old_id, new_id) ORDER BY id FOR UPDATE;
 UPDATE scenarios SET content_revision = ` + mint + ` WHERE id IN (old_id, new_id);
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;`},
	}
	for _, child := range []struct {
		name    string
		columns []string
	}{
		{"group", scenarioGroupAuthoredColumns}, {"pool", scenarioPoolAuthoredColumns}, {"monitor", scenarioMonitorAuthoredColumns},
	} {
		functions = append(functions, scenarioRevisionFunction{"apexload_bump_scenario_" + child.name + "_revision", `DECLARE
 old_id TEXT;
 new_id TEXT;
BEGIN
 IF TG_OP = 'INSERT' THEN new_id := NEW.scenario_id;
 ELSIF TG_OP = 'DELETE' THEN old_id := OLD.scenario_id;
 ELSE
  IF NOT (` + scenarioColumnsDifferenceSQL(storeDialectPostgres, child.columns) + `) THEN RETURN NEW; END IF;
  old_id := OLD.scenario_id; new_id := NEW.scenario_id;
 END IF;
 PERFORM id FROM scenarios WHERE id IN (old_id, new_id) ORDER BY id FOR UPDATE;
 UPDATE scenarios SET content_revision = ` + mint + ` WHERE id IN (old_id, new_id);
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;`})
	}

	functions = append(functions, scenarioRevisionFunction{"apexload_bump_scenario_script_owner_revision", `BEGIN
 IF OLD.project_id IS NOT DISTINCT FROM NEW.project_id THEN RETURN NEW; END IF;
 PERFORM id FROM scenarios WHERE id IN (SELECT scenario_id FROM scenario_scripts WHERE script_id=NEW.id) ORDER BY id FOR UPDATE;
 UPDATE scenarios SET content_revision = ` + mint + ` WHERE id IN (SELECT scenario_id FROM scenario_scripts WHERE script_id=NEW.id);
 RETURN NEW;
END;`})
	return functions

}

// validatePersistedScenarioRevisions never replaces an invalid nonempty nonce.
// Missing values may only be minted by the controlled initialization phase.
func validatePersistedScenarioRevisions(tx *storeTx, allowMissing bool) error {
	rows, err := tx.Query(`SELECT COALESCE(content_revision, '') FROM scenarios`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return err
		}
		if allowMissing && value == "" {
			continue
		}
		if !ValidScenarioContentRevision(value) {
			return ErrScenarioAuthoringUnavailable
		}
	}
	return rows.Err()
}

func (s *SQLiteStore) validateScenarioRevisionInitialization() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validatePersistedScenarioRevisions(tx, true); err != nil {
		return err
	}
	return tx.Commit()
}

// ensureScenarioContentRevisionTracking installs immediate per-resource tracking
// in one transaction before release38 publication. It repairs the complete
// repository-owned trigger/function shape, not just a same-name existence test.
func (s *SQLiteStore) ensureScenarioContentRevisionTracking() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if tx.dialect == storeDialectPostgres {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(7241801551050296664)`); err != nil {
			return err
		}
		functions := postgresScenarioRevisionFunctions()
		for _, function := range functions {
			query := postgresScenarioRevisionFunctionSQL(function)
			if _, err := tx.Exec(query); err != nil {
				return fmt.Errorf("install scenario content function: %w", err)
			}
			var installed bool
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure(?) AND prosrc=? AND prorettype='trigger'::regtype AND prolang=(SELECT oid FROM pg_language WHERE lanname='plpgsql') AND pronargs=0 AND prokind='f' AND NOT proretset AND NOT prosecdef AND NOT proisstrict AND provolatile='v' AND proparallel='u' AND proconfig IS NULL)`, function.name+"()", function.body).Scan(&installed); err != nil {
				return err
			}
			if !installed {
				return fmt.Errorf("scenario content function shape unavailable")
			}
		}
		triggers := postgresScenarioRevisionTriggers()
		for _, trigger := range triggers {
			var installed bool
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=?::regclass AND tgname=? AND NOT tgisinternal AND tgparentid=0 AND tgconstraint=0 AND tgfoid=to_regprocedure(?) AND tgtype=? AND tgenabled='O' AND NOT tgdeferrable AND NOT tginitdeferred AND tgnargs=0 AND tgattr=''::int2vector AND tgqual IS NULL AND tgoldtable IS NULL AND tgnewtable IS NULL)`, trigger.table, trigger.name, trigger.function+"()", trigger.kind).Scan(&installed); err != nil {
				return err
			}
			if !installed {
				if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name + ` ON ` + trigger.table); err != nil {
					return err
				}
				if _, err := tx.Exec(postgresScenarioRevisionTriggerSQL(trigger)); err != nil {
					return err
				}
			}
		}
	} else {
		if _, err := tx.Exec(`UPDATE scenarios SET content_revision=content_revision WHERE 1=0`); err != nil {
			return err
		}
		for _, trigger := range sqliteScenarioRevisionTriggers() {
			var query string
			err := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, trigger.name).Scan(&query)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if query == trigger.sql {
				continue
			}
			if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
				return err
			}
			if _, err := tx.Exec(trigger.sql); err != nil {
				return fmt.Errorf("install scenario content trigger: %w", err)
			}
		}
	}
	if err := validatePersistedScenarioRevisions(tx, true); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE scenarios SET content_revision=` + scenarioRevisionMintSQL(tx.dialect) + ` WHERE content_revision IS NULL OR content_revision=''`); err != nil {
		return err
	}
	if err := validatePersistedScenarioRevisions(tx, false); err != nil {
		return err
	}
	return tx.Commit()
}

type postgresScenarioRevisionTrigger struct {
	name, table, function, events string
	kind                          int
}

func postgresScenarioRevisionTriggers() []postgresScenarioRevisionTrigger {
	return []postgresScenarioRevisionTrigger{
		{"apexload_scenario_content_parent", "scenarios", "apexload_mint_scenario_content_revision", "BEFORE INSERT OR UPDATE", 1 | 2 | 4 | 16},
		{"apexload_scenario_content_placement", "scenario_library_assignments", "apexload_bump_scenario_placement_revision", "AFTER INSERT OR UPDATE OR DELETE", 1 | 4 | 8 | 16},
		{"apexload_scenario_content_release", "scenario_library_assignments_releases", "apexload_bump_scenario_release_revision", "AFTER INSERT OR UPDATE OR DELETE", 1 | 4 | 8 | 16},
		{"apexload_scenario_content_group", "scenario_scripts", "apexload_bump_scenario_group_revision", "AFTER INSERT OR UPDATE OR DELETE", 1 | 4 | 8 | 16},
		{"apexload_scenario_content_pool", "scenario_data_pool_bindings", "apexload_bump_scenario_pool_revision", "AFTER INSERT OR UPDATE OR DELETE", 1 | 4 | 8 | 16},
		{"apexload_scenario_content_monitor", "monitor_bindings", "apexload_bump_scenario_monitor_revision", "AFTER INSERT OR UPDATE OR DELETE", 1 | 4 | 8 | 16},
		{"apexload_scenario_script_owner", "scripts", "apexload_bump_scenario_script_owner_revision", "AFTER UPDATE", 1 | 16},
	}
}

func postgresScenarioRevisionFunctionSQL(function scenarioRevisionFunction) string {
	return `CREATE OR REPLACE FUNCTION ` + function.name + `() RETURNS TRIGGER AS $scenario_revision$` + function.body + `$scenario_revision$ LANGUAGE plpgsql VOLATILE SECURITY INVOKER CALLED ON NULL INPUT PARALLEL UNSAFE`
}

func postgresScenarioRevisionTriggerSQL(trigger postgresScenarioRevisionTrigger) string {
	return `CREATE TRIGGER ` + trigger.name + ` ` + trigger.events + ` ON ` + trigger.table + ` FOR EACH ROW EXECUTE FUNCTION ` + trigger.function + `() `
}
