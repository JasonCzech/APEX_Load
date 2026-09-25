package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/grafana"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
)

// DashboardPanel is one resolved imported-dashboard panel. Line panels carry Series;
// single-value tiles carry Value. Error is set (and the panel is otherwise empty) when
// that panel's query failed — the resolve is fail-soft so one bad panel never blanks
// the whole dashboard.
type DashboardPanel struct {
	ComparisonIdentity string                   `json:"comparison_identity,omitempty"`
	PanelID            int                      `json:"panel_id"`
	Title              string                   `json:"title"`
	Kind               string                   `json:"kind"`
	Unit               string                   `json:"unit,omitempty"`
	GridPos            grafana.GridPos          `json:"grid_pos"`
	RowTitle           string                   `json:"row_title,omitempty"`
	Series             []PrometheusSeries       `json:"series,omitempty"`
	Value              *float64                 `json:"value,omitempty"`
	Error              string                   `json:"error,omitempty"`
	Thresholds         []grafana.PanelThreshold `json:"thresholds,omitempty"`
	ThresholdEvaluated bool                     `json:"threshold_evaluated,omitempty"`
	ThresholdBreached  bool                     `json:"threshold_breached,omitempty"`
	ThresholdDetail    string                   `json:"threshold_detail,omitempty"`
}

// DashboardPanelsResult is the resolved-panel set for one dashboard over a window.
type DashboardPanelsResult struct {
	DashboardID    string           `json:"dashboard_id"`
	DashboardTitle string           `json:"dashboard_title,omitempty"`
	DatasourceID   string           `json:"datasource_id,omitempty"`
	Panels         []DashboardPanel `json:"panels"`
}

// AllPanelsFailed reports whether every panel errored — the condition under which the
// handler surfaces a datasource-level failure (502) rather than a 200 with per-panel
// errors.
func (r DashboardPanelsResult) AllPanelsFailed() bool {
	if len(r.Panels) == 0 {
		return false
	}
	for _, p := range r.Panels {
		if p.Error == "" {
			return false
		}
	}
	return true
}

// dashboardResolvePlan carries everything needed to resolve one stored dashboard's
// panels over a window.
type dashboardResolvePlan struct {
	dashboardID string
	panels      []grafana.Panel
	resolution  grafana.QueryResolution
	start       time.Time
	end         time.Time
	stepSeconds int
	// perQueryTimeoutSeconds is the selected datasource timeout; the whole-dashboard
	// budget is derived from it.
	perQueryTimeoutSeconds int
	aggregatePointsUsed    *atomic.Int64
	aggregatePointsMax     int64
	aggregateBytesUsed     *atomic.Int64
	aggregateBytesMax      int64
	querySemaphore         chan struct{}
}

// resolveDashboardPanels runs every panel's queries against the datasource with bounded
// concurrency, a per-panel series cap, an aggregate sample budget, and a whole-dashboard
// timeout. It is fail-soft: a panel that errors carries its message and the rest still
// resolve.
func resolveDashboardPanels(ctx context.Context, ds graphDataSource, plan dashboardResolvePlan) []DashboardPanel {
	ctx, cancel := context.WithTimeout(ctx, dashboardTotalTimeout(plan.perQueryTimeoutSeconds))
	defer cancel()

	resolution := plan.resolution
	resolution.StepSeconds = plan.stepSeconds
	resolution.RangeSeconds = int(plan.end.Sub(plan.start).Seconds())

	var pointsUsed atomic.Int64
	var bytesUsed atomic.Int64
	sem := plan.querySemaphore
	if sem == nil {
		sem = make(chan struct{}, maxDashboardQueryConcurrency)
	}
	out := make([]DashboardPanel, len(plan.panels))
	var wg sync.WaitGroup
	for i, panel := range plan.panels {
		wg.Add(1)
		go func(i int, panel grafana.Panel) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				out[i] = failedPanel(panel, "dashboard query budget exhausted before this panel ran")
				return
			}
			defer func() { <-sem }()
			out[i] = resolveOnePanel(ctx, ds, plan, resolution, panel, &pointsUsed, &bytesUsed)
		}(i, panel)
	}
	wg.Wait()
	return out
}

