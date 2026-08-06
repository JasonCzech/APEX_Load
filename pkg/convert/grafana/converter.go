// Package grafana converts an imported Grafana dashboard JSON into a set of APEX Load
// panels backed by PromQL. Unlike the other pkg/convert packages it does NOT emit a
// dsl.Script — a dashboard is an observability artifact, not a load script — so it has
// no coupling to pkg/dsl or the script-import response machinery.
package grafana

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
)

// Structural import limits. These bound the parsed document; the query-time resource
// caps (series/points/bytes/concurrency) live in the api layer.
const (
	MaxPanels              = 40
	MaxTargetsPerPanel     = 10
	MaxQueriesTotal        = 60
	MaxVariables           = 128
	MaxExprBytes           = 4096
	MaxOptionsPerVar       = 50
	MinSchemaVersion       = 16
	MaxTitleBytes          = 512
	MaxSourceUIDBytes      = 256
	MaxRefIDBytes          = 128
	MaxLegendBytes         = 1024
	MaxDatasourceBytes     = 512
	MaxTypeBytes           = 64
	MaxUnitIDBytes         = 128
	MaxVariableValueBytes  = 128
	MaxDatasourceJSONBytes = 4096
	MaxOverridesPerPanel   = 128
	MaxOverrideProperties  = 128
	MaxGridY               = 512
	MaxGridHeight          = 64
	MaxThresholdSteps      = 32
	MaxThresholdColorBytes = 64
)

// Panel kinds in APEX vocabulary (not Grafana's).
const (
	PanelKindLine        = "line"
	PanelKindSingleValue = "single_value"
	PanelKindTable       = "table"
	PanelKindHeatmap     = "heatmap"
)

