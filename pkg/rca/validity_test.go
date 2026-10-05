package rca

import (
	"reflect"
	"testing"
	"time"
)

var validityStart = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// validityRun is a closed-model run of n one-second intervals at a steady
// level, with healthy samples from each generator in every interval.
func validityRun(n int, generators ...string) ValidityInput {
	if len(generators) == 0 {
		generators = []string{"g1"}
	}
	input := ValidityInput{ArrivalModel: ArrivalClosed, Generators: generators, GeneratorIdentityRecorded: true, DataExhausted: new(bool),
		SteadyLevels: []TimeRange{{Start: validityStart, End: validityStart.Add(time.Duration(n) * time.Second)}}}
	zero := int64(0)
	for i := 0; i < n; i++ {
		start := validityStart.Add(time.Duration(i) * time.Second)
		input.Intervals = append(input.Intervals, ValidityInterval{Start: start, End: start.Add(time.Second), Attempts: 1000, DroppedResults: &zero, ShedArrivals: &zero, TPS: 1000, AvgLatencyMs: 50})
		for _, g := range generators {
			input.Samples = append(input.Samples, GeneratorSample{Generator: g, At: start.Add(time.Second), CPUPct: 40, StealPct: 1, Periods: 10})
		}
	}
	return input
}

func sampleAt(input *ValidityInput, interval int, generator string) *GeneratorSample {
	for i := range input.Samples {
		s := &input.Samples[i]
		if s.Generator == generator && s.At.Equal(input.Intervals[interval].End) {
			return s
		}
	}
	return nil
}

func cumulative(v int64) *int64 { return &v }

