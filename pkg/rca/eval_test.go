package rca

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestEvalSplitIsStableAndNearThirtySeventy(t *testing.T) {
	calibration := 0
	for i := 0; i < 2000; i++ {
		seed := fmt.Sprintf("trial-%04d", i)
		if EvalSplit(seed) != EvalSplit(seed) {
			t.Fatal("split is not a function of the seed")
		}
		if EvalSplit(seed) == SplitCalibration {
			calibration++
		}
	}
	if share := float64(calibration) / 2000; share < 0.27 || share > 0.33 {
		t.Fatalf("calibration share %.3f", share)
	}
}

func TestWilsonInterval(t *testing.T) {
	// 8 of 10: the Wilson 95% interval is about 0.490 to 0.943.
	r := NewRate(8, 10)
	if r.Value != 0.8 || math.Abs(r.Low-0.490) > 0.001 || math.Abs(r.High-0.943) > 0.001 {
		t.Fatalf("%+v", r)
	}
	if r := NewRate(0, 0); r.Value != 0 || r.Low != 0 || r.High != 0 {
		t.Fatalf("%+v", r)
	}
	if r := NewRate(5, 5); r.High != 1 || r.Low < 0.56 {
		t.Fatalf("%+v", r)
	}
}

// evalAssessment is an assessment whose ranked hypotheses name the given
// signatures, the first one's cause beginning at onset.
func evalAssessment(onset time.Time, confidence string, names ...string) Assessment {
	a := Assessment{SchemaVersion: AssessmentSchemaVersion, Outcome: OutcomeNoSupportedHypothesis, Inputs: InputDigests{Engine: "engine-1"},
		Anomalies: []Anomaly{{ID: "cause", Onset: onset}}}
	for i, name := range names {
		a.Outcome = OutcomeSupported
		a.Hypotheses = append(a.Hypotheses, Hypothesis{ID: name, Signature: name, Rank: i + 1, Confidence: confidence, Chain: []ChainStep{{From: "cause", To: "symptom"}}})
	}
	return a
}

func evalCorpus() (EvalManifest, map[string]Assessment) {
	start := detectStart
	manifest := EvalManifest{Schema: EvalManifestSchema}
	assessments := map[string]Assessment{}
	add := func(id, class, expected string, a Assessment) {
		trial := EvalTrial{ID: id, Seed: "seed-" + id, Class: class, RunID: "run-" + id, ExpectedSignature: expected, Assessment: id + ".json"}
		if class != HealthyClass {
			trial.FaultStart = &start
		}
		manifest.Trials = append(manifest.Trials, trial)
		assessments[id] = a
	}
	// CPU faults: recovered first, recovered third, missed.
	add("cpu-1", "cpu_stress", "resource_pressure", evalAssessment(start.Add(20*time.Second), ConfidenceHigh, "resource_pressure"))
	add("cpu-2", "cpu_stress", "resource_pressure", evalAssessment(start.Add(5*time.Minute), ConfidenceMedium, "garbage_collection", "retries", "resource_pressure"))
	add("cpu-3", "cpu_stress", "resource_pressure", evalAssessment(start, ConfidenceHigh, "garbage_collection"))
	// DNS fault, recovered but its cause began late.
	add("dns-1", "dns_delay", "network_phase", evalAssessment(start.Add(3*time.Minute), ConfidenceMedium, "network_phase"))
	// Controls: one quiet, one falsely attributed.
	add("ok-1", HealthyClass, "", evalAssessment(start, ""))
	add("ok-2", HealthyClass, "", evalAssessment(start, ConfidenceLow, "retries"))
	return manifest, assessments
}

