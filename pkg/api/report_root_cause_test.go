package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// reportRootCauseTestAssessment: Checkout's p95 rose after the cart
// service's CPU did; garbage collection is a weaker second hypothesis.
func reportRootCauseTestAssessment() *rca.Assessment {
	onset := time.Date(2026, 10, 5, 12, 3, 20, 0, time.UTC)
	return &rca.Assessment{
		SchemaVersion: rca.AssessmentSchemaVersion, AnalysisID: "rca-1", Revision: 2,
		Inputs:     rca.InputDigests{Engine: "apexload.rca.engine.v2+test"},
		Acceptance: rca.Acceptance{Status: rca.AcceptanceBreached},
		Validity:   rca.Validity{Verdict: rca.ValidityUnknown, Reasons: []string{"no load generator statistics were archived"}},
		Anomalies: []rca.Anomaly{
			{ID: "anomaly-cpu", Series: "monitor/dynatrace/dt-src/builtin:service.cpu.time/abcdef012345", Onset: onset.Add(-50 * time.Second), Effect: 60, Unit: "%"},
			{ID: "anomaly-sym", Series: "transaction/Checkout/p95_ms", Onset: onset, Effect: 300, Unit: "ms"},
		},
		Hypotheses: []rca.Hypothesis{
			{ID: "hypothesis-cpu", Signature: "resource_pressure", Symptoms: []string{"anomaly-sym"}, Rank: 1, Confidence: rca.ConfidenceMedium,
				ConfidenceReasons: []string{"the evidence is connected to the symptom through the run's graph"},
				Chain:             []rca.ChainStep{{From: "anomaly-cpu", To: "anomaly-sym", Relation: rca.RelationPrecedes}},
				Supporting:        []rca.EvidenceRef{{Locator: "monitor_points.jsonl#cpu"}},
				Competing:         []string{"garbage_collection"}, Falsification: "CPU stays below saturation while latency rises"},
			{ID: "hypothesis-gc", Signature: "garbage_collection", Symptoms: []string{"anomaly-sym"}, Rank: 2, Confidence: rca.ConfidenceLow,
				Contradicting: []rca.EvidenceRef{{Locator: "monitor_points.jsonl#gc"}}, Falsification: "GC pauses stay flat while latency rises"},
		},
		Outcome:     rca.OutcomeSupported,
		Coverage:    []rca.Coverage{{Evidence: "monitor_sources", Status: rca.CoveragePartial, Detail: "1 of 2 sources"}},
		Limitations: []rca.Limitation{{Code: "validity_caps_confidence", Detail: "run validity is unknown"}},
		BrowserBaseline: &rca.BrowserBaseline{Version: rca.BrowserBaselineVersion, Status: rca.DetectionComplete, Baseline: rca.BaselineRef{TestID: "base-run", Qualification: rca.BaselineQualified},
			Results: []rca.BrowserBaselineResult{
				{ID: "bb-1", Series: "browser_document/lcp/abc/p75", Metric: "lcp", Unit: "ms", Level: "vusers=20", State: rca.BrowserBaselineAnomaly, Direction: rca.DirectionIncrease, Current: 3100, ExpectedLow: 1800, ExpectedHigh: 2400,
					Evidence: []rca.EvidenceRef{{Kind: "browser_document", Locator: "browser_documents.jsonl#lcp · chromium · profile=desktop · main_frame · navigation"}}},
				{ID: "bb-2", Series: "browser_document/cls/def/p75", Metric: "cls", Unit: "score", Level: "vusers=20", State: rca.BrowserBaselineWithinRange, Current: 0.05, ExpectedLow: 0.03, ExpectedHigh: 0.08},
				{ID: "bb-3", Series: "browser_document/inp/ghi/p75", Metric: "inp", Unit: "ms", Level: "vusers=20", State: rca.BrowserBaselineUnscored, Reason: rca.BrowserBaselineNoBaselineCohort},
			}, Limitations: []rca.Limitation{}},
	}
}

