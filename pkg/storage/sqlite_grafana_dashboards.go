package storage

// SQLite store — imported Grafana dashboards. Same SQL serves PostgreSQL via
// rewriteSQL; the `enabled INTEGER DEFAULT 1` DDL and `COALESCE(enabled, 1)` reads are
// written byte-for-byte to match the rewriter's boolean-column entries.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/grafana"
)

func (s *SQLiteStore) SaveGrafanaDashboard(d *GrafanaDashboard) error {
	if d == nil {
		return fmt.Errorf("grafana dashboard is required")
	}
	if strings.TrimSpace(d.ID) == "" {
		return errors.New("grafana dashboard id is required")
	}
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("grafana dashboard name is required")
	}
	if len(strings.TrimSpace(d.Name)) > MaxGrafanaDashboardNameBytes {
		return fmt.Errorf("grafana dashboard name exceeds %d bytes", MaxGrafanaDashboardNameBytes)
	}
	if len(d.Description) > MaxGrafanaDashboardDescriptionBytes {
		return fmt.Errorf("grafana dashboard description exceeds %d bytes", MaxGrafanaDashboardDescriptionBytes)
	}
	if len(d.SourceTitle) > grafana.MaxTitleBytes || len(d.SourceUID) > grafana.MaxSourceUIDBytes {
		return errors.New("grafana dashboard source identity exceeds its size limit")
	}
	if len(d.Panels) == 0 || len(d.Panels) > grafana.MaxPanels {
		return fmt.Errorf("grafana dashboard must contain 1-%d panels", grafana.MaxPanels)
	}
	if (strings.TrimSpace(d.ImportSessionID) == "") != (strings.TrimSpace(d.ImportClaimToken) == "") {
		return errors.New("grafana import session id and claim token must be provided together")
	}
	panelIDs := make(map[int]struct{}, len(d.Panels))
	totalTargets := 0
	for _, panel := range d.Panels {
		if panel.PanelID <= 0 {
			return errors.New("grafana dashboard panels require positive ids")
		}
		if _, exists := panelIDs[panel.PanelID]; exists {
			return fmt.Errorf("grafana dashboard panel id %d is duplicated", panel.PanelID)
		}
		panelIDs[panel.PanelID] = struct{}{}
		if panel.Kind != grafana.PanelKindLine && panel.Kind != grafana.PanelKindSingleValue {
			return fmt.Errorf("grafana dashboard panel %d has unsupported kind %q", panel.PanelID, panel.Kind)
		}
		if len(panel.Title) > grafana.MaxTitleBytes || len(panel.RowTitle) > grafana.MaxTitleBytes || len(panel.DatasourceUID) > grafana.MaxDatasourceBytes || len(panel.Unit) > grafana.MaxUnitIDBytes {
			return fmt.Errorf("grafana dashboard panel %d metadata exceeds its size limit", panel.PanelID)
		}
		if len(panel.Targets) == 0 || len(panel.Targets) > grafana.MaxTargetsPerPanel {
			return fmt.Errorf("grafana dashboard panel %d must contain 1-%d targets", panel.PanelID, grafana.MaxTargetsPerPanel)
		}
		if panel.Kind == grafana.PanelKindSingleValue && len(panel.Targets) != 1 {
			return fmt.Errorf("grafana dashboard single-value panel %d must contain exactly one target", panel.PanelID)
		}
		totalTargets += len(panel.Targets)
		if totalTargets > grafana.MaxQueriesTotal {
			return fmt.Errorf("grafana dashboard exceeds the limit of %d total queries", grafana.MaxQueriesTotal)
		}
		for _, target := range panel.Targets {
			if len(target.Expr) == 0 || len(target.Expr) > grafana.MaxExprBytes || len(target.RefID) > grafana.MaxRefIDBytes || len(target.LegendFormat) > grafana.MaxLegendBytes {
				return fmt.Errorf("grafana dashboard panel %d target exceeds its size limit", panel.PanelID)
			}
		}
	}
	d.ProjectID = normalizeProjectID(d.ProjectID)
	now := time.Now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now

	panelsJSON, err := marshalJSONColumn(d.Panels, "[]")
	if err != nil {
		return fmt.Errorf("marshal panels: %w", err)
	}
	bindingsJSON, err := marshalJSONColumn(d.Bindings, "")
	if err != nil {
		return fmt.Errorf("marshal bindings: %w", err)
	}
	diagnosticsJSON, err := marshalJSONColumn(d.Diagnostics, "")
	if err != nil {
		return fmt.Errorf("marshal diagnostics: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if d.ProjectID, err = lockProjectForOwnedMutation(tx, d.ProjectID); err != nil {
		return err
	}
	if d.ImportSessionID != "" {
		var claimToken string
		var expiresAt time.Time
		if err := tx.QueryRow(`SELECT COALESCE(claim_token, ''), expires_at FROM grafana_import_sessions
			WHERE id = ? AND project_id = ?`, strings.TrimSpace(d.ImportSessionID), d.ProjectID).Scan(&claimToken, &expiresAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrGrafanaImportClaimLost
			}
			return err
		}
		if claimToken != strings.TrimSpace(d.ImportClaimToken) || !expiresAt.After(now) {
			return ErrGrafanaImportClaimLost
		}
	}
	// Cross-project takeover guard: a row that already exists under a different
	// project must not be overwritten by a same-id save from another project.
	var currentProjectID string
	err = tx.QueryRow(`SELECT project_id FROM grafana_dashboards WHERE id = ?`, d.ID).Scan(&currentProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(currentProjectID) != d.ProjectID {
		return fmt.Errorf("grafana dashboard %q belongs to another project", d.ID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM grafana_dashboards WHERE project_id = ?`, d.ProjectID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxGrafanaDashboardsPerProject {
			return fmt.Errorf("%w: project already has the maximum of %d imported Grafana dashboards", ErrGrafanaDashboardLimit, MaxGrafanaDashboardsPerProject)
		}
	}
	result, err := tx.Exec(`INSERT INTO grafana_dashboards (
			id, project_id, name, description, source_title, source_uid, source_sha256,
			schema_version, enabled, panels_json, bindings_json, diagnostics_json,
			created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, description=excluded.description,
			source_title=excluded.source_title, source_uid=excluded.source_uid, source_sha256=excluded.source_sha256,
			schema_version=excluded.schema_version, enabled=excluded.enabled,
			panels_json=excluded.panels_json, bindings_json=excluded.bindings_json, diagnostics_json=excluded.diagnostics_json,
			updated_at=excluded.updated_at
		WHERE grafana_dashboards.project_id = excluded.project_id`,
		d.ID, d.ProjectID, d.Name, d.Description, d.SourceTitle, d.SourceUID, d.SourceSHA256,
		d.SchemaVersion, d.Enabled, panelsJSON, bindingsJSON, diagnosticsJSON,
		d.CreatedBy, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("grafana dashboard %q belongs to another project", d.ID)
	}
	if d.ImportSessionID != "" {
		result, err := tx.Exec(`DELETE FROM grafana_import_sessions WHERE id = ? AND project_id = ? AND claim_token = ?`,
			strings.TrimSpace(d.ImportSessionID), d.ProjectID, strings.TrimSpace(d.ImportClaimToken))
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return ErrGrafanaImportClaimLost
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) UpdateGrafanaDashboardIfUnchanged(d *GrafanaDashboard, expectedUpdatedAt time.Time) (bool, error) {
	if d == nil || strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Name) == "" || expectedUpdatedAt.IsZero() {
		return false, errors.New("grafana dashboard id, name, and expected updated_at are required")
	}
	name := strings.TrimSpace(d.Name)
	if len(name) > MaxGrafanaDashboardNameBytes || len(d.Description) > MaxGrafanaDashboardDescriptionBytes {
		return false, errors.New("grafana dashboard metadata exceeds its size limit")
	}
	projectID := normalizeProjectID(d.ProjectID)
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return false, err
	}
	var current time.Time
	var currentRaw string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM grafana_dashboards WHERE id = ? AND project_id = ?`, d.ID, projectID).Scan(&current, &currentRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !current.Equal(expectedUpdatedAt) {
		return false, nil
	}
	now := time.Now()
	if !now.After(current) {
		now = current.Add(time.Microsecond)
	}
	result, err := tx.Exec(`UPDATE grafana_dashboards SET name = ?, description = ?, enabled = ?, updated_at = ?
		WHERE id = ? AND project_id = ? AND CAST(updated_at AS TEXT) = ?`,
		name, strings.TrimSpace(d.Description), d.Enabled, now, d.ID, projectID, currentRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	d.Name = name
	d.Description = strings.TrimSpace(d.Description)
	d.ProjectID = projectID
	d.UpdatedAt = now
	return true, nil
}

const grafanaDashboardSelect = `SELECT id, COALESCE(project_id, ''), name, COALESCE(description, ''),
	COALESCE(source_title, ''), COALESCE(source_uid, ''), COALESCE(source_sha256, ''),
	COALESCE(schema_version, 0), COALESCE(enabled, 1),
	COALESCE(panels_json, ''), COALESCE(bindings_json, ''), COALESCE(diagnostics_json, ''),
	COALESCE(created_by, ''), created_at, updated_at FROM grafana_dashboards`

func scanGrafanaDashboard(row capacityRowScanner) (*GrafanaDashboard, error) {
	var d GrafanaDashboard
	var enabled any
	var panelsJSON, bindingsJSON, diagnosticsJSON string
	if err := row.Scan(&d.ID, &d.ProjectID, &d.Name, &d.Description,
		&d.SourceTitle, &d.SourceUID, &d.SourceSHA256,
		&d.SchemaVersion, &enabled,
		&panelsJSON, &bindingsJSON, &diagnosticsJSON,
		&d.CreatedBy, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.Enabled = sqlBool(enabled)
	if panelsJSON != "" && panelsJSON != "null" {
		if err := json.Unmarshal([]byte(panelsJSON), &d.Panels); err != nil {
			return nil, fmt.Errorf("unmarshal panels: %w", err)
		}
	}
	if d.Panels == nil {
		d.Panels = []grafana.Panel{}
	}
	if bindingsJSON != "" && bindingsJSON != "null" {
		if err := json.Unmarshal([]byte(bindingsJSON), &d.Bindings); err != nil {
			return nil, fmt.Errorf("unmarshal bindings: %w", err)
		}
	}
	if diagnosticsJSON != "" && diagnosticsJSON != "null" {
		if err := json.Unmarshal([]byte(diagnosticsJSON), &d.Diagnostics); err != nil {
			return nil, fmt.Errorf("unmarshal diagnostics: %w", err)
		}
	}
	return &d, nil
}

func (s *SQLiteStore) GetGrafanaDashboard(id string) (*GrafanaDashboard, error) {
	return scanGrafanaDashboard(s.db.QueryRow(grafanaDashboardSelect+` WHERE id = ?`, id))
}

func (s *SQLiteStore) ListGrafanaDashboards(projectID string, limit int, offset int) ([]*GrafanaDashboard, error) {
	query := grafanaDashboardSelect
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dashboards := make([]*GrafanaDashboard, 0)
	for rows.Next() {
		d, err := scanGrafanaDashboard(rows)
		if err != nil {
			return nil, err
		}
		dashboards = append(dashboards, d)
	}
	return dashboards, rows.Err()
}

func (s *SQLiteStore) DeleteGrafanaDashboardIfUnchanged(id, projectID string, expectedUpdatedAt time.Time) (bool, error) {
	if strings.TrimSpace(id) == "" || expectedUpdatedAt.IsZero() {
		return false, errors.New("grafana dashboard id and expected updated_at are required")
	}
	projectID = normalizeProjectID(projectID)
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return false, err
	}
	var current time.Time
	var currentRaw string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM grafana_dashboards WHERE id = ? AND project_id = ?`, id, projectID).Scan(&current, &currentRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !current.Equal(expectedUpdatedAt) {
		return false, nil
	}
	result, err := tx.Exec(`DELETE FROM grafana_dashboards WHERE id = ? AND project_id = ? AND CAST(updated_at AS TEXT) = ?`, id, projectID, currentRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// marshalJSONColumn marshals a value for a JSON text column, returning the provided
// zero string (e.g. "" or "[]") for nil/empty inputs so an empty collection is stored
// consistently rather than as a literal "null".
func marshalJSONColumn(value any, empty string) (string, error) {
	switch v := value.(type) {
	case nil:
		return empty, nil
	case []grafana.Panel:
		if len(v) == 0 {
			return empty, nil
		}
	case map[string]string:
		if len(v) == 0 {
			return empty, nil
		}
	case diag.Diagnostics:
		if len(v) == 0 {
			return empty, nil
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
