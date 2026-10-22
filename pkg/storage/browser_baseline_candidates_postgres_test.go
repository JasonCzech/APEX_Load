//go:build postgres_integration

package storage

import "testing"

func TestPostgresBrowserBaselineCandidatePages(t *testing.T) {
	store, _ := managementPostgresFixture(t)
	exerciseBrowserBaselineCandidatePages(t, store.SQLiteStore)
}
