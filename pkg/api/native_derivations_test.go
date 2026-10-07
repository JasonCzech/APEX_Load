package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func archiveBucketOutcomes(series NativeArchiveSeries) string {
	parts := make([]string, len(series.Buckets))
	for i, b := range series.Buckets {
		if b.Value != nil {
			body, _ := json.Marshal(*b.Value)
			parts[i] = string(body)
		} else {
			parts[i] = b.Reason
		}
	}
	return strings.Join(parts, " ")
}

// An archive panel's derivation is evaluated on its buckets: a complete
// window with no events is a measured zero for counts, an unscored zero
// denominator for ratios, and a bucket past completion is incomplete.
func TestNativeArchiveDerivationsOnTheBucketGrid(t *testing.T) {
	record, _ := nativeSeriesFixture(t)
	derive := func(q storage.AnalyticsQuery, baseline *NativeArchiveSeries, comparable bool) (NativeArchiveResponse, string) {
		t.Helper()
		if err := storage.ValidateNativeArchiveQuery(q); err != nil {
			t.Fatal(err)
		}
		current, err := buildNativeArchiveSeries(context.Background(), record, q)
		if err != nil {
			t.Fatal(err)
		}
		response := NativeArchiveResponse{Current: current, Baseline: baseline, Comparable: comparable}
		if err := applyNativeArchiveDerivation("p", q, &response); err != nil {
			t.Fatal(err)
		}
		return response, archiveBucketOutcomes(response.Current)
	}
	ratio := storage.AnalyticsQuery{Population: "action", Metric: "error_rate", Aggregation: "avg", BucketSeconds: 10, EndSeconds: 30, Derivation: &analysis.QueryDerivation{Operation: "error_ratio"}}
	response, outcomes := derive(ratio, nil, false)
	if outcomes != "0.3333333333333333 zero_denominator incomplete_evidence" || response.Current.Unit != "ratio" || response.Derivation == nil || response.Derivation.Operation != "error_ratio" {
		t.Fatalf("error ratio %q unit %q", outcomes, response.Current.Unit)
	}
	moving := storage.AnalyticsQuery{Population: "action", Metric: "observation_count", Aggregation: "count", BucketSeconds: 10, EndSeconds: 20, Derivation: &analysis.QueryDerivation{Operation: "moving_mean", WindowBuckets: 2, MinimumCoverage: 1}}
	if _, outcomes := derive(moving, nil, false); outcomes != "insufficient_window 1.5" {
		t.Fatalf("moving mean over a measured zero %q", outcomes)
	}
	// A baseline derivation compares only a comparable baseline run.
	plain := storage.AnalyticsQuery{Population: "action", Metric: "response_time", Aggregation: "max", BucketSeconds: 10, EndSeconds: 20}
	baseline, err := buildNativeArchiveSeries(context.Background(), record, plain)
	if err != nil {
		t.Fatal(err)
	}
	delta := plain
	delta.ComparisonRunID = "baseline-run"
	delta.Derivation = &analysis.QueryDerivation{Operation: "baseline_delta"}
	if _, outcomes := derive(delta, &baseline, true); outcomes != "0 missing_baseline_bucket" {
		t.Fatalf("baseline delta %q", outcomes)
	}
	if _, outcomes := derive(delta, &baseline, false); outcomes != "baseline_not_comparable baseline_not_comparable" {
		t.Fatalf("incomparable baseline %q", outcomes)
	}
	// A query without a derivation renders exactly as before.
	unchanged, outcomes := derive(plain, nil, false)
	if outcomes != "30 " || unchanged.Derivation != nil || unchanged.Current.Unit != "" || unchanged.Current.Buckets[1].Reason != "" {
		t.Fatalf("plain archive panel changed: %q %+v", outcomes, unchanged)
	}
}

func TestNativeGroupedEvidenceDerivation(t *testing.T) {
	record, _ := nativeSeriesFixture(t)
	q := storage.AnalyticsQuery{EvidenceMode: "archive", Population: "action", Metric: "error_rate", Aggregation: "avg", BucketSeconds: 10, EndSeconds: 20, MaxSeries: 2, Dimensions: []string{"action"},
		Derivation: &analysis.QueryDerivation{Operation: "success_ratio"}}
	if err := storage.ValidateNativeEvidenceQuery(q); err != nil {
		t.Fatal(err)
	}
	out, err := buildNativeGroupedArchive(context.Background(), record, q)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyNativeEvidenceDerivation("p", q, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Series) != 1 || out.Series[0].Unit != "ratio" || out.Series[0].Points[0].Value == nil || *out.Series[0].Points[0].Value != 2.0/3 ||
		out.Series[0].Points[1].Reason != "zero_denominator" || out.Derivation == nil {
		t.Fatalf("%+v", out)
	}
}

