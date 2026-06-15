package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// ReportAnnotations holds the analyst-editable commentary attached to a
// generated report. It is persisted per (test, profile) so an analyst can
// revise the narrative and per-section comments and have them survive across
// sessions and appear in every rendered/downloaded copy of the report.
type ReportAnnotations struct {
	TestID             string            `json:"test_id"`
	Profile            string            `json:"profile"`
	Title              string            `json:"title"`
	PreparedBy         string            `json:"prepared_by"`
	ExecutiveNarrative string            `json:"executive_narrative"`
	SectionComments    map[string]string `json:"section_comments"`
	// VerdictOverride, when non-empty, replaces the auto-derived headline
	// verdict (e.g. "Conditional Pass" for a transient, self-recovered breach).
	// VerdictExplanation accompanies it in the verdict bar.
	VerdictOverride    string `json:"verdict_override,omitempty"`
	VerdictExplanation string `json:"verdict_explanation,omitempty"`
	// Findings, when non-empty, replaces the auto-derived findings list with an
	// analyst-curated set rendered verbatim in the Findings section.
	Findings  []ReportFinding `json:"findings,omitempty"`
	UpdatedAt time.Time       `json:"updated_at"`
	UpdatedBy string          `json:"updated_by"`
}

// reportCommentarySection describes a labeled, analyst-editable comment block.
// The same ordered set drives the default-text seeding, the rendered HTML
// report, and the dashboard composer textareas.
type reportCommentarySection struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// reportCommentarySectionOrder is the canonical, ordered list of comment
// sections an analyst can fill in. Kept profile-independent so Executive and
// Detailed reports expose the same narrative structure.
func reportCommentarySectionOrder() []reportCommentarySection {
	return []reportCommentarySection{
		{ID: "overview", Label: "Test Overview"},
		{ID: "performance", Label: "Performance & Throughput"},
		{ID: "sla", Label: "SLA / Goal Assessment"},
		{ID: "errors", Label: "Errors & Stability"},
		{ID: "recommendations", Label: "Recommendations & Next Steps"},
	}
}

func reportAnnotationsSettingKey(testID, profile string) string {
	return fmt.Sprintf("report:annotations:%s:%s", strings.TrimSpace(testID), normalizeReportProfileID(profile))
}

// normalizeReportProfileID resolves an incoming profile id to a known report
// profile id, defaulting to the engineer (Detailed Technical) profile to match
// lookupRunArchiveReportProfile.
func normalizeReportProfileID(profile string) string {
	resolved, ok := lookupRunArchiveReportProfile(profile)
	if !ok {
		return "engineer"
	}
	return resolved.ID
}

// loadReportAnnotations returns the persisted annotations for a test+profile,
// or (nil, nil) when none have been saved yet.
func loadReportAnnotations(store storage.Store, testID, profile string) (*ReportAnnotations, error) {
	if store == nil {
		return nil, nil
	}
	raw, err := store.GetSetting(reportAnnotationsSettingKey(testID, profile))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var ann ReportAnnotations
	if err := json.Unmarshal([]byte(raw), &ann); err != nil {
		return nil, fmt.Errorf("decode report annotations: %w", err)
	}
	if ann.SectionComments == nil {
		ann.SectionComments = map[string]string{}
	}
	return &ann, nil
}

// saveReportAnnotations persists the annotations as JSON keyed by test+profile.
func saveReportAnnotations(store storage.Store, ann *ReportAnnotations) error {
	if store == nil {
		return fmt.Errorf("storage backend unavailable")
	}
	if ann == nil {
		return fmt.Errorf("nil annotations")
	}
	data, err := json.Marshal(ann)
	if err != nil {
		return fmt.Errorf("encode report annotations: %w", err)
	}
	return store.SaveSetting(reportAnnotationsSettingKey(ann.TestID, ann.Profile), string(data))
}

