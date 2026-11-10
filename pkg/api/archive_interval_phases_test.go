package api

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

func archivePhaseClose(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

// A completed run's chart reads archive interval summaries, so the archive
// must keep each interval's fleet network phases, and an interval without
// phases must stay unknown on every read path the dashboard uses.
func TestRunArchiveIntervalSummariesKeepNetworkPhases(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "archive-phases", Name: "phases"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.November, 10, 9, 0, 0, 0, time.UTC)
	if err := writer.RecordIntervalSummary(&LiveMetrics{Timestamp: start, AvgResponseMs: 90, TotalTx: 3}); err != nil {
		t.Fatal(err)
	}
	if err := writer.RecordIntervalSummary(&LiveMetrics{
		Timestamp: start.Add(time.Second), AvgResponseMs: 120, TotalTx: 10,
		AvgDNSMs: 1.5, AvgConnectMs: 4.5, AvgTLSMs: 9.25, AvgTTFBMs: 80, AvgWaitMs: 30,
		PhaseObserved: &engine.PhaseObservations{DNS: 2, Connect: 4, TLS: 4, NoTTFB: 1, NoTTFBSetupMs: 2.5, NoTTFBRemainderMs: 60},
		AvgServerMs:   float64Ptr(62), AvgNoResponseMs: float64Ptr(6),
	}); err != nil {
		t.Fatal(err)
	}
	record, err := writer.Finalize("COMPLETE", start.Add(2*time.Second), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	paged, _, err := loadArchiveIntervalSummaries(record, archivePage{Limit: 10}, "", groupFilterQuery{})
	if err != nil {
		t.Fatal(err)
	}
	downsampled, _, err := loadArchiveIntervalSummariesDownsampled(record, 10)
	if err != nil {
		t.Fatal(err)
	}
	for name, rows := range map[string][]IntervalSummaryRecord{"paged": paged, "downsampled": downsampled} {
		if len(rows) != 2 {
			t.Fatalf("%s summaries = %d rows, want 2", name, len(rows))
		}
		encoded, err := json.Marshal(rows[0])
		if err != nil {
			t.Fatal(err)
		}
		var legacy map[string]any
		if err := json.Unmarshal(encoded, &legacy); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"avg_dns_ms", "avg_connect_ms", "avg_tls_ms", "avg_ttfb_ms", "avg_wait_ms", "phase_observed", "avg_server_ms", "avg_no_response_ms"} {
			if _, ok := legacy[key]; ok {
				t.Fatalf("%s: interval without phases invented %s: %s", name, key, encoded)
			}
		}
		got := rows[1]
		if got.AvgDNSMs != 1.5 || got.AvgConnectMs != 4.5 || got.AvgTLSMs != 9.25 || got.AvgTTFBMs != 80 || got.AvgWaitMs != 30 {
			t.Fatalf("%s: phase averages = %+v", name, got)
		}
		if got.PhaseObserved == nil || *got.PhaseObserved != (engine.PhaseObservations{DNS: 2, Connect: 4, TLS: 4, NoTTFB: 1, NoTTFBSetupMs: 2.5, NoTTFBRemainderMs: 60}) {
			t.Fatalf("%s: phase_observed = %+v", name, got.PhaseObserved)
		}
		if got.AvgServerMs == nil || *got.AvgServerMs != 62 || got.AvgNoResponseMs == nil || *got.AvgNoResponseMs != 6 {
			t.Fatalf("%s: split = %v / %v", name, got.AvgServerMs, got.AvgNoResponseMs)
		}
	}
	// A group/action-filtered summary has no phase dimension: unknown, not 0.
	filtered, _, err := loadArchiveIntervalSummaries(record, archivePage{Limit: 10}, "Search", groupFilterQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 {
		t.Fatalf("filtered summaries = %d rows, want 2", len(filtered))
	}
	for _, row := range filtered {
		if archiveIntervalHasPhases(row) {
			t.Fatalf("filtered summary carried fleet phases: %+v", row)
		}
	}
}

func writeArchivePhaseBundle(t *testing.T, target string, row any) remoteArchiveBundle {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "interval_summaries.jsonl")
	payload, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return remoteArchiveBundle{target: target, summariesPath: path, manifest: RunArchiveManifest{IntervalSummaryCount: 1}}
}

