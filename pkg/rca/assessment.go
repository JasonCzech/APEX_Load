package rca

import (
	"sort"
	"time"
)

// AssessmentSchemaVersion is the v2 root-cause assessment contract.
const AssessmentSchemaVersion = "apexload.root_cause.v2"

// Acceptance statuses: the run's SLA verdict, kept apart from validity.
const (
	AcceptancePassed        = "passed"
	AcceptanceBreached      = "breached"
	AcceptanceNotConfigured = "not_configured"
	AcceptanceUnknown       = "unknown"
)

// Assessment outcomes.
const (
	OutcomeSupported             = "supported"
	OutcomeInsufficientEvidence  = "insufficient_evidence"
	OutcomeNoSupportedHypothesis = "no_supported_hypothesis"
)

// LimitationDetectionUnavailable: onset detection and hypotheses are not
// built yet (Phase 3b), so no anomaly can be supported.
const LimitationDetectionUnavailable = "detection_unavailable"

// Coverage statuses.
const (
	CoverageComplete = "complete"
	CoveragePartial  = "partial"
	CoverageMissing  = "missing"
)

// Assessment is a run's v2 root-cause assessment. The service fills the
// analysis identity, revision and input digests; Analyze fills the rest.
type Assessment struct {
	SchemaVersion string       `json:"schema_version"`
	AnalysisID    string       `json:"analysis_id"`
	Revision      int64        `json:"revision"`
	Inputs        InputDigests `json:"inputs"`
	Acceptance    Acceptance   `json:"acceptance"`
	Validity      Validity     `json:"validity"`
	Anomalies     []Anomaly    `json:"anomalies"`
	Hypotheses    []Hypothesis `json:"hypotheses"`
	Outcome       string       `json:"outcome"`
	Coverage      []Coverage   `json:"coverage"`
	Limitations   []Limitation `json:"limitations"`
	Budget        Budget       `json:"budget"`
}

// InputDigests identify the frozen inputs an assessment read.
type InputDigests struct {
	Archive          string `json:"archive"`
	EvidenceRevision int64  `json:"evidence_revision"`
	Baseline         string `json:"baseline,omitempty"`
	Mapping          string `json:"mapping,omitempty"`
	Policy           string `json:"policy,omitempty"`
	Engine           string `json:"engine"`
	SignatureSet     string `json:"signature_set,omitempty"`
}

// Acceptance is the run's SLA verdict.
type Acceptance struct {
	Status  string   `json:"status"`
	Details []string `json:"details"`
}

// Anomaly is a detected change in one series (Phase 3b).
type Anomaly struct {
	ID               string        `json:"id"`
	Series           string        `json:"series"`
	Onset            time.Time     `json:"onset"`
	End              time.Time     `json:"end"`
	OnsetUncertainty time.Duration `json:"onset_uncertainty_ns"`
	// Scope is load or time.
	Scope    string        `json:"scope"`
	Effect   float64       `json:"effect"`
	Unit     string        `json:"unit"`
	Baseline BaselineRef   `json:"baseline"`
	Evidence []EvidenceRef `json:"evidence"`
}

// BaselineRef names what an anomaly was measured against.
type BaselineRef struct {
	TestID        string `json:"test_id,omitempty"`
	Qualification string `json:"qualification,omitempty"`
}

// Hypothesis is one ranked explanation (Phase 3b).
type Hypothesis struct {
	ID                string        `json:"id"`
	Signature         string        `json:"signature"`
	Rank              int           `json:"rank"`
	Confidence        string        `json:"confidence"`
	ConfidenceReasons []string      `json:"confidence_reasons"`
	Chain             []ChainStep   `json:"chain"`
	Supporting        []EvidenceRef `json:"supporting"`
	Contradicting     []EvidenceRef `json:"contradicting"`
	Competing         []string      `json:"competing"`
	Falsification     string        `json:"falsification"`
	Limitations       []string      `json:"limitations"`
}

// ChainStep is one link in a hypothesis's causal chain.
type ChainStep struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
}

// EvidenceRef points at the evidence a claim rests on. Kind is archive_row,
// interval, monitor_series, provider_diagnostic, exemplar or phase_event.
type EvidenceRef struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
	Digest  string `json:"digest,omitempty"`
}

// Coverage says how much of one kind of evidence the assessment had.
type Coverage struct {
	Evidence string `json:"evidence"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

// Limitation is something the assessment could not do, and why.
type Limitation struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// AnalysisInput is the frozen evidence one assessment reads.
type AnalysisInput struct {
	Acceptance Acceptance
	Validity   ValidityInput
	Coverage   []Coverage
}

// Analyze builds an assessment within budget. Phase 3a judges acceptance and
// validity; anomalies and hypotheses need onset detection (Phase 3b), so the
// outcome is insufficient_evidence.
func Analyze(input AnalysisInput, budgeter *Budgeter) Assessment {
	if budgeter == nil {
		budgeter = NewBudgeter(DefaultBudget, nil, nil)
	}
	out := Assessment{
		SchemaVersion: AssessmentSchemaVersion, Acceptance: input.Acceptance,
		Anomalies: []Anomaly{}, Hypotheses: []Hypothesis{}, Outcome: OutcomeInsufficientEvidence,
		Coverage: append([]Coverage{}, input.Coverage...), Limitations: []Limitation{}, Budget: budgeter.budget,
	}
	if out.Acceptance.Status == "" {
		out.Acceptance.Status = AcceptanceUnknown
	}
	if out.Acceptance.Details == nil {
		out.Acceptance.Details = []string{}
	}
	if budgeter.Exhausted() {
		out.Validity = Validity{RulesVersion: ValidityRulesVersion, Rules: DefaultValidityRules, Verdict: ValidityUnknown, Reasons: []string{}, Windows: []ValidityWindow{}, Anomalies: []ValidityAnomaly{}, Limitations: []string{LimitationBudgetTruncated}}
	} else {
		out.Validity = AssessValidity(input.Validity, DefaultValidityRules)
	}
	out.Limitations = append(out.Limitations, Limitation{Code: LimitationDetectionUnavailable, Detail: "onset detection and hypotheses are not available yet"})
	// Only limits that cut evidence or skipped a stage are reported; a stage
	// that finished over time still used everything it was given.
	for _, t := range budgeter.Truncations() {
		out.Limitations = append(out.Limitations, Limitation{Code: LimitationBudgetTruncated, Detail: t.Detail()})
	}
	sort.SliceStable(out.Coverage, func(i, j int) bool { return out.Coverage[i].Evidence < out.Coverage[j].Evidence })
	return out
}