// resolveReportAnnotations returns the saved annotations when present, otherwise
// freshly generated defaults seeded from the report data. The result always has
// a populated SectionComments map covering reportCommentarySectionOrder so the
// composer and renderer have a stable shape to work with.
func resolveReportAnnotations(store storage.Store, report *RunArchiveReport, testID, profile string) (*ReportAnnotations, error) {
	saved, err := loadReportAnnotations(store, testID, profile)
	if err != nil {
		return nil, err
	}
	defaults := defaultReportAnnotations(report, testID, profile)
	if saved == nil {
		return defaults, nil
	}
	// Backfill any sections the analyst has not yet touched so newly added
	// sections still render with generated starter text.
	if saved.SectionComments == nil {
		saved.SectionComments = map[string]string{}
	}
	for id, text := range defaults.SectionComments {
		if strings.TrimSpace(saved.SectionComments[id]) == "" {
			saved.SectionComments[id] = text
		}
	}
	if strings.TrimSpace(saved.Title) == "" {
		saved.Title = defaults.Title
	}
	if strings.TrimSpace(saved.ExecutiveNarrative) == "" {
		saved.ExecutiveNarrative = defaults.ExecutiveNarrative
	}
	saved.TestID = testID
	saved.Profile = normalizeReportProfileID(profile)
	return saved, nil
}

// defaultReportAnnotations seeds sensible starter text from the report so a
// freshly generated report reads well before the analyst edits anything.
func defaultReportAnnotations(report *RunArchiveReport, testID, profile string) *ReportAnnotations {
	profileID := normalizeReportProfileID(profile)
	label := profileID
	if p, ok := lookupRunArchiveReportProfile(profileID); ok {
		label = p.Label
	}

	testName := testID
	slaBreached := false
	var slaDetails []string
	if report != nil && report.Manifest != nil {
		if strings.TrimSpace(report.Manifest.TestName) != "" {
			testName = report.Manifest.TestName
		}
		slaBreached = report.Manifest.SLABreached
		slaDetails = report.Manifest.SLADetails
	}

	ann := &ReportAnnotations{
		TestID:          testID,
		Profile:         profileID,
		Title:           fmt.Sprintf("%s — %s Report", testName, label),
		SectionComments: map[string]string{},
	}

	if report == nil {
		ann.ExecutiveNarrative = "No archived run evidence was available to summarize this test."
		for _, section := range reportCommentarySectionOrder() {
			ann.SectionComments[section.ID] = ""
		}
		return ann
	}

	ov := report.Overview
	verdict := "met its SLA targets"
	if slaBreached {
		verdict = "breached one or more SLA targets"
	}
	ann.ExecutiveNarrative = fmt.Sprintf(
		"This run executed %s transaction(s) at a peak of %.1f TPS with %.2f%% errors and a %.1f ms average response time, reaching %s active VUsers. The test %s.",
		formatPDFInt(ov.Transactions), ov.PeakTPS, ov.ErrorPct, ov.AvgResponseMs, formatPDFInt(ov.PeakActiveVUsers), verdict,
	)

	ann.SectionComments["overview"] = fmt.Sprintf(
		"%s transaction(s) were recorded with a %.2f%% error rate across the run.",
		formatPDFInt(ov.Transactions), ov.ErrorPct,
	)
	ann.SectionComments["performance"] = fmt.Sprintf(
		"Throughput peaked at %.1f TPS and response time ranged from %.1f ms to %.1f ms (avg %.1f ms).",
		ov.PeakTPS, ov.MinResponseMs, ov.MaxResponseMs, ov.AvgResponseMs,
	)
	if slaBreached && len(slaDetails) > 0 {
		ann.SectionComments["sla"] = "SLA breached: " + strings.Join(slaDetails, "; ") + "."
	} else if slaBreached {
		ann.SectionComments["sla"] = "One or more SLA targets were breached during the run."
	} else {
		ann.SectionComments["sla"] = "All configured SLA targets were met."
	}
	if len(report.ErrorPatterns) > 0 {
		top := report.ErrorPatterns[0]
		ann.SectionComments["errors"] = fmt.Sprintf(
			"%d distinct error pattern(s) were observed; the most frequent was %q (%d occurrence(s)).",
			len(report.ErrorPatterns), top.Sample, top.Count,
		)
	} else {
		ann.SectionComments["errors"] = "No significant error patterns were recorded during the run."
	}
	ann.SectionComments["recommendations"] = "Document recommended follow-up actions, retest scope, or sign-off here."

	return ann
}
