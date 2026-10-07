package rca

import (
	"encoding/json"
	"math"
	"math/rand"
	"testing"
	"time"
)

var detectStart = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

const detectBucket = 10 * time.Second

func hasLimitation(limitations []Limitation, code string) bool {
	for _, l := range limitations {
		if l.Code == code {
			return true
		}
	}
	return false
}

// detectSeries builds a 10 s latency series from values; NaN marks a gap.
func detectSeries(id string, values []float64) Series {
	s := Series{ID: id, Kind: SeriesLatency, Unit: "ms", Observation: ObservationInterval, Bucket: detectBucket, Evidence: EvidenceRef{Kind: "interval", Locator: "interval_distributions.jsonl#" + id}}
	for i, v := range values {
		s.Points = append(s.Points, SeriesPoint{At: detectStart.Add(time.Duration(i) * detectBucket), Value: v, Valid: !math.IsNaN(v)})
	}
	return s
}

// oneLevel is a single steady level spanning n buckets.
func oneLevel(n int) []LoadLevel {
	return []LoadLevel{{Start: detectStart, End: detectStart.Add(time.Duration(n) * detectBucket), VUsers: 50}}
}

// noisy returns n values around mean with Gaussian noise of sd.
func noisy(rng *rand.Rand, n int, mean, sd float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = mean + rng.NormFloat64()*sd
	}
	return out
}

func detect(t *testing.T, input DetectionInput) ([]Anomaly, Detection, []Limitation) {
	t.Helper()
	anomalies, detection, limitations := Detect(input, DefaultDetectParams, NewBudgeter(DefaultBudget, nil, nil))
	return anomalies, detection, limitations
}

func bucketOf(at time.Time) int { return int(at.Sub(detectStart) / detectBucket) }

func TestDetectStepOnsetWithinOneBucket(t *testing.T) {
	for seed := int64(1); seed <= 25; seed++ {
		rng := rand.New(rand.NewSource(seed))
		values := append(noisy(rng, 40, 200, 10), noisy(rng, 30, 300, 10)...)
		anomalies, _, _ := detect(t, DetectionInput{Levels: oneLevel(len(values)), Series: []Series{detectSeries("transaction/checkout/p95_ms", values)}})
		if len(anomalies) != 1 {
			t.Fatalf("seed %d: %d anomalies: %+v", seed, len(anomalies), anomalies)
		}
		a := anomalies[0]
		if onset := bucketOf(a.Onset); onset < 39 || onset > 41 {
			t.Fatalf("seed %d: onset bucket %d, want 40±1", seed, onset)
		}
		if a.Scope != ScopeTime || a.Direction != DirectionIncrease || a.Recovered || a.Effect < 80 || a.Effect > 120 || a.Unit != "ms" || a.PeakZ < 5 ||
			a.Baseline.Qualification != BaselineSameRun || a.Level != "vusers=50" || a.OnsetUncertainty < detectBucket || len(a.Evidence) != 1 {
			t.Fatalf("seed %d: %+v", seed, a)
		}
	}
}

func TestDetectGradualRamp(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	values := noisy(rng, 40, 200, 10)
	for i := 0; i < 40; i++ {
		values = append(values, 200+float64(i)*5+rng.NormFloat64()*10)
	}
	anomalies, _, _ := detect(t, DetectionInput{Levels: oneLevel(len(values)), Series: []Series{detectSeries("ramp", values)}})
	if len(anomalies) != 1 || anomalies[0].Direction != DirectionIncrease {
		t.Fatalf("%+v", anomalies)
	}
	// A ramp is found after it starts, with a wider uncertainty than a step.
	if onset := bucketOf(anomalies[0].Onset); onset < 40 || onset > 60 {
		t.Fatalf("ramp onset bucket %d", onset)
	}
}

func TestDetectSpikeIsNotAnOnset(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	values := noisy(rng, 60, 200, 10)
	values[40] = 600
	anomalies, detection, _ := detect(t, DetectionInput{Levels: oneLevel(len(values)), Series: []Series{detectSeries("spike", values)}})
	if len(anomalies) != 0 {
		t.Fatalf("spike became an onset: %+v", anomalies)
	}
	if len(detection.Spikes) != 1 || bucketOf(detection.Spikes[0].At) != 40 || detection.Spikes[0].Z < 5 || detection.Spikes[0].Value != 600 {
		t.Fatalf("spikes %+v", detection.Spikes)
	}
}

