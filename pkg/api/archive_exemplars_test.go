package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

var exemplarFixtureStart = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// exemplarArchiveFixture archives one request per second for six minutes in
// 10-second intervals. Requests between 120 s and 240 s are slow.
func exemplarArchiveFixture(t *testing.T, server *Server, store *storage.SQLiteStore, id string, mutate func(i int, tx *TransactionEvent)) *storage.RunArchiveRecord {
	t.Helper()
	start := exemplarFixtureStart
	root := t.TempDir()
	sent := true
	var rows []TransactionEvent
	for i := 0; i < 360; i++ {
		duration := 100 + float64(i%7)
		if i >= 120 && i < 240 {
			duration = 2000 + float64(i%11)
		}
		tx := TransactionEvent{Timestamp: start.Add(time.Duration(i)*time.Second + 500*time.Millisecond), Group: "shop", ActionName: "GET /cart", Population: "action", DependencyType: "http", Success: true, StatusCode: 200, DurationMs: duration, TraceSent: &sent, SentTraceID: fmt.Sprintf("%032x", i+1), SentSpanID: fmt.Sprintf("%016x", i+1)}
		if mutate != nil {
			mutate(i, &tx)
		}
		rows = append(rows, tx)
	}
	var summaries []IntervalSummaryRecord
	for i := 1; i <= 36; i++ {
		summaries = append(summaries, IntervalSummaryRecord{Timestamp: start.Add(time.Duration(i) * 10 * time.Second), SampleWindowSeconds: 10, ActiveVUsers: 5})
	}
	writeJSONL := func(name string, items any) {
		f, err := os.Create(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		encoder := json.NewEncoder(f)
		switch v := items.(type) {
		case []TransactionEvent:
			for _, item := range v {
				if err := encoder.Encode(item); err != nil {
					t.Fatal(err)
				}
			}
		case []IntervalSummaryRecord:
			for _, item := range v {
				if err := encoder.Encode(item); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	end := start.Add(6 * time.Minute)
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: id, Status: "COMPLETE", CreatedAt: start, CompletedAt: end, Provenance: RunArchiveProvenance{StartedAt: &start}, TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl", TransactionCount: int64(len(rows)), IntervalSummaryCount: int64(len(summaries))}
	writeJSONL("transactions.jsonl", rows)
	writeJSONL("interval_summaries.jsonl", summaries)
	if err := os.WriteFile(filepath.Join(root, "resources.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", Groups: []any{}, StartTime: start}); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: id, SchemaVersion: archiveSchemaVersion, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json"), CreatedAt: start, CompletedAt: end, TransactionCount: int64(len(rows)), IntervalSummaryCount: int64(len(summaries))}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	server.runner.archives[id] = record
	return record
}

func selectFixtureExemplars(t *testing.T, record *storage.RunArchiveRecord) ([]ArchiveExemplar, bool, *RunArchiveManifest) {
	t.Helper()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	exemplars, truncated, err := selectArchiveExemplars(context.Background(), record, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return exemplars, truncated, manifest
}

func TestExemplarsSurfaceAnInjectedSlowWindow(t *testing.T) {
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "slow-window", nil)
	exemplars, truncated, _ := selectFixtureExemplars(t, record)
	if truncated || len(exemplars) == 0 {
		t.Fatalf("selected %d, truncated %v", len(exemplars), truncated)
	}
	slowIntervals := map[time.Time]bool{}
	for _, e := range exemplars {
		if e.Reason != exemplarReasonSlowest || e.Schema != exemplarSchema || e.Population != "action" || e.Name != "GET /cart" {
			continue
		}
		offset := e.IntervalEnd.Sub(exemplarFixtureStart)
		inSlowWindow := offset > 120*time.Second && offset <= 240*time.Second
		if inSlowWindow != (e.DurationMs >= 2000) {
			t.Fatalf("interval ending %s picked %.0f ms", offset, e.DurationMs)
		}
		if inSlowWindow {
			slowIntervals[e.IntervalEnd] = true
		}
	}
	if len(slowIntervals) != 12 {
		t.Fatalf("slow window covered %d of 12 intervals", len(slowIntervals))
	}
}

func TestExemplarsAreByteIdenticalAcrossRuns(t *testing.T) {
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "deterministic", nil)
	admitArchiveForTest(t, record)
	write := func() []byte {
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		manifest.ExemplarsFile = ""
		if err := server.runner.persistExemplarArchive(context.Background(), record, manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.ExemplarsFile != exemplarsArchiveFileName || manifest.ExemplarCount == 0 {
			t.Fatalf("manifest file %q count %d", manifest.ExemplarsFile, manifest.ExemplarCount)
		}
		data, err := os.ReadFile(filepath.Join(record.ArchivePath, exemplarsArchiveFileName))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if first, second := write(), write(); !bytes.Equal(first, second) {
		t.Fatal("two selections over the same archive differ")
	}
}

func TestExemplarsPickDistinctErrorClassesAndSkipUntracedRows(t *testing.T) {
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "errors", func(i int, tx *TransactionEvent) {
		switch {
		case i < 3: // three 503s and a timeout in the first interval
			tx.Success, tx.StatusCode, tx.ErrorMessage = false, 503, "service unavailable"
		case i == 3:
			tx.Success, tx.StatusCode, tx.ErrorMessage = false, 0, "context deadline exceeded"
		case i == 4: // the slowest request was never traced
			tx.TraceSent, tx.DurationMs = nil, 99999
		case i == 5: // served from cache; nothing was sent
			unsent := false
			tx.TraceSent, tx.DurationMs = &unsent, 88888
		}
	})
	exemplars, _, _ := selectFixtureExemplars(t, record)
	classes := map[string]string{}
	for _, e := range exemplars {
		if e.IntervalEnd.Equal(exemplarFixtureStart.Add(10 * time.Second)) {
			if e.DurationMs >= 88888 {
				t.Fatalf("untraced row selected: %+v", e)
			}
			if e.Reason == exemplarReasonError {
				classes[e.ErrorClass] = e.TraceID
			}
		}
	}
	if len(classes) != 2 || classes["http_5xx"] != fmt.Sprintf("%032x", 1) {
		t.Fatalf("error exemplars by class = %v", classes)
	}
}

func TestExemplarsNearTheIntervalMedian(t *testing.T) {
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "median", nil)
	admitArchiveForTest(t, record)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	// Medians come from the interval distributions written just before.
	if err := server.runner.persistIntervalDistributionArchive(context.Background(), record.TestID, record, manifest); err != nil {
		t.Fatal(err)
	}
	exemplars, _, err := selectArchiveExemplars(context.Background(), record, manifest)
	if err != nil {
		t.Fatal(err)
	}
	near := 0
	for _, e := range exemplars {
		if e.Reason == exemplarReasonNearMedian {
			near++
			if e.IntervalMedianMs == nil || e.DurationMs-*e.IntervalMedianMs > 10 || *e.IntervalMedianMs-e.DurationMs > 10 {
				t.Fatalf("near-median pick %+v", e)
			}
		}
	}
	if near == 0 {
		t.Fatal("no near-median exemplars despite archived interval medians")
	}
}

func TestExemplarCapsSpreadAcrossIntervals(t *testing.T) {
	previous := maxTransactionExemplars
	maxTransactionExemplars = 12
	defer func() { maxTransactionExemplars = previous }()
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "capped", nil)
	exemplars, truncated, _ := selectFixtureExemplars(t, record)
	intervals := map[time.Time]bool{}
	for _, e := range exemplars {
		intervals[e.IntervalEnd] = true
	}
	if !truncated || len(exemplars) != 12 || len(intervals) != 12 {
		t.Fatalf("capped to %d exemplars over %d intervals, truncated %v", len(exemplars), len(intervals), truncated)
	}
}
