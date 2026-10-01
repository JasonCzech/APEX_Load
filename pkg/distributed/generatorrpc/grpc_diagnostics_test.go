package generatorrpc

import (
	"reflect"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/protobuf/proto"
)

func TestGRPCDiagnosticsWireRoundTrip(t *testing.T) {
	zero, status := 0.0, int32(0)
	for _, d := range []*protocol.GRPCDiagnostics{
		nil,
		{Method: "/example.Service/Call", Mode: "unary", CallMs: 1, FirstMessageMs: &zero, StatusCode: &status, MessagesReceived: 1},
		{Method: "/example.Service/Watch", Mode: "server_stream", CallMs: 1, ReceiveLimitReached: true},
	} {
		message := &pb.ServerTelemetry{Grpc: grpcDiagnosticsToProto(d)}
		encoded, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		var restored pb.ServerTelemetry
		if err := proto.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		got := grpcDiagnosticsFromProto(restored.Grpc)
		if !reflect.DeepEqual(got, d) {
			t.Fatalf("roundtrip=%+v want=%+v", got, d)
		}
		if got != nil && got.StatusCode != nil {
			*got.StatusCode = 7
			if *restored.Grpc.StatusCode != 0 || *d.StatusCode != 0 {
				t.Fatal("wire conversion aliases input")
			}
		}
	}
	if grpcDiagnosticsFromProto(&pb.GRPCDiagnostics{Method: "invalid"}) != nil {
		t.Fatal("invalid peer evidence accepted")
	}
}

func TestGRPCDiagnosticsSurviveTransactionBufferAndMetricsStream(t *testing.T) {
	zero, status := 0.0, int32(0)
	d := &protocol.GRPCDiagnostics{Method: "/example.Service/Call", Mode: "unary", CallMs: 1, FirstMessageMs: &zero, StatusCode: &status, MessagesReceived: 1}
	buffer := &transactionBuffer{}
	if err := buffer.RecordTransaction(&protocol.Result{ActionName: "call", ServerTelemetry: &protocol.ServerTelemetry{GRPC: d}}); err != nil {
		t.Fatal(err)
	}
	transactions := buffer.Drain()
	*transactions[0].ServerTelemetry.GRPC.StatusCode = 7
	if *d.StatusCode != 0 {
		t.Fatal("buffer shares producer evidence")
	}
	service := &Service{}
	batch := service.metricsBatchForMetrics("run", 1, GeneratorStats{}, transactions, metricsAggregateSet{}, false, nil, "RUNNING", "", nil)
	encoded, err := proto.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	var restored pb.MetricsBatch
	if err := proto.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	stream := &MetricsStream{stream: oneTimingBatchStream{batch: &restored}}
	got, err := stream.Receive()
	if err != nil || len(got.Transactions) != 1 {
		t.Fatalf("received=%+v err=%v", got, err)
	}
	if !reflect.DeepEqual(got.Transactions[0].ServerTelemetry.GRPC, transactions[0].ServerTelemetry.GRPC) {
		t.Fatal("metrics stream dropped or changed typed diagnostic fields")
	}
}
