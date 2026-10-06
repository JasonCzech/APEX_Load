package storage

import (
	"time"
)

func (s *SQLiteStore) CreateAppDynamicsInspection(op *AppDynamicsInspection) error {
	_, err := s.db.Exec(`INSERT INTO appdynamics_inspections(id,project_id,connection_id,connection_revision,collector_id,provider,kind,state,payload_json,created_at,expires_at) VALUES(?,?,?,?,?,'appdynamics',?,'pending',?,?,?)`, op.ID, op.ProjectID, op.ConnectionID, op.ConnectionRevision, op.CollectorID, op.Kind, op.PayloadJSON, op.CreatedAt, op.ExpiresAt)
	return err
}
func (s *SQLiteStore) ListAppDynamicsInspections(project, collector string, limit int) ([]*AppDynamicsInspection, error) {
	return s.listProviderInspections(project, collector, "appdynamics", limit)
}
func (s *SQLiteStore) ClaimAppDynamicsInspection(id, owner string, now, until time.Time) (bool, error) {
	return s.claimProviderInspection(id, owner, "appdynamics", now, until)
}
