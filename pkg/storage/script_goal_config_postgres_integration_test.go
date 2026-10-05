//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestPostgresScriptGoalConfigMigrationWritersAndReopen(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("apexload_script_goals_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	url := postgresIntegrationURLWithSearchPath(t, databaseURL, schema)
	store, err := NewPostgresStore(url, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &dsl.Script{ID: "legacy-goals", Name: "Legacy", ProjectID: DefaultProjectID}
	if err := store.SaveScript(legacy); err != nil {
		store.Close()
		t.Fatal(err)
	}
	before, err := store.SchemaStatus()
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := removeScriptRevisionTrackingForTest(store.SQLiteStore); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`ALTER TABLE scripts DROP COLUMN content_revision`, `ALTER TABLE scripts DROP COLUMN goal_config_json`, `DELETE FROM schema_migrations WHERE version>=36`} {
		if _, err := store.db.Exec(query); err != nil {
			store.Close()
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewPostgresStore(url, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.SchemaStatus()
	if err != nil || after.AppliedVersion != currentSchemaRelease().Version || !reflect.DeepEqual(before.Applied[:35], after.Applied[:35]) {
		store.Close()
		t.Fatalf("published ledger changed: %#v, %v", after, err)
	}
	requireScriptGoals(t, store.SQLiteStore, legacy.ID, nil)
	var raw *string
	if err := store.db.QueryRow(`SELECT goal_config_json FROM scripts WHERE id=?`, legacy.ID).Scan(&raw); err != nil || raw != nil {
		store.Close()
		t.Fatalf("legacy goals = %#v, %v; want SQL NULL", raw, err)
	}
	exerciseScriptGoalConfigWriters(t, store.SQLiteStore)
	exerciseScriptGoalConfigGitHub(t, store.SQLiteStore)
	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "project-bruno", Name: "Bruno", CreatedAt: now, UpdatedAt: now}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.SaveFolder(&Folder{ID: "native-folder", ProjectID: "project-bruno", Name: "Imports", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	exerciseScriptGoalConfigBruno(t, store.SQLiteStore)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewPostgresStore(url, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	requireScriptGoals(t, store.SQLiteStore, "goals-create-only", scriptGoalsFixture())
	requireScriptGoals(t, store.SQLiteStore, "github-goals", nil)
	requireScriptGoals(t, store.SQLiteStore, "native-script-1", scriptGoalsFixture())
}
