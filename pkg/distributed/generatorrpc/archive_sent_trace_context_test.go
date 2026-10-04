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

func TestGeneratorArchiveRecordsSentTraceContext(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	recorder, err := newArchiveRecorder("trace-generator", "sent-trace", "sent-trace")
	if err != nil {
		t.Fatal(err)
	}
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	results := []*protocol.Result{
		{ActionName: "sent", Success: true, CorrelationID: "gateway-request-7", TraceContext: protocol.NewSentTraceContext(traceparent, protocol.TraceSourceGenerated, true)},
		{ActionName: "cached", Success: true, CorrelationID: traceparent, TraceContext: protocol.NewSentTraceContext(traceparent, protocol.TraceSourceGenerated, false)},
		{ActionName: "legacy", Success: true, CorrelationID: traceparent},
	}
	for _, result := range results {
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
	if row := rows["sent"]; row["trace_sent"] != true || row["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" || row["trace_source"] != protocol.TraceSourceGenerated {
		t.Fatalf("sent row = %v, want the sent trace beside the gateway request id", row)
	}
	if row := rows["cached"]; row["trace_sent"] != false || row["trace_id"] != nil || row["sent_trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("cached row = %v, want an unsent context and no trace_id", row)
	}
	// Without a recorded context the controller keeps deriving from correlation_id.
	if row := rows["legacy"]; row["trace_sent"] != nil || row["trace_id"] != nil {
		t.Fatalf("legacy row = %v, want no sent-context fields", row)
	}
}
