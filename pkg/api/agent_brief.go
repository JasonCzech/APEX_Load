package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/runcontract"
)

// agentBriefInputs is everything a brief reads. The handler gathers it; the
// builder is a pure function of it.
type agentBriefInputs struct {
	// Report is the archive report after attachReportVerdictAndSLA,
	// attachIdentityNames, attachRootCause and attachRootCauseSummary.
	Report         *RunArchiveReport
	ManifestSHA256 string
	Comparison     *BaselineComparisonSnapshot
	// BaselineSelection is explicit, latest or none.
	BaselineSelection string
	// BaselineFindingIDs are the baseline run's finding ids, when known.
	BaselineFindingIDs []string
	BaselineSource     *AgentSourceContext
	// CI is the run's evaluated CI response, when the run is a CI run.
	CI         *ciRunResponse
	CIMetadata *ciRunMetadata
}

// agentBriefOptions selects what a brief keeps.
type agentBriefOptions struct {
	Detail string
	// Unbounded keeps every finding and metric row: finding lookups use it.
	Unbounded bool
}

// agentDetailBudget is one detail level's caps.
type agentDetailBudget struct {
	JSONBytes        int
	MarkdownBytes    int
	ListEntries      int // scripts and validity reasons each
	ListOmittedIDs   bool
	MetricRows       int
	Findings         int
	FindingsFloor    int
	UntrustedRunes   int
	NextFindings     int
	EvidencePerEntry int
}

// agentMaxOmittedFindingIDs bounds the omitted-id list a full brief keeps.
const agentMaxOmittedFindingIDs = 200

// agentDetailBudgets assume about 3 bytes per token, conservative for
// compact JSON. An MCP result carries the JSON and the Markdown together, so
// each level's two caps sum to its token budget: about 2.5k, 9k and 22k
// tokens, the last under Claude Code's 25k tool-result cap.
var agentDetailBudgets = map[string]agentDetailBudget{
	agentDetailSummary:  {JSONBytes: 4608, MarkdownBytes: 3072, ListEntries: 5, MetricRows: 5, Findings: 5, FindingsFloor: 3, UntrustedRunes: 0, NextFindings: 3, EvidencePerEntry: 1},
	agentDetailStandard: {JSONBytes: 16384, MarkdownBytes: 10240, ListEntries: 20, MetricRows: 15, Findings: 25, FindingsFloor: 10, UntrustedRunes: 256, NextFindings: 25, EvidencePerEntry: 3},
	agentDetailFull:     {JSONBytes: 40960, MarkdownBytes: 24576, ListEntries: math.MaxInt32, ListOmittedIDs: true, MetricRows: 50, Findings: math.MaxInt32, FindingsFloor: 10, UntrustedRunes: 512, NextFindings: math.MaxInt32, EvidencePerEntry: math.MaxInt32},
}

func normalizeAgentDetail(detail string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case "":
		return agentDetailStandard, true
	case agentDetailSummary, agentDetailStandard, agentDetailFull:
		return strings.ToLower(strings.TrimSpace(detail)), true
	}
	return "", false
}