func TestNativeLiveEvidenceDerivation(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	interval := func(seconds int, metrics map[string]storage.NativeIntervalMetric, incomplete bool) *LiveMetrics {
		return &LiveMetrics{Timestamp: start.Add(time.Duration(seconds) * time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Version: 1, WindowSeconds: 1, Incomplete: incomplete,
			Dimensions: map[string]map[string]storage.NativeIntervalMetric{"action": metrics}}}
	}
	history := []*LiveMetrics{
		interval(1, map[string]storage.NativeIntervalMetric{"Checkout": {Count: 4, Errors: 1, Avg: 10}}, false),
		interval(2, map[string]storage.NativeIntervalMetric{"Other": {Count: 2, Avg: 10}}, false),
		interval(3, map[string]storage.NativeIntervalMetric{"Checkout": {Count: 5, Errors: 5, Avg: 10}}, false),
	}
	q := storage.AnalyticsQuery{EvidenceMode: "live", LiveBucketMode: "source", Population: "action", Metric: "error_rate", Aggregation: "avg", BucketSeconds: 1, EndSeconds: 10, MaxSeries: 1,
		Filters: map[string]string{"action": "Checkout"}, Derivation: &analysis.QueryDerivation{Operation: "error_ratio"}}
	if err := storage.ValidateNativeEvidenceQuery(q); err != nil {
		t.Fatal(err)
	}
	out, err := buildNativeLiveEvidence(context.Background(), start, history, q)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyNativeEvidenceDerivation("p", q, &out); err != nil {
		t.Fatal(err)
	}
	points := out.Series[0].Points
	if len(points) != 3 || *points[0].Value != .25 || points[1].Reason != "zero_denominator" || *points[2].Value != 1 {
		t.Fatalf("%+v", points)
	}
}

// Message ratios read the archive's messaging v2 counters: the counted
// messages among the observed ones, refusing a bucket the counter did not
// observe completely or that only v1 summaries describe.
func TestNativeMessageRatioFromMessagingEvidence(t *testing.T) {
	root := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{TestID: "run", CompletedAt: start.Add(40 * time.Second), IntervalSummariesFile: "intervals.jsonl", Provenance: RunArchiveProvenance{StartedAt: &start}}
	body, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	counter := func(eligible, observed, count int64) protocol.MessagingCounterObservation {
		return protocol.MessagingCounterObservation{Metric: "dead_letter", Population: "received_message", Scope: "delivery_header", Eligible: eligible, Observed: observed, Count: count}
	}
	dimension := func(source string, received int64, evidence *protocol.MessagingEvidence) protocol.MessagingDimension {
		return protocol.MessagingDimension{Source: source, Received: received, Evidence: evidence}
	}
	summaries := []IntervalSummaryRecord{
		{Timestamp: start.Add(5 * time.Second), Messaging: map[string]*protocol.MessagingSummary{"consume": {Dimensions: []protocol.MessagingDimension{
			dimension("orders", 100, &protocol.MessagingEvidence{Operations: 100, Counters: []protocol.MessagingCounterSummary{counter(100, 100, 4)}}),
			dimension("audit", 50, &protocol.MessagingEvidence{Operations: 50, Counters: []protocol.MessagingCounterSummary{counter(50, 50, 1)}}),
		}}}},
		{Timestamp: start.Add(15 * time.Second), Messaging: map[string]*protocol.MessagingSummary{"consume": {Dimensions: []protocol.MessagingDimension{
			dimension("orders", 80, &protocol.MessagingEvidence{Operations: 80, Counters: []protocol.MessagingCounterSummary{counter(80, 60, 2)}}),
		}}}},
		{Timestamp: start.Add(25 * time.Second), Messaging: map[string]*protocol.MessagingSummary{"consume": {Dimensions: []protocol.MessagingDimension{dimension("orders", 30, nil)}}}},
	}
	file, err := os.Create(filepath.Join(root, "intervals.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range summaries {
		if err := json.NewEncoder(file).Encode(summary); err != nil {
			t.Fatal(err)
		}
	}
	file.Close()
	record := &storage.RunArchiveRecord{TestID: "run", ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}
	q := storage.AnalyticsQuery{Population: "message", Metric: "message_count", Aggregation: "count", BucketSeconds: 10, EndSeconds: 40, Derivation: &analysis.QueryDerivation{Operation: "dlq_ratio"}}
	if err := storage.ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal(err)
	}
	series, err := buildNativeMessageSeriesForTest(t, record, q)
	if err != nil {
		t.Fatal(err)
	}
	response := NativeArchiveResponse{Current: series}
	if err := applyNativeArchiveDerivation("p", q, &response); err != nil {
		t.Fatal(err)
	}
	if outcomes := archiveBucketOutcomes(response.Current); outcomes != "0.03333333333333333 incomplete_evidence incomplete_evidence incomplete_evidence" {
		t.Fatalf("dead-letter ratio %q", outcomes)
	}
	// A source filter selects one consumer source; the plain count is the
	// received messages with v2 evidence, so a v1-only bucket stays missing.
	q.Filters, q.Derivation = map[string]string{"source": "orders"}, nil
	plain, err := buildNativeMessageSeriesForTest(t, record, q)
	if err != nil || archiveBucketOutcomes(plain) != "100 80  " || len(plain.Identities) != 1 {
		t.Fatalf("message count %q %+v %v", archiveBucketOutcomes(plain), plain.Identities, err)
	}
	q.Derivation = &analysis.QueryDerivation{Operation: "acknowledgement_ratio"}
	unavailable, _ := buildNativeMessageSeriesForTest(t, record, q)
	response = NativeArchiveResponse{Current: unavailable}
	if err := applyNativeArchiveDerivation("p", q, &response); err != nil || !strings.HasPrefix(archiveBucketOutcomes(response.Current), "evidence_unavailable") {
		t.Fatalf("acknowledgement ratio %q %v", archiveBucketOutcomes(response.Current), err)
	}
}

