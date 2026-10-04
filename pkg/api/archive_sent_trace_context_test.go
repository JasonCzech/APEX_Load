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

const (
	sentTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	sentTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	sentSpanID      = "00f067aa0ba902b7"
)

func TestApplySentTraceContext(t *testing.T) {
	tests := []struct {
		name        string
		correlation string
		sent        *protocol.SentTraceContext
		wantTrace   string
		wantSent    *bool
	}{
		{
			name:        "gateway request id keeps the sent trace",
			correlation: "gateway-request-7",
			sent:        protocol.NewSentTraceContext(sentTraceparent, protocol.TraceSourceGenerated, true),
			wantTrace:   sentTraceID,
			wantSent:    ptr(true),
		},
		{
			name:        "cache hit archives no trace",
			correlation: sentTraceparent,
			sent:        protocol.NewSentTraceContext(sentTraceparent, protocol.TraceSourceGenerated, false),
			wantSent:    ptr(false),
		},
		{
			name:        "unrecorded context falls back to the correlation id",
			correlation: sentTraceparent,
			wantTrace:   sentTraceID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := TransactionEvent{CorrelationID: tt.correlation}
			applySentTraceContext(&event, tt.sent)
			// Finalization runs again after collection; it must not re-derive.
			finalizeTransactionEventDerivedFields(&event, 0)
			if event.TraceID != tt.wantTrace {
				t.Fatalf("TraceID = %q, want %q", event.TraceID, tt.wantTrace)
			}
			if (event.TraceSent == nil) != (tt.wantSent == nil) || (event.TraceSent != nil && *event.TraceSent != *tt.wantSent) {
				t.Fatalf("TraceSent = %v, want %v", event.TraceSent, tt.wantSent)
			}
			if tt.sent != nil && (event.SentTraceID != sentTraceID || event.SentSpanID != sentSpanID || event.TraceSource != protocol.TraceSourceGenerated) {
				t.Fatalf("sent fields = %q %q %q", event.SentTraceID, event.SentSpanID, event.TraceSource)
			}
		})
	}
}

func TestLegacyArchiveRowWithoutSentContextStillDerivesTrace(t *testing.T) {
	var event TransactionEvent
	if err := json.Unmarshal([]byte(`{"correlation_id":"`+sentTraceparent+`","duration_ms":1,"success":true}`), &event); err != nil {
		t.Fatal(err)
	}
	finalizeTransactionEventDerivedFields(&event, 0)
	if event.TraceSent != nil || event.TraceID != sentTraceID || event.SpanID != sentSpanID {
		t.Fatalf("legacy row = %#v, want trace derived from correlation_id and trace_sent absent", event)
	}
	row := toParquetTransactions([]TransactionEvent{event})[0]
	if row.TraceSent != "" {
		t.Fatalf("parquet trace_sent = %q, want empty for an unrecorded context", row.TraceSent)
	}
}

func TestRunArchiveWriterRecordsSentTraceContext(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "sent-trace", Name: "Sent trace"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.October, 4, 12, 0, 0, 0, time.UTC)
	for i, sent := range []bool{true, false} {
		if err := writer.RecordResult(&protocol.Result{
			ActionName:    "request",
			StartTime:     start.Add(time.Duration(i) * time.Second),
			Duration:      time.Millisecond,
			Success:       true,
			CorrelationID: "gateway-request-7",
			TraceContext:  protocol.NewSentTraceContext(sentTraceparent, protocol.TraceSourceGenerated, sent),
		}); err != nil {
			t.Fatal(err)
		}
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
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].TraceID != sentTraceID || rows[0].TraceSent == nil || !*rows[0].TraceSent {
		t.Fatalf("sent row = %#v, want the sent trace despite the gateway request id", rows[0])
	}
	if rows[1].TraceID != "" || rows[1].TraceSent == nil || *rows[1].TraceSent || rows[1].SentTraceID != sentTraceID {
		t.Fatalf("cache-hit row = %#v, want no archived trace but the recorded unsent context", rows[1])
	}
}