// buildAgentBrief projects an archived run into apexload.agent_brief.v1.
// The same inputs always produce the same bytes.
func buildAgentBrief(in agentBriefInputs, opts agentBriefOptions) (*AgentBrief, error) {
	report := in.Report
	if report == nil || report.Manifest == nil {
		return nil, fmt.Errorf("archived run evidence is unavailable")
	}
	detail, ok := normalizeAgentDetail(opts.Detail)
	if !ok {
		return nil, fmt.Errorf("unknown detail level %q", opts.Detail)
	}
	manifest := report.Manifest
	runID := manifest.TestID
	brief := &AgentBrief{
		Schema:        agentBriefSchema,
		GeneratedFrom: AgentBriefProvenance{ManifestSHA256: in.ManifestSHA256, BuilderVersion: agentBriefBuilderVersion},
		DetailLevel:   detail,
		Run:           agentRunFromReport(report, in.CIMetadata),
		Links: AgentLinks{
			Brief:  agentTestPath(runID, "/archive/agent-brief"),
			Report: agentTestPath(runID, "/archive/report"),
			UI:     "#/report/" + url.PathEscape(runID) + "?section=summary",
		},
		Truncation: AgentTruncation{Omitted: map[string]int{}},
	}
	gate := ""
	if in.CIMetadata != nil {
		gate = in.CIMetadata.ValidityGate
	}
	brief.Validity = agentValidityOf(report, gate)
	brief.Evidence = agentEvidenceOf(report)
	brief.Baseline = agentBaselineOf(in)
	if in.Comparison != nil {
		brief.GeneratedFrom.ComparisonID = in.Comparison.ID
	}
	brief.RCA = agentRCAOf(report)
	if brief.RCA != nil && brief.RCA.AnalysisID != "" {
		brief.GeneratedFrom.RCAAnalysis = brief.RCA.AnalysisID + "@" + strconv.FormatInt(brief.RCA.Revision, 10)
	}

	findings := collectAgentFindings(agentFindingInputs{Report: report, Comparison: in.Comparison, ValidityGate: gate})
	brief.Baseline.ResolvedIDs, brief.Baseline.ResolvedCount = assignAgentBaselineState(findings, in.BaselineFindingIDs, in.BaselineSelection != agentBaselineSelectionNone && in.BaselineFindingIDs != nil)
	rankAgentFindings(findings)
	if brief.RCA != nil {
		for _, finding := range findings {
			if finding.Kind == agentKindRCAHypothesis && len(brief.RCA.TopHypotheses) < 3 {
				brief.RCA.TopHypotheses = append(brief.RCA.TopHypotheses, finding.ID)
			}
		}
	}
	brief.Verdict = deriveAgentVerdict(report, in, findings, brief)
	brief.Findings = findings
	brief.Metrics = agentMetricRows(report, in.Comparison)
	var phaseOmitted int
	if brief.PhaseAttribution, phaseOmitted = agentPhaseOf(report, detail); phaseOmitted > 0 {
		brief.Truncation.Omitted["phase_attribution"] = phaseOmitted
	}
	brief.ctrf = agentCTRFInputsOf(report)
	// A drill-down reads the finding the way this brief did: against the
	// same explicit comparison (a regression exists only there), or with no
	// baseline when the brief had none.
	var drillKey, drillValue string
	switch {
	case in.Comparison != nil:
		// The newest comparison can change; pin the one this brief read.
		drillKey, drillValue = "comparison_id", in.Comparison.ID
	case in.BaselineSelection == agentBaselineSelectionNone:
		drillKey, drillValue = "baseline", agentBaselineSelectionNone
	}
	if drillKey != "" {
		detailPath := agentTestPath(manifest.TestID, "/archive/agent-brief/findings/")
		for i := range brief.Findings {
			for j := range brief.Findings[i].Next {
				if brief.Findings[i].Next[j].Tool == "apex_get_finding" {
					brief.Findings[i].Next[j].Args[drillKey] = drillValue
				}
			}
			for j := range brief.Findings[i].Evidence {
				if uri := brief.Findings[i].Evidence[j].URI; strings.HasPrefix(uri, detailPath) && !strings.Contains(uri, "?") {
					brief.Findings[i].Evidence[j].URI = uri + "?" + url.Values{drillKey: {drillValue}}.Encode()
				}
			}
		}
	}
	roundAgentBrief(brief)
	if detail != agentDetailFull {
		// Only a full brief lists resolved ids; clear them before the budget
		// so it does not pay for bytes that are never sent.
		brief.Baseline.ResolvedIDs = nil
	} else if cut := len(brief.Baseline.ResolvedIDs) - agentMaxOmittedFindingIDs; cut > 0 && !opts.Unbounded {
		brief.Baseline.ResolvedIDs = brief.Baseline.ResolvedIDs[:agentMaxOmittedFindingIDs]
		brief.Truncation.Omitted["resolved_ids"] = cut
	}
	if !opts.Unbounded {
		applyAgentBudget(brief, agentDetailBudgets[detail])
	} else {
		trimAgentUntrusted(brief.Findings, agentDetailBudgets[agentDetailFull].UntrustedRunes)
	}
	if err := sealAgentBrief(brief); err != nil {
		return nil, err
	}
	return brief, nil
}

func agentRunFromReport(report *RunArchiveReport, metadata *ciRunMetadata) AgentRun {
	manifest := report.Manifest
	provenance := manifest.Provenance
	initial := provenance
	if manifest.InitialProvenance != nil {
		initial = *manifest.InitialProvenance
	}
	run := AgentRun{
		ID:                manifest.TestID,
		Name:              manifest.TestName,
		ProjectID:         provenance.ProjectID,
		ApplicationID:     provenance.ApplicationID,
		ReleaseID:         provenance.ReleaseID,
		ScenarioID:        provenance.ScenarioID,
		Scripts:           []string{},
		TerminationReason: firstNonEmpty(manifest.TerminationReason, "unknown"),
		PeakVUsers:        report.Overview.PeakActiveVUsers,
	}
	scripts := map[string]bool{}
	for _, group := range initial.Groups {
		if name := strings.TrimSpace(group.ScriptName); name != "" {
			scripts[name] = true
		}
	}
	for name := range scripts {
		run.Scripts = append(run.Scripts, name)
	}
	sort.Strings(run.Scripts)
	if provenance.StartedAt != nil {
		started := provenance.StartedAt.UTC().Truncate(time.Second)
		run.StartedAt = &started
	}
	if completed := archiveExecutionCompletedAt(manifest); !completed.IsZero() {
		completed = completed.UTC().Truncate(time.Second)
		run.CompletedAt = &completed
		if provenance.StartedAt != nil && !completed.Before(*provenance.StartedAt) {
			run.ObservedDurationS = agentFloat(archiveExecutionCompletedAt(manifest).Sub(*provenance.StartedAt).Seconds())
		}
	}
	if duration, err := time.ParseDuration(initial.Duration); err == nil && duration >= 0 {
		run.PlannedDurationS = agentFloat(duration.Seconds())
	}
	// The trigger is kept only when the rule's own stop request went out
	// (a stop an operator issued first clears it), so it stands even when an
	// operator also pressed Stop while the run drained.
	if manifest.StopTrigger != nil {
		trigger := *manifest.StopTrigger
		trigger.At = trigger.At.UTC().Truncate(time.Second)
		run.StopTrigger = &trigger
	}
	if metadata != nil {
		run.CIRunID = manifest.TestID
		run.SourceContext = agentSourceContextOf(metadata.Source)
	}
	return run
}

