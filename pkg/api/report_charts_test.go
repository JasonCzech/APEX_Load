package api

import (
	"strings"
	"testing"
	"time"
)

func sampleTimelineReport() *RunArchiveReport {
	base := time.Date(2026, time.May, 15, 12, 0, 0, 0, time.UTC)
	return &RunArchiveReport{
		Manifest: &RunArchiveManifest{TestID: "t1", TestName: "Sample"},
		SummaryTimeline: []RunArchiveSummaryPoint{
			{Timestamp: base, ActiveVUsers: 10, TPS: 12.5, TargetTPS: 20, P95Ms: 180, ErrorPct: 0.5},
			{Timestamp: base.Add(time.Minute), ActiveVUsers: 25, TPS: 30, TargetTPS: 20, P95Ms: 240, ErrorPct: 1.2},
			{Timestamp: base.Add(2 * time.Minute), ActiveVUsers: 25, TPS: 28, TargetTPS: 20, P95Ms: 220, ErrorPct: 3.4},
		},
		ByAction: map[string]RunArchiveActionStats{
			"checkout": {Transactions: 100, AvgResponseMs: 210, MaxResponseMs: 800},
			"login":    {Transactions: 250, AvgResponseMs: 90, MaxResponseMs: 300},
		},
	}
}

func TestBuildReportChartsWithTimelineAndActions(t *testing.T) {
	charts := buildReportCharts(sampleTimelineReport())

	if !charts.HasTimeline {
		t.Fatal("expected HasTimeline to be true for >=2 summary points")
	}
	if !charts.HasActions {
		t.Fatal("expected HasActions to be true when ByAction is populated")
	}

	throughput := string(charts.Throughput)
	for _, want := range []string{"<svg", "Throughput (TPS)", "polyline", "Target TPS"} {
		if !strings.Contains(throughput, want) {
			t.Fatalf("throughput chart missing %q: %s", want, throughput)
		}
	}
	if !strings.Contains(string(charts.ResponseTime), "Response Time (p95, ms)") {
		t.Fatalf("response-time chart missing title: %s", charts.ResponseTime)
	}
	if !strings.Contains(string(charts.ErrorRate), "Error Rate") {
		t.Fatalf("error-rate chart missing title: %s", charts.ErrorRate)
	}
	if !strings.Contains(string(charts.VUsers), "Active VUsers") {
		t.Fatalf("vusers chart missing title: %s", charts.VUsers)
	}

	action := string(charts.ActionLatency)
	for _, want := range []string{"<svg", "Average Response Time by Action", "checkout", "<rect"} {
		if !strings.Contains(action, want) {
			t.Fatalf("action chart missing %q: %s", want, action)
		}
	}
}

func TestBuildReportChartsEmptyInputs(t *testing.T) {
	if charts := buildReportCharts(nil); charts.HasTimeline || charts.HasActions {
		t.Fatal("expected empty charts for nil report")
	}

	single := &RunArchiveReport{
		Manifest:        &RunArchiveManifest{TestID: "t1"},
		SummaryTimeline: []RunArchiveSummaryPoint{{Timestamp: time.Now(), TPS: 1}},
		ByAction:        map[string]RunArchiveActionStats{},
	}
	charts := buildReportCharts(single)
	if charts.HasTimeline {
		t.Fatal("expected HasTimeline false for a single summary point")
	}
	if charts.HasActions {
		t.Fatal("expected HasActions false for empty ByAction")
	}
}

func TestBuildReportChartsEscapesActionLabels(t *testing.T) {
	report := &RunArchiveReport{
		Manifest: &RunArchiveManifest{TestID: "t1"},
		ByAction: map[string]RunArchiveActionStats{
			"<b>inject</b>": {Transactions: 5, AvgResponseMs: 100},
		},
	}
	charts := buildReportCharts(report)
	out := string(charts.ActionLatency)
	if strings.Contains(out, "<b>inject") {
		t.Fatalf("expected action label to be HTML-escaped, got: %s", out)
	}
	if !strings.Contains(out, "&lt;b&gt;inject") {
		t.Fatalf("expected escaped action label in output, got: %s", out)
	}
}
