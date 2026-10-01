package api

import (
	"github.com/jasonczech/apexload/pkg/analysis"
	"strings"
	"testing"
	"time"
)

func TestStatisticalFindingsQualifiedWindows(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	history := make([]*LiveMetrics, 30)
	for i := range history {
		history[i] = &LiveMetrics{Timestamp: start.Add(time.Duration(i) * time.Second), ActiveRuntimeSeconds: float64(i + 1), TotalTx: 10, ActiveVUsers: 10, SampleWindowSeconds: 1, TPS: 10 + float64(i%2), AvgResponseMs: 5}
	}
	current := &LiveMetrics{Timestamp: start.Add(30 * time.Second), ActiveRuntimeSeconds: 31, TotalTx: 10, ActiveVUsers: 10, SampleWindowSeconds: 1, TPS: 30, AvgResponseMs: 5}
	findings := statisticalFindings("p", "r", current, history)
	if len(findings) != 1 || findings[0].MetricName != "tps" || findings[0].BaselineBuckets != 30 || !findings[0].Advisory {
		t.Fatalf("findings: %+v", findings)
	}
	for _, tc := range []struct {
		name   string
		change func(*LiveMetrics)
	}{
		{"ramp", func(p *LiveMetrics) { p.ActiveVUsers = 11 }},
		{"pause", func(p *LiveMetrics) { p.ActiveRuntimeSeconds += 3 }},
		{"stale", func(p *LiveMetrics) { p.ObservationState = "stale" }},
		{"missing", func(p *LiveMetrics) { p.Timestamp = p.Timestamp.Add(-time.Second) }},
		{"dropped", func(p *LiveMetrics) { p.Telemetry = &TelemetryHealth{DroppedResults: 1} }},
		{"empty", func(p *LiveMetrics) { p.TotalTx = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := *history[15]
			tc.change(history[15])
			defer func() { *history[15] = saved }()
			if len(statisticalFindings("p", "r", current, history)) != 0 {
				t.Fatal("unqualified baseline scored")
			}
		})
	}
	if len(statisticalFindings("p", "r", current, history[1:])) != 0 {
		t.Fatal("short baseline scored")
	}
	alerts := advisoryDiagnosticAlerts(&LiveMetrics{StatisticalFindings: findings})
	if len(alerts) != 1 || alerts[0]["action"] != "advisory" {
		t.Fatalf("alerts: %+v", alerts)
	}
}

func TestArchivedStatisticalFindingsAndAdvisoryErrorPatterns(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var summaries []IntervalSummaryRecord
	for i := 0; i < 31; i++ {
		tps := 10 + float64(i%2)
		if i == 30 {
			tps = 30
		}
		summaries = append(summaries, IntervalSummaryRecord{Timestamp: start.Add(time.Duration(i) * time.Second), SampleWindowSeconds: 1, ActiveVUsers: 10, TotalTx: 10, TPS: tps})
	}
	findings := archiveStatisticalFindings("p", "r", summaries)
	if len(findings) != 1 || findings[0].BaselineBuckets != 30 {
		t.Fatalf("archive findings: %+v", findings)
	}
	for i := range summaries {
		runtime := float64(i + 1)
		summaries[i].ActiveRuntimeSeconds = &runtime
	}
	pausedRuntime := float64(18)
	summaries[15].ActiveRuntimeSeconds = &pausedRuntime
	if len(archiveStatisticalFindings("p", "r", summaries)) != 0 {
		t.Fatal("archived pause qualified as a comparable baseline")
	}
	pausedRuntime = 16
	summaries[15].ObservationState = "stale"
	if len(archiveStatisticalFindings("p", "r", summaries)) != 0 {
		t.Fatal("archived stale baseline scored")
	}
	rows := toParquetSummaries(summaries)
	if rows[15].ObservationState != "stale" || rows[15].SampleWindowSeconds != 1 {
		t.Fatal("Parquet dropped qualification evidence")
	}
	now := start.Add(30 * time.Second)
	alerts := advisoryDiagnosticAlerts(&LiveMetrics{Timestamp: now, SampleWindowSeconds: 1, ErrorPatterns: []LiveErrorPattern{{Signature: "pattern", Count: 3, LatestSeen: now}, {Signature: "old", Count: 10, LatestSeen: start}}})
	if len(alerts) != 1 || alerts[0]["action"] != "advisory" || alerts[0]["transaction"] != "pattern" {
		t.Fatalf("error alerts: %+v", alerts)
	}
}

func TestStatisticalFindingsRemainAdvisoryInNotificationsAndRootCause(t *testing.T) {
	explanation := "Repeated error pattern; advisory only"
	event := thresholdAlertNotificationEvent(&ManagedTest{ID: "r", ProjectID: "p"}, "RUNNING", map[string]any{"metric": "error_pattern", "value": float64(3), "threshold": float64(3), "transaction": "signature", "action": "advisory", "explanation": explanation}, time.Now())
	if event.Summary != explanation {
		t.Fatalf("advisory rewritten: %s", event.Summary)
	}
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{}, StatisticalFindings: []StatisticalFinding{{AnomalyEvidence: analysis.AnomalyEvidence{MetricName: "tps", BaselineBuckets: 30, ZScore: 4}, Advisory: true}}}
	summary := buildRunRootCauseSummary("r", report)
	found := false
	for _, finding := range summary.TopFindings {
		if finding.Category == "statistical" {
			found = true
			if !strings.Contains(finding.Detail, "does not establish causation") {
				t.Fatal(finding.Detail)
			}
		}
	}
	if !found {
		t.Fatal("missing advisory root-cause evidence")
	}
}

func TestArchivedStatisticalFindingsBounded(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	summaries := make([]IntervalSummaryRecord, 4000)
	for i := range summaries {
		value := 10 + float64(i%2)
		if i%31 == 30 {
			value = 60
		}
		summaries[i] = IntervalSummaryRecord{Timestamp: start.Add(time.Duration(i) * time.Second), TotalTx: 10, ActiveVUsers: 10, SampleWindowSeconds: 1, TPS: value, AvgResponseMs: value, ErrorPct: value}
	}
	if got := len(archiveStatisticalFindings("p", "r", summaries)); got != 300 {
		t.Fatalf("finding cap: %d", got)
	}
}