func agentSourceContextOf(source ciRunSource) *AgentSourceContext {
	context := &AgentSourceContext{
		Provider: source.Provider, Repository: source.Repository, SHA: source.SHA, Ref: source.Ref,
		HeadRef: source.HeadRef, EventName: source.EventName, WorkflowRunID: source.RunID, RunAttempt: source.RunAttempt,
	}
	if *context == (AgentSourceContext{}) {
		return nil
	}
	return context
}

func agentValidityOf(report *RunArchiveReport, gate string) AgentValidity {
	validity := AgentValidity{Verdict: rca.ValidityUnknown, Reasons: []string{}, Gate: gate}
	if report.Validity != nil {
		validity.Verdict = firstNonEmpty(report.Validity.Verdict, rca.ValidityUnknown)
		for _, reason := range report.Validity.Reasons {
			validity.Reasons = append(validity.Reasons, agentSafeText(reason, agentTitleMaxRunes))
		}
		sort.Strings(validity.Reasons)
	}
	return validity
}

func agentEvidenceOf(report *RunArchiveReport) AgentEvidence {
	evidence := AgentEvidence{Status: "unavailable"}
	if report.Completeness.Status != "" {
		evidence.Status = "partial"
		if report.Completeness.Status == "complete" {
			evidence.Status = "complete"
		}
		evidence.CoveragePercent = agentFloat(report.Completeness.ScorePct)
	}
	return evidence
}

// Baseline selections.
const (
	agentBaselineSelectionExplicit = "explicit"
	agentBaselineSelectionLatest   = "latest"
	agentBaselineSelectionNone     = "none"
)

func agentBaselineOf(in agentBriefInputs) AgentBaseline {
	baseline := AgentBaseline{Selection: firstNonEmpty(in.BaselineSelection, agentBaselineSelectionNone), RunIDs: []string{}, SourceContext: in.BaselineSource}
	if in.Comparison == nil {
		return baseline
	}
	baseline.ComparisonID = in.Comparison.ID
	for _, run := range in.Comparison.Baselines {
		baseline.RunIDs = append(baseline.RunIDs, run.ID)
	}
	sort.Strings(baseline.RunIDs)
	if in.Comparison.Verdict != nil {
		baseline.Verdict = in.Comparison.Verdict.Status
	}
	return baseline
}

func agentRCAOf(report *RunArchiveReport) *AgentRCA {
	view, summary := report.RootCauseView, report.RootCauseSummary
	if view == nil && summary == nil {
		return nil
	}
	result := &AgentRCA{TopHypotheses: []string{}}
	if view != nil {
		result.Source = view.Source
		result.AnalysisID = view.AnalysisID
		result.Revision = view.Revision
		result.Outcome = view.Outcome
	}
	if summary != nil {
		result.State = summary.State
		if result.AnalysisID == "" {
			result.AnalysisID = summary.AnalysisID
			result.Revision = summary.Revision
		}
	}
	return result
}

// assignAgentBaselineState marks each finding new or recurring against the
// baseline's finding ids, and returns the baseline ids this run no longer
// has. Without baseline ids every finding is unknown.
func assignAgentBaselineState(findings []AgentFinding, baselineIDs []string, known bool) ([]string, int) {
	if !known {
		for i := range findings {
			findings[i].BaselineState = agentBaselineUnknown
		}
		return nil, 0
	}
	prior := map[string]bool{}
	for _, id := range baselineIDs {
		prior[id] = true
	}
	current := map[string]bool{}
	for i := range findings {
		current[findings[i].ID] = true
		if prior[findings[i].ID] {
			findings[i].BaselineState = agentBaselineRecurring
		} else {
			findings[i].BaselineState = agentBaselineNew
		}
	}
	resolved := []string{}
	for id := range prior {
		if !current[id] {
			resolved = append(resolved, id)
		}
	}
	sort.Strings(resolved)
	return resolved, len(resolved)
}

