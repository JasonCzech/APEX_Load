//go:build postgres_integration

package storage

import "testing"

func TestPostgresManagementLegacyServiceBusCadenceRemainsReadableAndRevisioned(t *testing.T) {
	pg, _ := managementPostgresFixture(t)
	seedMonitorFixture(t, pg.SQLiteStore)
	assertLegacyServiceBusCadence(t, pg.SQLiteStore)
}
