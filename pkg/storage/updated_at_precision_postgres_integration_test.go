//go:build postgres_integration

package storage

import "testing"

// On Linux, Go's clock has nanoseconds and PostgreSQL keeps microseconds, so
// these fail there unless every write returns the version PostgreSQL stored.
func TestPostgresUpdatedAtPrecision(t *testing.T) {
	for _, tc := range updatedAtPrecisionExercises {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newPostgresScriptRevisionFixture(t)
			tc.run(t, store)
		})
	}
}
