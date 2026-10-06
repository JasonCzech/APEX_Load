//go:build postgres_integration

package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPostgresAuthoringFidelityLifecycle(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("unqualified: actual PostgreSQL fixture required for authoring fidelity acceptance")
	}
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	schema := fmt.Sprintf("scenario_fidelity_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
			return
		}
		var exists bool
		if err := admin.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", schema).Scan(&exists); err != nil || exists {
			t.Errorf("owned schema cleanup exists=%v err=%v", exists, err)
		}
		t.Logf("Owned PostgreSQL schema cleanup verified: %s", schema)
	})
	var version string
	if err := admin.QueryRowContext(ctx, "SHOW server_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("Actual PostgreSQL version=%s owned schema=%s", version, schema)
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("invalid PostgreSQL fixture URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", schema)
	parsed.RawQuery = query.Encode()
	runAuthoringFidelityLifecycle(t, func() storage.Store {
		store, err := storage.NewPostgresStore(parsed.String(), storage.StoreConnectionOptions{MaxOpenConns: 4}, false)
		if err != nil {
			t.Fatal(err)
		}
		return store
	})
}
