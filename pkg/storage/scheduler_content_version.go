package storage

import (
	"fmt"
	"strconv"
)

// SchedulerContentVersion returns the database-owned monotonic revision for
// script/scenario inputs consumed by the HA scheduler. Database triggers advance
// the revision in the same transaction as every insert, update, or delete. This
// is independent of controller clocks and cannot miss an edit to a non-newest
// row, unlike COUNT+MAX(updated_at) fingerprints.
func (s *SQLiteStore) SchedulerContentVersion() (string, error) {
	var revision int64
	if err := s.db.QueryRow(`SELECT revision FROM scheduler_content_revision WHERE id = 1`).Scan(&revision); err != nil {
		return "", err
	}
	return strconv.FormatInt(revision, 10), nil
}

// ensureSchedulerContentRevisionTracking installs cross-controller revision
// tracking for both supported databases. Triggers are preferable to application
// callbacks: every supported write path, maintenance repair, and direct SQL
// migration participates automatically and atomically.
func (s *SQLiteStore) ensureSchedulerContentRevisionTracking() error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO scheduler_content_revision (id, revision) VALUES (1, 1) ON CONFLICT(id) DO NOTHING`); err != nil {
		return fmt.Errorf("initialize scheduler content revision: %w", err)
	}
	tables := schedulerContentRevisionTables()
	if s.db.dialect == storeDialectPostgres {
		// PostgreSQL constraint triggers are replaced in one DDL transaction so
		// concurrent writers always observe either the old complete trigger set or
		// the new one. In particular, never drop/create them in separate commits.
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin scheduler content trigger migration: %w", err)
		}
		defer tx.Rollback()
		// Serialize only this schema migration across HA replicas. Without the
		// advisory lock, replicas that all observed the legacy trigger could each
		// take ACCESS EXCLUSIVE table locks to repeat the same replacement.
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(7241801551050296661)`); err != nil {
			return fmt.Errorf("lock scheduler content trigger migration: %w", err)
		}
		if _, err := tx.Exec(postgresSchedulerContentRevisionFunctionSQL()); err != nil {
			return fmt.Errorf("create scheduler content revision function: %w", err)
		}
		for _, table := range tables {
			trigger := "apexload_scheduler_content_" + table
			installed, err := postgresSchedulerContentTriggerInstalled(tx, trigger, table)
			if err != nil {
				return fmt.Errorf("inspect scheduler content trigger for %s: %w", table, err)
			}
			if installed {
				continue
			}
			if _, err := tx.Exec("DROP TRIGGER IF EXISTS " + trigger + " ON " + table); err != nil {
				return fmt.Errorf("replace scheduler content trigger for %s: %w", table, err)
			}
			if _, err := tx.Exec(postgresSchedulerContentTriggerSQL(trigger, table)); err != nil {
				return fmt.Errorf("create scheduler content trigger for %s: %w", table, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit scheduler content trigger migration: %w", err)
		}
		return nil
	}

	for _, table := range tables {
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			trigger := "apexload_scheduler_content_" + table + "_" + operation
			query := "CREATE TRIGGER IF NOT EXISTS " + trigger + " AFTER " + operation + " ON " + table +
				" BEGIN UPDATE scheduler_content_revision SET revision = revision + 1 WHERE id = 1; END"
			if _, err := s.db.Exec(query); err != nil {
				return fmt.Errorf("create scheduler content trigger for %s %s: %w", table, operation, err)
			}
		}
	}
	return nil
}

func schedulerContentRevisionTables() []string {
	return []string{"scripts", "scenarios", "scenario_scripts", "scenario_schedules", "scenario_data_pool_bindings"}
}

const postgresSchedulerContentTriggerType = 1 | 4 | 8 | 16 // ROW | INSERT | DELETE | UPDATE

func postgresSchedulerContentTriggerInstalled(tx *storeTx, trigger, table string) (bool, error) {
	if tx == nil {
		return false, nil
	}
	var installed bool
	err := tx.QueryRow(postgresSchedulerContentTriggerProbeSQL(), table, trigger, postgresSchedulerContentTriggerType).Scan(&installed)
	return installed, err
}

func postgresSchedulerContentTriggerProbeSQL() string {
	// Validate the complete repository-owned shape. Merely finding a same-name
	// deferred trigger is insufficient: a disabled trigger, a trigger covering
	// only one event, or one linked to another function would otherwise suppress
	// repair forever.
	return `SELECT EXISTS (
		SELECT 1 FROM pg_trigger
		WHERE tgrelid = ?::regclass AND tgname = ? AND NOT tgisinternal
			AND tgparentid = 0 AND tgconstraint <> 0
			AND tgfoid = to_regprocedure('apexload_bump_scheduler_content_revision()')
			AND tgtype = ? AND tgenabled = 'O'
			AND tgdeferrable AND tginitdeferred
			AND tgnargs = 0 AND tgattr = ''::int2vector
			AND tgqual IS NULL AND tgoldtable IS NULL AND tgnewtable IS NULL
	)`
}

func postgresSchedulerContentRevisionFunctionSQL() string {
	// A deferred row trigger runs once for every affected row. The transaction ID
	// marker coalesces those callbacks (including callbacks from several tracked
	// tables) into one revision update per transaction. The custom setting is
	// transaction-local, and comparing the XID also remains safe if a connection
	// happens to retain an empty/reset setting between pooled transactions.
	return `CREATE OR REPLACE FUNCTION apexload_bump_scheduler_content_revision()
		RETURNS TRIGGER AS $body$
		DECLARE
			current_xid TEXT := pg_current_xact_id()::text;
		BEGIN
			IF current_setting('apexload.scheduler_content_revision_xid', true) IS DISTINCT FROM current_xid THEN
				UPDATE scheduler_content_revision SET revision = revision + 1 WHERE id = 1;
				PERFORM set_config('apexload.scheduler_content_revision_xid', current_xid, true);
			END IF;
			IF TG_OP = 'DELETE' THEN
				RETURN OLD;
			END IF;
			RETURN NEW;
		END;
		$body$ LANGUAGE plpgsql`
}

func postgresSchedulerContentTriggerSQL(trigger, table string) string {
	// Existing storage paths sometimes lock a content row before mutating it
	// (for example version-fenced script compensation and occurrence lifecycle
	// transitions). Taking the shared revision-row lock in a BEFORE trigger can
	// therefore deadlock with another writer that took the revision lock first
	// and is waiting on that content row. A deferred constraint trigger acquires
	// the revision row only at transaction end, after content-row lock ordering is
	// complete, while its update remains part of the same atomic commit.
	return "CREATE CONSTRAINT TRIGGER " + trigger + " AFTER INSERT OR UPDATE OR DELETE ON " + table + " DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION apexload_bump_scheduler_content_revision()"
}