func mergeArchivePhaseRows(t *testing.T, rows ...any) IntervalSummaryRecord {
	t.Helper()
	bundles := make([]remoteArchiveBundle, 0, len(rows))
	for index, row := range rows {
		bundles = append(bundles, writeArchivePhaseBundle(t, string(rune('a'+index)), row))
	}
	destination := filepath.Join(t.TempDir(), "merged.jsonl")
	count, err := mergeRemoteArchiveIntervalSummaries(destination, bundles)
	if err != nil || count != 1 {
		t.Fatalf("merge = %d, %v", count, err)
	}
	payload, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var merged IntervalSummaryRecord
	if err := json.Unmarshal(payload, &merged); err != nil {
		t.Fatal(err)
	}
	return merged
}

// generatorArchivePhaseRow is a generator's archived HTTP window exactly as
// pkg/distributed/generatorrpc writes it (its golden test pins the same file):
// 3 attempts, one of which got no first byte.
func generatorArchivePhaseRow(t *testing.T, at time.Time) map[string]any {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "distributed", "generatorrpc", "testdata", "archive_interval_phase_row.json"))
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(payload, &row); err != nil {
		t.Fatal(err)
	}
	row["timestamp"] = at.Format(time.RFC3339Nano)
	return row
}

// Distributed finalization merges generator rows into the fleet interval the
// way the live fleet merge does: attempt-weighted averages, summed
// observations, and the server/download/no-response split recomputed.
func TestMergeRemoteArchiveIntervalSummariesMergesNetworkPhases(t *testing.T) {
	at := time.Date(2026, time.November, 10, 9, 0, 0, 0, time.UTC)
	a := generatorArchivePhaseRow(t, at)
	b := IntervalSummaryRecord{
		Timestamp: at, TestID: "phase-run", TotalTx: 6, AvgResponseMs: 200,
		AvgConnectMs: 2, AvgTLSMs: 4, AvgTTFBMs: 150, AvgWaitMs: 50,
		PhaseObserved: &engine.PhaseObservations{Connect: 3, TLS: 3},
		AvgServerMs:   float64Ptr(144), AvgNoResponseMs: float64Ptr(0),
	}

	merged := mergeArchivePhaseRows(t, a, b)
	// Weights: a has 3 attempts, b 6, so averages are over 9.
	if !archivePhaseClose(merged.AvgResponseMs, 1600.0/9) || !archivePhaseClose(merged.AvgDNSMs, 2.0/9) || !archivePhaseClose(merged.AvgConnectMs, 29.0/9) || !archivePhaseClose(merged.AvgTLSMs, 35.0/9) || !archivePhaseClose(merged.AvgTTFBMs, 1120.0/9) {
		t.Fatalf("merged averages = %+v", merged)
	}
	if merged.PhaseObserved == nil || *merged.PhaseObserved != (engine.PhaseObservations{DNS: 1, Connect: 6, TLS: 5, NoTTFB: 1, NoTTFBSetupMs: 10, NoTTFBRemainderMs: 90}) {
		t.Fatalf("merged phase_observed = %+v", merged.PhaseObserved)
	}
	// Over 9 attempts: no-response 90/9; server 1120/9 - (66/9 - 10/9);
	// download 1600/9 - 1120/9 - 10/9 - 10. A wrong attempt count changes all
	// three, and the stack adds up to the average.
	if merged.AvgNoResponseMs == nil || !archivePhaseClose(*merged.AvgNoResponseMs, 10) || merged.AvgServerMs == nil || !archivePhaseClose(*merged.AvgServerMs, 1064.0/9) || !archivePhaseClose(merged.AvgWaitMs, 380.0/9) {
		t.Fatalf("merged split = server %v no-response %v download %v", merged.AvgServerMs, merged.AvgNoResponseMs, merged.AvgWaitMs)
	}

	// A generator build that archived no phases (although it sent them live)
	// makes the fleet interval's phases unknown, not diluted toward 0.
	oldFormat := json.RawMessage(`{"timestamp":"2026-11-10T09:00:00Z","test_id":"phase-run","test_name":"Phases","active_vusers":2,"tps":6,` +
		`"avg_response_ms":200,"error_pct":0,"total_transactions":6,"total_errors":0,"p50_ms":0,"p90_ms":0,"p95_ms":0,"p99_ms":0}`)
	if mixed := mergeArchivePhaseRows(t, a, oldFormat); archiveIntervalHasPhases(mixed) || !archivePhaseClose(mixed.AvgResponseMs, 1600.0/9) {
		t.Fatalf("fleet with an old-format generator row = %+v, want unknown phases", mixed)
	}

	// Proto robustness: a row with averages but no observations keeps the
	// averages, and the split is unknown with download from the rows' own tails.
	averagesOnly := b
	averagesOnly.PhaseObserved, averagesOnly.AvgServerMs, averagesOnly.AvgNoResponseMs = nil, nil, nil
	partial := mergeArchivePhaseRows(t, a, averagesOnly)
	if partial.PhaseObserved != nil || partial.AvgServerMs != nil || partial.AvgNoResponseMs != nil {
		t.Fatalf("merge invented observations: %+v", partial)
	}
	if !archivePhaseClose(partial.AvgTTFBMs, 1120.0/9) || !archivePhaseClose(partial.AvgWaitMs, (80.0+50*6)/9) {
		t.Fatalf("averages-only merge = ttfb %v wait %v", partial.AvgTTFBMs, partial.AvgWaitMs)
	}

	// Rows without phases keep the fleet interval's phases unknown.
	plainB := oldFormat
	plainA := json.RawMessage(`{"timestamp":"2026-11-10T09:00:00Z","test_id":"phase-run","test_name":"Phases","active_vusers":1,"tps":2,` +
		`"avg_response_ms":100,"error_pct":33,"total_transactions":2,"total_errors":1,"p50_ms":0,"p90_ms":0,"p95_ms":0,"p99_ms":0}`)
	if plain := mergeArchivePhaseRows(t, plainA, plainB); archiveIntervalHasPhases(plain) {
		t.Fatalf("phase-less fleet invented phases: %+v", plain)
	}
}

