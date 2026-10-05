//go:build postgres_integration

package storage

import "testing"

func TestPostgresStartInitialProvenanceWinnerIntegration(t *testing.T) {
	store, _ := newPostgresScriptRevisionFixture(t)
	exerciseStartInitialProvenanceWinner(t, store)
}

func TestPostgresStartInitialProvenanceFailureIntegration(t *testing.T) {
	store, _ := newPostgresScriptRevisionFixture(t)
	exerciseStartInitialProvenanceFailure(t, store)
}

func TestPostgresStartInitialProvenanceExecutionLeaseIntegration(t *testing.T) {
	store, _ := newPostgresScriptRevisionFixture(t)
	exerciseStartInitialProvenanceExecutionLease(t, store)
}
