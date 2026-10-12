//go:build postgres_integration

package storage

import "testing"

func TestPostgresProjectLiveTableDeletionOwnership(t *testing.T) {
	for _, target := range []string{"run", "project"} {
		t.Run(target, func(t *testing.T) {
			checkProjectLiveTableDeletionOwnership(t, newPostgresLiveTableTestStore(t).SQLiteStore, target)
		})
	}
}
