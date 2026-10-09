package api

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func browserIntelligenceReportFixture() *ReportBrowserIntelligence {
	score, observed, index, rate := 72.5, 3100.0, 0.125, 0.25
	return &ReportBrowserIntelligence{
		Findings: &RunBrowserFindingsResponse{RunID: "run-1", Status: "ready", Result: &BrowserIntelligenceResult{
			Policy: BrowserPolicyRef{ID: "policy-1", Name: "Vitals", Revision: 3},
			Score:  BrowserScore{Score: &score, Severity: "needs_improvement"},
			Findings: []BrowserFinding{
				{RuleID: "metric.poor", Metric: "lcp", Unit: "ms", Cohort: &BrowserFindingCohort{Engine: "webkit", DeviceProfile: "iphone_15"}, State: "active", Severity: "poor", Observed: &observed, Summary: "LCP p75 is poor."},
				{RuleID: "collection.incomplete", State: "suppressed", Summary: "Some documents were dropped."},
			},
			Counts: map[string]int{"active": 1, "suppressed": 1},
			Notes:  []string{"Scores weigh only qualified final documents."},
		}},
		Interactions: &BrowserInteractionAnalysis{Version: "browser_interaction_patterns.v1", Status: "ready",
			Coverage: BrowserInteractionCoverage{Interactions: 40, Mapped: 30, Dropped: 2},
			Patterns: []BrowserInteractionPattern{{Kind: "scripted_repetition", Provenance: "scripted", Route: "/cart", ActionName: "Add to cart", Count: 4, Detail: "4 clicks within 2 s"}},
			Heatmaps: []BrowserInteractionHeatmap{{Route: "/cart", LayoutRevision: "L1", ViewportWidth: 390, ViewportHeight: 844, Orientation: "portrait", Engine: "webkit", DeviceProfile: "iphone_15", Columns: 64, Rows: 64, Cells: [][3]int{{1, 2, 3}}, Points: 3}},
			Notes:    []string{"Heatmaps show where gestures were dispatched."}},
		Journeys: &JourneyRiskReport{Availability: "available", Policy: JourneyPolicyRef{ID: "journeys-1", Name: "Shop", Revision: 2}, WeightedFailureIndex: &index,
			Journeys: []JourneyAccounting{{Name: "Checkout", Eligible: 8, Completed: 6, Failed: 2, FailureRate: &rate}}, Notes: []string{"Censored attempts are not failures."}},
	}
}

func renderBrowserIntelligenceSections(t *testing.T, report *RunArchiveReport) map[string][3]string {
	t.Helper()
	profile, ok := lookupRunArchiveReportProfile("engineer")
	if !ok {
		t.Fatal("no engineer profile")
	}
	data := buildRunArchiveReportRenderData(report, profile, nil, consistencyMatrixManifest())
	out := map[string][3]string{}
	for _, id := range browserIntelligenceReportSections {
		renderer := reportSectionRegistry[id]
		html, err := renderer.HTML(&data)
		if err != nil {
			t.Fatalf("%s HTML: %v", id, err)
		}
		var pdf []string
		renderer.PDF(&data, &pdf)
		var csv strings.Builder
		if err := renderer.CSV(&data, func(a, b, c, d string) error { csv.WriteString(a + "," + b + "," + c + "," + d + "\n"); return nil }); err != nil {
			t.Fatalf("%s CSV: %v", id, err)
		}
		out[id] = [3]string{string(html), strings.Join(pdf, "\n"), csv.String()}
	}
	return out
}

// The four browser intelligence sections render the attached results in
// HTML, PDF and CSV (BI-4).
func TestBrowserIntelligenceReportSectionsRenderTheirResults(t *testing.T) {
	report := consistencyMatrixReport()
	report.BrowserIntelligence = browserIntelligenceReportFixture()
	sections := renderBrowserIntelligenceSections(t, report)
	expect := map[string][]string{
		reportSectionBrowserFindings: {"Score 72.5/100 (needs improvement) under Vitals revision 3", "LCP · webkit / iphone_15", "3100 ms", "LCP p75 is poor."},
		reportSectionInteractionMap:  {"1 patterns and 1 heatmaps from 40 interactions (30 mapped, 2 dropped)", "scripted_repetition", "Add to cart", "390×844 portrait"},
		reportSectionJourneyRisk:     {"Weighted failure index 0.125 under Shop revision 2", "Checkout", "25.0%"},
	}
	for id, wants := range expect {
		for index, format := range []string{"HTML", "PDF", "CSV"} {
			for _, want := range wants {
				if !strings.Contains(sections[id][index], want) {
					t.Errorf("%s %s lacks %q:\n%s", id, format, want, sections[id][index])
				}
			}
		}
	}
}

// A section without a result says why, in every format.
func TestBrowserIntelligenceReportSectionsSayWhyAResultIsMissing(t *testing.T) {
	report := consistencyMatrixReport()
	report.BrowserIntelligence = nil
	for id, formats := range renderBrowserIntelligenceSections(t, report) {
		for _, body := range formats {
			if !strings.Contains(body, "generated without browser intelligence results") {
				t.Errorf("%s: %s", id, body)
			}
		}
	}
	report.BrowserIntelligence = &ReportBrowserIntelligence{
		Findings:     &RunBrowserFindingsResponse{Status: "pending"},
		Interactions: &BrowserInteractionAnalysis{Status: "not_captured"},
		Journeys:     &JourneyRiskReport{Availability: "unavailable", Reason: "no_iterations"},
	}
	sections := renderBrowserIntelligenceSections(t, report)
	for id, want := range map[string]string{
		reportSectionBrowserFindings: "queued or running",
		reportSectionInteractionMap:  "captured no interaction evidence",
		reportSectionJourneyRisk:     "unavailable for this run: no_iterations",
	} {
		for _, body := range sections[id] {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q: %s", id, want, body)
			}
		}
	}
}

// Exports read the results only for a profile that renders them, and name
// what could not be read.
func TestBrowserIntelligenceIsAttachedOnlyForProfilesThatRenderIt(t *testing.T) {
	server, id, record := harDownloadFixture(t)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	executive := &RunArchiveReport{Manifest: manifest}
	server.attachBrowserIntelligenceForProfile(context.Background(), executive, "executive")
	if executive.BrowserIntelligence != nil {
		t.Fatal("the executive profile, which renders no browser section, read browser results")
	}
	engineer := &RunArchiveReport{Manifest: manifest}
	server.attachBrowserIntelligenceForProfile(context.Background(), engineer, "engineer")
	got := engineer.BrowserIntelligence
	if got == nil || got.Findings == nil || got.Findings.Status != "not_evaluated" || got.Findings.RunID != id {
		t.Fatalf("findings %+v", got)
	}
	for _, section := range []string{reportSectionInteractionMap, reportSectionJourneyRisk} {
		if got.Unavailable[section] == "" {
			t.Errorf("%s has no reason: %+v", section, got.Unavailable)
		}
	}
	// Rendering the attached report states each reason.
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(&RunArchiveReport{Manifest: manifest, BrowserIntelligence: got}, &csv, "engineer"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), "Interaction patterns read the finalized archive, which is not available yet.") {
		t.Fatalf("CSV lacks the interaction reason:\n%s", csv.String())
	}
}
