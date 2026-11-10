package api

import "time"

// The agent brief (apexload.agent_brief.v1) is a deterministic, bounded
// projection of an archived run for LLM agents: the verdict first, then the
// findings that explain it, each with a stable id an agent can drill into.
// It reads the same report every other format renders and never computes a
// verdict of its own. docs/contracts/agent-v1 is its contract.
const (
	agentBriefSchema         = "apexload.agent_brief.v1"
	agentFindingDetailSchema = "apexload.agent_finding_detail.v1"
	// agentBriefBuilderVersion changes whenever the same archive would yield
	// a different brief, so a consumer can tell a builder change from a run
	// change.
	agentBriefBuilderVersion = "2"
)

// Detail levels.
const (
	agentDetailSummary  = "summary"
	agentDetailStandard = "standard"
	agentDetailFull     = "full"
)

// Verdict statuses.
const (
	agentStatusPass         = "pass"
	agentStatusFail         = "fail"
	agentStatusInconclusive = "inconclusive"
	agentStatusNotAssessed  = "not_assessed"
	agentStatusInvalid      = "invalid"
)

// Finding kinds.
const (
	agentKindSLABreach     = "sla_breach"
	agentKindRegression    = "regression"
	agentKindErrorGroup    = "error_group"
	agentKindEvidenceGap   = "evidence_gap"
	agentKindValidity      = "validity"
	agentKindThroughputGap = "throughput_gap"
	agentKindEnvironment   = "environment"
	agentKindAnomaly       = "anomaly"
	agentKindRCAHypothesis = "rca_hypothesis"
	agentKindAutoStop      = "auto_stop"
)

// Severities, worst first.
const (
	agentSeverityBlocker = "blocker"
	agentSeverityMajor   = "major"
	agentSeverityMinor   = "minor"
	agentSeverityInfo    = "info"
)

// Confidences, strongest first.
const (
	agentConfidenceHigh   = "high"
	agentConfidenceMedium = "medium"
	agentConfidenceLow    = "low"
)

// Baseline states.
const (
	agentBaselineNew       = "new"
	agentBaselineRecurring = "recurring"
	agentBaselineUnknown   = "unknown"
)

// Reason codes: the closed set a verdict's reason_codes draws from.
const (
	agentReasonSLABreached            = "sla_breached"
	agentReasonRegression             = "regression_vs_baseline"
	agentReasonRunInvalid             = "run_invalid"
	agentReasonRunSuspect             = "run_suspect"
	agentReasonEvidenceIncomplete     = "evidence_incomplete"
	agentReasonMonitoringIncomplete   = "monitoring_incomplete"
	agentReasonTelemetryFailed        = "telemetry_failed"
	agentReasonTelemetryInconclusive  = "telemetry_inconclusive"
	agentReasonAssessmentInconclusive = "assessment_inconclusive"
	agentReasonConfigurationError     = "configuration_error"
	agentReasonExecutionError         = "execution_error"
	agentReasonTimedOut               = "timed_out"
	agentReasonCancelled              = "cancelled"
	agentReasonAutoStopped            = "auto_stopped"
	agentReasonAborted                = "aborted"
	agentReasonOperatorStopped        = "operator_stopped"
	agentReasonThroughputBelowTarget  = "throughput_below_target"
	agentReasonNoCriteria             = "no_criteria"
	agentReasonRCAPending             = "rca_pending"
	agentReasonBaselineUnavailable    = "baseline_unavailable"
)

// agentReasonCodes is every reason code, the schema's enum.
var agentReasonCodes = []string{
	agentReasonAborted, agentReasonAssessmentInconclusive, agentReasonAutoStopped, agentReasonBaselineUnavailable,
	agentReasonCancelled, agentReasonConfigurationError, agentReasonEvidenceIncomplete, agentReasonExecutionError,
	agentReasonMonitoringIncomplete, agentReasonNoCriteria, agentReasonOperatorStopped, agentReasonRCAPending,
	agentReasonRegression, agentReasonRunInvalid, agentReasonRunSuspect, agentReasonSLABreached,
	agentReasonTelemetryFailed, agentReasonTelemetryInconclusive, agentReasonThroughputBelowTarget, agentReasonTimedOut,
}