func TestScoreEvaluation(t *testing.T) {
	manifest, assessments := evalCorpus()
	scores, err := ScoreEvaluation(manifest, assessments, SplitAll)
	if err != nil {
		t.Fatal(err)
	}
	if scores.FaultTrials != 4 || scores.HealthyControls != 2 || scores.Top1.Successes != 2 || scores.Top3.Successes != 3 ||
		scores.FalseAttribution.Successes != 1 || scores.Abstention.Successes != 0 || scores.UnsupportedHighConfidence.Successes != 1 {
		t.Fatalf("%+v", scores)
	}
	// Of the two top-1 recoveries, one cause began within a minute.
	if scores.OnsetWithinTolerance.Successes != 1 || scores.OnsetWithinTolerance.Total != 2 || scores.OnsetErrorMedianSeconds != 20 || scores.OnsetErrorP90Seconds != 180 {
		t.Fatalf("onset %+v %.0f %.0f", scores.OnsetWithinTolerance, scores.OnsetErrorMedianSeconds, scores.OnsetErrorP90Seconds)
	}
	if len(scores.PerClass) != 2 || scores.PerClass[0].Class != "cpu_stress" || scores.PerClass[0].Top1.Successes != 1 || scores.PerClass[0].Top3.Successes != 2 {
		t.Fatalf("%+v", scores.PerClass)
	}
	// A small corpus is not ready, and these scores miss the bars.
	if scores.CorpusReady || len(scores.Gaps) < 3 {
		t.Fatalf("gaps %+v", scores.Gaps)
	}
	for _, bar := range scores.Bars {
		if bar.Met {
			t.Fatalf("bar %+v met", bar)
		}
	}
	// Two runs give identical scores.
	again, _ := ScoreEvaluation(manifest, assessments, SplitAll)
	first, _ := json.Marshal(scores)
	second, _ := json.Marshal(again)
	if string(first) != string(second) {
		t.Fatal("scores differ between runs")
	}
}

