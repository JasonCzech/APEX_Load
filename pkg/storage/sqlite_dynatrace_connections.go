package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"strings"
	"time"
)

const dynatraceConnectionColumns = `id, project_id, name, description, enabled, environment_url, platform_url,
	metrics_enabled, problems_enabled, logs_enabled, environment_token_secret, platform_auth_type, platform_token_secret,
	oauth_client_id_secret, oauth_client_secret_secret, oauth_resource, oauth_scopes_json, metric_queries_json,
	problem_selector, problem_entity_selector, log_base_filter, log_test_id_field, log_timestamp_field, log_status_field,
	log_message_field, log_trace_id_field, poll_interval_seconds, last_checked_at, last_check_status, last_check_message,
	last_capability_health_json, created_at, updated_at,revision,verified_revision,native_config_json`

type dynatraceNativeConfig struct {
	CollectionMode            string                 `json:"collection_mode"`
	CollectorAgentID          string                 `json:"collector_agent_id,omitempty"`
	EventsEnabled             bool                   `json:"events_enabled"`
	SpansEnabled              bool                   `json:"spans_enabled"`
	GrailMetricsEnabled       bool                   `json:"grail_metrics_enabled"`
	GrailEventsEnabled        bool                   `json:"grail_events_enabled"`
	TopologyMode              string                 `json:"topology_mode,omitempty"`
	RequestsPerMinute         int                    `json:"requests_per_minute,omitempty"`
	PlatformRequestsPerMinute int                    `json:"platform_requests_per_minute,omitempty"`
	MaxScanBytes              int64                  `json:"max_scan_bytes,omitempty"`
	MaxRunScanBytes           int64                  `json:"max_run_scan_bytes,omitempty"`
	DrainReserveBytes         int64                  `json:"drain_reserve_bytes,omitempty"`
	Capabilities              dynatrace.Capabilities `json:"capabilities"`
}

