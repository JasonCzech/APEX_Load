//go:build postgres_integration

package storage

import "testing"

func TestPostgresProjectLiveTableOperatorCommitIsAtomic(t *testing.T) {
	checkProjectLiveTableOperatorCommitIsAtomic(t, newPostgresLiveTableTestStore(t).SQLiteStore)
}
