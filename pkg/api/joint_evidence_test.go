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

// A local window's persisted copy keeps its time at the store's resolution
// (PostgreSQL: microseconds) while the retained copy keeps nanoseconds: they
// are one window, archived once.
func TestJointEvidenceArchiveMatchesLocalWindowsAtStoreResolution(t *testing.T) {
	server, store := baselineTestServer(t)
	comparisonArchiveFixture(t, server, store, "joint-local", 100, 100)
	at := time.Date(2026, 10, 8, 12, 0, 1, 123456789, time.UTC)
	local := func(timestamp time.Time) *LiveMetrics {
		return &LiveMetrics{Timestamp: timestamp, NativeEvidence: &storage.NativeIntervalEvidence{Version: 3, WindowSeconds: 1,
			Dimensions: map[string]map[string]storage.NativeIntervalMetric{storage.NativeJointDimension: {jointKey("login", "east", "3g"): nativeBucketMetric(10, 20)}},
			Joint:      &storage.NativeJointEvidence{Population: "action", Dimensions: jointTestProjection.Dimensions, Availability: "measured"}}}
	}
	if err := store.SaveHistory("joint-local", at.Truncate(time.Microsecond), local(at.Truncate(time.Microsecond))); err != nil {
		t.Fatal(err)
	}
	record := server.runner.archives["joint-local"]
	admitArchiveForTest(t, record)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Provenance.JointEvidence = jointTestProjection.Clone()
	if err := server.runner.persistJointIntervalArchive(context.Background(), &ManagedTest{ID: "joint-local", History: []*LiveMetrics{local(at)}}, record, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.JointIntervalCount != 1 || manifest.JointIntervalsTruncated {
		t.Fatalf("local window archived %d times, truncated %v", manifest.JointIntervalCount, manifest.JointIntervalsTruncated)
	}
}

// A distributed run publishes at most once a second, so most accepted
// batches are only stored: each keeps its joint evidence until the next
// installed point carries it, and the archive and the merged live query see
// every source window (a restarted generator's windows too, by epoch).
func TestJointEvidenceKeepsEveryAcceptedBatch(t *testing.T) {
	runner, run, backend := newDistributedThresholdTestRunner(t)
	run.JointEvidence = jointTestProjection.Clone()
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	batch := func(source, epoch string, sequence uint64) generatorrpc.MetricsBatch {
		b := jointBatch(sequence, start.Add(time.Second), jointAggregate(jointKey("login", source, "3g"), 10, 20))
		b.SourceEpoch, b.SourceEpochStartedUnixMs = epoch, start.UnixMilli()
		return b
	}
	stored := []struct {
		target string
		batch  generatorrpc.MetricsBatch
	}{
		{"gen-a", batch("gen-a", "a1", 1)},
		{"gen-b", batch("gen-b", "b1", 1)},
		{"gen-c", batch("gen-c", "c1", 1)},
		// gen-a restarted: a new epoch restarts its sequence.
		{"gen-a", func() generatorrpc.MetricsBatch {
			b := batch("gen-a", "a2", 1)
			b.TimestampUnixMs, b.SourceEpochStartedUnixMs = start.Add(2*time.Second).UnixMilli(), start.Add(1500*time.Millisecond).UnixMilli()
			return b
		}()},
	}
	for _, s := range stored {
		if !backend.storeRemoteMetricsBatchOnly(runner, run, s.target, s.batch) {
			t.Fatalf("%s rejected", s.target)
		}
	}
	runner.mu.Lock()
	if len(run.pendingJointPublications) != 4 {
		runner.mu.Unlock()
		t.Fatalf("pending %d", len(run.pendingJointPublications))
	}
	// gen-c's batch is the one published; the point carries the others.
	published := nativePublicationEvidence("gen-c", stored[2].batch, map[string]generatorrpc.MetricsBatch{"gen-a": stored[3].batch, "gen-b": stored[1].batch, "gen-c": stored[2].batch}, jointTestProjection)
	others := takePendingJointPublicationsLocked(run, published)
	left := len(run.pendingJointPublications)
	runner.mu.Unlock()
	if len(others) != 3 || left != 0 {
		t.Fatalf("carried %d, left %d", len(others), left)
	}
	point := &LiveMetrics{Timestamp: start.Add(time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Publication: published, JointPublications: others}}
	rows := jointIntervalRows([]*LiveMetrics{point})
	epochs := map[string]bool{}
	for _, row := range rows {
		epochs[row.SourceID+"/"+row.SourceEpoch] = true
	}
	if len(rows) != 4 || len(epochs) != 4 {
		t.Fatalf("archived %d rows from %v", len(rows), epochs)
	}
	q := jointQuery()
	q.Dimensions = []string{"name"}
	out, err := buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{point}, q)
	if err != nil || len(out.Series) != 1 || out.Series[0].Points[0].Count != 8 {
		t.Fatalf("merged live joint query %+v %v", out.Series, err)
	}
}

