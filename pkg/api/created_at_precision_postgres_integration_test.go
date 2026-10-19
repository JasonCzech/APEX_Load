//go:build postgres_integration

package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// On Linux, Go's clock has nanoseconds and PostgreSQL keeps microseconds, so
// this fails there unless the controller's cached copies match what
// PostgreSQL stored.
func TestPostgresCreatedAtPrecisionAPI(t *testing.T) {
	server, store := openPostgresPrecisionAPI(t)
	exerciseCreatedAtPrecisionAPI(t, server, store.SQLiteStore)
}

// openPostgresPrecisionAPI opens a single controller on its own PostgreSQL
// schema. It does not take part in HA leadership, so it can create runs.
func openPostgresPrecisionAPI(t *testing.T) (*Server, *storage.PostgresStore) {
	t.Helper()
	store, err := storage.NewPostgresStore(newPostgresScriptAuthoringAPIFixture(t), storage.StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	t.Cleanup(func() { shutdownPostgresScriptAuthoringAPI(t, server) })
	return server, store
}
