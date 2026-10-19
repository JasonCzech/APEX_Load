//go:build postgres_integration

package api

import "testing"

// On Linux, Go's clock has nanoseconds and PostgreSQL keeps microseconds, so
// this fails there unless every write returns the version PostgreSQL stored.
func TestPostgresUpdatedAtPrecisionAPI(t *testing.T) {
	server, store := openPostgresPrecisionAPI(t)
	exerciseUpdatedAtPrecisionAPI(t, server, store.SQLiteStore)
}
