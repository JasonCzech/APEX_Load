package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

var jointTestProjection = &engine.JointProjection{Population: "action", Dimensions: []string{"name", "region", "network_profile"}}

func jointKey(values ...string) string {
	key, _ := engine.JointTupleKey(values)
	return key
}

func jointAggregate(key string, values ...float64) generatorrpc.MetricAggregate {
	m := nativeBucketMetric(values...)
	return generatorrpc.MetricAggregate{Name: key, Count: m.Count, AvgMs: m.Avg, DurationDigest: m.DurationDigest}
}

func jointBatch(sequence uint64, at time.Time, tuples ...generatorrpc.MetricAggregate) generatorrpc.MetricsBatch {
	return generatorrpc.MetricsBatch{ActionMetricsVersion: 3, BatchSequence: sequence, ExecutionGeneration: 1, TimestampUnixMs: at.UnixMilli(), WindowMs: 1000,
		JointMetrics: tuples, JointInfo: &generatorrpc.JointMetricsInfo{Version: engine.JointEvidenceVersion, Population: jointTestProjection.Population, Dimensions: jointTestProjection.Dimensions}}
}

func TestJointEvidenceProjectionPersistsAndFreezes(t *testing.T) {
	if _, err := normalizeJointEvidence(&engine.JointProjection{Population: "action", Dimensions: []string{"host"}}); err == nil || !strings.Contains(err.Error(), "joint_evidence") {
		t.Fatalf("invalid projection: %v", err)
	}
	test := &ManagedTest{ID: "joint", ProjectID: "p", JointEvidence: jointTestProjection.Clone()}
	record := buildManagedTestRecord(test)
	encoded, err := json.Marshal(record.JointEvidence)
	if err != nil {
		t.Fatal(err)
	}
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatal(err)
	}
	record.JointEvidence = generic
	if restored := managedTestFromRecord(record); !reflect.DeepEqual(restored.JointEvidence, jointTestProjection) {
		t.Fatalf("restored %+v", restored.JointEvidence)
	}
	if decodeStoredJointEvidence(map[string]any{"population": "action", "dimensions": []any{"host"}}) != nil {
		t.Fatal("a stored projection that no longer validates was kept")
	}
	provenance := buildRunArchiveProvenanceFromSnapshot(test)
	if !reflect.DeepEqual(provenance.JointEvidence, jointTestProjection) {
		t.Fatalf("provenance %+v", provenance.JointEvidence)
	}
	// Start freezes the projection: a later edit of the test does not move it.
	test.initialArchiveProvenance = &provenance
	test.JointEvidence = &engine.JointProjection{Population: "transaction", Dimensions: []string{"group"}}
	if got := runJointProjection(test); !reflect.DeepEqual(got, jointTestProjection) {
		t.Fatalf("running projection %+v, want the frozen one", got)
	}
}

func TestJointEvidenceAssignmentGatedByCapability(t *testing.T) {
	capable, older := &startupAssignmentFakeClient{}, &startupAssignmentFakeClient{}
	plans := []distributedTargetAssignmentPlan{
		{target: "gen-new", client: capable, jointEvidence: true, shards: []GeneratorShardSummary{{Group: "checkout", Target: "gen-new", VUsers: 1, ShardCount: 2}}},
		{target: "gen-old", client: older, shards: []GeneratorShardSummary{{Group: "checkout", Target: "gen-old", VUsers: 1, ShardOrdinal: 1, ShardCount: 2}}},
	}
	backend := newDistributedExecutionBackend(nil, 30*time.Second, nil)
	backend.assignDistributedGroupTargets(context.Background(), plans, generatorrpc.Script{ID: "s"}, generatorrpc.Assignment{TestID: "run", GroupName: "checkout", ScriptID: "s", JointEvidenceProjection: jointTestProjection.Clone()}, "Checkout", func() bool { return true }, nil)
	if got := capable.recordedAssignments(); len(got) != 1 || !reflect.DeepEqual(got[0].JointEvidenceProjection, jointTestProjection) {
		t.Fatalf("capable generator assignment %+v", got)
	}
	if got := older.recordedAssignments(); len(got) != 1 || got[0].JointEvidenceProjection != nil {
		t.Fatalf("an older generator received the projection: %+v", got)
	}
}