func TestEvalManifestValidation(t *testing.T) {
	manifest, _ := evalCorpus()
	for name, mutate := range map[string]func(*EvalManifest){
		"duplicate id":        func(m *EvalManifest) { m.Trials[1].ID = m.Trials[0].ID },
		"duplicate seed":      func(m *EvalManifest) { m.Trials[1].Seed = m.Trials[0].Seed },
		"wrong split":         func(m *EvalManifest) { m.Trials[0].Split = otherSplit(EvalSplit(m.Trials[0].Seed)) },
		"unknown signature":   func(m *EvalManifest) { m.Trials[0].ExpectedSignature = "astrology" },
		"no fault start":      func(m *EvalManifest) { m.Trials[0].FaultStart = nil },
		"control expectation": func(m *EvalManifest) { m.Trials[4].ExpectedSignature = "retries" },
		"unknown case":        func(m *EvalManifest) { m.Trials[0].Case = "misleading_corelation" },
		"nothing expected":    func(m *EvalManifest) { m.Trials[0].ExpectedSignature = "" },
		"unknown outcome":     func(m *EvalManifest) { m.Trials[0].ExpectedOutcome = "maybe" },
		"unknown validity":    func(m *EvalManifest) { m.Trials[0].ExpectedValidity = "fine" },
		"schema":              func(m *EvalManifest) { m.Schema = "other" },
	} {
		copy := manifest
		copy.Trials = append([]EvalTrial(nil), manifest.Trials...)
		mutate(&copy)
		if err := copy.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Scoring one split never reads the other's assessments.
	_, assessments := evalCorpus()
	for _, trial := range manifest.TrialsIn(SplitHeldOut) {
		delete(assessments, trial.ID)
	}
	if _, err := ScoreEvaluation(manifest, assessments, SplitCalibration); err != nil {
		t.Fatalf("calibration scoring needed a held-out assessment: %v", err)
	}
}

func otherSplit(split string) string {
	if split == SplitCalibration {
		return SplitHeldOut
	}
	return SplitCalibration
}

// readyCorpus meets every minimum: 8 classes of 20 recovered trials, 100
// quiet controls and one trial of each special case.
func readyCorpus() (EvalManifest, map[string]Assessment) {
	start := detectStart
	manifest := EvalManifest{Schema: EvalManifestSchema}
	assessments := map[string]Assessment{}
	add := func(t EvalTrial, a Assessment) {
		t.Seed, t.RunID, t.Assessment = "seed-"+t.ID, "run-"+t.ID, t.ID+".json"
		if t.Class != HealthyClass {
			t.FaultStart = &start
		}
		manifest.Trials = append(manifest.Trials, t)
		assessments[t.ID] = a
	}
	for c := 0; c < MinFaultClasses; c++ {
		for i := 0; i < MinTrialsPerClass; i++ {
			add(EvalTrial{ID: fmt.Sprintf("c%d-%02d", c, i), Class: fmt.Sprintf("class-%d", c), ExpectedSignature: "retries"}, evalAssessment(start, ConfidenceMedium, "retries"))
		}
	}
	for i := 0; i < MinHealthyControls; i++ {
		add(EvalTrial{ID: fmt.Sprintf("ok-%03d", i), Class: HealthyClass}, evalAssessment(start, ""))
	}
	missing := evalAssessment(start, "")
	missing.Outcome = OutcomeInsufficientEvidence
	add(EvalTrial{ID: "missing", Class: "dns_delay", Case: CaseMissingEvidence, ExpectedOutcome: OutcomeInsufficientEvidence}, missing)
	bottleneck := evalAssessment(start, "")
	bottleneck.Validity.Verdict = ValiditySuspect
	add(EvalTrial{ID: "bottleneck", Class: "generator_cpu", Case: CaseGeneratorBottleneck, ExpectedValidity: ValidityInvalid}, bottleneck)
	add(EvalTrial{ID: "misleading", Class: "class-0", Case: CaseMisleadingCorrelation, ExpectedSignature: "retries"}, evalAssessment(start, ConfidenceMedium, "garbage_collection", "retries"))
	return manifest, assessments
}

func TestCorpusReadinessCountsTheWholeCorpus(t *testing.T) {
	manifest, assessments := readyCorpus()
	scores, err := ScoreEvaluation(manifest, assessments, SplitHeldOut)
	if err != nil {
		t.Fatal(err)
	}
	if !scores.CorpusReady || len(scores.Gaps) != 0 || scores.Trials >= len(manifest.Trials) {
		t.Fatalf("ready %v gaps %v, %d of %d trials", scores.CorpusReady, scores.Gaps, scores.Trials, len(manifest.Trials))
	}
	all, _ := ScoreEvaluation(manifest, assessments, SplitAll)
	cases := map[string]Rate{}
	for _, c := range all.PerCase {
		cases[c.Case] = c.Correct
	}
	// The missing-evidence trial abstained as it should; the bottleneck was
	// judged suspect, not invalid; the misleading trial ranked its cause
	// second.
	if cases[CaseMissingEvidence].Successes != 1 || cases[CaseGeneratorBottleneck].Successes != 0 || cases[CaseMisleadingCorrelation].Successes != 0 ||
		all.OutcomeAgreement.Successes != 1 || all.ValidityAgreement.Total != 1 || all.ValidityAgreement.Successes != 0 {
		t.Fatalf("%+v %+v %+v", all.PerCase, all.OutcomeAgreement, all.ValidityAgreement)
	}
}

func TestRecoveryNeedsTheTargetEntity(t *testing.T) {
	trial := EvalTrial{ExpectedSignature: "resource_pressure", TargetEntity: "SERVICE-ORDERS"}
	on := Hypothesis{Signature: "resource_pressure", Nodes: []string{EntityNodeID("dynatrace", "SERVICE-ORDERS")}}
	off := Hypothesis{Signature: "resource_pressure", Nodes: []string{EntityNodeID("dynatrace", "SERVICE-CART")}}
	full := EvalTrial{ExpectedSignature: "resource_pressure", TargetEntity: EntityNodeID("dynatrace", "SERVICE-ORDERS")}
	if !recovers(trial, on) || recovers(trial, off) || !recovers(full, on) || recovers(EvalTrial{ExpectedSignature: "retries"}, on) {
		t.Fatal("target matching")
	}
}
