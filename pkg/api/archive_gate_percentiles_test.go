package api

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// gateSweepSamples draws latencies near 120 ms with a share of 30-second
// timeouts, the shape whose p99 sits on a mode boundary when timeouts are close
// to 1% of requests.
func gateSweepSamples(seed int64, n int, slowShare float64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	samples := make([]float64, n)
	for i := range samples {
		value := math.Exp(math.Log(120) + 0.3*rng.NormFloat64())
		if rng.Float64() < slowShare {
			value = 30000 + rng.Float64()*50
		}
		samples[i] = value
	}
	return samples
}

func sortedGateSamples(samples []float64) []float64 {
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	return sorted
}

// archiveBracketError reports how far a percentile lies outside the sketch's
// accuracy band around the two samples that bracket rank q*(n-1).
func archiveBracketError(sorted []float64, q, got float64) float64 {
	rank := q * float64(len(sorted)-1)
	lo, hi := sorted[int(math.Floor(rank))], sorted[int(math.Ceil(rank))]
	low, high := lo*(1-engine.DurationSketchRelativeAccuracy-1e-9), hi*(1+engine.DurationSketchRelativeAccuracy+1e-9)
	switch {
	case got < low:
		return (low - got) / lo
	case got > high:
		return (got - high) / hi
	}
	return 0
}

func TestArchiveGateQuantilesStayExactThroughTheExactLimit(t *testing.T) {
	samples := gateSweepSamples(7, archiveGateExactQuantileLimit, 0.00987)
	acc := newArchiveGateQuantileAccumulator(newArchiveQuantileBudget())
	for _, sample := range samples {
		acc.Add(sample)
	}
	if acc.sketch != nil {
		t.Fatalf("gate accumulator sketched %d samples, want exact through %d", len(samples), archiveGateExactQuantileLimit)
	}
	sorted := sortedGateSamples(samples)
	for _, q := range []float64{0.50, 0.90, 0.95, 0.99} {
		if got, want := acc.Quantile(q), percentileFromSortedSamples(sorted, q); got != want {
			t.Fatalf("p%.0f = %v, want exact %v", q*100, got, want)
		}
	}
}

func TestArchiveQuantilesHaveNoModeBoundaryErrorBeyondTheExactLimit(t *testing.T) {
	// Timeouts near 1% put p99 on the boundary between modes, where the old
	// t-digest reported values from the empty gap. Beyond the exact limits the
	// sketch stays within its accuracy band in every run.
	const runs, samplesPerRun = 60, 60_000
	for trial := 0; trial < runs; trial++ {
		samples := gateSweepSamples(int64(1000+trial), samplesPerRun, 0.0095+float64(trial%20)*0.00005)
		ordinary := &archiveQuantileAccumulator{}
		gate := newArchiveGateQuantileAccumulator(newArchiveQuantileBudget())
		for _, sample := range samples {
			ordinary.Add(sample)
			gate.Add(sample)
		}
		sorted := sortedGateSamples(samples)
		for name, acc := range map[string]*archiveQuantileAccumulator{"ordinary": ordinary, "gate": gate} {
			if acc.sketch == nil {
				t.Fatalf("%s accumulator kept %d samples exact", name, len(acc.exact))
			}
			for _, q := range []float64{0.95, 0.99} {
				if err := archiveBracketError(sorted, q, acc.Quantile(q)); err > 0 {
					t.Fatalf("run %d %s p%v = %v, outside the sketch's band by %.3g", trial, name, q*100, acc.Quantile(q), err)
				}
			}
		}
	}
}