func encodeDynatraceNative(c *DynatraceConnection) (string, error) {
	b, e := json.Marshal(dynatraceNativeConfig{c.CollectionMode, c.CollectorAgentID, c.EventsEnabled, c.SpansEnabled, c.GrailMetricsEnabled, c.GrailEventsEnabled, c.TopologyMode, c.RequestsPerMinute, c.PlatformRequestsPerMinute, c.MaxScanBytes, c.MaxRunScanBytes, c.DrainReserveBytes, c.Capabilities})
	return string(b), e
}
func decodeDynatraceNative(c *DynatraceConnection, body string) error {
	var n dynatraceNativeConfig
	if err := json.Unmarshal([]byte(body), &n); err != nil {
		return err
	}
	c.CollectionMode = n.CollectionMode
	if c.CollectionMode == "" {
		c.CollectionMode = "direct"
	}
	c.CollectorAgentID = n.CollectorAgentID
	c.EventsEnabled = n.EventsEnabled
	c.SpansEnabled = n.SpansEnabled
	c.GrailMetricsEnabled = n.GrailMetricsEnabled
	c.GrailEventsEnabled = n.GrailEventsEnabled
	c.TopologyMode = n.TopologyMode
	c.RequestsPerMinute = n.RequestsPerMinute
	c.PlatformRequestsPerMinute = n.PlatformRequestsPerMinute
	c.MaxScanBytes = n.MaxScanBytes
	c.MaxRunScanBytes = n.MaxRunScanBytes
	c.DrainReserveBytes = n.DrainReserveBytes
	c.Capabilities = n.Capabilities
	return nil
}

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
	c.Revision = 1
	c.VerifiedRevision = 0
	native, err := encodeDynatraceNative(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO dynatrace_connections (`+dynatraceConnectionColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.ProjectID, c.Name, c.Description, c.Enabled, c.EnvironmentURL, c.PlatformURL, c.MetricsEnabled, c.ProblemsEnabled, c.LogsEnabled,
		c.EnvironmentTokenSecret, c.PlatformAuthType, c.PlatformTokenSecret, c.OAuthClientIDSecret, c.OAuthClientSecretSecret, c.OAuthResource,
		scopes, metrics, c.ProblemSelector, c.ProblemEntitySelector, c.LogBaseFilter, c.LogTestIDField, c.LogTimestampField, c.LogStatusField,
		c.LogMessageField, c.LogTraceIDField, c.PollIntervalSeconds, c.LastCheckedAt, c.LastCheckStatus, c.LastCheckMessage, health, c.CreatedAt, c.UpdatedAt, c.Revision, c.VerifiedRevision, native)
	return err
}

func scanDynatraceConnection(row interface{ Scan(...any) error }) (*DynatraceConnection, error) {
	var c DynatraceConnection
	var scopes, metrics, health, native string
	err := row.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Description, &c.Enabled, &c.EnvironmentURL, &c.PlatformURL, &c.MetricsEnabled, &c.ProblemsEnabled, &c.LogsEnabled,
		&c.EnvironmentTokenSecret, &c.PlatformAuthType, &c.PlatformTokenSecret, &c.OAuthClientIDSecret, &c.OAuthClientSecretSecret, &c.OAuthResource,
		&scopes, &metrics, &c.ProblemSelector, &c.ProblemEntitySelector, &c.LogBaseFilter, &c.LogTestIDField, &c.LogTimestampField, &c.LogStatusField,
		&c.LogMessageField, &c.LogTraceIDField, &c.PollIntervalSeconds, &c.LastCheckedAt, &c.LastCheckStatus, &c.LastCheckMessage, &health, &c.CreatedAt, &c.UpdatedAt, &c.Revision, &c.VerifiedRevision, &native)
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
	if err = decodeDynatraceNative(&c, native); err != nil {
		return nil, err
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
	return s.updateDynatraceConnection(c, 0, &expected)
}
func (s *SQLiteStore) UpdateDynatraceConnectionIfRevision(c *DynatraceConnection, expected int64) (bool, error) {
	if expected < 1 {
		return false, ErrMonitorRevision
	}
	return s.updateDynatraceConnection(c, expected, nil)
}
func (s *SQLiteStore) updateDynatraceConnection(c *DynatraceConnection, expectedRevision int64, expectedTime *time.Time) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, c.ProjectID); err != nil {
		return false, err
	}
	prior, err := scanDynatraceConnection(tx.QueryRow(`SELECT `+dynatraceConnectionColumns+` FROM dynatrace_connections WHERE id=? AND project_id=?`, c.ID, c.ProjectID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if expectedRevision > 0 && prior.Revision != expectedRevision || expectedTime != nil && !prior.UpdatedAt.Equal(*expectedTime) {
		return false, nil
	}
	c.Revision = prior.Revision + 1
	c.CreatedAt = prior.CreatedAt
	if SameDynatraceIdentity(prior, c) {
		c.VerifiedRevision = 0
		if prior.VerifiedRevision == prior.Revision {
			c.VerifiedRevision = c.Revision
		}
		c.LastCheckStatus = prior.LastCheckStatus
		c.LastCheckedAt = prior.LastCheckedAt
		c.LastCheckMessage = prior.LastCheckMessage
		c.Capabilities = prior.Capabilities
		c.LastCapabilityHealth = prior.LastCapabilityHealth
	} else {
		c.VerifiedRevision = 0
		c.LastCheckStatus = "configuration_changed"
		c.LastCheckedAt = nil
		c.LastCheckMessage = "Verify the changed connection before the next run"
		c.Capabilities = dynatrace.Capabilities{}
		c.LastCapabilityHealth = map[string]DynatraceCapabilityHealth{}
	}
	metrics, scopes, health, err := encodeDynatraceJSON(c)
	if err != nil {
		return false, err
	}
	native, err := encodeDynatraceNative(c)
	if err != nil {
		return false, err
	}
	updated := time.Now().UTC()
	result, err := tx.Exec(`UPDATE dynatrace_connections SET name=?,description=?,enabled=?,environment_url=?,platform_url=?,metrics_enabled=?,problems_enabled=?,logs_enabled=?,environment_token_secret=?,platform_auth_type=?,platform_token_secret=?,oauth_client_id_secret=?,oauth_client_secret_secret=?,oauth_resource=?,oauth_scopes_json=?,metric_queries_json=?,problem_selector=?,problem_entity_selector=?,log_base_filter=?,log_test_id_field=?,log_timestamp_field=?,log_status_field=?,log_message_field=?,log_trace_id_field=?,poll_interval_seconds=?,last_capability_health_json=?,updated_at=?,revision=?,verified_revision=?,native_config_json=?,last_checked_at=?,last_check_status=?,last_check_message=? WHERE id=? AND project_id=? AND revision=?`, c.Name, c.Description, c.Enabled, c.EnvironmentURL, c.PlatformURL, c.MetricsEnabled, c.ProblemsEnabled, c.LogsEnabled, c.EnvironmentTokenSecret, c.PlatformAuthType, c.PlatformTokenSecret, c.OAuthClientIDSecret, c.OAuthClientSecretSecret, c.OAuthResource, scopes, metrics, c.ProblemSelector, c.ProblemEntitySelector, c.LogBaseFilter, c.LogTestIDField, c.LogTimestampField, c.LogStatusField, c.LogMessageField, c.LogTraceIDField, c.PollIntervalSeconds, health, updated, c.Revision, c.VerifiedRevision, native, c.LastCheckedAt, c.LastCheckStatus, c.LastCheckMessage, c.ID, c.ProjectID, prior.Revision)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if count == 1 {
		c.UpdatedAt = updated
	}
	if err != nil {
		return false, err
	}
	if count == 1 {
		if _, err = cancelMonitorPreparationsTx(tx, c.ProjectID, func(p *Preparation) bool {
			if p.Monitors == nil {
				return false
			}
			for _, snapshot := range p.Monitors.DynatraceConnections {
				if snapshot != nil && snapshot.ConnectionID == c.ID {
					return true
				}
			}
			return false
		}); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return count == 1, nil
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

func (s *SQLiteStore) UpdateDynatraceConnectionHealthIfRevision(id, project string, expected int64, status, message string, caps dynatrace.Capabilities, at time.Time) (bool, error) {
	return s.UpdateDynatraceConnectionHealthIfRevisionFenced(id, project, expected, status, message, caps, at, "", "", 0)
}

// UpdateDynatraceConnectionHealthIfRevisionFenced rejects results from a
// superseded direct executor even when the connection revision is unchanged.
func (s *SQLiteStore) UpdateDynatraceConnectionHealthIfRevisionFenced(id, project string, expected int64, status, message string, caps dynatrace.Capabilities, at time.Time, leaseName, leaseOwner string, generation uint64) (bool, error) {
	if expected < 1 {
		return false, ErrMonitorRevision
	}
	c, err := s.GetDynatraceConnection(id)
	if err != nil {
		return false, err
	}
	if c.ProjectID != project || c.Revision != expected {
		return false, nil
	}
	c.Capabilities = caps
	c.VerifiedRevision = 0
	if status == "connected" {
		c.VerifiedRevision = expected
	}
	if len(message) > 1024 {
		message = message[:1024]
	}
	native, err := encodeDynatraceNative(c)
	if err != nil {
		return false, err
	}
	health := map[string]DynatraceCapabilityHealth{}
	for k, v := range caps.Status {
		health[k] = DynatraceCapabilityHealth{Status: v}
	}
	body, err := json.Marshal(health)
	if err != nil {
		return false, err
	}
	var tx *storeTx
	if leaseName != "" {
		tx, err = s.beginExecutionLeaseTermTransaction(leaseName, leaseOwner, generation)
		if err == nil && tx == nil {
			return false, errors.New("monitor execution owner fence rejected")
		}
	} else {
		tx, err = s.db.Begin()
	}
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE dynatrace_connections SET verified_revision=?,native_config_json=?,last_checked_at=?,last_check_status=?,last_check_message=?,last_capability_health_json=? WHERE id=? AND project_id=? AND revision=?`, c.VerifiedRevision, native, at.UTC(), status, message, string(body), id, project, expected)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, tx.Commit()
}
func (s *SQLiteStore) DeleteDynatraceConnection(id, projectID string) (bool, error) {
	result, err := s.db.Exec(`DELETE FROM dynatrace_connections WHERE id=? AND project_id=?`, strings.TrimSpace(id), strings.TrimSpace(projectID))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
func (s *SQLiteStore) DeleteDynatraceConnectionIfRevision(id, project string, expected int64) (bool, error) {
	if expected < 1 {
		return false, ErrMonitorRevision
	}
	tx, e := s.db.Begin()
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	if _, e = lockProjectForOwnedMutation(tx, project); e != nil {
		return false, e
	}
	r, e := tx.Exec(`DELETE FROM dynatrace_connections WHERE id=? AND project_id=? AND revision=?`, id, project, expected)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return false, e
	}
	if n == 1 {
		if _, e = cancelMonitorPreparationsTx(tx, project, func(p *Preparation) bool {
			if p.Monitors == nil {
				return false
			}
			for _, snapshot := range p.Monitors.DynatraceConnections {
				if snapshot != nil && snapshot.ConnectionID == id {
					return true
				}
			}
			return false
		}); e != nil {
			return false, e
		}
	}
	return n == 1, tx.Commit()
}