type ImportRequest struct {
	Name      string         `json:"name,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Content   string         `json:"content"`
	Options   ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	// DashboardName overrides the stored name; when empty the source title is used.
	DashboardName string `json:"dashboard_name,omitempty"`
}

type ConvertStats struct {
	PanelsSeen                int      `json:"panels_seen"`
	PanelsConverted           int      `json:"panels_converted"`
	LineCharts                int      `json:"line_charts"`
	SingleValueTiles          int      `json:"single_value_tiles"`
	Tables                    int      `json:"tables"`
	Heatmaps                  int      `json:"heatmaps"`
	Targets                   int      `json:"targets"`
	RowsExpanded              int      `json:"rows_expanded"`
	UnsupportedPanelTypes     []string `json:"unsupported_panel_types,omitempty"`
	VariablesResolved         int      `json:"variables_resolved"`
	VariablesRequiringBinding int      `json:"variables_requiring_binding"`
}

type ConvertResult struct {
	Dashboard   *Dashboard        `json:"dashboard"`
	Warnings    []string          `json:"warnings,omitempty"`
	Unsupported diag.Diagnostics  `json:"unsupported,omitempty"`
	Assumptions []string          `json:"assumptions,omitempty"`
	Stats       ConvertStats      `json:"stats"`
	Bindings    []VariableBinding `json:"bindings,omitempty"`
}

// HasBlockers reports whether any diagnostic blocks committing the import.
func (r *ConvertResult) HasBlockers() bool {
	if r == nil {
		return false
	}
	for _, d := range r.Unsupported {
		if d.Severity == diag.Blocker {
			return true
		}
	}
	return false
}

// RequiredBindingNames returns the names of variables the user must bind at commit —
// only those that are both required and actually referenced by an imported query, so a
// declared-but-unused input variable never forces a binding.
func (r *ConvertResult) RequiredBindingNames() []string {
	if r == nil {
		return nil
	}
	out := []string{}
	for _, b := range r.Bindings {
		if b.Kind == BindingRequired && len(b.UsedBy) > 0 {
			out = append(out, b.Name)
		}
	}
	sort.Strings(out)
	return out
}

type Dashboard struct {
	SourceTitle   string  `json:"source_title"`
	SourceUID     string  `json:"source_uid,omitempty"`
	SchemaVersion int     `json:"schema_version"`
	Panels        []Panel `json:"panels"`
}

type Panel struct {
	PanelID       int              `json:"panel_id"`
	Title         string           `json:"title"`
	Kind          string           `json:"kind"`
	Unit          string           `json:"unit,omitempty"`
	GridPos       GridPos          `json:"grid_pos"`
	Targets       []Target         `json:"targets"`
	RowTitle      string           `json:"row_title,omitempty"`
	DatasourceUID string           `json:"datasource_uid,omitempty"`
	Thresholds    []PanelThreshold `json:"thresholds,omitempty"`
}

type PanelThreshold struct {
	Value    *float64 `json:"value,omitempty"`
	Color    string   `json:"color"`
	Severity string   `json:"severity"`
}

type GridPos struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type Target struct {
	RefID         string   `json:"ref_id"`
	Expr          string   `json:"expr"`
	LegendFormat  string   `json:"legend_format,omitempty"`
	LabelFields   []string `json:"label_fields,omitempty"`
	DatasourceUID string   `json:"datasource_uid,omitempty"`
}

type VariableBinding struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	GrafanaType   string   `json:"grafana_type"`
	Label         string   `json:"label,omitempty"`
	ResolvedTo    string   `json:"resolved_to,omitempty"`
	Default       string   `json:"default,omitempty"`
	Options       []string `json:"options,omitempty"`
	Query         string   `json:"query,omitempty"`
	DatasourceUID string   `json:"datasource_uid,omitempty"`
	Reason        string   `json:"reason"`
	UsedBy        []string `json:"used_by,omitempty"`
}

var legendTokenPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// Convert parses a Grafana dashboard JSON and produces the reviewable panel set plus
// diagnostics and variable bindings. It returns an error only for input that cannot be
// parsed at all; everything else is reported through diagnostics so the review UI can
// show what was and was not imported.
func Convert(req ImportRequest) (*ConvertResult, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, fmt.Errorf("grafana dashboard content is empty")
	}
	raw, err := unwrapDashboard([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("parse grafana dashboard: %w", err)
	}

	col := &diag.Collector{}
	assumptions := map[string]struct{}{}

	if raw.SchemaVersion > 0 && raw.SchemaVersion < MinSchemaVersion {
		col.Add(diag.Blocker, "dashboard", "schemaVersion",
			fmt.Sprintf("Grafana schema version %d predates the supported import baseline (%d).", raw.SchemaVersion, MinSchemaVersion))
	}

	variables := raw.Templating.List
	if len(variables) > MaxVariables {
		col.Add(diag.Blocker, "dashboard", "templating.list",
			fmt.Sprintf("dashboard declares %d variables, exceeding the import limit of %d", len(variables), MaxVariables))
		variables = variables[:MaxVariables]
	}
	bindings, policy := classifyVariables(variables, col)
	usedBy := map[string]map[string]struct{}{}

	dashboard := &Dashboard{
		SourceTitle:   strings.TrimSpace(raw.Title),
		SourceUID:     strings.TrimSpace(raw.UID),
		SchemaVersion: raw.SchemaVersion,
		Panels:        []Panel{},
	}
	if len(dashboard.SourceTitle) > MaxTitleBytes {
		col.Add(diag.Blocker, "dashboard", "title", fmt.Sprintf("dashboard title exceeds the limit of %d bytes", MaxTitleBytes))
	}
	if len(dashboard.SourceUID) > MaxSourceUIDBytes {
		col.Add(diag.Blocker, "dashboard", "uid", fmt.Sprintf("dashboard uid exceeds the limit of %d bytes", MaxSourceUIDBytes))
	}
	stats := ConvertStats{}
	unsupportedTypes := map[string]struct{}{}

	flat, panelsTruncated := flattenPanels(raw.Panels, col, &stats, MaxPanels)
	if panelsTruncated {
		col.Add(diag.Blocker, "dashboard", "panels",
			fmt.Sprintf("dashboard declares more than %d panel/row entries, exceeding the import limit of %d; trim it before importing", MaxPanels, MaxPanels))
	}

	panelIDs := make(map[int]string, len(flat))
	for _, fp := range flat {
		if fp.panel.ID <= 0 {
			col.Add(diag.Blocker, "panel", fp.location, "panel is missing a positive Grafana id; stable panel identity is required for import")
			continue
		}
		if first, exists := panelIDs[fp.panel.ID]; exists {
			col.Add(diag.Blocker, "panel", fp.location,
				fmt.Sprintf("panel id %d duplicates %s; panel ids must be unique", fp.panel.ID, first))
			continue
		}
		panelIDs[fp.panel.ID] = fp.location
	}

	totalQueries := 0
	for _, fp := range flat {
		stats.PanelsSeen++
		panel, ok := convertPanel(fp, policy, col, assumptions, usedBy, unsupportedTypes, &totalQueries, strings.TrimSpace(req.ProjectID) != "")
		if !ok {
			continue
		}
		dashboard.Panels = append(dashboard.Panels, panel)
		stats.PanelsConverted++
		switch panel.Kind {
		case PanelKindLine:
			stats.LineCharts++
		case PanelKindSingleValue:
			stats.SingleValueTiles++
		case PanelKindTable:
			stats.Tables++
		case PanelKindHeatmap:
			stats.Heatmaps++
		}
		stats.Targets += len(panel.Targets)
	}

	if totalQueries > MaxQueriesTotal {
		col.Add(diag.Blocker, "dashboard", "targets",
			fmt.Sprintf("dashboard declares %d queries across its panels, exceeding the import limit of %d", totalQueries, MaxQueriesTotal))
	}

	// Attach used-by provenance and roll up variable stats.
	for i := range bindings {
		name := bindings[i].Name
		if locs, ok := usedBy[name]; ok {
			bindings[i].UsedBy = sortedKeys(locs)
		}
		switch bindings[i].Kind {
		case BindingAuto:
			stats.VariablesResolved++
		case BindingRequired:
			stats.VariablesRequiringBinding++
		}
	}

	if len(unsupportedTypes) > 0 {
		stats.UnsupportedPanelTypes = sortedKeys(unsupportedTypes)
	}

	if len(dashboard.Panels) == 0 {
		col.Add(diag.Blocker, "dashboard", "panels", "no supported panels were found; a dashboard must contain at least one importable timeseries/graph, stat/gauge, table, or heatmap panel")
	}

	return &ConvertResult{
		Dashboard:   dashboard,
		Warnings:    col.Items().Strings(),
		Unsupported: col.Items(),
		Assumptions: sortedKeys(assumptions),
		Stats:       stats,
		Bindings:    bindings,
	}, nil
}

// unwrapDashboard tolerates the /api/dashboards/uid/:uid export envelope
// ({"dashboard": {...}, "meta": {...}}) that users routinely paste, falling back to
// treating the top-level object as the dashboard itself.
func unwrapDashboard(content []byte) (rawDashboard, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(content, &object); err != nil {
		return rawDashboard{}, err
	}
	if object == nil {
		return rawDashboard{}, fmt.Errorf("dashboard JSON must be an object")
	}
	var envelope rawEnvelope
	if err := json.Unmarshal(content, &envelope); err == nil && len(strings.TrimSpace(string(envelope.Dashboard))) > 0 {
		var innerObject map[string]json.RawMessage
		if err := json.Unmarshal(envelope.Dashboard, &innerObject); err != nil || innerObject == nil {
			return rawDashboard{}, fmt.Errorf("dashboard envelope must contain an object")
		}
		var inner rawDashboard
		if err := json.Unmarshal(envelope.Dashboard, &inner); err != nil {
			return rawDashboard{}, err
		}
		return inner, nil
	}
	var top rawDashboard
	if err := json.Unmarshal(content, &top); err != nil {
		return rawDashboard{}, err
	}
	return top, nil
}

// CanonicalizeImportSource retains only fields the importer understands. This keeps
// short-lived commit sessions bounded by relevant source data and drops unrelated
// export metadata (including plugin-specific fields that may contain credentials).
func CanonicalizeImportSource(content string) (string, error) {
	raw, err := unwrapDashboard([]byte(strings.TrimSpace(content)))
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

type flatPanel struct {
	panel    rawPanel
	location string
	rowTitle string
}

func flattenPanels(panels []rawPanel, col *diag.Collector, stats *ConvertStats, maxEntries int) ([]flatPanel, bool) {
	out := []flatPanel{}
	currentRow := ""
	visited := 0
	for i, p := range panels {
		if visited >= maxEntries {
			return out, true
		}
		visited++
		if isRowPanel(p) {
			stats.RowsExpanded++
			currentRow = strings.TrimSpace(p.Title)
			col.Add(diag.Info, "row", fmt.Sprintf("panels[%d]", i),
				fmt.Sprintf("Row %q is layout structure; its panels are lifted into the flat list.", currentRow))
			for j, child := range p.Panels {
				if visited >= maxEntries {
					return out, true
				}
				visited++
				if isRowPanel(child) {
					col.Add(diag.Warning, "row", fmt.Sprintf("panels[%d].panels[%d]", i, j), "nested rows are not supported and were skipped")
					continue
				}
				out = append(out, flatPanel{panel: child, location: fmt.Sprintf("panels[%d].panels[%d]", i, j), rowTitle: currentRow})
			}
			continue
		}
		out = append(out, flatPanel{panel: p, location: fmt.Sprintf("panels[%d]", i), rowTitle: currentRow})
	}
	return out, false
}

func isRowPanel(p rawPanel) bool {
	return strings.EqualFold(strings.TrimSpace(p.Type), "row")
}

func convertPanel(
	fp flatPanel,
	policy map[string]variablePolicy,
	col *diag.Collector,
	assumptions map[string]struct{},
	usedBy map[string]map[string]struct{},
	unsupportedTypes map[string]struct{},
	totalQueries *int,
	requireRunScope bool,
) (Panel, bool) {
	p := fp.panel
	loc := fp.location
	title := strings.TrimSpace(p.Title)
	panelType := strings.ToLower(strings.TrimSpace(p.Type))
	if len(title) > MaxTitleBytes {
		col.Add(diag.Blocker, "panel", loc, fmt.Sprintf("panel title exceeds the limit of %d bytes", MaxTitleBytes))
		return Panel{}, false
	}
	if len(fp.rowTitle) > MaxTitleBytes {
		col.Add(diag.Blocker, "row", loc, fmt.Sprintf("row title exceeds the limit of %d bytes", MaxTitleBytes))
		return Panel{}, false
	}
	if len(panelType) > MaxTypeBytes {
		col.Add(diag.Blocker, "panel", loc, fmt.Sprintf("panel type exceeds the limit of %d bytes", MaxTypeBytes))
		return Panel{}, false
	}

	kind, supported := panelKind(panelType)
	if !supported {
		unsupportedTypes[panelType] = struct{}{}
		col.Add(diag.Warning, "panel", panelLocation(loc, title),
			fmt.Sprintf("panel type %q is not supported; APEX renders timeseries/graph, stat/gauge, table, and heatmap panels", p.Type))
		return Panel{}, false
	}

	// Panel-level datasource policy.
	if len(p.Datasource) > MaxDatasourceJSONBytes {
		col.Add(diag.Blocker, "panel datasource", panelLocation(loc, title), "datasource metadata exceeds its size limit")
		return Panel{}, false
	}
	panelDS := parseDatasource(p.Datasource)
	if len(panelDS.UID) > MaxDatasourceBytes || len(panelDS.Type) > MaxTypeBytes {
		col.Add(diag.Blocker, "panel datasource", panelLocation(loc, title), "datasource type or uid exceeds its size limit")
		return Panel{}, false
	}
	if !datasourceAllowed(panelDS, "panel", loc, title, col, assumptions) {
		return Panel{}, false
	}

	unit := resolvePanelUnit(p, loc, title, col, assumptions)
	thresholds := resolvePanelThresholds(p, loc, title, col)
	warnDroppedPanelConstructs(p, loc, title, col)

	targets := []Target{}
	targetInputs := p.Targets
	if len(targetInputs) > MaxTargetsPerPanel {
		col.Add(diag.Blocker, "panel", panelLocation(loc, title),
			fmt.Sprintf("panel declares %d target entries, exceeding the per-panel limit of %d", len(targetInputs), MaxTargetsPerPanel))
		targetInputs = targetInputs[:MaxTargetsPerPanel]
	}
	for ti, rt := range targetInputs {
		if rt.Hide {
			col.Add(diag.Info, "target", targetLocation(loc, ti, rt.RefID), "hidden target skipped")
			continue
		}
		target, ok := convertTarget(rt, loc, ti, title, policy, col, assumptions, usedBy, requireRunScope)
		if !ok {
			continue
		}
		targets = append(targets, target)
		*totalQueries++
	}

	if len(targets) == 0 {
		col.Add(diag.Warning, "panel", panelLocation(loc, title), "panel has no importable Prometheus query and was excluded")
		return Panel{}, false
	}
	if kind == PanelKindSingleValue && len(targets) != 1 {
		col.Add(diag.Blocker, "panel", panelLocation(loc, title), "stat/gauge panels must contain exactly one visible Prometheus target; APEX renders one deterministic value per tile")
		return Panel{}, false
	}

	return Panel{
		PanelID:       p.ID,
		Title:         convertutil.FirstNonEmpty(title, fmt.Sprintf("Panel %d", p.ID)),
		Kind:          kind,
		Unit:          unit,
		GridPos:       normalizeGridPos(p.GridPos, loc, title, col),
		Targets:       targets,
		RowTitle:      fp.rowTitle,
		DatasourceUID: datasourceUIDForStorage(panelDS),
		Thresholds:    thresholds,
	}, true
}

func resolvePanelThresholds(p rawPanel, loc, title string, col *diag.Collector) []PanelThreshold {
	raw := p.FieldConfig.Defaults.Thresholds
	if len(raw.Steps) == 0 {
		return nil
	}
	if mode := strings.ToLower(strings.TrimSpace(raw.Mode)); mode != "" && mode != "absolute" {
		col.Add(diag.Warning, "panel thresholds", panelLocation(loc, title), fmt.Sprintf("threshold mode %q is not evaluated; only absolute thresholds are supported", raw.Mode))
		return nil
	}
	if len(raw.Steps) > MaxThresholdSteps {
		col.Add(diag.Blocker, "panel thresholds", panelLocation(loc, title), fmt.Sprintf("panel declares more than %d threshold steps", MaxThresholdSteps))
		raw.Steps = raw.Steps[:MaxThresholdSteps]
	}
	out := make([]PanelThreshold, 0, len(raw.Steps))
	for _, step := range raw.Steps {
		color := strings.ToLower(strings.TrimSpace(step.Color))
		if color == "" {
			color = "neutral"
		}
		if len(color) > MaxThresholdColorBytes {
			col.Add(diag.Warning, "panel thresholds", panelLocation(loc, title), "threshold color exceeds its size limit and was normalized to neutral")
			color = "neutral"
		}
		severity := thresholdSeverity(color)
		if severity == "unknown" {
			col.Add(diag.Warning, "panel thresholds", panelLocation(loc, title), fmt.Sprintf("threshold color %q has no SLA severity mapping; its line is rendered neutral and it does not affect the run verdict", color))
		}
		out = append(out, PanelThreshold{Value: step.Value, Color: color, Severity: severity})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Value == nil && out[j].Value == nil {
			return false
		}
		if out[i].Value == nil {
			return true
		}
		if out[j].Value == nil {
			return false
		}
		return *out[i].Value < *out[j].Value
	})
	return out
}

func thresholdSeverity(color string) string {
	color = strings.ToLower(strings.TrimSpace(color))
	switch color {
	case "red", "dark-red", "semi-dark-red", "super-light-red":
		return "critical"
	case "orange", "yellow", "dark-orange", "semi-dark-orange", "dark-yellow", "semi-dark-yellow":
		return "warning"
	case "green", "dark-green", "semi-dark-green", "blue", "dark-blue":
		return "normal"
	}
	if strings.HasSuffix(color, "-red") {
		return "critical"
	}
	if strings.HasSuffix(color, "-orange") || strings.HasSuffix(color, "-yellow") {
		return "warning"
	}
	if strings.HasSuffix(color, "-green") || strings.HasSuffix(color, "-blue") {
		return "normal"
	}
	return "unknown"
}

func warnDroppedPanelConstructs(p rawPanel, loc, title string, col *diag.Collector) {
	location := panelLocation(loc, title)
	if trimmed := strings.TrimSpace(string(p.Repeat)); trimmed != "" && trimmed != "null" && trimmed != `""` {
		col.Add(diag.Warning, "panel repeat", location, "panel.repeat is not reproduced; one panel is imported without repetition")
	}
	if len(p.Links) > 0 || len(p.FieldConfig.Defaults.Links) > 0 {
		col.Add(diag.Info, "panel links", location, "panel and data links are not reproduced")
	}
	if len(p.FieldConfig.Defaults.Mappings) > 0 {
		col.Add(diag.Info, "value mappings", location, "value mappings are not reproduced; raw numeric values are rendered")
	}
	if len(p.Transformations) > 0 {
		col.Add(diag.Warning, "transformations", location, "Grafana transformations are not reproduced; datasource results are rendered directly")
	}
}

func convertTarget(
	rt rawTarget,
	panelLoc string,
	index int,
	panelTitle string,
	policy map[string]variablePolicy,
	col *diag.Collector,
	assumptions map[string]struct{},
	usedBy map[string]map[string]struct{},
	requireRunScope bool,
) (Target, bool) {
	baseLoc := fmt.Sprintf("%s.targets[%d]", panelLoc, index)
	if len(strings.TrimSpace(rt.RefID)) > MaxRefIDBytes {
		col.Add(diag.Blocker, "target", baseLoc, fmt.Sprintf("refId exceeds the limit of %d bytes", MaxRefIDBytes))
		return Target{}, false
	}
	if len(strings.TrimSpace(rt.LegendFormat)) > MaxLegendBytes {
		col.Add(diag.Blocker, "target", baseLoc, fmt.Sprintf("legend format exceeds the limit of %d bytes", MaxLegendBytes))
		return Target{}, false
	}
	loc := targetLocation(panelLoc, index, rt.RefID)
	expr := strings.TrimSpace(rt.Expr)

	if expr == "" {
		if strings.TrimSpace(rt.RawSQL) != "" {
			col.Add(diag.Warning, "target", loc, "target uses a SQL datasource (rawSql), not Prometheus; excluded")
		} else {
			col.Add(diag.Warning, "target", loc, "target has no Prometheus expr; excluded")
		}
		return Target{}, false
	}

	// Per-target datasource policy (mixed-datasource panels).
	if len(rt.Datasource) > MaxDatasourceJSONBytes {
		col.Add(diag.Blocker, "target datasource", loc, "datasource metadata exceeds its size limit")
		return Target{}, false
	}
	targetDS := parseDatasource(rt.Datasource)
	if len(targetDS.UID) > MaxDatasourceBytes || len(targetDS.Type) > MaxTypeBytes {
		col.Add(diag.Blocker, "target datasource", loc, "datasource type or uid exceeds its size limit")
		return Target{}, false
	}
	if !datasourceAllowed(targetDS, "target", panelLoc, panelTitle, col, assumptions) {
		col.Add(diag.Warning, "target", loc, "target datasource is not Prometheus; excluded")
		return Target{}, false
	}

	if len(expr) > MaxExprBytes {
		col.Add(diag.Blocker, "target", loc, fmt.Sprintf("query length %d exceeds the limit of %d bytes", len(expr), MaxExprBytes))
		return Target{}, false
	}
	if bad := firstDisallowedControlChar(expr); bad != "" {
		col.Add(diag.Blocker, "target", loc, "query contains a disallowed control character ("+bad+")")
		return Target{}, false
	}
	if hasMatchAllSelector(expr) {
		col.Add(diag.Blocker, "target", loc, "query uses a match-all selector ({} or {__name__=~\".+\"}), which would scan every series")
		return Target{}, false
	}
	if requireRunScope && !IsRunScopedExpression(expr) {
		col.Add(diag.Blocker, "target", loc, "query is not run-scoped: every vector selector must use an exact test_id=\"$test_id\" or project_id=\"$project_id\" label matcher")
		return Target{}, false
	}

	// Variable references.
	ok := true
	for _, ref := range scanExprVariables(expr) {
		if !checkVariableReference(ref, loc, policy, col, assumptions, usedBy) {
			ok = false
		}
	}
	if !ok {
		return Target{}, false
	}

	return Target{
		RefID:         strings.TrimSpace(rt.RefID),
		Expr:          expr,
		LegendFormat:  strings.TrimSpace(rt.LegendFormat),
		LabelFields:   legendLabelFields(rt.LegendFormat),
		DatasourceUID: datasourceUIDForStorage(targetDS),
	}, true
}

// checkVariableReference validates one variable reference inside a query against the
// declared-variable policy and the built-in/reserved/rejected macro sets. It returns
// false (and records a blocker) for anything that cannot be resolved safely.
func checkVariableReference(
	ref exprVarRef,
	loc string,
	policy map[string]variablePolicy,
	col *diag.Collector,
	assumptions map[string]struct{},
	usedBy map[string]map[string]struct{},
) bool {
	name := ref.Name
	if ref.Modifier != "" {
		col.Add(diag.Blocker, "target", loc,
			fmt.Sprintf("variable format modifier ${%s:%s} is not supported; escaping semantics vary and could inject a query", name, ref.Modifier))
		return false
	}
	// Built-in time macros.
	if _, ok := builtinTimeMacros[name]; ok || strings.HasPrefix(name, "__auto_interval_") {
		if name == "__rate_interval" {
			assumptions["$__rate_interval resolved to max(4×step, 60s); Grafana derives it from the datasource scrape interval, which APEX cannot observe."] = struct{}{}
		}
		return true
	}
	if reason, rejected := rejectedGlobals[name]; rejected {
		col.Add(diag.Blocker, "target", loc, fmt.Sprintf("variable $%s is not supported: %s", name, reason))
		return false
	}
	// Reserved run-scoped auto-bindings.
	if name == reservedTestID || name == reservedProjectID {
		assumptions[fmt.Sprintf("$%s binds to the live run's %s at query time.", name, name)] = struct{}{}
		trackUsedBy(usedBy, name, loc)
		return true
	}
	pol, declared := policy[name]
	if !declared {
		col.Add(diag.Blocker, "target", loc, fmt.Sprintf("query references undeclared variable $%s", name))
		return false
	}
	trackUsedBy(usedBy, name, loc)
	if pol.kind == BindingRejected {
		col.Add(diag.Blocker, "target", loc, fmt.Sprintf("query uses variable $%s, which cannot be imported: %s", name, pol.reason))
		return false
	}
	return true
}

func panelKind(panelType string) (string, bool) {
	switch panelType {
	case "timeseries", "graph":
		return PanelKindLine, true
	case "stat", "gauge", "singlestat":
		return PanelKindSingleValue, true
	case "table":
		return PanelKindTable, true
	case "heatmap":
		return PanelKindHeatmap, true
	default:
		return "", false
	}
}

func resolvePanelUnit(p rawPanel, loc, title string, col *diag.Collector, assumptions map[string]struct{}) string {
	unitID := strings.TrimSpace(p.FieldConfig.Defaults.Unit)
	if len(unitID) > MaxUnitIDBytes {
		col.Add(diag.Blocker, "panel unit", panelLocation(loc, title), fmt.Sprintf("unit id exceeds the limit of %d bytes", MaxUnitIDBytes))
		return ""
	}
	disp := mapUnit(unitID)
	if unitID != "" && !disp.Recognized {
		col.Add(diag.Info, "panel unit", panelLocation(loc, title),
			fmt.Sprintf("Grafana unit %q has no APEX equivalent; the panel renders unitless", unitID))
	}
	if disp.Note != "" {
		assumptions[disp.Note] = struct{}{}
	}
	if len(p.FieldConfig.Overrides) > MaxOverridesPerPanel {
		col.Add(diag.Blocker, "field overrides", panelLocation(loc, title), fmt.Sprintf("panel declares more than %d field overrides", MaxOverridesPerPanel))
		return disp.Label
	}
	for _, override := range p.FieldConfig.Overrides {
		if len(override.Properties) > MaxOverrideProperties {
			col.Add(diag.Blocker, "field overrides", panelLocation(loc, title), fmt.Sprintf("field override declares more than %d properties", MaxOverrideProperties))
			return disp.Label
		}
	}
	if overrideSetsUnit(p.FieldConfig.Overrides) {
		col.Add(diag.Info, "panel unit", panelLocation(loc, title), "per-series unit overrides are not applied; the panel default unit is used")
	}
	return disp.Label
}

func overrideSetsUnit(overrides []rawOverride) bool {
	for _, o := range overrides {
		for _, prop := range o.Properties {
			if prop.ID == "unit" {
				return true
			}
		}
	}
	return false
}

func normalizeGridPos(g rawGridPos, loc, title string, col *diag.Collector) GridPos {
	out := GridPos{X: g.X, Y: g.Y, W: g.W, H: g.H}
	clamped := false
	if out.W <= 0 {
		out.W = 24
		clamped = true
	}
	if out.W > 24 {
		out.W = 24
		clamped = true
	}
	if out.X < 0 || out.X >= 24 {
		out.X = 0
		clamped = true
	}
	if out.X+out.W > 24 {
		out.W = 24 - out.X
		clamped = true
	}
	if out.Y < 0 {
		out.Y = 0
		clamped = true
	}
	if out.Y > MaxGridY {
		out.Y = MaxGridY
		clamped = true
	}
	if out.H <= 0 {
		out.H = 8
		clamped = true
	}
	if out.H > MaxGridHeight {
		out.H = MaxGridHeight
		clamped = true
	}
	if clamped {
		col.Add(diag.Info, "panel layout", panelLocation(loc, title), "grid position was out of range and was clamped to the 24-column grid")
	}
	return out
}

func legendLabelFields(legend string) []string {
	matches := legendTokenPattern.FindAllStringSubmatch(legend, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, m := range matches {
		field := m[1]
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}

var (
	matchAllNamePattern     = regexp.MustCompile(`\{\s*__name__\s*=~\s*"\.[\*\+]"\s*\}`)
	bareEmptySelector       = regexp.MustCompile(`(^|[^A-Za-z0-9_:\])"'])\{\s*\}`)
	runScopeMatcher         = regexp.MustCompile(`^\s*(project_id|test_id)\s*=\s*"(\$project_id|\$\{project_id\}|\[\[project_id\]\]|\$test_id|\$\{test_id\}|\[\[test_id\]\])"\s*$`)
	resolvedRunScopeMatcher = regexp.MustCompile(`^\s*(project_id|test_id)\s*=\s*"([^"\\]*)"\s*$`)
)

