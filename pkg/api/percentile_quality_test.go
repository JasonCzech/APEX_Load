package api

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Past the exact limits a report's percentiles come from the duration sketch:
// its rows, overview, SLA compliance rows and percentile SLA rules say so, and
// every report format marks them.
func TestReportMarksSketchedPercentiles(t *testing.T) {
	const n = archiveGateExactQuantileLimit + 5_000
	durations := timeoutBoundaryDurations(21, n, n/200)
	manifest := RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion, TestID: "approximate", TransactionCount: n,
		TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl",
		Provenance: RunArchiveProvenance{GoalConfig: &GoalConfig{TransactionRules: []TransactionSLARule{
			{Transaction: "Checkout", Metric: "p95_duration_ms", Comparator: "lte", Threshold: 60000},
			{Transaction: "Checkout", Metric: "average_duration_ms", Comparator: "lte", Threshold: 60000},
		}}},
	}
	dir, manifestPath := writeGatePercentileArchive(t, manifest, durations)
	if err := applyTransactionSLARules(dir, &manifest); err != nil {
		t.Fatal(err)
	}
	if got := manifest.TransactionRuleResults; len(got) != 2 || got[0].PercentileQuality != percentileQualityApproximate || got[1].PercentileQuality != "" {
		t.Fatalf("rule results = %+v; want the p95 rule approximate and the average rule exact", got)
	}
	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: dir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatal(err)
	}
	for name, quality := range map[string]string{
		"transaction row":        report.ByTransaction["Checkout"].PercentileQuality,
		"transaction population": report.MetricPopulations["transaction"].PercentileQuality,
		"overview":               report.Overview.PercentileQuality,
		"SLA compliance row":     buildSLAComplianceRowMap(report)["Checkout"].PercentileQuality,
	} {
		if quality != percentileQualityApproximate {
			t.Fatalf("%s quality = %q, want %q", name, quality, percentileQualityApproximate)
		}
	}

	html, err := renderRunArchiveHTMLReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(html, []byte(`<span class="approx" title="Approximate: from a duration sketch, within 0.5% of the value at its rank.">≈</span>`)) {
		t.Fatal("HTML report does not mark the sketched percentiles")
	}
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSV(report, &csv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), ",percentile_quality,approximate\n") {
		t.Fatal("CSV report does not name the sketched percentiles' quality")
	}
	profile, _ := lookupRunArchiveReportProfile("engineer")
	if lines := strings.Join(buildRunArchiveReportPDFLines(report, profile, nil, nil), "\n"); !strings.Contains(lines, "(approximate)") {
		t.Fatal("PDF and DOCX text does not mark the sketched percentiles")
	}
}

func TestReportPercentilesWithinTheExactLimitsCarryNoQuality(t *testing.T) {
	durations := timeoutBoundaryDurations(22, 500, 5)
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "exact", TransactionCount: int64(len(durations)),
		TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl"}
	dir, manifestPath := writeGatePercentileArchive(t, manifest, durations)
	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: dir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatal(err)
	}
	if report.ByTransaction["Checkout"].PercentileQuality != "" || report.Overview.PercentileQuality != "" || report.MetricPopulations["transaction"].PercentileQuality != "" {
		t.Fatalf("exact percentiles carry a quality: row %q, overview %q", report.ByTransaction["Checkout"].PercentileQuality, report.Overview.PercentileQuality)
	}
	html, err := renderRunArchiveHTMLReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(html, []byte(`class="approx"`)) {
		t.Fatal("HTML report marks exact percentiles")
	}
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSV(report, &csv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), ",percentile_quality,exact\n") || strings.Contains(csv.String(), ",percentile_quality,approximate") {
		t.Fatal("CSV report does not name exact percentiles as exact")
	}
}

