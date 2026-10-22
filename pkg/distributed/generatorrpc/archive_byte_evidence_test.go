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

// The generator keeps the byte evidence its HTTP adapter set, so the
// controller reading this generator's archive can tell a measured zero from
// a count an earlier generator never labelled, or a step that was not a
// request (NA-C).
func TestGeneratorArchiveStampsHTTPByteEvidence(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	recorder, err := newArchiveRecorder("byte-generator", "byte-evidence", "byte-evidence")
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*protocol.Result{
		{ActionName: "http", DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, Success: true, BytesSent: 0, BytesReceived: 512},
		{ActionName: "check", DependencyType: "http", ByteEvidence: protocol.ByteEvidenceNone, Success: true},
		{ActionName: "grpc", DependencyType: "grpc", Success: true, BytesSent: 10, BytesReceived: 20},
		{ActionName: "transaction", DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, BusinessTransaction: true, Success: true, BytesReceived: 512},
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
	rows := map[string]map[string]any{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		row := map[string]any{}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		rows[row["action_name"].(string)] = row
	}
	if row := rows["http"]; row["byte_evidence"] != protocol.ByteEvidenceHTTPBody || row["bytes_received"] != float64(512) {
		t.Fatalf("http row = %v, want http_body.v1 evidence", row)
	}
	if row := rows["check"]; row["byte_evidence"] != protocol.ByteEvidenceNone {
		t.Fatalf("check row = %v, want no-request evidence", row)
	}
	for _, name := range []string{"grpc", "transaction"} {
		if _, labelled := rows[name]["byte_evidence"]; labelled {
			t.Fatalf("%s row = %v, want no byte evidence", name, rows[name])
		}
	}
	if events := toParquetTransactions([]archiveTransactionEvent{{ByteEvidence: protocol.ByteEvidenceHTTPBody}}); events[0].ByteEvidence != protocol.ByteEvidenceHTTPBody {
		t.Fatal("the Parquet copy lost the byte evidence")
	}
}