func TestValidityRules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*ValidityInput)
		verdict string
		reasons []string
	}{
		{"healthy run", func(*ValidityInput) {}, ValidityValid, []string{}},
		{"CPU at the threshold for three intervals", func(in *ValidityInput) {
			for i := 3; i < 6; i++ {
				sampleAt(in, i, "g1").CPUPct = 85
			}
		}, ValiditySuspect, []string{RuleGeneratorCPU}},
		{"CPU for two intervals is not sustained", func(in *ValidityInput) {
			for _, i := range []int{3, 4, 6, 7} {
				sampleAt(in, i, "g1").CPUPct = 95
			}
		}, ValidityValid, []string{}},
		{"CPU spread across generators is not sustained", func(in *ValidityInput) {
			sampleAt(in, 3, "g1").CPUPct = 95
			sampleAt(in, 4, "g2").CPUPct = 95
			sampleAt(in, 5, "g1").CPUPct = 95
		}, ValidityValid, []string{}},
		{"steal", func(in *ValidityInput) {
			for i := 0; i < 3; i++ {
				sampleAt(in, i, "g2").StealPct = 12
			}
		}, ValiditySuspect, []string{RuleGeneratorSteal}},
		{"throttled in a tenth of periods", func(in *ValidityInput) {
			for i := 5; i < 8; i++ {
				sampleAt(in, i, "g1").ThrottledPeriods = 1
			}
		}, ValiditySuspect, []string{RuleGeneratorThrottled}},
		{"an OOM kill", func(in *ValidityInput) { sampleAt(in, 7, "g2").OOMKills = 1 }, ValidityInvalid, []string{RuleGeneratorOOMKill}},
		{"dropped results above 0.1%", func(in *ValidityInput) {
			for i := 4; i < len(in.Intervals); i++ {
				in.Intervals[i].DroppedResults = cumulative(2)
			}
		}, ValidityInvalid, []string{RuleDroppedResults}},
		{"dropped results at 0.1% are tolerated", func(in *ValidityInput) {
			for i := 4; i < len(in.Intervals); i++ {
				in.Intervals[i].DroppedResults = cumulative(1)
			}
		}, ValidityValid, []string{}},
		{"shed arrivals in an open model", func(in *ValidityInput) {
			in.ArrivalModel = ArrivalOpen
			in.Intervals[9].ShedArrivals = cumulative(3)
		}, ValidityInvalid, []string{RuleShedArrivals}},
		{"data exhaustion", func(in *ValidityInput) { exhausted := true; in.DataExhausted = &exhausted }, ValidityInvalid, []string{RuleDataExhausted}},
		{"invalid outranks suspect and names only invalidating reasons", func(in *ValidityInput) {
			for i := 0; i < 3; i++ {
				sampleAt(in, i, "g1").CPUPct = 99
			}
			sampleAt(in, 10, "g1").OOMKills = 2
		}, ValidityInvalid, []string{RuleGeneratorOOMKill}},
		{"stats missing for more than a tenth of intervals", func(in *ValidityInput) {
			drop := map[int]bool{2: true, 5: true, 9: true}
			kept := in.Samples[:0]
			for _, s := range in.Samples {
				if !(s.Generator == "g2" && drop[int(s.At.Sub(validityStart)/time.Second)-1]) {
					kept = append(kept, s)
				}
			}
			in.Samples = kept
		}, ValidityUnknown, []string{RuleGeneratorStatsMissing}},
		{"stats missing for a tenth of intervals still judge the rest", func(in *ValidityInput) {
			kept := in.Samples[:0]
			for _, s := range in.Samples {
				if !(s.Generator == "g2" && s.At.Equal(in.Intervals[4].End)) {
					kept = append(kept, s)
				}
			}
			in.Samples = kept
		}, ValidityValid, []string{}},
		{"an expected generator that never reported", func(in *ValidityInput) { in.Generators = append(in.Generators, "g3") }, ValidityUnknown, []string{RuleGeneratorStatsMissing}},
		{"suspect outranks missing stats", func(in *ValidityInput) {
			in.Generators = append(in.Generators, "g3")
			for i := 0; i < 4; i++ {
				sampleAt(in, i, "g1").CPUPct = 90
			}
		}, ValiditySuspect, []string{RuleGeneratorCPU}},
		{"evidence gaps keep a run from valid", func(in *ValidityInput) { in.EvidenceGaps = []string{"interval_summaries_truncated"} }, ValidityUnknown, []string{RuleEvidenceIncomplete}},
		{"saturation outside a steady level is not judged", func(in *ValidityInput) {
			in.SteadyLevels = []TimeRange{{Start: validityStart.Add(5 * time.Second), End: validityStart.Add(20 * time.Second)}}
			for i := 0; i < 4; i++ {
				sampleAt(in, i, "g1").CPUPct = 99
			}
		}, ValidityValid, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := validityRun(20, "g1", "g2")
			tc.mutate(&input)
			got := AssessValidity(input, DefaultValidityRules)
			if got.Verdict != tc.verdict || !reflect.DeepEqual(got.Reasons, tc.reasons) {
				t.Fatalf("verdict %s %v, want %s %v; windows %+v", got.Verdict, got.Reasons, tc.verdict, tc.reasons, got.Windows)
			}
			if tc.verdict != ValidityValid && tc.verdict != ValidityUnknown && len(got.Windows) == 0 && tc.reasons[0] != RuleDataExhausted {
				t.Fatal("a failing verdict lists no windows")
			}
		})
	}
}

func TestValidityListsTheSustainedWindows(t *testing.T) {
	input := validityRun(10)
	for i := 2; i < 5; i++ {
		sampleAt(&input, i, "g1").CPUPct = 92
	}
	got := AssessValidity(input, DefaultValidityRules)
	if got.Verdict != ValiditySuspect || len(got.Windows) != 3 || !got.Windows[0].Start.Equal(input.Intervals[2].Start) || got.IntervalsJudged != 10 {
		t.Fatalf("%+v", got)
	}
	f := got.Windows[1].Findings[0]
	if f.Rule != RuleGeneratorCPU || f.Generator != "g1" || f.Value != 92 || f.Threshold != 85 {
		t.Fatalf("finding %+v", f)
	}
}

func TestClosedLoopSUTSlowdownStaysValidWithAnAnomaly(t *testing.T) {
	input := validityRun(30)
	for i := 20; i < 30; i++ {
		input.Intervals[i].TPS = 600
		input.Intervals[i].AvgLatencyMs = 90
	}
	got := AssessValidity(input, DefaultValidityRules)
	if got.Verdict != ValidityValid || len(got.Anomalies) != 1 || got.Anomalies[0].Kind != AnomalyClosedModelSlowdown || got.Anomalies[0].Value != -40 {
		t.Fatalf("%+v", got)
	}
}

