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

func TestGeneratorArchiveRecordsTraceLinks(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	recorder, err := newArchiveRecorder("trace-generator", "links", "links")
	if err != nil {
		t.Fatal(err)
	}
	link := protocol.TraceLink{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7", Kind: protocol.TraceLinkConsumed}
	if err := recorder.RecordResult(&protocol.Result{ActionName: "consume", Success: true, TraceLinks: []protocol.TraceLink{link}}); err != nil {
		t.Fatal(err)
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
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	if !scanner.Scan() {
		t.Fatal("no archived row")
	}
	var row struct {
		TraceLinks []protocol.TraceLink `json:"trace_links"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &row); err != nil || len(row.TraceLinks) != 1 || row.TraceLinks[0] != link {
		t.Fatalf("generator archive links = %+v %v", row.TraceLinks, err)
	}
}
