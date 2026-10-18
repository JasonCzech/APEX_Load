package storage

import (
	"fmt"
	"log/slog"
	"strings"
)

// insertSeqTable is a history table whose rows that share created_at list in
// the order they were inserted. History reads newest first, but created_at
// alone cannot order rows written within one clock tick: Windows wall-clock
// time advances in coarse steps, PostgreSQL keeps only microseconds, and the
// GitHub target-owner repair stamps audit events from SQLite's millisecond
// clock. Ties follow insertion order rather than the row ID, whose order means
// nothing, so a resume lists above the start written just before it.
//
// SQLite already numbers rows in insertion order: these are rowid tables
// without AUTOINCREMENT, so each new row gets the largest existing rowid plus
// one. PostgreSQL has no equivalent, so their rows carry insert_seq, filled
// from a sequence per table. Rows written before that column existed keep
// NULL; they sort below newer rows with the same created_at, and by ID among
// themselves.
type insertSeqTable string

const (
	auditEventsTable    insertSeqTable = "audit_events"
	objectActivityTable insertSeqTable = "object_activity_events"
)

func (table insertSeqTable) sequence() string { return string(table) + "_insert_seq" }

// notLinkedWarning and foreignOwnerWarning are the two messages
// ensurePostgresInsertSeq logs, named for the table.
func (table insertSeqTable) notLinkedWarning() string {
	return table.sequence() + " is not linked to " + string(table) + ".insert_seq"
}

func (table insertSeqTable) foreignOwnerWarning() string {
	return table.sequence() + " is owned by a role other than the owner of " + string(table)
}

// newestFirst is the ORDER BY for an insertSeqTable read newest first.
// qualifier is a table name or alias followed by a dot, or empty.
func (dialect storeDialect) newestFirst(qualifier string) string {
	if dialect == storeDialectPostgres {
		return qualifier + "created_at DESC, " + qualifier + "insert_seq DESC NULLS LAST, " + qualifier + "id DESC"
	}
	return qualifier + "created_at DESC, " + qualifier + "rowid DESC"
}

// oldestFirst is the exact reverse of newestFirst.
func (dialect storeDialect) oldestFirst(qualifier string) string {
	if dialect == storeDialectPostgres {
		return qualifier + "created_at ASC, " + qualifier + "insert_seq ASC NULLS FIRST, " + qualifier + "id ASC"
	}
	return qualifier + "created_at ASC, " + qualifier + "rowid ASC"
}

// newerThan is the predicate "row a lists before row b in newestFirst order",
// for rows of the same table qualified by a and b, each a table name or alias
// followed by a dot. On PostgreSQL a NULL insert_seq counts as 0, below every
// number the sequence hands out (they start at 1), as NULLS LAST ranks it in
// newestFirst, and two NULL rows that tie fall back to their IDs.
func (dialect storeDialect) newerThan(a, b string) string {
	if dialect == storeDialectPostgres {
		return "(" + a + "created_at, COALESCE(" + a + "insert_seq, 0), " + a + "id) > (" + b + "created_at, COALESCE(" + b + "insert_seq, 0), " + b + "id)"
	}
	return "(" + a + "created_at, " + a + "rowid) > (" + b + "created_at, " + b + "rowid)"
}

// postgresMigration adds insert_seq without rewriting the table: a volatile
// default on ADD COLUMN would fill every existing row while holding an ACCESS
// EXCLUSIVE lock that blocks reads and writes on running controllers, and
// audit retention is opt-in, so these tables can be large. Setting the default
// afterwards applies it only to new rows.
//
// The login might only be a member of the role that owns the tables, rather
// than that role itself. The sequence is handed to that owner and linked to
// the column when the login may do so (OWNED BY requires matching owners).
// Otherwise the login keeps the sequence and grants the owner every privilege
// a sequence has: USAGE to insert, SELECT for pg_dump and UPDATE for a
// data-only restore, though not owner-only rights such as ALTER or DROP.
// ensurePostgresInsertSeq then warns on every start until an administrator
// links it. The guard keeps later restarts from repeating the block, and the
// DO block commits all of its statements together, so it never leaves the
// column without its default. CREATE SEQUENCE has no IF NOT EXISTS: any
// existing relation of that name stops initialization instead of being
// adopted. CACHE 1 is the default, spelled out because a larger cache hands
// each session (every pooled connection, on every controller) its own block of
// numbers, which would break insertion order.
//
// Changes made by hand afterwards are not repaired. If the sequence or the
// default is dropped, new rows get NULL: they sort below numbered rows with
// the same created_at, and by ID among themselves. A sequence left unlinked
// beside the table, for example after OWNED BY NONE, is reported. If
// insert_seq is dropped or renamed, the next start runs the block again: it
// re-adds the column when the sequence went with it, and stops when the
// sequence is still there.
func (table insertSeqTable) postgresMigration() string {
	return table.expand(postgresInsertSeqMigration)
}

