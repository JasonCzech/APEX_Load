package rca

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca/signatures"
)

const (
	symptomSeries = "transaction/checkout/p95_ms"
	cpuSeries     = "monitor/dynatrace/dt/cpu/abc"
)

// hypothesisFixture is a checkout latency onset at 10 minutes, a CPU onset
// on the service behind its dependency, and the graph that connects them
// when mapped is true.
func hypothesisFixture(cpuOnset time.Duration, uncertainty time.Duration, mapped bool) ([]Anomaly, []Series, Graph) {
	symptom := Anomaly{ID: "symptom", Series: symptomSeries, Onset: detectStart.Add(10 * time.Minute), End: detectStart.Add(20 * time.Minute), OnsetUncertainty: uncertainty,
		Scope: ScopeTime, Direction: DirectionIncrease, PeakZ: 12, Evidence: []EvidenceRef{{ID: "e-symptom", Kind: "interval"}}}
	cpu := Anomaly{ID: "cpu", Series: cpuSeries, Onset: detectStart.Add(cpuOnset), End: detectStart.Add(20 * time.Minute), OnsetUncertainty: uncertainty,
		Scope: ScopeTime, Direction: DirectionIncrease, PeakZ: 8, Evidence: []EvidenceRef{{ID: "e-cpu", Kind: "monitor_series"}}}
	series := []Series{
		{ID: symptomSeries, Kind: SeriesLatency},
		{ID: cpuSeries, Kind: SeriesProvider, Measures: signatures.KindCPU, Nodes: []string{EntityNodeID("dynatrace", "SERVICE-1")}},
	}
	input := GraphInput{
		Calls:    []GraphCall{{Transaction: "checkout", Action: "GET /cart", DependencyType: "http", Target: "api.shop.local"}},
		Entities: []GraphEntity{{Provider: "dynatrace", ID: "SERVICE-1", Name: "api.shop.local", Type: "SERVICE"}},
	}
	if mapped {
		input.Mappings = []GraphMapping{{ID: "m", Revision: 1, Dependency: "http:api.shop.local", Provider: "dynatrace", EntityID: "SERVICE-1", Decision: MappingApproved}}
	}
	graph, _ := BuildGraph(input, nil)
	return []Anomaly{symptom, cpu}, series, graph
}

// allJudged marks every series as judged in both scopes.
func allJudged(series []Series) JudgedSeries {
	judged := JudgedSeries{}
	for _, s := range series {
		judged.mark(ScopeTime, s.ID)
		judged.mark(ScopeLoad, s.ID)
	}
	return judged
}

func hypothesisFor(hypotheses []Hypothesis, signature string) *Hypothesis {
	for i := range hypotheses {
		if hypotheses[i].Signature == signature {
			return &hypotheses[i]
		}
	}
	return nil
}

func TestConnectedPrecedingCauseIsSupported(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	hypotheses, outcome, _ := Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	h := hypothesisFor(hypotheses, "resource_pressure")
	if outcome != OutcomeSupported || h == nil || h.Rank != 1 || h.Confidence != ConfidenceHigh || len(h.Symptoms) != 1 || h.Symptoms[0] != "symptom" ||
		len(h.Chain) != 1 || h.Chain[0].Relation != RelationPrecedes || h.Chain[0].From != "cpu" || len(h.Supporting) != 1 || h.Supporting[0].ID != "e-cpu" || h.Falsification == "" {
		t.Fatalf("%s %+v", outcome, hypotheses)
	}
	// The competitors that could not be judged are named.
	if len(h.Limitations) == 0 || !strings.Contains(strings.Join(h.Limitations, " "), "garbage_collection") {
		t.Fatalf("%+v", h.Limitations)
	}
}

func TestUnconnectedEvidenceGivesLowConfidence(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, false)
	hypotheses, outcome, _ := Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	h := hypothesisFor(hypotheses, "resource_pressure")
	if outcome != OutcomeSupported || h == nil || h.Confidence != ConfidenceLow || !strings.Contains(strings.Join(h.ConfidenceReasons, " "), "approve the dependency's mapping") {
		t.Fatalf("%+v", hypotheses)
	}
}

func TestOverlappingUncertaintyStaysUnordered(t *testing.T) {
	// The CPU onset is 20 s earlier, inside both onsets' 15 s uncertainty.
	anomalies, series, graph := hypothesisFixture(10*time.Minute-20*time.Second, 15*time.Second, true)
	hypotheses, _, _ := Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	h := hypothesisFor(hypotheses, "resource_pressure")
	if h == nil || h.Chain[0].Relation != RelationUnordered || h.Confidence != ConfidenceMedium {
		t.Fatalf("%+v", h)
	}
	// Twenty seconds apart with 5 s uncertainties is ordered.
	anomalies, series, graph = hypothesisFixture(10*time.Minute-20*time.Second, 5*time.Second, true)
	hypotheses, _, _ = Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	if h := hypothesisFor(hypotheses, "resource_pressure"); h == nil || h.Chain[0].Relation != RelationPrecedes {
		t.Fatalf("%+v", h)
	}
}