// Batches accepted after the last published point are archived from the
// pending list; any dropped past its bound make the sidecar truncated.
func TestJointEvidenceArchiveIncludesUnpublishedBatches(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	pending := []*storage.NativePublicationEvidence{
		jointPublicationEvidence("gen-a", jointBatch(7, start, jointAggregate(jointKey("login", "east", "3g"), 10)), jointTestProjection),
		jointPublicationEvidence("gen-b", jointBatch(9, start, jointAggregate(jointKey("login", "west", "3g"), 10)), jointTestProjection),
	}
	tr := NewTestRunner(1, NewHub(), nil)
	record, manifest := jointSidecarRecord(t, "joint-pending")
	test := &ManagedTest{ID: "joint-pending", pendingJointPublications: pending}
	if err := tr.persistJointIntervalArchive(context.Background(), test, record, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.JointIntervalCount != 2 {
		t.Fatalf("pending batches archived %d rows", manifest.JointIntervalCount)
	}
}

// With the persisted evidence available, carried batches still held at
// finalize complete the sidecar; batches dropped past the bound mark it
// truncated.
func TestJointEvidenceArchiveTruncatesDroppedBatches(t *testing.T) {
	server, store := baselineTestServer(t)
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		id      string
		dropped int64
	}{{"joint-pending-kept", 0}, {"joint-pending-dropped", 3}} {
		comparisonArchiveFixture(t, server, store, tc.id, 100, 100)
		record := server.runner.archives[tc.id]
		admitArchiveForTest(t, record)
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Provenance.JointEvidence = jointTestProjection.Clone()
		test := &ManagedTest{ID: tc.id, jointPublicationsDropped: tc.dropped, pendingJointPublications: []*storage.NativePublicationEvidence{
			jointPublicationEvidence("gen-a", jointBatch(7, start, jointAggregate(jointKey("login", "east", "3g"), 10)), jointTestProjection),
		}}
		if err := server.runner.persistJointIntervalArchive(context.Background(), test, record, manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.JointIntervalCount != 1 || manifest.JointIntervalsTruncated != (tc.dropped > 0) {
			t.Fatalf("%s: %d rows, truncated %v", tc.id, manifest.JointIntervalCount, manifest.JointIntervalsTruncated)
		}
	}
}

// Through the real publication path, the installed point carries the joint
// evidence of the batches accepted since the previous point (but not its
// own), and the pending list is emptied.
func TestJointEvidencePublishedPointCarriesPendingBatches(t *testing.T) {
	runner, run, backend := newDistributedThresholdTestRunner(t)
	run.JointEvidence = jointTestProjection.Clone()
	runner.mu.Lock()
	run.remoteClientsByTarget["gen-a"], run.remoteClientsByTarget["gen-b"] = nil, nil
	delete(run.remoteClientsByTarget, "generator-a")
	runner.mu.Unlock()
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, target := range []string{"gen-a", "gen-b"} {
		b := distributedP99Batch(run.ID, start.Add(time.Second).UnixMilli(), 20)
		joint := jointBatch(1, start.Add(time.Second), jointAggregate(jointKey("login", target, "3g"), 10, 20))
		b.ActionMetricsVersion, b.BatchSequence, b.ExecutionGeneration, b.JointMetrics, b.JointInfo = joint.ActionMetricsVersion, 1, joint.ExecutionGeneration, joint.JointMetrics, joint.JointInfo
		b.SourceEpoch, b.SourceEpochStartedUnixMs = target+"-epoch", start.UnixMilli()
		if !backend.storeRemoteMetricsBatchOnly(runner, run, target, b) {
			t.Fatalf("%s rejected", target)
		}
	}
	live, _, _, fresh, publication := backend.applyStoredRemoteMetricsBatch(runner, run, "gen-b")
	if !fresh || live == nil {
		t.Fatalf("the stored batch was not accepted: fresh %v", fresh)
	}
	backend.publishDistributedMetricsRevision(context.Background(), runner, run, live, publication)
	runner.mu.RLock()
	installed, left := run.LiveMetrics, len(run.pendingJointPublications)
	runner.mu.RUnlock()
	if installed == nil || installed.NativeEvidence == nil {
		t.Fatal("no published point with native evidence was installed")
	}
	var carried []string
	for _, p := range installed.NativeEvidence.JointPublications {
		carried = append(carried, p.SourceID)
	}
	if left != 0 || len(carried) != 1 || carried[0] != "gen-a" {
		t.Fatalf("published point carried %v (own %+v), %d left pending", carried, installed.NativeEvidence.Publication, left)
	}
}

// The pending list is bounded by count and by estimated size; the oldest
// batches go first and are counted.
func TestJointEvidencePendingBatchesAreBounded(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	test := &ManagedTest{ID: "bounded", JointEvidence: jointTestProjection.Clone()}
	for i := 1; i <= maxPendingJointPublications+5; i++ {
		rememberJointPublicationLocked(test, "gen-a", jointBatch(uint64(i), start.Add(time.Duration(i)*time.Second), jointAggregate(jointKey("login", "east", "3g"), 10)))
	}
	if len(test.pendingJointPublications) != maxPendingJointPublications || test.jointPublicationsDropped != 5 || test.pendingJointPublications[0].Sequence != 6 {
		t.Fatalf("count bound: %d pending, %d dropped, oldest %d", len(test.pendingJointPublications), test.jointPublicationsDropped, test.pendingJointPublications[0].Sequence)
	}
	var sum int64
	for _, p := range test.pendingJointPublications {
		sum += jointPublicationBytes(p)
	}
	if sum != test.pendingJointBytes {
		t.Fatalf("size accounting %d, want %d", test.pendingJointBytes, sum)
	}

	previous := maxPendingJointBytes
	t.Cleanup(func() { maxPendingJointBytes = previous })
	one := jointPublicationEvidence("gen-a", jointBatch(1, start, jointAggregate(jointKey("login", "east", "3g"), 10)), jointTestProjection)
	maxPendingJointBytes = 3 * jointPublicationBytes(one)
	sized := &ManagedTest{ID: "sized", JointEvidence: jointTestProjection.Clone()}
	for i := 1; i <= 5; i++ {
		rememberJointPublicationLocked(sized, "gen-a", jointBatch(uint64(i), start.Add(time.Duration(i)*time.Second), jointAggregate(jointKey("login", "east", "3g"), 10)))
	}
	if len(sized.pendingJointPublications) != 3 || sized.jointPublicationsDropped != 2 || sized.pendingJointBytes > maxPendingJointBytes {
		t.Fatalf("size bound: %d pending (%d bytes), %d dropped", len(sized.pendingJointPublications), sized.pendingJointBytes, sized.jointPublicationsDropped)
	}
	if taken := takePendingJointPublicationsLocked(sized, nil); len(taken) != 3 || sized.pendingJointBytes != 0 {
		t.Fatalf("taking the pending batches left %d bytes", sized.pendingJointBytes)
	}
}

// A batch carried by one point that a later point publishes as its own (a
// delayed flush) is one window: counted once live and archived once.
func TestJointEvidenceCarriedBatchRepublishedOnce(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	batches := map[string]generatorrpc.MetricsBatch{
		"gen-a": jointBatch(4, start.Add(time.Second), jointAggregate(jointKey("login", "east", "3g"), 10, 20)),
		"gen-b": jointBatch(9, start.Add(1500*time.Millisecond), jointAggregate(jointKey("login", "west", "3g"), 10, 20, 30)),
	}
	ownA := nativePublicationEvidence("gen-a", batches["gen-a"], batches, jointTestProjection)
	ownB := nativePublicationEvidence("gen-b", batches["gen-b"], batches, jointTestProjection)
	carriedB := jointPublicationEvidence("gen-b", batches["gen-b"], jointTestProjection)
	history := []*LiveMetrics{
		{Timestamp: start.Add(time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Publication: ownA, JointPublications: []*storage.NativePublicationEvidence{carriedB}}},
		{Timestamp: start.Add(1500 * time.Millisecond), NativeEvidence: &storage.NativeIntervalEvidence{Publication: ownB}},
	}
	q := jointQuery()
	q.Dimensions = []string{"name"}
	out, err := buildNativeLiveEvidence(context.Background(), start, history, q)
	if err != nil || len(out.Series) != 1 || out.Series[0].Points[0].Count != 5 {
		t.Fatalf("merged live joint query %+v %v", out.Series, err)
	}
	if rows := jointIntervalRows(history); len(rows) != 2 {
		t.Fatalf("archived %d rows, want one per window", len(rows))
	}
}

// A distributed run (its manifest names generator sources) that this
// controller did not hold from its start cannot know what another controller
// held for its next point: its sidecar is truncated, even with no rows. A run
// held from its start, or a local run, is not. The batches stay held until
// the archive is committed.
func TestJointEvidenceArchiveAfterAdoption(t *testing.T) {
	server, store := baselineTestServer(t)
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		id                         string
		distributed, held, pending bool
		rows                       int64
		truncated                  bool
	}{
		{"joint-adopted-distributed", true, false, true, 1, true},
		{"joint-adopted-no-rows", true, false, false, 0, true},
		{"joint-held-distributed", true, true, true, 1, false},
		{"joint-local", false, false, false, 1, false},
	} {
		comparisonArchiveFixture(t, server, store, tc.id, 100, 100)
		record := server.runner.archives[tc.id]
		admitArchiveForTest(t, record)
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Provenance.JointEvidence = jointTestProjection.Clone()
		if tc.distributed {
			manifest.SourceCoverage = &RunArchiveSourceCoverage{Mode: "distributed", Sources: []RunArchiveSource{{Target: "gen-a", Collected: true}}}
		}
		test := &ManagedTest{ID: tc.id, jointHeldFromStart: tc.held}
		if tc.pending {
			test.pendingJointPublications = []*storage.NativePublicationEvidence{
				jointPublicationEvidence("gen-a", jointBatch(7, start, jointAggregate(jointKey("login", "east", "3g"), 10)), jointTestProjection),
			}
		} else if tc.rows > 0 {
			test.History = []*LiveMetrics{{Timestamp: start, NativeEvidence: &storage.NativeIntervalEvidence{Version: 3, WindowSeconds: 1,
				Dimensions: map[string]map[string]storage.NativeIntervalMetric{storage.NativeJointDimension: {jointKey("login", "east", "3g"): nativeBucketMetric(10, 20)}},
				Joint:      &storage.NativeJointEvidence{Population: "action", Dimensions: jointTestProjection.Dimensions, Availability: "measured"}}}}
			if err := store.SaveHistory(tc.id, start, test.History[0]); err != nil {
				t.Fatal(err)
			}
		}
		if err := server.runner.persistJointIntervalArchive(context.Background(), test, record, manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.JointIntervalCount != tc.rows || manifest.JointIntervalsTruncated != tc.truncated {
			t.Fatalf("%s: %d rows, truncated %v", tc.id, manifest.JointIntervalCount, manifest.JointIntervalsTruncated)
		}
		if tc.pending && len(test.pendingJointPublications) != 1 {
			t.Fatalf("%s: the batches were released before the archive was committed", tc.id)
		}
	}
	// Committing the archive releases the batches held for it and counts them
	// as dropped, so a sidecar written only later (its first write failed,
	// then a refresh) is marked truncated.
	comparisonArchiveFixture(t, server, store, "joint-commit", 100, 100)
	record := server.runner.archives["joint-commit"]
	admitArchiveForTest(t, record)
	run := &ManagedTest{ID: "joint-commit", Status: "COMPLETE", jointHeldFromStart: true, pendingJointBytes: 300,
		pendingJointPublications: []*storage.NativePublicationEvidence{jointPublicationEvidence("gen-a", jointBatch(7, start, jointAggregate(jointKey("login", "east", "3g"), 10)), jointTestProjection)}}
	server.runner.mu.Lock()
	server.runner.tests[run.ID] = run
	server.runner.mu.Unlock()
	server.runner.attachFinalizedRunArchive(run.ID, record, nil)
	server.runner.mu.RLock()
	released := run.pendingJointPublications == nil && run.pendingJointBytes == 0 && run.jointPublicationsDropped == 1
	server.runner.mu.RUnlock()
	if !released {
		t.Fatal("committing the archive kept or did not count the batches")
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Provenance.JointEvidence = jointTestProjection.Clone()
	if err := server.runner.persistJointIntervalArchive(context.Background(), run, record, manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.JointIntervalsTruncated {
		t.Fatal("a sidecar written after the held batches were released is not marked truncated")
	}
	// A run that held nothing at commit (a local run) releases nothing, so a
	// sidecar written later is complete.
	comparisonArchiveFixture(t, server, store, "joint-commit-local", 100, 100)
	localRecord := server.runner.archives["joint-commit-local"]
	admitArchiveForTest(t, localRecord)
	local := &ManagedTest{ID: "joint-commit-local", Status: "COMPLETE"}
	server.runner.mu.Lock()
	server.runner.tests[local.ID] = local
	server.runner.mu.Unlock()
	server.runner.attachFinalizedRunArchive(local.ID, localRecord, nil)
	localManifest, err := loadRunArchiveManifestFromRecord(localRecord)
	if err != nil {
		t.Fatal(err)
	}
	localManifest.Provenance.JointEvidence = jointTestProjection.Clone()
	if err := server.runner.persistJointIntervalArchive(context.Background(), local, localRecord, localManifest); err != nil {
		t.Fatal(err)
	}
	if local.jointPublicationsDropped != 0 || localManifest.JointIntervalsTruncated {
		t.Fatalf("a run that held nothing: dropped %d, truncated %v", local.jointPublicationsDropped, localManifest.JointIntervalsTruncated)
	}
}

// A distributed start whose RUNNING save is refused (the lease moved to a
// successor) rolls back, and the run is not held from its start.
func TestJointEvidenceDistributedStartRollbackClearsTheHold(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	leadership := newExecutionLeadership(store, "joint-controller")
	leadership.tick()
	runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(leadership))
	token := leadership.executionFenceToken()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{ID: "joint-rollback", Name: "Joint rollback", Status: "INITIALIZING", ProjectID: storage.DefaultProjectID, CreatedAt: time.Now().UTC(),
		Groups: []TestGroup{{Name: "buyers", VUsers: 1}}, executionCtx: ctx, cancel: cancel, executionFence: token}
	if err := runner.saveManagedTestRecord(test); err != nil {
		t.Fatalf("seed: %v", err)
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	if err := store.ReleaseSchedulerLease(executionLeaseName, token); err != nil {
		t.Fatal(err)
	}
	successor := newExecutionLeadership(store, "joint-successor")
	successor.tick()
	defer successor.relinquish()
	backend := &distributedExecutionBackend{}
	started, _, err := backend.prepareDistributedTestRun(runner, test, nil, map[string]generatorClient{"gen-a": nil}, nil)
	if started || err == nil {
		t.Fatalf("a fenced start was accepted: started %v err %v", started, err)
	}
	runner.mu.RLock()
	held := test.jointHeldFromStart
	runner.mu.RUnlock()
	if held {
		t.Fatal("a rolled-back start left the run held from its start")
	}
}

// A run's history reached the store's retention cap (its oldest rows may be
// trimmed): both distribution sidecars read from that store say so.
func TestJointEvidenceArchiveTruncatedAtTheHistoryCap(t *testing.T) {
	server, store := baselineTestServer(t)
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	comparisonArchiveFixture(t, server, store, "joint-capped", 100, 100)
	record := server.runner.archives["joint-capped"]
	admitArchiveForTest(t, record)
	evidence := &storage.NativeIntervalEvidence{Version: 3, WindowSeconds: 1,
		Dimensions: map[string]map[string]storage.NativeIntervalMetric{storage.NativeJointDimension: {jointKey("login", "east", "3g"): nativeBucketMetric(10, 20)}, "transaction": {"Purchase": nativeBucketMetric(10, 20)}},
		Joint:      &storage.NativeJointEvidence{Population: "action", Dimensions: jointTestProjection.Dimensions, Availability: "measured"}}
	if err := store.SaveHistory("joint-capped", start, &LiveMetrics{Timestamp: start, NativeEvidence: evidence}); err != nil {
		t.Fatal(err)
	}
	persist := func(test *ManagedTest) *RunArchiveManifest {
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Provenance.JointEvidence = jointTestProjection.Clone()
		if err := server.runner.persistJointIntervalArchive(context.Background(), test, record, manifest); err != nil {
			t.Fatal(err)
		}
		return manifest
	}
	if manifest := persist(&ManagedTest{ID: "joint-capped"}); manifest.JointIntervalsTruncated {
		t.Fatal("a run below the cap was marked truncated")
	}
	server.runner.testStore = sqliteOverlay(cappedHistoryStore{store})
	manifest := persist(&ManagedTest{ID: "joint-capped"})
	if !manifest.JointIntervalsTruncated {
		t.Fatal("a run at the history cap was not marked truncated")
	}
	if err := server.runner.persistIntervalDistributionArchive(context.Background(), "joint-capped", record, manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.IntervalDistributionCount == 0 || !manifest.IntervalDistributionsTruncated {
		t.Fatalf("interval distributions at the history cap: %d, truncated %v", manifest.IntervalDistributionCount, manifest.IntervalDistributionsTruncated)
	}
}

// cappedHistoryStore reports every run's history as having reached the cap.
type cappedHistoryStore struct{ SQLiteStore *storage.SQLiteStore }

func (cappedHistoryStore) NativeIntervalEvidenceTrimmed(context.Context, string) (bool, error) {
	return true, nil
}

// Starting a distributed run marks it held from its start.
func TestJointEvidenceDistributedStartHoldsTheRun(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	test := &ManagedTest{ID: "joint-start", Status: "INITIALIZING", Groups: []TestGroup{{Name: "buyers", VUsers: 1}}, executionCtx: ctx, cancel: cancel}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	backend := &distributedExecutionBackend{}
	started, _, err := backend.prepareDistributedTestRun(runner, test, nil, map[string]generatorClient{"gen-a": nil}, nil)
	if err != nil || !started {
		t.Fatalf("start: %v %v", started, err)
	}
	runner.mu.RLock()
	held := test.jointHeldFromStart
	runner.mu.RUnlock()
	if !held {
		t.Fatal("a started distributed run is not held from its start")
	}
}
