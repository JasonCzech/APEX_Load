package rca

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca/signatures"
)

// tailAttribution is checkout's P95 tail +400 ms over a 600 ms median, with
// the given phases carrying it.
func tailAttribution(phases map[string]float64) PhaseAttribution {
	return PhaseAttribution{
		Transaction: "checkout", Source: signatures.PhaseSourceTail, Population: 400, Instances: 20, ReferenceInstances: 200,
		JoinCoverage: 1, ReferenceJoinCoverage: 1, ReferenceMs: 600, DeltaMs: 400, Phases: phases,
		Evidence: EvidenceRef{Kind: "time_anatomy", Locator: "report#time_anatomy/transaction=checkout"},
	}
}

func TestPhaseAttributionShiftAppliesTheEvidenceBounds(t *testing.T) {
	ok := tailAttribution(map[string]float64{"server": 360, "connect": 30, "download": 10})
	shift, valid := ok.Shift()
	if !valid || shift.Dominant != signatures.PhaseServer || len(shift.Grown) != 1 || shift.Grown[0].DeltaMs != 360 {
		t.Fatalf("%+v %v", shift, valid)
	}
	for name, mutate := range map[string]func(*PhaseAttribution){
		"population below the tail minimum": func(p *PhaseAttribution) { p.Population = PhaseMinInstances - 1 },
		"too few tail instances":            func(p *PhaseAttribution) { p.Instances = PhaseMinTailInstances - 1 },
		"too few median instances":          func(p *PhaseAttribution) { p.ReferenceInstances = PhaseMinMedianInstances - 1 },
		"low join coverage":                 func(p *PhaseAttribution) { p.JoinCoverage = 0.79 },
		"delta under 20 ms":                 func(p *PhaseAttribution) { p.DeltaMs, p.ReferenceMs = 19, 10 },
		"delta under a fifth of the median": func(p *PhaseAttribution) { p.ReferenceMs = 2500 },
		"no phase carries a quarter":        func(p *PhaseAttribution) { p.Phases = map[string]float64{"server": 90, "connect": 90} },
		"unknown source":                    func(p *PhaseAttribution) { p.Source = "guess" },
	} {
		p := ok
		mutate(&p)
		if _, valid := p.Shift(); valid {
			t.Fatalf("%s: became evidence", name)
		}
	}
	// A baseline comparison needs the tail minimum on both sides.
	baseline := ok
	baseline.Source, baseline.Instances, baseline.ReferenceInstances = signatures.PhaseSourceBaseline, PhaseMinInstances, PhaseMinInstances-1
	if _, valid := baseline.Shift(); valid {
		t.Fatal("a thin baseline became evidence")
	}
	// Two material phases, none dominant.
	split := tailAttribution(map[string]float64{"server": 180, "connect": 160})
	if shift, valid := split.Shift(); !valid || shift.Dominant != "" || len(shift.Grown) != 2 || shift.Grown[0].Phase != signatures.PhaseServer {
		t.Fatalf("%+v", shift)
	}
}

func TestBaselineHandshakeShift(t *testing.T) {
	reuse, refReuse, tls, refTLS := 40.0, 92.0, 70.0, 12.0
	p := tailAttribution(map[string]float64{"tls": 300})
	p.Source, p.Instances, p.ReferenceInstances = signatures.PhaseSourceBaseline, 100, 100
	p.ReusePct, p.ReferenceReusePct, p.TLSObservedPct, p.ReferenceTLSObservedPct = &reuse, &refReuse, &tls, &refTLS
	shift, ok := p.Shift()
	if !ok || !shift.HandshakesUp || !strings.Contains(shift.HandshakeDetail, "reuse fell from 92% to 40%") || !strings.Contains(shift.HandshakeDetail, "70% of requests, up from 12%") {
		t.Fatalf("%+v", shift)
	}
	// A steady reuse rate is no handshake shift.
	refReuse, refTLS = 45, 65
	if shift, _ := p.Shift(); shift.HandshakesUp {
		t.Fatalf("%+v", shift)
	}
}

