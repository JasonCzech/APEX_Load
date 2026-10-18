package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	reportAnnotationsMaxRequestBytes = 512 << 10
	reportAnnotationsMaxStoredBytes  = 256 << 10
	reportAnnotationsMaxFindings     = 100
	reportAnnotationsMaxTitleBytes   = 512
	reportAnnotationsMaxNameBytes    = 256
	reportAnnotationsMaxNarrative    = 32 << 10
	reportAnnotationsMaxComment      = 32 << 10
	reportAnnotationsMaxFindingText  = 8 << 10
)

var errReportAnnotationsTooLarge = errors.New("report annotations exceed the stored-size limit")

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
	// The verdict is stored once per run (runReportVerdict), not per profile.
	// These response-only fields say which audience last set it, when and by
	// whom; they are never written to the per-profile record.
	VerdictProfile   string     `json:"verdict_profile,omitempty"`
	VerdictUpdatedAt *time.Time `json:"verdict_updated_at,omitempty"`
	VerdictUpdatedBy string     `json:"verdict_updated_by,omitempty"`
}

// VerdictAuthor names who set the analyst verdict, falling back to the
// commentary author for records that predate the run-level verdict.
func (a *ReportAnnotations) VerdictAuthor() string {
	if a == nil {
		return ""
	}
	return firstNonEmpty(a.VerdictUpdatedBy, a.UpdatedBy)
}

// reportCommentarySection describes a labeled, analyst-editable comment block.
// The same ordered set drives the default-text seeding, the rendered HTML
// report, and the dashboard composer textareas.
type reportCommentarySection struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// reportCommentarySectionOrder is the canonical, ordered list of comment
// sections an analyst can fill in for a report profile. The optional profile
// parameter keeps older call sites/tests compiling while allowing tailored
// prompts for new profile-aware report composition.
func reportCommentarySectionOrder(profile ...string) []reportCommentarySection {
	profileID := "engineer"
	if len(profile) > 0 {
		profileID = normalizeReportProfileID(profile[0])
	}
	switch profileID {
	case "executive":
		return []reportCommentarySection{
			{ID: "decision", Label: "Decision Context"},
			{ID: "business_impact", Label: "Business Impact"},
			{ID: "sla", Label: "SLA / Release Gate"},
			{ID: "risks", Label: "Top Risks"},
			{ID: "recommendations", Label: "Recommendation & Sign-Off"},
		}
	case "audit":
		return []reportCommentarySection{
			{ID: "audit_scope", Label: "Audit Scope"},
			{ID: "exceptions", Label: "Exceptions / Waivers"},
			{ID: "evidence_notes", Label: "Evidence Notes"},
			{ID: "integrity_notes", Label: "Integrity Notes"},
			{ID: "approvals", Label: "Approval Notes"},
		}
	default:
		return []reportCommentarySection{
			{ID: "overview", Label: "Technical Overview"},
			{ID: "performance", Label: "Performance & Throughput"},
			{ID: "sla", Label: "SLA / Goal Assessment"},
			{ID: "bottlenecks", Label: "Bottlenecks / Root Cause"},
			{ID: "errors", Label: "Errors & Stability"},
			{ID: "recommendations", Label: "Engineering Next Steps"},
		}
	}
}

func reportAnnotationsSettingKey(testID, profile string) string {
	return fmt.Sprintf("report:annotations:%s:%s", strings.TrimSpace(testID), normalizeReportProfileID(profile))
}

