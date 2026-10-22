//go:build postgres_integration

package storage

import "testing"

func TestPostgresBrowserAcceptancePromotionFence(t *testing.T) {
	first, dsn := managementPostgresFixture(t)
	second, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	exerciseBrowserAcceptanceConcurrentWithdrawal(t, first.SQLiteStore, second.SQLiteStore)
}
