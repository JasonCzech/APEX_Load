package storage

import (
	"fmt"
	"log/slog"
	"strings"
)

// auditEventTieBreak orders audit events that share created_at. Audit history
// reads newest first, but created_at alone cannot order events written within
// one clock tick: Windows wall-clock time advances in coarse steps, PostgreSQL
// keeps only microseconds, and the GitHub target-owner repair stamps events
// from SQLite's millisecond clock. Ties follow insertion order rather than the
// random event ID, so a resume lists above the start written just before it.
//
// SQLite already numbers rows in insertion order: audit_events is a rowid
// table without AUTOINCREMENT, so each new row gets the largest existing rowid
// plus one. PostgreSQL has no equivalent, so its rows carry insert_seq, filled
// from a sequence. Rows written before that column existed keep NULL; they
// sort below newer rows with the same created_at, and by ID among themselves.
func (dialect storeDialect) auditEventTieBreak() string {
	if dialect == storeDialectPostgres {
		return "insert_seq DESC NULLS LAST, id DESC"
	}
	return "rowid DESC"
}

// postgresAuditEventInsertSeqMigration adds insert_seq without rewriting the
// table: a volatile default on ADD COLUMN would fill every existing row while
// holding an ACCESS EXCLUSIVE lock that blocks audit reads and writes on
// running controllers, and audit retention is opt-in, so the table can be
// large. Setting the default afterwards applies it only to new rows.
//
// The login might only be a member of the role that owns the tables, rather
// than that role itself. The sequence is handed to that owner and linked to
// the column when the login may do so (OWNED BY requires matching owners).
// Otherwise the login keeps the sequence and grants the owner every privilege
// a sequence has: USAGE to insert, SELECT for pg_dump and UPDATE for a
// data-only restore, though not owner-only rights such as ALTER or DROP.
// ensurePostgresAuditEventInsertSeq then warns on every start until an
// administrator links it. The guard keeps later restarts from repeating the
// block, and the DO block commits all of its statements together, so it never
// leaves the column without its default. CREATE SEQUENCE has no IF NOT EXISTS:
// any existing relation of that name stops initialization instead of being
// adopted. CACHE 1 is the default, spelled out because a larger cache hands
// each session (every pooled connection, on every controller) its own block of
// numbers, which would break insertion order.
//
// Changes made by hand afterwards are not repaired. If the sequence or the
// default is dropped, new rows get NULL: they sort below numbered rows with
// the same created_at, and by ID among themselves. A sequence left unlinked
// beside the table, for example after OWNED BY NONE, is reported. If
// insert_seq is dropped or renamed, the next start runs the block again: it
// re-adds the column when the sequence went with it, and stops when
// audit_events_insert_seq is still there.
const postgresAuditEventInsertSeqMigration = `DO $apexload$
DECLARE
	table_owner TEXT;
BEGIN
	IF NOT EXISTS (
		SELECT 1
		FROM pg_attribute
		WHERE attrelid = 'audit_events'::regclass
			AND attname = 'insert_seq'
			AND NOT attisdropped
	) THEN
		CREATE SEQUENCE audit_events_insert_seq AS BIGINT CACHE 1;
		ALTER TABLE audit_events ADD COLUMN insert_seq BIGINT;
		ALTER TABLE audit_events ALTER COLUMN insert_seq SET DEFAULT nextval('audit_events_insert_seq');
		SELECT pg_get_userbyid(relowner) INTO table_owner FROM pg_class WHERE oid = 'audit_events'::regclass;
		BEGIN
			EXECUTE format('ALTER SEQUENCE audit_events_insert_seq OWNER TO %I', table_owner);
			ALTER SEQUENCE audit_events_insert_seq OWNED BY audit_events.insert_seq;
		EXCEPTION WHEN insufficient_privilege THEN
			EXECUTE format('GRANT USAGE, SELECT, UPDATE ON SEQUENCE audit_events_insert_seq TO %I', table_owner);
		END;
	END IF;
END
$apexload$;`

