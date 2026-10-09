package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Root-cause presentation sources.
const (
	reportRootCauseV1 = "v1"
	reportRootCauseV2 = "v2"
)

// ReportRootCause is the generated root-cause analysis as every report
// format presents it: the dashboard, HTML, PDF, Word and CSV all render this
// one model. It comes from the run's latest completed v2 assessment where
// the project presents v2, and otherwise from the v1 summary. Analyst
// findings and run annotations are never part of it.
type ReportRootCause struct {
	Source        string     `json:"source"`
	SchemaVersion string     `json:"schema_version"`
	AnalysisID    string     `json:"analysis_id,omitempty"`
	Revision      int64      `json:"revision,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	Engine        string     `json:"engine,omitempty"`
	// Outcome is v2's outcome or v1's status.
	Outcome string `json:"outcome"`
	Summary string `json:"summary"`
	// Confidence is v1's overall confidence; v2 states it per hypothesis.
	Confidence      string                      `json:"confidence,omitempty"`
	Validity        string                      `json:"validity,omitempty"`
	ValidityReasons []string                    `json:"validity_reasons,omitempty"`
	Acceptance      string                      `json:"acceptance,omitempty"`
	Hypotheses      []ReportRootCauseHypothesis `json:"hypotheses"`
	Coverage        []rca.Coverage              `json:"coverage,omitempty"`
	Limitations     []string                    `json:"limitations,omitempty"`
	NextActions     []string                    `json:"next_actions,omitempty"`
	// Notes say why this source was chosen when it is not the one asked for.
	Notes []string `json:"notes,omitempty"`
	// BrowserBaseline is v2's comparison of the run's browser document
	// cohorts with the baseline run's (browser_baseline.v1), when the run
	// had document series.
	BrowserBaseline *ReportBrowserBaseline `json:"browser_baseline,omitempty"`
}

// ReportBrowserBaseline presents the assessment's browser baseline: how many
// document cohorts were scored against the baseline run at the same load,
// how many moved, and the ones that did.
type ReportBrowserBaseline struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	// Baseline names the baseline run the cohorts were compared with.
	Baseline  string `json:"baseline,omitempty"`
	Compared  int    `json:"compared"`
	Anomalies int    `json:"anomalies"`
	Unscored  int    `json:"unscored"`
	// Summary is the one-line account every format prints.
	Summary string `json:"summary"`
	// Findings describe each anomaly, strongest first; the first
	// reportBrowserBaselineFindings are kept.
	Findings    []string `json:"findings,omitempty"`
	Limitations []string `json:"limitations,omitempty"`
}

// reportBrowserBaselineFindings bounds the anomalies a report lists.
const reportBrowserBaselineFindings = 10

// reportBrowserBaselineFromV2 presents a browser baseline, or nil without one.
func reportBrowserBaselineFromV2(b *rca.BrowserBaseline) *ReportBrowserBaseline {
	if b == nil {
		return nil
	}
	out := &ReportBrowserBaseline{Version: b.Version, Status: b.Status, Baseline: b.Baseline.TestID, Findings: []string{}, Limitations: []string{}}
	for _, r := range b.Results {
		switch r.State {
		case rca.BrowserBaselineAnomaly:
			out.Compared++
			out.Anomalies++
			if len(out.Findings) < reportBrowserBaselineFindings {
				out.Findings = append(out.Findings, reportBrowserBaselineFinding(r))
			}
		case rca.BrowserBaselineWithinRange:
			out.Compared++
		default:
			out.Unscored++
		}
	}
	for _, l := range b.Limitations {
		out.Limitations = append(out.Limitations, rcaLimitationText(l))
	}
	out.Summary = reportBrowserBaselineSummary(out)
	return out
}

// reportBrowserBaselineFinding states one cohort anomaly: the cohort and
// level, its p75, and the baseline range it left.
func reportBrowserBaselineFinding(r rca.BrowserBaselineResult) string {
	cohort := strings.ToUpper(r.Metric)
	if len(r.Evidence) > 0 {
		if locator := r.Evidence[0].Locator; strings.Contains(locator, "#") {
			cohort = locator[strings.Index(locator, "#")+1:]
		}
	}
	direction := "higher"
	if r.Direction == rca.DirectionDecrease {
		direction = "lower"
	}
	value := func(v float64) string {
		if strings.EqualFold(r.Unit, "ms") {
			return fmt.Sprintf("%.0f ms", v)
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".")
	}
	return fmt.Sprintf("%s at %s: p75 %s, %s than the baseline's expected %s to %s", cohort, r.Level, value(r.Current), direction, value(r.ExpectedLow), value(r.ExpectedHigh))
}

// reportBrowserBaselineSummary is the browser baseline's one-line account.
func reportBrowserBaselineSummary(b *ReportBrowserBaseline) string {
	against := "a baseline run"
	if b.Baseline != "" {
		against = "baseline run " + b.Baseline
	}
	return fmt.Sprintf("Browser baseline (%s, %s): %d cohorts compared with %s at the same load, %d anomalies, %d unscored.", b.Version, strings.ToLower(rcaHumanize(b.Status)), b.Compared, against, b.Anomalies, b.Unscored)
}

// ReportRootCauseHypothesis is one ranked explanation: a v2 hypothesis or a
// v1 finding.
type ReportRootCauseHypothesis struct {
	ID    string `json:"id,omitempty"`
	Rank  int    `json:"rank"`
	Title string `json:"title"`
	// Confidence is set for v2 hypotheses, Severity for v1 findings.
	Confidence    string   `json:"confidence,omitempty"`
	Severity      string   `json:"severity,omitempty"`
	Detail        string   `json:"detail,omitempty"`
	Explains      []string `json:"explains,omitempty"`
	Reasons       []string `json:"reasons,omitempty"`
	Chain         []string `json:"chain,omitempty"`
	Contradicting []string `json:"contradicting,omitempty"`
	Competing     []string `json:"competing,omitempty"`
	Falsification string   `json:"falsification,omitempty"`
	Limitations   []string `json:"limitations,omitempty"`
	Evidence      []string `json:"evidence,omitempty"`
}

// attachRootCause sets the report's generated root cause: the latest
// completed v2 assessment where the project presents v2, otherwise the v1
// summary. Like the release trend it is attached here, at the storage-aware
// layer, so buildRunArchiveReport stays a pure function of one archive.
func (s *Server) attachRootCause(report *RunArchiveReport) {
	if report == nil || report.Manifest == nil {
		return
	}
	testID := strings.TrimSpace(report.Manifest.TestID)
	project := normalizeProjectID(report.Manifest.Provenance.ProjectID)
	var notes []string
	if rcaV2PresentEnabled(project) {
		view, err := s.latestRootCauseV2(project, testID)
		switch {
		case view != nil:
			report.RootCauseView = view
			return
		case err != nil:
			slog.Warn("archive report: root-cause v2 unavailable", "test", testID, "err", err)
			notes = append(notes, "The run's root-cause v2 analyses could not be read; the v1 summary is shown.")
		default:
			notes = append(notes, "No root-cause v2 analysis has completed for this run; the v1 summary is shown.")
		}
	}
	view := reportRootCauseFromV1(buildRunRootCauseSummary(testID, report))
	view.Notes = notes
	report.RootCauseView = &view
}

// attachRootCauseForProfile attaches the root cause only for profiles that
// render the rca section.
func (s *Server) attachRootCauseForProfile(report *RunArchiveReport, profileID string) {
	if profileIncludesSection(profileID, "rca") {
		s.attachRootCause(report)
	}
}

// latestRootCauseV2 reads the run's latest completed v2 assessment: nil
// without an error when none completed.
func (s *Server) latestRootCauseV2(project, testID string) (*ReportRootCause, error) {
	store, ok := s.store.(storage.RCAStore)
	if !ok {
		return nil, errors.New("root-cause analysis storage unavailable")
	}
	if testID == "" {
		return nil, nil
	}
	latest, err := store.LatestCompletedRCAAnalysis(project, testID)
	if err != nil || latest == nil {
		return nil, err
	}
	var payload rcaAnalysisPayload
	if err := json.Unmarshal(latest.Payload, &payload); err != nil {
		return nil, fmt.Errorf("analysis %s is unreadable: %w", latest.ID, err)
	}
	if payload.Assessment == nil {
		return nil, fmt.Errorf("analysis %s has no assessment", latest.ID)
	}
	view := reportRootCauseFromV2(payload.Assessment, latest.CompletedAt)
	if s.rcaArchiveChanged(context.Background(), testID, payload) {
		view.Notes = append(view.Notes, "This analysis judged an earlier revision of the run's archive; evidence archived since is not in it. Re-analyze the run to include it.")
	}
	return &view, nil
}

// reportRootCauseFromV2 presents a v2 assessment.
func reportRootCauseFromV2(a *rca.Assessment, completedAt *time.Time) ReportRootCause {
	anomalies := make(map[string]rca.Anomaly, len(a.Anomalies))
	for _, anomaly := range a.Anomalies {
		anomalies[anomaly.ID] = anomaly
	}
	label := func(id string) string {
		if anomaly, ok := anomalies[id]; ok {
			return rcaSeriesTitle(anomaly.Series)
		}
		return id
	}
	view := ReportRootCause{
		Source: reportRootCauseV2, SchemaVersion: a.SchemaVersion, AnalysisID: a.AnalysisID, Revision: a.Revision, CompletedAt: completedAt,
		Engine: a.Inputs.Engine, Outcome: a.Outcome, Summary: rcaAssessmentSummary(a, label),
		Validity: a.Validity.Verdict, ValidityReasons: append([]string{}, a.Validity.Reasons...), Acceptance: a.Acceptance.Status,
		Hypotheses: []ReportRootCauseHypothesis{}, Coverage: append([]rca.Coverage{}, a.Coverage...),
	}
	for _, h := range a.Hypotheses {
		out := ReportRootCauseHypothesis{
			ID: h.ID, Rank: h.Rank, Title: rcaSignatureTitle(h.Signature), Confidence: h.Confidence,
			Reasons: append([]string{}, h.ConfidenceReasons...), Falsification: h.Falsification, Limitations: append([]string{}, h.Limitations...),
		}
		for _, id := range h.Symptoms {
			out.Explains = append(out.Explains, label(id))
		}
		for _, step := range h.Chain {
			cause := label(step.From)
			if anomaly, ok := anomalies[step.From]; ok {
				cause += " (" + rcaEffectText(anomaly) + ")"
			}
			out.Chain = append(out.Chain, cause+" "+rcaRelationText(step.Relation)+" "+label(step.To))
		}
		for _, ref := range h.Contradicting {
			out.Contradicting = append(out.Contradicting, ref.Locator)
		}
		for _, ref := range h.Supporting {
			out.Evidence = append(out.Evidence, ref.Locator)
		}
		for _, name := range h.Competing {
			out.Competing = append(out.Competing, rcaSignatureTitle(name))
		}
		view.Hypotheses = append(view.Hypotheses, out)
	}
	view.BrowserBaseline = reportBrowserBaselineFromV2(a.BrowserBaseline)
	for _, l := range a.Limitations {
		view.Limitations = append(view.Limitations, rcaLimitationText(l))
	}
	for _, code := range a.Validity.Limitations {
		view.Limitations = append(view.Limitations, rcaHumanize(code))
	}
	return view
}

// reportRootCauseFromV1 presents a v1 summary; its findings are its ranked
// explanations.
func reportRootCauseFromV1(summary RunRootCauseSummary) ReportRootCause {
	view := ReportRootCause{
		Source: reportRootCauseV1, SchemaVersion: summary.SchemaVersion, Outcome: summary.Status, Summary: summary.Summary,
		Confidence: summary.Confidence, Hypotheses: []ReportRootCauseHypothesis{}, NextActions: append([]string{}, summary.NextActions...),
	}
	for i, finding := range summary.TopFindings {
		view.Hypotheses = append(view.Hypotheses, ReportRootCauseHypothesis{
			Rank: i + 1, Title: finding.Title, Severity: finding.Severity, Detail: finding.Detail, Evidence: append([]string{}, finding.Evidence...),
		})
	}
	return view
}

// reportRootCauseHypotheses is what a profile shows of the hypotheses.
func reportRootCauseHypotheses(view *ReportRootCause, profile RunArchiveReportProfile) []ReportRootCauseHypothesis {
	if view == nil {
		return nil
	}
	if limit := profile.RootCauseHypothesisLimit; limit > 0 && len(view.Hypotheses) > limit {
		return view.Hypotheses[:limit]
	}
	return view.Hypotheses
}

// rcaAssessmentSummary is the assessment's one-line answer.
func rcaAssessmentSummary(a *rca.Assessment, label func(string) string) string {
	if a.Outcome != rca.OutcomeSupported || len(a.Hypotheses) == 0 {
		if a.Outcome == rca.OutcomeInsufficientEvidence {
			return "Root-cause analysis found too little evidence to judge any hypothesis."
		}
		return "Root-cause analysis found no hypothesis the evidence supports."
	}
	top := a.Hypotheses[0]
	subject := ""
	if len(top.Symptoms) > 0 {
		subject = " for " + label(top.Symptoms[0])
	}
	return fmt.Sprintf("%s is the leading hypothesis%s (%s confidence).", rcaSignatureTitle(top.Signature), subject, top.Confidence)
}

var rcaSignatureTitles = map[string]string{
	"cold_start":         "Cold start",
	"consumer_lag":       "Consumer lag",
	"database_wait":      "Database wait",
	"dead_letters":       "Dead letters",
	"garbage_collection": "Garbage collection",
	"network_phase":      "Network phase delay",
	"pool_exhaustion":    "Pool exhaustion",
	"rebalance":          "Consumer rebalance",
	"resource_pressure":  "Resource pressure",
	"retries":            "Retries",
}

// rcaSignatureTitle names a signature for people. The dashboard's
// rcaSignatureLabel holds the same table.
func rcaSignatureTitle(name string) string {
	if title, ok := rcaSignatureTitles[name]; ok {
		return title
	}
	return rcaHumanize(name)
}

var rcaMetricTitles = map[string]string{
	"p95_ms":     "p95 latency",
	"error_rate": "error rate",
	"throughput": "throughput",
	"dns_ms":     "DNS time",
	"connect_ms": "connect time",
	"tls_ms":     "TLS time",
}

// rcaSeriesTitle names a series ID (transaction/<name>/<metric>,
// action/<name>/<metric>, dependency/<label>/<phase> or
// monitor/<provider>/<source>/<metric>/<digest>) for people, as the
// dashboard's rcaSeriesLabel does.
func rcaSeriesTitle(id string) string {
	parts := strings.Split(id, "/")
	if parts[0] == "monitor" && len(parts) >= 5 {
		provider := parts[1]
		if d, ok := monitorprovider.ByProviderName(provider); ok && d.Label != "" {
			provider = d.Label
		}
		return strings.Join(parts[3:len(parts)-1], "/") + " · " + provider
	}
	if len(parts) < 3 {
		return id
	}
	metric := parts[len(parts)-1]
	if title, ok := rcaMetricTitles[metric]; ok {
		metric = title
	} else {
		metric = strings.ReplaceAll(metric, "_", " ")
	}
	return strings.Join(parts[1:len(parts)-1], "/") + " · " + metric
}

func rcaRelationText(relation string) string {
	switch relation {
	case rca.RelationPrecedes:
		return "began before"
	case rca.RelationUnordered:
		return "began at about the same time as"
	case rca.RelationLoadCoincident:
		return "began with a load step, as did"
	}
	return strings.ReplaceAll(relation, "_", " ")
}

// rcaEffectText states an anomaly's change in its unit.
func rcaEffectText(a rca.Anomaly) string {
	if math.IsNaN(a.Effect) || math.IsInf(a.Effect, 0) {
		return "change not recorded"
	}
	sign := ""
	switch {
	case a.Effect > 0:
		sign = "+"
	case a.Effect < 0:
		sign = "-"
	}
	magnitude := math.Abs(a.Effect)
	digits := 2
	if magnitude >= 100 {
		digits = 0
	} else if magnitude >= 10 {
		digits = 1
	}
	amount := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.*f", digits, magnitude), "0"), ".")
	switch a.Unit {
	case "ms":
		return sign + amount + " ms"
	case "%":
		return sign + amount + " pts"
	case "1/s":
		return sign + amount + "/s"
	case "":
		return sign + amount
	}
	return sign + amount + " " + a.Unit
}

func rcaLimitationText(l rca.Limitation) string {
	if l.Detail == "" {
		return rcaHumanize(l.Code)
	}
	return rcaHumanize(l.Code) + ": " + l.Detail
}

func rcaHumanize(value string) string {
	text := strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if text == "" {
		return value
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

// reportRootCauseSourceLabel describes where the generated root cause came
// from.
func reportRootCauseSourceLabel(view *ReportRootCause) string {
	if view == nil {
		return "Not available"
	}
	if view.Source == reportRootCauseV2 {
		return fmt.Sprintf("Root-cause v2, analysis revision %d (%s)", view.Revision, view.Engine)
	}
	return "Root-cause v1 summary (" + view.SchemaVersion + ")"
}
