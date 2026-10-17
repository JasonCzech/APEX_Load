//go:build postgres_integration

package storage

import "testing"

func TestPostgresExplicitDialectUpgrade(t *testing.T) {
	store, _ := managementPostgresFixture(t)
	exerciseExplicitDialectUpgrade(t, store.SQLiteStore)
}