func TestDetectRecovery(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	values := append(append(noisy(rng, 30, 200, 10), noisy(rng, 10, 320, 10)...), noisy(rng, 30, 200, 10)...)
	anomalies, _, _ := detect(t, DetectionInput{Levels: oneLevel(len(values)), Series: []Series{detectSeries("recovery", values)}})
	if len(anomalies) != 1 || !anomalies[0].Recovered {
		t.Fatalf("%+v", anomalies)
	}
	if end := bucketOf(anomalies[0].End); end < 39 || end > 41 {
		t.Fatalf("recovered at bucket %d, want 40±1", end)
	}
}

func TestDetectZeroVarianceBaselineUsesTheUnitFloor(t *testing.T) {
	values := make([]float64, 40)
	for i := range values {
		values[i] = 100
	}
	// A 3 ms change is inside the 5 ms floor; a 60 ms step is not.
	small := append(append([]float64{}, values...), 103, 103, 103, 103, 103)
	if anomalies, _, _ := detect(t, DetectionInput{Levels: oneLevel(len(small)), Series: []Series{detectSeries("flat", small)}}); len(anomalies) != 0 {
		t.Fatalf("floor ignored: %+v", anomalies)
	}
	large := append(append([]float64{}, values...), 160, 160, 160, 160, 160)
	anomalies, _, _ := detect(t, DetectionInput{Levels: oneLevel(len(large)), Series: []Series{detectSeries("flat", large)}})
	if len(anomalies) != 1 || bucketOf(anomalies[0].Onset) != 40 || anomalies[0].PeakZ != 12 || anomalies[0].Effect != 60 {
		t.Fatalf("%+v", anomalies)
	}
}

func TestDetectGaps(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	values := append(noisy(rng, 40, 200, 10), noisy(rng, 30, 300, 10)...)
	// Short gaps inside the step do not hide it.
	values[42], values[50] = math.NaN(), math.NaN()
	anomalies, _, _ := detect(t, DetectionInput{Levels: oneLevel(len(values)), Series: []Series{detectSeries("gappy", values)}})
	if len(anomalies) != 1 || bucketOf(anomalies[0].Onset) < 39 || bucketOf(anomalies[0].Onset) > 41 {
		t.Fatalf("%+v", anomalies)
	}
	// A long gap resets the CUSUM: two short excursions on either side of it
	// do not add up to an onset.
	flat := noisy(rand.New(rand.NewSource(9)), 80, 200, 10)
	for i := 40; i < 50; i++ {
		flat[i] = math.NaN()
	}
	flat[38], flat[39], flat[50], flat[51] = 220, 220, 220, 220
	if anomalies, _, _ := detect(t, DetectionInput{Levels: oneLevel(len(flat)), Series: []Series{detectSeries("gap", flat)}}); len(anomalies) != 0 {
		t.Fatalf("gap bridged: %+v", anomalies)
	}
}

func TestDetectBaselinesAreAtTheSameLoad(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	// Two levels: latency is higher at 100 VUsers, which is not an anomaly
	// in this run. The second level has too few buckets for its own
	// baseline, which is a limitation.
	values := append(noisy(rng, 30, 200, 10), noisy(rng, 8, 400, 10)...)
	levels := []LoadLevel{
		{Start: detectStart, End: detectStart.Add(30 * detectBucket), VUsers: 50},
		{Start: detectStart.Add(30 * detectBucket), End: detectStart.Add(38 * detectBucket), VUsers: 100},
	}
	anomalies, detection, limitations := detect(t, DetectionInput{Levels: levels, Series: []Series{detectSeries("levels", values)}})
	if len(anomalies) != 0 || !hasLimitation(limitations, LimitationInsufficientBaseline) || detection.SeriesEvaluated != 1 {
		t.Fatalf("%+v %+v", anomalies, limitations)
	}
	// A return to the first level reuses its baseline.
	values = append(values, noisy(rng, 20, 290, 10)...)
	levels = append(levels, LoadLevel{Start: detectStart.Add(38 * detectBucket), End: detectStart.Add(58 * detectBucket), VUsers: 51})
	anomalies, _, _ = detect(t, DetectionInput{Levels: levels, Series: []Series{detectSeries("levels", values)}})
	if len(anomalies) != 1 || anomalies[0].Level != "vusers=51" || bucketOf(anomalies[0].Onset) != 38 {
		t.Fatalf("%+v", anomalies)
	}
	// Buckets outside every steady level are never judged.
	if _, _, limitations := detect(t, DetectionInput{Series: []Series{detectSeries("levels", values)}}); !hasLimitation(limitations, LimitationLoadLevelsUnavailable) {
		t.Fatalf("%+v", limitations)
	}
}