func hasMatchAllSelector(expr string) bool {
	return bareEmptySelector.MatchString(expr) || matchAllNamePattern.MatchString(expr)
}

// IsRunScopedExpression is the query-time defense for persisted/legacy dashboard
// rows. Import validation uses the same parser, so storage corruption or an older
// importer cannot bypass project/run scoping at execution.
func IsRunScopedExpression(expr string) bool {
	return isRunScopedExpression(expr, selectorHasRunScope, true)
}

// IsRunScopedResolvedExpression validates the concrete PromQL produced after
// template substitution. Every vector selector must retain an exact matcher for the
// authorized test or project.
func IsRunScopedResolvedExpression(expr, testID, projectID string) bool {
	testID = strings.TrimSpace(testID)
	projectID = strings.TrimSpace(projectID)
	if testID == "" && projectID == "" {
		return false
	}
	return isRunScopedExpression(expr, func(selector string) bool {
		for _, matcher := range splitSelectorMatchers(selector) {
			parts := resolvedRunScopeMatcher.FindStringSubmatch(matcher)
			if len(parts) != 3 {
				continue
			}
			if parts[1] == reservedTestID && testID != "" && parts[2] == testID {
				return true
			}
			if parts[1] == reservedProjectID && projectID != "" && parts[2] == projectID {
				return true
			}
		}
		return false
	}, false)
}