func TestArchiveGateQuantileBudget(t *testing.T) {
	addSamples := func(acc *archiveQuantileAccumulator, n int) {
		for i := 0; i < n; i++ {
			acc.Add(float64(i))
		}
	}
	extraExactBytes := int64(archiveGateExactQuantileLimit-archiveExactQuantileLimit) * archiveGateExactSampleBytes

	t.Run("promotion returns extra exact samples to the budget", func(t *testing.T) {
		budget := newArchiveQuantileBudget()
		acc := newArchiveGateQuantileAccumulator(budget)
		addSamples(acc, archiveGateExactQuantileLimit)
		if acc.sketch != nil || budget.remainingBytes != archiveGateQuantileBudgetBytes-extraExactBytes {
			t.Fatalf("after %d samples: sketch %v, budget %d; want exact samples charged beyond the ordinary limit", archiveGateExactQuantileLimit, acc.sketch != nil, budget.remainingBytes)
		}
		acc.Add(1)
		if acc.sketch == nil || acc.Count() != archiveGateExactQuantileLimit+1 || budget.remainingBytes != archiveGateQuantileBudgetBytes {
			t.Fatalf("after promotion: sketch %v, count %d, budget %d", acc.sketch != nil, acc.Count(), budget.remainingBytes)
		}
	})

	t.Run("a spent budget keeps the ordinary limit", func(t *testing.T) {
		acc := newArchiveGateQuantileAccumulator(&archiveQuantileBudget{})
		addSamples(acc, archiveExactQuantileLimit)
		if acc.sketch != nil {
			t.Fatalf("sketched within the ordinary exact limit")
		}
		acc.Add(1)
		if acc.sketch == nil {
			t.Fatalf("still exact past the ordinary limit with no budget")
		}
	})

	t.Run("accumulators share one bound", func(t *testing.T) {
		const extraSamples = 5_000
		budget := &archiveQuantileBudget{remainingBytes: extraSamples * archiveGateExactSampleBytes}
		first, second := newArchiveGateQuantileAccumulator(budget), newArchiveGateQuantileAccumulator(budget)
		addSamples(first, archiveExactQuantileLimit+extraSamples)
		addSamples(second, archiveExactQuantileLimit+1)
		if first.sketch != nil || second.sketch == nil || budget.remainingBytes != 0 {
			t.Fatalf("first sketch %v, second sketch %v, budget %d; want the first holding the whole budget", first.sketch != nil, second.sketch != nil, budget.remainingBytes)
		}
		first.Add(1)
		if first.sketch == nil || budget.remainingBytes != extraSamples*archiveGateExactSampleBytes {
			t.Fatalf("first sketch %v, budget %d; want the budget returned", first.sketch != nil, budget.remainingBytes)
		}
	})
}

// writeGatePercentileArchive writes a minimal archive whose transactions are
// one business transaction named Checkout with the given durations.
func writeGatePercentileArchive(t *testing.T, manifest RunArchiveManifest, durations []float64) (string, string) {
	t.Helper()
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, manifest.TransactionsFile))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, duration := range durations {
		if err := encoder.Encode(TransactionEvent{ActionName: "Checkout", BusinessTransaction: true, Success: true, DurationMs: duration}); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, manifestPath
}

func TestTransactionSLAAndReportShareExactPercentiles(t *testing.T) {
	const fast, slow = 14_852, 148 // timeouts just under 1%: the true p99 is still a fast request
	rng := rand.New(rand.NewSource(42))
	durations := make([]float64, 0, fast+slow)
	for i := 0; i < fast; i++ {
		durations = append(durations, math.Exp(math.Log(120)+0.3*rng.NormFloat64()))
	}
	for i := 0; i < slow; i++ {
		durations = append(durations, 30000+float64(i))
	}
	rng.Shuffle(len(durations), func(i, j int) { durations[i], durations[j] = durations[j], durations[i] })
	exact := percentileFromSortedSamples(sortedGateSamples(durations), 0.99)
	if exact >= 500 {
		t.Fatalf("fixture exact p99 = %v, want it under the 500 ms threshold", exact)
	}

	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "gate-percentiles",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount:      fast + slow,
		Provenance: RunArchiveProvenance{GoalConfig: &GoalConfig{
			TransactionRules: []TransactionSLARule{{Transaction: "Checkout", Metric: "p99_duration_ms", Comparator: "lte", Threshold: 500}},
		}},
	}
	dir, manifestPath := writeGatePercentileArchive(t, manifest, durations)

	if err := applyTransactionSLARules(dir, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.TransactionRuleResults) != 1 {
		t.Fatalf("results = %+v", manifest.TransactionRuleResults)
	}
	result := manifest.TransactionRuleResults[0]
	if result.Status != "pass" || result.Observed == nil || *result.Observed != exact {
		t.Fatalf("SLA result = %s observed %v (%s), want pass at the exact p99 %v", result.Status, result.Observed, result.Message, exact)
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: dir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]float64{
		"transaction table":      report.ByTransaction["Checkout"].P99Ms,
		"transaction population": report.MetricPopulations["transaction"].P99Ms,
		"overview":               report.Overview.P99Ms,
	} {
		if got != exact {
			t.Fatalf("report %s p99 = %v, want the SLA's exact %v", name, got, exact)
		}
	}
	exactP95 := percentileFromSortedSamples(sortedGateSamples(durations), 0.95)
	if row := buildSLAComplianceRowMap(report)["Checkout"]; row.P95Ms != exactP95 {
		t.Fatalf("SLA compliance row p95 = %v, want exact %v", row.P95Ms, exactP95)
	}
}
