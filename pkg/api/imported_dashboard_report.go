package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
)

// attachDashboardPanels enriches a freshly built archive report with the compact
// imported-dashboard panel digest, read from the run's frozen snapshot. Like
// attachReleaseTrend it is the storage-aware counterpart to the pure
// buildRunArchiveReport, and is best-effort: a run with no snapshot (or an unreadable
// one) simply leaves report.DashboardPanels nil so the report still renders.
func (s *Server) attachDashboardPanels(report *RunArchiveReport) {
	if s == nil || s.store == nil || report == nil || report.Manifest == nil {
		return
	}
	testID := strings.TrimSpace(report.Manifest.TestID)
	if testID == "" {
		return
	}
	projectID := normalizeProjectID(report.Manifest.Provenance.ProjectID)
	snapshot, err := s.store.GetGrafanaDashboardSnapshot(testID, projectID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("archive report: imported dashboard snapshot unavailable", "test", testID, "err", err)
		}
		return
	}
	var resp ImportedDashboardGraphsResponse
	if err := json.Unmarshal(snapshot.Payload, &resp); err != nil {
		slog.Warn("archive report: imported dashboard snapshot is corrupt", "test", testID, "err", err)
		return
	}
	report.DashboardPanels = buildRunArchiveDashboardPanels(&resp, snapshot.CapturedAt)
	attachReportVerdictAndSLA(report)
}

// attachDashboardPanelsForProfile attaches the digest only when the named profile
// actually renders the dashboard_panels section, sparing the snapshot read for
// profiles (audit) that never display it.
func (s *Server) attachDashboardPanelsForProfile(report *RunArchiveReport, profileID string) {
	if !profileIncludesSection(profileID, "dashboard_panels") {
		return
	}
	s.attachDashboardPanels(report)
}

func buildRunArchiveDashboardPanels(resp *ImportedDashboardGraphsResponse, capturedAt time.Time) *RunArchiveDashboardPanels {
	if resp == nil || len(resp.Dashboards) == 0 {
		return nil
	}
	out := &RunArchiveDashboardPanels{CapturedAt: capturedAt}
	for _, dashboard := range resp.Dashboards {
		if len(dashboard.Panels) == 0 {
			continue
		}
		summary := RunArchiveDashboard{Title: strings.TrimSpace(dashboard.DashboardTitle)}
		for _, panel := range dashboard.Panels {
			summary.Panels = append(summary.Panels, summarizeDashboardPanel(panel))
		}
		if len(summary.Panels) > 0 {
			out.Dashboards = append(out.Dashboards, summary)
		}
	}
	if len(out.Dashboards) == 0 {
		return nil
	}
	return out
}

func summarizeDashboardPanel(panel DashboardPanel) RunArchiveDashboardPanel {
	summary := RunArchiveDashboardPanel{
		Title:               strings.TrimSpace(panel.Title),
		Kind:                panel.Kind,
		Unit:                strings.TrimSpace(panel.Unit),
		Error:               strings.TrimSpace(panel.Error),
		ThresholdConfigured: len(panel.Thresholds) > 0,
		ThresholdEvaluated:  panel.ThresholdEvaluated,
		ThresholdBreached:   panel.ThresholdBreached,
		ThresholdDetail:     strings.TrimSpace(panel.ThresholdDetail),
	}
	if panel.Value != nil {
		v := *panel.Value
		summary.Value = &v
	}
	summary.SeriesCount = len(panel.Series)
	var minV, maxV, sum float64
	var count int
	var last *float64
	var lastTs int64 = math.MinInt64
	for _, series := range panel.Series {
		for _, point := range series.Values {
			if count == 0 || point.Value < minV {
				minV = point.Value
			}
			if count == 0 || point.Value > maxV {
				maxV = point.Value
			}
			sum += point.Value
			count++
			if point.TimestampMs >= lastTs {
				lastTs = point.TimestampMs
				v := point.Value
				last = &v
			}
		}
	}
	if count > 0 {
		minCopy, maxCopy, avg := minV, maxV, sum/float64(count)
		summary.Min = &minCopy
		summary.Max = &maxCopy
		summary.Avg = &avg
		summary.Last = last
	}
	return summary
}

// dashboardPanelsPDF renders the imported dashboard digest into the PDF line buffer,
// falling back to a graceful note when no dashboards were captured.
func dashboardPanelsPDF(d *runArchiveHTMLReportData, lines *[]string) {
	pdfHeading(lines, reportSectionTitle("dashboard_panels"))
	panels := d.Report.DashboardPanels
	if panels == nil || len(panels.Dashboards) == 0 {
		*lines = append(*lines, "No imported Grafana dashboards were captured for this run.", "")
		return
	}
	for _, dashboard := range panels.Dashboards {
		title := dashboard.Title
		if title == "" {
			title = "Dashboard"
		}
		*lines = append(*lines, title)
		for _, panel := range dashboard.Panels {
			*lines = append(*lines, "  "+formatDashboardPanelLine(panel))
		}
		*lines = append(*lines, "")
	}
}

func dashboardPanelsCSV(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
	panels := d.Report.DashboardPanels
	if panels == nil || len(panels.Dashboards) == 0 {
		return csvNote(write, "dashboard_panels", "No imported Grafana dashboards were captured for this run.")
	}
	for _, dashboard := range panels.Dashboards {
		for _, panel := range dashboard.Panels {
			key := strings.TrimSpace(dashboard.Title + " / " + panel.Title)
			if panel.Error != "" {
				if err := write("dashboard_panels", key, "error", panel.Error); err != nil {
					return err
				}
				continue
			}
			if panel.Kind == "single_value" {
				if err := write("dashboard_panels", key, "value", formatOptionalFloat(panel.Value)); err != nil {
					return err
				}
				continue
			}
			for _, cell := range []struct{ metric, value string }{
				{"series_count", strconv.Itoa(panel.SeriesCount)},
				{"min", formatOptionalFloat(panel.Min)},
				{"avg", formatOptionalFloat(panel.Avg)},
				{"max", formatOptionalFloat(panel.Max)},
				{"last", formatOptionalFloat(panel.Last)},
			} {
				if cell.value == "" {
					continue
				}
				if err := write("dashboard_panels", key, cell.metric, cell.value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func formatDashboardPanelLine(panel RunArchiveDashboardPanel) string {
	title := panel.Title
	if title == "" {
		title = "Panel"
	}
	unit := ""
	if panel.Unit != "" {
		unit = " " + panel.Unit
	}
	if panel.Error != "" {
		return fmt.Sprintf("%s — error: %s", title, panel.Error)
	}
	if panel.Kind == "single_value" {
		return fmt.Sprintf("%s: %s%s", title, formatOptionalFloat(panel.Value), unit)
	}
	return fmt.Sprintf("%s — min %s / avg %s / max %s / last %s%s (%d series)",
		title, formatOptionalFloat(panel.Min), formatOptionalFloat(panel.Avg),
		formatOptionalFloat(panel.Max), formatOptionalFloat(panel.Last), unit, panel.SeriesCount)
}

func formatOptionalFloat(value *float64) string {
	if value == nil {
		return "—"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}