func resolveOnePanel(
	ctx context.Context,
	ds graphDataSource,
	plan dashboardResolvePlan,
	resolution grafana.QueryResolution,
	panel grafana.Panel,
	pointsUsed *atomic.Int64,
	bytesUsed *atomic.Int64,
) DashboardPanel {
	result := DashboardPanel{
		ComparisonIdentity: comparisonDigest(panel.Targets), PanelID: panel.PanelID,
		Title:      panel.Title,
		Kind:       panel.Kind,
		Unit:       panel.Unit,
		GridPos:    panel.GridPos,
		RowTitle:   panel.RowTitle,
		Thresholds: panel.Thresholds,
	}
	keyPrefix := dashboardPanelKeyPrefix(plan.dashboardID, panel.PanelID)
	allSeries := make([]PrometheusSeries, 0, len(panel.Targets))
	seriesUsed := 0
	for ti, target := range panel.Targets {
		if (strings.TrimSpace(resolution.ProjectID) != "" || strings.TrimSpace(resolution.TestID) != "") && !grafana.IsRunScopedExpression(target.Expr) {
			result.Error = "stored query is not run-scoped and was blocked before datasource execution"
			return result
		}
		expr, err := grafana.ResolvePanelExpr(target.Expr, resolution)
		if err != nil {
			result.Error = dashboardPanelError(err)
			return result
		}
		// A binding is deliberately constrained to a small character set, but some
		// combinations (for example "foo+bar" in a scalar position) can still alter
		// PromQL structure. Re-parse the concrete expression so a safe stored template
		// cannot turn into an unscoped selector after substitution.
		if (strings.TrimSpace(resolution.ProjectID) != "" || strings.TrimSpace(resolution.TestID) != "") && !grafana.IsRunScopedResolvedExpression(expr, resolution.TestID, resolution.ProjectID) {
			result.Error = "resolved query is not run-scoped and was blocked before datasource execution"
			return result
		}
		remainingSeries := maxDashboardSeriesPerPanel - seriesUsed
		if remainingSeries <= 0 {
			result.Error = fmt.Sprintf("panel exceeded its aggregate series limit of %d", maxDashboardSeriesPerPanel)
			return result
		}
		series, err := ds.QueryRange(ctx, graphRangeQuery{
			Expr:            expr,
			DatasourceUID:   firstNonEmptyString(target.DatasourceUID, panel.DatasourceUID),
			Start:           plan.start,
			End:             plan.end,
			StepSeconds:     plan.stepSeconds,
			MaxSeries:       remainingSeries,
			MaxBytes:        maxImportedDashboardQueryResponseBytes,
			LabelFields:     target.LabelFields,
			SeriesKeyPrefix: fmt.Sprintf("%s_%s", keyPrefix, targetKeySuffix(target, ti)),
		})
		if err != nil {
			result.Error = dashboardPanelError(err)
			return result
		}
		for i := range series {
			kind := baselineCapturedMetricKind(target.Expr, series[i].Labels["__name__"])
			series[i].ComparisonDefinition = &BaselineMetricDefinition{Identity: comparisonDigest(target), Unit: panel.Unit, Kind: kind, Source: "application", Aggregation: "recorded query"}
		}
		seriesUsed += len(series)
		seriesBytes := estimatePrometheusSeriesBytes(series)
		if !reservePoints(bytesUsed, seriesBytes, maxDashboardBytesTotal) {
			result.Error = fmt.Sprintf("dashboard exceeded its aggregate decoded-byte budget of %d", maxDashboardBytesTotal)
			return result
		}
		if plan.aggregateBytesUsed != nil && !reservePoints(plan.aggregateBytesUsed, seriesBytes, plan.aggregateBytesMax) {
			result.Error = fmt.Sprintf("imported dashboards exceeded their aggregate decoded-byte budget of %d", plan.aggregateBytesMax)
			return result
		}
		if !reserveDashboardPoints(pointsUsed, int64(countSeriesPoints(series))) {
			result.Error = fmt.Sprintf("dashboard exceeded its aggregate sample budget of %d; widen the step or narrow the range", maxDashboardPointsTotal)
			return result
		}
		if plan.aggregatePointsUsed != nil && !reservePoints(plan.aggregatePointsUsed, int64(countSeriesPoints(series)), plan.aggregatePointsMax) {
			result.Error = fmt.Sprintf("imported dashboards exceeded their aggregate sample budget of %d; widen the step or narrow the range", plan.aggregatePointsMax)
			return result
		}
		allSeries = append(allSeries, series...)
	}
	if panel.Kind == grafana.PanelKindSingleValue {
		if len(allSeries) > 1 {
			result.Error = "single-value panel returned multiple series; aggregate the query to one series"
			return result
		}
		result.Value = latestSeriesValue(allSeries)
		// Retain a bounded tail for the stat-tile sparkline. Query/resource budgets
		// were already enforced above; trimming here also keeps terminal snapshots
		// compact without issuing a second datasource request.
		result.Series = trimPrometheusSeriesTail(allSeries, 120)
	} else {
		result.Series = allSeries
	}
	result.ThresholdEvaluated, result.ThresholdBreached, result.ThresholdDetail = evaluatePanelThresholds(panel.Thresholds, allSeries)
	return result
}

