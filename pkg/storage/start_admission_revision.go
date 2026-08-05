package storage

import (
	"fmt"
)

// StartAdmissionRevision is the database-owned monotonic revision for durable
// inputs that authorize a run's SHAKEOUT/INITIALIZING transition. Callers pair
// this value with a coherent evidence snapshot and the start transaction checks
// it again before committing.
func (s *SQLiteStore) StartAdmissionRevision() (int64, error) {
	var revision int64
	if err := s.db.QueryRow(`SELECT revision FROM start_admission_revision WHERE id = 1`).Scan(&revision); err != nil {
		return 0, err
	}
	return revision, nil
}

func (s *SQLiteStore) ensureStartAdmissionRevisionTracking() error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO start_admission_revision (id, revision) VALUES (1, 1) ON CONFLICT(id) DO NOTHING`); err != nil {
		return fmt.Errorf("initialize start admission revision: %w", err)
	}
	if s.db.dialect == storeDialectPostgres {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin start admission trigger migration: %w", err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(7241801551050296662)`); err != nil {
			return fmt.Errorf("lock start admission trigger migration: %w", err)
		}
		if _, err := tx.Exec(postgresStartAdmissionRevisionFunctionSQL()); err != nil {
			return fmt.Errorf("create start admission revision function: %w", err)
		}
		for _, table := range startAdmissionRevisionTables() {
			trigger := "apexload_start_admission_" + table
			installed, err := postgresStartAdmissionTriggerInstalled(tx, trigger, table)
			if err != nil {
				return fmt.Errorf("inspect start admission trigger for %s: %w", table, err)
			}
			if installed {
				continue
			}
			if _, err := tx.Exec("DROP TRIGGER IF EXISTS " + trigger + " ON " + table); err != nil {
				return fmt.Errorf("replace start admission trigger for %s: %w", table, err)
			}
			if _, err := tx.Exec(postgresStartAdmissionTriggerSQL(trigger, table)); err != nil {
				return fmt.Errorf("create start admission trigger for %s: %w", table, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit start admission trigger migration: %w", err)
		}
		return nil
	}

	for _, table := range startAdmissionRevisionTables() {
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			trigger := "apexload_start_admission_" + table + "_" + operation
			when := ""
			if table == "app_settings" {
				switch operation {
				case "INSERT":
					when = " WHEN NEW.key = 'system_settings'"
				case "UPDATE":
					when = " WHEN OLD.key = 'system_settings' OR NEW.key = 'system_settings'"
				case "DELETE":
					when = " WHEN OLD.key = 'system_settings'"
				}
			}
			query := "CREATE TRIGGER IF NOT EXISTS " + trigger + " AFTER " + operation + " ON " + table + when +
				" BEGIN UPDATE start_admission_revision SET revision = revision + 1 WHERE id = 1; END"
			if _, err := s.db.Exec(query); err != nil {
				return fmt.Errorf("create start admission trigger for %s %s: %w", table, operation, err)
			}
		}
	}
	return nil
}

func startAdmissionRevisionTables() []string {
	return []string{
		"scripts",
		"scenarios",
		"scenario_scripts",
		"scenario_schedules",
		"scenario_data_pool_bindings",
		"capacity_reservations",
		"scenario_schedule_occurrences",
		"app_settings",
	}
}

func postgresStartAdmissionTriggerInstalled(tx *storeTx, trigger, table string) (bool, error) {
	var installed bool
	err := tx.QueryRow(postgresStartAdmissionTriggerProbeSQL(), table, trigger, postgresSchedulerContentTriggerType).Scan(&installed)
	return installed, err
}

