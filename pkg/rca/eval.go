package rca

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/rca/signatures"
)

// Evaluation contracts.
const (
	EvalManifestSchema = "apexload.rca_eval_manifest.v1"
	EvalScoresSchema   = "apexload.rca_eval_scores.v1"
)

// Evaluation splits. A trial's split is fixed by its seed before any
// tuning: 30% calibrate, 70% are held out.
const (
	SplitCalibration = "calibration"
	SplitHeldOut     = "held_out"
	SplitAll         = "all"
	// CalibrationPercent of trials calibrate.
	CalibrationPercent = 30
)

// HealthyClass labels a control run with no injected fault.
const HealthyClass = "healthy"

// Special cases the corpus must include.
const (
	CaseMisleadingCorrelation = "misleading_correlation"
	CaseMissingEvidence       = "missing_evidence"
	CaseGeneratorBottleneck   = "generator_bottleneck"
)

var evalCases = map[string]bool{CaseMisleadingCorrelation: true, CaseMissingEvidence: true, CaseGeneratorBottleneck: true}

// Corpus minimums and release bars from the plan.
const (
	MinFaultClasses     = 8
	MinTrialsPerClass   = 20
	MinHealthyControls  = 100
	BarTop1             = 0.80
	BarTop3             = 0.90
	BarFalseAttribution = 0.05
	// OnsetTolerance is how far a recovered cause's onset may be from the
	// injected fault's start.
	OnsetTolerance = time.Minute
)

// EvalTrial is one labelled run of the corpus.
type EvalTrial struct {
	ID string `json:"id"`
	// Seed fixes the trial's split.
	Seed  string `json:"seed"`
	Class string `json:"class"`
	// Case marks the special cases: misleading_correlation, missing_evidence
	// or generator_bottleneck.
	Case         string     `json:"case,omitempty"`
	RunID        string     `json:"run_id"`
	TargetEntity string     `json:"target_entity,omitempty"`
	FaultStart   *time.Time `json:"fault_start,omitempty"`
	FaultEnd     *time.Time `json:"fault_end,omitempty"`
	// ExpectedSignature is the signature that should rank first; healthy
	// controls have none. A trial whose right answer is not a signature
	// expects an outcome (a missing-evidence trial: insufficient_evidence)
	// or a validity verdict (a generator bottleneck: suspect or invalid)
	// instead.
	ExpectedSignature string `json:"expected_signature,omitempty"`
	ExpectedOutcome   string `json:"expected_outcome,omitempty"`
	ExpectedValidity  string `json:"expected_validity,omitempty"`
	// BaselineRunID is the run a replay compares the trial's levels with.
	BaselineRunID string `json:"baseline_run_id,omitempty"`
	// Assessment is the trial's assessment file, relative to the corpus.
	Assessment string `json:"assessment"`
	// Split, when recorded, must be the one the seed fixes.
	Split string `json:"split,omitempty"`
}

// EvalManifest lists a corpus's trials.
type EvalManifest struct {
	Schema string      `json:"schema"`
	Trials []EvalTrial `json:"trials"`
}

// EvalSplit is the split a seed fixes: the first eight bytes of its SHA-256,
// modulo 100, below CalibrationPercent calibrate.
func EvalSplit(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	if binary.BigEndian.Uint64(sum[:8])%100 < CalibrationPercent {
		return SplitCalibration
	}
	return SplitHeldOut
}