// exactArchivePhaseInterval is one fleet window built from raw events: two first-byte
// requests and one that got none (10 ms setup, then 90 ms of nothing).
func exactArchivePhaseInterval(at time.Time) remoteArchiveFleetIntervalMetric {
	var raw remoteArchiveFleetRawMetric
	for _, event := range []TransactionEvent{
		{Timestamp: at, Success: true, DurationMs: 100, DNSMs: 2, ConnectMs: 4, TLSMs: 6, TTFBMs: 70},
		{Timestamp: at, Success: true, DurationMs: 200, ConnectMs: 3, TLSMs: 5, TTFBMs: 150},
		{Timestamp: at, Success: false, DurationMs: 100, ConnectMs: 10},
	} {
		addRemoteArchiveFleetRawMetric(&raw, event)
	}
	interval := remoteArchiveFleetIntervalMetric{}
	var durations []float64
	mergeRemoteArchiveFleetRawMetric(&interval, &durations, raw)
	return interval
}

// rewriteExactArchivePhases runs the exact pass over provisional fleet rows,
// each window rebuilt from exactArchivePhaseInterval's raw events.
func rewriteExactArchivePhases(t *testing.T, rows ...IntervalSummaryRecord) []IntervalSummaryRecord {
	t.Helper()
	dir := t.TempDir()
	provisional := filepath.Join(dir, "provisional.jsonl")
	var payload []byte
	intervals := make([]remoteArchiveFleetIntervalMetric, 0, len(rows))
	for _, row := range rows {
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		payload = append(append(payload, line...), '\n')
		intervals = append(intervals, exactArchivePhaseInterval(row.Timestamp))
	}
	if err := os.WriteFile(provisional, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "final.jsonl")
	count, err := writeRemoteArchiveFleetSummaries(destination, provisional, intervals, true, nil)
	if err != nil || int(count) != len(rows) {
		t.Fatalf("writeRemoteArchiveFleetSummaries = %d, %v", count, err)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	out := make([]IntervalSummaryRecord, len(rows))
	for index := range out {
		if err := decoder.Decode(&out[index]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// requireExactArchivePhases checks a window rebuilt from the raw events: over
// 3 attempts, averages 2/3, 17/3, 11/3 and TTFB 220/3, and with observations
// the split adds up to the rewritten average 400/3.
func requireExactArchivePhases(t *testing.T, name string, row IntervalSummaryRecord, observed bool) {
	t.Helper()
	if !archivePhaseClose(row.AvgResponseMs, 400.0/3) || !archivePhaseClose(row.AvgDNSMs, 2.0/3) || !archivePhaseClose(row.AvgConnectMs, 17.0/3) || !archivePhaseClose(row.AvgTLSMs, 11.0/3) || !archivePhaseClose(row.AvgTTFBMs, 220.0/3) {
		t.Fatalf("%s: exact averages = %+v", name, row)
	}
	if !observed {
		if row.PhaseObserved != nil || row.AvgServerMs != nil || row.AvgNoResponseMs != nil || !archivePhaseClose(row.AvgWaitMs, 60) {
			t.Fatalf("%s: unknown observations restored: %+v", name, row)
		}
		return
	}
	if row.PhaseObserved == nil || *row.PhaseObserved != (engine.PhaseObservations{DNS: 1, Connect: 3, TLS: 2, NoTTFB: 1, NoTTFBSetupMs: 10, NoTTFBRemainderMs: 90}) {
		t.Fatalf("%s: exact phase_observed = %+v", name, row.PhaseObserved)
	}
	if row.AvgServerMs == nil || row.AvgNoResponseMs == nil {
		t.Fatalf("%s: exact split missing: %+v", name, row)
	}
	stack := row.AvgDNSMs + row.AvgConnectMs + row.AvgTLSMs + *row.AvgServerMs + row.AvgWaitMs + *row.AvgNoResponseMs
	if !archivePhaseClose(stack, row.AvgResponseMs) || !archivePhaseClose(*row.AvgNoResponseMs, 30) || !archivePhaseClose(*row.AvgServerMs, 200.0/3) || !archivePhaseClose(row.AvgWaitMs, 80.0/3) {
		t.Fatalf("%s: exact split = server %v download %v no-response %v, stack %v != avg %v", name, *row.AvgServerMs, row.AvgWaitMs, *row.AvgNoResponseMs, stack, row.AvgResponseMs)
	}
}

// When raw events let finalization rebuild a fleet interval exactly, the
// phases are rebuilt from the same events, so the stack still adds up to the
// rewritten average. Whether phases are known is decided for the whole fleet.
func TestWriteRemoteArchiveFleetSummariesRebuildsExactNetworkPhases(t *testing.T) {
	at := time.Date(2026, time.November, 10, 9, 0, 0, 0, time.UTC)
	reported := func(offset int, cumulative int64) IntervalSummaryRecord {
		return IntervalSummaryRecord{
			Timestamp: at.Add(time.Duration(offset) * time.Second), TestID: "fleet", TotalTx: cumulative - 1, TotalErrors: 1, AvgResponseMs: 999,
			AvgConnectMs: 50, AvgTTFBMs: 500, AvgWaitMs: 400,
			PhaseObserved: &engine.PhaseObservations{Connect: 6}, AvgServerMs: float64Ptr(450), AvgNoResponseMs: float64Ptr(0),
		}
	}
	bare := func(offset int, cumulative int64) IntervalSummaryRecord {
		return IntervalSummaryRecord{Timestamp: at.Add(time.Duration(offset) * time.Second), TestID: "fleet", TotalTx: cumulative - 1, TotalErrors: 1, AvgResponseMs: 999}
	}

	// Every generator reported phases. The second window's provisional row
	// had no completions (so no phases), but its raw events, bucketed by start
	// time, give it attempts: it is rebuilt too.
	known := rewriteExactArchivePhases(t, reported(0, 6), bare(1, 6))
	requireExactArchivePhases(t, "reported window", known[0], true)
	requireExactArchivePhases(t, "window with no provisional completions", known[1], true)

	// A window whose generators sent no phases makes the fleet unknown in
	// every window: raw events from that generator can land in any window by
	// start time, including one whose provisional row carries another
	// generator's phases, so neither the provisional phases (computed against
	// the pre-rewrite average) nor a raw-event rebuild is kept.
	unknown := rewriteExactArchivePhases(t, reported(0, 6), bare(1, 12))
	for index, row := range unknown {
		if archiveIntervalHasPhases(row) {
			t.Fatalf("window %d of a fleet with an unreported generator kept phases: %+v", index, row)
		}
	}

	// Phases without observations: the averages are rebuilt, the observations
	// and split stay unknown, and download is avg - TTFB.
	averagesOnly := reported(0, 6)
	averagesOnly.PhaseObserved, averagesOnly.AvgServerMs, averagesOnly.AvgNoResponseMs = nil, nil, nil
	requireExactArchivePhases(t, "window without observations", rewriteExactArchivePhases(t, averagesOnly)[0], false)
}