// expand fills a statement template with the table and sequence names. Both
// are constants of this package, never input.
func (table insertSeqTable) expand(template string) string {
	return strings.NewReplacer("{table}", string(table), "{sequence}", table.sequence()).Replace(template)
}

const postgresInsertSeqMigration = `DO $apexload$
DECLARE
	table_owner TEXT;
BEGIN
	IF NOT EXISTS (
		SELECT 1
		FROM pg_attribute
		WHERE attrelid = '{table}'::regclass
			AND attname = 'insert_seq'
			AND NOT attisdropped
	) THEN
		CREATE SEQUENCE {sequence} AS BIGINT CACHE 1;
		ALTER TABLE {table} ADD COLUMN insert_seq BIGINT;
		ALTER TABLE {table} ALTER COLUMN insert_seq SET DEFAULT nextval('{sequence}');
		SELECT pg_get_userbyid(relowner) INTO table_owner FROM pg_class WHERE oid = '{table}'::regclass;
		BEGIN
			EXECUTE format('ALTER SEQUENCE {sequence} OWNER TO %I', table_owner);
			ALTER SEQUENCE {sequence} OWNED BY {table}.insert_seq;
		EXCEPTION WHEN insufficient_privilege THEN
			EXECUTE format('GRANT USAGE, SELECT, UPDATE ON SEQUENCE {sequence} TO %I', table_owner);
		END;
	END IF;
END
$apexload$;`

func (s *SQLiteStore) ensurePostgresInsertSeq(table insertSeqTable) error {
	if s == nil || s.db == nil || s.db.dialect != storeDialectPostgres {
		return nil
	}
	if _, err := s.db.Exec(table.postgresMigration()); err != nil {
		return fmt.Errorf("add %s insertion sequence: %w", table, err)
	}
	state, err := s.inspectPostgresInsertSeq(table)
	if err != nil {
		return err
	}
	if state.linked || state.sequenceOwner == "" {
		return nil
	}
	link := strings.Join(state.linkStatements(), "; ") + ";"
	if state.sequenceOwner == state.tableOwner {
		slog.Warn(table.notLinkedWarning(),
			"table", string(table), "sequence", table.sequence(), "schema", state.schema, "table_owner", state.tableOwner,
			"remediation", "as the table owner (directly or through SET ROLE) or a member that inherits its privileges, run: "+link)
		return nil
	}
	// Normally the fallback left the sequence with the login that ran it: that
	// login cannot be dropped until its objects are reassigned, and dropping
	// the column would leave the sequence behind to stop the next
	// initialization.
	slog.Warn(table.foreignOwnerWarning(),
		"table", string(table), "sequence", table.sequence(), "schema", state.schema, "sequence_owner", state.sequenceOwner, "table_owner", state.tableOwner,
		"remediation", "as a superuser, or as the sequence owner once it may SET ROLE to the table owner and the table owner may CREATE in the schema, run: "+link+
			" Until then, dropping the sequence owner needs REASSIGN OWNED BY it TO the table owner first, which moves everything it owns, and dropping insert_seq must drop the sequence too.")
	return nil
}

// postgresInsertSeqState describes how a table's insert_seq relates to the
// sequence the migration created beside it.
type postgresInsertSeqState struct {
	table         insertSeqTable
	linked        bool   // OWNED BY the column, as the migration prefers
	sequenceOwner string // empty when the schema has no such sequence
	tableOwner    string
	schema        string
}

// inspectPostgresInsertSeq reads a table's insert_seq sequence state.
func (s *SQLiteStore) inspectPostgresInsertSeq(table insertSeqTable) (postgresInsertSeqState, error) {
	state := postgresInsertSeqState{table: table}
	if err := s.db.QueryRow(table.expand(`SELECT pg_get_serial_sequence('{table}', 'insert_seq') IS NOT NULL,
		COALESCE((SELECT pg_get_userbyid(seq.relowner) FROM pg_class seq
			WHERE seq.relnamespace = tbl.relnamespace AND seq.relname = '{sequence}' AND seq.relkind = 'S'), ''),
		pg_get_userbyid(tbl.relowner),
		ns.nspname
		FROM pg_class tbl
		JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
		WHERE tbl.oid = '{table}'::regclass`)).Scan(&state.linked, &state.sequenceOwner, &state.tableOwner, &state.schema); err != nil {
		return state, fmt.Errorf("inspect %s insertion sequence: %w", table, err)
	}
	return state, nil
}

// linkStatements returns the statements that hand an unlinked sequence to the
// table owner, when another role holds it, and link it to the column. They
// are schema-qualified, so they run from any search_path.
func (state postgresInsertSeqState) linkStatements() []string {
	schema := quotePostgresIdentifier(state.schema)
	sequence := schema + "." + state.table.sequence()
	statements := []string{}
	if state.sequenceOwner != state.tableOwner {
		statements = append(statements, "ALTER SEQUENCE "+sequence+" OWNER TO "+quotePostgresIdentifier(state.tableOwner))
	}
	return append(statements, "ALTER SEQUENCE "+sequence+" OWNED BY "+schema+"."+string(state.table)+".insert_seq")
}