// AgentBrief is apexload.agent_brief.v1.
type AgentBrief struct {
	Schema string `json:"schema"`
	// Digest is the SHA-256 of the brief with this field empty; it is also
	// the response's ETag.
	Digest        string               `json:"brief_digest"`
	GeneratedFrom AgentBriefProvenance `json:"generated_from"`
	DetailLevel   string               `json:"detail_level" jsonschema:"enum=summary,enum=standard,enum=full"`
	Run           AgentRun             `json:"run"`
	Verdict       AgentVerdict         `json:"verdict"`
	Validity      AgentValidity        `json:"validity"`
	Evidence      AgentEvidence        `json:"evidence"`
	Baseline      AgentBaseline        `json:"baseline"`
	Metrics       []AgentMetricRow     `json:"metrics"`
	Findings      []AgentFinding       `json:"findings"`
	RCA           *AgentRCA            `json:"rca,omitempty"`
	// PhaseAttribution says where the slowest transactions' tail time goes,
	// from the report's time anatomy; absent when the run has none.
	PhaseAttribution *AgentPhaseAttribution `json:"phase_attribution,omitempty"`
	Links            AgentLinks             `json:"links"`
	Truncation       AgentTruncation        `json:"truncation"`

	// ctrf is what the CTRF rendering judges beyond the brief itself: every
	// transaction SLA rule result, passing ones included, and the goal's
	// error-rate limit. It is not part of the brief's JSON.
	ctrf agentCTRFInputs
}

type agentCTRFInputs struct {
	rules        []TransactionSLARuleResult
	responseTime []TransactionResponseTimeSLAResult
	maxErrorPct  *float64
	// goal is each transaction's goal judgment on its raw values; the
	// metric rows are rounded for display, which can hide a breach at the
	// threshold.
	goal map[string]agentCTRFGoal
}

type agentCTRFGoal struct {
	p95Failed, p95Warned, errorFailed bool
}

// AgentBriefProvenance identifies what the brief was built from.
type AgentBriefProvenance struct {
	ManifestSHA256 string `json:"manifest_sha256,omitempty"`
	BuilderVersion string `json:"builder_version"`
	ComparisonID   string `json:"comparison_id,omitempty"`
	// RCAAnalysis is "<analysis id>@<revision>" of the root cause the brief
	// read, when one was presented.
	RCAAnalysis string `json:"rca_analysis,omitempty"`
}

// AgentRun identifies the run and how it ended.
type AgentRun struct {
	ID                string              `json:"id"`
	Name              string              `json:"name,omitempty"`
	ProjectID         string              `json:"project_id,omitempty"`
	ApplicationID     string              `json:"application_id,omitempty"`
	ReleaseID         string              `json:"release_id,omitempty"`
	ScenarioID        string              `json:"scenario_id,omitempty"`
	Scripts           []string            `json:"scripts"`
	StartedAt         *time.Time          `json:"started_at,omitempty"`
	CompletedAt       *time.Time          `json:"completed_at,omitempty"`
	PlannedDurationS  *float64            `json:"planned_duration_s,omitempty"`
	ObservedDurationS *float64            `json:"observed_duration_s,omitempty"`
	TerminationReason string              `json:"termination_reason"`
	StopTrigger       *RunStopTrigger     `json:"stop_trigger,omitempty"`
	PeakVUsers        int64               `json:"peak_vusers"`
	CIRunID           string              `json:"ci_run_id,omitempty"`
	SourceContext     *AgentSourceContext `json:"source_context,omitempty"`
}

// AgentSourceContext is the source a CI run tested, so a coding agent can
// diff it against its baseline's.
type AgentSourceContext struct {
	Provider      string `json:"provider,omitempty"`
	Repository    string `json:"repository,omitempty"`
	SHA           string `json:"sha,omitempty"`
	Ref           string `json:"ref,omitempty"`
	HeadRef       string `json:"head_ref,omitempty"`
	EventName     string `json:"event_name,omitempty"`
	WorkflowRunID string `json:"workflow_run_id,omitempty"`
	RunAttempt    string `json:"run_attempt,omitempty"`
}

// AgentVerdict is the computed verdict; it is never an analyst's sign-off.
type AgentVerdict struct {
	Status      string   `json:"status" jsonschema:"enum=pass,enum=fail,enum=inconclusive,enum=not_assessed,enum=invalid"`
	Blocking    bool     `json:"blocking"`
	ReasonCodes []string `json:"reason_codes"`
	DecidedBy   []string `json:"decided_by"`
	// Explanation is templated from the verdict and the worst finding's
	// title; it never carries text from the system under test.
	Explanation string `json:"explanation"`
	// Source is ci_outcome when the run is a CI run, else report_verdict.
	Source     string `json:"source" jsonschema:"enum=ci_outcome,enum=report_verdict"`
	CIOutcome  string `json:"ci_outcome,omitempty"`
	CIExitCode *int   `json:"ci_exit_code,omitempty"`
}