func TestPhaseAttributionCorroboratesAndCites(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	plain, _, _ := Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	base := hypothesisFor(plain, "resource_pressure")
	server := tailAttribution(map[string]float64{"server": 360, "connect": 40})
	hypotheses, outcome, _ := HypothesizeWithPhases(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams, []PhaseAttribution{server})
	h := hypothesisFor(hypotheses, "resource_pressure")
	if outcome != OutcomeSupported || h == nil || base == nil || h.Score <= base.Score || h.Confidence != ConfidenceHigh {
		t.Fatalf("%+v vs %+v", h, base)
	}
	cited := false
	for _, ref := range h.Supporting {
		cited = cited || ref.Kind == "time_anatomy" && strings.Contains(ref.Locator, "checkout")
	}
	reasons := strings.Join(h.ConfidenceReasons, " | ")
	if !cited || !strings.Contains(reasons, "the P95 tail of checkout is +400 ms over its median, +360 ms (90%) of it in server") {
		t.Fatalf("supporting %+v reasons %s", h.Supporting, reasons)
	}
}

func TestPhaseAttributionWeakensASignatureItCannotExplain(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	download := tailAttribution(map[string]float64{"download": 380})
	hypotheses, _, limitations := HypothesizeWithPhases(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams, []PhaseAttribution{download})
	h := hypothesisFor(hypotheses, "resource_pressure")
	if h == nil || h.Confidence == ConfidenceHigh || len(h.Contradicting) != 1 || h.Contradicting[0].Kind != "time_anatomy" ||
		!strings.Contains(strings.Join(h.ConfidenceReasons, " "), "of it in download, not in server or TLS") {
		t.Fatalf("%+v", h)
	}
	if !hasLimitation(limitations, LimitationPhaseTransfer) {
		t.Fatalf("%+v", limitations)
	}
}

func TestThinPhaseAttributionChangesNothing(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	plain, _, plainLimits := Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	thin := tailAttribution(map[string]float64{"download": 380})
	thin.Population = PhaseMinInstances - 1
	hypotheses, _, limitations := HypothesizeWithPhases(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams, []PhaseAttribution{thin})
	a, b := hypothesisFor(plain, "resource_pressure"), hypothesisFor(hypotheses, "resource_pressure")
	if a == nil || b == nil || a.Score != b.Score || a.Confidence != b.Confidence || len(b.Contradicting) != 0 || len(limitations) != len(plainLimits) {
		t.Fatalf("%+v vs %+v", a, b)
	}
}

func TestPhaseAttributionAloneSupportsNothing(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	// CPU measured but flat, and the tail grew in server: still no
	// hypothesis, since attribution has no onset to order.
	hypotheses, outcome, _ := HypothesizeWithPhases(anomalies[:1], series, allJudged(series), graph, oneLevel(200), DefaultDetectParams, []PhaseAttribution{tailAttribution(map[string]float64{"server": 380})})
	if outcome != OutcomeNoSupportedHypothesis || len(hypotheses) != 0 {
		t.Fatalf("%s %+v", outcome, hypotheses)
	}
}

func TestBaselinePhaseShiftWinsOverTheTail(t *testing.T) {
	tail := tailAttribution(map[string]float64{"download": 380})
	baseline := tailAttribution(map[string]float64{"server": 380})
	baseline.Source, baseline.Instances, baseline.ReferenceInstances = signatures.PhaseSourceBaseline, 100, 100
	for _, order := range [][]PhaseAttribution{{tail, baseline}, {baseline, tail}} {
		shifts, refs := phaseShifts(order)
		if shift := shifts[TransactionNodeID("checkout")]; shift.Source != signatures.PhaseSourceBaseline || refs[TransactionNodeID("checkout")].ID == "" {
			t.Fatalf("%+v", shift)
		}
	}
}

