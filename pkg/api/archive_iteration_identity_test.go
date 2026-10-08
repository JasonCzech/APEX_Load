package api

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func readTransactionRows(t *testing.T, path string) []TransactionEvent {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var rows []TransactionEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		var row TransactionEvent
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// Archived rows keep the VUser iteration and interruption that journey
// accounting reads, in JSONL, Parquet and the distributed merge.
func TestArchiveRowsCarryVUserIterationIdentity(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "identity", Name: "Identity"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.October, 8, 9, 0, 0, 0, time.UTC)
	for _, result := range []*protocol.Result{
		{ActionName: "login", StartTime: start, Duration: time.Millisecond, Success: true, VUserID: 12, Iteration: 3},
		{ActionName: "Checkout", BusinessTransaction: true, StartTime: start, Duration: time.Millisecond, VUserID: 12, Iteration: 3, Interrupted: true, VUserLifetime: 77, WallElapsed: 3 * time.Second},
	} {
		if err := writer.RecordResult(result); err != nil {
			t.Fatal(err)
		}
	}
	record, err := writer.Finalize("COMPLETE", start.Add(time.Minute), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := readTransactionRows(t, filepath.Join(record.ArchivePath, "transactions.jsonl"))
	if len(rows) != 2 || rows[0].VUserID != 12 || rows[0].Iteration != 3 || rows[0].Interrupted || rows[1].VUserID != 12 || rows[1].Iteration != 3 || !rows[1].Interrupted {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[1].VUserLifetime != 77 || rows[1].ElapsedMs != 3000 || rows[0].VUserLifetime != 0 || rows[0].ElapsedMs != 0 {
		t.Fatalf("lifetime/elapsed rows = %+v", rows)
	}
	parquet := toParquetTransactions(rows)
	if parquet[0].VUserID != 12 || parquet[0].Iteration != 3 || parquet[0].Interrupted || !parquet[1].Interrupted || parquet[1].Iteration != 3 || parquet[1].VUserLifetime != 77 || parquet[1].ElapsedMs != 3000 {
		t.Fatalf("parquet rows = %+v", parquet)
	}
	destination := filepath.Join(t.TempDir(), "merged.jsonl")
	manifest := RunArchiveManifest{}
	if err := mergeRemoteArchiveTransactions(destination, &manifest, []remoteArchiveBundle{{
		target: "a", transactionsPath: filepath.Join(record.ArchivePath, "transactions.jsonl"), manifest: RunArchiveManifest{TransactionCount: 2},
	}}, nil); err != nil {
		t.Fatal(err)
	}
	merged := readTransactionRows(t, destination)
	if len(merged) != 2 || merged[1].VUserID != 12 || merged[1].Iteration != 3 || !merged[1].Interrupted || merged[1].VUserLifetime != 77 || merged[1].ElapsedMs != 3000 {
		t.Fatalf("merged = %+v", merged)
	}
	// A row archived before identity existed decodes as outside iterations.
	var legacy TransactionEvent
	if err := json.Unmarshal([]byte(`{"action_name":"login","duration_ms":1,"success":true}`), &legacy); err != nil || legacy.Iteration != 0 || legacy.VUserID != 0 || legacy.Interrupted {
		t.Fatalf("legacy = %+v %v", legacy, err)
	}
}