func (s *SQLiteStore) ensurePostgresAuditEventInsertSeq() error {
	if s == nil || s.db == nil || s.db.dialect != storeDialectPostgres {
		return nil
	}
	if _, err := s.db.Exec(postgresAuditEventInsertSeqMigration); err != nil {
		return fmt.Errorf("add audit event insertion sequence: %w", err)
	}
	state, err := s.inspectPostgresAuditEventInsertSeq()
	if err != nil {
		return err
	}
	if state.linked || state.sequenceOwner == "" {
		return nil
	}
	link := strings.Join(state.linkStatements(), "; ") + ";"
	if state.sequenceOwner == state.tableOwner {
		slog.Warn("audit_events_insert_seq is not linked to audit_events.insert_seq",
			"schema", state.schema, "table_owner", state.tableOwner,
			"remediation", "as the table owner (directly or through SET ROLE) or a member that inherits its privileges, run: "+link)
		return nil
	}
	// Normally the fallback left the sequence with the login that ran it: that
	// login cannot be dropped until its objects are reassigned, and dropping
	// the column would leave the sequence behind to stop the next
	// initialization.
	slog.Warn("audit_events_insert_seq is owned by a role other than the owner of audit_events",
		"schema", state.schema, "sequence_owner", state.sequenceOwner, "table_owner", state.tableOwner,
		"remediation", "as a superuser, or as the sequence owner once it may SET ROLE to the table owner and the table owner may CREATE in the schema, run: "+link+
			" Until then, dropping the sequence owner needs REASSIGN OWNED BY it TO the table owner first, which moves everything it owns, and dropping insert_seq must drop the sequence too.")
	return nil
}

// postgresAuditEventInsertSeqState describes how insert_seq relates to the
// sequence the migration created beside audit_events.
type postgresAuditEventInsertSeqState struct {
	linked        bool   // OWNED BY the column, as the migration prefers
	sequenceOwner string // empty when the schema has no audit_events_insert_seq
	tableOwner    string
	schema        string
}

// inspectPostgresAuditEventInsertSeq reads insert_seq's sequence state.
func (s *SQLiteStore) inspectPostgresAuditEventInsertSeq() (postgresAuditEventInsertSeqState, error) {
	var state postgresAuditEventInsertSeqState
	if err := s.db.QueryRow(`SELECT pg_get_serial_sequence('audit_events', 'insert_seq') IS NOT NULL,
		COALESCE((SELECT pg_get_userbyid(seq.relowner) FROM pg_class seq
			WHERE seq.relnamespace = audit.relnamespace AND seq.relname = 'audit_events_insert_seq' AND seq.relkind = 'S'), ''),
		pg_get_userbyid(audit.relowner),
		ns.nspname
		FROM pg_class audit
		JOIN pg_namespace ns ON ns.oid = audit.relnamespace
		WHERE audit.oid = 'audit_events'::regclass`).Scan(&state.linked, &state.sequenceOwner, &state.tableOwner, &state.schema); err != nil {
		return state, fmt.Errorf("inspect audit event insertion sequence: %w", err)
	}
	return state, nil
}

// linkStatements returns the statements that hand an unlinked sequence to the
// table owner, when another role holds it, and link it to the column. They
// are schema-qualified, so they run from any search_path.
func (state postgresAuditEventInsertSeqState) linkStatements() []string {
	schema := quotePostgresIdentifier(state.schema)
	sequence := schema + ".audit_events_insert_seq"
	statements := []string{}
	if state.sequenceOwner != state.tableOwner {
		statements = append(statements, "ALTER SEQUENCE "+sequence+" OWNER TO "+quotePostgresIdentifier(state.tableOwner))
	}
	return append(statements, "ALTER SEQUENCE "+sequence+" OWNED BY "+schema+".audit_events.insert_seq")
}