func postgresStartAdmissionTriggerProbeSQL() string {
	return `SELECT EXISTS (
		SELECT 1 FROM pg_trigger
		WHERE tgrelid = ?::regclass AND tgname = ? AND NOT tgisinternal
			AND tgparentid = 0 AND tgconstraint <> 0
			AND tgfoid = to_regprocedure('apexload_bump_start_admission_revision()')
			AND tgtype = ? AND tgenabled = 'O'
			AND tgdeferrable AND tginitdeferred
			AND tgnargs = 0 AND tgattr = ''::int2vector
			AND tgqual IS NULL AND tgoldtable IS NULL AND tgnewtable IS NULL
	)`
}

func postgresStartAdmissionRevisionFunctionSQL() string {
	// Deferred triggers preserve the content-row -> revision-row lock order. The
	// transaction marker coalesces multi-row and multi-table changes into one
	// revision increment. app_settings is filtered to the sole admission-bearing
	// key so unrelated counters do not create false start retries.
	return `CREATE OR REPLACE FUNCTION apexload_bump_start_admission_revision()
		RETURNS TRIGGER AS $body$
		DECLARE
			current_xid TEXT := pg_current_xact_id()::text;
			setting_key TEXT;
		BEGIN
			IF TG_TABLE_NAME = 'app_settings' THEN
				IF TG_OP = 'DELETE' THEN
					setting_key := OLD.key;
				ELSIF TG_OP = 'INSERT' THEN
					setting_key := NEW.key;
				ELSIF OLD.key = 'system_settings' OR NEW.key = 'system_settings' THEN
					setting_key := 'system_settings';
				ELSE
					setting_key := NEW.key;
				END IF;
				IF setting_key IS DISTINCT FROM 'system_settings' THEN
					IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
					RETURN NEW;
				END IF;
			END IF;
			IF current_setting('apexload.start_admission_revision_xid', true) IS DISTINCT FROM current_xid THEN
				UPDATE start_admission_revision SET revision = revision + 1 WHERE id = 1;
				PERFORM set_config('apexload.start_admission_revision_xid', current_xid, true);
			END IF;
			IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
			RETURN NEW;
		END;
		$body$ LANGUAGE plpgsql`
}

func postgresStartAdmissionTriggerSQL(trigger, table string) string {
	return "CREATE CONSTRAINT TRIGGER " + trigger + " AFTER INSERT OR UPDATE OR DELETE ON " + table + " DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION apexload_bump_start_admission_revision()"
}

func validateExpectedStartAdmissionRevision(tx *storeTx, dialect storeDialect, expected int64) error {
	if expected <= 0 {
		return nil
	}
	query := `SELECT revision FROM start_admission_revision WHERE id = 1`
	if dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var current int64
	if err := tx.QueryRow(query).Scan(&current); err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("%w: expected %d, found %d", ErrTestStartAdmissionRevisionChanged, expected, current)
	}
	return nil
}

func postgresStartAdmissionRevisionLockOrderSQL() []string {
	return []string{
		`SELECT revision FROM scheduler_content_revision WHERE id = 1 FOR UPDATE`,
		`SELECT revision FROM start_admission_revision WHERE id = 1 FOR UPDATE`,
	}
}

func validateExpectedPostgresStartAdmissionRevision(tx *storeTx, expected int64) error {
	if tx == nil {
		return nil
	}
	// scenario_schedules has both scheduler-content and start-admission deferred
	// triggers. PostgreSQL fires same-event triggers by name, so writers acquire
	// scheduler_content_revision before start_admission_revision at commit. A
	// start transaction that updates an occurrence's parent schedule must take
	// the two singleton rows in that identical order.
	queries := postgresStartAdmissionRevisionLockOrderSQL()
	var schedulerRevision, admissionRevision int64
	if err := tx.QueryRow(queries[0]).Scan(&schedulerRevision); err != nil {
		return err
	}
	if err := tx.QueryRow(queries[1]).Scan(&admissionRevision); err != nil {
		return err
	}
	if expected > 0 && admissionRevision != expected {
		return fmt.Errorf("%w: expected %d, found %d", ErrTestStartAdmissionRevisionChanged, expected, admissionRevision)
	}
	return nil
}