func TestOpenModelShortfallWithHealthyGeneratorsIsAnAnomaly(t *testing.T) {
	input := validityRun(12)
	input.ArrivalModel = ArrivalOpen
	for i := 4; i < 8; i++ {
		input.Intervals[i].TargetMissed = true
	}
	// A shortfall while a generator was saturated is not the system's.
	input.Intervals[10].TargetMissed = true
	sampleAt(&input, 8, "g1").CPUPct = 99
	sampleAt(&input, 9, "g1").CPUPct = 99
	sampleAt(&input, 10, "g1").CPUPct = 99
	got := AssessValidity(input, DefaultValidityRules)
	if got.Verdict != ValiditySuspect || len(got.Anomalies) != 1 || got.Anomalies[0].Kind != AnomalyOpenModelShortfall || got.Anomalies[0].Value != 4 || !got.Anomalies[0].Start.Equal(input.Intervals[4].Start) {
		t.Fatalf("%+v", got)
	}
}

func TestValidityLimitationsAndUnrecordedEvidence(t *testing.T) {
	input := validityRun(10)
	input.SteadyLevels = nil
	input.DataExhausted = nil
	input.ArrivalModel = ArrivalUnknown
	for i := range input.Intervals {
		input.Intervals[i].DroppedResults = nil
		input.Intervals[i].ShedArrivals = nil
	}
	got := AssessValidity(input, DefaultValidityRules)
	want := []string{LimitationArrivalModelUnrecorded, LimitationDataExhaustionUnrecorded, LimitationDroppedResultsUnrecorded, LimitationNoSteadyLevel, LimitationShedArrivalsUnrecorded}
	if got.Verdict != ValidityValid || !reflect.DeepEqual(got.Limitations, want) || got.IntervalsJudged != 10 {
		t.Fatalf("%+v", got)
	}
	if empty := AssessValidity(ValidityInput{}, DefaultValidityRules); empty.Verdict != ValidityUnknown || !reflect.DeepEqual(empty.Reasons, []string{RuleNoIntervals}) {
		t.Fatalf("no intervals: %+v", empty)
	}
	// Without any generator sample nothing can be judged.
	input = validityRun(10)
	input.Samples = nil
	if got := AssessValidity(input, DefaultValidityRules); got.Verdict != ValidityUnknown {
		t.Fatalf("no samples: %+v", got)
	}
}

func TestValidityWithoutGeneratorIdentityUsesTheWorstSample(t *testing.T) {
	input := validityRun(10, "a", "b")
	input.GeneratorIdentityRecorded = false
	input.Generators = []string{"target-1", "target-2"}
	for i := 0; i < 3; i++ {
		// Alternate generators saturate; without identity the pool's worst
		// sample counts.
		sampleAt(&input, i, []string{"a", "b"}[i%2]).CPUPct = 90
	}
	got := AssessValidity(input, DefaultValidityRules)
	if got.Verdict != ValiditySuspect || got.Windows[0].Findings[0].Generator != "" || !contains(got.Limitations, LimitationGeneratorIdentityUnrecorded) {
		t.Fatalf("%+v", got)
	}
}

func TestThrottlingWithoutPeriodsIsALimitation(t *testing.T) {
	input := validityRun(10)
	for i := range input.Samples {
		input.Samples[i].Periods = 0
		input.Samples[i].ThrottledPeriods = 5
	}
	got := AssessValidity(input, DefaultValidityRules)
	if got.Verdict != ValidityValid || !contains(got.Limitations, LimitationThrottlePeriodsUnrecorded) {
		t.Fatalf("%+v", got)
	}
}

