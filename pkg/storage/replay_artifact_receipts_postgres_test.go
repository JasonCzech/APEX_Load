//go:build postgres_integration

package storage

import "testing"

func TestPostgresReplayArtifactReceipts(t *testing.T) {
	first, dsn := managementPostgresFixture(t)
	second, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	exerciseReplayArtifactReceipts(t, first.SQLiteStore, second.SQLiteStore)
}

func TestPostgresReplayArtifactConcurrentAdmission(t *testing.T) {
	first, dsn := managementPostgresFixture(t)
	second, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	exerciseReplayArtifactConcurrentAdmission(t, first.SQLiteStore, second.SQLiteStore)
}

func TestPostgresReplayArtifactProjectDeletion(t *testing.T) {
	first, dsn := managementPostgresFixture(t)
	second, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	exerciseReplayArtifactProjectDeletion(t, first.SQLiteStore, second.SQLiteStore)
}