func TestDetectAgainstTheBaselineRun(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	current := detectSeries("transaction/checkout/p95_ms", noisy(rng, 30, 260, 10))
	baseline := detectSeries("transaction/checkout/p95_ms", noisy(rng, 30, 200, 10))
	input := DetectionInput{Levels: oneLevel(30), Series: []Series{current}, Baseline: &DetectionBaseline{TestID: "base", Qualification: BaselineQualified, Levels: []LoadLevel{{Start: detectStart, End: detectStart.Add(30 * detectBucket), VUsers: 49}}, Series: []Series{baseline}}}
	anomalies, _, _ := detect(t, input)
	var load *Anomaly
	for i := range anomalies {
		if anomalies[i].Scope == ScopeLoad {
			load = &anomalies[i]
		}
	}
	// The whole level is worse than the baseline run's same level.
	if load == nil || load.Baseline.TestID != "base" || load.Baseline.Qualification != BaselineQualified || bucketOf(load.Onset) != 0 || load.Effect < 40 {
		t.Fatalf("%+v", anomalies)
	}
	// An unqualified baseline is never compared.
	input.Baseline.Qualification = BaselineUnqualified
	if anomalies, _, _ := detect(t, input); len(anomalies) != 0 {
		t.Fatalf("unqualified baseline used: %+v", anomalies)
	}
}

func TestDetectExcludesCumulativeSeriesAndKeepsBudgets(t *testing.T) {
	rng := rand.New(rand.NewSource(19))
	counter := detectSeries("monitor/elastic/hits_total", noisy(rng, 40, 100, 1))
	counter.Kind, counter.Observation = SeriesProvider, ObservationCumulative
	steps := make([]Series, 3)
	for i := range steps {
		steps[i] = detectSeries(string(rune('a'+i)), append(noisy(rng, 40, 200, 10), noisy(rng, 20, 300, 10)...))
	}
	budget := DefaultBudget
	budget.MaxSeries = 2
	anomalies, detection, limitations := Detect(DetectionInput{Levels: oneLevel(60), Series: append(steps, counter)}, DefaultDetectParams, NewBudgeter(budget, nil, nil))
	if detection.SeriesExcluded != 1 || !hasLimitation(limitations, LimitationCumulativeExcluded) || detection.SeriesEvaluated != 2 || detection.Status != DetectionPartial || len(anomalies) != 2 {
		t.Fatalf("%+v %+v %+v", detection, limitations, anomalies)
	}
	params := DefaultDetectParams
	params.MaxAnomalies = 1
	anomalies, _, limitations = Detect(DetectionInput{Levels: oneLevel(60), Series: steps}, params, nil)
	if len(anomalies) != 1 || !hasLimitation(limitations, LimitationAnomaliesTruncated) {
		t.Fatalf("%+v %+v", anomalies, limitations)
	}
}

func TestDetectionIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	series := []Series{detectSeries("b", append(noisy(rng, 40, 200, 10), noisy(rng, 20, 300, 10)...)), detectSeries("a", append(noisy(rng, 40, 50, 2), noisy(rng, 20, 20, 2)...))}
	first, d1, _ := detect(t, DetectionInput{Levels: oneLevel(60), Series: series})
	second, d2, _ := detect(t, DetectionInput{Levels: oneLevel(60), Series: []Series{series[1], series[0]}})
	a, _ := json.Marshal([]any{first, d1})
	b, _ := json.Marshal([]any{second, d2})
	if string(a) != string(b) || len(first) != 2 || first[0].ID == first[1].ID || first[1].Direction != DirectionDecrease && first[0].Direction != DirectionDecrease {
		t.Fatalf("%s\n%s", a, b)
	}
}