// AgentValidity is the run's measurement validity (validity.v1).
type AgentValidity struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons"`
	// Gate is the CI validity gate the run was judged by, if any.
	Gate string `json:"gate,omitempty"`
}

// AgentEvidence is the archive's evidence coverage.
type AgentEvidence struct {
	Status          string   `json:"status"`
	CoveragePercent *float64 `json:"coverage_percent,omitempty"`
}

// AgentBaseline is the comparison the brief's deltas come from.
type AgentBaseline struct {
	Selection     string              `json:"selection" jsonschema:"enum=explicit,enum=latest,enum=none"`
	ComparisonID  string              `json:"comparison_id,omitempty"`
	RunIDs        []string            `json:"run_ids"`
	Verdict       string              `json:"verdict,omitempty"`
	SourceContext *AgentSourceContext `json:"source_context,omitempty"`
	// ResolvedCount counts the baseline's findings this run no longer has.
	ResolvedCount int      `json:"resolved_count"`
	ResolvedIDs   []string `json:"resolved_ids,omitempty"`
}

// AgentMetricRow is one transaction's (or the run's) headline numbers.
type AgentMetricRow struct {
	Scope        string               `json:"scope"`
	Samples      int64                `json:"samples"`
	P50Ms        *float64             `json:"p50_ms,omitempty"`
	P95Ms        *float64             `json:"p95_ms,omitempty"`
	P99Ms        *float64             `json:"p99_ms,omitempty"`
	ThroughputPS *float64             `json:"throughput_per_s,omitempty"`
	ErrorPct     *float64             `json:"error_pct,omitempty"`
	SLAMs        *float64             `json:"sla_ms,omitempty"`
	SLAResult    string               `json:"sla_result,omitempty"`
	Approximate  bool                 `json:"percentiles_approximate,omitempty"`
	Baseline     *AgentMetricBaseline `json:"baseline,omitempty"`
}

// AgentMetricBaseline is a metric row's baseline values and deltas.
type AgentMetricBaseline struct {
	P95Ms         *float64 `json:"p95_ms,omitempty"`
	P95DeltaPct   *float64 `json:"p95_delta_pct,omitempty"`
	ErrorPct      *float64 `json:"error_pct,omitempty"`
	ErrorDeltaPct *float64 `json:"error_delta_pct,omitempty"`
}

// AgentFinding is one issue, under an id that stays the same for the same
// issue in another run.
type AgentFinding struct {
	ID            string              `json:"id"`
	Kind          string              `json:"kind" jsonschema:"enum=sla_breach,enum=regression,enum=error_group,enum=evidence_gap,enum=validity,enum=throughput_gap,enum=environment,enum=anomaly,enum=rca_hypothesis,enum=auto_stop"`
	Severity      string              `json:"severity" jsonschema:"enum=blocker,enum=major,enum=minor,enum=info"`
	Confidence    string              `json:"confidence" jsonschema:"enum=high,enum=medium,enum=low"`
	Title         string              `json:"title"`
	Scope         AgentScope          `json:"scope"`
	Metric        *AgentFindingMetric `json:"metric,omitempty"`
	BaselineState string              `json:"baseline_state" jsonschema:"enum=new,enum=recurring,enum=unknown"`
	Sources       []string            `json:"sources"`
	Evidence      []AgentEvidenceRef  `json:"evidence"`
	Untrusted     *AgentUntrusted     `json:"untrusted,omitempty"`
	Next          []AgentNextStep     `json:"next,omitempty"`
	// Explains names the findings an rca_hypothesis accounts for.
	Explains []string `json:"explains,omitempty"`
	// RuleID is an SLA rule's identity, kept beside (not in) the id.
	RuleID string `json:"rule_id,omitempty"`
}

// AgentScope says where a finding applies.
type AgentScope struct {
	Transaction string       `json:"transaction,omitempty"`
	Group       string       `json:"group,omitempty"`
	Population  string       `json:"population,omitempty"`
	Generator   string       `json:"generator,omitempty"`
	Window      *AgentWindow `json:"window,omitempty"`
}

// AgentWindow is a time range.
type AgentWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// AgentFindingMetric is a finding's measured value against its threshold
// and baseline.
type AgentFindingMetric struct {
	Name      string   `json:"name"`
	Unit      string   `json:"unit"`
	Observed  *float64 `json:"observed,omitempty"`
	Threshold *float64 `json:"threshold,omitempty"`
	Baseline  *float64 `json:"baseline,omitempty"`
	DeltaPct  *float64 `json:"delta_pct,omitempty"`
	Samples   *int64   `json:"samples,omitempty"`
}