func TestValidityIsDeterministic(t *testing.T) {
	input := validityRun(40, "g1", "g2", "g3")
	for i := 10; i < 15; i++ {
		sampleAt(&input, i, "g2").CPUPct = 97
		sampleAt(&input, i, "g3").StealPct = 30
	}
	first, second := AssessValidity(input, DefaultValidityRules), AssessValidity(input, DefaultValidityRules)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("verdicts differ")
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestValidityDroppedShareUsesEveryResult(t *testing.T) {
	input := validityRun(10)
	results := int64(2000)
	for i := range input.Intervals {
		input.Intervals[i].Attempts = 100 // business transactions only
		input.Intervals[i].Results = &results
		if i >= 5 {
			input.Intervals[i].DroppedResults = cumulative(1)
		}
	}
	if got := AssessValidity(input, DefaultValidityRules); got.Verdict != ValidityValid {
		t.Fatalf("1 of 2,001 results dropped: %+v", got)
	}
	input.Intervals[5].Results = nil
	if got := AssessValidity(input, DefaultValidityRules); got.Verdict != ValidityInvalid {
		t.Fatalf("without result counts the transaction attempts are the denominator: %+v", got)
	}
}

func TestValidityCountersIgnoreDipsFromLateGenerators(t *testing.T) {
	input := validityRun(12)
	// A fleet sum dips when one generator's batch misses a window; only new
	// highs are new drops.
	for i, value := range []int64{0, 0, 0, 2, 2, 2, 0, 0, 2, 2, 4, 4} {
		input.Intervals[i].DroppedResults = cumulative(value)
	}
	got := AssessValidity(input, DefaultValidityRules)
	if got.Verdict != ValidityInvalid || len(got.Windows) != 2 || !got.Windows[0].Start.Equal(input.Intervals[3].Start) || !got.Windows[1].Start.Equal(input.Intervals[10].Start) {
		t.Fatalf("drops across a dip: %+v", got)
	}
}

func TestGeneratorsAreMissingOnlyInsideTheSpanTheyReported(t *testing.T) {
	input := validityRun(20, "steady", "added", "removed")
	kept := input.Samples[:0]
	for _, sample := range input.Samples {
		second := int(sample.At.Sub(validityStart)/time.Second) - 1
		if sample.Generator == "added" && second < 8 || sample.Generator == "removed" && second >= 12 {
			continue
		}
		kept = append(kept, sample)
	}
	input.Samples = kept
	if got := AssessValidity(input, DefaultValidityRules); got.Verdict != ValidityValid || got.MissingFraction != 0 {
		t.Fatalf("scaled generators: %+v", got)
	}
	// A gap inside a generator's span still counts.
	input.Samples = append([]GeneratorSample(nil), kept...)
	for i := len(input.Samples) - 1; i >= 0; i-- {
		second := int(input.Samples[i].At.Sub(validityStart)/time.Second) - 1
		if input.Samples[i].Generator == "steady" && second >= 4 && second < 7 {
			input.Samples = append(input.Samples[:i], input.Samples[i+1:]...)
		}
	}
	if got := AssessValidity(input, DefaultValidityRules); got.Verdict != ValidityUnknown || got.MissingFraction != 0.15 {
		t.Fatalf("gap inside a span: %+v", got)
	}
}

func TestValidityDroppedShareFloorsResultsAtCompletedTransactions(t *testing.T) {
	input := validityRun(10)
	none := int64(0)
	for i := range input.Intervals {
		input.Intervals[i].Attempts = 5000
		input.Intervals[i].Results = &none // nothing started in the interval
	}
	input.Intervals[6].DroppedResults = cumulative(1)
	for i := 7; i < 10; i++ {
		input.Intervals[i].DroppedResults = cumulative(1)
	}
	if got := AssessValidity(input, DefaultValidityRules); got.Verdict != ValidityValid {
		t.Fatalf("one drop against 5,000 completed transactions: %+v", got)
	}
}

func TestSustainedSaturationMustBeAtTheSteadyLevel(t *testing.T) {
	input := validityRun(20)
	input.SteadyLevels = []TimeRange{{Start: validityStart.Add(5 * time.Second), End: validityStart.Add(20 * time.Second)}}
	// Two ramp seconds and one steady second.
	for i := 3; i < 6; i++ {
		sampleAt(&input, i, "g1").CPUPct = 95
	}
	if got := AssessValidity(input, DefaultValidityRules); got.Verdict != ValidityValid {
		t.Fatalf("saturation crossing into the steady level: %+v", got)
	}
}

func TestValidityJudgesGeneratorsBeyondTheExpectedList(t *testing.T) {
	input := validityRun(10, "listed", "unlisted")
	input.Generators = []string{"listed"}
	for i := 2; i < 6; i++ {
		sampleAt(&input, i, "unlisted").CPUPct = 96
	}
	got := AssessValidity(input, DefaultValidityRules)
	if got.Verdict != ValiditySuspect || got.Windows[0].Findings[0].Generator != "unlisted" {
		t.Fatalf("%+v", got)
	}
}
