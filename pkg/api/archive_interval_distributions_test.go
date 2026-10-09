package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func readIntervalDistributions(t *testing.T, record *storage.RunArchiveRecord) []IntervalDistributionRecord {
	t.Helper()
	file, err := os.Open(filepath.Join(record.ArchivePath, intervalDistributionsArchiveFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var out []IntervalDistributionRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		var r IntervalDistributionRecord
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// admitArchiveForTest places the fixture's archive under the configured
// filesystem archive root, as finalization requires.
func admitArchiveForTest(t *testing.T, record *storage.RunArchiveRecord) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", filepath.Dir(record.ArchivePath))
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv(envArchiveAdjunctMinFreeBytes, "1")
}

func persistIntervalDistributionsForTest(t *testing.T, server *Server, id string) (*storage.RunArchiveRecord, *RunArchiveManifest) {
	t.Helper()
	record := server.runner.archives[id]
	admitArchiveForTest(t, record)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.runner.persistIntervalDistributionArchive(context.Background(), id, record, manifest); err != nil {
		t.Fatal(err)
	}
	return record, manifest
}

func nativeDigest(values ...float64) *engine.TDigestSnapshot {
	digest := engine.NewTDigest(200)
	for _, v := range values {
		digest.Add(v * float64(time.Millisecond))
	}
	snapshot := digest.Snapshot()
	return &snapshot
}

func TestIntervalDistributionsCopyLiveNativeEvidence(t *testing.T) {
	server, store := baselineTestServer(t)
	comparisonArchiveFixture(t, server, store, "native", 100, 100)
	start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	var live []*storage.NativeIntervalEvidence
	for i := 1; i <= 3; i++ {
		evidence := &storage.NativeIntervalEvidence{Version: 2, WindowSeconds: 5, Dimensions: map[string]map[string]storage.NativeIntervalMetric{
			"transaction": {"Purchase": {Count: 3, Errors: 1, P50: 20, P90: 30, P95: 30, P99: 30, DurationDigest: nativeDigest(10, 20, float64(30*i))}},
			"action":      {"request": {Count: 3, P95: 9, DurationDigest: nativeDigest(5, 7, 9)}},
			// Joint tuples have their own sidecar and are not copied here.
			storage.NativeJointDimension: {"tuple": {Count: 3, P95: 9, DurationDigest: nativeDigest(5, 7, 9)}},
		}}
		live = append(live, evidence)
		if err := store.SaveHistory("native", start.Add(time.Duration(i)*5*time.Second), &LiveMetrics{Timestamp: start.Add(time.Duration(i) * 5 * time.Second), NativeEvidence: evidence}); err != nil {
			t.Fatal(err)
		}
	}
	// A version 1 row has no mergeable distributions and is not copied.
	if err := store.SaveHistory("native", start.Add(20*time.Second), &LiveMetrics{Timestamp: start.Add(20 * time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Version: 1, WindowSeconds: 5, Dimensions: map[string]map[string]storage.NativeIntervalMetric{}}}); err != nil {
		t.Fatal(err)
	}
	record, manifest := persistIntervalDistributionsForTest(t, server, "native")
	if manifest.IntervalDistributionsFile != intervalDistributionsArchiveFileName || manifest.IntervalDistributionsSource != intervalDistributionSourceNative || manifest.IntervalDistributionCount != 3 || !manifest.IntervalDistributionsTruncated {
		t.Fatalf("manifest = file %q source %q count %d truncated %v", manifest.IntervalDistributionsFile, manifest.IntervalDistributionsSource, manifest.IntervalDistributionCount, manifest.IntervalDistributionsTruncated)
	}
	records := readIntervalDistributions(t, record)
	if len(records) != 3 {
		t.Fatalf("records = %d", len(records))
	}
	for i, r := range records {
		want := live[i].Dimensions["transaction"]["Purchase"]
		got := r.Dimensions["transaction"]["Purchase"]
		if r.Schema != intervalDistributionSchema || r.EvidenceVersion != 2 || r.DigestUnit != "ns" || got.PercentileQuality != intervalPercentileNativeDigest {
			t.Fatalf("record %d = %+v", i, r)
		}
		if _, ok := r.Dimensions[storage.NativeJointDimension]; ok || len(r.Dimensions) != 2 {
			t.Fatalf("record %d copied the joint tuples: %v", i, r.Dimensions)
		}
		// The sidecar keeps the live distribution and the percentiles live showed.
		if !reflect.DeepEqual(got.DurationDigest, want.DurationDigest) || got.P95 != want.P95 || got.Count != want.Count || got.Errors != want.Errors {
			t.Fatalf("record %d transaction = %+v, want %+v", i, got, want)
		}
	}
	found := false
	for _, artifact := range manifest.Artifacts {
		if artifact.Name == intervalDistributionsArchiveFileName {
			found = artifact.SHA256 != "" && artifact.Count == 3
		}
	}
	if !found {
		t.Fatalf("sidecar missing from artifact integrity: %+v", manifest.Artifacts)
	}
	// Written once: a later refresh keeps the first sidecar.
	if err := os.WriteFile(filepath.Join(record.ArchivePath, intervalDistributionsArchiveFileName), []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.runner.persistIntervalDistributionArchive(context.Background(), "native", record, manifest); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(record.ArchivePath, intervalDistributionsArchiveFileName)); string(data) != "kept\n" {
		t.Fatal("refresh rewrote the interval distributions")
	}
}

func TestIntervalDistributionsReconstructLegacyRows(t *testing.T) {
	server, store := baselineTestServer(t)
	comparisonArchiveFixture(t, server, store, "legacy", 100, 100)
	record, manifest := persistIntervalDistributionsForTest(t, server, "legacy")
	if manifest.IntervalDistributionsSource != intervalDistributionSourceRows || manifest.IntervalDistributionsTruncated {
		t.Fatalf("manifest source %q truncated %v", manifest.IntervalDistributionsSource, manifest.IntervalDistributionsTruncated)
	}
	records := readIntervalDistributions(t, record)
	total := int64(0)
	for _, r := range records {
		metric, ok := r.Dimensions["transaction"]["request"]
		if !ok || r.Source != intervalDistributionSourceRows || metric.PercentileQuality != intervalPercentileRows || r.WindowSeconds != 5 {
			t.Fatalf("record = %+v", r)
		}
		// Every fixture row takes 100 ms, so each interval's P95 is exactly that.
		if metric.P95 != 100 || metric.DurationDigest == nil || metric.DurationDigest.Count != metric.Count {
			t.Fatalf("metric = %+v", metric)
		}
		total += metric.Count
	}
	if total != 2400 || int64(len(records)) != manifest.IntervalDistributionCount {
		t.Fatalf("reconstructed %d rows in %d records, manifest count %d", total, len(records), manifest.IntervalDistributionCount)
	}
}

func TestIntervalDistributionsReconstructionBounds(t *testing.T) {
	if intervalDistributionMaxObservations != 100000 || intervalDistributionMaxBuckets != 1000 || intervalDistributionMaxKeys != 128 {
		t.Fatal("reconstruction bounds drifted from the archive query bounds")
	}
	restore := func(observations, buckets, keys int) func() {
		previous := [3]int{intervalDistributionMaxObservations, intervalDistributionMaxBuckets, intervalDistributionMaxKeys}
		intervalDistributionMaxObservations, intervalDistributionMaxBuckets, intervalDistributionMaxKeys = observations, buckets, keys
		return func() {
			intervalDistributionMaxObservations, intervalDistributionMaxBuckets, intervalDistributionMaxKeys = previous[0], previous[1], previous[2]
		}
	}
	t.Run("observations", func(t *testing.T) {
		defer restore(500, 1000, 128)()
		server, store := baselineTestServer(t)
		comparisonArchiveFixture(t, server, store, "bounded", 100, 100)
		record, manifest := persistIntervalDistributionsForTest(t, server, "bounded")
		total := int64(0)
		records := readIntervalDistributions(t, record)
		for _, r := range records {
			total += r.Dimensions["transaction"]["request"].Count
		}
		if total != 500 || !manifest.IntervalDistributionsTruncated {
			t.Fatalf("kept %d observations, truncated %v", total, manifest.IntervalDistributionsTruncated)
		}
		// The interval the bound stopped in holds only part of its rows.
		if last := records[len(records)-1]; !last.Incomplete || records[0].Incomplete {
			t.Fatalf("incomplete flags: first %v, last %v", records[0].Incomplete, last.Incomplete)
		}
	})
	t.Run("observations in merged order", func(t *testing.T) {
		defer restore(500, 1000, 128)()
		server, store := baselineTestServer(t)
		comparisonArchiveFixture(t, server, store, "merged", 100, 100)
		// A merged distributed archive lists each generator's rows in turn,
		// so rows are not in time order; reversing them stands in for that.
		record := server.runner.archives["merged"]
		path := filepath.Join(record.ArchivePath, "transactions.jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
		if err := os.WriteFile(path, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		record, _ = persistIntervalDistributionsForTest(t, server, "merged")
		for _, r := range readIntervalDistributions(t, record) {
			if !r.Incomplete {
				t.Fatalf("interval %s claims every row though the cut scan was unordered", r.Timestamp)
			}
		}
	})
	t.Run("buckets", func(t *testing.T) {
		defer restore(100000, 10, 128)()
		server, store := baselineTestServer(t)
		comparisonArchiveFixture(t, server, store, "few-buckets", 100, 100)
		record, manifest := persistIntervalDistributionsForTest(t, server, "few-buckets")
		for _, r := range readIntervalDistributions(t, record) {
			if r.Timestamp.After(time.Date(2026, 9, 20, 12, 0, 50, 0, time.UTC)) {
				t.Fatalf("record beyond the first 10 intervals: %s", r.Timestamp)
			}
		}
		if !manifest.IntervalDistributionsTruncated {
			t.Fatal("bucket truncation not flagged")
		}
	})
	t.Run("keys per dimension", func(t *testing.T) {
		defer restore(100000, 1000, 1)()
		server, store := baselineTestServer(t)
		comparisonArchiveFixture(t, server, store, "keys", 100, 100)
		// Rename every other row so one interval holds two action identities.
		record := server.runner.archives["keys"]
		path := filepath.Join(record.ArchivePath, "transactions.jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var rewritten []byte
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for i := 0; scanner.Scan(); i++ {
			var tx TransactionEvent
			if err := json.Unmarshal(scanner.Bytes(), &tx); err != nil {
				t.Fatal(err)
			}
			if i%2 == 1 {
				tx.ActionName = "zz-other"
			}
			line, _ := json.Marshal(tx)
			rewritten = append(append(rewritten, line...), '\n')
		}
		if err := os.WriteFile(path, rewritten, 0o600); err != nil {
			t.Fatal(err)
		}
		_, manifest := persistIntervalDistributionsForTest(t, server, "keys")
		for _, r := range readIntervalDistributions(t, record) {
			if len(r.Dimensions["transaction"]) != 1 || !r.Incomplete {
				t.Fatalf("key cap not held: %v incomplete=%v", r.Dimensions["transaction"], r.Incomplete)
			}
			if _, kept := r.Dimensions["transaction"]["request"]; !kept {
				t.Fatal("the cap must keep the lexically first keys")
			}
		}
		if !manifest.IntervalDistributionsTruncated {
			t.Fatal("key truncation not flagged")
		}
	})
}

func TestIntervalDistributionsStopAtTheirOwnBudget(t *testing.T) {
	server, store := baselineTestServer(t)
	comparisonArchiveFixture(t, server, store, "unbounded", 100, 100)
	full, _ := persistIntervalDistributionsForTest(t, server, "unbounded")
	fullInfo, err := os.Stat(filepath.Join(full.ArchivePath, intervalDistributionsArchiveFileName))
	if err != nil {
		t.Fatal(err)
	}
	fullCount := len(readIntervalDistributions(t, full))
	limit := fullInfo.Size() / 2
	comparisonArchiveFixture(t, server, store, "budgeted", 100, 100)
	t.Setenv(envArchiveIntervalDistributionsMaxBytes, strconv.FormatInt(limit, 10))
	record, manifest := persistIntervalDistributionsForTest(t, server, "budgeted")
	records := readIntervalDistributions(t, record)
	if !manifest.IntervalDistributionsTruncated || len(records) == 0 || len(records) >= fullCount || int64(len(records)) != manifest.IntervalDistributionCount {
		t.Fatalf("budgeted sidecar: %d of %d records, manifest count %d, truncated %v", len(records), fullCount, manifest.IntervalDistributionCount, manifest.IntervalDistributionsTruncated)
	}
	info, err := os.Stat(filepath.Join(record.ArchivePath, intervalDistributionsArchiveFileName))
	if err != nil || info.Size() > limit {
		t.Fatalf("sidecar size %v over %d: %v", info.Size(), limit, err)
	}
}

// Finalization writes the sidecar beside the other adjuncts and records it in
// the archive manifest on disk.
func TestTestManifestArtifactArchivesIntervalDistributions(t *testing.T) {
	server, store := baselineTestServer(t)
	test := comparisonArchiveFixture(t, server, store, "finalized", 100, 100)
	record := server.runner.archives["finalized"]
	admitArchiveForTest(t, record)
	if err := server.runner.persistTestManifestArtifact(test, record); err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.IntervalDistributionsFile != intervalDistributionsArchiveFileName || manifest.IntervalDistributionCount == 0 {
		t.Fatalf("manifest file %q count %d", manifest.IntervalDistributionsFile, manifest.IntervalDistributionCount)
	}
	names, err := archiveManifestArtifactNames(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := names[intervalDistributionsArchiveFileName]; !ok {
		t.Fatal("sidecar missing from the archive's downloadable artifacts")
	}
	// A refresh validates the sidecar's recorded digest and keeps it.
	if err := server.runner.persistTestManifestArtifact(test, record); err != nil {
		t.Fatal("refresh", err)
	}
}
