package storage

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSchedulerLeaseGenerationSchemaUsesBigint(t *testing.T) {
	var createStatement string
	for _, statement := range schemaCreateTableStatements() {
		if strings.Contains(statement, "CREATE TABLE IF NOT EXISTS scheduler_leases") {
			createStatement = statement
			break
		}
	}
	if createStatement == "" {
		t.Fatal("scheduler_leases CREATE TABLE statement not found")
	}
	for name, statement := range map[string]string{
		"shared schema":      createStatement,
		"Postgres rewrite":   rewriteSQL(createStatement, storeDialectPostgres),
		"SQLite passthrough": rewriteSQL(createStatement, storeDialectSQLite),
	} {
		if !strings.Contains(statement, "generation BIGINT NOT NULL DEFAULT 1") {
			t.Errorf("%s does not declare the scheduler lease generation as BIGINT: %s", name, statement)
		}
	}

	var additiveMigration string
	for _, migration := range schemaColumnMigrations() {
		if migration.name == "scheduler_leases.generation" {
			additiveMigration = migration.query
			break
		}
	}
	if additiveMigration != `ALTER TABLE scheduler_leases ADD COLUMN generation BIGINT NOT NULL DEFAULT 1` {
		t.Fatalf("scheduler lease additive migration = %q, want BIGINT generation", additiveMigration)
	}
}

func TestPostgresSchedulerLeaseGenerationMigrationIsGuardedAndIdempotent(t *testing.T) {
	for _, fragment := range []string{
		"attrelid = 'scheduler_leases'::regclass",
		"attname = 'generation'",
		"atttypid <> 'int8'::regtype",
		"ALTER TABLE scheduler_leases ALTER COLUMN generation TYPE BIGINT",
	} {
		if !strings.Contains(postgresSchedulerLeaseGenerationBigintMigration, fragment) {
			t.Errorf("Postgres scheduler generation migration missing %q: %s", fragment, postgresSchedulerLeaseGenerationBigintMigration)
		}
	}
	if got := rewriteSQL(postgresSchedulerLeaseGenerationBigintMigration, storeDialectPostgres); got != postgresSchedulerLeaseGenerationBigintMigration {
		t.Fatalf("dialect rewrite changed native Postgres migration:\ngot:  %s\nwant: %s", got, postgresSchedulerLeaseGenerationBigintMigration)
	}
}

func TestSchedulerLeaseGenerationAdvancesOnlyOnTakeover(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	acquired, first, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-a/1", time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("first acquire = (%v, %#v, %v)", acquired, first, err)
	}
	if first.Generation != 1 {
		t.Fatalf("first generation = %d, want 1", first.Generation)
	}
	acquired, renewed, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-a/1", time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("renew = (%v, %#v, %v)", acquired, renewed, err)
	}
	if renewed.Generation != first.Generation {
		t.Fatalf("renew generation = %d, want %d", renewed.Generation, first.Generation)
	}
	if !renewed.AcquiredAt.Equal(first.AcquiredAt) {
		t.Fatalf("renew changed acquired_at from %s to %s", first.AcquiredAt, renewed.AcquiredAt)
	}
	if acquired, blocked, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-b/1", time.Now(), time.Minute); err != nil || acquired || blocked != nil {
		t.Fatalf("blocked contender = (%v, %#v, %v), want false/nil/nil", acquired, blocked, err)
	}
	if err := store.ReleaseSchedulerLease("execution", "controller-a/1"); err != nil {
		t.Fatal(err)
	}
	acquired, takeover, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-b/1", time.Now(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("takeover = (%v, %#v, %v)", acquired, takeover, err)
	}
	if takeover.Generation != 2 {
		t.Fatalf("takeover generation = %d, want 2", takeover.Generation)
	}
	// A delayed release by the old owner cannot expire the successor.
	if err := store.ReleaseSchedulerLease("execution", "controller-a/1"); err != nil {
		t.Fatal(err)
	}
	lease, err := store.GetSchedulerLease("execution")
	if err != nil {
		t.Fatal(err)
	}
	if lease.OwnerID != "controller-b/1" || lease.Generation != 2 || !lease.ExpiresAt.After(lease.UpdatedAt) {
		t.Fatalf("successor lease changed by delayed release: %#v", lease)
	}
}

func TestSchedulerLeaseGenerationOverflowFailsClosed(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if acquired, _, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-a/1", time.Now(), time.Minute); err != nil || !acquired {
		t.Fatalf("seed lease = (%v, %v)", acquired, err)
	}
	if _, err := store.db.Exec(`UPDATE scheduler_leases SET generation = ?, expires_at = ? WHERE name = ?`, int64(math.MaxInt64), time.Now().Add(-time.Minute), "execution"); err != nil {
		t.Fatal(err)
	}
	if acquired, lease, err := store.TryAcquireSchedulerLeaseTerm("execution", "controller-b/1", time.Now(), time.Minute); err == nil || acquired || lease != nil {
		t.Fatalf("overflow takeover = (%v, %#v, %v), want fail closed", acquired, lease, err)
	}
	lease, err := store.GetSchedulerLease("execution")
	if err != nil {
		t.Fatal(err)
	}
	if lease.OwnerID != "controller-a/1" || lease.Generation != math.MaxInt64 {
		t.Fatalf("overflow attempt mutated lease: %#v", lease)
	}
}