// agentCIStatus maps a CI outcome to a brief status, its primary reason and
// what decided it. Execution failures are not_assessed: a run that did not
// execute is neither an invalid measurement nor a performance failure.
func agentCIStatus(outcome string) (status, reason, decidedBy string) {
	switch outcome {
	case runcontract.CIOutcomePassed:
		return agentStatusPass, "", ""
	case runcontract.CIOutcomeSLABreached:
		return agentStatusFail, agentReasonSLABreached, "sla"
	case runcontract.CIOutcomeRunInvalid:
		return agentStatusInvalid, agentReasonRunInvalid, "validity"
	case runcontract.CIOutcomeTelemetryFailed:
		return agentStatusFail, agentReasonTelemetryFailed, "evidence"
	case runcontract.CIOutcomeTelemetryInconclusive:
		return agentStatusInconclusive, agentReasonTelemetryInconclusive, "evidence"
	case runcontract.CIOutcomeConfigurationError:
		return agentStatusNotAssessed, agentReasonConfigurationError, "execution"
	case runcontract.CIOutcomeMonitoringIncomplete:
		return agentStatusInconclusive, agentReasonMonitoringIncomplete, "evidence"
	case runcontract.CIOutcomeInconclusive:
		return agentStatusInconclusive, agentReasonAssessmentInconclusive, "sla"
	case runcontract.CIOutcomeExecutionError:
		return agentStatusNotAssessed, agentReasonExecutionError, "execution"
	case runcontract.CIOutcomeTimedOut:
		return agentStatusNotAssessed, agentReasonTimedOut, "execution"
	case runcontract.CIOutcomeCancelled:
		return agentStatusNotAssessed, agentReasonCancelled, "execution"
	}
	return agentStatusInconclusive, agentReasonAssessmentInconclusive, "execution"
}

// deriveAgentVerdict reads the verdict the platform already computed: a CI
// run's outcome (so the brief cannot disagree with the CI gate) or else the
// report verdict. It adds only reason codes.
func deriveAgentVerdict(report *RunArchiveReport, in agentBriefInputs, findings []AgentFinding, brief *AgentBrief) AgentVerdict {
	verdict := AgentVerdict{ReasonCodes: []string{}, DecidedBy: []string{}}
	reasons := map[string]bool{}
	decided := map[string]bool{}
	if in.CI != nil && in.CI.Outcome != "" {
		status, reason, by := agentCIStatus(in.CI.Outcome)
		code := runcontract.CIResultExitCode(in.CI.Outcome, in.CI.Status)
		if code == 1 && status == agentStatusNotAssessed {
			// A run that executed, then failed or was aborted, fails the
			// gate as a test failure; the brief says so too.
			status = agentStatusFail
		}
		verdict.Status, verdict.Source, verdict.CIOutcome, verdict.CIExitCode = status, "ci_outcome", in.CI.Outcome, &code
		verdict.Blocking = code != 0
		if reason != "" {
			reasons[reason] = true
		}
		if by != "" {
			decided[by] = true
		}
	} else {
		reportVerdict := deriveReportVerdict(report)
		switch reportVerdict.Status {
		case reportVerdictPass:
			verdict.Status = agentStatusPass
		case reportVerdictFail:
			verdict.Status = agentStatusFail
			decided["sla"] = true
		case reportVerdictNotAssessed:
			verdict.Status = agentStatusNotAssessed
			reasons[agentReasonNoCriteria] = true
		default:
			verdict.Status = agentStatusInconclusive
			reasons[agentReasonAssessmentInconclusive] = true
		}
		if brief.Validity.Verdict == rca.ValidityInvalid {
			verdict.Status = agentStatusInvalid
			decided["validity"] = true
		}
		verdict.Source = "report_verdict"
		verdict.Blocking = verdict.Status == agentStatusFail || verdict.Status == agentStatusInvalid
	}
	for _, finding := range findings {
		switch finding.Kind {
		case agentKindSLABreach:
			if finding.Severity == agentSeverityBlocker {
				reasons[agentReasonSLABreached] = true
				decided["sla"] = true
			}
		case agentKindRegression:
			reasons[agentReasonRegression] = true
			decided["baseline"] = true
		case agentKindThroughputGap:
			reasons[agentReasonThroughputBelowTarget] = true
		case agentKindAutoStop:
			reasons[agentReasonAutoStopped] = true
		}
	}
	switch brief.Validity.Verdict {
	case rca.ValidityInvalid:
		reasons[agentReasonRunInvalid] = true
	case rca.ValiditySuspect:
		reasons[agentReasonRunSuspect] = true
	}
	if brief.Evidence.Status != "complete" {
		reasons[agentReasonEvidenceIncomplete] = true
		decided["evidence"] = verdict.Status == agentStatusInconclusive || decided["evidence"]
	}
	switch brief.Run.TerminationReason {
	case "aborted", "failed":
		reasons[agentReasonAborted] = true
		decided["execution"] = true
	case "operator_stopped":
		// A graceful automatic stop is archived as operator_stopped too;
		// the stop trigger says no operator was involved.
		// A graceful automatic stop is archived as operator_stopped too;
		// only a recorded operator makes it one.
		if brief.Run.StopTrigger == nil || report.Manifest.Provenance.StoppedBySubject != "" {
			reasons[agentReasonOperatorStopped] = true
		}
	}
	if brief.RCA != nil && (brief.RCA.State == "queued" || brief.RCA.State == "running") {
		reasons[agentReasonRCAPending] = true
	}
	if brief.Baseline.Selection == agentBaselineSelectionLatest && in.Comparison == nil {
		reasons[agentReasonBaselineUnavailable] = true
	}
	for reason := range reasons {
		verdict.ReasonCodes = append(verdict.ReasonCodes, reason)
	}
	sort.Strings(verdict.ReasonCodes)
	for by, ok := range decided {
		if ok {
			verdict.DecidedBy = append(verdict.DecidedBy, by)
		}
	}
	sort.Strings(verdict.DecidedBy)
	verdict.Explanation = agentVerdictExplanation(verdict, findings)
	return verdict
}