func TestSignaturePhaseScoring(t *testing.T) {
	support := signatures.Observation{AnomalyID: "a", Kind: signatures.KindTLS, Direction: signatures.Increase, Order: signatures.Precedes, Connected: true, PeakZ: 5}
	cpu := signatures.Observation{AnomalyID: "c", Kind: signatures.KindCPU, Direction: signatures.Increase, Order: signatures.Precedes, Connected: true, PeakZ: 5}
	tls := signatures.PhaseShift{Transaction: "login", Source: signatures.PhaseSourceTail, DeltaMs: 200, Dominant: signatures.PhaseTLS, Grown: []signatures.PhaseGrowth{{Phase: signatures.PhaseTLS, DeltaMs: 180, Share: 0.9}}}
	judge := func(sig signatures.Signature, o signatures.Observation, shift *signatures.PhaseShift) signatures.Result {
		return sig.Judge(signatures.Context{
			Symptom:      signatures.Observation{Kind: signatures.KindLatency, Direction: signatures.Increase},
			Observations: []signatures.Observation{o},
			Available:    map[string]signatures.Availability{o.Kind: {Any: true, Connected: true}},
			Phase:        shift,
		})
	}
	if r, plain := judge(signatures.NetworkPhase, support, &tls), judge(signatures.NetworkPhase, support, nil); !r.PhaseSupported || r.Score <= plain.Score {
		t.Fatalf("network_phase %+v", r)
	}
	// TLS growth backs handshake CPU only with more handshakes; a
	// TLS-dominated change without them is one resource pressure cannot
	// explain, so it weakens it.
	if r, plain := judge(signatures.ResourcePressure, cpu, &tls), judge(signatures.ResourcePressure, cpu, nil); r.PhaseSupported || !r.PhaseContradicted ||
		r.Score != plain.Score-0.5 || !strings.Contains(strings.Join(r.Reasons, " "), "not in server or TLS with more handshakes") {
		t.Fatalf("resource_pressure without handshakes %+v", r)
	}
	tls.HandshakesUp, tls.HandshakeDetail = true, "connection reuse fell from 90% to 20%"
	if r := judge(signatures.ResourcePressure, cpu, &tls); !r.PhaseSupported || !strings.Contains(strings.Join(r.Notes, " "), "reuse fell") {
		t.Fatalf("resource_pressure with handshakes %+v", r)
	}
	// Connect growth backs a pool exhaustion.
	connect := signatures.PhaseShift{Transaction: "login", Source: signatures.PhaseSourceTail, DeltaMs: 200, Dominant: signatures.PhaseConnect, Grown: []signatures.PhaseGrowth{{Phase: signatures.PhaseConnect, DeltaMs: 180, Share: 0.9}}}
	pool := signatures.Observation{AnomalyID: "p", Kind: signatures.KindPool, Direction: signatures.Increase, Order: signatures.Precedes, Connected: true, PeakZ: 5}
	if r := judge(signatures.PoolExhaustion, pool, &connect); !r.PhaseSupported {
		t.Fatalf("pool_exhaustion %+v", r)
	}
	// A signature that declares no phases is untouched.
	lag := signatures.Observation{AnomalyID: "l", Kind: signatures.KindConsumerLag, Direction: signatures.Increase, Order: signatures.Precedes, Connected: true, PeakZ: 5}
	if r, plain := judge(signatures.ConsumerLag, lag, &connect), judge(signatures.ConsumerLag, lag, nil); r.Score != plain.Score || r.PhaseContradicted {
		t.Fatalf("consumer_lag %+v", r)
	}
}

