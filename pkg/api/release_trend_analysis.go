package api

import (
	"sort"
	"strings"
	"time"
)

// Regression thresholds for run-over-run trend analysis. Conservative defaults
// so the trend only flags meaningful regressions, not noise.
const (
	trendErrorPctRegressionPoints = 1.0  // error rate up by >1 percentage point
	trendAvgResponseRegressionPct = 10.0 // avg response up by >10%
	trendCompletenessDropPoints   = 10.0 // completeness down by >10 points
)

// ReleaseTrendDeltas holds run-over-run changes vs the immediately prior run.
type ReleaseTrendDeltas struct {
	ErrorPctDelta       float64 `json:"error_pct_delta"`
	AvgResponseMsDelta  float64 `json:"avg_response_ms_delta"`
	AvgResponsePctDelta float64 `json:"avg_response_pct_delta"`
	PeakTPSDelta        float64 `json:"peak_tps_delta"`
	CompletenessDelta   float64 `json:"completeness_delta"`
}

// ReleaseTrendPoint is one run in the series, with deltas and regression flags
// relative to the previous run.
type ReleaseTrendPoint struct {
	RunID            string              `json:"run_id"`
	RunName          string              `json:"run_name"`
	Status           string              `json:"status"`
	CompletedAt      time.Time           `json:"completed_at"`
	Transactions     int64               `json:"transactions"`
	ErrorPct         float64             `json:"error_pct"`
	AvgResponseMs    float64             `json:"avg_response_ms"`
	PeakTPS          float64             `json:"peak_tps"`
	PeakActiveVUsers int64               `json:"peak_active_vusers"`
	CompletenessPct  float64             `json:"completeness_pct"`
	SLABreached      bool                `json:"sla_breached"`
	TelemetryStatus  string              `json:"telemetry_status,omitempty"`
	Deltas           *ReleaseTrendDeltas `json:"deltas,omitempty"`
	Regressions      []string            `json:"regressions,omitempty"`
}

// ReleaseTrend is the multi-run trend analysis for a release.
type ReleaseTrend struct {
	ReleaseID string              `json:"release_id"`
	RunCount  int                 `json:"run_count"`
	Points    []ReleaseTrendPoint `json:"points"`
	Summary   ReleaseTrendSummary `json:"summary"`
}

// ReleaseTrendSummary highlights the latest run's health and the overall
// trajectory across the series.
type ReleaseTrendSummary struct {
	LatestRegressed     bool     `json:"latest_regressed"`
	LatestRegressions   []string `json:"latest_regressions,omitempty"`
	AvgResponseTrendPct float64  `json:"avg_response_trend_pct"`
	ErrorPctTrend       float64  `json:"error_pct_trend"`
}

// buildReleaseTrend converts archived runs into an ordered trend series with
// run-over-run deltas, per-run regression flags, and a first→last trajectory.
func buildReleaseTrend(releaseID string, runs []releaseTrendRun) ReleaseTrend {
	ordered := make([]releaseTrendRun, 0, len(runs))
	for _, run := range runs {
		if run.Test == nil || (run.Report == nil && run.Point == nil) {
			continue
		}
		ordered = append(ordered, run)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return releaseTrendRunCompletedAt(ordered[i]).Before(releaseTrendRunCompletedAt(ordered[j]))
	})

	points := make([]ReleaseTrendPoint, 0, len(ordered))
	for i, run := range ordered {
		point := trendPointFromRun(run)
		if i > 0 {
			prev := points[i-1]
			point.Deltas = computeTrendDeltas(prev, point)
			point.Regressions = detectTrendRegressions(prev, point)
		}
		points = append(points, point)
	}

	trend := ReleaseTrend{
		ReleaseID: releaseID,
		RunCount:  len(points),
		Points:    points,
	}
	if len(points) > 0 {
		latest := points[len(points)-1]
		trend.Summary.LatestRegressed = len(latest.Regressions) > 0
		trend.Summary.LatestRegressions = latest.Regressions
	}
	if len(points) >= 2 {
		first := points[0]
		last := points[len(points)-1]
		if first.AvgResponseMs > 0 {
			trend.Summary.AvgResponseTrendPct = (last.AvgResponseMs - first.AvgResponseMs) / first.AvgResponseMs * 100
		}
		trend.Summary.ErrorPctTrend = last.ErrorPct - first.ErrorPct
	}
	return trend
}

func trendPointFromRun(run releaseTrendRun) ReleaseTrendPoint {
	if run.Point != nil {
		point := *run.Point
		point.Deltas = nil
		point.Regressions = nil
		return point
	}
	report := run.Report
	point := ReleaseTrendPoint{
		RunID:            run.Test.ID,
		RunName:          run.Test.Name,
		Status:           run.Test.Status,
		CompletedAt:      releaseTrendRunCompletedAt(run),
		Transactions:     report.Overview.Transactions,
		ErrorPct:         report.Overview.ErrorPct,
		AvgResponseMs:    report.Overview.AvgResponseMs,
		PeakTPS:          report.Overview.PeakTPS,
		PeakActiveVUsers: report.Overview.PeakActiveVUsers,
		CompletenessPct:  report.Completeness.ScorePct,
	}
	if report.Manifest != nil {
		point.SLABreached = report.Manifest.SLABreached
	}
	if report.Overview.Telemetry != nil {
		point.TelemetryStatus = report.Overview.Telemetry.Status
	}
	return point
}