func agentVerdictExplanation(verdict AgentVerdict, findings []AgentFinding) string {
	blockers := 0
	for _, finding := range findings {
		if finding.Severity == agentSeverityBlocker {
			blockers++
		}
	}
	lead := ""
	if len(findings) > 0 && findings[0].Severity != agentSeverityInfo {
		lead = " Worst finding: " + findings[0].Title + "."
	}
	switch verdict.Status {
	case agentStatusPass:
		if verdict.Blocking {
			return "The run passed its criteria but the CI gate still blocks it." + lead
		}
		if len(findings) == 0 {
			return "The run met its acceptance criteria with no findings."
		}
		return "The run met its acceptance criteria; " + reportCountNoun(int64(len(findings)), "finding", "findings") + " to review."
	case agentStatusFail:
		if verdict.CIOutcome == runcontract.CIOutcomeExecutionError || verdict.CIOutcome == runcontract.CIOutcomeCancelled {
			return "The run did not finish normally: its CI outcome is " + verdict.CIOutcome + ", which the CI gate treats as a test failure." + lead
		}
		return "The run failed its acceptance criteria with " + reportCountNoun(int64(blockers), "blocking finding", "blocking findings") + "." + lead
	case agentStatusInvalid:
		return "The run's measurement is invalid, so it cannot judge the system under test." + lead
	case agentStatusNotAssessed:
		if verdict.CIOutcome != "" {
			return "The run was not assessed: its CI outcome is " + verdict.CIOutcome + "."
		}
		return "The run was not assessed: no acceptance criteria were configured." + lead
	}
	return "The run's assessment is inconclusive." + lead
}

// agentMetricRows is the overall row then the transactions, worst first.
func agentMetricRows(report *RunArchiveReport, comparison *BaselineComparisonSnapshot) []AgentMetricRow {
	overview := report.Overview
	overall := AgentMetricRow{
		Scope:       "overall",
		Samples:     overview.Transactions,
		P50Ms:       agentPositive(overview.P50Ms),
		P95Ms:       agentPositive(overview.P95Ms),
		P99Ms:       agentPositive(overview.P99Ms),
		ErrorPct:    agentFloat(overview.ErrorPct),
		Approximate: overview.PercentileQuality == "approximate",
	}
	if seconds := agentRunSeconds(report); seconds > 0 {
		overall.ThroughputPS = agentFloat(float64(overview.Transactions) / seconds)
	}
	rows := []AgentMetricRow{overall}
	stats := report.ByAction
	if report.MetricPopulations != nil {
		stats = report.ByTransaction
	}
	slaRows := report.SLARows
	if slaRows == nil {
		slaRows = buildSLAComplianceRowMap(report)
	}
	baselines := agentComparisonP95ByName(comparison)
	seconds := agentRunSeconds(report)
	var transactions []AgentMetricRow
	for name, stat := range stats {
		row := AgentMetricRow{
			Scope:       name,
			Samples:     stat.Transactions,
			P50Ms:       agentPositive(stat.P50Ms),
			P95Ms:       agentPositive(stat.P95Ms),
			P99Ms:       agentPositive(stat.P99Ms),
			ErrorPct:    agentFloat(stat.ErrorPct),
			Approximate: stat.PercentileQuality == "approximate",
		}
		if seconds > 0 {
			row.ThroughputPS = agentFloat(float64(stat.Transactions) / seconds)
		}
		if sla, ok := slaRows[name]; ok {
			row.SLAResult = sla.ResultKind
			if sla.HasSLA {
				row.SLAMs = agentFloat(sla.SLAMs)
			}
		}
		if base, ok := baselines[name]; ok && base > 0 {
			row.Baseline = &AgentMetricBaseline{P95Ms: agentFloat(base)}
			if row.P95Ms != nil {
				row.Baseline.P95DeltaPct = agentFloat((*row.P95Ms - base) / base * 100)
			}
		}
		transactions = append(transactions, row)
	}
	slaRank := map[string]int{"fail": 0, "warn": 1}
	sort.Slice(transactions, func(i, j int) bool {
		a, b := transactions[i], transactions[j]
		ra, oka := slaRank[a.SLAResult]
		rb, okb := slaRank[b.SLAResult]
		if !oka {
			ra = 2
		}
		if !okb {
			rb = 2
		}
		if ra != rb {
			return ra < rb
		}
		da, db := 0.0, 0.0
		if a.Baseline != nil && a.Baseline.P95DeltaPct != nil {
			da = abs(*a.Baseline.P95DeltaPct)
		}
		if b.Baseline != nil && b.Baseline.P95DeltaPct != nil {
			db = abs(*b.Baseline.P95DeltaPct)
		}
		if da != db {
			return da > db
		}
		if a.Samples != b.Samples {
			return a.Samples > b.Samples
		}
		return a.Scope < b.Scope
	})
	return append(rows, transactions...)
}