// TestDetectFalseOnsetRate pins the uncalibrated parameters' behaviour on
// healthy series: k = 0.5 and h = 5 alarm about once per 465 buckets even
// with a perfect baseline, and WP3.9 calibrates them. This guards against
// regressions that make it worse.
func TestDetectFalseOnsetRate(t *testing.T) {
	onsets := 0
	for seed := int64(1); seed <= 200; seed++ {
		values := noisy(rand.New(rand.NewSource(seed+10000)), 360, 200, 10)
		anomalies, _, _ := detect(t, DetectionInput{Levels: oneLevel(len(values)), Series: []Series{detectSeries("healthy", values)}})
		onsets += len(anomalies)
	}
	if rate := float64(onsets) / 200; rate > 0.35 {
		t.Fatalf("%.2f false onsets per series-hour", rate)
	}
}

// steady alternates 195 and 205: a median of 200 and a scale of 10.
func steady(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 195 + float64(i%2)*10
	}
	return out
}

func TestDetectSpikeRightAfterARecovery(t *testing.T) {
	values := steady(40)
	for i := 0; i < 10; i++ {
		values = append(values, 320)
	}
	// Three in-band buckets recover the step; the very next bucket is a lone
	// excursion.
	values = append(values, 200, 200, 200, 600)
	values = append(values, steady(16)...)
	anomalies, detection, _ := detect(t, DetectionInput{Levels: oneLevel(len(values)), Series: []Series{detectSeries("after", values)}})
	if len(anomalies) != 1 || !anomalies[0].Recovered || bucketOf(anomalies[0].Onset) != 40 {
		t.Fatalf("%+v", anomalies)
	}
	if len(detection.Spikes) != 1 || bucketOf(detection.Spikes[0].At) != 53 {
		t.Fatalf("spikes %+v", detection.Spikes)
	}
}

func TestDetectSpikeBeforeAnOnsetStaysOutOfIt(t *testing.T) {
	values := steady(40)
	// Two slightly high buckets, a lone excursion, an in-band bucket, then a
	// real shift.
	values = append(values, 212, 212, 600, 200)
	for i := 0; i < 12; i++ {
		values = append(values, 260)
	}
	anomalies, detection, _ := detect(t, DetectionInput{Levels: oneLevel(len(values)), Series: []Series{detectSeries("before", values)}})
	if len(detection.Spikes) != 1 || bucketOf(detection.Spikes[0].At) != 42 {
		t.Fatalf("spikes %+v", detection.Spikes)
	}
	if len(anomalies) != 1 || bucketOf(anomalies[0].Onset) != 44 || anomalies[0].PeakZ > 10 {
		t.Fatalf("%+v", anomalies)
	}
}

func TestDetectReportsAnUnmatchedBaseline(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	input := DetectionInput{Levels: oneLevel(30), Series: []Series{detectSeries("transaction/checkout/p95_ms", noisy(rng, 30, 200, 10))},
		Baseline: &DetectionBaseline{TestID: "base", Qualification: BaselinePartial, Levels: oneLevel(30), Series: []Series{detectSeries("action/GET /cart/p95_ms", noisy(rng, 30, 200, 10))}}}
	if _, _, limitations := detect(t, input); !hasLimitation(limitations, LimitationBaselineUnmatched) {
		t.Fatalf("%+v", limitations)
	}
}

// Under the anomaly cap, anomalies of series that can be a symptom or a
// cause are kept before joint action-dimension slices, however large the
// slices' scores.
func TestAnomalyCapKeepsCausesBeforeJointSlices(t *testing.T) {
	anomalies := []Anomaly{
		{ID: "j1", Series: SeriesJointPrefix + "a", PeakZ: 40},
		{ID: "j2", Series: SeriesJointPrefix + "b", PeakZ: 39},
		{ID: "cpu", Series: "monitor/prometheus/cpu", PeakZ: 15},
		{ID: "tx", Series: "transaction/checkout", PeakZ: 20},
	}
	kept, cut := keepAnomalies(anomalies, 3)
	ids := map[string]bool{}
	for _, anomaly := range kept {
		ids[anomaly.ID] = true
	}
	if cut != 1 || len(kept) != 3 || !ids["cpu"] || !ids["tx"] || !ids["j1"] || ids["j2"] {
		t.Fatalf("kept %+v cut %d", kept, cut)
	}
	if kept, cut := keepAnomalies(anomalies, 10); cut != 0 || len(kept) != 4 {
		t.Fatal("cut within the cap")
	}
}