// Validate checks a manifest: unique trial IDs and seeds, a recorded split
// that matches its seed, and labels a fault trial needs.
func (m EvalManifest) Validate() error {
	if m.Schema != EvalManifestSchema {
		return fmt.Errorf("manifest schema must be %s", EvalManifestSchema)
	}
	known := map[string]bool{}
	for _, s := range signatures.All() {
		known[s.Name] = true
	}
	ids, seeds := map[string]bool{}, map[string]bool{}
	for _, t := range m.Trials {
		switch {
		case t.ID == "" || ids[t.ID]:
			return fmt.Errorf("trial %q: missing or duplicate ID", t.ID)
		case t.Seed == "" || seeds[t.Seed]:
			return fmt.Errorf("trial %s: missing or duplicate seed", t.ID)
		case t.Split != "" && t.Split != EvalSplit(t.Seed):
			return fmt.Errorf("trial %s: recorded split %s differs from its seed's %s", t.ID, t.Split, EvalSplit(t.Seed))
		case t.Class == "" || t.Assessment == "":
			return fmt.Errorf("trial %s: class and assessment are required", t.ID)
		case t.Class == HealthyClass && t.ExpectedSignature != "":
			return fmt.Errorf("trial %s: a healthy control expects no signature", t.ID)
		case t.Case != "" && !evalCases[t.Case]:
			return fmt.Errorf("trial %s: unknown case %q", t.ID, t.Case)
		case t.ExpectedSignature != "" && !known[t.ExpectedSignature]:
			return fmt.Errorf("trial %s: unknown expected signature %q", t.ID, t.ExpectedSignature)
		case t.ExpectedOutcome != "" && t.ExpectedOutcome != OutcomeSupported && t.ExpectedOutcome != OutcomeInsufficientEvidence && t.ExpectedOutcome != OutcomeNoSupportedHypothesis:
			return fmt.Errorf("trial %s: unknown expected outcome %q", t.ID, t.ExpectedOutcome)
		case t.ExpectedValidity != "" && t.ExpectedValidity != ValidityValid && t.ExpectedValidity != ValidityUnknown && t.ExpectedValidity != ValiditySuspect && t.ExpectedValidity != ValidityInvalid:
			return fmt.Errorf("trial %s: unknown expected validity %q", t.ID, t.ExpectedValidity)
		case t.Class != HealthyClass && (t.FaultStart == nil || t.ExpectedSignature == "" && t.ExpectedOutcome == "" && t.ExpectedValidity == ""):
			return fmt.Errorf("trial %s: a fault trial needs a fault start and an expected signature, outcome or validity", t.ID)
		}
		ids[t.ID], seeds[t.Seed] = true, true
	}
	return nil
}