// agentComparisonP95ByName is each compared transaction's first baseline
// successful p95, by transaction name.
func agentComparisonP95ByName(comparison *BaselineComparisonSnapshot) map[string]float64 {
	out := map[string]float64{}
	if comparison == nil || comparison.Verdict == nil {
		return out
	}
	names := map[string]string{}
	for _, row := range comparison.Transactions {
		names[row.ID] = row.Name
	}
	for _, evidence := range comparison.Verdict.Evidence {
		if evidence.Metric != "successful_p95" || evidence.Baseline == nil {
			continue
		}
		if name := names[evidence.RowID]; name != "" {
			if _, seen := out[name]; !seen {
				out[name] = *evidence.Baseline
			}
		}
	}
	return out
}

func agentRunSeconds(report *RunArchiveReport) float64 {
	manifest := report.Manifest
	if manifest == nil || manifest.Provenance.StartedAt == nil {
		return 0
	}
	return archiveExecutionCompletedAt(manifest).Sub(*manifest.Provenance.StartedAt).Seconds()
}

func agentPositive(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return agentFloat(v)
}

// agentRound rounds a value the way every brief does: milliseconds to one
// decimal under 100 and whole above, percentages to two decimals, anything
// else to three significant figures. Fixed rounding keeps briefs byte-stable
// and lets a claim check compare an agent's numbers with the brief's.
func agentRound(v float64, unit string) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	switch unit {
	case "ms":
		if math.Abs(v) < 100 {
			return math.Round(v*10) / 10
		}
		return math.Round(v)
	case "pct":
		return math.Round(v*100) / 100
	case "count", "bytes":
		return math.Round(v)
	}
	if v == 0 {
		return 0
	}
	magnitude := math.Pow(10, 2-math.Floor(math.Log10(math.Abs(v))))
	return math.Round(v*magnitude) / magnitude
}

// agentFormatNumber renders a rounded value with its unit for a templated
// sentence.
func agentFormatNumber(v float64, unit string) string {
	text := strconv.FormatFloat(agentRound(v, unit), 'f', -1, 64)
	switch unit {
	case "ms":
		return text + " ms"
	case "pct":
		return text + "%"
	case "per_s":
		return text + "/s"
	}
	return text
}

func roundAgentPtr(v *float64, unit string) *float64 {
	if v == nil {
		return nil
	}
	return agentFloat(agentRound(*v, unit))
}

func roundAgentBrief(brief *AgentBrief) {
	brief.Run.PlannedDurationS = roundAgentPtr(brief.Run.PlannedDurationS, "")
	brief.Run.ObservedDurationS = roundAgentPtr(brief.Run.ObservedDurationS, "")
	brief.Evidence.CoveragePercent = roundAgentPtr(brief.Evidence.CoveragePercent, "pct")
	for i := range brief.Metrics {
		row := &brief.Metrics[i]
		row.P50Ms, row.P95Ms, row.P99Ms = roundAgentPtr(row.P50Ms, "ms"), roundAgentPtr(row.P95Ms, "ms"), roundAgentPtr(row.P99Ms, "ms")
		row.ThroughputPS, row.ErrorPct, row.SLAMs = roundAgentPtr(row.ThroughputPS, ""), roundAgentPtr(row.ErrorPct, "pct"), roundAgentPtr(row.SLAMs, "ms")
		if row.Baseline != nil {
			row.Baseline.P95Ms = roundAgentPtr(row.Baseline.P95Ms, "ms")
			row.Baseline.P95DeltaPct = roundAgentPtr(row.Baseline.P95DeltaPct, "pct")
			row.Baseline.ErrorPct = roundAgentPtr(row.Baseline.ErrorPct, "pct")
			row.Baseline.ErrorDeltaPct = roundAgentPtr(row.Baseline.ErrorDeltaPct, "pct")
		}
	}
	for i := range brief.Findings {
		if metric := brief.Findings[i].Metric; metric != nil {
			metric.Observed = roundAgentPtr(metric.Observed, metric.Unit)
			metric.Threshold = roundAgentPtr(metric.Threshold, metric.Unit)
			metric.Baseline = roundAgentPtr(metric.Baseline, metric.Unit)
			metric.DeltaPct = roundAgentPtr(metric.DeltaPct, "pct")
		}
	}
}

func trimAgentUntrusted(findings []AgentFinding, maxRunes int) {
	for i := range findings {
		untrusted := findings[i].Untrusted
		if untrusted == nil {
			continue
		}
		if maxRunes <= 0 {
			findings[i].Untrusted = nil
			continue
		}
		if runes := []rune(untrusted.Text); len(runes) > maxRunes {
			trimmed := *untrusted
			trimmed.Text = string(runes[:maxRunes])
			trimmed.Truncated = true
			findings[i].Untrusted = &trimmed
		}
	}
}

