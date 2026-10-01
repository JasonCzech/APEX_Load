package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	MaxDashboardDefinitionsPerProject = 20
	MaxDashboardTabs                  = 12
	MaxDashboardPanels                = 64
	MaxDashboardSeries                = 100
	MaxDashboardDefinitionBytes       = 1 << 20
)

type DashboardDefinition struct {
	ID                string         `json:"id"`
	ProjectID         string         `json:"project_id"`
	Name              string         `json:"name"`
	Description       string         `json:"description,omitempty"`
	Owner             string         `json:"owner,omitempty"`
	Visibility        string         `json:"visibility"`
	Revision          int64          `json:"revision"`
	SlideshowInterval int            `json:"slideshow_interval_seconds,omitempty"`
	Tabs              []DashboardTab `json:"tabs"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type DashboardTab struct {
	ID     string           `json:"id"`
	Title  string           `json:"title"`
	Panels []DashboardPanel `json:"panels"`
}

type DashboardPanel struct {
	ID     string          `json:"id"`
	Title  string          `json:"title"`
	CardID string          `json:"card_id,omitempty"`
	Kind   string          `json:"kind"`
	X      int             `json:"x"`
	Y      int             `json:"y"`
	W      int             `json:"w"`
	H      int             `json:"h"`
	Query  *AnalyticsQuery `json:"query,omitempty"`
}

type NativeOverlaySelection struct {
	DatasetID     string            `json:"dataset_id,omitempty"`
	SourceID      string            `json:"source_id,omitempty"`
	Metric        string            `json:"metric"`
	Aggregation   string            `json:"aggregation"`
	Host          string            `json:"host,omitempty"`
	Dimensions    map[string]string `json:"dimensions,omitempty"`
	MaxLagSeconds int               `json:"max_lag_seconds,omitempty"`
}

type AnalyticsQuery struct {
	LiveBucketMode   string                  `json:"live_bucket_mode,omitempty"`
	EvidenceMode     string                  `json:"evidence_mode,omitempty"`
	OverlaySelection *NativeOverlaySelection `json:"overlay_selection,omitempty"`

	Population      string `json:"population,omitempty"`
	PropertyName    string `json:"property_name,omitempty"`
	PropertyValue   string `json:"property_value,omitempty"`
	ComparisonRunID string `json:"comparison_run_id,omitempty"`
	StartSeconds    int    `json:"start_seconds,omitempty"`
	EndSeconds      int    `json:"end_seconds,omitempty"`

	Metric        string            `json:"metric"`
	Aggregation   string            `json:"aggregation"`
	Dimensions    []string          `json:"dimensions,omitempty"`
	Filters       map[string]string `json:"filters,omitempty"`
	BucketSeconds int               `json:"bucket_seconds,omitempty"`
	MaxSeries     int               `json:"max_series,omitempty"`
	Overlay       bool              `json:"overlay,omitempty"`
}

var dashboardMetrics = map[string]struct{}{
	"tps": {}, "response_time": {}, "error_rate": {}, "active_vusers": {},
	"throughput": {}, "bytes_sent": {}, "bytes_received": {}, "regional_activity": {},
	"observation_count": {}, "transaction_duration": {}, "resource_cpu": {}, "resource_memory": {},
}
var dashboardAggregations = map[string]struct{}{
	"avg": {}, "sum": {}, "min": {}, "max": {}, "p50": {}, "p90": {}, "p95": {}, "p99": {}, "stddev": {}, "count": {}, "rate": {},
}
var dashboardDimensions = map[string]struct{}{
	"action": {}, "transaction": {}, "region": {}, "property": {}, "dependency": {}, "network_profile": {}, "phase": {}, "page": {}, "hierarchy": {},
}
var dashboardPanelKinds = map[string]struct{}{
	"line": {}, "stat": {}, "table": {}, "map": {}, "workspace_card": {},
}

func validateDashboardDefinition(d *DashboardDefinition) error {
	if d == nil || strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Name) == "" {
		return errors.New("dashboard definition id and name are required")
	}
	if len(d.Name) > MaxGrafanaDashboardNameBytes || len(d.Description) > MaxGrafanaDashboardDescriptionBytes {
		return errors.New("dashboard definition metadata exceeds its size limit")
	}
	if d.Visibility == "" {
		d.Visibility = "project"
	}
	if d.Visibility != "project" {
		return errors.New("dashboard definition visibility must be project")
	}
	if d.SlideshowInterval < 0 || d.SlideshowInterval > 86400 {
		return errors.New("dashboard slideshow interval is invalid")
	}
	if len(d.Tabs) == 0 || len(d.Tabs) > MaxDashboardTabs {
		return fmt.Errorf("dashboard definition must contain 1-%d tabs", MaxDashboardTabs)
	}
	panelCount := 0
	seenTabs := map[string]struct{}{}
	seenPanels := map[string]struct{}{}
	for _, tab := range d.Tabs {
		if strings.TrimSpace(tab.ID) == "" || strings.TrimSpace(tab.Title) == "" {
			return errors.New("dashboard tabs require id and title")
		}
		if len(tab.ID) > 128 || len(tab.Title) > 256 {
			return errors.New("dashboard tab metadata exceeds its size limit")
		}
		if _, exists := seenTabs[tab.ID]; exists {
			return fmt.Errorf("dashboard tab %q is duplicated", tab.ID)
		}
		seenTabs[tab.ID] = struct{}{}
		for _, panel := range tab.Panels {
			panelCount++
			if panelCount > MaxDashboardPanels {
				return fmt.Errorf("dashboard definition exceeds %d panels", MaxDashboardPanels)
			}
			if strings.TrimSpace(panel.ID) == "" || strings.TrimSpace(panel.Title) == "" {
				return errors.New("dashboard panels require id and title")
			}
			if len(panel.ID) > 128 || len(panel.Title) > 256 || len(panel.CardID) > 128 {
				return fmt.Errorf("dashboard panel %q metadata exceeds its size limit", panel.ID)
			}
			if _, ok := dashboardPanelKinds[panel.Kind]; !ok {
				return fmt.Errorf("dashboard panel %q uses unsupported kind %q", panel.ID, panel.Kind)
			}
			if _, exists := seenPanels[panel.ID]; exists {
				return fmt.Errorf("dashboard panel %q is duplicated", panel.ID)
			}
			seenPanels[panel.ID] = struct{}{}
			if panel.X < 0 || panel.X >= 24 || panel.W < 1 || panel.X+panel.W > 24 || panel.Y < 0 || panel.H < 1 || panel.H > 100 {
				return fmt.Errorf("dashboard panel %q has invalid layout", panel.ID)
			}
			if panel.Query == nil {
				if strings.TrimSpace(panel.CardID) == "" {
					return fmt.Errorf("dashboard panel %q requires card_id or query", panel.ID)
				}
				continue
			}
			q := panel.Query
			if q.EvidenceMode != "" {
				if err := ValidateNativeEvidenceQuery(*q); err != nil {
					return err
				}
			} else if q.OverlaySelection != nil || q.LiveBucketMode != "" {
				return errors.New("overlay selection requires evidence mode")
			} else if q.Population != "" {
				if q.MaxSeries == 0 {
					q.MaxSeries = 1
				}
				if err := ValidateNativeArchiveQuery(*q); err != nil {
					return err
				}
			} else if q.PropertyName != "" || q.PropertyValue != "" || q.ComparisonRunID != "" || q.StartSeconds != 0 || q.EndSeconds != 0 {
				return errors.New("archive query fields require an explicit population")
			}
			if _, ok := dashboardMetrics[q.Metric]; !ok {
				return fmt.Errorf("dashboard panel %q uses unsupported metric %q", panel.ID, q.Metric)
			}
			if _, ok := dashboardAggregations[q.Aggregation]; !ok {
				return fmt.Errorf("dashboard panel %q uses unsupported aggregation %q", panel.ID, q.Aggregation)
			}
			if len(q.Dimensions) > 3 || len(q.Filters) > 16 {
				return fmt.Errorf("dashboard panel %q query exceeds dimension/filter limits", panel.ID)
			}
			for _, dimension := range q.Dimensions {
				if _, ok := dashboardDimensions[dimension]; !ok {
					return fmt.Errorf("dashboard panel %q uses unsupported dimension %q", panel.ID, dimension)
				}
			}
			for name, value := range q.Filters {
				if _, ok := dashboardDimensions[name]; !ok {
					return fmt.Errorf("dashboard panel %q uses unsupported filter %q", panel.ID, name)
				}
				if len(value) > 256 {
					return fmt.Errorf("dashboard panel %q filter %q exceeds its size limit", panel.ID, name)
				}
			}
			if q.BucketSeconds != 0 && (q.BucketSeconds < 1 || q.BucketSeconds > 3600) {
				return fmt.Errorf("dashboard panel %q bucket is invalid", panel.ID)
			}
			if q.MaxSeries == 0 {
				q.MaxSeries = 20
			}
			if q.MaxSeries < 1 || q.MaxSeries > MaxDashboardSeries {
				return fmt.Errorf("dashboard panel %q max_series is invalid", panel.ID)
			}
		}
	}
	return nil
}

func (s *SQLiteStore) SaveDashboardDefinition(d *DashboardDefinition) error {
	if err := validateDashboardDefinition(d); err != nil {
		return err
	}
	d.ProjectID = normalizeProjectID(d.ProjectID)
	now := time.Now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	if d.Revision <= 0 {
		d.Revision = 1
	}
	payload, err := json.Marshal(struct {
		SlideshowInterval int            `json:"slideshow_interval_seconds,omitempty"`
		Tabs              []DashboardTab `json:"tabs"`
	}{d.SlideshowInterval, d.Tabs})
	if err != nil || len(payload) > MaxDashboardDefinitionBytes {
		return errors.New("dashboard definition exceeds its encoded size limit")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if d.ProjectID, err = lockProjectForOwnedMutation(tx, d.ProjectID); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM dashboard_definitions WHERE project_id = ?`, d.ProjectID).Scan(&count); err != nil {
		return err
	}
	if count >= MaxDashboardDefinitionsPerProject {
		return fmt.Errorf("project already has the maximum of %d dashboard definitions", MaxDashboardDefinitionsPerProject)
	}
	_, err = tx.Exec(`INSERT INTO dashboard_definitions (id, project_id, name, description, owner, visibility, revision, definition_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.ID, d.ProjectID, d.Name, d.Description, d.Owner, d.Visibility, d.Revision, string(payload), d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const dashboardDefinitionSelect = `SELECT id, project_id, name, COALESCE(description, ''), COALESCE(owner, ''), visibility, revision, definition_json, created_at, updated_at FROM dashboard_definitions`

func scanDashboardDefinition(row capacityRowScanner) (*DashboardDefinition, error) {
	var d DashboardDefinition
	var payload string
	if err := row.Scan(&d.ID, &d.ProjectID, &d.Name, &d.Description, &d.Owner, &d.Visibility, &d.Revision, &payload, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	var definition struct {
		SlideshowInterval int            `json:"slideshow_interval_seconds,omitempty"`
		Tabs              []DashboardTab `json:"tabs"`
	}
	if err := json.Unmarshal([]byte(payload), &definition); err != nil {
		return nil, err
	}
	d.SlideshowInterval, d.Tabs = definition.SlideshowInterval, definition.Tabs
	return &d, nil
}

func (s *SQLiteStore) GetDashboardDefinition(id string) (*DashboardDefinition, error) {
	return scanDashboardDefinition(s.db.QueryRow(dashboardDefinitionSelect+` WHERE id = ?`, id))
}
func (s *SQLiteStore) ListDashboardDefinitions(projectID string, limit, offset int) ([]*DashboardDefinition, error) {
	query := dashboardDefinitionSelect + ` WHERE project_id = ? ORDER BY updated_at DESC`
	args := []any{normalizeProjectID(projectID)}
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
	var out []*DashboardDefinition
	for rows.Next() {
		item, err := scanDashboardDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *SQLiteStore) UpdateDashboardDefinitionIfRevision(d *DashboardDefinition, expected int64) (bool, error) {
	if expected <= 0 {
		return false, errors.New("expected revision is required")
	}
	if err := validateDashboardDefinition(d); err != nil {
		return false, err
	}
	payload, _ := json.Marshal(struct {
		SlideshowInterval int            `json:"slideshow_interval_seconds,omitempty"`
		Tabs              []DashboardTab `json:"tabs"`
	}{d.SlideshowInterval, d.Tabs})
	now := time.Now()
	result, err := s.db.Exec(`UPDATE dashboard_definitions SET name = ?, description = ?, visibility = ?, revision = revision + 1, definition_json = ?, updated_at = ? WHERE id = ? AND project_id = ? AND revision = ?`, d.Name, d.Description, d.Visibility, string(payload), now, d.ID, normalizeProjectID(d.ProjectID), expected)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 1 {
		d.Revision, d.UpdatedAt = expected+1, now
	}
	return affected == 1, nil
}
func (s *SQLiteStore) DeleteDashboardDefinitionIfRevision(id, projectID string, expected int64) (bool, error) {
	result, err := s.db.Exec(`DELETE FROM dashboard_definitions WHERE id = ? AND project_id = ? AND revision = ?`, id, normalizeProjectID(projectID), expected)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// ValidateNativeArchiveQuery is shared by saved definitions and query execution.
func ValidateNativeArchiveQuery(q AnalyticsQuery) error {
	if q.EvidenceMode != "" || q.OverlaySelection != nil || q.LiveBucketMode != "" {
		return errors.New("use the native evidence query for live/grouped overlays")
	}
	if q.Population != "action" && q.Population != "transaction" {
		return errors.New("select action or transaction population")
	}
	if len(q.Dimensions) != 0 || q.Overlay || len(q.Filters) > 1 || q.MaxSeries > 1 || q.MaxSeries < 0 {
		return errors.New("archive panels support an action identity filter; dimensions and overlays are unavailable")
	}
	for k, v := range q.Filters {
		if k != "action" || len(v) > 256 {
			return errors.New("invalid archive action filter")
		}
	}
	if (q.PropertyName == "") != (q.PropertyValue == "") || len(q.PropertyName) > 64 || len(q.PropertyValue) > 64 || q.PropertyName != "" && q.Population != "action" {
		return errors.New("property filters require an action population and bounded name/value pair")
	}
	if len(q.ComparisonRunID) > 128 {
		return errors.New("comparison run id exceeds limit")
	}
	if q.StartSeconds < 0 || q.EndSeconds <= q.StartSeconds || q.EndSeconds > 86400 || q.BucketSeconds < 1 || q.BucketSeconds > 3600 || (q.EndSeconds-q.StartSeconds+q.BucketSeconds-1)/q.BucketSeconds > 1000 {
		return errors.New("select a window within 24 hours and at most 1000 buckets")
	}
	switch q.Metric {
	case "observation_count":
		if q.Aggregation == "count" {
			return nil
		}
	case "error_rate":
		if q.Aggregation == "avg" {
			return nil
		}
	case "response_time":
		switch q.Aggregation {
		case "avg", "min", "max", "p50", "p90", "p95", "p99":
			return nil
		}
	}
	return errors.New("unsupported archive measurement or aggregation")
}
