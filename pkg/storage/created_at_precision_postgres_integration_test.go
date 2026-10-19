//go:build postgres_integration

package storage

import "testing"

// On Linux, Go's clock has nanoseconds and PostgreSQL keeps microseconds, so
// these fail there unless every create path stamps what PostgreSQL stores.
func TestPostgresCreatedAtPrecision(t *testing.T) {
	for _, tc := range createdAtPrecisionExercises {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newPostgresScriptRevisionFixture(t)
			tc.run(t, store)
		})
	}
}