// applyAgentBudget applies a detail level's structural caps, then keeps the
// most findings (and, past the floor, metric rows) for which both the JSON
// and its Markdown fit their byte caps. Findings and rows are ranked, so the
// kept ones are a prefix; the prefix length is found by an exponential then
// a binary search, so the work tracks what fits rather than how many
// findings the run has. Every cut is counted.
func applyAgentBudget(brief *AgentBrief, budget agentDetailBudget) {
	omitted := map[string]int{}
	for key, n := range brief.Truncation.Omitted {
		omitted[key] = n
	}
	if len(brief.Run.Scripts) > budget.ListEntries {
		omitted["scripts"] += len(brief.Run.Scripts) - budget.ListEntries
		brief.Run.Scripts = brief.Run.Scripts[:budget.ListEntries]
	}
	if len(brief.Validity.Reasons) > budget.ListEntries {
		omitted["validity_reasons"] += len(brief.Validity.Reasons) - budget.ListEntries
		brief.Validity.Reasons = brief.Validity.Reasons[:budget.ListEntries]
	}
	metrics := brief.Metrics
	structuralMetrics := 0
	if limit := budget.MetricRows + 1; len(metrics) > limit {
		structuralMetrics = len(metrics) - limit
		metrics = metrics[:limit]
	}
	all := brief.Findings
	most := min(len(all), budget.Findings)
	trimAgentUntrusted(all[:most], budget.UntrustedRunes)
	// evidenceCut[i] is the evidence cut from finding i; only kept
	// findings' cuts count, since a dropped finding is counted whole.
	evidenceCut := make([]int, most)
	for i := range all[:most] {
		finding := &all[i]
		if len(finding.Evidence) > budget.EvidencePerEntry {
			evidenceCut[i] = len(finding.Evidence) - budget.EvidencePerEntry
			finding.Evidence = finding.Evidence[:budget.EvidencePerEntry]
		}
		if i >= budget.NextFindings {
			finding.Next = nil
		}
	}
	keep := func(findings, rows int) {
		brief.Findings, brief.Metrics = all[:findings], metrics[:rows]
		brief.Truncation.Omitted = map[string]int{}
		for key, n := range omitted {
			brief.Truncation.Omitted[key] = n
		}
		if cut := len(all) - findings; cut > 0 {
			brief.Truncation.Omitted["findings"] += cut
		}
		evidence := 0
		for _, cut := range evidenceCut[:findings] {
			evidence += cut
		}
		if evidence > 0 {
			brief.Truncation.Omitted["evidence"] += evidence
		}
		if cut := structuralMetrics + len(metrics) - rows; cut > 0 {
			brief.Truncation.Omitted["metrics"] += cut
		}
		brief.Truncation.OmittedFindingIDs = nil
		if budget.ListOmittedIDs {
			// The highest-ranked omitted findings, in rank order.
			for _, finding := range all[findings:min(len(all), findings+agentMaxOmittedFindingIDs)] {
				brief.Truncation.OmittedFindingIDs = append(brief.Truncation.OmittedFindingIDs, finding.ID)
			}
		}
		brief.Truncation.Truncated = len(brief.Truncation.Omitted) > 0
		brief.Truncation.HowToGetMore = agentHowToGetMore(brief)
	}
	fits := func(findings, rows int) bool {
		keep(findings, rows)
		jsonSize, markdownSize := agentBriefSizes(brief)
		return jsonSize <= budget.JSONBytes && markdownSize <= budget.MarkdownBytes
	}
	// largestFitting returns the largest n in [lo, hi] with fit(n), given
	// fit(lo); it probes upward exponentially, then bisects.
	largestFitting := func(lo, hi int, fit func(int) bool) int {
		step := max(1, lo)
		for lo < hi {
			probe := min(hi, lo+step)
			if !fit(probe) {
				hi = probe - 1
				break
			}
			lo, step = probe, step*2
		}
		for lo < hi {
			mid := lo + (hi-lo+1)/2
			if fit(mid) {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo
	}
	rows := len(metrics)
	// Phase rows give way first. Each cut is counted before the next fit is
	// measured, so the brief is weighed with its own omitted count and
	// how-to-get-more sentence.
	agentTrimPhaseAttribution(brief.PhaseAttribution, func() bool { return fits(most, rows) }, func() { omitted["phase_attribution"]++ })
	floor := min(budget.FindingsFloor, most)
	switch {
	case fits(floor, rows):
		keep(largestFitting(floor, most, func(n int) bool { return fits(n, rows) }), rows)
	case rows > 1 && fits(floor, 1):
		keep(floor, largestFitting(1, rows, func(n int) bool { return fits(floor, n) }))
	case fits(0, min(rows, 1)):
		keep(largestFitting(0, floor, func(n int) bool { return fits(n, min(rows, 1)) }), min(rows, 1))
	default:
		keep(0, min(rows, 1))
	}
}

func agentHowToGetMore(brief *AgentBrief) string {
	if len(brief.Truncation.Omitted) == 0 {
		return ""
	}
	keys := make([]string, 0, len(brief.Truncation.Omitted))
	for key := range brief.Truncation.Omitted {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", brief.Truncation.Omitted[key], key))
	}
	var next []string
	if brief.DetailLevel != agentDetailFull {
		next = append(next, "call apex_run_brief with detail=full")
	}
	if omitted := brief.Truncation.Omitted["findings"]; omitted > 0 && brief.DetailLevel == agentDetailFull {
		listed := len(brief.Truncation.OmittedFindingIDs)
		if listed < omitted {
			next = append(next, fmt.Sprintf("truncation.omitted_finding_ids lists the first %d of the %d omitted findings; fetch each with apex_get_finding", listed, omitted))
		} else {
			next = append(next, "truncation.omitted_finding_ids lists the omitted findings; fetch each with apex_get_finding")
		}
	}
	if brief.Truncation.Omitted["metrics"] > 0 {
		next = append(next, "the archive report lists every transaction")
	}
	if brief.Truncation.Omitted["phase_attribution"] > 0 {
		next = append(next, "the archive report's time_anatomy splits every transaction")
	}
	if brief.Truncation.Omitted["resolved_ids"] > 0 {
		next = append(next, "the baseline run's own brief lists its findings")
	}
	if brief.Truncation.Omitted["scripts"] > 0 || brief.Truncation.Omitted["validity_reasons"] > 0 {
		next = append(next, "the archive report and manifest hold the full run details")
	}
	if len(next) == 0 {
		next = append(next, "call apex_get_finding(run_id, finding_id) for one finding")
	}
	return strings.Join(parts, ", ") + " omitted; " + strings.Join(next, "; ") + "."
}

func agentBriefSizes(brief *AgentBrief) (int, int) {
	saved := brief.Digest
	brief.Digest = strings.Repeat("0", sha256.Size*2)
	defer func() { brief.Digest = saved }()
	body, err := json.Marshal(brief)
	if err != nil {
		return 0, 0
	}
	return len(body), len(renderAgentBriefMarkdown(brief))
}

// sealAgentBrief computes the digest over the brief with the digest empty.
func sealAgentBrief(brief *AgentBrief) error {
	brief.Digest = ""
	body, err := json.Marshal(brief)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	brief.Digest = hex.EncodeToString(sum[:])
	return nil
}

// buildAgentBriefFromCIOutcome is the brief of a finished CI run whose
// archive cannot be read: its identity, source and CI verdict, with no
// metrics or findings. Evidence is "unavailable", and the reason codes say
// so.
func buildAgentBriefFromCIOutcome(test *ManagedTest, response ciRunResponse, metadata ciRunMetadata, detail string) (*AgentBrief, error) {
	detail, ok := normalizeAgentDetail(detail)
	if !ok {
		return nil, fmt.Errorf("unknown detail level %q", detail)
	}
	status, reason, decidedBy := agentCIStatus(response.Outcome)
	code := runcontract.CIResultExitCode(response.Outcome, response.Status)
	if code == 1 && status == agentStatusNotAssessed {
		status = agentStatusFail
	}
	verdict := AgentVerdict{
		Status: status, Blocking: code != 0, Source: "ci_outcome", CIOutcome: response.Outcome, CIExitCode: &code,
		ReasonCodes: []string{agentReasonEvidenceIncomplete}, DecidedBy: []string{},
	}
	if reason != "" && reason != agentReasonEvidenceIncomplete {
		verdict.ReasonCodes = append(verdict.ReasonCodes, reason)
	}
	sort.Strings(verdict.ReasonCodes)
	if decidedBy != "" {
		verdict.DecidedBy = []string{decidedBy}
	}
	verdict.Explanation = "The run's archive is unavailable, so only its CI outcome is known: " + response.Outcome + "."
	brief := &AgentBrief{
		Schema:        agentBriefSchema,
		GeneratedFrom: AgentBriefProvenance{BuilderVersion: agentBriefBuilderVersion},
		DetailLevel:   detail,
		Run: AgentRun{
			ID: test.ID, Name: test.Name, ProjectID: test.ProjectID, ApplicationID: test.ApplicationID, ReleaseID: test.ReleaseID,
			ScenarioID: test.ScenarioID, Scripts: []string{}, TerminationReason: "unknown", CIRunID: test.ID,
			SourceContext: agentSourceContextOf(metadata.Source),
		},
		Verdict:    verdict,
		Validity:   AgentValidity{Verdict: rca.ValidityUnknown, Reasons: []string{}, Gate: metadata.ValidityGate},
		Evidence:   AgentEvidence{Status: "unavailable"},
		Baseline:   AgentBaseline{Selection: agentBaselineSelectionNone, RunIDs: []string{}},
		Metrics:    []AgentMetricRow{},
		Findings:   []AgentFinding{},
		Links:      AgentLinks{Brief: agentTestPath(test.ID, "/archive/agent-brief"), Report: agentTestPath(test.ID, "/archive/report"), UI: "#/report/" + url.PathEscape(test.ID) + "?section=summary"},
		Truncation: AgentTruncation{Omitted: map[string]int{}},
	}
	if test.StartedAt != nil {
		started := test.StartedAt.UTC().Truncate(time.Second)
		brief.Run.StartedAt = &started
	}
	if test.EndedAt != nil {
		ended := test.EndedAt.UTC().Truncate(time.Second)
		brief.Run.CompletedAt = &ended
	}
	if err := sealAgentBrief(brief); err != nil {
		return nil, err
	}
	return brief, nil
}
