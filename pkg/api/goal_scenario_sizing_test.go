package api

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// buildBaselineReport materializes a tiny archive from the given transactions
// and manifest groups, then runs the real buildRunArchiveReport so the sizing
// tests exercise the same ByGroup/Overview rollup the API serves.
func buildBaselineReport(t *testing.T, groups []TestGroup, txns []TransactionEvent) *RunArchiveReport {
	t.Helper()
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "baseline-1",
		TestName:              "Baseline",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		Provenance:            RunArchiveProvenance{Groups: groups},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	txFile, err := os.Create(filepath.Join(dir, "transactions.jsonl"))
	if err != nil {
		t.Fatalf("create transactions: %v", err)
	}
	encoder := json.NewEncoder(txFile)
	for _, tx := range txns {
		if err := encoder.Encode(tx); err != nil {
			t.Fatalf("encode transaction: %v", err)
		}
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("close transactions: %v", err)
	}
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: dir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport: %v", err)
	}
	return report
}

func TestBuildRunArchiveReportRollsUpByGroup(t *testing.T) {
	ts := time.Date(2026, time.June, 2, 9, 0, 0, 0, time.UTC)
	report := buildBaselineReport(t,
		[]TestGroup{{Name: "Checkout", ScriptID: "script-a"}, {Name: "Search", ScriptID: "script-b"}},
		[]TransactionEvent{
			{Timestamp: ts, Group: "Checkout", ActionName: "pay", DurationMs: 2000, Success: true},
			{Timestamp: ts, Group: "Checkout", ActionName: "pay", DurationMs: 4000, Success: true},
			{Timestamp: ts, Group: "Search", ActionName: "query", DurationMs: 500, Success: true},
		},
	)
	checkout, ok := report.ByGroup["Checkout"]
	if !ok {
		t.Fatalf("expected ByGroup to contain Checkout, got %#v", report.ByGroup)
	}
	if checkout.Transactions != 2 {
		t.Fatalf("Checkout transactions = %d, want 2", checkout.Transactions)
	}
	if checkout.AvgResponseMs != 3000 {
		t.Fatalf("Checkout avg = %.1f, want 3000", checkout.AvgResponseMs)
	}
	if search := report.ByGroup["Search"]; search.AvgResponseMs != 500 {
		t.Fatalf("Search avg = %.1f, want 500", search.AvgResponseMs)
	}
}

func TestBuildGoalSizingReportLittlesLaw(t *testing.T) {
	// Single group, ART 3000ms, target 100 TPS -> 300 VUsers (the canonical example).
	report := buildBaselineReport(t,
		[]TestGroup{{Name: "Checkout", ScriptID: "script-a"}},
		[]TransactionEvent{
			{Group: "Checkout", ActionName: "pay", DurationMs: 3000, Success: true},
		},
	)
	sizing, err := buildGoalSizingReport(report, GoalSizingRequest{
		BaselineTestID: "baseline-1",
		Groups:         []GoalSizingGroupTarget{{ScriptID: "script-a", TargetTPS: 100}},
	})
	if err != nil {
		t.Fatalf("buildGoalSizingReport: %v", err)
	}
	if len(sizing.Groups) != 1 {
		t.Fatalf("expected 1 sized group, got %#v", sizing.Groups)
	}
	g := sizing.Groups[0]
	if g.ComputedVUsers != 300 {
		t.Fatalf("computed vusers = %d, want 300", g.ComputedVUsers)
	}
	if g.Basis != "baseline" {
		t.Fatalf("basis = %q, want baseline", g.Basis)
	}
	if sizing.TotalVUsers != 300 || sizing.TotalTargetTPS != 100 {
		t.Fatalf("totals = %d vusers / %.1f tps, want 300 / 100", sizing.TotalVUsers, sizing.TotalTargetTPS)
	}
	if sizing.ScenarioDraft.ScenarioMode != "goal_oriented" {
		t.Fatalf("scenario mode = %q, want goal_oriented", sizing.ScenarioDraft.ScenarioMode)
	}
	gc := sizing.ScenarioDraft.GoalConfig
	if gc == nil || gc.TargetAverageTPS == nil || *gc.TargetAverageTPS != 100 {
		t.Fatalf("goal config target tps not set correctly: %#v", gc)
	}
	if gc.MinVUsers == nil || gc.MaxVUsers == nil || *gc.MinVUsers != 300 || *gc.MaxVUsers != 600 {
		t.Fatalf("bounds = %v/%v, want 300/600", gc.MinVUsers, gc.MaxVUsers)
	}
	if sizing.ScenarioDraft.Duration != "60m" {
		t.Fatalf("duration = %q, want 60m", sizing.ScenarioDraft.Duration)
	}
	intent := gc.WorkloadShapeConfig.GoalDriven.Groups["group_0"]
	if intent.TargetTPS != 100 || intent.ART == nil || intent.ART.Basis != "baseline" || intent.ART.ARTMs != 3000 {
		t.Fatalf("goal-driven intent = %#v, want baseline 3000ms at 100 TPS", intent)
	}
	if len(sizing.ScenarioDraft.Groups) != 1 || sizing.ScenarioDraft.Groups[0].VUsers != 300 {
		t.Fatalf("draft group vusers wrong: %#v", sizing.ScenarioDraft.Groups)
	}
	if sizing.ScenarioDraft.Groups[0].ScriptID != "script-a" {
		t.Fatalf("draft group script id = %q, want script-a", sizing.ScenarioDraft.Groups[0].ScriptID)
	}
}