func TestReportRootCauseFromV2(t *testing.T) {
	view := reportRootCauseFromV2(reportRootCauseTestAssessment(), nil)
	top := view.Hypotheses[0]
	if view.Source != reportRootCauseV2 || view.AnalysisID != "rca-1" || view.Revision != 2 || view.Acceptance != rca.AcceptanceBreached ||
		view.Summary != "Resource pressure is the leading hypothesis for Checkout · p95 latency (medium confidence)." {
		t.Fatalf("%+v", view)
	}
	if top.Title != "Resource pressure" || top.Explains[0] != "Checkout · p95 latency" ||
		top.Chain[0] != "builtin:service.cpu.time · Dynatrace (+60 pts) began before Checkout · p95 latency" ||
		top.Competing[0] != "Garbage collection" || len(top.Contradicting) != 0 || top.Evidence[0] != "monitor_points.jsonl#cpu" {
		t.Fatalf("%+v", top)
	}
	if view.Hypotheses[1].Contradicting[0] != "monitor_points.jsonl#gc" || view.Limitations[0] != "Validity caps confidence: run validity is unknown" {
		t.Fatalf("%+v", view)
	}
	for id, want := range map[string]string{
		"action/Cart/Add item/error_rate":   "Cart/Add item · error rate",
		"dependency/http:api.shop/dns_ms":   "http:api.shop · DNS time",
		"monitor/prometheus/prom/jvm_gc/ab": "jvm_gc · Prometheus",
		"odd":                               "odd",
	} {
		if got := rcaSeriesTitle(id); got != want {
			t.Errorf("rcaSeriesTitle(%q) = %q, want %q", id, got, want)
		}
	}
	for effect, want := range map[float64]string{312.4: "+312 ms", -4.25: "-4.25 ms", 12.5: "+12.5 ms", 0: "0 ms"} {
		if got := rcaEffectText(rca.Anomaly{Effect: effect, Unit: "ms"}); got != want {
			t.Errorf("rcaEffectText(%v) = %q, want %q", effect, got, want)
		}
	}
}

// rcaSectionHTML cuts one report section out of a rendered HTML report.
func rcaSectionHTML(t *testing.T, html, id string) string {
	t.Helper()
	start := strings.Index(html, `data-report-section="`+id+`"`)
	if start < 0 {
		t.Fatalf("section %s missing", id)
	}
	end := strings.Index(html[start+1:], "data-report-section=")
	if end < 0 {
		return html[start:]
	}
	return html[start : start+1+end]
}

// TestRootCauseSectionRendersOneModelInEveryFormat: HTML, PDF and CSV render
// the same generated root cause, from v1 or v2, and keep it apart from the
// analyst's findings.
func TestRootCauseSectionRendersOneModelInEveryFormat(t *testing.T) {
	manifest := consistencyMatrixManifest()
	v2 := reportRootCauseFromV2(reportRootCauseTestAssessment(), nil)
	for _, tc := range []struct {
		name  string
		view  func(*RunArchiveReport) *ReportRootCause
		title string
		extra []string
	}{
		{"v2", func(*RunArchiveReport) *ReportRootCause { return &v2 }, "Resource pressure", []string{"CPU stays below saturation while latency rises", "Contradicting evidence",
			"2 cohorts compared with baseline run base-run at the same load, 1 anomaly, 1 unscored", "p75 3100 ms, higher than the baseline"}},
		{"v1", func(r *RunArchiveReport) *ReportRootCause {
			view := reportRootCauseFromV1(buildRunRootCauseSummary("matrix-run", r))
			if len(view.Hypotheses) == 0 {
				t.Fatal("the matrix report yields no v1 findings")
			}
			return &view
		}, "", []string{"Root-cause v1 summary"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := consistencyMatrixReport()
			report.RootCauseView = tc.view(report)
			title := tc.title
			if title == "" {
				title = report.RootCauseView.Hypotheses[0].Title
			}
			annotations := defaultReportAnnotations(report, "matrix-run", "engineer")
			annotations.Findings = []ReportFinding{{ID: "F-1", Title: "Analyst-only finding", Severity: "major", Observed: "seen by the analyst"}}

			html, err := renderRunArchiveHTMLReportWithAnnotations(report, "engineer", annotations, manifest)
			if err != nil {
				t.Fatal(err)
			}
			section, findings := rcaSectionHTML(t, string(html), "rca"), rcaSectionHTML(t, string(html), "root_cause")
			if !strings.Contains(section, title) || strings.Contains(section, "Analyst-only finding") {
				t.Fatalf("rca section: %s", section)
			}
			for _, want := range tc.extra {
				if !strings.Contains(section, want) {
					t.Fatalf("rca section lacks %q: %s", want, section)
				}
			}
			if !strings.Contains(findings, "Analyst-only finding") || strings.Contains(findings, title) {
				t.Fatalf("findings section: %s", findings)
			}

			profile, _ := lookupRunArchiveReportProfile("engineer")
			pdf := strings.Join(buildRunArchiveReportPDFLines(report, profile, annotations, manifest), "\n")
			if !strings.Contains(pdf, "Root-Cause Analysis (generated)") || !strings.Contains(pdf, title) {
				t.Fatalf("pdf lacks the root cause")
			}
			for _, want := range tc.extra {
				if !strings.Contains(pdf, want) {
					t.Fatalf("pdf lacks %q", want)
				}
			}

			var out bytes.Buffer
			if err := writeRunArchiveReportCSVWithProfileAndAnnotations(report, &out, "engineer", annotations, manifest); err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(&out).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			found, browser := false, false
			for _, row := range rows {
				if row[0] == "rca" && row[1] == "hypothesis_1" && row[2] == "title" && row[3] == title {
					found = true
				}
				if row[0] == "rca" && row[1] == "browser_baseline" && row[2] == "finding" && strings.Contains(row[3], "p75 3100 ms") {
					browser = true
				}
				if row[0] == "rca" && strings.Contains(strings.Join(row, ","), "Analyst-only finding") {
					t.Fatalf("analyst finding in the rca rows: %v", row)
				}
			}
			if !found || (tc.name == "v2") != browser {
				t.Fatalf("csv lacks the rca hypothesis or browser baseline: %v", rows)
			}
			if tc.name == "v2" {
				if b := report.RootCauseView.BrowserBaseline; b == nil || b.Compared != 2 || b.Anomalies != 1 || b.Unscored != 1 || len(b.Findings) != 1 {
					t.Fatalf("browser baseline view: %+v", b)
				}
			}
		})
	}
}

