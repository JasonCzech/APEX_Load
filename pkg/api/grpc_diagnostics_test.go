package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	localParquet "github.com/xitongsys/parquet-go-source/local"
	parquetReader "github.com/xitongsys/parquet-go/reader"
)

func TestGRPCDiagnosticsDistributedAndLocalSummariesAgree(t *testing.T) {
	first, ok := 0.0, int32(0)
	evidence := []*protocol.GRPCDiagnostics{
		{Method: "/example.Service/Call", Mode: "unary", CallMs: 5, FirstMessageMs: &first, StatusCode: &ok, MessagesReceived: 1},
		{Method: "/example.Service/Call", Mode: "unary", CallMs: 20},
	}
	var local *protocol.GRPCSummary
	remote := &TxMetrics{}
	for _, d := range evidence {
		protocol.MergeGRPCDiagnostic(&local, d)
		mergeDistributedServerTelemetry(remote, &generatorrpc.ServerTelemetry{GRPC: d})
	}
	finalizeAPIServerTelemetrySummary(remote.ServerTelemetry)
	projected := cloneEngineServerTelemetrySummary(&engine.ServerTelemetrySummary{GRPC: local})
	if !reflect.DeepEqual(projected.GRPC, remote.ServerTelemetry.GRPC) {
		t.Fatalf("local and distributed diagnostics differ: %+v / %+v", projected.GRPC, remote.ServerTelemetry.GRPC)
	}
	clone := cloneTxMetricsMap(map[string]*TxMetrics{"call": remote})
	remote.ServerTelemetry.GRPC.Call.Buckets[0]++
	if reflect.DeepEqual(clone["call"].ServerTelemetry.GRPC, remote.ServerTelemetry.GRPC) {
		t.Fatal("snapshot shares diagnostic summary")
	}
}

func TestGRPCDiagnosticsArchiveJSONRetainsOptionalZeros(t *testing.T) {
	zero, status := 0.0, int32(0)
	event := TransactionEvent{ServerTelemetry: &protocol.ServerTelemetry{GRPC: &protocol.GRPCDiagnostics{
		Method: "/example.Service/Call", Mode: "unary", CallMs: 1, FirstMessageMs: &zero, StatusCode: &status, MessagesReceived: 1,
	}}}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var restored TransactionEvent
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event.ServerTelemetry.GRPC, restored.ServerTelemetry.GRPC) {
		t.Fatalf("archive lost optional evidence: %s", encoded)
	}
}

func TestGRPCMissingResponseDoesNotDiluteDistributedFirstEventAverage(t *testing.T) {
	first := 5.0
	d := &protocol.GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary", CallMs: 10, FirstMessageMs: &first, MessagesSent: 1, MessagesReceived: 1}
	m := &TxMetrics{}
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{GRPC: d, FirstEventMs: first, MessagesSent: 1, MessagesReceived: 1})
	d = d.Clone()
	d.FirstMessageMs, d.MessagesReceived = nil, 0
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{GRPC: d, MessagesSent: 1})
	finalizeAPIServerTelemetrySummary(m.ServerTelemetry)
	s := m.ServerTelemetry
	if s.Samples != 2 || s.FirstEventSamples != 1 || s.AvgFirstEventMs != first {
		t.Fatalf("missing response diluted distributed average: %+v", s)
	}
}

func TestGRPCDiagnosticsSurviveJSONLToParquetArchiveConversion(t *testing.T) {
	zero, ok := 0.0, int32(0)
	events := []TransactionEvent{
		{ServerTelemetry: &protocol.ServerTelemetry{GRPC: &protocol.GRPCDiagnostics{
			Method: "/example.Service/Call", Mode: "unary", CallMs: 1,
			FirstMessageMs: &zero, StatusCode: &ok, MessagesReceived: 1,
		}}},
		{ServerTelemetry: &protocol.ServerTelemetry{GRPC: &protocol.GRPCDiagnostics{
			Method: "/example.Service/Watch", Mode: "server_stream", CallMs: 2, ReceiveLimitReached: true,
		}}},
		{}, // Historical transactions have no typed diagnostics.
	}
	var jsonl bytes.Buffer
	for i := range events {
		sanitizeArchivedTransaction(&events[i])
		if err := json.NewEncoder(&jsonl).Encode(events[i]); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "transactions.jsonl"), filepath.Join(dir, "transactions.parquet")
	if err := os.WriteFile(input, jsonl.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	count, err := streamJSONLToParquet(input, output, func(event TransactionEvent) parquetTransactionEvent {
		return toParquetTransactions([]TransactionEvent{event})[0]
	})
	if err != nil || count != int64(len(events)) {
		t.Fatalf("archive conversion count=%d err=%v", count, err)
	}
	f, err := localParquet.NewLocalFileReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := parquetReader.NewParquetReader(f, new(parquetTransactionEvent), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.ReadStop()
	if r.GetNumRows() != int64(len(events)) {
		t.Fatalf("archive rows=%d", r.GetNumRows())
	}
	rows := make([]parquetTransactionEvent, len(events))
	if err := r.Read(&rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		var telemetry *protocol.ServerTelemetry
		if err := json.Unmarshal([]byte(row.ServerTelemetryJSON), &telemetry); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(telemetry, events[i].ServerTelemetry) {
			t.Fatalf("row %d lost diagnostic availability: %s", i, row.ServerTelemetryJSON)
		}
	}
}