func buildNativeMessageSeriesForTest(t *testing.T, record *storage.RunArchiveRecord, q storage.AnalyticsQuery) (NativeArchiveSeries, error) {
	t.Helper()
	return buildNativeArchiveSeries(context.Background(), record, q)
}

func TestImportedCounterDerivationsAreResetAware(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	point := func(seconds int, value float64) analysis.MetricPoint {
		return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", Source: "src", MetricName: "requests_total", Population: analysis.PopulationSample, MetricVersion: "v1",
			Kind: analysis.KindCounter, Unit: "count", TimestampUTC: start.Add(time.Duration(seconds) * time.Second), Quality: analysis.QualityValid, Value: &value}
	}
	in := &storage.AnalysisImport{ID: "ds", ProjectID: "p", Revision: 3}
	query := analytics.SeriesQuery{Metric: "requests_total", From: start, To: start.Add(50 * time.Second), BucketSeconds: 10, Aggregation: "max"}
	points := []analysis.MetricPoint{point(0, 100), point(10, 160), point(20, 40), point(40, 90)}
	rate, err := importedDerivation(in, query, points, analysis.QueryDerivation{Operation: "rate"})
	if err != nil || rate.Unit != "count/s" {
		t.Fatalf("%+v %v", rate, err)
	}
	outcomes := make([]string, len(rate.Buckets))
	for i, b := range rate.Buckets {
		outcomes[i] = b.Reason
		if b.Value != nil {
			body, _ := json.Marshal(*b.Value)
			outcomes[i] = string(body)
		}
	}
	if strings.Join(outcomes, " ") != "no_prior_sample 6 counter_reset missing_counter_sample missing_counter_sample" {
		t.Fatalf("rate %q", strings.Join(outcomes, " "))
	}
	query.Aggregation = "avg"
	if _, err := importedDerivation(in, query, points, analysis.QueryDerivation{Operation: "delta"}); err == nil {
		t.Fatal("a counter derivation over bucket means was accepted")
	}
}

func TestDashboardDefinitionsValidateDerivationsAndLegacyPanels(t *testing.T) {
	saved := 0
	definition := func(query storage.AnalyticsQuery) *storage.DashboardDefinition {
		saved++
		return &storage.DashboardDefinition{ID: "d" + string(rune('a'+saved)), Name: "D", Tabs: []storage.DashboardTab{{ID: "t", Title: "T", Panels: []storage.DashboardPanel{{ID: "p", Title: "P", Kind: "line", W: 12, H: 4, Query: &query}}}}}
	}
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// A legacy live card keeps saving exactly as before.
	if err := store.SaveDashboardDefinition(definition(storage.AnalyticsQuery{Metric: "tps", Aggregation: "stddev", Dimensions: []string{"page"}})); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDashboardDefinition(definition(storage.AnalyticsQuery{EvidenceMode: "archive", Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 60, MaxSeries: 1,
		Derivation: &analysis.QueryDerivation{Operation: "moving_mean", WindowBuckets: 6, MinimumCoverage: .5}})); err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]storage.AnalyticsQuery{
		"legacy card derivation": {Metric: "tps", Aggregation: "avg", Derivation: &analysis.QueryDerivation{Operation: "moving_mean", WindowBuckets: 2, MinimumCoverage: 1}},
		"unknown metric":         {Metric: "latency", Aggregation: "avg"},
		"unbounded window":       {EvidenceMode: "archive", Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 60, MaxSeries: 1, Derivation: &analysis.QueryDerivation{Operation: "moving_mean", WindowBuckets: 5000, MinimumCoverage: 1}},
		"baseline without run":   {EvidenceMode: "archive", Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 60, MaxSeries: 1, Derivation: &analysis.QueryDerivation{Operation: "baseline_delta"}},
	} {
		if err := store.SaveDashboardDefinition(definition(query)); err == nil {
			t.Fatalf("%s saved", name)
		}
	}
}