func TestExecutiveRootCauseShowsTheLeadingHypothesis(t *testing.T) {
	report := consistencyMatrixReport()
	v2 := reportRootCauseFromV2(reportRootCauseTestAssessment(), nil)
	report.RootCauseView = &v2
	html, err := renderRunArchiveHTMLReportWithAnnotations(report, "executive", nil, consistencyMatrixManifest())
	if err != nil {
		t.Fatal(err)
	}
	section := rcaSectionHTML(t, string(html), "rca")
	if !strings.Contains(section, "Resource pressure") || strings.Contains(section, "GC pauses stay flat") || !strings.Contains(section, "1 more ranked in the Detailed Technical report") {
		t.Fatalf("executive rca section: %s", section)
	}
	// A report without an attached view renders v1, so the section is never
	// blank.
	report.RootCauseView = nil
	engineer, _ := lookupRunArchiveReportProfile("engineer")
	if data := buildRunArchiveReportRenderData(report, engineer, nil, nil); data.RootCause == nil || data.RootCause.Source != reportRootCauseV1 {
		t.Fatalf("fallback: %+v", data.RootCause)
	}
	// Profiles without the section skip it.
	audit, _ := lookupRunArchiveReportProfile("audit")
	if data := buildRunArchiveReportRenderData(report, audit, nil, nil); data.RootCause != nil {
		t.Fatalf("audit computed a root cause: %+v", data.RootCause)
	}
}

// TestArchiveReportAttachesV2RootCauseWherePresented: the report JSON and
// its exports carry v2 where the project presents it and an analysis
// completed, and v1 otherwise.
func TestArchiveReportAttachesV2RootCauseWherePresented(t *testing.T) {
	server, store := baselineTestServer(t)
	exemplarArchiveFixture(t, server, store, "rca-report-run", func(i int, tx *TransactionEvent) {
		tx.DurationMs = 100 + float64(i%7)
		if i >= 200 {
			tx.DurationMs = 400 + float64(i%7)
		}
	})
	view := func() *ReportRootCause {
		t.Helper()
		reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/rca-report-run/archive/report", nil)
		var report RunArchiveReport
		if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &report) != nil {
			t.Fatalf("report: %d %s", reply.Code, reply.Body.String())
		}
		return report.RootCauseView
	}
	if v := view(); v == nil || v.Source != reportRootCauseV1 || len(v.Notes) != 0 {
		t.Fatalf("not presented: %+v", v)
	}
	t.Setenv("APEXLOAD_RCA_V2_PRESENT_PROJECTS", storage.DefaultProjectID)
	if v := view(); v == nil || v.Source != reportRootCauseV1 || len(v.Notes) != 1 {
		t.Fatalf("presented without an analysis: %+v", v)
	}
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)
	assessment := completedRCAAssessment(t, server, "rca-report-run", map[string]any{})
	if v := view(); v == nil || v.Source != reportRootCauseV2 || v.AnalysisID != assessment.AnalysisID || v.Revision != assessment.Revision || v.CompletedAt == nil {
		t.Fatalf("presented: %+v", v)
	}
	reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/rca-report-run/archive/report?format=csv&profile=engineer", nil)
	if reply.Code != http.StatusOK || !strings.Contains(reply.Body.String(), "rca,,source,v2") || !strings.Contains(reply.Body.String(), "rca,,analysis_id,"+assessment.AnalysisID) {
		t.Fatalf("csv: %d %s", reply.Code, reply.Body.String())
	}
	if v := view(); len(v.Notes) != 0 {
		t.Fatalf("a current analysis carries notes: %+v", v.Notes)
	}
	// Evidence archived after the analysis changes the archive it judged.
	rewriteCIRunArchiveManifest(t, store, server.runner.archives["rca-report-run"], func(m *RunArchiveManifest) {
		m.SLADetails = append(m.SLADetails, "late monitoring evidence")
	})
	if v := view(); v == nil || v.Source != reportRootCauseV2 || len(v.Notes) != 1 || !strings.Contains(v.Notes[0], "earlier revision") {
		t.Fatalf("an analysis of an earlier archive revision: %+v", v)
	}
}