// normalizeReportProfileID resolves an incoming profile id to a known report
// profile id, defaulting to the engineer profile to match
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
	if len(raw) > reportAnnotationsMaxStoredBytes {
		return nil, fmt.Errorf("stored report annotations exceed %d bytes", reportAnnotationsMaxStoredBytes)
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

func validateReportAnnotationsRequest(profile string, req *reportAnnotationsRequest) error {
	if req == nil {
		return fmt.Errorf("report annotations are required")
	}
	for label, value := range map[string]string{
		"title": req.Title, "prepared_by": req.PreparedBy,
		"executive_narrative": req.ExecutiveNarrative,
		"verdict_override":    req.VerdictOverride,
		"verdict_explanation": req.VerdictExplanation,
	} {
		limit := reportAnnotationsMaxNarrative
		switch label {
		case "title":
			limit = reportAnnotationsMaxTitleBytes
		case "prepared_by", "verdict_override":
			limit = reportAnnotationsMaxNameBytes
		case "verdict_explanation":
			limit = reportAnnotationsMaxFindingText
		}
		if len(value) > limit {
			return fmt.Errorf("%s exceeds %d bytes", label, limit)
		}
	}
	allowedSections := make(map[string]struct{})
	for _, section := range reportCommentarySectionOrder(profile) {
		allowedSections[section.ID] = struct{}{}
	}
	for key, value := range req.SectionComments {
		if _, ok := allowedSections[key]; !ok {
			return fmt.Errorf("unknown report section %q for profile %q", key, normalizeReportProfileID(profile))
		}
		if len(value) > reportAnnotationsMaxComment {
			return fmt.Errorf("section %q exceeds %d bytes", key, reportAnnotationsMaxComment)
		}
	}
	if len(req.Findings) > reportAnnotationsMaxFindings {
		return fmt.Errorf("findings exceeds limit of %d", reportAnnotationsMaxFindings)
	}
	for index, finding := range req.Findings {
		for label, value := range map[string]string{
			"id": finding.ID, "title": finding.Title, "severity": finding.Severity,
			"observed": finding.Observed, "evidence": finding.Evidence,
			"root_cause": finding.RootCause, "impact": finding.Impact,
		} {
			limit := reportAnnotationsMaxFindingText
			if label == "id" || label == "severity" {
				limit = reportAnnotationsMaxNameBytes
			} else if label == "title" {
				limit = reportAnnotationsMaxTitleBytes
			}
			if len(value) > limit {
				return fmt.Errorf("findings[%d].%s exceeds %d bytes", index, label, limit)
			}
		}
	}
	return nil
}

// saveReportAnnotations persists the annotations as JSON keyed by test+profile.
func saveReportAnnotations(store storage.Store, ann *ReportAnnotations) error {
	if store == nil {
		return fmt.Errorf("storage backend unavailable")
	}
	if ann == nil {
		return fmt.Errorf("nil annotations")
	}
	data, err := marshalReportAnnotations(ann)
	if err != nil {
		return err
	}
	return store.SaveSetting(reportAnnotationsSettingKey(ann.TestID, ann.Profile), string(data))
}

// saveReportAnnotationsIfUnchanged protects the full-document editor from
// overwriting commentary saved by another browser between load and save. The
// annotations timestamp is the user-visible revision while the exact stored
// JSON is the durable CAS witness used across controller replicas.
func saveReportAnnotationsIfUnchanged(store storage.Store, ann *ReportAnnotations, expectedUpdatedAt time.Time) (bool, error) {
	if store == nil {
		return false, fmt.Errorf("storage backend unavailable")
	}
	if ann == nil {
		return false, fmt.Errorf("nil annotations")
	}
	cas, ok := store.(settingsCompareAndSwapper)
	if !ok {
		return false, fmt.Errorf("storage backend does not support report annotation compare-and-swap")
	}
	data, err := marshalReportAnnotations(ann)
	if err != nil {
		return false, err
	}
	key := reportAnnotationsSettingKey(ann.TestID, ann.Profile)
	currentRaw, err := store.GetSetting(key)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(currentRaw) == "" {
		if !expectedUpdatedAt.IsZero() {
			return false, nil
		}
	} else {
		var current ReportAnnotations
		if err := json.Unmarshal([]byte(currentRaw), &current); err != nil {
			return false, fmt.Errorf("decode current report annotations: %w", err)
		}
		if expectedUpdatedAt.IsZero() || !current.UpdatedAt.Equal(expectedUpdatedAt) {
			return false, nil
		}
	}
	return cas.CompareAndSwapSetting(key, currentRaw, string(data))
}

func marshalReportAnnotations(ann *ReportAnnotations) ([]byte, error) {
	data, err := json.Marshal(ann)
	if err != nil {
		return nil, fmt.Errorf("encode report annotations: %w", err)
	}
	if len(data) > reportAnnotationsMaxStoredBytes {
		return nil, fmt.Errorf("%w of %d bytes", errReportAnnotationsTooLarge, reportAnnotationsMaxStoredBytes)
	}
	return data, nil
}

// resolveReportAnnotations returns the saved annotations when present, otherwise
// freshly generated defaults seeded from the report data. The result always has
// a populated SectionComments map covering reportCommentarySectionOrder so the
// composer and renderer have a stable shape to work with.
func resolveReportAnnotations(store storage.Store, report *RunArchiveReport, testID, profile string) (*ReportAnnotations, error) {
	ann, err := resolveProfileReportAnnotations(store, report, testID, profile)
	if err != nil {
		return nil, err
	}
	verdict, err := loadRunReportVerdict(store, testID)
	if err != nil {
		return nil, err
	}
	applyRunReportVerdict(ann, verdict)
	return ann, nil
}

func resolveProfileReportAnnotations(store storage.Store, report *RunArchiveReport, testID, profile string) (*ReportAnnotations, error) {
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
		// Earlier defaults stored the placeholder "Not provided" as text;
		// it reads as content in an export, so it counts as empty.
		if current := strings.TrimSpace(saved.SectionComments[id]); current == "" || current == reportNotProvided {
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

	testName := reportRunName(report)
	if report != nil && report.Manifest != nil {
		if strings.TrimSpace(report.Manifest.TestName) != "" {
			testName = report.Manifest.TestName
		}
	}

	ann := &ReportAnnotations{
		TestID:          testID,
		Profile:         profileID,
		Title:           fmt.Sprintf("%s — %s report", testName, label),
		SectionComments: map[string]string{},
	}

	if report == nil {
		ann.ExecutiveNarrative = "No archived run evidence was available to summarize this test."
		for _, section := range reportCommentarySectionOrder(profileID) {
			ann.SectionComments[section.ID] = ""
		}
		return ann
	}

	ov := report.Overview
	// Narrative numbers come from the same population as the on-screen KPIs.
	headline := reportHeadline(report)
	verdict := "has not been assessed against acceptance criteria"
	assessment := deriveReportVerdict(report)
	if assessment.Status == reportVerdictPass {
		verdict = "met its evaluated acceptance criteria"
	}
	if assessment.Status == reportVerdictConditional {
		verdict = "requires further evidence before acceptance"
	}
	if assessment.Status == reportVerdictFail {
		verdict = "breached one or more SLA targets"
	}
	switch profileID {
	case "executive":
		ann.ExecutiveNarrative = fmt.Sprintf(
			"This run reached %s active VUsers, peaked at %.1f attempts/s, recorded a %s error rate (%s), and %s.",
			formatReportCount(ov.PeakActiveVUsers), headline.PeakTPS, formatReportPercentage(headline.ErrorPct), headline.Label, verdict,
		)
		ann.SectionComments["decision"] = ""
		ann.SectionComments["business_impact"] = fmt.Sprintf("%s: %s error rate and %s average response time.", headline.Label, formatReportPercentage(headline.ErrorPct), formatReportMs(headline.AvgResponseMs))
		ann.SectionComments["sla"] = deriveReportVerdict(report).Explanation
		ann.SectionComments["risks"] = defaultRiskComment(report)
		ann.SectionComments["recommendations"] = ""
	case "audit":
		ann.ExecutiveNarrative = fmt.Sprintf(
			"Audit package for %s. The run archived %s, %s and %s; evidence coverage is %.0f%% (%s).",
			testName, reportCountNoun(ov.Transactions, "archive record", "archive records"), reportCountNoun(ov.ResourceSamples, "resource sample", "resource samples"), reportCountNoun(ov.IntervalSummaries, "interval summary record", "interval summary records"), report.Completeness.ScorePct, reportTokenLabel(report.Completeness.Status),
		)
		ann.SectionComments["audit_scope"] = ""
		ann.SectionComments["exceptions"] = ""
		ann.SectionComments["evidence_notes"] = deriveReportVerdict(report).Explanation
		ann.SectionComments["integrity_notes"] = fmt.Sprintf("Evidence coverage scored %.0f%% with %s.", report.Completeness.ScorePct, reportCountNoun(int64(len(report.Completeness.Findings)), "coverage finding", "coverage findings"))
		ann.SectionComments["approvals"] = ""
	default:
		ann.ExecutiveNarrative = fmt.Sprintf(
			"This run captured %s. %s peaked at %.1f attempts/s with a %s error rate and a %s average response time, reaching %s active VUsers. The test %s.",
			reportCountNoun(ov.Transactions, "archive record", "archive records"), headline.Label, headline.PeakTPS, formatReportPercentage(headline.ErrorPct), formatReportMs(headline.AvgResponseMs), formatReportCount(ov.PeakActiveVUsers), verdict,
		)
		// The narrative already states the record count and error rate.
		ann.SectionComments["overview"] = ""
		ann.SectionComments["performance"] = fmt.Sprintf("Throughput peaked at %.1f attempts/s and response time ranged from %.1f ms to %.1f ms (avg %s; %s).", headline.PeakTPS, headline.MinResponseMs, headline.MaxResponseMs, formatReportMs(headline.AvgResponseMs), headline.Label)
		ann.SectionComments["sla"] = deriveReportVerdict(report).Explanation
		ann.SectionComments["bottlenecks"] = ""
		ann.SectionComments["errors"] = defaultRiskComment(report)
		ann.SectionComments["recommendations"] = ""
	}

	return ann
}

// reportNotProvided is the placeholder older default commentary stored.
const reportNotProvided = "Not provided"

// reportCountNoun states a count with its noun in the right number:
// "1 archive record", "15,940 archive records".
func reportCountNoun(n int64, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return formatReportCount(n) + " " + plural
}

func defaultSLAComment(slaBreached bool, slaDetails []string) string {
	if slaBreached && len(slaDetails) > 0 {
		return "SLA breached: " + strings.Join(slaDetails, "; ") + "."
	}
	if slaBreached {
		return "One or more SLA targets were breached during the run."
	}
	return "All configured SLA targets were met."
}

func defaultRiskComment(report *RunArchiveReport) string {
	if report == nil {
		return "Run evidence is unavailable; performance risk cannot be assessed."
	}
	var risks []string
	for _, finding := range report.Completeness.Findings {
		if finding.Severity == "critical" || finding.Severity == "high" || finding.Severity == "warning" {
			risks = append(risks, finding.Message)
		}
	}
	if report.Manifest == nil || report.Manifest.TerminationReason == "" || report.Manifest.Provenance.StartedAt == nil || archiveExecutionCompletedAt(report.Manifest).IsZero() {
		risks = append(risks, "Run timing or termination evidence is incomplete; completion of the planned workload is unverified.")
	}
	if report.Manifest != nil && report.Manifest.Provenance.TotalVUsers > 0 && report.Overview.PeakActiveVUsers < int64(report.Manifest.Provenance.TotalVUsers) {
		planned := report.Manifest.Provenance.TotalVUsers
		risks = append(risks, fmt.Sprintf("Observed peak load was %d of %d planned VUsers; workload coverage needs review.", report.Overview.PeakActiveVUsers, planned))
	}
	if report.Overview.Transactions < 100 {
		risks = append(risks, fmt.Sprintf("Only %d archive records were captured; this small sample does not establish representative performance.", report.Overview.Transactions))
	}
	if len(report.ErrorPatterns) > 0 {
		top := report.ErrorPatterns[0]
		risks = append(risks, fmt.Sprintf("%s observed; the most frequent was %q (%s).", reportCountNoun(int64(len(report.ErrorPatterns)), "distinct error pattern was", "distinct error patterns were"), top.Sample, reportCountNoun(top.Count, "occurrence", "occurrences")))
	} else {
		risks = append(risks, "No significant error patterns were recorded during the run. Absence of recorded errors does not establish low risk.")
	}
	return strings.Join(risks, " ")
}