func TestJointEvidenceIngestRecordsAvailability(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	batch := jointBatch(1, at, jointAggregate(jointKey("login", "east", "3g"), 10, 20), jointAggregate("not a tuple", 5))
	publication := nativePublicationEvidence("gen-a", batch, map[string]generatorrpc.MetricsBatch{"gen-a": batch}, jointTestProjection)
	if publication.Joint == nil || publication.Joint.Availability != "measured" || !publication.Joint.Incomplete {
		t.Fatalf("an undecodable tuple must leave the measured window incomplete: %+v", publication.Joint)
	}
	if got := publication.Dimensions[storage.NativeJointDimension]; len(got) != 1 || got[jointKey("login", "east", "3g")].Count != 2 {
		t.Fatalf("tuples %+v", got)
	}
	old := generatorrpc.MetricsBatch{ActionMetricsVersion: 3, BatchSequence: 1, ExecutionGeneration: 1, TimestampUnixMs: at.UnixMilli(), WindowMs: 1000}
	if got := nativePublicationEvidence("gen-old", old, nil, jointTestProjection).Joint; got == nil || got.Availability != "unavailable" || got.Reason != jointReasonSourceLacksCapability {
		t.Fatalf("an older generator's window %+v", got)
	}
	mismatched := jointBatch(2, at)
	mismatched.JointInfo.Dimensions = []string{"name"}
	if got := nativePublicationEvidence("gen-a", mismatched, nil, jointTestProjection).Joint; got.Availability != "unavailable" || got.Reason != jointReasonProjectionMismatch {
		t.Fatalf("a window under another projection %+v", got)
	}
	if got := nativePublicationEvidence("gen-a", old, nil, nil).Joint; got != nil {
		t.Fatalf("a run without a projection has joint evidence %+v", got)
	}
	// A local run's engine window.
	snapshot := engine.MetricsSnapshot{Timestamp: at, Window: time.Second, ByAction: map[string]*engine.ActionMetrics{}, Joint: &engine.JointMetricsSnapshot{Version: 4, Population: "action", Dimensions: []string{"name"}, Tuples: map[string]*engine.ActionMetrics{jointKey("login"): {Count: 3, AvgDuration: 10 * time.Millisecond}}}}
	live := buildLiveMetricsFromSnapshot(snapshot, nil)
	if live.NativeEvidence.Joint == nil || live.NativeEvidence.Joint.Availability != "measured" || live.NativeEvidence.Dimensions[storage.NativeJointDimension][jointKey("login")].Count != 3 {
		t.Fatalf("local joint evidence %+v", live.NativeEvidence.Joint)
	}
}

// The fleet merge of a mixed fleet keeps the measured tuples exact and says
// the window is incomplete because one generator had no joint evidence.
func TestJointEvidenceFleetMergeWithMixedGenerators(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	a := jointBatch(1, at, jointAggregate(jointKey("login", "east", "3g"), 10, 20))
	b := jointBatch(1, at, jointAggregate(jointKey("login", "east", "3g"), 30), jointAggregate(jointKey("login", "west", "wifi"), 5))
	combined, _, _ := aggregateDistributedMetricsBatches("run", map[string]generatorrpc.MetricsBatch{"a": a, "b": b})
	counts := map[string]int64{}
	for _, aggregate := range combined.JointMetrics {
		counts[aggregate.Name] = aggregate.Count
	}
	if counts[jointKey("login", "east", "3g")] != 3 || counts[jointKey("login", "west", "wifi")] != 1 || combined.JointInfo.Incomplete {
		t.Fatalf("fleet merge %v incomplete=%v", counts, combined.JointInfo.Incomplete)
	}
	old := generatorrpc.MetricsBatch{ActionMetricsVersion: 3, BatchSequence: 1, ExecutionGeneration: 1, TimestampUnixMs: at.UnixMilli(), WindowMs: 1000}
	combined, _, _ = aggregateDistributedMetricsBatches("run", map[string]generatorrpc.MetricsBatch{"a": a, "old": old})
	if combined.JointInfo == nil || !combined.JointInfo.Incomplete || combined.JointInfo.UnavailableSources != 1 {
		t.Fatalf("mixed fleet %+v", combined.JointInfo)
	}
}

