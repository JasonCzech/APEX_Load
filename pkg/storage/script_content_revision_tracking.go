package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var scriptAuthoredColumns = []string{"id", "ref_key", "name", "protocol", "version", "project_id", "folder_id", "generated_by", "generated_from", "metadata_json", "goal_config_json", "config", "data_sources", "parameters", "init", "actions", "teardown", "created_at"}

func scriptRevisionMintSQL(dialect storeDialect) string {
	if dialect == storeDialectPostgres {
		return `'scr-v1-' || replace(gen_random_uuid()::text, '-', '') || replace(gen_random_uuid()::text, '-', '')`
	}
	return `'scr-v1-' || lower(hex(randomblob(32)))`
}

func scriptAuthoredDifferenceSQL(dialect storeDialect) string {
	operator := " IS NOT "
	if dialect == storeDialectPostgres {
		operator = " IS DISTINCT FROM "
	}
	predicates := make([]string, 0, len(scriptAuthoredColumns))
	for _, column := range scriptAuthoredColumns {
		predicates = append(predicates, "OLD."+column+operator+"NEW."+column)
	}
	return strings.Join(predicates, " OR ")
}

type scriptRevisionTrigger struct{ name, table, sql string }

func sqliteScriptRevisionTriggers() []scriptRevisionTrigger {
	mint := scriptRevisionMintSQL(storeDialectSQLite)
	bump := func(id string) string {
		return `UPDATE scripts SET content_revision = ` + mint + ` WHERE id = ` + id + `;`
	}
	definitions := []scriptRevisionTrigger{
		{"apexload_script_content_insert", "scripts", `AFTER INSERT ON scripts BEGIN ` + bump("NEW.id") + ` END`},
		{"apexload_script_content_update", "scripts", `AFTER UPDATE OF ` + strings.Join(scriptAuthoredColumns, ", ") + ` ON scripts WHEN ` + scriptAuthoredDifferenceSQL(storeDialectSQLite) + ` BEGIN ` + bump("NEW.id") + ` END`},
		{"apexload_script_placement_insert", "script_library_assignments", `AFTER INSERT ON script_library_assignments WHEN NEW.application_id IS NOT NULL BEGIN ` + bump("NEW.item_id") + ` END`},
		{"apexload_script_placement_delete", "script_library_assignments", `AFTER DELETE ON script_library_assignments WHEN OLD.application_id IS NOT NULL BEGIN ` + bump("OLD.item_id") + ` END`},
		{"apexload_script_placement_update", "script_library_assignments", `AFTER UPDATE ON script_library_assignments WHEN OLD.application_id IS NOT NEW.application_id OR (OLD.item_id IS NOT NEW.item_id AND (OLD.application_id IS NOT NULL OR NEW.application_id IS NOT NULL OR EXISTS (SELECT 1 FROM script_library_assignments_releases WHERE item_id IN (OLD.item_id, NEW.item_id)))) BEGIN UPDATE scripts SET content_revision = ` + mint + ` WHERE id IN (OLD.item_id, NEW.item_id); END`},
		{"apexload_script_release_insert", "script_library_assignments_releases", `AFTER INSERT ON script_library_assignments_releases BEGIN ` + bump("NEW.item_id") + ` END`},
		{"apexload_script_release_delete", "script_library_assignments_releases", `AFTER DELETE ON script_library_assignments_releases BEGIN ` + bump("OLD.item_id") + ` END`},
		{"apexload_script_release_update", "script_library_assignments_releases", `AFTER UPDATE ON script_library_assignments_releases WHEN OLD.item_id IS NOT NEW.item_id OR OLD.release_id IS NOT NEW.release_id BEGIN UPDATE scripts SET content_revision = ` + mint + ` WHERE id IN (OLD.item_id, NEW.item_id); END`},
	}
	for i := range definitions {
		definitions[i].sql = `CREATE TRIGGER ` + definitions[i].name + ` ` + definitions[i].sql
	}
	return definitions
}

type scriptRevisionFunction struct{ name, body string }

func postgresScriptRevisionFunctions() []scriptRevisionFunction {
	mint := scriptRevisionMintSQL(storeDialectPostgres)
	return []scriptRevisionFunction{
		{"apexload_mint_script_content_revision", `BEGIN
 IF TG_OP = 'INSERT' THEN
  NEW.content_revision := ` + mint + `;
 ELSIF ` + scriptAuthoredDifferenceSQL(storeDialectPostgres) + ` THEN
  NEW.content_revision := ` + mint + `;
 END IF;
 RETURN NEW;
END;`},
		{"apexload_bump_script_placement_revision", `DECLARE
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
    EXISTS (SELECT 1 FROM script_library_assignments_releases WHERE item_id IN (old_id, new_id))));
 END IF;
 IF changed THEN
  PERFORM id FROM scripts WHERE id IN (old_id, new_id) ORDER BY id FOR UPDATE;
  UPDATE scripts SET content_revision = ` + mint + ` WHERE id IN (old_id, new_id);
 END IF;
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;`},
		{"apexload_bump_script_release_revision", `DECLARE
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
 PERFORM id FROM scripts WHERE id IN (old_id, new_id) ORDER BY id FOR UPDATE;
 UPDATE scripts SET content_revision = ` + mint + ` WHERE id IN (old_id, new_id);
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;`},
	}
}

