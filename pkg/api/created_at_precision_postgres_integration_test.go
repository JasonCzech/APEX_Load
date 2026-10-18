//go:build postgres_integration

package api

import "testing"

// On Linux, Go's clock has nanoseconds and PostgreSQL keeps microseconds, so
// this fails there unless the controller's cached copies match what
// PostgreSQL stored.
func TestPostgresCreatedAtPrecisionAPI(t *testing.T) {
	server, store := openPostgresScriptAuthoringAPI(t, newPostgresScriptAuthoringAPIFixture(t))
	exerciseCreatedAtPrecisionAPI(t, server, store.SQLiteStore)
}
