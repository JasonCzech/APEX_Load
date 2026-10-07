package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A fake dataset store: ListAnalysisImports pages through rows in order.
type rcaDatasetStore struct{ rows []storage.AnalysisImport }

func (s *rcaDatasetStore) GetAnalysisImport(id string) (*storage.AnalysisImport, error) {
	for i := range s.rows {
		if s.rows[i].ID == id {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}
func (s *rcaDatasetStore) ListAnalysisImports(project string, limit, offset int) ([]storage.AnalysisImport, error) {
	if offset >= len(s.rows) {
		return nil, nil
	}
	return s.rows[offset:min(len(s.rows), offset+limit)], nil
}
func (s *rcaDatasetStore) SaveAnalysisImport(in storage.AnalysisImport, _ int64) (*storage.AnalysisImport, error) {
	return &in, nil
}

func rcaDatasetFixture(id, project, run, state string, revision int64) storage.AnalysisImport {
	return storage.AnalysisImport{ID: id, ProjectID: project, RunID: run, State: state, Revision: revision,
		PartitionManifest: &analysis.NormalizedPartitionManifest{MappingSHA256: strings.Repeat("a", 64), Digest: strings.Repeat(id[len(id)-1:], 64)}}
}

func TestRCAEvidenceFreezesOnlyTheRunsAssociatedReadyDatasets(t *testing.T) {
	store := &rcaDatasetStore{rows: []storage.AnalysisImport{
		rcaDatasetFixture("ds-c", "p", "run-1", "ready", 3),
		rcaDatasetFixture("ds-a", "p", "run-1", "ready", 7),
		rcaDatasetFixture("ds-b", "p", "run-2", "ready", 1),       // another run
		rcaDatasetFixture("ds-d", "other", "run-1", "ready", 1),   // another project
		rcaDatasetFixture("ds-e", "p", "run-1", "normalizing", 1), // not ready
		rcaDatasetFixture("ds-f", "p", "", "ready", 1),            // unassociated
	}}
	noManifest := rcaDatasetFixture("ds-g", "p", "run-1", "ready", 1)
	noManifest.PartitionManifest = nil
	store.rows = append(store.rows, noManifest)
	found, truncated, err := loadRCADatasets(store, "p", "run-1")
	if err != nil || truncated || len(found) != 2 || found[0].DatasetID != "ds-a" || found[0].Revision != 7 || found[1].DatasetID != "ds-c" || found[1].PartitionDigest != strings.Repeat("c", 64) {
		t.Fatalf("found %+v truncated %t err %v", found, truncated, err)
	}
	// More associations than an analysis reads are cut in ID order.
	store.rows = nil
	for i := 0; i < rcaMaxImportedDatasets+3; i++ {
		store.rows = append(store.rows, rcaDatasetFixture("ds-"+string(rune('z'-i)), "p", "run-1", "ready", 1))
	}
	found, truncated, _ = loadRCADatasets(store, "p", "run-1")
	if !truncated || len(found) != rcaMaxImportedDatasets || found[0].DatasetID != "ds-"+string(rune('z'-rcaMaxImportedDatasets-2)) {
		t.Fatalf("truncation %t %+v", truncated, found)
	}
	if none, _, err := loadRCADatasets(struct{}{}, "p", "run-1"); err != nil || none != nil {
		t.Fatal("a store without datasets has none")
	}

	// A new association or dataset revision is new evidence.
	base := rcaEvidence{ArchiveDigest: "a", MappingDigest: "m", Datasets: []rcaEvidenceDataset{{DatasetID: "ds-a", Revision: 7}}}
	if !sameEvidence(base, base) {
		t.Fatal("identical evidence not reused")
	}
	for name, change := range map[string]func(*rcaEvidence){
		"revision":   func(e *rcaEvidence) { e.Datasets = []rcaEvidenceDataset{{DatasetID: "ds-a", Revision: 8}} },
		"added":      func(e *rcaEvidence) { e.Datasets = append(e.Datasets, rcaEvidenceDataset{DatasetID: "ds-b"}) },
		"removed":    func(e *rcaEvidence) { e.Datasets = nil },
		"truncation": func(e *rcaEvidence) { e.DatasetsTruncated = true },
	} {
		current := base
		current.Datasets = append([]rcaEvidenceDataset{}, base.Datasets...)
		change(&current)
		if sameEvidence(base, current) {
			t.Fatalf("%s: reused", name)
		}
	}
}

func TestImportedSeriesKeysAndWindow(t *testing.T) {
	at := func(seconds int) *time.Time { v := time.Unix(1700000000+int64(seconds), 0).UTC(); return &v }
	metric, other := "cpu", "memory"
	identity := func(host string) *analysis.NormalizedPartitionIdentity {
		return &analysis.NormalizedPartitionIdentity{Source: "src", Host: host, Dimensions: map[string]string{"zone": "a"}}
	}
	manifest := &analysis.NormalizedPartitionManifest{Partitions: []analysis.NormalizedPartition{
		{Stats: analysis.NormalizedPartitionStats{Kind: analysis.KindGauge, Metric: &metric, Identity: identity("h2"), MinTime: at(0), MaxTime: at(50)}},
		{Stats: analysis.NormalizedPartitionStats{Kind: analysis.KindGauge, Metric: &metric, Identity: identity("h1"), MinTime: at(0), MaxTime: at(50)}},
		{Stats: analysis.NormalizedPartitionStats{Kind: analysis.KindGauge, Metric: &metric, Identity: identity("h1"), MinTime: at(60), MaxTime: at(90)}}, // same series
		{Stats: analysis.NormalizedPartitionStats{Kind: analysis.KindCounter, Metric: &other, Identity: identity("h1"), MinTime: at(500), MaxTime: at(600)}}, // outside
		{Stats: analysis.NormalizedPartitionStats{Kind: analysis.KindGauge, Identity: identity("h3"), MinTime: at(0), MaxTime: at(10)}},                   // mixed metrics
		{Stats: analysis.NormalizedPartitionStats{Kind: analysis.KindGauge, Metric: &metric, MinTime: at(0), MaxTime: at(10)}},                           // mixed identities
	}}
	keys, mixed := importedSeriesKeys(manifest, *at(0), *at(100))
	if mixed != 2 || len(keys) != 2 || keys[0].identity.Host != "h1" || keys[1].identity.Host != "h2" || keys[0].metric != "cpu" || keys[0].kind != analysis.KindGauge {
		t.Fatalf("keys %+v mixed %d", keys, mixed)
	}

	if _, _, _, ok := rcaImportedWindow(nil); ok {
		t.Fatal("a run without intervals has no window")
	}
	var summaries []IntervalSummaryRecord
	start := time.Unix(1700000000, 0).UTC()
	for i := 0; i < 10; i++ {
		summaries = append(summaries, IntervalSummaryRecord{Timestamp: start.Add(time.Duration(i) * 5 * time.Second)})
	}
	from, to, bucket, ok := rcaImportedWindow(summaries)
	if !ok || bucket != 5*time.Second || !from.Equal(start.Add(-5*time.Second)) || !to.Equal(start.Add(50*time.Second)) {
		t.Fatalf("window %s..%s by %s", from, to, bucket)
	}
	// A window longer than the point bound widens its grid.
	long := []IntervalSummaryRecord{{Timestamp: start}, {Timestamp: start.Add(time.Second)}, {Timestamp: start.Add(20000 * time.Second)}}
	if from, to, bucket, _ := rcaImportedWindow(long); to.Sub(from)/bucket > analysis.MaxSeriesPoints || bucket < 2*time.Second {
		t.Fatalf("long window %s..%s by %s", from, to, bucket)
	}
}

func TestRCAImportedSeriesReportsWhatItCannotRead(t *testing.T) {
	server, _ := rcaTestServer(t)
	summaries := []IntervalSummaryRecord{{Timestamp: exemplarFixtureStart}, {Timestamp: exemplarFixtureStart.Add(10 * time.Second)}}
	frozen := []rcaEvidenceDataset{{DatasetID: "missing", Revision: 1}}
	budgeter := rca.NewBudgeter(rca.DefaultBudget, nil, nil)
	// Native analysis off for the project: nothing is read, and it says so.
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "")
	series, limitations, err := server.rcaImportedSeries(context.Background(), storage.DefaultProjectID, "rca-run", frozen, summaries, -1, budgeter)
	if err != nil || len(series) != 0 || limitationCodes(limitations) != limitationImportedDatasetUnavailable {
		t.Fatalf("%+v %+v %v", series, limitations, err)
	}
	// A frozen dataset that no longer matches is reported, never read.
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", storage.DefaultProjectID)
	series, limitations, err = server.rcaImportedSeries(context.Background(), storage.DefaultProjectID, "rca-run", frozen, summaries, -1, budgeter)
	if err != nil || len(series) != 0 || limitationCodes(limitations) != limitationImportedDatasetChanged {
		t.Fatalf("%+v %+v %v", series, limitations, err)
	}
	// Without intervals there is no window to read.
	if _, limitations, _ := server.rcaImportedSeries(context.Background(), storage.DefaultProjectID, "rca-run", frozen, nil, -1, budgeter); limitationCodes(limitations) != limitationImportedDatasetUnavailable {
		t.Fatalf("%+v", limitations)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := server.rcaImportedSeries(cancelled, storage.DefaultProjectID, "rca-run", frozen, summaries, -1, budgeter); err == nil {
		t.Fatal("cancellation was not reported")
	}
}

func TestRCASeriesIdentitiesAreMeasurementIdentities(t *testing.T) {
	point := analysis.MetricPoint{Source: "dt-1", MetricName: "builtin:host.cpu.usage", Kind: analysis.KindGauge, Unit: "%", Dimensions: map[string]string{"host": "app-1"},
		Provider: &analysis.ProviderMeasurement{Name: monitorprovider.ProviderDynatrace, MetricID: "builtin:host.cpu.usage", ResolutionSeconds: 60, Statistic: "avg", EntityIDs: map[string]string{"dt.entity.host": "HOST-1"}}}
	first, ok := monitorSeriesTemplate(point, "project-a", "monitor_points.jsonl")
	if !ok || len(first.Identity) != 64 || !strings.HasSuffix(first.ID, "/"+first.Identity[:12]) {
		t.Fatalf("%+v", first)
	}
	// Entity IDs are nodes, not identity; the run's project fills a point
	// recorded without one; a rebuilt template is identical (restart).
	point.Provider.EntityIDs = map[string]string{"dt.entity.host": "HOST-1", "dt.entity.process_group": "PG-2"}
	if again, _ := monitorSeriesTemplate(point, "project-a", "monitor_points.jsonl"); again.ID != first.ID || again.Identity != first.Identity {
		t.Fatalf("entity IDs split the series: %s vs %s", again.ID, first.ID)
	}
	point.ProjectID = "project-a"
	if explicit, _ := monitorSeriesTemplate(point, "ignored", "monitor_points.jsonl"); explicit.Identity != first.Identity {
		t.Fatal("an explicit project changed the identity")
	}
	measured := point
	measured.SchemaVersion, measured.TimestampUTC, measured.Quality = analysis.SchemaVersion, time.Unix(1700000040, 0).UTC(), analysis.QualityValid
	value := 42.0
	measured.Value = &value
	measured.Provider = &analysis.ProviderMeasurement{Name: monitorprovider.ProviderDynatrace, MetricID: "builtin:host.cpu.usage", ResolutionSeconds: 60, Statistic: "avg",
		BucketStart: measured.TimestampUTC, BucketEnd: measured.TimestampUTC.Add(time.Minute)}
	if identity, err := analysis.MeasurementSeriesIdentity(measured, analysis.SeriesObservation{Mode: rca.ObservationInterval, BucketNanos: int64(time.Minute)}); err != nil || identity.SHA256 != first.Identity {
		t.Fatalf("RCA identity %s differs from the measurement identity %+v (%v)", first.Identity, identity, err)
	}
	// Another host is another series; a point without a recorded metric ID
	// keeps the archive.2 key and no identity.
	point.Dimensions = map[string]string{"host": "app-2"}
	if other, _ := monitorSeriesTemplate(point, "project-a", "monitor_points.jsonl"); other.ID == first.ID {
		t.Fatal("different hosts share a series")
	}
	point.MetricName = ""
	if legacy, ok := monitorSeriesTemplate(point, "project-a", "monitor_points.jsonl"); !ok || legacy.Identity != "" || legacy.ID == "" {
		t.Fatalf("legacy %+v", legacy)
	}

	scope := rcaIdentityScope{project: "project-a", scenario: "scenario-1"}
	checkout := rcaRunSeriesIdentity(scope, "transaction", "checkout", "p95_ms", "ms", analysis.KindObservation, 10*time.Second)
	if len(checkout) != 64 || checkout == rcaRunSeriesIdentity(scope, "transaction", "cart", "p95_ms", "ms", analysis.KindObservation, 10*time.Second) ||
		checkout == rcaRunSeriesIdentity(scope, "action", "checkout", "p95_ms", "ms", analysis.KindObservation, 10*time.Second) ||
		checkout == rcaRunSeriesIdentity(rcaIdentityScope{project: "project-b"}, "transaction", "checkout", "p95_ms", "ms", analysis.KindObservation, 10*time.Second) ||
		checkout != rcaRunSeriesIdentity(scope, "transaction", "checkout", "p95_ms", "ms", analysis.KindObservation, 10*time.Second) {
		t.Fatal("transaction identities do not name their measurement")
	}
}

// Joint (version 3) interval evidence becomes series on its exact tuple;
// earlier records without it are not zeros, and the slices measure their
// action.
func TestRCAJointIntervalEvidenceBecomesTupleSeries(t *testing.T) {
	if rcaJointDimension+"/" != rca.SeriesJointPrefix {
		t.Fatal("joint series IDs do not carry the joint prefix")
	}
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "rca-run", nil)
	tuple := func(action, network string) string {
		body, _ := json.Marshal([4]string{action, network, "", ""})
		return string(body)
	}
	slow, fast := tuple("orders", "3g"), tuple("orders", "wifi")
	var distributions []IntervalDistributionRecord
	for i := 1; i <= 36; i++ {
		r := IntervalDistributionRecord{Schema: intervalDistributionSchema, Timestamp: exemplarFixtureStart.Add(time.Duration(i) * 10 * time.Second), WindowSeconds: 10,
			Dimensions: map[string]map[string]IntervalDistributionMetric{"transaction": {"checkout": {Count: 20, P95: 300}}}}
		if i > 6 {
			r.EvidenceVersion = 3
			r.Dimensions[rcaJointDimension] = map[string]IntervalDistributionMetric{slow: {Count: 10, P95: 900}, fast: {Count: 10, P95: 100}}
		}
		distributions = append(distributions, r)
	}
	writeArchiveSidecar(t, record, intervalDistributionsArchiveFileName, distributions, func(m *RunArchiveManifest) {
		m.IntervalDistributionsFile = intervalDistributionsArchiveFileName
	})
	manifest, _ := loadRunArchiveManifestFromRecord(record)
	input, err := archiveDetectionInput(context.Background(), record, manifest, nil, rca.ArrivalClosed, rca.NewBudgeter(rca.DefaultBudget, nil, nil), rca.DefaultBudget, nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]rca.Series{}
	for _, s := range input.Series {
		byID[s.ID] = s
	}
	joint := byID[rca.SeriesJointPrefix+slow+"/p95_ms"]
	throughput := byID[rca.SeriesJointPrefix+fast+"/throughput"]
	if len(joint.Points) != 30 || joint.Points[0].Value != 900 || len(joint.Nodes) != 1 || joint.Nodes[0] != rca.ActionNodeID("orders") || len(joint.Identity) != 64 ||
		len(throughput.Points) != 30 || byID["transaction/checkout/p95_ms"].Identity == "" {
		t.Fatalf("joint %+v throughput %d points", joint, len(throughput.Points))
	}
	// A series budget that the transactions fill leaves joint slices out.
	tight := rca.DefaultBudget
	tight.MaxSeries = 3
	input, _ = archiveDetectionInput(context.Background(), record, manifest, nil, rca.ArrivalClosed, rca.NewBudgeter(tight, nil, nil), tight, nil)
	for _, s := range input.Series {
		if strings.HasPrefix(s.ID, rca.SeriesJointPrefix) {
			t.Fatalf("joint series %s kept beyond the budget", s.ID)
		}
	}
	if !strings.Contains(limitationCodes(input.Limitations), limitationDetectionEvidenceCut) {
		t.Fatalf("%+v", input.Limitations)
	}
}

func TestCoarsenImportedSeriesRegridsSparseSamples(t *testing.T) {
	start := time.Unix(1700000000, 0).UTC()
	var points []rca.SeriesPoint
	for i := 0; i < 20; i++ {
		points = append(points, rca.SeriesPoint{At: start.Add(time.Duration(i) * time.Second), Value: float64(i), Valid: i%5 == 0})
	}
	coarse, bucket := coarsenImportedSeries(points, time.Second)
	if bucket != 5*time.Second || len(coarse) != 4 || !coarse[1].Valid || coarse[1].Value != 5 || !coarse[1].At.Equal(start.Add(5*time.Second)) {
		t.Fatalf("%s %+v", bucket, coarse)
	}
	// A dense series keeps the run grid.
	dense := []rca.SeriesPoint{{At: start, Value: 1, Valid: true}, {At: start.Add(time.Second), Value: 2, Valid: true}, {At: start.Add(2 * time.Second), Value: 3, Valid: true}}
	if same, bucket := coarsenImportedSeries(dense, time.Second); bucket != time.Second || len(same) != 3 {
		t.Fatalf("%s %+v", bucket, same)
	}
	if none, bucket := coarsenImportedSeries(nil, time.Second); none != nil || bucket != time.Second {
		t.Fatal("an empty series changed")
	}
}
