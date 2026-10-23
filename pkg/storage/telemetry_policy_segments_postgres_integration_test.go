//go:build postgres_integration

package storage

import "testing"

func TestPostgresTelemetryPolicySegmentsAppendOnly(t *testing.T) {
	exerciseTelemetryPolicySegments(t, newPostgresLiveTableTestStore(t).SQLiteStore)
}
