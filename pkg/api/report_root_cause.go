package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"regexp"
	"slices"
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
	// BrowserSelection is v2's browser baseline admission receipt
	// (browser_selection.v1), present even when no baseline comparison ran.
	BrowserSelection *ReportBrowserSelection `json:"browser_selection,omitempty"`
}

// ReportBrowserSelection presents the assessment's browser baseline admission
// receipt (browser_selection.v1) and candidate rejections.
type ReportBrowserSelection struct {
	Version        string                          `json:"version"`
	Policy         string                          `json:"policy,omitempty"`
	Status         string                          `json:"status"`
	InventoryCount int                             `json:"inventory_count"`
	SelectedTestID string                          `json:"selected_test_id,omitempty"`
	SelectedDigest string                          `json:"selected_digest,omitempty"`
	ReceiptDigest  string                          `json:"receipt_digest,omitempty"`
	Reasons        []string                        `json:"reasons,omitempty"`
	Rejected       []rca.BrowserCandidateRejection `json:"rejected,omitempty"`
	Summary        string                          `json:"summary"`
}

func reportBrowserSelectionFromV2(sel *rca.BrowserBaselineSelection, receiptDigest string) *ReportBrowserSelection {
	if sel == nil {
		return nil
	}
	out := &ReportBrowserSelection{
		Version:        sel.Version,
		Policy:         sel.Policy,
		Status:         sel.Status,
		InventoryCount: sel.InventoryCount,
		SelectedTestID: sel.SelectedTestID,
		SelectedDigest: sel.SelectedDigest,
		ReceiptDigest:  receiptDigest,
		Reasons:        append([]string(nil), sel.Reasons...),
	}
	for _, rej := range sel.Rejected {
		out.Rejected = append(out.Rejected, rca.BrowserCandidateRejection{
			TestID:  rej.TestID,
			Digest:  rej.Digest,
			Reasons: append([]string(nil), rej.Reasons...),
		})
	}
	out.Summary = reportBrowserSelectionSummary(out)
	return out
}