func TestBuildGoalSizingReportMultiGroupPerGroupTargets(t *testing.T) {
	report := buildBaselineReport(t,
		[]TestGroup{{Name: "Checkout", ScriptID: "script-a"}, {Name: "Search", ScriptID: "script-b"}},
		[]TransactionEvent{
			{Group: "Checkout", ActionName: "pay", DurationMs: 2000, Success: true},
			{Group: "Search", ActionName: "query", DurationMs: 500, Success: true},
		},
	)
	sizing, err := buildGoalSizingReport(report, GoalSizingRequest{
		BaselineTestID: "baseline-1",
		Groups: []GoalSizingGroupTarget{
			{ScriptID: "script-a", TargetTPS: 50},  // 50 * 2.0s = 100
			{ScriptID: "script-b", TargetTPS: 200}, // 200 * 0.5s = 100
		},
	})
	if err != nil {
		t.Fatalf("buildGoalSizingReport: %v", err)
	}
	byScript := map[string]GoalSizingGroupResult{}
	for _, g := range sizing.Groups {
		byScript[g.ScriptID] = g
	}
	if byScript["script-a"].ComputedVUsers != 100 {
		t.Fatalf("script-a vusers = %d, want 100", byScript["script-a"].ComputedVUsers)
	}
	if byScript["script-b"].ComputedVUsers != 100 {
		t.Fatalf("script-b vusers = %d, want 100", byScript["script-b"].ComputedVUsers)
	}
	if sizing.TotalVUsers != 200 || sizing.TotalTargetTPS != 250 {
		t.Fatalf("totals = %d / %.1f, want 200 / 250", sizing.TotalVUsers, sizing.TotalTargetTPS)
	}
}

func TestBuildGoalSizingReportSingleGroupFallsBackToOverview(t *testing.T) {
	// Baseline transactions carry no Group tag; ByGroup is empty so sizing must
	// fall back to overall ART (overview) for a single-group request.
	report := buildBaselineReport(t,
		[]TestGroup{{Name: "Main", ScriptID: "script-a"}},
		[]TransactionEvent{
			{ActionName: "step", DurationMs: 1000, Success: true},
			{ActionName: "step", DurationMs: 3000, Success: true},
		},
	)
	sizing, err := buildGoalSizingReport(report, GoalSizingRequest{
		BaselineTestID: "baseline-1",
		Groups:         []GoalSizingGroupTarget{{ScriptID: "script-a", TargetTPS: 10}},
	})
	if err != nil {
		t.Fatalf("buildGoalSizingReport: %v", err)
	}
	g := sizing.Groups[0]
	if g.Basis != "baseline" {
		t.Fatalf("basis = %q, want baseline", g.Basis)
	}
	if g.BaselineARTMs != 2000 { // (1000+3000)/2
		t.Fatalf("art = %.1f, want 2000", g.BaselineARTMs)
	}
	if g.ComputedVUsers != 20 { // 10 * 2.0s
		t.Fatalf("vusers = %d, want 20", g.ComputedVUsers)
	}
}

