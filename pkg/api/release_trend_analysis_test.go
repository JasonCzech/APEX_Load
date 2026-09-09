package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func trendRunFixture(id string, completedAt time.Time, errorPct, avgMs, completeness float64, slaBreached bool) releaseTrendRun {
	return releaseTrendRun{
		Test: &ManagedTest{ID: id, Name: id, Status: "COMPLETE"},
		Report: &RunArchiveReport{
			Overview: RunArchiveOverview{
				Transactions:  1000,
				ErrorPct:      errorPct,
				AvgResponseMs: avgMs,
				PeakTPS:       50,
			},
			Completeness: RunArchiveCompletenessScore{ScorePct: completeness, Status: "complete"},
			Manifest:     &RunArchiveManifest{CompletedAt: completedAt, SLABreached: slaBreached},
		},
	}
}

func TestBuildReleaseTrendDeltasAndRegressions(t *testing.T) {
	base := time.Date(2026, time.May, 20, 12, 0, 0, 0, time.UTC)
	// Late run regresses on every dimension vs the early run.
	early := trendRunFixture("run-a", base, 1.0, 100, 100, false)
	late := trendRunFixture("run-b", base.Add(time.Hour), 3.0, 130, 80, true)

	// Provide out of order to prove the builder sorts by completion time.
	trend := buildReleaseTrend("rel-1", []releaseTrendRun{late, early})

	if trend.RunCount != 2 {
		t.Fatalf("run count = %d, want 2", trend.RunCount)
	}
	if trend.Points[0].RunID != "run-a" || trend.Points[1].RunID != "run-b" {
		t.Fatalf("points not ordered by completion: %s, %s", trend.Points[0].RunID, trend.Points[1].RunID)
	}
	if trend.Points[0].Deltas != nil || trend.Points[0].Regressions != nil {
		t.Fatalf("first point should have no deltas/regressions")
	}

	b := trend.Points[1]
	if b.Deltas == nil {
		t.Fatal("second point should have deltas")
	}
	if b.Deltas.ErrorPctDelta != 2.0 {
		t.Fatalf("error pct delta = %v, want 2.0", b.Deltas.ErrorPctDelta)
	}
	if b.Deltas.AvgResponsePctDelta != 30 {
		t.Fatalf("avg response pct delta = %v, want 30", b.Deltas.AvgResponsePctDelta)
	}
	want := map[string]bool{
		"error rate regressed":        true,
		"avg response time regressed": true,
		"SLA newly breached":          true,
		"run completeness dropped":    true,
	}
	for _, r := range b.Regressions {
		if !want[r] {
			t.Fatalf("unexpected regression %q", r)
		}
		delete(want, r)
	}
	if len(want) != 0 {
		t.Fatalf("missing regressions: %#v", want)
	}
	if !trend.Summary.LatestRegressed {
		t.Fatal("summary should flag latest regressed")
	}
	if trend.Summary.AvgResponseTrendPct != 30 {
		t.Fatalf("trajectory avg response pct = %v, want 30", trend.Summary.AvgResponseTrendPct)
	}
}

func TestBuildReleaseTrendUsesCompactPointsWithoutReports(t *testing.T) {
	completed := time.Date(2026, time.May, 21, 12, 0, 0, 0, time.UTC)
	point := &ReleaseTrendPoint{RunID: "compact-run", RunName: "Compact", Status: "COMPLETE", CompletedAt: completed, Transactions: 42, AvgResponseMs: 123}
	trend := buildReleaseTrend("rel-compact", []releaseTrendRun{{Test: &ManagedTest{ID: "compact-run"}, Point: point}})
	if trend.RunCount != 1 || trend.Points[0].Transactions != 42 || trend.Points[0].AvgResponseMs != 123 {
		t.Fatalf("trend = %#v", trend)
	}
}

func TestBuildReleaseTrendNoRegression(t *testing.T) {
	base := time.Date(2026, time.May, 20, 12, 0, 0, 0, time.UTC)
	a := trendRunFixture("run-a", base, 1.0, 100, 100, false)
	b := trendRunFixture("run-b", base.Add(time.Hour), 1.2, 102, 100, false) // within thresholds
	trend := buildReleaseTrend("rel-1", []releaseTrendRun{a, b})
	if trend.Summary.LatestRegressed {
		t.Fatalf("should not flag regression for within-threshold changes: %#v", trend.Points[1].Regressions)
	}
}