func jointLiveHistory(start time.Time) []*LiveMetrics {
	publication := func(source string, sequence uint64, offset time.Duration, tuples map[string]storage.NativeIntervalMetric, joint *storage.NativeJointEvidence) *LiveMetrics {
		dims := map[string]map[string]storage.NativeIntervalMetric{storage.NativeJointDimension: tuples}
		return &LiveMetrics{Timestamp: start.Add(offset), NativeEvidence: &storage.NativeIntervalEvidence{Publication: &storage.NativePublicationEvidence{SourceID: source, Sequence: sequence, ExecutionGeneration: 1, Timestamp: start.Add(offset), Version: 3, WindowSeconds: 1, Dimensions: dims, ExpectedSources: []string{"a", "b"}, Joint: joint}}}
	}
	measured := func() *storage.NativeJointEvidence {
		return &storage.NativeJointEvidence{Population: "action", Dimensions: jointTestProjection.Dimensions, Availability: "measured"}
	}
	return []*LiveMetrics{
		publication("a", 1, time.Second, map[string]storage.NativeIntervalMetric{jointKey("login", "east", "3g"): nativeBucketMetric(10, 20), jointKey("login", "west", "3g"): nativeBucketMetric(40)}, measured()),
		publication("b", 1, time.Second, map[string]storage.NativeIntervalMetric{jointKey("login", "east", "wifi"): nativeBucketMetric(30), jointKey("search", "east", "3g"): nativeBucketMetric(500)}, measured()),
	}
}

func jointQuery() storage.AnalyticsQuery {
	q := nativeEvidenceQuery("live")
	q.LiveBucketMode, q.JointEvidence, q.Metric, q.Aggregation = "merged", true, "observation_count", "count"
	q.Dimensions, q.EndSeconds, q.BucketSeconds = []string{"name", "region"}, 2, 2
	return q
}

// A joint query groups by projected dimensions, summing the exact tuples
// over the unselected ones, across sources.
func TestJointEvidenceLiveQueryGroupsExactTuples(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	q := jointQuery()
	if err := storage.ValidateNativeEvidenceQuery(q); err != nil {
		t.Fatal(err)
	}
	out, err := buildNativeLiveEvidence(context.Background(), start, jointLiveHistory(start), q)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, series := range out.Series {
		counts[series.Name] = series.Points[0].Count
	}
	if counts[jointKey("login", "east")] != 3 || counts[jointKey("login", "west")] != 1 || counts[jointKey("search", "east")] != 1 || len(counts) != 3 {
		t.Fatalf("grouped tuples %v", counts)
	}
	q.Filters = map[string]string{"network_profile": "3g"}
	q.Dimensions = []string{"name"}
	out, err = buildNativeLiveEvidence(context.Background(), start, jointLiveHistory(start), q)
	if err != nil || len(out.Series) != 2 || out.Series[0].Name != jointKey("login") || out.Series[0].Points[0].Count != 3 {
		t.Fatalf("filtered %+v %v", out.Series, err)
	}
	// An unprojected dimension or another population is refused.
	q.Dimensions = []string{"group"}
	if _, err := buildNativeLiveEvidence(context.Background(), start, jointLiveHistory(start), q); err == nil || !strings.Contains(err.Error(), "does not project") {
		t.Fatalf("unprojected dimension: %v", err)
	}
	q = jointQuery()
	q.Population = "transaction"
	if _, err := buildNativeLiveEvidence(context.Background(), start, jointLiveHistory(start), q); err == nil {
		t.Fatal("another population was answered")
	}
}

