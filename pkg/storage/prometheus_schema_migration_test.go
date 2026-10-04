package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPrometheusSchemaUpgradePreservesAppDynamicsInspectionJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v29.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, now := seedMonitorFixture(t, s)
	op := &AppDynamicsInspection{ID: "old-appd-op", ProjectID: "monitor-p", ConnectionID: "appd", ConnectionRevision: 1, Kind: "test", PayloadJSON: `{}`, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = s.CreateAppDynamicsInspection(op); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DROP TABLE prometheus_connections`, `ALTER TABLE appdynamics_inspections DROP COLUMN provider`, `DELETE FROM schema_migrations WHERE version>=30`} {
		if _, err = s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err := s.SchemaStatus()
	if err != nil || status.AppliedVersion != 30 {
		t.Fatal(status, err)
	}
	old, err := s.GetAppDynamicsInspection(op.ID)
	if err != nil || old.Provider != "appdynamics" || old.PayloadJSON != op.PayloadJSON {
		t.Fatal(old, err)
	}
	if _, err = s.GetPrometheusInspection(op.ID); err == nil {
		t.Fatal("legacy AppDynamics operation reassigned")
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM prometheus_connections`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