func TestBuildGoalSizingReportManualARTWithoutBaseline(t *testing.T) {
	artMs := 1250.0
	sizing, err := buildGoalSizingReport(nil, GoalSizingRequest{
		Groups: []GoalSizingGroupTarget{{Name: "Checkout", ScriptID: "script-a", TargetTPS: 12.5, ARTBasis: "manual", ARTMs: &artMs}},
	})
	if err != nil {
		t.Fatalf("buildGoalSizingReport: %v", err)
	}
	if sizing.TotalVUsers != 16 {
		t.Fatalf("total vusers = %d, want 16", sizing.TotalVUsers)
	}
	group := sizing.Groups[0]
	if group.Basis != "manual" || group.BaselineARTMs != artMs {
		t.Fatalf("manual evidence = %#v", group)
	}
	intent := sizing.ScenarioDraft.GoalConfig.WorkloadShapeConfig.GoalDriven.Groups["group_0"]
	if intent.ART == nil || intent.ART.Basis != "manual" || intent.ART.BaselineTestID != "" {
		t.Fatalf("manual intent = %#v", intent)
	}
}

func TestBuildGoalSizingReportRejectsInvalidManualART(t *testing.T) {
	zero := 0.0
	_, err := buildGoalSizingReport(nil, GoalSizingRequest{Groups: []GoalSizingGroupTarget{{TargetTPS: 10, ARTBasis: "manual", ARTMs: &zero}}})
	if err == nil || !strings.Contains(err.Error(), "positive finite") {
		t.Fatalf("error = %v, want positive finite ART error", err)
	}
}

func TestBuildGoalSizingReportClampsAndRoundsUp(t *testing.T) {
	report := buildBaselineReport(t,
		[]TestGroup{{Name: "Fast", ScriptID: "script-a"}},
		[]TransactionEvent{
			{Group: "Fast", ActionName: "ping", DurationMs: 50, Success: true},
		},
	)
	// 1 TPS * 0.05s = 0.05 -> ceil & clamp to minimum 1 VUser.
	sizing, err := buildGoalSizingReport(report, GoalSizingRequest{
		BaselineTestID: "baseline-1",
		Groups:         []GoalSizingGroupTarget{{ScriptID: "script-a", TargetTPS: 1}},
	})
	if err != nil {
		t.Fatalf("buildGoalSizingReport: %v", err)
	}
	if sizing.Groups[0].ComputedVUsers != 1 {
		t.Fatalf("vusers = %d, want 1 (clamped)", sizing.Groups[0].ComputedVUsers)
	}
}

