//go:build postgres_integration

package storage

import "testing"

func TestPostgresProjectLiveTablePersistenceLifecycle(t *testing.T) {
	checkProjectLiveTablePersistenceLifecycle(t, newPostgresLiveTableTestStore(t).SQLiteStore)
}