// validatePersistedScriptRevisions never replaces an invalid nonempty nonce.
// Missing values may only be minted by the controlled initialization phase.
func validatePersistedScriptRevisions(tx *storeTx, allowMissing bool) error {
	rows, err := tx.Query(`SELECT COALESCE(content_revision, '') FROM scripts`)
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
		if !ValidScriptContentRevision(value) {
			return ErrScriptAuthoringUnavailable
		}
	}
	return rows.Err()
}

func (s *SQLiteStore) validateScriptRevisionInitialization() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validatePersistedScriptRevisions(tx, true); err != nil {
		return err
	}
	return tx.Commit()
}

// ensureScriptContentRevisionTracking installs immediate per-resource tracking
// in one transaction before release37 publication. It repairs the complete
// repository-owned trigger/function shape, not just a same-name existence test.
func (s *SQLiteStore) ensureScriptContentRevisionTracking() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if tx.dialect == storeDialectPostgres {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(7241801551050296663)`); err != nil {
			return err
		}
		functions := postgresScriptRevisionFunctions()
		for _, function := range functions {
			query := postgresScriptRevisionFunctionSQL(function)
			if _, err := tx.Exec(query); err != nil {
				return fmt.Errorf("install script content function: %w", err)
			}
			var installed bool
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure(?) AND prosrc=? AND prorettype='trigger'::regtype AND prolang=(SELECT oid FROM pg_language WHERE lanname='plpgsql') AND pronargs=0 AND prokind='f' AND NOT proretset AND NOT prosecdef AND NOT proisstrict AND provolatile='v' AND proparallel='u' AND proconfig IS NULL)`, function.name+"()", function.body).Scan(&installed); err != nil {
				return err
			}
			if !installed {
				return fmt.Errorf("script content function shape unavailable")
			}
		}
		triggers := postgresScriptRevisionTriggers()
		for _, trigger := range triggers {
			var installed bool
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=?::regclass AND tgname=? AND NOT tgisinternal AND tgparentid=0 AND tgconstraint=0 AND tgfoid=to_regprocedure(?) AND tgtype=? AND tgenabled='O' AND NOT tgdeferrable AND NOT tginitdeferred AND tgnargs=0 AND tgattr=''::int2vector AND tgqual IS NULL AND tgoldtable IS NULL AND tgnewtable IS NULL)`, trigger.table, trigger.name, trigger.function+"()", trigger.kind).Scan(&installed); err != nil {
				return err
			}
			if !installed {
				if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name + ` ON ` + trigger.table); err != nil {
					return err
				}
				if _, err := tx.Exec(postgresScriptRevisionTriggerSQL(trigger)); err != nil {
					return err
				}
			}
		}
	} else {
		if _, err := tx.Exec(`UPDATE scripts SET content_revision=content_revision WHERE 1=0`); err != nil {
			return err
		}
		for _, trigger := range sqliteScriptRevisionTriggers() {
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
				return fmt.Errorf("install script content trigger: %w", err)
			}
		}
	}
	if err := validatePersistedScriptRevisions(tx, true); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE scripts SET content_revision=` + scriptRevisionMintSQL(tx.dialect) + ` WHERE content_revision IS NULL OR content_revision=''`); err != nil {
		return err
	}
	if err := validatePersistedScriptRevisions(tx, false); err != nil {
		return err
	}
	return tx.Commit()
}

type postgresScriptRevisionTrigger struct {
	name, table, function, events string
	kind                          int
}

func postgresScriptRevisionTriggers() []postgresScriptRevisionTrigger {
	return []postgresScriptRevisionTrigger{
		{"apexload_script_content_parent", "scripts", "apexload_mint_script_content_revision", "BEFORE INSERT OR UPDATE", 1 | 2 | 4 | 16},
		{"apexload_script_content_placement", "script_library_assignments", "apexload_bump_script_placement_revision", "AFTER INSERT OR UPDATE OR DELETE", 1 | 4 | 8 | 16},
		{"apexload_script_content_release", "script_library_assignments_releases", "apexload_bump_script_release_revision", "AFTER INSERT OR UPDATE OR DELETE", 1 | 4 | 8 | 16},
	}
}

func postgresScriptRevisionFunctionSQL(function scriptRevisionFunction) string {
	return `CREATE OR REPLACE FUNCTION ` + function.name + `() RETURNS TRIGGER AS $script_revision$` + function.body + `$script_revision$ LANGUAGE plpgsql VOLATILE SECURITY INVOKER CALLED ON NULL INPUT PARALLEL UNSAFE`
}

func postgresScriptRevisionTriggerSQL(trigger postgresScriptRevisionTrigger) string {
	return `CREATE TRIGGER ` + trigger.name + ` ` + trigger.events + ` ON ` + trigger.table + ` FOR EACH ROW EXECUTE FUNCTION ` + trigger.function + `() `
}
