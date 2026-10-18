package storage

import (
	"strings"
	"testing"
)

func TestPostgresInsertSeqMigrationIsGuardedAndNative(t *testing.T) {
	for _, table := range []insertSeqTable{auditEventsTable, objectActivityTable} {
		t.Run(string(table), func(t *testing.T) {
			migration := table.postgresMigration()
			for _, fragment := range []string{
				"IF NOT EXISTS (",
				"attrelid = '" + string(table) + "'::regclass",
				"attname = 'insert_seq'",
				"AND NOT attisdropped",
				"CREATE SEQUENCE " + string(table) + "_insert_seq AS BIGINT CACHE 1;",
				"ALTER TABLE " + string(table) + " ADD COLUMN insert_seq BIGINT;",
				"SET DEFAULT nextval('" + string(table) + "_insert_seq')",
				"SELECT pg_get_userbyid(relowner) INTO table_owner FROM pg_class WHERE oid = '" + string(table) + "'::regclass;",
				"ALTER SEQUENCE " + string(table) + "_insert_seq OWNER TO %I",
				"OWNED BY " + string(table) + ".insert_seq",
				"EXCEPTION WHEN insufficient_privilege THEN",
				"GRANT USAGE, SELECT, UPDATE ON SEQUENCE " + string(table) + "_insert_seq TO %I",
			} {
				if !strings.Contains(migration, fragment) {
					t.Errorf("Postgres insertion sequence migration missing %q: %s", fragment, migration)
				}
			}
			if strings.Contains(migration, "{") {
				t.Fatalf("Postgres insertion sequence migration has an unfilled placeholder: %s", migration)
			}
			if got := rewriteSQL(migration, storeDialectPostgres); got != migration {
				t.Fatalf("dialect rewrite changed native Postgres migration:\ngot:  %s\nwant: %s", got, migration)
			}
		})
	}
}

// TestInsertionOrderPerDialect pins each dialect's ORDER BY and newerThan text,
// because only the postgres_integration tests (with APEXLOAD_TEST_POSTGRES_URL)
// run the PostgreSQL ones, and checks that the PostgreSQL rewrite leaves them
// as written.
func TestInsertionOrderPerDialect(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dialect storeDialect
		got     string
		want    string
	}{
		{name: "postgres newest first", dialect: storeDialectPostgres, got: storeDialectPostgres.newestFirst(""), want: "created_at DESC, insert_seq DESC NULLS LAST, id DESC"},
		{name: "postgres oldest first", dialect: storeDialectPostgres, got: storeDialectPostgres.oldestFirst(""), want: "created_at ASC, insert_seq ASC NULLS FIRST, id ASC"},
		{name: "postgres qualified", dialect: storeDialectPostgres, got: storeDialectPostgres.newestFirst("latest."), want: "latest.created_at DESC, latest.insert_seq DESC NULLS LAST, latest.id DESC"},
		{name: "postgres qualified oldest first", dialect: storeDialectPostgres, got: storeDialectPostgres.oldestFirst("latest."), want: "latest.created_at ASC, latest.insert_seq ASC NULLS FIRST, latest.id ASC"},
		{name: "postgres newer than", dialect: storeDialectPostgres, got: storeDialectPostgres.newerThan("c.", "o."), want: "(c.created_at, COALESCE(c.insert_seq, 0), c.id) > (o.created_at, COALESCE(o.insert_seq, 0), o.id)"},
		{name: "sqlite newest first", dialect: storeDialectSQLite, got: storeDialectSQLite.newestFirst(""), want: "created_at DESC, rowid DESC"},
		{name: "sqlite oldest first", dialect: storeDialectSQLite, got: storeDialectSQLite.oldestFirst(""), want: "created_at ASC, rowid ASC"},
		{name: "sqlite qualified", dialect: storeDialectSQLite, got: storeDialectSQLite.newestFirst("latest."), want: "latest.created_at DESC, latest.rowid DESC"},
		{name: "sqlite qualified oldest first", dialect: storeDialectSQLite, got: storeDialectSQLite.oldestFirst("latest."), want: "latest.created_at ASC, latest.rowid ASC"},
		{name: "sqlite newer than", dialect: storeDialectSQLite, got: storeDialectSQLite.newerThan("c.", "o."), want: "(c.created_at, c.rowid) > (o.created_at, o.rowid)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("SQL = %q, want %q", tc.got, tc.want)
			}
			if got := rewriteSQL(tc.want, tc.dialect); got != tc.want {
				t.Fatalf("dialect rewrite changed the SQL:\ngot:  %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestInsertSeqWarningsNameTheTable(t *testing.T) {
	for _, tc := range []struct {
		table                   insertSeqTable
		notLinked, foreignOwner string
	}{
		{table: auditEventsTable, notLinked: "audit_events_insert_seq is not linked to audit_events.insert_seq", foreignOwner: "audit_events_insert_seq is owned by a role other than the owner of audit_events"},
		{table: objectActivityTable, notLinked: "object_activity_events_insert_seq is not linked to object_activity_events.insert_seq", foreignOwner: "object_activity_events_insert_seq is owned by a role other than the owner of object_activity_events"},
	} {
		if got := tc.table.notLinkedWarning(); got != tc.notLinked {
			t.Errorf("%s not-linked warning = %q, want %q", tc.table, got, tc.notLinked)
		}
		if got := tc.table.foreignOwnerWarning(); got != tc.foreignOwner {
			t.Errorf("%s foreign-owner warning = %q, want %q", tc.table, got, tc.foreignOwner)
		}
	}
}

func TestPostgresInsertSeqLinkStatementsAreSchemaQualified(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state postgresInsertSeqState
		want  []string
	}{
		{
			name:  "held by the migrating login",
			state: postgresInsertSeqState{table: auditEventsTable, sequenceOwner: "apex-login", tableOwner: "Apex Owner", schema: `audit"ops`},
			want: []string{
				`ALTER SEQUENCE "audit""ops".audit_events_insert_seq OWNER TO "Apex Owner"`,
				`ALTER SEQUENCE "audit""ops".audit_events_insert_seq OWNED BY "audit""ops".audit_events.insert_seq`,
			},
		},
		{
			name:  "already held by the table owner",
			state: postgresInsertSeqState{table: objectActivityTable, sequenceOwner: "apexload", tableOwner: "apexload", schema: "public"},
			want:  []string{`ALTER SEQUENCE "public".object_activity_events_insert_seq OWNED BY "public".object_activity_events.insert_seq`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.state.linkStatements(); strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("link statements = %q, want %q", got, tc.want)
			}
		})
	}
}
