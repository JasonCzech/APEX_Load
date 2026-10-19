//go:build postgres_integration

package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestPostgresAuditEventTiesListInInsertionOrder runs the shared tie-order
// check on a fresh schema, upgrades a pre-insert_seq audit table, and upgrades
// one as a login that may not hand the sequence to the table's owner. Run with:
//
//	APEXLOAD_TEST_POSTGRES_URL='postgres://...' go test -tags postgres_integration \
//		./pkg/storage -run TestPostgresAuditEventTiesListInInsertionOrder
func TestPostgresAuditEventTiesListInInsertionOrder(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	t.Run("fresh", func(t *testing.T) {
		// The common install: the login owns the tables, so the sequence is
		// linked at once and no start warns.
		warnings := captureInsertSeqWarnings(t)
		store, err := NewPostgresStore(postgresIsolatedSchema(t, databaseURL, "fresh"), StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatalf("NewPostgresStore returned error: %v", err)
		}
		defer store.Close()
		if state, err := store.inspectPostgresInsertSeq(auditEventsTable); err != nil || !state.linked || state.sequenceOwner != state.tableOwner {
			t.Fatalf("fresh sequence state = %+v (err=%v), want linked and owned by the table owner", state, err)
		}
		if got := warnings(); len(got) != 0 {
			t.Fatalf("fresh install logged insert_seq warnings: %+v", got)
		}
		checkAuditEventTiesListInInsertionOrder(t, store.SQLiteStore)
	})
	t.Run("upgrade", func(t *testing.T) {
		checkPostgresAuditEventInsertSeqUpgrade(t, databaseURL)
	})
	t.Run("member-login-owner-without-create", func(t *testing.T) {
		checkPostgresAuditEventInsertSeqForMemberLogin(t, databaseURL, false)
	})
	t.Run("member-login-without-set", func(t *testing.T) {
		checkPostgresAuditEventInsertSeqForMemberLogin(t, databaseURL, true)
	})
}