func computeTrendDeltas(prev, cur ReleaseTrendPoint) *ReleaseTrendDeltas {
	deltas := &ReleaseTrendDeltas{
		ErrorPctDelta:      cur.ErrorPct - prev.ErrorPct,
		AvgResponseMsDelta: cur.AvgResponseMs - prev.AvgResponseMs,
		PeakTPSDelta:       cur.PeakTPS - prev.PeakTPS,
		CompletenessDelta:  cur.CompletenessPct - prev.CompletenessPct,
	}
	if prev.AvgResponseMs > 0 {
		deltas.AvgResponsePctDelta = (cur.AvgResponseMs - prev.AvgResponseMs) / prev.AvgResponseMs * 100
	}
	return deltas
}

// detectTrendRegressions flags a run as regressed vs the previous run when a key
// quality metric crosses its threshold.
func detectTrendRegressions(prev, cur ReleaseTrendPoint) []string {
	regressions := make([]string, 0, 4)
	if cur.ErrorPct-prev.ErrorPct > trendErrorPctRegressionPoints {
		regressions = append(regressions, "error rate regressed")
	}
	if prev.AvgResponseMs > 0 && (cur.AvgResponseMs-prev.AvgResponseMs)/prev.AvgResponseMs*100 > trendAvgResponseRegressionPct {
		regressions = append(regressions, "avg response time regressed")
	}
	if !prev.SLABreached && cur.SLABreached {
		regressions = append(regressions, "SLA newly breached")
	}
	if prev.CompletenessPct-cur.CompletenessPct > trendCompletenessDropPoints {
		regressions = append(regressions, "run completeness dropped")
	}
	if len(regressions) == 0 {
		return nil
	}
	return regressions
}

// RunArchiveReleaseTrendSummary is the compact, single-run slice of the multi-run
// ReleaseTrend that the executive archive report renders: this run measured against
// the immediately prior run in the same release, plus the release-wide first→last
// trajectory. It is attached at the storage-aware handler layer (which can read the
// sibling archives); buildRunArchiveReport stays a pure function of one record.
type RunArchiveReleaseTrendSummary struct {
	ReleaseID   string              `json:"release_id"`
	RunCount    int                 `json:"run_count"`
	HasBaseline bool                `json:"has_baseline"`
	Regressed   bool                `json:"regressed"`
	Regressions []string            `json:"regressions,omitempty"`
	Current     ReleaseTrendPoint   `json:"current"`
	Baseline    *ReleaseTrendPoint  `json:"baseline,omitempty"`
	Deltas      *ReleaseTrendDeltas `json:"deltas,omitempty"`
	Trajectory  ReleaseTrendSummary `json:"trajectory"`
	Note        string              `json:"note,omitempty"`
}

// buildRunArchiveReleaseTrendSummary distills a full ReleaseTrend down to the
// compact this-run-vs-prior-run view for runID. The prior run is the point that
// immediately precedes runID in completion-time order, so the deltas/regression
// flags are exactly those the multi-run analysis already computed for this run.
//
// Returns nil when runID is empty or is not represented in the trend series (e.g.
// the run has no release association, or its sibling archives were unreadable), so
// callers can treat a nil summary as "no release-over-release trend available".
// The first archived run in a release has no baseline; that case returns a non-nil
// summary with HasBaseline=false and an explanatory Note.
func buildRunArchiveReleaseTrendSummary(trend ReleaseTrend, runID string) *RunArchiveReleaseTrendSummary {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	index := -1
	for i := range trend.Points {
		if trend.Points[i].RunID == runID {
			index = i
			break
		}
	}
	if index < 0 {
		return nil
	}

	current := trend.Points[index]
	// Surface the deltas/regressions at the top level so renderers read one place;
	// blank them on the embedded Current point so it stays a clean headline snapshot.
	deltas := current.Deltas
	regressions := current.Regressions
	current.Deltas = nil
	current.Regressions = nil

	summary := &RunArchiveReleaseTrendSummary{
		ReleaseID:   trend.ReleaseID,
		RunCount:    trend.RunCount,
		Current:     current,
		Regressed:   len(regressions) > 0,
		Regressions: regressions,
		Deltas:      deltas,
		Trajectory:  trend.Summary,
	}
	if index > 0 {
		baseline := trend.Points[index-1]
		baseline.Deltas = nil
		baseline.Regressions = nil
		summary.HasBaseline = true
		summary.Baseline = &baseline
	} else {
		summary.Note = "First archived run for this release; no prior run to compare against."
	}
	return summary
}
