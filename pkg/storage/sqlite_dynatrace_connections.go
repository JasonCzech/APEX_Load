package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const dynatraceConnectionColumns = `id, project_id, name, description, enabled, environment_url, platform_url,
	metrics_enabled, problems_enabled, logs_enabled, environment_token_secret, platform_auth_type, platform_token_secret,
	oauth_client_id_secret, oauth_client_secret_secret, oauth_resource, oauth_scopes_json, metric_queries_json,
	problem_selector, problem_entity_selector, log_base_filter, log_test_id_field, log_timestamp_field, log_status_field,
	log_message_field, log_trace_id_field, poll_interval_seconds, last_checked_at, last_check_status, last_check_message,
	last_capability_health_json, created_at, updated_at`

func encodeDynatraceJSON(c *DynatraceConnection) (string, string, string, error) {
	metrics, err := json.Marshal(c.MetricQueries)
	if err != nil {
		return "", "", "", err
	}
	scopes, err := json.Marshal(c.OAuthScopes)
	if err != nil {
		return "", "", "", err
	}
	health, err := json.Marshal(c.LastCapabilityHealth)
	if err != nil {
		return "", "", "", err
	}
	return string(metrics), string(scopes), string(health), nil
}

func (s *SQLiteStore) CreateDynatraceConnection(c *DynatraceConnection) error {
	if err := c.Validate(); err != nil {
		return err
	}
	metrics, scopes, health, err := encodeDynatraceJSON(c)
	if err != nil {
		return fmt.Errorf("encode Dynatrace connection: %w", err)
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	_, err = s.db.Exec(`INSERT INTO dynatrace_connections (`+dynatraceConnectionColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.ProjectID, c.Name, c.Description, c.Enabled, c.EnvironmentURL, c.PlatformURL, c.MetricsEnabled, c.ProblemsEnabled, c.LogsEnabled,
		c.EnvironmentTokenSecret, c.PlatformAuthType, c.PlatformTokenSecret, c.OAuthClientIDSecret, c.OAuthClientSecretSecret, c.OAuthResource,
		scopes, metrics, c.ProblemSelector, c.ProblemEntitySelector, c.LogBaseFilter, c.LogTestIDField, c.LogTimestampField, c.LogStatusField,
		c.LogMessageField, c.LogTraceIDField, c.PollIntervalSeconds, c.LastCheckedAt, c.LastCheckStatus, c.LastCheckMessage, health, c.CreatedAt, c.UpdatedAt)
	return err
}

func scanDynatraceConnection(row interface{ Scan(...any) error }) (*DynatraceConnection, error) {
	var c DynatraceConnection
	var scopes, metrics, health string
	err := row.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Description, &c.Enabled, &c.EnvironmentURL, &c.PlatformURL, &c.MetricsEnabled, &c.ProblemsEnabled, &c.LogsEnabled,
		&c.EnvironmentTokenSecret, &c.PlatformAuthType, &c.PlatformTokenSecret, &c.OAuthClientIDSecret, &c.OAuthClientSecretSecret, &c.OAuthResource,
		&scopes, &metrics, &c.ProblemSelector, &c.ProblemEntitySelector, &c.LogBaseFilter, &c.LogTestIDField, &c.LogTimestampField, &c.LogStatusField,
		&c.LogMessageField, &c.LogTraceIDField, &c.PollIntervalSeconds, &c.LastCheckedAt, &c.LastCheckStatus, &c.LastCheckMessage, &health, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(scopes) != "" {
		if err := json.Unmarshal([]byte(scopes), &c.OAuthScopes); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(metrics) != "" {
		if err := json.Unmarshal([]byte(metrics), &c.MetricQueries); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(health) != "" {
		if err := json.Unmarshal([]byte(health), &c.LastCapabilityHealth); err != nil {
			return nil, err
		}
	}
	if c.OAuthScopes == nil {
		c.OAuthScopes = []string{}
	}
	if c.MetricQueries == nil {
		c.MetricQueries = []DynatraceMetricQuery{}
	}
	if c.LastCapabilityHealth == nil {
		c.LastCapabilityHealth = map[string]DynatraceCapabilityHealth{}
	}
	return &c, nil
}

func (s *SQLiteStore) GetDynatraceConnection(id string) (*DynatraceConnection, error) {
	return scanDynatraceConnection(s.db.QueryRow(`SELECT `+dynatraceConnectionColumns+` FROM dynatrace_connections WHERE id=?`, strings.TrimSpace(id)))
}
func (s *SQLiteStore) ListDynatraceConnections(projectID string) ([]*DynatraceConnection, error) {
	rows, err := s.db.Query(`SELECT `+dynatraceConnectionColumns+` FROM dynatrace_connections WHERE project_id=? ORDER BY name,id`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]*DynatraceConnection, 0)
	for rows.Next() {
		c, err := scanDynatraceConnection(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *SQLiteStore) UpdateDynatraceConnectionIfUnchanged(c *DynatraceConnection, expected time.Time) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
	}
	metrics, scopes, health, err := encodeDynatraceJSON(c)
	if err != nil {
		return false, err
	}
	updated := time.Now().UTC()
	result, err := s.db.Exec(`UPDATE dynatrace_connections SET name=?,description=?,enabled=?,environment_url=?,platform_url=?,metrics_enabled=?,problems_enabled=?,logs_enabled=?,environment_token_secret=?,platform_auth_type=?,platform_token_secret=?,oauth_client_id_secret=?,oauth_client_secret_secret=?,oauth_resource=?,oauth_scopes_json=?,metric_queries_json=?,problem_selector=?,problem_entity_selector=?,log_base_filter=?,log_test_id_field=?,log_timestamp_field=?,log_status_field=?,log_message_field=?,log_trace_id_field=?,poll_interval_seconds=?,last_capability_health_json=?,updated_at=? WHERE id=? AND project_id=? AND updated_at=?`, c.Name, c.Description, c.Enabled, c.EnvironmentURL, c.PlatformURL, c.MetricsEnabled, c.ProblemsEnabled, c.LogsEnabled, c.EnvironmentTokenSecret, c.PlatformAuthType, c.PlatformTokenSecret, c.OAuthClientIDSecret, c.OAuthClientSecretSecret, c.OAuthResource, scopes, metrics, c.ProblemSelector, c.ProblemEntitySelector, c.LogBaseFilter, c.LogTestIDField, c.LogTimestampField, c.LogStatusField, c.LogMessageField, c.LogTraceIDField, c.PollIntervalSeconds, health, updated, c.ID, c.ProjectID, expected)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if count == 1 {
		c.UpdatedAt = updated
	}
	return count == 1, err
}
func (s *SQLiteStore) UpdateDynatraceConnectionHealth(id, projectID, status, message string, capabilities map[string]DynatraceCapabilityHealth, checkedAt time.Time) error {
	health, err := json.Marshal(capabilities)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`UPDATE dynatrace_connections SET last_checked_at=?,last_check_status=?,last_check_message=?,last_capability_health_json=? WHERE id=? AND project_id=?`, checkedAt, status, message, string(health), id, projectID)
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
func (s *SQLiteStore) DeleteDynatraceConnection(id, projectID string) (bool, error) {
	result, err := s.db.Exec(`DELETE FROM dynatrace_connections WHERE id=? AND project_id=?`, strings.TrimSpace(id), strings.TrimSpace(projectID))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
