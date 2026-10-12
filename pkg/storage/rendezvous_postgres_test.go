//go:build postgres_integration

package storage

import "testing"

func TestPostgresRendezvousDurableOutcomeFencing(t *testing.T) {
	checkRendezvousDurableOutcomeFencing(t, newPostgresLiveTableTestStore(t).SQLiteStore)
}