func reportBrowserSelectionSummary(s *ReportBrowserSelection) string {
	parts := []string{fmt.Sprintf("Browser baseline selection (%s, %s): %d candidate%s inspected", s.Version, strings.ToLower(rcaHumanize(s.Status)), s.InventoryCount, pluralSuffix(s.InventoryCount))}
	if s.Policy != "" {
		parts = append(parts, "policy "+s.Policy)
	}
	if s.SelectedTestID != "" {
		sel := "selected " + s.SelectedTestID
		if s.SelectedDigest != "" {
			sel += " (" + s.SelectedDigest + ")"
		}
		parts = append(parts, sel)
	}
	if s.ReceiptDigest != "" {
		parts = append(parts, "receipt "+s.ReceiptDigest)
	}
	return strings.Join(parts, ", ") + "."
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
	// Qualification states how the comparison was qualified: the baseline
	// run's own qualification when it is not fully qualified, the browser
	// policy it followed, the current buckets it excluded and why, and why
	// cohorts were left unscored (each "reason: count").
	Qualification []string `json:"qualification,omitempty"`
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
	if q := b.Baseline.Qualification; q != "" && q != rca.BaselineQualified {
		line := fmt.Sprintf("Baseline run %s %s", b.Baseline.TestID, q)
		if len(b.Baseline.Reasons) > 0 {
			reasons := make([]string, 0, len(b.Baseline.Reasons))
			for _, reason := range b.Baseline.Reasons {
				reasons = append(reasons, strings.ReplaceAll(reason, "_", " "))
			}
			line += ": " + strings.Join(reasons, ", ")
		}
		out.Qualification = append(out.Qualification, line)
	}
	if b.Policy != "" {
		out.Qualification = append(out.Qualification, "Browser policy "+b.Policy)
	}
	for _, reason := range slices.Sorted(maps.Keys(b.Excluded)) {
		out.Qualification = append(out.Qualification, fmt.Sprintf("Excluded %s: %d bucket%s", strings.ReplaceAll(reason, "_", " "), b.Excluded[reason], pluralSuffix(int(b.Excluded[reason]))))
	}
	unscored := map[string]int{}
	for _, r := range b.Results {
		if r.State != rca.BrowserBaselineAnomaly && r.State != rca.BrowserBaselineWithinRange && r.Reason != "" {
			unscored[r.Reason]++
		}
	}
	for _, reason := range slices.Sorted(maps.Keys(unscored)) {
		out.Qualification = append(out.Qualification, fmt.Sprintf("Unscored %s: %d cohort%s", strings.ReplaceAll(reason, "_", " "), unscored[reason], pluralSuffix(unscored[reason])))
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
	anomalies := "anomalies"
	if b.Anomalies == 1 {
		anomalies = "anomaly"
	}
	return fmt.Sprintf("Browser baseline (%s, %s): %d cohort%s compared with %s at the same load, %d %s, %d unscored.", b.Version, strings.ToLower(rcaHumanize(b.Status)), b.Compared, pluralSuffix(b.Compared), against, b.Anomalies, anomalies, b.Unscored)
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
	if rcaV2PresentEnabled(s.store, project) {
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
			Falsification: h.Falsification,
		}
		for _, reason := range h.ConfidenceReasons {
			out.Reasons = append(out.Reasons, rcaSentence(reason))
		}
		for _, limitation := range h.Limitations {
			out.Limitations = append(out.Limitations, rcaLimitationSentence(limitation))
		}
		symptoms := make([]string, 0, len(h.Symptoms))
		for _, id := range h.Symptoms {
			symptoms = append(symptoms, label(id))
		}
		// A series that changed more than once is one symptom with a count,
		// not the same label repeated.
		out.Explains = rcaCollapseLabels(symptoms)
		out.Chain = rcaChainLines(h.Chain, anomalies, label)
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
	view.BrowserSelection = reportBrowserSelectionFromV2(a.BrowserSelection, a.Inputs.BrowserSelection)
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

// rcaCollapseLabels keeps each label once, in first-seen order, counting a
// label that repeats: "Login · error rate (2 changes)".
func rcaCollapseLabels(labels []string) []string {
	counts := map[string]int{}
	order := make([]string, 0, len(labels))
	for _, label := range labels {
		if counts[label] == 0 {
			order = append(order, label)
		}
		counts[label]++
	}
	out := make([]string, 0, len(order))
	for _, label := range order {
		if n := counts[label]; n > 1 {
			label = fmt.Sprintf("%s (%d changes)", label, n)
		}
		out = append(out, label)
	}
	return out
}

// rcaChainLines states a hypothesis's evidence chain one line per cause and
// relation, naming every symptom it reaches, instead of one line per step.
func rcaChainLines(chain []rca.ChainStep, anomalies map[string]rca.Anomaly, label func(string) string) []string {
	type group struct {
		cause, relation string
		targets         []string
	}
	var groups []*group
	byKey := map[string]*group{}
	for _, step := range chain {
		cause := label(step.From)
		if anomaly, ok := anomalies[step.From]; ok {
			cause += " (" + rcaEffectText(anomaly) + ")"
		}
		key := cause + "\x00" + step.Relation
		g, ok := byKey[key]
		if !ok {
			g = &group{cause: cause, relation: step.Relation}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.targets = append(g.targets, label(step.To))
	}
	lines := make([]string, 0, len(groups))
	for _, g := range groups {
		lines = append(lines, g.cause+" "+rcaRelationText(g.relation)+" "+strings.Join(rcaCollapseLabels(g.targets), ", "))
	}
	return lines
}

// rcaCompetingPattern matches the engine's "competing <signature> could not
// be judged" limitation, which names the signature by its identifier.
var rcaCompetingPattern = regexp.MustCompile(`competing ([a-z][a-z0-9_]*) could not be judged`)

// rcaLimitationSentence presents an engine limitation for people: signature
// identifiers become their titles and the sentence starts with a capital.
// Stored analyses keep the engine's wording, so this applies to old ones too.
func rcaLimitationSentence(text string) string {
	text = rcaCompetingPattern.ReplaceAllStringFunc(text, func(match string) string {
		name := rcaCompetingPattern.FindStringSubmatch(match)[1]
		return "the competing cause " + rcaSignatureTitle(name) + " could not be judged"
	})
	return rcaSentence(text)
}

// rcaSentence capitalizes the first letter of an engine phrase.
func rcaSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
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
	// Two significant digits under 1, so a small change never reads as zero.
	digits := 2
	if magnitude >= 100 {
		digits = 0
	} else if magnitude >= 10 {
		digits = 1
	} else if magnitude > 0 && magnitude < 1 {
		digits = min(int(math.Ceil(-math.Log10(magnitude)))+1, 6)
	}
	amount := fmt.Sprintf("%.*f", digits, magnitude)
	// Trim only fractional zeros: an integer amount such as 100 keeps its own.
	if strings.Contains(amount, ".") {
		amount = strings.TrimRight(strings.TrimRight(amount, "0"), ".")
	}
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
