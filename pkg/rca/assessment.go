package rca

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/rca/signatures"
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

// LimitationStageSkipped: the analysis budget ran out before a stage began.
const LimitationStageSkipped = "stage_skipped"

// EngineVersionBase names the engine's generation; EngineVersion appends the
// versions and a fingerprint of every value that shapes an assessment.
const EngineVersionBase = "apexload.rca.engine.v4"

// EngineVersion identifies everything that decides an assessment from frozen
// inputs: the rules' and parameters' versions, a fingerprint of every value
// the stages use (budget, detection parameters, validity rules, hypothesis,
// graph and capacity bounds) and the signature set's. Changing a value
// changes it, so identical requests are not answered with an analysis
// computed under different behaviour; a change to a stage's logic bumps
// EngineVersionBase.
func EngineVersion() string {
	body, _ := json.Marshal(struct {
		Budget     Budget
		Detect     DetectParams
		Validity   ValidityRules
		Hypotheses []any
		Capacity   []any
		Graph      []any
		Browser    BrowserBaselineParams
	}{
		DefaultBudget, DefaultDetectParams, DefaultValidityRules,
		[]any{LeadMargin, MaxGraphHops, MaxHypotheses},
		[]any{KneeSlopeRatio, MinKneeLevels, LittlesFlatFraction, LittlesMinPoints, MaxCapacityResults},
		[]any{MaxGraphBytes, MaxValidityWindows},
		DefaultBrowserBaselineParams,
	})
	sum := sha256.Sum256(body)
	return EngineVersionBase + "+" + ValidityRulesVersion + "+" + DetectParamsVersion + "+" + BrowserBaselineVersion + "+" + GraphSchemaVersion + "+" + signatures.Version +
		"+" + hex.EncodeToString(sum[:6]) + "." + signatures.Fingerprint()
}

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

	Detection  Detection    `json:"detection"`
	Graph      Graph        `json:"graph"`
	Capacity   []Capacity   `json:"capacity"`
	LittlesLaw []LittlesLaw `json:"littles_law"`
	// BrowserBaseline compares the run's browser document cohorts with the
	// baseline run's (browser_baseline.v1); absent without document series.
	BrowserBaseline *BrowserBaseline `json:"browser_baseline,omitempty"`
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

// Anomaly is a detected change in one series.
type Anomaly struct {
	ID     string    `json:"id"`
	Series string    `json:"series"`
	Onset  time.Time `json:"onset"`
	// End is where in-band buckets resumed when Recovered, otherwise the end
	// of the level span the anomaly was found in.
	End              time.Time     `json:"end"`
	OnsetUncertainty time.Duration `json:"onset_uncertainty_ns"`
	// Scope is time (against earlier intervals at the same load in this run)
	// or load (against the baseline run at the same level).
	Scope     string `json:"scope"`
	Direction string `json:"direction"`
	Recovered bool   `json:"recovered"`
	// Level labels the load level the anomaly was found at.
	Level string `json:"level"`
	// Effect is the anomaly's median minus the baseline median, in Unit.
	Effect float64 `json:"effect"`
	Unit   string  `json:"unit"`
	// PeakZ is the largest robust z-score in the anomaly, signed.
	PeakZ    float64       `json:"peak_z"`
	Baseline BaselineRef   `json:"baseline"`
	Evidence []EvidenceRef `json:"evidence"`
}

// BaselineRef names what an anomaly was measured against.
type BaselineRef struct {
	TestID        string `json:"test_id,omitempty"`
	Qualification string `json:"qualification,omitempty"`
	// Reasons say why a partial baseline is partial, when known.
	Reasons []string `json:"reasons,omitempty"`
}

// Hypothesis is one ranked explanation of one or more symptoms.
type Hypothesis struct {
	ID        string `json:"id"`
	Signature string `json:"signature"`
	// Symptoms are the anomalies the hypothesis explains.
	Symptoms          []string      `json:"symptoms"`
	Rank              int           `json:"rank"`
	Score             float64       `json:"score"`
	Confidence        string        `json:"confidence"`
	ConfidenceReasons []string      `json:"confidence_reasons"`
	Chain             []ChainStep   `json:"chain"`
	Supporting        []EvidenceRef `json:"supporting"`
	Contradicting     []EvidenceRef `json:"contradicting"`
	Competing         []string      `json:"competing"`
	Falsification     string        `json:"falsification"`
	Limitations       []string      `json:"limitations"`
	// Nodes are the graph nodes the supporting evidence measured.
	Nodes []string `json:"nodes"`
}