// An incomplete or unavailable source window leaves the bucket a gap.
func TestJointEvidenceLiveQueryNeverGraphsPartialTotals(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*storage.NativeJointEvidence){
		"overflow": func(j *storage.NativeJointEvidence) { j.Incomplete, j.OmittedTuples = true, 12 },
		"unavailable": func(j *storage.NativeJointEvidence) {
			j.Availability, j.Reason = "unavailable", jointReasonSourceLacksCapability
		},
	} {
		history := jointLiveHistory(start)
		mutate(history[1].NativeEvidence.Publication.Joint)
		out, err := buildNativeLiveEvidence(context.Background(), start, history, jointQuery())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, series := range out.Series {
			if series.Points[0].Value != nil {
				t.Fatalf("%s: a partial total was graphed: %+v", name, series)
			}
		}
	}
	q := jointQuery()
	q.Dimensions = []string{"name", "region", "network_profile", "phase"}
	if storage.ValidateNativeEvidenceQuery(q) == nil {
		t.Fatal("four joint dimensions accepted")
	}
	q = jointQuery()
	q.LiveBucketMode = "source"
	if storage.ValidateNativeEvidenceQuery(q) == nil {
		t.Fatal("a joint query outside merged live buckets accepted")
	}
}

func TestJointEvidenceArchiveSidecar(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	history := jointLiveHistory(start)
	history = append(history, history[0]) // a republished publication counts once
	history[1].NativeEvidence.Publication.Joint.Other = &storage.NativeIntervalMetric{Count: 4}
	unavailable := &LiveMetrics{Timestamp: start.Add(2 * time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Publication: &storage.NativePublicationEvidence{SourceID: "old", Sequence: 1, ExecutionGeneration: 1, Timestamp: start.Add(2 * time.Second), WindowSeconds: 1, Joint: &storage.NativeJointEvidence{Population: "action", Dimensions: jointTestProjection.Dimensions, Availability: "unavailable", Reason: jointReasonSourceLacksCapability}}}}
	history = append(history, unavailable)
	rows := jointIntervalRows(history)
	if len(rows) != 6 {
		t.Fatalf("rows %d: %+v", len(rows), rows)
	}
	var others, missing int
	for _, row := range rows {
		if row.Other {
			others++
		}
		if row.Availability == "unavailable" {
			missing++
		}
	}
	if others != 1 || missing != 1 || rows[0].Values == nil || rows[0].DurationDigest == nil {
		t.Fatalf("rows %+v", rows)
	}

	root := t.TempDir()
	record := &storage.RunArchiveRecord{TestID: "joint-run", ArchivePath: root, ManifestPath: root + "/manifest.json"}
	admitArchiveForTest(t, record)
	manifest := &RunArchiveManifest{TestID: "joint-run", Provenance: RunArchiveProvenance{JointEvidence: jointTestProjection.Clone()}}
	tr := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{ID: "joint-run", History: history, historyTrimmed: 2}
	if err := tr.persistJointIntervalArchive(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.JointIntervalCount != 6 || !manifest.JointIntervalsTruncated || manifest.JointIntervalsParquetFile == "" {
		t.Fatalf("manifest %+v", manifest)
	}
	file, err := os.Open(filepath.Join(root, manifest.JointIntervalsFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	lines := 0
	for scanner := bufio.NewScanner(file); scanner.Scan(); lines++ {
		var row JointIntervalRecord
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil || row.SchemaVersion != jointIntervalSchemaVersion {
			t.Fatalf("row %s %v", scanner.Text(), err)
		}
	}
	if lines != 6 {
		t.Fatalf("lines %d", lines)
	}
	if info, err := os.Stat(filepath.Join(root, manifest.JointIntervalsParquetFile)); err != nil || info.Size() == 0 {
		t.Fatalf("parquet %v", err)
	}
	// A run without a projection writes nothing.
	plain := &RunArchiveManifest{TestID: "plain"}
	if err := tr.persistJointIntervalArchive(context.Background(), test, record, plain); err != nil || plain.JointIntervalsFile != "" {
		t.Fatalf("plain run %+v %v", plain, err)
	}
}

// The marginal dimensions' key caps make a window's marginal evidence
// incomplete, never its joint evidence; sample loss makes both incomplete.
func TestJointEvidenceQueryIgnoresMarginalKeyCaps(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	digest := nativeBucketMetric(10, 20).DurationDigest
	actions := map[string]*engine.ActionMetrics{}
	for i := 0; i < 200; i++ {
		actions[fmt.Sprintf("step-%03d", i)] = &engine.ActionMetrics{Count: 2, DurationDigest: digest}
	}
	snapshot := func(dropped int64) engine.MetricsSnapshot {
		s := engine.MetricsSnapshot{Timestamp: start.Add(time.Second), Window: time.Second, ByAction: actions,
			Joint: &engine.JointMetricsSnapshot{Version: 4, Population: "action", Dimensions: jointTestProjection.Dimensions, Tuples: map[string]*engine.ActionMetrics{jointKey("login", "east", "3g"): {Count: 2, DurationDigest: digest}}}}
		if dropped > 0 {
			s.Telemetry = &engine.TelemetryMetrics{DroppedResults: dropped}
		}
		return s
	}
	local := buildLiveMetricsFromSnapshot(snapshot(0), nil)
	if !local.NativeEvidence.Incomplete || local.NativeEvidence.Joint.Incomplete {
		t.Fatalf("marginal incomplete %v, joint incomplete %v", local.NativeEvidence.Incomplete, local.NativeEvidence.Joint.Incomplete)
	}
	out, err := buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{local}, jointQuery())
	if err != nil || len(out.Series) != 1 || out.Series[0].Points[0].Count != 2 {
		t.Fatalf("local joint query over 200 actions: %+v %v", out.Series, err)
	}
	lossy := buildLiveMetricsFromSnapshot(snapshot(3), nil)
	if !lossy.NativeEvidence.Joint.Incomplete {
		t.Fatal("dropped results left the joint evidence complete")
	}
	// Its only window is a gap, so there is no measured evidence to answer.
	if out, err = buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{lossy}, jointQuery()); err == nil {
		t.Fatalf("a lossy window was graphed: %+v", out.Series)
	}

	// A generator publication with 200 actions.
	batch := jointBatch(1, start.Add(time.Second), jointAggregate(jointKey("login", "east", "3g"), 10, 20))
	for name := range actions {
		batch.ActionMetrics = append(batch.ActionMetrics, jointAggregate(name, 5))
	}
	publication := nativePublicationEvidence("gen-a", batch, map[string]generatorrpc.MetricsBatch{"gen-a": batch}, jointTestProjection)
	if !publication.Incomplete || publication.Joint.Incomplete {
		t.Fatalf("publication marginal incomplete %v, joint incomplete %v", publication.Incomplete, publication.Joint.Incomplete)
	}
	point := &LiveMetrics{Timestamp: start.Add(time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Publication: publication}}
	out, err = buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{point}, jointQuery())
	if err != nil || len(out.Series) != 1 || out.Series[0].Points[0].Count != 2 {
		t.Fatalf("merged joint query over a 200-action publication: %+v %v", out.Series, err)
	}
	batch.Telemetry = &generatorrpc.MetricsTelemetry{DroppedResults: 1}
	if got := nativePublicationEvidence("gen-a", batch, nil, jointTestProjection).Joint; !got.Incomplete {
		t.Fatal("a generator's dropped results left its joint evidence complete")
	}
}

