package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const elkConnectionColumns = `id, project_id, name, description, enabled, endpoint, index_pattern,
	timestamp_field, level_field, message_field, test_id_field, trace_id_field, base_filter, error_filter,
	auth_type, api_key_secret_name, username_secret_name, password_secret_name, metric_aggregations_json,
	last_checked_at, last_check_status, last_check_message, created_at, updated_at`

func (s *SQLiteStore) CreateELKConnection(connection *ELKConnection) error {
	if err := connection.Validate(); err != nil {
		return err
	}
	metrics, err := json.Marshal(connection.MetricAggregations)
	if err != nil {
		return fmt.Errorf("encode ELK metric aggregations: %w", err)
	}
	now := time.Now().UTC()
	if connection.CreatedAt.IsZero() {
		connection.CreatedAt = now
	}
	if connection.UpdatedAt.IsZero() {
		connection.UpdatedAt = now
	}
	_, err = s.db.Exec(`INSERT INTO elk_connections (`+elkConnectionColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		connection.ID, connection.ProjectID, connection.Name, connection.Description, connection.Enabled, connection.Endpoint,
		connection.IndexPattern, connection.TimestampField, connection.LevelField, connection.MessageField, connection.TestIDField,
		connection.TraceIDField, connection.BaseFilter, connection.ErrorFilter, connection.AuthType, connection.APIKeySecretName,
		connection.UsernameSecretName, connection.PasswordSecretName, string(metrics), connection.LastCheckedAt,
		connection.LastCheckStatus, connection.LastCheckMessage, connection.CreatedAt, connection.UpdatedAt)
	return err
}

func scanELKConnection(row interface{ Scan(...any) error }) (*ELKConnection, error) {
	var connection ELKConnection
	var metrics string
	err := row.Scan(&connection.ID, &connection.ProjectID, &connection.Name, &connection.Description, &connection.Enabled,
		&connection.Endpoint, &connection.IndexPattern, &connection.TimestampField, &connection.LevelField, &connection.MessageField,
		&connection.TestIDField, &connection.TraceIDField, &connection.BaseFilter, &connection.ErrorFilter, &connection.AuthType,
		&connection.APIKeySecretName, &connection.UsernameSecretName, &connection.PasswordSecretName, &metrics,
		&connection.LastCheckedAt, &connection.LastCheckStatus, &connection.LastCheckMessage, &connection.CreatedAt, &connection.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(metrics) != "" {
		if err := json.Unmarshal([]byte(metrics), &connection.MetricAggregations); err != nil {
			return nil, err
		}
	}
	if connection.MetricAggregations == nil {
		connection.MetricAggregations = []ELKMetricAggregation{}
	}
	return &connection, nil
}

func (s *SQLiteStore) GetELKConnection(id string) (*ELKConnection, error) {
	return scanELKConnection(s.db.QueryRow(`SELECT `+elkConnectionColumns+` FROM elk_connections WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) ListELKConnections(projectID string) ([]*ELKConnection, error) {
	rows, err := s.db.Query(`SELECT `+elkConnectionColumns+` FROM elk_connections WHERE project_id = ? ORDER BY name, id`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]*ELKConnection, 0)
	for rows.Next() {
		connection, err := scanELKConnection(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, connection)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) UpdateELKConnectionIfUnchanged(connection *ELKConnection, expected time.Time) (bool, error) {
	if err := connection.Validate(); err != nil {
		return false, err
	}
	metrics, err := json.Marshal(connection.MetricAggregations)
	if err != nil {
		return false, err
	}
	updatedAt := time.Now().UTC()
	result, err := s.db.Exec(`UPDATE elk_connections SET name=?, description=?, enabled=?, endpoint=?, index_pattern=?,
		timestamp_field=?, level_field=?, message_field=?, test_id_field=?, trace_id_field=?, base_filter=?, error_filter=?,
		auth_type=?, api_key_secret_name=?, username_secret_name=?, password_secret_name=?, metric_aggregations_json=?, updated_at=?
		WHERE id=? AND project_id=? AND updated_at=?`, connection.Name, connection.Description, connection.Enabled, connection.Endpoint,
		connection.IndexPattern, connection.TimestampField, connection.LevelField, connection.MessageField, connection.TestIDField,
		connection.TraceIDField, connection.BaseFilter, connection.ErrorFilter, connection.AuthType, connection.APIKeySecretName,
		connection.UsernameSecretName, connection.PasswordSecretName, string(metrics), updatedAt, connection.ID, connection.ProjectID, expected)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if count == 1 {
		connection.UpdatedAt = updatedAt
	}
	return count == 1, err
}

func (s *SQLiteStore) UpdateELKConnectionHealth(id, projectID, status, message string, checkedAt time.Time) error {
	result, err := s.db.Exec(`UPDATE elk_connections SET last_checked_at=?, last_check_status=?, last_check_message=? WHERE id=? AND project_id=?`, checkedAt, status, message, id, projectID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) DeleteELKConnection(id, projectID string) (bool, error) {
	result, err := s.db.Exec(`DELETE FROM elk_connections WHERE id=? AND project_id=?`, strings.TrimSpace(id), strings.TrimSpace(projectID))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "foreign key") {
			return false, errors.New("ELK connection is referenced by a test")
		}
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