// ChainStep is one link in a hypothesis's causal chain.
type ChainStep struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
}

// EvidenceRef points at the evidence a claim rests on. Kind is archive_row,
// interval, monitor_series, imported_series, browser_document,
// provider_diagnostic, exemplar or phase_event.
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
	// Sources name the monitor sources behind a monitor_sources row,
	// incomplete ones first, so a reader can open the source that left a gap.
	Sources []CoverageSource `json:"sources,omitempty"`
}

// CoverageSource is one monitor source's outcome for the run: its ID (the
// project's monitor source), pack, job state, why its coverage is incomplete
// (empty when complete) and whether the run required it.
type CoverageSource struct {
	ID       string `json:"id"`
	Pack     string `json:"pack,omitempty"`
	State    string `json:"state,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Required bool   `json:"required,omitempty"`
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
	Detection  DetectionInput
	Graph      GraphInput
}

// Analyze builds an assessment within budget: acceptance, validity, the
// anomalies onset detection finds, the run's typed graph, the hypotheses
// that explain the anomalies, and capacity: latency knees and Little's law
// at broker hops. Each stage starts only within the wall-time and heap
// budget; a skipped stage is listed.
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
	if budgeter.Exhausted() {
		out.Detection = Detection{Status: DetectionSkipped, ParamsVersion: DetectParamsVersion, Params: DefaultDetectParams, Levels: []LoadLevel{}, Spikes: []Spike{}}
		out.Limitations = append(out.Limitations, input.Detection.Limitations...)
		out.Limitations = append(out.Limitations, Limitation{Code: LimitationStageSkipped, Detail: "detection: the analysis budget ran out first"})
	} else {
		var limitations []Limitation
		out.Anomalies, out.Detection, limitations = Detect(input.Detection, DefaultDetectParams, budgeter)
		out.Limitations = append(out.Limitations, limitations...)
		// Browser document cohorts against the baseline run's: advisory,
		// apart from onsets and hypotheses.
		out.BrowserBaseline = DetectBrowserBaseline(input.Detection, BrowserBaselineParamsFor(input.Detection.BrowserPolicy))
	}
	if budgeter.Exhausted() {
		out.Graph = Graph{Schema: GraphSchemaVersion, Nodes: []GraphNode{}, Edges: []GraphEdge{}, Cycles: [][]string{}}
		out.Limitations = append(out.Limitations, input.Graph.Limitations...)
		out.Limitations = append(out.Limitations, Limitation{Code: LimitationStageSkipped, Detail: "graph: the analysis budget ran out first"})
	} else {
		var limitations []Limitation
		out.Graph, limitations = BuildGraph(input.Graph, budgeter)
		out.Limitations = append(out.Limitations, limitations...)
	}
	switch {
	case out.Detection.Status == DetectionUnavailable || out.Detection.Status == DetectionSkipped:
		// Without detection there is no saying whether anything changed.
		out.Outcome = OutcomeInsufficientEvidence
	case budgeter.Exhausted():
		out.Outcome = OutcomeInsufficientEvidence
		out.Limitations = append(out.Limitations, Limitation{Code: LimitationStageSkipped, Detail: "hypotheses: the analysis budget ran out first"})
	default:
		var limitations []Limitation
		out.Hypotheses, out.Outcome, limitations = Hypothesize(out.Anomalies, input.Detection.Series, out.Detection.Judged, out.Graph, out.Detection.Levels, DefaultDetectParams)
		out.Limitations = append(out.Limitations, limitations...)
		if limitation, capped := capConfidenceByValidity(out.Hypotheses, out.Validity); capped {
			out.Limitations = append(out.Limitations, limitation)
		}
	}
	out.Capacity, out.LittlesLaw = []Capacity{}, []LittlesLaw{}
	if budgeter.Exhausted() {
		out.Limitations = append(out.Limitations, Limitation{Code: LimitationStageSkipped, Detail: "capacity: the analysis budget ran out first"})
	} else {
		out.Capacity = Capacities(input.Detection.Series, out.Detection.Levels, DefaultDetectParams)
		out.LittlesLaw = LittlesLawChecks(input.Detection.Series, out.Detection.Levels)
	}
	// Only limits that cut evidence or skipped a stage are reported; a stage
	// that finished over time still used everything it was given.
	for _, t := range budgeter.Truncations() {
		out.Limitations = append(out.Limitations, Limitation{Code: LimitationBudgetTruncated, Detail: t.Detail()})
	}
	sort.SliceStable(out.Coverage, func(i, j int) bool { return out.Coverage[i].Evidence < out.Coverage[j].Evidence })
	return out
}