func TestReleaseTrendEndpointReturnsJSON(t *testing.T) {
	server := NewServer(ServerConfig{})
	releaseID := "release-trend-json"
	started := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)

	early := writeReleaseTrendArchiveFixture(t, releaseID, "run-early", "Early Run", started, started.Add(3*time.Minute), true)
	late := writeReleaseTrendArchiveFixture(t, releaseID, "run-late", "Late Run", started.Add(5*time.Minute), started.Add(8*time.Minute), false)

	server.runner.mu.Lock()
	server.runner.tests["run-early"] = &ManagedTest{ID: "run-early", Name: "Early Run", Status: "COMPLETE", ReleaseID: releaseID, CreatedAt: started, EndedAt: ptrTime(started.Add(3 * time.Minute)), Archive: early}
	server.runner.tests["run-late"] = &ManagedTest{ID: "run-late", Name: "Late Run", Status: "COMPLETE", ReleaseID: releaseID, CreatedAt: started.Add(5 * time.Minute), EndedAt: ptrTime(started.Add(8 * time.Minute)), Archive: late}
	server.runner.archives["run-early"] = early
	server.runner.archives["run-late"] = late
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/releases/"+releaseID+"/trend", nil)
	rr := httptest.NewRecorder()
	server.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var trend ReleaseTrend
	if err := json.NewDecoder(rr.Body).Decode(&trend); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if trend.RunCount != 2 {
		t.Fatalf("run_count = %d, want 2", trend.RunCount)
	}
	if trend.Points[0].RunID != "run-early" || trend.Points[1].RunID != "run-late" {
		t.Fatalf("runs not in completion order: %#v", trend.Points)
	}
}

func TestReleaseTrendSeparatesMetricPopulations(t *testing.T) {
	early := trendRunFixture("legacy", time.Now(), 0, 50, 100, false)
	late := trendRunFixture("typed", time.Now().Add(time.Minute), 0, 50, 100, false)
	late.Report.MetricPopulations = map[string]ArchiveMetricPopulation{"transaction": {RunArchiveActionStats: RunArchiveActionStats{Transactions: 7, Errors: 1, ErrorPct: 14.28, AvgResponseMs: 120, PeakTPS: 3}}}
	trend := buildReleaseTrend("release", []releaseTrendRun{early, late})
	point := trend.Points[1]
	if point.Transactions != 7 || point.AvgResponseMs != 120 || point.MetricPopulation != "transaction" || point.Deltas != nil {
		t.Fatalf("mixed population comparison: %+v", point)
	}
	summary := buildRunArchiveReleaseTrendSummary(trend, "typed")
	if summary.HasBaseline || !strings.Contains(summary.Note, "population") {
		t.Fatalf("summary: %+v", summary)
	}
}

func TestReleaseTrendRequiresSamplesInBothRuns(t *testing.T) {
	for _, counts := range [][2]int64{{0, 0}, {100, 0}, {0, 100}, {100, 100}} {
		t.Run(fmt.Sprintf("%d-%d", counts[0], counts[1]), func(t *testing.T) {
			a := trendRunFixture("a", time.Now(), 0, 20, 100, false)
			b := trendRunFixture("b", time.Now().Add(time.Hour), 0, 30, 100, false)
			a.Report.MetricPopulations = map[string]ArchiveMetricPopulation{"transaction": {RunArchiveActionStats: RunArchiveActionStats{Transactions: counts[0], AvgResponseMs: 20}}}
			b.Report.MetricPopulations = map[string]ArchiveMetricPopulation{"transaction": {RunArchiveActionStats: RunArchiveActionStats{Transactions: counts[1], AvgResponseMs: 30}}}
			trend := buildReleaseTrend("release", []releaseTrendRun{a, b})
			summary := buildRunArchiveReleaseTrendSummary(trend, "b")
			comparable := counts[0] > 0 && counts[1] > 0
			if summary.HasBaseline != comparable || (trend.Points[1].Deltas != nil) != comparable {
				t.Fatalf("unexpected comparison: %+v", summary)
			}
			if !comparable && (summary.Note == "" || summary.Regressed || len(summary.Regressions) > 0) {
				t.Fatalf("missing no-samples explanation: %+v", summary)
			}
		})
	}
}