func checkPostgresAuditEventInsertSeqUpgrade(t *testing.T, databaseURL string) {
	t.Helper()
	// The store must use pg_database_owner's privileges (USAGE), and handing
	// it the table needs SET on it from PostgreSQL 16; before that, the
	// membership USAGE implies is enough. pg_database_owner exists from 14.
	if !postgresLoginHas(t, databaseURL, `CASE
		WHEN current_setting('server_version_num')::int < 140000 THEN false
		WHEN current_setting('server_version_num')::int < 160000 THEN pg_has_role('pg_database_owner', 'USAGE')
		ELSE pg_has_role('pg_database_owner', 'SET') AND pg_has_role('pg_database_owner', 'USAGE') END`) {
		t.Skip("handing a table to pg_database_owner needs PostgreSQL 14 or later and a superuser or the database owner")
	}
	scopedURL := postgresIsolatedSchema(t, databaseURL, "upgrade")
	tied := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	// A table owned by another role (here pg_database_owner): OWNED BY must
	// still work. The grant lets a non-superuser database owner hand it over.
	legacyStatements := []string{
		legacyAuditEventsDDL,
		`GRANT CREATE ON SCHEMA ` + quotePostgresIdentifier(postgresSearchPath(t, scopedURL)) + ` TO pg_database_owner`,
		`ALTER TABLE audit_events OWNER TO pg_database_owner`,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	legacyDB, err := sql.Open("pgx", scopedURL)
	if err != nil {
		t.Fatalf("open legacy PostgreSQL schema: %v", err)
	}
	for _, statement := range legacyStatements {
		if _, err := legacyDB.ExecContext(ctx, statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy audit_events (%s): %v", statement, err)
		}
	}
	// Neither the physical order (b,c,a) nor its reverse matches ID order, so the
	// expected legacy-c,legacy-b,legacy-a can only come from the id DESC fallback.
	for _, id := range []string{"legacy-b", "legacy-c", "legacy-a"} {
		if _, err := legacyDB.ExecContext(ctx, `INSERT INTO audit_events
			(id, action, actor_type, actor_subject, actor_display_name, project_id, domain_id, resource_type, resource_id, status, details_json, created_at)
			VALUES ($1, 'legacy.action', 'user', 'alice', '', '', '', '', '', 'success', '{}', $2)`, id, tied); err != nil {
			legacyDB.Close()
			t.Fatalf("seed legacy audit event %s: %v", id, err)
		}
	}
	var legacyFile int64
	if err := legacyDB.QueryRowContext(ctx, `SELECT pg_relation_filenode('audit_events')`).Scan(&legacyFile); err != nil {
		legacyDB.Close()
		t.Fatalf("read legacy audit_events file node: %v", err)
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy PostgreSQL connection: %v", err)
	}

	// Cleanups run before the schema drop registered earlier; Close is
	// idempotent, so the explicit Close before reopening is safe too.
	open := func(step string) *PostgresStore {
		t.Helper()
		store, err := NewPostgresStore(scopedURL, StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}

	// A linked sequence logs nothing on any start.
	warnings := captureInsertSeqWarnings(t)
	store := open("initialize PostgreSQL store over legacy audit_events")
	var legacyNulls int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE insert_seq IS NULL`).Scan(&legacyNulls); err != nil {
		t.Fatalf("count legacy rows without insert_seq: %v", err)
	}
	if legacyNulls != 3 {
		t.Fatalf("legacy rows without insert_seq = %d, want 3", legacyNulls)
	}
	var upgradedFile int64
	if err := store.db.QueryRow(`SELECT pg_relation_filenode('audit_events')`).Scan(&upgradedFile); err != nil {
		t.Fatalf("read upgraded audit_events file node: %v", err)
	}
	if upgradedFile != legacyFile {
		t.Fatalf("audit_events file node = %d, want %d (the upgrade must not rewrite the table)", upgradedFile, legacyFile)
	}
	ownedSequence, tableOwner, sequenceOwner := readAuditInsertSeqOwnership(t, store)
	if !ownedSequence.Valid || !strings.HasSuffix(ownedSequence.String, ".audit_events_insert_seq") {
		t.Fatalf("insert_seq owned sequence = %#v, want audit_events_insert_seq", ownedSequence)
	}
	if tableOwner != "pg_database_owner" || sequenceOwner != tableOwner {
		t.Fatalf("owners: table %q, sequence %q; want both pg_database_owner", tableOwner, sequenceOwner)
	}
	if state, err := store.inspectPostgresInsertSeq(auditEventsTable); err != nil || !state.linked {
		t.Fatalf("upgraded sequence state = %+v (err=%v), want linked", state, err)
	}
	// Rows from before the upgrade list below newer rows with the same time.
	saveTiedAuditEvent(t, store, "z-new-first", tied)
	saveTiedAuditEvent(t, store, "a-new-second", tied)
	if got, want := listAuditEventIDs(t, store), "a-new-second,z-new-first,legacy-c,legacy-b,legacy-a"; got != want {
		t.Fatalf("audit order after upgrade = %s, want %s", got, want)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close upgraded PostgreSQL store: %v", err)
	}

	// A second initialization skips the migration, and the sequence keeps counting.
	reopened := open("reopen upgraded PostgreSQL store")
	saveTiedAuditEvent(t, reopened, "m-new-third", tied)
	if got, want := listAuditEventIDs(t, reopened), "m-new-third,a-new-second,z-new-first,legacy-c,legacy-b,legacy-a"; got != want {
		t.Fatalf("audit order after reopen = %s, want %s", got, want)
	}

	// A sequence dropped by hand is out of scope: startup still succeeds without
	// a warning, and audit history stays readable.
	if _, err := reopened.db.Exec(`DROP SEQUENCE audit_events_insert_seq CASCADE`); err != nil {
		t.Fatalf("drop the insertion sequence by hand: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close PostgreSQL store after dropping the sequence: %v", err)
	}
	dropped := open("restart with the sequence dropped by hand")
	if got := warnings(); len(got) != 0 {
		t.Fatalf("a linked or hand-dropped sequence logged insert_seq warnings: %+v", got)
	}
	// The new row has no insert_seq: below the numbered rows, then by ID.
	saveTiedAuditEvent(t, dropped, "n-after-drop", tied)
	if got, want := listAuditEventIDs(t, dropped), "m-new-third,a-new-second,z-new-first,n-after-drop,legacy-c,legacy-b,legacy-a"; got != want {
		t.Fatalf("audit order after dropping the sequence = %s, want %s", got, want)
	}
}

// checkPostgresAuditEventInsertSeqForMemberLogin upgrades as a login that only
// belongs to the role owning audit_events and may not hand the sequence to it:
// either that role has no CREATE on the schema, or (PostgreSQL 16 and later)
// the membership was granted WITH SET FALSE. The login keeps the sequence and
// grants the owner its privileges instead of failing initialization. Each
// start then warns, and the test follows the route the warning names for
// that case until the sequence is linked and the warnings stop.
func checkPostgresAuditEventInsertSeqForMemberLogin(t *testing.T, databaseURL string, withoutSet bool) {
	t.Helper()
	if !postgresLoginHas(t, databaseURL, `rolsuper`) {
		t.Skip("creating the owner and member roles needs a superuser login")
	}
	if withoutSet && !postgresLoginHas(t, databaseURL, `current_setting('server_version_num')::int >= 160000`) {
		t.Skip("membership WITH SET FALSE needs PostgreSQL 16 or later")
	}
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })

	suffix := time.Now().UnixNano()
	ownerRole := fmt.Sprintf("apexload_audit_owner_%d", suffix)
	loginRole := fmt.Sprintf("apexload_audit_login_%d", suffix)
	password := rand.Text()
	schemaName := fmt.Sprintf("apexload_insert_seq_member_%d", suffix)
	owner, login, schema := quotePostgresIdentifier(ownerRole), quotePostgresIdentifier(loginRole), quotePostgresIdentifier(schemaName)
	admin := func(step, statement string) {
		t.Helper()
		statementCtx, statementCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer statementCancel()
		if _, err := adminDB.ExecContext(statementCtx, statement); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	// Cleanups run last-in first-out: the store closes, the schema and then
	// both roles are dropped, and the admin connection closes last.
	dropLater := func(step, statement string) {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if _, err := adminDB.ExecContext(cleanupCtx, statement); err != nil {
				t.Errorf("%s: %v", step, err)
			}
		})
	}
	admin("create owner role", `CREATE ROLE `+owner+` NOLOGIN`)
	dropLater("drop owner role", `DROP ROLE IF EXISTS `+owner)
	admin("create member login", `CREATE ROLE `+login+` LOGIN PASSWORD '`+password+`'`)
	dropLater("drop member login", `DROP ROLE IF EXISTS `+login)
	if withoutSet {
		admin("grant membership without SET", `GRANT `+owner+` TO `+login+` WITH INHERIT TRUE, SET FALSE`)
	} else {
		admin("grant membership", `GRANT `+owner+` TO `+login)
	}
	admin("create member schema", `CREATE SCHEMA `+schema+` AUTHORIZATION `+login)
	dropLater("drop member schema", `DROP SCHEMA `+schema+` CASCADE`)
	if withoutSet {
		// The owner could hold the sequence; only the missing SET blocks it.
		admin("let the owner role create in the schema", `GRANT CREATE ON SCHEMA `+schema+` TO `+owner)
	}
	admin("create legacy audit_events", strings.Replace(legacyAuditEventsDDL, "CREATE TABLE audit_events", "CREATE TABLE "+schema+".audit_events", 1))
	admin("hand audit_events to the owner role", `ALTER TABLE `+schema+`.audit_events OWNER TO `+owner)

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse APEXLOAD_TEST_POSTGRES_URL: %v", err)
	}
	// Connect as the member login even when the URL names its user in the query.
	parsed.User = url.UserPassword(loginRole, password)
	query := parsed.Query()
	query.Del("user")
	query.Del("password")
	parsed.RawQuery = query.Encode()
	memberURL := postgresIntegrationURLWithSearchPath(t, parsed.String(), schemaName)
	if err := pingPostgres(memberURL); err != nil {
		var pgErr *pgconn.PgError
		// Class 28 is a refused password login; 42501 a database without
		// CONNECT for PUBLIC.
		if errors.As(err, &pgErr) && (strings.HasPrefix(pgErr.Code, "28") || pgErr.Code == "42501") {
			t.Skipf("the test server refuses a login for a new role: %v", err)
		}
		t.Fatalf("connect as the member login: %v", err)
	}
	open := func(step string) *PostgresStore {
		t.Helper()
		store, err := NewPostgresStore(memberURL, StoreConnectionOptions{}, false)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	warnings := captureInsertSeqWarnings(t)
	store := open("initialize PostgreSQL store as a member login")

	ownedSequence, tableOwner, sequenceOwner := readAuditInsertSeqOwnership(t, store)
	if ownedSequence.Valid {
		t.Fatalf("insert_seq owned sequence = %q, want none when the owner cannot take the sequence", ownedSequence.String)
	}
	if tableOwner != ownerRole || sequenceOwner != loginRole {
		t.Fatalf("owners: table %q, sequence %q; want %q and %q", tableOwner, sequenceOwner, ownerRole, loginRole)
	}
	state, err := store.inspectPostgresInsertSeq(auditEventsTable)
	if want := (postgresInsertSeqState{table: auditEventsTable, sequenceOwner: loginRole, tableOwner: ownerRole, schema: schemaName}); err != nil || state != want {
		t.Fatalf("sequence state = %+v (err=%v), want %+v", state, err, want)
	}
	for _, privilege := range []string{"USAGE", "SELECT", "UPDATE"} {
		var granted bool
		if err := store.db.QueryRow(`SELECT has_sequence_privilege(CAST(? AS NAME), 'audit_events_insert_seq', ?)`, ownerRole, privilege).Scan(&granted); err != nil {
			t.Fatalf("read owner sequence privilege %s: %v", privilege, err)
		}
		if !granted {
			t.Fatalf("owner role %q lacks %s on audit_events_insert_seq", ownerRole, privilege)
		}
	}
	tied := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	saveTiedAuditEvent(t, store, "z-member-first", tied)
	saveTiedAuditEvent(t, store, "a-member-second", tied)
	if got, want := listAuditEventIDs(t, store), "a-member-second,z-member-first"; got != want {
		t.Fatalf("audit order as a member login = %s, want %s", got, want)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close member login store: %v", err)
	}

	// Every start warns while the sequence stays with the login, and the
	// insertion order carries on across the restart.
	reopened := open("reopen PostgreSQL store as a member login")
	link := state.linkStatements()
	if len(link) != 2 || !strings.Contains(link[0], "OWNER TO "+owner) || !strings.Contains(link[1], "OWNED BY "+schema+".audit_events.insert_seq") {
		t.Fatalf("link statements = %q, want the hand-over to %s and then the link", link, owner)
	}
	logged := warnings()
	if len(logged) != 2 {
		t.Fatalf("insert_seq warnings = %+v, want one per start (2)", logged)
	}
	for _, warning := range logged {
		if warning.Msg != "audit_events_insert_seq is owned by a role other than the owner of audit_events" ||
			warning.Table != "audit_events" || warning.Sequence != "audit_events_insert_seq" ||
			warning.SequenceOwner != loginRole || warning.TableOwner != ownerRole || warning.Schema != schemaName ||
			!strings.Contains(warning.Remediation, link[0]+"; "+link[1]+";") {
			t.Fatalf("login-owned sequence warning = %+v, want owners %q/%q and remediation %q", warning, loginRole, ownerRole, link)
		}
	}
	saveTiedAuditEvent(t, reopened, "m-member-third", tied)
	if got, want := listAuditEventIDs(t, reopened), "m-member-third,a-member-second,z-member-first"; got != want {
		t.Fatalf("audit order after reopening as a member login = %s, want %s", got, want)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened member login store: %v", err)
	}

	// Follow the warning, by each route it names.
	if withoutSet {
		// A superuser runs the hand-over; the next start then asks only for
		// the link, which a member that inherits the owner's privileges may
		// run.
		admin("hand the sequence to the owner as the warning says", link[0])
		handedOver := open("reopen after the sequence was handed to the owner")
		logged = warnings()
		if len(logged) != 3 || logged[2].Msg != "audit_events_insert_seq is not linked to audit_events.insert_seq" ||
			logged[2].Table != "audit_events" || logged[2].TableOwner != ownerRole ||
			!strings.Contains(logged[2].Remediation, link[1]+";") || strings.Contains(logged[2].Remediation, "OWNER TO") {
			t.Fatalf("insert_seq warnings after the hand-over = %+v, want a third asking only for %q", logged, link[1])
		}
		sameOwner, err := handedOver.inspectPostgresInsertSeq(auditEventsTable)
		if err != nil || sameOwner.linked || sameOwner.sequenceOwner != ownerRole {
			t.Fatalf("sequence state after hand-over = %+v (err=%v), want unlinked and owned by %q", sameOwner, err, ownerRole)
		}
		if remaining := sameOwner.linkStatements(); len(remaining) != 1 || remaining[0] != link[1] {
			t.Fatalf("remaining link statements = %q, want only %q", remaining, link[1])
		}
		if _, err := handedOver.db.Exec(link[1]); err != nil {
			t.Fatalf("link the sequence as a member of the owner: %v", err)
		}
		if err := handedOver.Close(); err != nil {
			t.Fatalf("close store after linking the sequence: %v", err)
		}
	} else {
		// Once the table owner may create in the schema, the sequence owner
		// runs both statements itself.
		admin("let the owner role create in the schema", `GRANT CREATE ON SCHEMA `+schema+` TO `+owner)
		loginDB, err := sql.Open("pgx", memberURL)
		if err != nil {
			t.Fatalf("open member login connection: %v", err)
		}
		for _, statement := range link {
			statementCtx, statementCancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err := loginDB.ExecContext(statementCtx, statement)
			statementCancel()
			if err != nil {
				loginDB.Close()
				t.Fatalf("run %q as the sequence owner: %v", statement, err)
			}
		}
		if err := loginDB.Close(); err != nil {
			t.Fatalf("close member login connection: %v", err)
		}
	}
	relinked := open("reopen after linking the sequence")
	if state, err := relinked.inspectPostgresInsertSeq(auditEventsTable); err != nil || !state.linked || state.sequenceOwner != ownerRole {
		t.Fatalf("sequence state after linking = %+v (err=%v), want linked and owned by %q", state, err, ownerRole)
	}
	if got := warnings(); len(got) != len(logged) {
		t.Fatalf("insert_seq warnings after linking = %+v, want none beyond the earlier %d", got, len(logged))
	}
}

// insertSeqWarning is one insert_seq warning as the JSON handler logs it.
type insertSeqWarning struct {
	Msg           string `json:"msg"`
	Table         string `json:"table"`
	Sequence      string `json:"sequence"`
	Schema        string `json:"schema"`
	SequenceOwner string `json:"sequence_owner"`
	TableOwner    string `json:"table_owner"`
	Remediation   string `json:"remediation"`
}

// captureInsertSeqWarnings routes slog warnings to a buffer until the test
// ends and returns a reader for the insert_seq ones, so unrelated startup
// warnings cannot affect the counts.
func captureInsertSeqWarnings(t *testing.T) func() []insertSeqWarning {
	t.Helper()
	var mu sync.Mutex
	var logs bytes.Buffer
	// slog.SetDefault also redirects the log package, and restoring the
	// original slog handler does not undo that, so restore both.
	original, logWriter, logFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewJSONHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return logs.Write(p)
	}), &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() {
		slog.SetDefault(original)
		log.SetOutput(logWriter)
		log.SetFlags(logFlags)
	})
	return func() []insertSeqWarning {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		var warnings []insertSeqWarning
		for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var warning insertSeqWarning
			if err := json.Unmarshal([]byte(line), &warning); err != nil {
				t.Fatalf("log line is not JSON: %q: %v", line, err)
			}
			if strings.Contains(warning.Msg, "_insert_seq ") {
				warnings = append(warnings, warning)
			}
		}
		return warnings
	}
}

type writerFunc func([]byte) (int, error)

func (write writerFunc) Write(p []byte) (int, error) { return write(p) }

func pingPostgres(databaseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}

// postgresLoginHas evaluates a boolean expression over the login's pg_roles
// row, such as rolsuper, to decide whether a subtest can run.
func postgresLoginHas(t *testing.T, databaseURL, expression string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	defer db.Close()
	var allowed bool
	if err := db.QueryRowContext(ctx, `SELECT `+expression+` FROM pg_roles WHERE rolname = current_user`).Scan(&allowed); err != nil {
		t.Fatalf("read PostgreSQL login attributes (%s): %v", expression, err)
	}
	return allowed
}

// legacyAuditEventsDDL is audit_events as PostgreSQL held it before insert_seq.
const legacyAuditEventsDDL = `CREATE TABLE audit_events (
	id TEXT PRIMARY KEY,
	action TEXT NOT NULL,
	actor_type TEXT NOT NULL,
	actor_subject TEXT NOT NULL,
	actor_display_name TEXT,
	project_id TEXT,
	domain_id TEXT,
	resource_type TEXT,
	resource_id TEXT,
	status TEXT NOT NULL,
	details_json TEXT,
	created_at TIMESTAMPTZ NOT NULL
)`

func readAuditInsertSeqOwnership(t *testing.T, store *PostgresStore) (ownedSequence sql.NullString, tableOwner, sequenceOwner string) {
	t.Helper()
	if err := store.db.QueryRow(`SELECT pg_get_serial_sequence('audit_events', 'insert_seq'),
		pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid = 'audit_events'::regclass)),
		pg_get_userbyid((SELECT relowner FROM pg_class WHERE oid = 'audit_events_insert_seq'::regclass))`).Scan(&ownedSequence, &tableOwner, &sequenceOwner); err != nil {
		t.Fatalf("read insert_seq sequence ownership: %v", err)
	}
	return ownedSequence, tableOwner, sequenceOwner
}

func saveTiedAuditEvent(t *testing.T, store *PostgresStore, id string, createdAt time.Time) {
	t.Helper()
	if err := store.SaveAuditEvent(&AuditEvent{ID: id, Action: "new.action", ActorType: "user", ActorSubject: "alice", Status: "success", CreatedAt: createdAt}); err != nil {
		t.Fatalf("SaveAuditEvent(%s): %v", id, err)
	}
}

func listAuditEventIDs(t *testing.T, store *PostgresStore) string {
	t.Helper()
	events, err := store.ListAuditEvents(AuditEventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	return strings.Join(auditEventIDs(events), ",")
}

// postgresSearchPath returns the schema a scoped integration URL selects.
func postgresSearchPath(t *testing.T, scopedURL string) string {
	t.Helper()
	parsed, err := url.Parse(scopedURL)
	if err != nil {
		t.Fatalf("parse scoped PostgreSQL URL: %v", err)
	}
	return parsed.Query().Get("search_path")
}

// postgresIsolatedSchema creates an isolated schema dropped after the test
// and returns the database URL scoped to it.
func postgresIsolatedSchema(t *testing.T, databaseURL, label string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	schemaName := fmt.Sprintf("apexload_insert_seq_%s_%d", label, time.Now().UnixNano())
	quotedSchema := quotePostgresIdentifier(schemaName)
	if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		adminDB.Close()
		t.Fatalf("create isolated PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		defer adminDB.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := adminDB.ExecContext(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated PostgreSQL schema: %v", err)
		}
	})
	return postgresIntegrationURLWithSearchPath(t, databaseURL, schemaName)
}