func TestBuildGoalSizingReportErrors(t *testing.T) {
	single := buildBaselineReport(t,
		[]TestGroup{{Name: "Checkout", ScriptID: "script-a"}},
		[]TransactionEvent{{Group: "Checkout", ActionName: "pay", DurationMs: 1000, Success: true}},
	)
	multi := buildBaselineReport(t,
		[]TestGroup{{Name: "A", ScriptID: "script-a"}, {Name: "B", ScriptID: "script-b"}},
		[]TransactionEvent{
			{Group: "A", ActionName: "x", DurationMs: 1000, Success: true},
			{Group: "B", ActionName: "y", DurationMs: 1000, Success: true},
		},
	)
	zeroART := buildBaselineReport(t,
		[]TestGroup{{Name: "Zero", ScriptID: "script-z"}},
		[]TransactionEvent{{Group: "Zero", ActionName: "noop", DurationMs: 0, Success: true}},
	)

	tests := []struct {
		name   string
		report *RunArchiveReport
		req    GoalSizingRequest
	}{
		{
			name:   "no groups",
			report: single,
			req:    GoalSizingRequest{BaselineTestID: "baseline-1"},
		},
		{
			name:   "non-positive target tps",
			report: single,
			req:    GoalSizingRequest{BaselineTestID: "baseline-1", Groups: []GoalSizingGroupTarget{{ScriptID: "script-a", TargetTPS: 0}}},
		},
		{
			name:   "non-finite target tps",
			report: single,
			req:    GoalSizingRequest{Groups: []GoalSizingGroupTarget{{ARTBasis: "manual", ARTMs: func() *float64 { value := 100.0; return &value }(), TargetTPS: math.NaN()}}},
		},
		{
			name:   "unknown group in multi-group baseline",
			report: multi,
			req:    GoalSizingRequest{BaselineTestID: "baseline-1", Groups: []GoalSizingGroupTarget{{ScriptID: "script-missing", TargetTPS: 10}}},
		},
		{
			name:   "zero baseline ART",
			report: zeroART,
			req:    GoalSizingRequest{BaselineTestID: "baseline-1", Groups: []GoalSizingGroupTarget{{ScriptID: "script-z", TargetTPS: 10}}},
		},
		{
			name:   "nil report",
			report: nil,
			req:    GoalSizingRequest{BaselineTestID: "baseline-1", Groups: []GoalSizingGroupTarget{{ScriptID: "script-a", TargetTPS: 10}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildGoalSizingReport(tc.report, tc.req); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestGoalSizingDraftIsAcceptedByStoreScenario(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-a",
		Name:     "Checkout",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []dsl.Action{{Name: "pay", Type: "http", Method: "GET", Target: "/pay"}},
	})

	report := buildBaselineReport(t,
		[]TestGroup{{Name: "Checkout", ScriptID: "script-a"}},
		[]TransactionEvent{{Group: "Checkout", ActionName: "pay", DurationMs: 3000, Success: true}},
	)
	sizing, err := buildGoalSizingReport(report, GoalSizingRequest{
		BaselineTestID: "baseline-1",
		Groups:         []GoalSizingGroupTarget{{ScriptID: "script-a", TargetTPS: 100}},
	})
	if err != nil {
		t.Fatalf("buildGoalSizingReport: %v", err)
	}

	scenario, err := server.runner.StoreScenario(sizing.ScenarioDraft)
	if err != nil {
		t.Fatalf("StoreScenario rejected the sized draft: %v", err)
	}
	if scenario.GoalConfig == nil || scenario.GoalConfig.TargetAverageTPS == nil || *scenario.GoalConfig.TargetAverageTPS != 100 {
		t.Fatalf("persisted scenario goal config missing target tps: %#v", scenario.GoalConfig)
	}
	if total := totalScenarioRefVUsers(scenario.Scripts); total != 300 {
		t.Fatalf("persisted scenario vusers = %d, want 300", total)
	}
}

func totalScenarioRefVUsers(scripts []ScenarioScriptRef) int {
	total := 0
	for _, s := range scripts {
		total += s.VUsers
	}
	return total
}

func TestHandleSizeScenarioFromGoalRouteWiring(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})

	manualART := 250.0
	cases := []struct {
		name string
		body GoalSizingRequest
		want int
	}{
		{name: "missing baseline id", body: GoalSizingRequest{Groups: []GoalSizingGroupTarget{{ScriptID: "x", TargetTPS: 10}}}, want: http.StatusBadRequest},
		{name: "manual ART needs no baseline", body: GoalSizingRequest{Groups: []GoalSizingGroupTarget{{Name: "Manual", ScriptID: "x", TargetTPS: 10, ARTBasis: "manual", ARTMs: &manualART}}}, want: http.StatusOK},
		{name: "unknown baseline", body: GoalSizingRequest{BaselineTestID: "nope", Groups: []GoalSizingGroupTarget{{ScriptID: "x", TargetTPS: 10}}}, want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/size-from-goal", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != tc.want {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, tc.want, resp.Body.String())
			}
		})
	}
}

func TestBuildGoalSizingReportRespectsBoundOverrides(t *testing.T) {
	report := buildBaselineReport(t,
		[]TestGroup{{Name: "Checkout", ScriptID: "script-a"}},
		[]TransactionEvent{{Group: "Checkout", ActionName: "pay", DurationMs: 3000, Success: true}},
	)
	min := 50
	max := 400
	sizing, err := buildGoalSizingReport(report, GoalSizingRequest{
		BaselineTestID: "baseline-1",
		Groups:         []GoalSizingGroupTarget{{ScriptID: "script-a", TargetTPS: 100}},
		MinVUsers:      &min,
		MaxVUsers:      &max,
	})
	if err != nil {
		t.Fatalf("buildGoalSizingReport: %v", err)
	}
	gc := sizing.ScenarioDraft.GoalConfig
	if *gc.MinVUsers != 50 || *gc.MaxVUsers != 400 {
		t.Fatalf("bounds = %d/%d, want 50/400", *gc.MinVUsers, *gc.MaxVUsers)
	}
}
