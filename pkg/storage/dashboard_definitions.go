package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
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
	// CurrentRevision is set on a read of a named revision: the
	// definition's revision now (equal to Revision when the current one was
	// named). RetainedRevisions lists the saved revisions a viewer can
	// reopen, newest first. Neither is stored.
	CurrentRevision   int64                         `json:"current_revision,omitempty"`
	RetainedRevisions []DashboardDefinitionRevision `json:"retained_revisions,omitempty"`
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
	// DatasetArtifactSHA256 pins the dataset's committed artifacts the
	// overlay was saved against; a dataset that changed since is refused.
	DatasetArtifactSHA256 string `json:"dataset_artifact_sha256,omitempty"`
}

// AnalyticsQueryVersion is the query semantics a saved query states; 0 is a
// query saved before the version was recorded, read with the same rules.
const AnalyticsQueryVersion = 3

type AnalyticsQuery struct {
	BrowserInputs *BrowserPanelInputs `json:"browser_inputs,omitempty"`
	PanelInputs   *SavedPanelInputs   `json:"panel_inputs,omitempty"`
	// BrowserTupleMode separates complete document measurement tuples in v3 archive panels.
	BrowserTupleMode string `json:"browser_tuple_mode,omitempty"`
	// QueryVersion names the query semantics the panel was saved under.
	QueryVersion     int                     `json:"query_version,omitempty"`
	CatalogVersion   string                  `json:"catalog_version,omitempty"`
	Alignment        string                  `json:"alignment,omitempty"`
	CoveragePolicy   string                  `json:"coverage_policy,omitempty"`
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
	// JointEvidence reads the run's joint evidence v4 (B5): Dimensions (up
	// to three) and Filters name projected dimensions. Merged live only.
	JointEvidence bool `json:"joint_evidence,omitempty"`
	// Derivation asks for one catalog operation over the query's series
	// (ratios, baseline deltas, moving statistics); it is validated by the
	// catalog and evaluated by pkg/analysis.
	Derivation *analysis.QueryDerivation `json:"derivation,omitempty"`
}