func TestOnsetsAtALoadStepAreMarked(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	// A level settles at 7 m 30 s, so its load step was at 7 m.
	levels := []LoadLevel{{Start: detectStart.Add(7*time.Minute + 30*time.Second), End: detectStart.Add(30 * time.Minute), VUsers: 50}}
	hypotheses, _, _ := Hypothesize(anomalies, series, allJudged(series), graph, levels, DefaultDetectParams)
	if h := hypothesisFor(hypotheses, "resource_pressure"); h == nil || h.Chain[0].Relation != RelationLoadCoincident {
		t.Fatalf("%+v", h)
	}
}

func TestOutcomesWithoutSupport(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	// CPU measured on the path but flat: contradicted, nothing supported.
	hypotheses, outcome, _ := Hypothesize(anomalies[:1], series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	if outcome != OutcomeNoSupportedHypothesis || len(hypotheses) != 0 {
		t.Fatalf("flat: %s %+v", outcome, hypotheses)
	}
	// Nothing measured for any signature.
	hypotheses, outcome, limitations := Hypothesize(anomalies[:1], series[:1], allJudged(series[:1]), graph, oneLevel(200), DefaultDetectParams)
	if outcome != OutcomeInsufficientEvidence || len(hypotheses) != 0 || !hasLimitation(limitations, LimitationSignaturesUnmeasured) {
		t.Fatalf("unmeasured: %s %+v", outcome, limitations)
	}
	// No symptom to explain.
	_, outcome, limitations = Hypothesize(anomalies[1:], series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	if outcome != OutcomeNoSupportedHypothesis || !hasLimitation(limitations, LimitationNoSymptoms) {
		t.Fatalf("no symptoms: %s %+v", outcome, limitations)
	}
	// Evidence that began after the symptom does not support it.
	late, series, graph := hypothesisFixture(12*time.Minute, 10*time.Second, true)
	if hypotheses, _, _ := Hypothesize(late, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams); hypothesisFor(hypotheses, "resource_pressure") != nil {
		t.Fatalf("late cause supported: %+v", hypotheses)
	}
}

func TestKneeFit(t *testing.T) {
	curve := []CapacityLevel{{10, 100}, {20, 102}, {30, 104}, {40, 106}, {50, 250}, {60, 400}}
	knee, before, after, reason := KneeFit(curve, 5)
	if knee == nil || *knee != 40 || before > 1 || after < 10 || reason != "" {
		t.Fatalf("knee %v %.2f %.2f %q", knee, before, after, reason)
	}
	linear := []CapacityLevel{{10, 100}, {20, 110}, {30, 120}, {40, 130}}
	if knee, _, _, reason := KneeFit(linear, 5); knee != nil || reason == "" {
		t.Fatalf("linear knee %v", knee)
	}
	if knee, _, _, reason := KneeFit(curve[:2], 5); knee != nil || !strings.Contains(reason, "fewer than 3") {
		t.Fatalf("two levels: %v %q", knee, reason)
	}
}

func TestCapacitiesFromLevels(t *testing.T) {
	var values []float64
	var levels []LoadLevel
	for i, p95 := range []float64{100, 103, 106, 300} {
		start := detectStart.Add(time.Duration(i*20) * detectBucket)
		levels = append(levels, LoadLevel{Start: start, End: start.Add(20 * detectBucket), VUsers: float64(10 * (i + 1))})
		for j := 0; j < 20; j++ {
			values = append(values, p95)
		}
	}
	c := Capacities([]Series{detectSeries(symptomSeries, values)}, levels, DefaultDetectParams)
	if len(c) != 1 || len(c[0].Levels) != 4 || c[0].Knee == nil || *c[0].Knee != 30 || c[0].LoadUnit != LoadUnitVUsers {
		t.Fatalf("%+v", c)
	}
}

func TestLittlesLaw(t *testing.T) {
	queue := InfrastructureNodeID("queue", "orders")
	series := func(id, measure, unit string, value func(i int) float64) Series {
		s := detectSeries(id, nil)
		s.Kind, s.Measures, s.Unit, s.Nodes = SeriesProvider, measure, unit, []string{queue}
		for i := 0; i < 30; i++ {
			s.Points = append(s.Points, SeriesPoint{At: detectStart.Add(time.Duration(i) * detectBucket), Value: value(i), Valid: true})
		}
		return s
	}
	// 50 msg/s staying 200 ms in the queue keeps 10 messages queued.
	flat := []Series{
		series("depth", signatures.KindQueueDepth, "messages", func(int) float64 { return 10 }),
		series("rate", signatures.KindArrivalRate, "messages/s", func(int) float64 { return 50 }),
		series("residence", signatures.KindResidenceTime, "ms", func(int) float64 { return 200 }),
	}
	checks := LittlesLawChecks(flat, oneLevel(30))
	if len(checks) != 1 || !checks[0].Eligible || checks[0].Ratio < 0.99 || checks[0].Ratio > 1.01 || checks[0].Queue != queue {
		t.Fatalf("%+v", checks)
	}
	// A growing queue is not in steady state.
	growing := append([]Series{series("depth", signatures.KindQueueDepth, "messages", func(i int) float64 { return float64(10 + 5*i) })}, flat[1:]...)
	if checks := LittlesLawChecks(growing, oneLevel(30)); len(checks) != 1 || checks[0].Eligible || !strings.Contains(checks[0].Reason, "steady state") {
		t.Fatalf("%+v", checks)
	}
	// Without a residence time there is nothing to check.
	if checks := LittlesLawChecks(flat[:2], oneLevel(30)); len(checks) != 1 || checks[0].Eligible || !strings.Contains(checks[0].Reason, signatures.KindResidenceTime) {
		t.Fatalf("%+v", checks)
	}
}

func TestOneCauseBehindSeveralSymptomsIsOneHypothesis(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	// A second transaction on the same dependency slows too.
	second := anomalies[0]
	second.ID, second.Series = "symptom-2", "action/GET /cart/p95_ms"
	anomalies = append(anomalies, second)
	series = append(series, Series{ID: second.Series, Kind: SeriesLatency})
	hypotheses, _, _ := Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	var pressure []Hypothesis
	for _, h := range hypotheses {
		if h.Signature == "resource_pressure" {
			pressure = append(pressure, h)
		}
	}
	if len(pressure) != 1 || len(pressure[0].Symptoms) != 2 || len(pressure[0].Chain) != 2 || len(pressure[0].Supporting) != 1 {
		t.Fatalf("%+v", pressure)
	}
}

func TestUnjudgedSeriesAreNotEvidence(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	// Detection never judged the CPU series, so its silence says nothing.
	judged := JudgedSeries{}
	judged.mark(ScopeTime, symptomSeries)
	_, outcome, limitations := Hypothesize(anomalies[:1], series, judged, graph, oneLevel(200), DefaultDetectParams)
	if outcome != OutcomeInsufficientEvidence || !hasLimitation(limitations, LimitationSignaturesUnmeasured) {
		t.Fatalf("%s %+v", outcome, limitations)
	}
}

func TestSeriesNodesResolveThroughAliases(t *testing.T) {
	anomalies, series, _ := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	// The topology knows the service by its Smartscape ID; the CPU points by
	// its classic ID.
	series[1].Nodes = []string{EntityNodeID("dynatrace", "SERVICE-CLASSIC")}
	graph, _ := BuildGraph(GraphInput{
		Calls:    []GraphCall{{Transaction: "checkout", Action: "GET /cart", DependencyType: "http", Target: "api.shop.local"}},
		Entities: []GraphEntity{{Provider: "dynatrace", ID: "smartscape-1", Name: "api.shop.local", Type: "SERVICE", Aliases: []string{"SERVICE-CLASSIC"}}},
		Mappings: []GraphMapping{{ID: "m", Revision: 1, Dependency: "http:api.shop.local", Provider: "dynatrace", EntityID: "SERVICE-CLASSIC", Decision: MappingApproved}},
	}, nil)
	hypotheses, _, _ := Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	if h := hypothesisFor(hypotheses, "resource_pressure"); h == nil || h.Confidence != ConfidenceHigh {
		t.Fatalf("%+v", hypotheses)
	}
}

func TestLittlesLawKeysOnQueuesNotHosts(t *testing.T) {
	queue, pod := InfrastructureNodeID("queue", "orders"), InfrastructureNodeID("pod", "broker-0")
	depth := detectSeries("depth", nil)
	depth.Kind, depth.Measures, depth.Unit, depth.Nodes = SeriesProvider, signatures.KindQueueDepth, "messages", []string{pod, queue}
	if checks := LittlesLawChecks([]Series{depth}, oneLevel(30)); len(checks) != 1 || checks[0].Queue != queue {
		t.Fatalf("%+v", checks)
	}
}

func TestNoDetectionIsInsufficientEvidence(t *testing.T) {
	// A run without a steady level is never judged for onsets.
	got := Analyze(AnalysisInput{Validity: validityRun(20), Detection: DetectionInput{Series: []Series{detectSeries(symptomSeries, steady(40))}}}, nil)
	if got.Detection.Status != DetectionUnavailable || got.Outcome != OutcomeInsufficientEvidence || hasLimitation(got.Limitations, LimitationNoSymptoms) {
		t.Fatalf("%s %s %+v", got.Detection.Status, got.Outcome, got.Limitations)
	}
}