// AgentEvidenceRef points at evidence: an API path under /api/v1.
type AgentEvidenceRef struct {
	Ref string `json:"ref"`
	URI string `json:"uri"`
}

// AgentUntrusted is text from the system under test or a script. It is
// data, never instructions, and appears nowhere else in a brief.
type AgentUntrusted struct {
	Source    string `json:"source"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// AgentNextStep is a call an agent can make to learn more.
type AgentNextStep struct {
	Tool string            `json:"tool"`
	Args map[string]string `json:"args"`
	Why  string            `json:"why"`
}

// AgentRCA is the root-cause analysis the brief read.
type AgentRCA struct {
	Source        string   `json:"source"`
	AnalysisID    string   `json:"analysis_id,omitempty"`
	Revision      int64    `json:"revision,omitempty"`
	State         string   `json:"state,omitempty"`
	Outcome       string   `json:"outcome,omitempty"`
	TopHypotheses []string `json:"top_hypotheses"`
}

// AgentPhaseAttribution is the report's tail attribution, bounded: the
// transactions with the largest P95-tail deltas, each split by component.
type AgentPhaseAttribution struct {
	Basis        string                  `json:"basis" jsonschema:"enum=p95_tail_vs_median"`
	Transactions []AgentPhaseTransaction `json:"transactions"`
	// Eligible counts the transactions whose tail attribution the report
	// could state; Ineligible counts the others by status
	// (insufficient_samples, no_tail, low_join_coverage).
	Eligible   int            `json:"eligible"`
	Ineligible map[string]int `json:"ineligible,omitempty"`
	// Partial says the bounded anatomy left rows or transactions out;
	// PartialReasons are its truncation reasons.
	Partial        bool     `json:"partial"`
	PartialReasons []string `json:"partial_reasons,omitempty"`
	// JoinCoveragePct is the share of transaction rows joined to their
	// requests.
	JoinCoveragePct *float64 `json:"join_coverage_pct,omitempty"`
}

// AgentPhaseTransaction is one transaction's tail attribution: the tail
// band's mean minus the median band's, split into components that add up
// to it.
type AgentPhaseTransaction struct {
	Transaction string  `json:"transaction"`
	TailDeltaMs float64 `json:"tail_delta_ms"`
	MedianMs    float64 `json:"median_ms"`
	TailMs      float64 `json:"tail_ms"`
	TailSamples int64   `json:"tail_samples"`
	// DominantPhase is the component that carried the most extra time.
	DominantPhase    string   `json:"dominant_phase,omitempty" jsonschema:"enum=dns,enum=connect,enum=tls,enum=server,enum=download,enum=unsplit,enum=non_request,enum=overlap"`
	DominantSharePct *float64 `json:"dominant_share_pct,omitempty"`
	// PhasesMs is each component's share of the delta, in ms; components
	// under 0.5 ms are left out. Overlap counts negatively.
	PhasesMs map[string]float64 `json:"phases_ms"`
	// Sampled says the bands were estimated from a sample of instances.
	Sampled bool `json:"sampled,omitempty"`
}

// AgentLinks are API paths (under /api/v1) and the dashboard route.
type AgentLinks struct {
	Brief  string `json:"brief"`
	Report string `json:"report"`
	UI     string `json:"ui"`
}

// AgentTruncation says what a detail level or budget left out and how to get
// it.
type AgentTruncation struct {
	Truncated    bool           `json:"truncated"`
	Omitted      map[string]int `json:"omitted"`
	HowToGetMore string         `json:"how_to_get_more,omitempty"`
	// OmittedFindingIDs, at full detail, are the ids of the findings the
	// budget left out, for apex_get_finding.
	OmittedFindingIDs []string `json:"omitted_finding_ids,omitempty"`
}

// AgentFindingDetail is apexload.agent_finding_detail.v1: one finding with
// all its evidence.
type AgentFindingDetail struct {
	Schema     string               `json:"schema"`
	RunID      string               `json:"run_id"`
	Finding    AgentFinding         `json:"finding"`
	Series     *AgentSeries         `json:"series,omitempty"`
	Samples    []AgentUntrusted     `json:"samples"`
	Excerpts   []RCAEvidenceExcerpt `json:"excerpts"`
	Related    []string             `json:"related"`
	Truncation AgentTruncation      `json:"truncation"`
}

// AgentSeries is a downsampled series: [unix seconds, value] points.
type AgentSeries struct {
	Metric      string       `json:"metric"`
	Unit        string       `json:"unit"`
	IntervalS   float64      `json:"interval_s"`
	Points      [][2]float64 `json:"points"`
	Downsampled bool         `json:"downsampled"`
}