// A signature its own evidence contradicts stays contradicted: the phase
// bonus never creates a hypothesis.
func TestPhaseBonusNeverRescuesAContradictedSignature(t *testing.T) {
	cpu := signatures.Observation{AnomalyID: "c", Kind: signatures.KindCPU, Direction: signatures.Increase, Order: signatures.Unordered, Connected: false, PeakZ: 3}
	memory := signatures.Observation{AnomalyID: "m", Kind: signatures.KindMemory, Direction: signatures.Increase, Order: signatures.Follows, Connected: true, PeakZ: 3}
	server := signatures.PhaseShift{Transaction: "checkout", Source: signatures.PhaseSourceTail, DeltaMs: 400, Dominant: signatures.PhaseServer, Grown: []signatures.PhaseGrowth{{Phase: signatures.PhaseServer, DeltaMs: 380, Share: 0.95}}}
	judge := func(shift *signatures.PhaseShift) signatures.Result {
		return signatures.ResourcePressure.Judge(signatures.Context{
			Symptom:      signatures.Observation{Kind: signatures.KindLatency, Direction: signatures.Increase},
			Observations: []signatures.Observation{cpu, memory},
			Available:    map[string]signatures.Availability{signatures.KindCPU: {Any: true}, signatures.KindMemory: {Any: true, Connected: true}},
			Phase:        shift,
		})
	}
	plain, phased := judge(nil), judge(&server)
	if plain.Verdict != signatures.Contradicted || plain.Score >= 0 {
		t.Fatalf("fixture: %+v", plain)
	}
	if phased.Verdict != signatures.Contradicted || phased.PhaseSupported || phased.Score != plain.Score {
		t.Fatalf("the phase bonus rescued a contradicted signature: %+v", phased)
	}
}

// A streamed LLM transaction's download time is generation time: it is
// neither a transfer problem nor evidence against service signatures.
func TestStreamedTransactionDownloadIsNotTransfer(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	download := tailAttribution(map[string]float64{"download": 380})
	ttft := func(runLevel bool, points ...SeriesPoint) []Series {
		return append(slices.Clone(series), Series{ID: "transaction/checkout/ttft_p95_ms", Kind: SeriesLatency, Measures: signatures.KindTTFT, RunLevel: runLevel, Points: points})
	}
	streamedSeries := ttft(false, SeriesPoint{At: detectStart, Value: 0}, SeriesPoint{At: detectStart.Add(time.Minute), Value: 840, Valid: true})
	plain, _, _ := Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	// Telemetry that does not show this transaction streaming does not
	// excuse its download: no points, only invalid (non-streamed) points,
	// or the run-level series the interval-summary fallback files under it.
	for name, notStreamed := range map[string][]Series{
		"point-less":  ttft(false),
		"all invalid": ttft(false, SeriesPoint{At: detectStart, Value: 0}, SeriesPoint{At: detectStart.Add(time.Minute), Value: 900}),
		"non-finite":  ttft(false, SeriesPoint{At: detectStart, Value: math.NaN(), Valid: true}),
		"run-level":   ttft(true, SeriesPoint{At: detectStart, Value: 840, Valid: true}),
	} {
		hypotheses, _, limitations := HypothesizeWithPhases(anomalies, notStreamed, allJudged(notStreamed), graph, oneLevel(200), DefaultDetectParams, []PhaseAttribution{download})
		if h := hypothesisFor(hypotheses, "resource_pressure"); !hasLimitation(limitations, LimitationPhaseTransfer) || h == nil || len(h.Contradicting) != 1 {
			t.Fatalf("%s: %+v %+v", name, h, limitations)
		}
	}
	hypotheses, _, limitations := HypothesizeWithPhases(anomalies, streamedSeries, allJudged(streamedSeries), graph, oneLevel(200), DefaultDetectParams, []PhaseAttribution{download})
	a, h := hypothesisFor(plain, "resource_pressure"), hypothesisFor(hypotheses, "resource_pressure")
	if a == nil || h == nil || h.Score != a.Score || h.Confidence != a.Confidence || len(h.Contradicting) != 0 || hasLimitation(limitations, LimitationPhaseTransfer) {
		t.Fatalf("%+v %+v", h, limitations)
	}
	// The same download tail without LLM telemetry is still a transfer
	// problem.
	if _, _, limitations := HypothesizeWithPhases(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams, []PhaseAttribution{download}); !hasLimitation(limitations, LimitationPhaseTransfer) {
		t.Fatalf("%+v", limitations)
	}
	// Server growth in a streamed transaction still counts.
	server := tailAttribution(map[string]float64{"server": 200, "download": 180})
	hypotheses, _, _ = HypothesizeWithPhases(anomalies, streamedSeries, allJudged(streamedSeries), graph, oneLevel(200), DefaultDetectParams, []PhaseAttribution{server})
	if h := hypothesisFor(hypotheses, "resource_pressure"); h == nil || h.Score <= a.Score {
		t.Fatalf("%+v", h)
	}
}