// A distributed history point keeps only the fleet's joint availability:
// merged queries and the archive read each publication's own tuples.
func TestJointEvidenceFleetPointKeepsNoTuples(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	batch := jointBatch(1, at, jointAggregate(jointKey("login", "east", "3g"), 10, 20))
	batch.JointInfo.Other = &generatorrpc.MetricAggregate{Name: "other", Count: 1}
	test := &ManagedTest{ID: "fleet", JointEvidence: jointTestProjection.Clone()}
	live := buildLiveMetricsFromDistributedBatch(test, batch, 1, nil)
	if live.NativeEvidence == nil || live.NativeEvidence.Joint == nil || live.NativeEvidence.Joint.Availability != "measured" {
		t.Fatalf("fleet joint availability %+v", live.NativeEvidence)
	}
	if _, ok := live.NativeEvidence.Dimensions[storage.NativeJointDimension]; ok || live.NativeEvidence.Joint.Other != nil {
		t.Fatalf("the fleet point kept joint tuples: %+v", live.NativeEvidence.Joint)
	}
}

func jointSidecarHistory(start time.Time, publications, tuples int) []*LiveMetrics {
	var history []*LiveMetrics
	for p := 0; p < publications; p++ {
		at := start.Add(time.Duration(p+1) * time.Second)
		values := map[string]storage.NativeIntervalMetric{}
		for i := 0; i < tuples; i++ {
			values[jointKey(fmt.Sprintf("step-%d", i), "east", "3g")] = nativeBucketMetric(float64(10+i), float64(20+p))
		}
		history = append(history, &LiveMetrics{Timestamp: at, NativeEvidence: &storage.NativeIntervalEvidence{Publication: &storage.NativePublicationEvidence{SourceID: "gen-a", Sequence: uint64(p + 1), ExecutionGeneration: 1, Timestamp: at, Version: 3, WindowSeconds: 1,
			Dimensions: map[string]map[string]storage.NativeIntervalMetric{storage.NativeJointDimension: values}, ExpectedSources: []string{"gen-a"},
			Joint: &storage.NativeJointEvidence{Population: "action", Dimensions: jointTestProjection.Dimensions, Availability: "measured"}}}})
	}
	return history
}

