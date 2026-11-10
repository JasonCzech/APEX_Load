package generatorrpc

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
)

// archivePhaseGoldenRow is the HTTP window's archived row, minus its duration
// distributions and timestamp. pkg/api's distributed merge test reads the same file, so the
// two packages are held to one wire row. Regenerate it with
// APEXLOAD_UPDATE_ARCHIVE_PHASE_GOLDEN=1 after an intended change.
const archivePhaseGoldenRow = "testdata/archive_interval_phase_row.json"

// windowFleet aggregates one generator window exactly as the generator does
// before it publishes a batch, and returns its fleet aggregate on the wire.
func windowFleet(transactions ...TransactionMetric) *pb.MetricAggregate {
	window := newTransactionWindowAggregate()
	for _, transaction := range transactions {
		window.add(transaction)
	}
	return metricAggregateToProto(window.finalize(1_000).Fleet)
}

// archivedGeneratorPhaseRows records one interval per fleet aggregate through
// the generator's own archive path and returns the JSON rows as written.
func archivedGeneratorPhaseRows(t *testing.T, fleets ...*pb.MetricAggregate) []map[string]any {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	writer, err := newArchiveRecorder("phase-generator", "phase-run", "Phases")
	if err != nil {
		t.Fatalf("newArchiveRecorder: %v", err)
	}
	at := time.Date(2026, time.November, 10, 9, 0, 0, 0, time.UTC)
	for index, fleet := range fleets {
		(&Service{}).recordMetricsBatch("phase-run", writer, &pb.MetricsBatch{
			TestId:          "phase-run",
			TimestampUnixMs: at.Add(time.Duration(index) * time.Second).UnixMilli(),
			WindowMs:        1_000,
			Stats:           &pb.GeneratorStats{ActiveVusers: 2},
			FleetMetrics:    fleet,
		})
	}
	record, err := writer.Finalize("COMPLETE", at.Add(time.Minute), "")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	file, err := os.Open(filepath.Join(record.ArchivePath, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var rows []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(fleets) {
		t.Fatalf("archived %d rows, want %d", len(rows), len(fleets))
	}
	return rows
}

func phaseRowNumber(t *testing.T, row map[string]any, key string, want float64) {
	t.Helper()
	got, ok := row[key].(float64)
	if !ok || math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v (row %v)", key, row[key], want, row)
	}
}

// Distributed finalization builds the fleet archive from generator rows, so a
// generator must archive each window's network phases under the keys of
// api.IntervalSummaryRecord, with the split the controller derives live.
func TestGeneratorArchiveIntervalSummaryRecordsNetworkPhases(t *testing.T) {
	rows := archivedGeneratorPhaseRows(t,
		// An HTTP window: two first-byte requests and one that got none.
		windowFleet(
			TransactionMetric{Name: "Search", Success: true, DurationMs: 100, DNSMs: 2, ConnectMs: 4, TLSMs: 6, TTFBMs: 70},
			TransactionMetric{Name: "Search", Success: true, DurationMs: 200, ConnectMs: 3, TLSMs: 5, TTFBMs: 150},
			TransactionMetric{Name: "Search", DurationMs: 100, ConnectMs: 10},
		),
		// A protocol without TTFB (gRPC, JDBC): every sample is unsplit.
		windowFleet(
			TransactionMetric{Name: "Query", Success: true, DurationMs: 40},
			TransactionMetric{Name: "Query", Success: true, DurationMs: 60},
		),
		// Proto robustness, not a shape this build sends: averages from a
		// peer whose observations are missing.
		&pb.MetricAggregate{Count: 10, AvgMs: 160, AvgConnectMs: 2.8, AvgTtfbMs: 114},
	)

	http := rows[0]
	// Over 3 attempts: DNS 2/3, connect 17/3, TLS 11/3, TTFB 220/3; no-response
	// 90/3; server 220/3 - (30/3 - 10/3); download 400/3 - 220/3 - 10/3 - 30.
	phaseRowNumber(t, http, "avg_dns_ms", 2.0/3)
	phaseRowNumber(t, http, "avg_connect_ms", 17.0/3)
	phaseRowNumber(t, http, "avg_tls_ms", 11.0/3)
	phaseRowNumber(t, http, "avg_ttfb_ms", 220.0/3)
	phaseRowNumber(t, http, "avg_no_response_ms", 30)
	phaseRowNumber(t, http, "avg_server_ms", 200.0/3)
	phaseRowNumber(t, http, "avg_wait_ms", 80.0/3)
	counts, _ := http["phase_observed"].(map[string]any)
	if counts == nil || counts["dns"] != 1.0 || counts["connect"] != 3.0 || counts["tls"] != 2.0 || counts["no_ttfb"] != 1.0 || counts["no_ttfb_setup_ms"] != 10.0 || counts["no_ttfb_remainder_ms"] != 90.0 {
		t.Fatalf("phase_observed = %v", http["phase_observed"])
	}

	// No TTFB: no setup, no server or download time, and the whole average
	// is the unsplit remainder, as the live chart shows it.
	unsplit := rows[1]
	phaseRowNumber(t, unsplit, "avg_no_response_ms", 50)
	phaseRowNumber(t, unsplit, "avg_server_ms", 0)
	for _, key := range []string{"avg_dns_ms", "avg_connect_ms", "avg_tls_ms", "avg_ttfb_ms", "avg_wait_ms"} {
		if _, ok := unsplit[key]; ok {
			t.Fatalf("unsplit window has %s: %v", key, unsplit)
		}
	}
	if counts, _ := unsplit["phase_observed"].(map[string]any); counts == nil || counts["no_ttfb"] != 2.0 || counts["no_ttfb_remainder_ms"] != 100.0 {
		t.Fatalf("unsplit phase_observed = %v", unsplit["phase_observed"])
	}

	// Without observations the split is unknown and download falls back to
	// avg - TTFB, as the controller's live fleet does.
	older := rows[2]
	phaseRowNumber(t, older, "avg_connect_ms", 2.8)
	phaseRowNumber(t, older, "avg_ttfb_ms", 114)
	phaseRowNumber(t, older, "avg_wait_ms", 46)
	for _, key := range []string{"phase_observed", "avg_server_ms", "avg_no_response_ms"} {
		if _, ok := older[key]; ok {
			t.Fatalf("row without observations invented %s: %v", key, older)
		}
	}

	golden := map[string]any{}
	for key, value := range http {
		// The distributions are bulky and covered elsewhere; the timestamp is
		// written in the machine's zone.
		if key != "duration_digest" && key != "duration_sketch" && key != "timestamp" {
			golden[key] = value
		}
	}
	if os.Getenv("APEXLOAD_UPDATE_ARCHIVE_PHASE_GOLDEN") == "1" {
		payload, err := json.MarshalIndent(golden, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archivePhaseGoldenRow, append(payload, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := os.ReadFile(archivePhaseGoldenRow)
	if err != nil {
		t.Fatalf("read golden row: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(payload, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(golden, want) {
		t.Fatalf("archived HTTP row drifted from %s:\n got %v\nwant %v", archivePhaseGoldenRow, golden, want)
	}
}
