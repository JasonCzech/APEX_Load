package storage

import (
	"strings"
)

func scanAppDynamicsConnection(row interface{ Scan(...any) error }) (*AppDynamicsConnection, error) {
	var data string
	var rev, verified int64
	if err := row.Scan(&data, &rev, &verified); err != nil {
		return nil, err
	}
	c, err := UnmarshalAppDynamicsConnectionPrivate([]byte(data))
	if err == nil {
		c.Revision = rev
		c.VerifiedRevision = verified
	}
	return c, err
}
func (s *SQLiteStore) GetAppDynamicsConnection(id string) (*AppDynamicsConnection, error) {
	return scanAppDynamicsConnection(s.db.QueryRow(`SELECT payload_json,revision,verified_revision FROM appdynamics_connections WHERE id=?`, strings.TrimSpace(id)))
}
func (s *SQLiteStore) ListAppDynamicsConnections(projectID string) ([]*AppDynamicsConnection, error) {
	rows, err := s.db.Query(`SELECT payload_json,revision,verified_revision FROM appdynamics_connections WHERE project_id=? ORDER BY name,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []*AppDynamicsConnection{}
	for rows.Next() {
		c, err := scanAppDynamicsConnection(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
