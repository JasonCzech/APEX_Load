package rca

import (
	"fmt"
	"testing"
	"time"
)

// benchmarkAnalysisInput is a run of rows generator resource samples: ten
// generators reporting each one-second interval at one steady level, with a
// sustained saturation, an OOM kill and dropped results to judge.
func benchmarkAnalysisInput(rows int) AnalysisInput {
	const generators = 10
	intervals := rows / generators
	input := ValidityInput{ArrivalModel: ArrivalClosed, GeneratorIdentityRecorded: true, DataExhausted: new(bool),
		SteadyLevels: []TimeRange{{Start: validityStart, End: validityStart.Add(time.Duration(intervals) * time.Second)}}}
	results := int64(20000)
	for i := 0; i < intervals; i++ {
		start := validityStart.Add(time.Duration(i) * time.Second)
		dropped := int64(i / 1000)
		input.Intervals = append(input.Intervals, ValidityInterval{Start: start, End: start.Add(time.Second), Attempts: 1000, Results: &results, DroppedResults: &dropped, TPS: 1000, AvgLatencyMs: 40})
		for g := 0; g < generators; g++ {
			sample := GeneratorSample{Generator: fmt.Sprintf("gen-%02d", g), At: start.Add(time.Second), CPUPct: 50, StealPct: 1, Periods: 10}
			if g == 3 && i%500 < 4 {
				sample.CPUPct = 92
			}
			if g == 7 && i == intervals/2 {
				sample.OOMKills = 1
			}
			input.Samples = append(input.Samples, sample)
		}
	}
	return AnalysisInput{Acceptance: Acceptance{Status: AcceptancePassed}, Validity: input}
}

// BenchmarkRCAAnalyze baselines the Phase 3a analysis (acceptance and
// validity); docs/ANALYSIS_WORKFLOWS.md records the results.
func BenchmarkRCAAnalyze(b *testing.B) {
	for _, rows := range []int{10000, 1000000} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			input := benchmarkAnalysisInput(rows)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got := Analyze(input, NewBudgeter(DefaultBudget, nil, LiveHeapBytes))
				if got.Validity.Verdict != ValidityInvalid {
					b.Fatalf("verdict %s", got.Validity.Verdict)
				}
			}
		})
	}
}