func jointSidecarRecord(t *testing.T, id string) (*storage.RunArchiveRecord, *RunArchiveManifest) {
	t.Helper()
	root := t.TempDir()
	record := &storage.RunArchiveRecord{TestID: id, ArchivePath: root, ManifestPath: root + "/manifest.json"}
	admitArchiveForTest(t, record)
	return record, &RunArchiveManifest{TestID: id, Provenance: RunArchiveProvenance{JointEvidence: jointTestProjection.Clone()}}
}

// A JSONL truncated at its budget still gets its Parquet copy (its own
// allowance), and a failed write leaves neither file nor manifest entry.
func TestJointEvidenceArchiveBudgetAndFailure(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	test := &ManagedTest{ID: "joint-budget", History: jointSidecarHistory(start, 100, 5)}
	tr := NewTestRunner(1, NewHub(), nil)
	full, fullManifest := jointSidecarRecord(t, "joint-budget")
	if err := tr.persistJointIntervalArchive(context.Background(), test, full, fullManifest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(full.ArchivePath, jointIntervalsFileName))
	if err != nil || fullManifest.JointIntervalCount != 500 {
		t.Fatalf("full sidecar %d rows: %v", fullManifest.JointIntervalCount, err)
	}
	t.Setenv(envArchiveJointIntervalsMaxBytes, strconv.FormatInt(info.Size()/2, 10))
	record, manifest := jointSidecarRecord(t, "joint-budget")
	if err := tr.persistJointIntervalArchive(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.JointIntervalsFile == "" || manifest.JointIntervalsParquetFile == "" || !manifest.JointIntervalsTruncated || manifest.JointIntervalCount == 0 || manifest.JointIntervalCount >= 500 {
		t.Fatalf("budgeted sidecar %+v", manifest)
	}
	if _, err := os.Stat(filepath.Join(record.ArchivePath, jointIntervalsParquetFileName)); err != nil {
		t.Fatal(err)
	}

	// The Parquet copy cannot be written: nothing is left behind.
	t.Setenv(envArchiveJointIntervalsMaxBytes, "")
	failed, failedManifest := jointSidecarRecord(t, "joint-budget")
	blocker := filepath.Join(failed.ArchivePath, jointIntervalsParquetFileName)
	if err := os.MkdirAll(filepath.Join(blocker, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := tr.persistJointIntervalArchive(context.Background(), test, failed, failedManifest); err == nil {
		t.Fatal("a failed Parquet copy was reported as written")
	}
	if _, err := os.Stat(filepath.Join(failed.ArchivePath, jointIntervalsFileName)); !os.IsNotExist(err) {
		t.Fatalf("the JSONL was left behind: %v", err)
	}
	if failedManifest.JointIntervalsFile != "" || failedManifest.JointIntervalCount != 0 || len(failedManifest.Artifacts) != 0 {
		t.Fatalf("a failed sidecar was declared: %+v", failedManifest)
	}
}

// Rows come from the persisted interval evidence, so a compacted terminal
// clone (or a reloaded run) still archives every publication.
func TestJointEvidenceArchiveReadsPersistedEvidence(t *testing.T) {
	server, store := baselineTestServer(t)
	comparisonArchiveFixture(t, server, store, "joint-stored", 100, 100)
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	history := jointSidecarHistory(start, 4, 3)
	for _, point := range history {
		if err := store.SaveHistory("joint-stored", point.Timestamp, point); err != nil {
			t.Fatal(err)
		}
	}
	record := server.runner.archives["joint-stored"]
	admitArchiveForTest(t, record)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Provenance.JointEvidence = jointTestProjection.Clone()
	compacted := *history[len(history)-1]
	compacted.NativeEvidence = nil
	clone := &ManagedTest{ID: "joint-stored", Status: "COMPLETED", History: []*LiveMetrics{&compacted}}
	if err := server.runner.persistJointIntervalArchive(context.Background(), clone, record, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.JointIntervalCount != 12 || manifest.JointIntervalsTruncated {
		t.Fatalf("persisted evidence gave %d rows, truncated %v", manifest.JointIntervalCount, manifest.JointIntervalsTruncated)
	}

	// A publication the retained history holds but the store lacks (its
	// history write failed) is added from the history, and the sidecar says
	// earlier ones may be missing too.
	unsaved := jointSidecarHistory(start.Add(time.Hour), 1, 2)[0]
	unsaved.NativeEvidence.Publication.Sequence = 99
	fresh := func() *RunArchiveManifest {
		m, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		m.Provenance.JointEvidence = jointTestProjection.Clone()
		m.JointIntervalsFile, m.JointIntervalsParquetFile = "", ""
		return m
	}
	withUnsaved := fresh()
	if err := server.runner.persistJointIntervalArchive(context.Background(), &ManagedTest{ID: "joint-stored", History: append(append([]*LiveMetrics(nil), history...), unsaved)}, record, withUnsaved); err != nil {
		t.Fatal(err)
	}
	if withUnsaved.JointIntervalCount != 14 || !withUnsaved.JointIntervalsTruncated {
		t.Fatalf("an unsaved publication gave %d rows, truncated %v", withUnsaved.JointIntervalCount, withUnsaved.JointIntervalsTruncated)
	}

	// The store-backed JSONL that reaches its allowance is truncated.
	info, err := os.Stat(filepath.Join(record.ArchivePath, jointIntervalsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveJointIntervalsMaxBytes, strconv.FormatInt(info.Size()/3, 10))
	budgeted := fresh()
	if err := server.runner.persistJointIntervalArchive(context.Background(), clone, record, budgeted); err != nil {
		t.Fatal(err)
	}
	if !budgeted.JointIntervalsTruncated || budgeted.JointIntervalCount == 0 || budgeted.JointIntervalCount >= 12 {
		t.Fatalf("budgeted store-backed sidecar %d rows, truncated %v", budgeted.JointIntervalCount, budgeted.JointIntervalsTruncated)
	}
}

func TestJointEvidenceCapabilityFromProbedFeatures(t *testing.T) {
	for _, tc := range []struct {
		features []string
		want     bool
	}{
		{[]string{generatorrpc.FeatureRunCorrelationV1, generatorrpc.FeatureJointEvidenceV4}, true},
		{[]string{strings.ToUpper(generatorrpc.FeatureJointEvidenceV4)}, true},
		{[]string{generatorrpc.FeatureRunCorrelationV1}, false},
		{nil, false},
	} {
		if got := generatorAcceptsJointEvidence(tc.features); got != tc.want {
			t.Fatalf("features %v: %v, want %v", tc.features, got, tc.want)
		}
	}
}
