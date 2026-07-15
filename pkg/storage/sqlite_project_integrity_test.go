package storage

import (
	"testing"
	"time"
)

func requireProjectFixtures(t *testing.T, store *SQLiteStore, ids ...string) {
	t.Helper()
	now := time.Now().UTC()
	for _, id := range ids {
		if err := store.SaveProject(&Project{ID: id, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveProject(%s): %v", id, err)
		}
	}
}
