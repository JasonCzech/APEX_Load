package generatorrpc

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A generator's archive keeps the VUser iteration and interruption journey
// accounting reads, in JSONL and Parquet.
func TestGeneratorArchiveRecordsVUserIterationIdentity(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	recorder, err := newArchiveRecorder("identity-generator", "identity", "identity")
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*protocol.Result{
		{ActionName: "login", Success: true, VUserID: 1_000_007, Iteration: 2},
		{ActionName: "pay", VUserID: 1_000_007, Iteration: 2, Interrupted: true, VUserLifetime: 5, WallElapsed: 2 * time.Second},
	} {
		if err := recorder.RecordResult(result); err != nil {
			t.Fatal(err)
		}
	}
	record, err := recorder.Finalize("COMPLETE", time.Now().UTC(), "")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(record.ArchivePath, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var rows []archiveTransactionEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		var row archiveTransactionEvent
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 2 || rows[0].VUserID != 1_000_007 || rows[0].Iteration != 2 || rows[0].Interrupted || !rows[1].Interrupted || rows[1].Iteration != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[1].VUserLifetime != 5 || rows[1].ElapsedMs != 2000 {
		t.Fatalf("lifetime/elapsed rows = %+v", rows)
	}
	parquet := toParquetTransactions(rows)
	if parquet[0].VUserID != 1_000_007 || parquet[0].Iteration != 2 || parquet[0].Interrupted || !parquet[1].Interrupted || parquet[1].VUserLifetime != 5 || parquet[1].ElapsedMs != 2000 {
		t.Fatalf("parquet = %+v", parquet)
	}
}