func isRunScopedExpression(expr string, selectorScoped func(string) bool, allowTemplateTokens bool) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}
	sawSelector := false
	for i := 0; i < len(expr); {
		switch {
		case expr[i] == '"':
			i = skipPromQLString(expr, i) + 1
		case expr[i] == '{':
			end := selectorEnd(expr, i+1)
			if end < 0 || !selectorScoped(expr[i+1:end]) {
				return false
			}
			sawSelector = true
			i = end + 1
		case expr[i] == '$':
			if !allowTemplateTokens {
				return false
			}
			end := skipTemplateToken(expr, i)
			if end <= i || templateTokenName(expr[i:end]) == "" {
				return false
			}
			i = end
		case expr[i] == '[' && i+1 < len(expr) && expr[i+1] == '[':
			if !allowTemplateTokens {
				return false
			}
			if relativeEnd := strings.Index(expr[i+2:], "]]"); relativeEnd >= 0 {
				end := i + relativeEnd + 4
				name := strings.TrimSpace(expr[i+2 : end-2])
				if name == "" {
					return false
				}
				i = end
			} else {
				return false
			}
		case isPromIdentifierStart(expr[i]):
			start := i
			for i < len(expr) && isPromIdentifierPart(expr[i]) {
				i++
			}
			identifier := expr[start:i]
			next := nextNonSpace(expr, i)
			if groupingKeyword(identifier) && next < len(expr) && expr[next] == '(' {
				end := balancedParenEnd(expr, next)
				if end < 0 {
					return false
				}
				i = end + 1
				continue
			}
			if aggregationKeyword(identifier) {
				if next < len(expr) && (expr[next] == '(' || followedByGroupingKeyword(expr[next:])) {
					continue
				}
				return false
			}
			if promKeyword(identifier) || (next < len(expr) && (expr[next] == '(' || expr[next] == '{')) {
				continue
			}
			// Any remaining identifier is a bare vector selector. Bare selectors cannot
			// carry the mandatory project/run matcher and are therefore rejected.
			return false
		case expr[i] >= '0' && expr[i] <= '9':
			for i < len(expr) && (isPromIdentifierPart(expr[i]) || expr[i] == '.') {
				i++
			}
		default:
			i++
		}
	}
	return sawSelector
}