func TestLoadLevelPercentileQualityNamesTheDistribution(t *testing.T) {
	started := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	level := ActiveLoadLevel{Group: "checkout", Name: "Level 1", TargetVUsers: 10, Duration: "10s", StartedAt: started}
	runtime := 10.0
	digest, sketch := boundaryDistributions([]float64{10, 20, 30})
	summary := func(withSketch bool) IntervalSummaryRecord {
		record := IntervalSummaryRecord{Timestamp: started, ActiveRuntimeSeconds: &runtime, SampleWindowSeconds: 10, LoadLevels: []ActiveLoadLevel{level},
			LoadLevelDurationDigests: map[string]*engine.TDigestSnapshot{"checkout": digest},
			Groups:                   map[string]*GroupLiveMetrics{"checkout": {Name: "checkout", Transactions: 3}}}
		if withSketch {
			record.LoadLevelDurationSketches = map[string]*engine.DurationSketchSnapshot{"checkout": sketch}
		}
		return record
	}
	for _, tc := range []struct {
		name       string
		withSketch bool
		want       string
	}{{"sketched", true, percentileQualityApproximate}, {"recorded before the sketch", false, percentileQualityTDigest}} {
		levels := buildRunArchiveLoadLevelSummaries([]IntervalSummaryRecord{summary(tc.withSketch)})
		if len(levels) != 1 || levels[0].PercentileQuality != tc.want || levels[0].P95Ms <= 0 {
			t.Fatalf("%s: load levels = %+v, want quality %q", tc.name, levels, tc.want)
		}
	}
}

// A distributed window whose percentiles could not come from merged sketches
// says so on the live metrics.
func TestLivePercentileQualityNamesFallbacks(t *testing.T) {
	parts := rawAggregates(timeoutBoundaryDurations(6, 2_000, 10), 2)
	older := func(values []generatorrpc.MetricAggregate, sketch, digest bool) []generatorrpc.MetricAggregate {
		out := append([]generatorrpc.MetricAggregate(nil), values...)
		if !sketch {
			out[0].DurationSketch = nil
		}
		if !digest {
			out[0].DurationDigest = nil
		}
		return out
	}
	for _, tc := range []struct {
		name   string
		values []generatorrpc.MetricAggregate
		want   string
	}{
		{"every generator sends a sketch", parts, ""},
		{"a generator sends only a t-digest", older(parts, false, true), percentileQualityTDigest},
		{"a generator sends no distribution", older(parts, false, false), percentileQualityAveraged},
		{"one generator that predates the sketch", older(parts[:1], false, true), percentileQualityTDigest},
		{"one current generator", parts[:1], ""},
	} {
		if got := mergeGeneratorMetricAggregates(tc.values).PercentileQuality; got != tc.want {
			t.Fatalf("%s: quality %q, want %q", tc.name, got, tc.want)
		}
	}

	merged := mergeGeneratorMetricAggregates(older(parts, false, true))
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, generatorrpc.MetricsBatch{WindowMs: 1000, FleetMetrics: merged, TransactionMetrics: []generatorrpc.MetricAggregate{*merged}}, 0, nil)
	if live.PercentileQuality != percentileQualityTDigest || live.Transactions["Checkout"].PercentileQuality != percentileQualityTDigest {
		t.Fatalf("live quality fleet %q, transaction %q", live.PercentileQuality, live.Transactions["Checkout"].PercentileQuality)
	}
}

func TestSelectedGroupPercentileQualityNamesFallbacks(t *testing.T) {
	durations := timeoutBoundaryDurations(7, 2_000, 10)
	batch := generatorrpc.MetricsBatch{WindowMs: 1000}
	for i, duration := range durations {
		batch.Transactions = append(batch.Transactions, generatorrpc.TransactionMetric{Name: "Checkout", Group: []string{"browse", "buy"}[i%2], DurationMs: duration, Success: true})
	}
	for _, tc := range []struct {
		name           string
		sketch, digest bool
		want           string
	}{{"sketches", true, true, ""}, {"t-digests", false, true, percentileQualityTDigest}, {"neither", false, false, percentileQualityAveraged}} {
		live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, batch, 0, nil)
		buy := live.GroupMetrics["buy"]
		if !tc.sketch {
			buy.DurationSketch = nil
		}
		if !tc.digest {
			buy.DurationDigest = nil
			live.GroupMetrics["browse"].DurationDigest = nil
		}
		applySelectedGroupAggregate(live, []*GroupLiveMetrics{live.GroupMetrics["browse"], buy})
		if live.PercentileQuality != tc.want {
			t.Fatalf("%s: quality %q, want %q", tc.name, live.PercentileQuality, tc.want)
		}
	}
}
