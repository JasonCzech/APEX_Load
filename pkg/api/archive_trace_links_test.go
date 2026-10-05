package api

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestRunArchiveWriterRecordsTraceLinks(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "trace-links", Name: "Trace links"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	link := protocol.TraceLink{TraceID: sentTraceID, SpanID: sentSpanID, Kind: protocol.TraceLinkRedelivery, DeliveryCount: 2}
	if err := writer.RecordResult(&protocol.Result{ActionName: "consume", StartTime: start, Duration: time.Millisecond, Success: true, TraceLinks: []protocol.TraceLink{link}, TraceLinksTruncated: true}); err != nil {
		t.Fatal(err)
	}
	record, err := writer.Finalize("COMPLETE", start.Add(time.Minute), "", false, false, nil)
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
	var row TransactionEvent
	if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if len(row.TraceLinks) != 1 || row.TraceLinks[0] != link || !row.TraceLinksTruncated {
		t.Fatalf("archived links = %+v truncated %v", row.TraceLinks, row.TraceLinksTruncated)
	}
	if encoded := parquetTraceLinks(row.TraceLinks); !strings.Contains(encoded, `"kind":"redelivery"`) || parquetTraceLinks(nil) != "" {
		t.Fatalf("parquet trace links = %q", encoded)
	}
}

func TestTestUsesMessagingTraceCarrier(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	carrier := &dsl.Script{Config: dsl.ScriptConfig{Observability: dsl.ObservabilityConfig{TraceCarrier: "W3C"}}}
	runner.tests["on"] = &ManagedTest{ID: "on", Groups: []TestGroup{{Name: "plain", script: &dsl.Script{}}, {Name: "events", script: carrier}}}
	runner.tests["off"] = &ManagedTest{ID: "off", Groups: []TestGroup{{Name: "plain", script: &dsl.Script{}}}}
	if !runner.testUsesMessagingTraceCarrier("on") || runner.testUsesMessagingTraceCarrier("off") || runner.testUsesMessagingTraceCarrier("missing") {
		t.Fatal("trace carrier detection")
	}
}