// Dashboard metrics, aggregations and dimensions are validated by the
// analysis catalog (analysis.ValidateDashboardQuery), whose alias table keeps
// saved display names meaning what they did.
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
			if q.PanelInputs != nil {
				NormalizeSavedPanelQueryDefaults(q)
			}
			if (q.QueryVersion == 4 || q.QueryVersion == 5) && q.BrowserInputs == nil && q.PanelInputs == nil {
				return errors.New("saved browser query version 4 or 5 requires evaluated input pins")
			}
			if err := validateAnalyticsQueryVersion(*q); err != nil {
				return fmt.Errorf("dashboard panel %q %w", panel.ID, err)
			}
			if IsNativeObservationRate(q.Metric) && q.Population == "" {
				return errors.New("retained rates require an explicit population")
			}
			if q.EvidenceMode != "" {
				if err := ValidateNativeEvidenceQuery(*q); err != nil {
					return err
				}
			} else if q.OverlaySelection != nil || q.LiveBucketMode != "" {
				return errors.New("overlay selection requires evidence mode")
			} else if q.Population != "" {
				if q.MaxSeries == 0 {
					q.MaxSeries = 1
					if q.BrowserTupleMode == "separate" {
						q.MaxSeries = 20
					}
				}
				if err := ValidateNativeArchiveQuery(*q); err != nil {
					return err
				}
			} else if q.PanelInputs == nil && (q.PropertyName != "" || q.PropertyValue != "" || q.ComparisonRunID != "" || q.StartSeconds != 0 || q.EndSeconds != 0) {
				return errors.New("archive query fields require an explicit population")
			}
			hasPinnedDataset := q.PanelInputs != nil && (q.PanelInputs.Current.DatasetID != "" || (q.PanelInputs.Baseline != nil && q.PanelInputs.Baseline.DatasetID != ""))
			if hasPinnedDataset && q.Population == "" {
				if strings.TrimSpace(q.Metric) == "" || len(q.Metric) > 256 || analysis.ValidateScalarAggregationName(q.Aggregation) != nil {
					return fmt.Errorf("dashboard panel %q: invalid dataset metric or aggregation", panel.ID)
				}
			} else if err := analysis.ValidateDashboardQuery(q.Metric, q.Aggregation, q.Dimensions, q.Filters); err != nil {
				return fmt.Errorf("dashboard panel %q: %w", panel.ID, err)
			}
			if q.Derivation != nil {
				// Only a qualified evidence query takes a derivation; the
				// evidence validators checked it against the catalog.
				if q.EvidenceMode == "" && q.Population == "" {
					return fmt.Errorf("dashboard panel %q: derivations need an evidence or archive query", panel.ID)
				}
			}
			for name, value := range q.Filters {
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

// DashboardDefinitionInvalid marks a definition refused for its own content
// (validation, its encoded size, the project's definition limit, a missing
// expected revision); any other save error is the store's own.
type DashboardDefinitionInvalid struct{ Err error }

func (e *DashboardDefinitionInvalid) Error() string { return e.Err.Error() }
func (e *DashboardDefinitionInvalid) Unwrap() error { return e.Err }

func invalidDashboardDefinition(err error) error { return &DashboardDefinitionInvalid{Err: err} }

func (s *SQLiteStore) SaveDashboardDefinition(d *DashboardDefinition) error {
	if err := validateDashboardDefinition(d); err != nil {
		return invalidDashboardDefinition(err)
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
		return invalidDashboardDefinition(errors.New("dashboard definition exceeds its encoded size limit"))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if d.ProjectID, err = lockProjectForOwnedMutation(tx, d.ProjectID); err != nil {
		if errors.Is(err, ErrOwnedProjectNotFound) {
			return invalidDashboardDefinition(err)
		}
		return err
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM dashboard_definitions WHERE project_id = ?`, d.ProjectID).Scan(&count); err != nil {
		return err
	}
	if count >= MaxDashboardDefinitionsPerProject {
		return invalidDashboardDefinition(fmt.Errorf("project already has the maximum of %d dashboard definitions", MaxDashboardDefinitionsPerProject))
	}
	_, err = tx.Exec(`INSERT INTO dashboard_definitions (id, project_id, name, description, owner, visibility, revision, definition_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, d.ID, d.ProjectID, d.Name, d.Description, d.Owner, d.Visibility, d.Revision, string(payload), d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return err
	}
	if err := recordDashboardDefinitionRevisionTx(tx, d, payload, d.UpdatedAt); err != nil {
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
		return false, invalidDashboardDefinition(errors.New("expected revision is required"))
	}
	if err := validateDashboardDefinition(d); err != nil {
		return false, invalidDashboardDefinition(err)
	}
	payload, err := json.Marshal(struct {
		SlideshowInterval int            `json:"slideshow_interval_seconds,omitempty"`
		Tabs              []DashboardTab `json:"tabs"`
	}{d.SlideshowInterval, d.Tabs})
	if err != nil || len(payload) > MaxDashboardDefinitionBytes {
		return false, invalidDashboardDefinition(errors.New("dashboard definition exceeds its encoded size limit"))
	}
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// The project row lock orders this edit against the project's deletion,
	// so a revision is never written for a project being deleted.
	projectID, err := lockProjectForOwnedMutation(tx, d.ProjectID)
	if errors.Is(err, ErrOwnedProjectNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE dashboard_definitions SET name = ?, description = ?, visibility = ?, revision = revision + 1, definition_json = ?, updated_at = ? WHERE id = ? AND project_id = ? AND revision = ?`, d.Name, d.Description, d.Visibility, string(payload), now, d.ID, projectID, expected)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	saved := *d
	saved.ProjectID, saved.Revision = projectID, expected+1
	if err := recordDashboardDefinitionRevisionTx(tx, &saved, payload, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	d.Revision, d.UpdatedAt = expected+1, now
	return true, nil
}
func (s *SQLiteStore) DeleteDashboardDefinitionIfRevision(id, projectID string, expected int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`DELETE FROM dashboard_definitions WHERE id = ? AND project_id = ? AND revision = ?`, id, normalizeProjectID(projectID), expected)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	// Its saved revisions go with it.
	if _, err := tx.Exec(`DELETE FROM dashboard_definition_revisions WHERE definition_id = ?`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// maxDashboardDefinitionRevisions is how many saved revisions of one
// definition are kept; the oldest beyond it are pruned when a revision is
// saved.
const maxDashboardDefinitionRevisions = 100

// ErrDashboardRevisionNotRetained says a definition revision was never saved
// or has been pruned.
var ErrDashboardRevisionNotRetained = errors.New("dashboard definition revision is not retained")

// recordDashboardDefinitionRevisionTx keeps the saved revision so a viewer
// can reopen exactly what was saved, and prunes the oldest beyond the bound.
func recordDashboardDefinitionRevisionTx(tx *storeTx, d *DashboardDefinition, payload []byte, savedAt time.Time) error {
	if _, err := tx.Exec(`INSERT INTO dashboard_definition_revisions (definition_id, project_id, revision, name, description, owner, visibility, definition_json, saved_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.ProjectID, d.Revision, d.Name, d.Description, d.Owner, d.Visibility, string(payload), savedAt); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM dashboard_definition_revisions WHERE definition_id = ? AND revision <= ?`, d.ID, d.Revision-maxDashboardDefinitionRevisions)
	return err
}

// GetDashboardDefinitionRevision returns one saved revision of a definition.
func (s *SQLiteStore) GetDashboardDefinitionRevision(id string, revision int64) (*DashboardDefinition, error) {
	var d DashboardDefinition
	var payload string
	err := s.db.QueryRow(`SELECT r.definition_id, r.project_id, r.name, COALESCE(r.description, ''), COALESCE(r.owner, ''), r.visibility, r.revision, r.definition_json, d.created_at, r.saved_at
		FROM dashboard_definition_revisions r JOIN dashboard_definitions d ON d.id = r.definition_id AND d.project_id = r.project_id WHERE r.definition_id = ? AND r.revision = ?`, id, revision).
		Scan(&d.ID, &d.ProjectID, &d.Name, &d.Description, &d.Owner, &d.Visibility, &d.Revision, &payload, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDashboardRevisionNotRetained
	}
	if err != nil {
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

// ListDashboardDefinitionRevisions lists a definition's retained revisions,
// newest first, without their panels.
func (s *SQLiteStore) ListDashboardDefinitionRevisions(id string) ([]DashboardDefinitionRevision, error) {
	rows, err := s.db.Query(`SELECT revision, name, saved_at FROM dashboard_definition_revisions WHERE definition_id = ? ORDER BY revision DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DashboardDefinitionRevision{}
	for rows.Next() {
		var item DashboardDefinitionRevision
		if err := rows.Scan(&item.Revision, &item.Name, &item.SavedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// DashboardDefinitionRevision names one retained revision.
type DashboardDefinitionRevision struct {
	Revision int64     `json:"revision"`
	Name     string    `json:"name"`
	SavedAt  time.Time `json:"saved_at"`
}

// validateAnalyticsQueryVersion refuses query semantics newer than this
// controller's, when a query is saved and when it runs.
func validateAnalyticsQueryVersion(q AnalyticsQuery) error {
	if q.Alignment != "" && q.Alignment != "elapsed" && q.Alignment != "utc" {
		return errors.New("alignment must be elapsed or utc")
	}
	if q.CoveragePolicy != "" && q.CoveragePolicy != "complete_bucket" && q.CoveragePolicy != "required" {
		return errors.New("coverage_policy must be complete_bucket or required")
	}
	if q.CatalogVersion != "" && q.CatalogVersion != analysis.CatalogVersion {
		return errors.New("unsupported catalog_version")
	}
	if err := validateBrowserPanelInputs(q); err != nil {
		return err
	}
	if q.BrowserTupleMode != "" && ((q.QueryVersion != 3 && q.QueryVersion != 4 && q.QueryVersion != 5) || q.BrowserTupleMode != "separate" || q.Population != analysis.PopulationBrowserDocument || q.EvidenceMode != "" || q.JointEvidence) {
		return errors.New("separate browser tuples require a version 3, 4 or 5 browser document archive query")
	}
	if q.QueryVersion < 0 || q.QueryVersion > 5 {
		return fmt.Errorf("query_version must be at most %d", 5)
	}
	return nil
}

// ValidateNativeArchiveQuery is shared by saved definitions and query execution.
func ValidateNativeArchiveQuery(q AnalyticsQuery) error {
	if err := validateAnalyticsQueryVersion(q); err != nil {
		return err
	}
	if q.EvidenceMode != "" || q.OverlaySelection != nil || q.LiveBucketMode != "" {
		return errors.New("use the native evidence query for live/grouped overlays")
	}
	if q.Population == analysis.PopulationJavaHTTPAction {
		return validateNativeJavaHTTPQuery(q)
	}
	if q.Population == analysis.PopulationByte {
		return validateNativeByteQuery(q)
	}
	if q.Population == analysis.PopulationMessage {
		return validateNativeMessageQuery(q)
	}
	if q.Population == analysis.PopulationBrowserDocument {
		return validateNativeBrowserDocumentQuery(q)
	}
	if q.Population != "action" && q.Population != "transaction" {
		return errors.New("select action or transaction population")
	}
	if len(q.Dimensions) != 0 || q.Overlay || len(q.Filters) > 4 || q.MaxSeries > 1 || q.MaxSeries < 0 {
		return errors.New("archive panels support bounded identity/dimension filters; grouping uses the evidence workflow")
	}
	for k, v := range q.Filters {
		if (k != "action" && k != "network_profile" && k != "hierarchy" && k != "phase") || v == "" || len(v) > 256 {
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
	if analysis.ValidateMetricAggregation(q.Metric, q.Population, q.Aggregation) != nil {
		return errors.New("unsupported archive measurement or aggregation")
	}
	if q.Derivation != nil {
		return analysis.ValidateQueryDerivation(q.Metric, q.Population, analysis.KindObservation, *q.Derivation, q.ComparisonRunID != "")
	}
	return nil
}

// validateNativeBrowserDocumentQuery admits an archive selection of browser
// document metrics: no grouping or property, and filters only on the fields
// of a document measurement tuple. Legacy queries require one tuple; separate
// mode retains bounded independent tuple series.
func validateNativeBrowserDocumentQuery(q AnalyticsQuery) error {
	if len(q.Dimensions) != 0 || q.Overlay || q.PropertyName != "" || q.PropertyValue != "" || q.MaxSeries < 0 || q.MaxSeries > 20 || q.BrowserTupleMode == "" && q.MaxSeries > 1 {
		return errors.New("browser document queries require a valid series limit and take no grouping or property")
	}
	for k, v := range q.Filters {
		if !slices.Contains(analysis.BrowserDocumentFilters(), k) || v == "" || len(v) > 256 {
			return errors.New("browser document queries filter only by engine, device_profile, frame_scope, lifecycle_phase, group, region or cohort")
		}
	}
	if len(q.ComparisonRunID) > 128 {
		return errors.New("comparison run id exceeds limit")
	}
	if q.StartSeconds < 0 || q.EndSeconds <= q.StartSeconds || q.EndSeconds > 86400 || q.BucketSeconds < 1 || q.BucketSeconds > 3600 || (q.EndSeconds-q.StartSeconds+q.BucketSeconds-1)/q.BucketSeconds > 1000 {
		return errors.New("select a window within 24 hours and at most 1000 buckets")
	}
	if err := analysis.ValidateMetricAggregation(q.Metric, q.Population, q.Aggregation); err != nil {
		return err
	}
	if q.BrowserTupleMode != "" && q.Derivation != nil {
		return errors.New("derived browser tuple panels require a single filtered tuple")
	}
	if q.Derivation != nil {
		return analysis.ValidateQueryDerivation(q.Metric, q.Population, analysis.KindObservation, *q.Derivation, q.ComparisonRunID != "")
	}
	return nil
}

// validateNativeMessageQuery admits an archive message query: received
// messages from messaging v2 evidence, counted per bucket or as a message
// ratio derivation, with no grouping and at most a source filter.
func validateNativeMessageQuery(q AnalyticsQuery) error {
	if len(q.Dimensions) != 0 || q.Overlay || q.PropertyName != "" || q.PropertyValue != "" || q.ComparisonRunID != "" || q.MaxSeries > 1 || q.MaxSeries < 0 {
		return errors.New("message queries take no grouping, property or comparison")
	}
	for k, v := range q.Filters {
		if k != "source" || v == "" || len(v) > 256 {
			return errors.New("message queries filter only by source")
		}
	}
	if q.StartSeconds < 0 || q.EndSeconds <= q.StartSeconds || q.EndSeconds > 86400 || q.BucketSeconds < 1 || q.BucketSeconds > 3600 || (q.EndSeconds-q.StartSeconds+q.BucketSeconds-1)/q.BucketSeconds > 1000 {
		return errors.New("select a window within 24 hours and at most 1000 buckets")
	}
	if err := analysis.ValidateMetricAggregation(q.Metric, q.Population, q.Aggregation); err != nil {
		return err
	}
	if q.Derivation != nil {
		return analysis.ValidateQueryDerivation(q.Metric, q.Population, analysis.KindObservation, *q.Derivation, false)
	}
	return nil
}

// ValidLowerSHA256Hex reports whether a value is a lowercase hex SHA-256.
func ValidLowerSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