func selectorHasRunScope(selector string) bool {
	for _, matcher := range splitSelectorMatchers(selector) {
		parts := runScopeMatcher.FindStringSubmatch(matcher)
		if len(parts) != 3 {
			continue
		}
		if strings.Trim(parts[2], "${}[]") == parts[1] {
			return true
		}
	}
	return false
}

func isPromIdentifierStart(ch byte) bool {
	return ch == '_' || ch == ':' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isPromIdentifierPart(ch byte) bool {
	return isPromIdentifierStart(ch) || ch >= '0' && ch <= '9'
}

func nextNonSpace(expr string, i int) int {
	for i < len(expr) && (expr[i] == ' ' || expr[i] == '\t' || expr[i] == '\r' || expr[i] == '\n') {
		i++
	}
	return i
}

func groupingKeyword(identifier string) bool {
	switch strings.ToLower(identifier) {
	case "by", "without", "on", "ignoring", "group_left", "group_right":
		return true
	default:
		return false
	}
}

func promKeyword(identifier string) bool {
	switch strings.ToLower(identifier) {
	case "and", "or", "unless", "bool", "offset", "group_left", "group_right", "atan2", "inf", "nan":
		return true
	default:
		return false
	}
}

func aggregationKeyword(identifier string) bool {
	switch strings.ToLower(identifier) {
	case "sum", "min", "max", "avg", "group", "stddev", "stdvar", "count", "count_values", "bottomk", "topk", "quantile", "limitk", "limit_ratio":
		return true
	default:
		return false
	}
}

func followedByGroupingKeyword(expr string) bool {
	for _, keyword := range []string{"by", "without"} {
		if strings.HasPrefix(expr, keyword) && (len(expr) == len(keyword) || !isPromIdentifierPart(expr[len(keyword)])) {
			return true
		}
	}
	return false
}

func balancedParenEnd(expr string, open int) int {
	depth := 0
	for i := open; i < len(expr); i++ {
		if expr[i] == '"' {
			i = skipPromQLString(expr, i)
			continue
		}
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func skipTemplateToken(expr string, start int) int {
	if start+1 < len(expr) && expr[start+1] == '{' {
		if end := strings.IndexByte(expr[start+2:], '}'); end >= 0 {
			return start + 3 + end
		}
	}
	i := start + 1
	for i < len(expr) && isPromIdentifierPart(expr[i]) {
		i++
	}
	return i
}

func templateTokenName(token string) string {
	token = strings.TrimSpace(token)
	if strings.HasPrefix(token, "${") && strings.HasSuffix(token, "}") {
		return strings.TrimSpace(token[2 : len(token)-1])
	}
	return strings.TrimPrefix(token, "$")
}

func skipPromQLString(expr string, quote int) int {
	escaped := false
	for i := quote + 1; i < len(expr); i++ {
		if escaped {
			escaped = false
			continue
		}
		if expr[i] == '\\' {
			escaped = true
			continue
		}
		if expr[i] == '"' {
			return i
		}
	}
	return len(expr) - 1
}

func selectorEnd(expr string, start int) int {
	for i := start; i < len(expr); i++ {
		if expr[i] == '"' {
			i = skipPromQLString(expr, i)
			continue
		}
		if expr[i] == '}' {
			return i
		}
	}
	return -1
}

func splitSelectorMatchers(selector string) []string {
	parts := []string{}
	start := 0
	for i := 0; i < len(selector); i++ {
		if selector[i] == '"' {
			i = skipPromQLString(selector, i)
			continue
		}
		if selector[i] == ',' {
			parts = append(parts, selector[start:i])
			start = i + 1
		}
	}
	return append(parts, selector[start:])
}

func firstDisallowedControlChar(expr string) string {
	for _, r := range expr {
		if r == '\n' || r == '\t' {
			continue
		}
		if r == 0 {
			return "NUL"
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Sprintf("U+%04X", r)
		}
	}
	return ""
}

func datasourceAllowed(ds datasourceRef, scope, loc, title string, col *diag.Collector, assumptions map[string]struct{}) bool {
	if !ds.present {
		assumptions["Panels with no explicit datasource are bound to the APEX-configured Prometheus."] = struct{}{}
		return true
	}
	if ds.variable {
		col.Add(diag.Warning, scope+" datasource", panelLocation(loc, title),
			"datasource is a dashboard variable; bound to the APEX-configured Prometheus")
		return true
	}
	dsType := strings.ToLower(strings.TrimSpace(ds.Type))
	uidLower := strings.ToLower(strings.TrimSpace(ds.UID))
	// Grafana built-in defaults, or an explicit Prometheus type/name.
	if dsType == "prometheus" || dsType == "" && isDefaultDatasourceName(uidLower) {
		if dsType == "prometheus" && ds.UID != "" {
			assumptions["Datasource UIDs are retained: direct-Prometheus mode ignores them, while Grafana-proxy mode routes each target by UID."] = struct{}{}
		}
		return true
	}
	if dsType == "" {
		// Bare string name that is not a known default: retain it as a proxy UID,
		// while direct-Prometheus mode safely ignores it.
		assumptions["Named datasource references are retained for Grafana-proxy routing and ignored in direct-Prometheus mode."] = struct{}{}
		return true
	}
	col.Add(diag.Warning, scope+" datasource", panelLocation(loc, title),
		fmt.Sprintf("datasource type %q requires the Grafana proxy backend; direct-Prometheus mode will return a panel error", ds.Type))
	return true
}

func isDefaultDatasourceName(uidLower string) bool {
	switch uidLower {
	case "", "default", "-- grafana --", "${datasource}", "prometheus":
		return true
	default:
		return false
	}
}

func datasourceUIDForStorage(ds datasourceRef) string {
	if !ds.present || ds.variable {
		return ""
	}
	return strings.TrimSpace(ds.UID)
}

func trackUsedBy(usedBy map[string]map[string]struct{}, name, loc string) {
	if usedBy[name] == nil {
		usedBy[name] = map[string]struct{}{}
	}
	usedBy[name][loc] = struct{}{}
}

func panelLocation(loc, title string) string {
	if title == "" {
		return loc
	}
	return fmt.Sprintf("%s %q", loc, title)
}

func targetLocation(panelLoc string, index int, refID string) string {
	refID = strings.TrimSpace(refID)
	if refID == "" {
		return fmt.Sprintf("%s.targets[%d]", panelLoc, index)
	}
	return fmt.Sprintf("%s.targets[%d] (%s)", panelLoc, index, refID)
}

func sortedKeys[T any](m map[string]T) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
