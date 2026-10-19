//go:build postgres_integration

package api

import "testing"

func TestPostgresRunLifecyclePrecisionAPI(t *testing.T) {
	server, store := openPostgresPrecisionAPI(t)
	exerciseRunLifecyclePrecisionAPI(t, server, store.SQLiteStore)
}

func TestPostgresScenarioSchedulePrecisionAPI(t *testing.T) {
	server, store := openPostgresPrecisionAPI(t)
	exerciseScenarioSchedulePrecisionAPI(t, server, store.SQLiteStore)
}

func TestPostgresExecutionPlanTimestampPrecisionAPI(t *testing.T) {
	server, store := openPostgresPrecisionAPI(t)
	exerciseExecutionPlanTimestampPrecisionAPI(t, server, store.SQLiteStore)
}

func TestPostgresRunTerminalPrecisionAPI(t *testing.T) {
	server, store := openPostgresPrecisionAPI(t)
	exerciseRunTerminalPrecisionAPI(t, server, store.SQLiteStore)
}

func TestPostgresRunArchiveLifecyclePrecisionAPI(t *testing.T) {
	_, store := openPostgresPrecisionAPI(t)
	exerciseRunArchiveLifecyclePrecisionAPI(t, store.SQLiteStore)
}