// Only a latency symptom reads the anatomy: an error-rate symptom on the
// same transaction gets no bonus, no penalty and no transfer limitation.
func TestPhaseAttributionIgnoresErrorRateSymptoms(t *testing.T) {
	anomalies, series, graph := hypothesisFixture(7*time.Minute, 10*time.Second, true)
	const errorSeries = "transaction/checkout/error_rate"
	anomalies[0].Series = errorSeries
	series[0] = Series{ID: errorSeries, Kind: SeriesErrorRate}
	plain, _, plainLimits := Hypothesize(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams)
	base := hypothesisFor(plain, "resource_pressure")
	if base == nil {
		t.Fatalf("fixture: %+v", plain)
	}
	for _, phases := range []map[string]float64{{"server": 380}, {"download": 380}} {
		hypotheses, _, limitations := HypothesizeWithPhases(anomalies, series, allJudged(series), graph, oneLevel(200), DefaultDetectParams, []PhaseAttribution{tailAttribution(phases)})
		h := hypothesisFor(hypotheses, "resource_pressure")
		if h == nil || h.Score != base.Score || h.Confidence != base.Confidence || len(h.Contradicting) != 0 || len(h.Supporting) != len(base.Supporting) ||
			hasLimitation(limitations, LimitationPhaseTransfer) || len(limitations) != len(plainLimits) {
			t.Fatalf("%v: %+v %+v", phases, h, limitations)
		}
	}
}

// A dominant phase the signature cannot explain weakens it even when a
// lesser phase it does explain also grew.
func TestUnexplainedDominantPhaseWinsOverALesserMatch(t *testing.T) {
	wait := signatures.Observation{AnomalyID: "d", Kind: signatures.KindDBWait, Direction: signatures.Increase, Order: signatures.Precedes, Connected: true, PeakZ: 5}
	shift := signatures.PhaseShift{Transaction: "checkout", Source: signatures.PhaseSourceTail, DeltaMs: 400, Dominant: signatures.PhaseDownload, Grown: []signatures.PhaseGrowth{
		{Phase: signatures.PhaseDownload, DeltaMs: 240, Share: 0.6}, {Phase: signatures.PhaseServer, DeltaMs: 120, Share: 0.3},
	}}
	judge := func(shift *signatures.PhaseShift) signatures.Result {
		return signatures.DatabaseWait.Judge(signatures.Context{
			Symptom:      signatures.Observation{Kind: signatures.KindLatency, Direction: signatures.Increase},
			Observations: []signatures.Observation{wait},
			Available:    map[string]signatures.Availability{signatures.KindDBWait: {Any: true, Connected: true}},
			Phase:        shift,
		})
	}
	plain, r := judge(nil), judge(&shift)
	if r.PhaseSupported || !r.PhaseContradicted || r.Score != plain.Score-0.5 || !strings.Contains(strings.Join(r.Reasons, " "), "in download, not in server") {
		t.Fatalf("%+v", r)
	}
	// Without a dominant phase, the lesser match still strengthens it.
	shift.Dominant = ""
	if r := judge(&shift); !r.PhaseSupported || r.PhaseContradicted || r.Score != plain.Score+0.5 {
		t.Fatalf("%+v", r)
	}
}