func evaluatePanelThresholds(thresholds []grafana.PanelThreshold, series []PrometheusSeries) (bool, bool, string) {
	if len(thresholds) == 0 {
		return false, false, ""
	}
	evaluated := false
	for _, item := range series {
		for _, point := range item.Values {
			evaluated = true
			var active *grafana.PanelThreshold
			for _, step := range thresholds {
				if step.Value == nil || point.Value >= *step.Value {
					stepCopy := step
					active = &stepCopy
				}
			}
			if active != nil && active.Severity == "critical" {
				return true, true, fmt.Sprintf("%s entered critical threshold range at %g", item.Label, point.Value)
			}
		}
	}
	return evaluated, false, ""
}

func trimPrometheusSeriesTail(series []PrometheusSeries, limit int) []PrometheusSeries {
	if limit <= 0 || len(series) == 0 {
		return nil
	}
	out := make([]PrometheusSeries, len(series))
	for i, item := range series {
		out[i] = item
		out[i].Labels = cloneStringMap(item.Labels)
		start := 0
		if len(item.Values) > limit {
			start = len(item.Values) - limit
		}
		out[i].Values = append([]PrometheusPoint(nil), item.Values[start:]...)
	}
	return out
}

func dashboardPanelError(err error) string {
	if err == nil {
		return ""
	}
	return boundedtext.TruncateUTF8(err.Error(), maxDashboardPanelErrorBytes)
}

// estimatePrometheusSeriesBytes conservatively accounts for the retained decoded
// representation and its eventual JSON snapshot. The upstream byte cap bounds the
// transient response; this estimate bounds what remains after decoding.
func estimatePrometheusSeriesBytes(series []PrometheusSeries) int64 {
	var total int64
	for _, item := range series {
		total += 256 + int64(len(item.Key)+len(item.Label))*2
		for key, value := range item.Labels {
			total += 128 + int64(len(key)+len(value))*2
		}
		total += int64(len(item.Values)) * 128
	}
	return total
}

func reserveDashboardPoints(used *atomic.Int64, points int64) bool {
	return reservePoints(used, points, maxDashboardPointsTotal)
}

func reservePoints(used *atomic.Int64, points, limit int64) bool {
	if points < 0 {
		return false
	}
	for {
		current := used.Load()
		if limit <= 0 || points > limit || current > limit-points {
			return false
		}
		if used.CompareAndSwap(current, current+points) {
			return true
		}
	}
}

func failedPanel(panel grafana.Panel, msg string) DashboardPanel {
	return DashboardPanel{
		PanelID:    panel.PanelID,
		Title:      panel.Title,
		Kind:       panel.Kind,
		Unit:       panel.Unit,
		GridPos:    panel.GridPos,
		RowTitle:   panel.RowTitle,
		Thresholds: panel.Thresholds,
		Error:      msg,
	}
}

// dashboardPanelKeyPrefix produces a globally-unique series-key prefix so imported
// panels can never collide with the static chartConfigs ids or with each other.
func dashboardPanelKeyPrefix(dashboardID string, panelID int) string {
	return fmt.Sprintf("gdash_%s_%d", dashboardID, panelID)
}

func targetKeySuffix(target grafana.Target, index int) string {
	if target.RefID != "" {
		return fmt.Sprintf("%s_%d", target.RefID, index)
	}
	return fmt.Sprintf("t%d", index)
}

func countSeriesPoints(series []PrometheusSeries) int {
	total := 0
	for _, s := range series {
		total += len(s.Values)
	}
	return total
}

// latestSeriesValue returns the value of the newest sample across all series, used to
// render a single-value tile from a range query.
func latestSeriesValue(series []PrometheusSeries) *float64 {
	var best *PrometheusPoint
	for si := range series {
		for pi := range series[si].Values {
			p := &series[si].Values[pi]
			if best == nil || p.TimestampMs >= best.TimestampMs {
				best = p
			}
		}
	}
	if best == nil {
		return nil
	}
	v := best.Value
	return &v
}
