//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDynatraceDurablePostgresHeadsCursorScanAndOwnerFence(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("dynatrace_durable_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	store, err := NewPostgresStore(postgresIntegrationURLWithSearchPath(t, databaseURL, schema), StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, source, start := seedDynatraceMonitorFixture(t, store.SQLiteStore)
	checkDynatraceDurableOwnerFenceAndScan(t, store.SQLiteStore, source, start)
	checkDynatraceDurableCanonicalCursorReconciliation(t, store.SQLiteStore, source, start)
	now := time.Now().UTC()
	op := &DynatraceOperation{ID: "pg-verify", ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: 1, Kind: "verify", PayloadJSON: `{}`, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.CreateDynatraceOperation(op); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ClaimDynatraceOperation(op.ID, "pg-owner", now, now.Add(30*time.Second)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := store.CompleteDynatraceOperation(op.ID, "pg-owner", `{}`, "", now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// Simulate the published release-29 physical layout. Legacy selections and
	// archived scalar captures must survive the additive native-column upgrade.
	legacy := &TestRecord{ID: "pg-legacy-capture", ProjectID: source.ProjectID, Name: "legacy capture", Status: "COMPLETE", StartTime: start, DynatraceCapture: map[string]any{"connection_id": source.ConnectionID, "metrics": map[string]any{"cpu": 12.5}}}
	if err := store.SaveTest(legacy); err != nil {
		t.Fatal(err)
	}
	legacy, err = store.GetTest(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(legacy.DynatraceCapture)
	for _, query := range []string{`DROP TABLE dynatrace_operations`, `DROP TABLE dynatrace_scan_reservations`, `ALTER TABLE dynatrace_connections DROP COLUMN revision`, `ALTER TABLE dynatrace_connections DROP COLUMN verified_revision`, `ALTER TABLE dynatrace_connections DROP COLUMN native_config_json`, `DELETE FROM schema_migrations WHERE version>=35`} {
		if _, err := store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewPostgresStore(postgresIntegrationURLWithSearchPath(t, databaseURL, schema), StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	status, err := store.SchemaStatus()
	if err != nil || status.AppliedVersion != currentSchemaRelease().Version || status.Applied[28].Fingerprint != "ba1618a00f60ea7b120e07b375d67d8fd7c6368d1a9e8a18ef25c09f9b4c0186" {
		t.Fatal(status, err)
	}
	connection, err := store.GetDynatraceConnection(source.ConnectionID)
	if err != nil || connection.Revision != 1 || connection.VerifiedRevision != 0 || connection.CollectionMode != "direct" || connection.EnvironmentTokenSecret != "dt-private-reference" || len(connection.MetricQueries) != 1 {
		t.Fatal(connection, err)
	}
	legacy, err = store.GetTest(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(legacy.DynatraceCapture)
	if string(before) != string(after) || legacy.MonitorBindings != nil {
		t.Fatal("upgrade rewrote historical evidence or selections", string(before), string(after))
	}
}
