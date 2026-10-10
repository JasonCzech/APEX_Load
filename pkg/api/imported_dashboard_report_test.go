package api

import (
	"strings"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }

func TestBuildRunArchiveDashboardPanels(t *testing.T) {
	now := time.Date(2026, time.June, 12, 14, 0, 0, 0, time.UTC)
	resp := &ImportedDashboardGraphsResponse{
		Dashboards: []DashboardPanelsResult{{
			DashboardID: "d1", DashboardTitle: "Infra",
			Panels: []DashboardPanel{
				{
					PanelID: 1, Title: "CPU", Kind: "line", Unit: "%",
					ThresholdEvaluated: true, ThresholdBreached: true, ThresholdDetail: "api-1 entered critical threshold range at 95",
					Series: []PrometheusSeries{{Key: "k", Label: "a", Values: []PrometheusPoint{
						{TimestampMs: 1, Value: 10}, {TimestampMs: 2, Value: 30}, {TimestampMs: 3, Value: 20},
					}}},
				},
				{PanelID: 2, Title: "Up", Kind: "single_value", Value: floatPtr(1)},
				{PanelID: 3, Title: "Broken", Kind: "line", Error: "query failed"},
			},
		}},
	}
	got := buildRunArchiveDashboardPanels(resp, now)
	if got == nil || len(got.Dashboards) != 1 || len(got.Dashboards[0].Panels) != 3 {
		t.Fatalf("unexpected digest: %#v", got)
	}
	cpu := got.Dashboards[0].Panels[0]
	if cpu.SeriesCount != 1 || cpu.Min == nil || *cpu.Min != 10 || cpu.Max == nil || *cpu.Max != 30 || cpu.Avg == nil || *cpu.Avg != 20 || cpu.Last == nil || *cpu.Last != 20 {
		t.Fatalf("cpu summary wrong: %#v", cpu)
	}
	if !cpu.ThresholdEvaluated || !cpu.ThresholdBreached || !strings.Contains(cpu.ThresholdDetail, "critical threshold") {
		t.Fatalf("threshold summary wrong: %#v", cpu)
	}
	if got.Dashboards[0].Panels[1].Value == nil || *got.Dashboards[0].Panels[1].Value != 1 {
		t.Fatalf("tile value wrong: %#v", got.Dashboards[0].Panels[1])
	}
	if got.Dashboards[0].Panels[2].Error != "query failed" {
		t.Fatalf("error not preserved: %#v", got.Dashboards[0].Panels[2])
	}
}

func TestImportedDashboardThresholdProducesFailedReportVerdict(t *testing.T) {
	report := &RunArchiveReport{DashboardPanels: &RunArchiveDashboardPanels{Dashboards: []RunArchiveDashboard{{
		Title: "Infra", Panels: []RunArchiveDashboardPanel{{Title: "CPU", ThresholdBreached: true, ThresholdDetail: "entered critical threshold range at 95"}},
	}}}}
	verdict := deriveReportVerdict(report)
	if !verdict.Failed || verdict.Status != reportVerdictFail || !strings.Contains(verdict.Explanation, "Infra / CPU") {
		t.Fatalf("verdict = %#v", verdict)
	}
}

func TestBuildRunArchiveDashboardPanelsEmpty(t *testing.T) {
	if got := buildRunArchiveDashboardPanels(&ImportedDashboardGraphsResponse{}, time.Now()); got != nil {
		t.Fatalf("empty response should produce nil digest, got %#v", got)
	}
}

func TestDashboardPanelsRenderersPopulated(t *testing.T) {
	data := &runArchiveHTMLReportData{Report: &RunArchiveReport{DashboardPanels: &RunArchiveDashboardPanels{
		Dashboards: []RunArchiveDashboard{{
			Title: "Infra",
			Panels: []RunArchiveDashboardPanel{
				{Title: "CPU", Kind: "line", Unit: "%", SeriesCount: 1, Min: floatPtr(10), Avg: floatPtr(20), Max: floatPtr(30), Last: floatPtr(20)},
				{Title: "Up", Kind: "single_value", Value: floatPtr(1)},
			},
		}},
	}}}

	var pdf []string
	dashboardPanelsPDF(data, &pdf)
	joined := strings.Join(pdf, "\n")
	if !strings.Contains(joined, "Imported dashboard panels") || !strings.Contains(joined, "CPU") || !strings.Contains(joined, "Up") {
		t.Fatalf("PDF missing content: %q", joined)
	}

	var rows [][4]string
	err := dashboardPanelsCSV(data, func(section, key, metric, value string) error {
		rows = append(rows, [4]string{section, key, metric, value})
		return nil
	})
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	var sawAvg, sawTile bool
	for _, row := range rows {
		if row[0] != "dashboard_panels" {
			t.Fatalf("unexpected csv section: %v", row)
		}
		if strings.Contains(row[1], "CPU") && row[2] == "avg" && row[3] == "20" {
			sawAvg = true
		}
		if strings.Contains(row[1], "Up") && row[2] == "value" && row[3] == "1" {
			sawTile = true
		}
	}
	if !sawAvg || !sawTile {
		t.Fatalf("csv missing expected rows: %#v", rows)
	}
}

func TestDashboardPanelsRenderersNil(t *testing.T) {
	data := &runArchiveHTMLReportData{Report: &RunArchiveReport{}}
	var pdf []string
	dashboardPanelsPDF(data, &pdf)
	if !strings.Contains(strings.Join(pdf, "\n"), "No imported Grafana dashboards") {
		t.Fatalf("nil PDF should render a graceful note: %v", pdf)
	}
	var count int
	_ = dashboardPanelsCSV(data, func(_, _, _, _ string) error { count++; return nil })
	if count == 0 {
		t.Fatalf("nil CSV should still emit a note row")
	}
}