// TrialsIn returns the trials of a split, in ID order. Calibration tooling
// asks for SplitCalibration only, so held-out labels never reach it.
func (m EvalManifest) TrialsIn(split string) []EvalTrial {
	var out []EvalTrial
	for _, t := range m.Trials {
		if split == SplitAll || EvalSplit(t.Seed) == split {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Rate is a proportion with its Wilson 95% interval.
type Rate struct {
	Successes int     `json:"successes"`
	Total     int     `json:"total"`
	Value     float64 `json:"value"`
	Low       float64 `json:"low"`
	High      float64 `json:"high"`
}

// NewRate computes a proportion's Wilson score interval at 95%.
func NewRate(successes, total int) Rate {
	r := Rate{Successes: successes, Total: total}
	if total == 0 {
		return r
	}
	const z = 1.959963984540054
	n, p := float64(total), float64(successes)/float64(total)
	denominator := 1 + z*z/n
	center := (p + z*z/(2*n)) / denominator
	half := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / denominator
	r.Value, r.Low, r.High = round6(p), round6(math.Max(0, center-half)), round6(math.Min(1, center+half))
	return r
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// CaseScore is how often the trials of one special case got the right
// answer: the expected signature first, outcome or validity verdict.
type CaseScore struct {
	Case    string `json:"case"`
	Correct Rate   `json:"correct"`
}

// ClassScore is one fault class's recall.
type ClassScore struct {
	Class string `json:"class"`
	Top1  Rate   `json:"top1"`
	Top3  Rate   `json:"top3"`
}

// BarResult is one release bar against its measured value.
type BarResult struct {
	Name  string  `json:"name"`
	Bar   string  `json:"bar"`
	Value float64 `json:"value"`
	Met   bool    `json:"met"`
}

// EvalScores is a harness run's result. Identical manifests and
// assessments always give identical scores.
type EvalScores struct {
	Schema          string   `json:"schema"`
	Split           string   `json:"split"`
	Engines         []string `json:"engines"`
	Trials          int      `json:"trials"`
	FaultTrials     int      `json:"fault_trials"`
	HealthyControls int      `json:"healthy_controls"`
	Top1            Rate     `json:"top1"`
	Top3            Rate     `json:"top3"`
	// UnsupportedHighConfidence counts trials with a high-confidence
	// hypothesis that is wrong: any on a healthy control, or a signature
	// other than the expected one on a fault trial.
	UnsupportedHighConfidence Rate `json:"unsupported_high_confidence"`
	// FalseAttribution counts healthy controls with any supported hypothesis.
	FalseAttribution Rate `json:"false_attribution"`
	// Abstention counts fault trials expecting a signature that got no
	// supported hypothesis.
	Abstention Rate `json:"abstention"`
	// OutcomeAgreement and ValidityAgreement count trials whose assessment
	// reached the outcome or validity verdict they expected.
	OutcomeAgreement  Rate `json:"outcome_agreement"`
	ValidityAgreement Rate `json:"validity_agreement"`
	// OnsetWithinTolerance counts top-1 recoveries whose earliest cause
	// began within OnsetTolerance of the fault.
	OnsetWithinTolerance    Rate         `json:"onset_within_tolerance"`
	OnsetErrorMedianSeconds float64      `json:"onset_error_median_s"`
	OnsetErrorP90Seconds    float64      `json:"onset_error_p90_s"`
	PerClass                []ClassScore `json:"per_class"`
	PerCase                 []CaseScore  `json:"per_case"`
	Bars                    []BarResult  `json:"bars"`
	// CorpusReady says whether the whole corpus, not only the scored split,
	// meets the plan's minimums; Gaps say what is short.
	CorpusReady bool     `json:"corpus_ready"`
	Gaps        []string `json:"gaps"`
}

// ScoreEvaluation scores a split's assessments against their labels.
func ScoreEvaluation(manifest EvalManifest, assessments map[string]Assessment, split string) (EvalScores, error) {
	if split != SplitCalibration && split != SplitHeldOut && split != SplitAll {
		return EvalScores{}, errors.New("split must be calibration, held_out or all")
	}
	if err := manifest.Validate(); err != nil {
		return EvalScores{}, err
	}
	trials := manifest.TrialsIn(split)
	scores := EvalScores{Schema: EvalScoresSchema, Split: split, Trials: len(trials), Engines: []string{}, PerClass: []ClassScore{}, PerCase: []CaseScore{}, Gaps: corpusGaps(manifest)}
	type tally struct{ top1, top3, total int }
	classes := map[string]*tally{}
	cases := map[string]*tally{}
	var signatureTrials, top1, top3, wrongHigh, attributed, abstained, onsetOK, recovered int
	var outcomeTrials, outcomeAgreed, validityTrials, validityAgreed int
	var onsetErrors []float64
	engines := map[string]bool{}
	for _, t := range trials {
		a, ok := assessments[t.ID]
		if !ok {
			return EvalScores{}, fmt.Errorf("trial %s has no assessment", t.ID)
		}
		engines[a.Inputs.Engine] = true
		ranked := slices.Clone(a.Hypotheses)
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Rank < ranked[j].Rank })
		highWrong := false
		for _, h := range ranked {
			if h.Confidence == ConfidenceHigh && (t.Class == HealthyClass || !recovers(t, h)) {
				highWrong = true
			}
		}
		if highWrong {
			wrongHigh++
		}
		correct := true
		if t.ExpectedOutcome != "" {
			outcomeTrials++
			if a.Outcome == t.ExpectedOutcome {
				outcomeAgreed++
			} else {
				correct = false
			}
		}
		if t.ExpectedValidity != "" {
			validityTrials++
			if a.Validity.Verdict == t.ExpectedValidity {
				validityAgreed++
			} else {
				correct = false
			}
		}
		if t.Class == HealthyClass {
			scores.HealthyControls++
			if a.Outcome == OutcomeSupported {
				attributed++
			}
			continue
		}
		scores.FaultTrials++
		if t.ExpectedSignature != "" {
			signatureTrials++
			c := classes[t.Class]
			if c == nil {
				c = &tally{}
				classes[t.Class] = c
			}
			c.total++
			if a.Outcome != OutcomeSupported {
				abstained++
			}
			first := false
			for i, h := range ranked {
				if i >= 3 {
					break
				}
				if !recovers(t, h) {
					continue
				}
				top3++
				c.top3++
				if i == 0 {
					first = true
					top1++
					c.top1++
					recovered++
					if onset, ok := earliestCause(a, h); ok {
						err := math.Abs(onset.Sub(*t.FaultStart).Seconds())
						onsetErrors = append(onsetErrors, err)
						if err <= OnsetTolerance.Seconds() {
							onsetOK++
						}
					}
				}
				break
			}
			correct = correct && first
		}
		if t.Case != "" {
			c := cases[t.Case]
			if c == nil {
				c = &tally{}
				cases[t.Case] = c
			}
			c.total++
			if correct {
				c.top1++
			}
		}
	}
	scores.Top1, scores.Top3 = NewRate(top1, signatureTrials), NewRate(top3, signatureTrials)
	scores.UnsupportedHighConfidence = NewRate(wrongHigh, scores.Trials)
	scores.FalseAttribution = NewRate(attributed, scores.HealthyControls)
	scores.Abstention = NewRate(abstained, signatureTrials)
	scores.OutcomeAgreement, scores.ValidityAgreement = NewRate(outcomeAgreed, outcomeTrials), NewRate(validityAgreed, validityTrials)
	scores.OnsetWithinTolerance = NewRate(onsetOK, recovered)
	scores.OnsetErrorMedianSeconds, scores.OnsetErrorP90Seconds = quantile(onsetErrors, 0.5), quantile(onsetErrors, 0.9)
	for _, name := range sortedKeys(classes) {
		c := classes[name]
		scores.PerClass = append(scores.PerClass, ClassScore{Class: name, Top1: NewRate(c.top1, c.total), Top3: NewRate(c.top3, c.total)})
	}
	for _, name := range sortedKeys(cases) {
		scores.PerCase = append(scores.PerCase, CaseScore{Case: name, Correct: NewRate(cases[name].top1, cases[name].total)})
	}
	scores.Engines = sortedKeys(engines)
	scores.Bars = []BarResult{
		{Name: "top1_recovery", Bar: ">= 0.80", Value: scores.Top1.Value, Met: signatureTrials > 0 && scores.Top1.Value >= BarTop1},
		{Name: "top3_recovery", Bar: ">= 0.90", Value: scores.Top3.Value, Met: signatureTrials > 0 && scores.Top3.Value >= BarTop3},
		{Name: "unsupported_high_confidence", Bar: "0", Value: float64(wrongHigh), Met: wrongHigh == 0},
		{Name: "false_attribution", Bar: "<= 0.05", Value: scores.FalseAttribution.Value, Met: scores.HealthyControls > 0 && scores.FalseAttribution.Value <= BarFalseAttribution},
	}
	if len(scores.Engines) > 1 {
		scores.Gaps = append(scores.Gaps, "assessments came from more than one engine version")
	}
	scores.CorpusReady = len(scores.Gaps) == 0
	return scores, nil
}

// corpusGaps compares the whole corpus, both splits, with the plan's
// minimums.
func corpusGaps(manifest EvalManifest) []string {
	gaps := []string{}
	perClass := map[string]int{}
	controls := 0
	cases := map[string]bool{}
	for _, t := range manifest.Trials {
		switch {
		case t.Class == HealthyClass:
			controls++
		case t.ExpectedSignature != "":
			perClass[t.Class]++
		}
		if t.Case != "" {
			cases[t.Case] = true
		}
	}
	for _, name := range sortedKeys(perClass) {
		if perClass[name] < MinTrialsPerClass {
			gaps = append(gaps, fmt.Sprintf("class %s has %d trials, fewer than %d", name, perClass[name], MinTrialsPerClass))
		}
	}
	if len(perClass) < MinFaultClasses {
		gaps = append(gaps, fmt.Sprintf("%d fault classes, fewer than %d", len(perClass), MinFaultClasses))
	}
	if controls < MinHealthyControls {
		gaps = append(gaps, fmt.Sprintf("%d healthy controls, fewer than %d", controls, MinHealthyControls))
	}
	for _, name := range sortedKeys(evalCases) {
		if !cases[name] {
			gaps = append(gaps, "no "+name+" trials")
		}
	}
	return gaps
}

// recovers reports whether a hypothesis names the trial's fault: the
// expected signature, about the target entity when the trial names one. A
// target matches a node ID exactly or the entity ID that ends it.
func recovers(t EvalTrial, h Hypothesis) bool {
	if t.ExpectedSignature == "" || h.Signature != t.ExpectedSignature {
		return false
	}
	if t.TargetEntity == "" {
		return true
	}
	for _, node := range h.Nodes {
		if node == t.TargetEntity || strings.HasSuffix(node, ":"+t.TargetEntity) {
			return true
		}
	}
	return false
}

// earliestCause is the earliest onset among the anomalies a hypothesis's
// chain starts from.
func earliestCause(a Assessment, h Hypothesis) (time.Time, bool) {
	onsets := make(map[string]time.Time, len(a.Anomalies))
	for _, anomaly := range a.Anomalies {
		onsets[anomaly.ID] = anomaly.Onset
	}
	var earliest time.Time
	for _, step := range h.Chain {
		if onset, ok := onsets[step.From]; ok && (earliest.IsZero() || onset.Before(earliest)) {
			earliest = onset
		}
	}
	return earliest, !earliest.IsZero()
}

// quantile is the nearest-rank quantile, or 0 without values.
func quantile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	sort.Float64s(sorted)
	index := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(0, min(index, len(sorted)-1))]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
