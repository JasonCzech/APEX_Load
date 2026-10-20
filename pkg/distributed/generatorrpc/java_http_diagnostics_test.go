package generatorrpc

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_http"
	"google.golang.org/protobuf/proto"
)

func javaWireDiagnostic() *protocol.JavaHTTPDiagnostics {
	zero, decoded := 0.0, int64(0)
	return &protocol.JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "empty", RequestDecodedBytes: &decoded, PreparedRequestBodyBytes: &decoded, AppliedRequestCompression: "none", ResponseState: "complete", ResponseRetainedBytes: &decoded, ResponseBase64Bytes: &decoded, Base64EncodeMs: &zero, EnclosingMs: 1, ObjectFootprint: "unavailable_embedded_opaque_bytes"}
}

func TestJavaHTTPDiagnosticsWirePreservesOptionalZerosAndRefusals(t *testing.T) {
	for _, original := range []*protocol.JavaHTTPDiagnostics{nil, javaWireDiagnostic(), {Version: 9, Mode: "secret-payload"}} {
		data, err := proto.Marshal(&pb.ServerTelemetry{JavaHttp: javaHTTPDiagnosticsToProto(original)})
		if err != nil {
			t.Fatal(err)
		}
		var restored pb.ServerTelemetry
		if err = proto.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		got := javaHTTPDiagnosticsFromProto(restored.JavaHttp)
		if !reflect.DeepEqual(got, original.Retained()) {
			t.Fatal("presence/refusal changed", got)
		}
		if got != nil && got.RequestDecodedBytes != nil {
			*got.RequestDecodedBytes = 7
			if *original.RequestDecodedBytes != 0 || *restored.JavaHttp.RequestDecodedBytes != 0 {
				t.Fatal("wire copy aliases input")
			}
		}
		if strings.Contains(string(data), "secret-payload") {
			t.Fatal("invalid payload retained")
		}
	}
	for _, peer := range []*pb.JavaHTTPDiagnostics{{Version: 2, Mode: "secret-payload"}, {Version: 1, Mode: "secret-payload"}} {
		got := javaHTTPDiagnosticsFromProto(peer)
		if got == nil || got.Valid() || got.Version != 0 {
			t.Fatal("refused peer became absent or measured", got)
		}
		data, _ := json.Marshal(got)
		if strings.Contains(string(data), "secret-payload") {
			t.Fatal("invalid peer content retained")
		}
	}
}

func TestJavaHTTPDiagnosticsBufferStreamAndRuntimeFeature(t *testing.T) {
	d := javaWireDiagnostic()
	buffer := &transactionBuffer{}
	if err := buffer.RecordTransaction(&protocol.Result{ActionName: "opaque-call", ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: d}}); err != nil {
		t.Fatal(err)
	}
	transactions := buffer.Drain()
	if len(transactions) != 1 || !reflect.DeepEqual(transactions[0].ServerTelemetry.JavaHTTP, d) {
		t.Fatal("buffer lost diagnostics", transactions)
	}
	*d.Base64EncodeMs = 99
	if *transactions[0].ServerTelemetry.JavaHTTP.Base64EncodeMs != 0 {
		t.Fatal("buffer aliases producer")
	}
	wire := (&Service{}).metricsBatchForMetrics("run", 1, GeneratorStats{}, transactions, metricsAggregateSet{}, false, nil, "RUNNING", "", nil)
	data, err := proto.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.MetricsBatch
	if err = proto.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	got, err := (&MetricsStream{stream: oneTimingBatchStream{batch: &decoded}}).Receive()
	if err != nil || len(got.Transactions) != 1 || !reflect.DeepEqual(got.Transactions[0].ServerTelemetry.JavaHTTP, transactions[0].ServerTelemetry.JavaHTTP) {
		t.Fatal("actual stream lost diagnostic representation", err)
	}
	available, features, _ := runtimeAdvertisementWithCapacities(context.Background(), false, nil, nil)
	if !slices.Contains(available, "java_http") || !slices.Contains(features, FeatureJavaHTTPDiagnosticsV1) {
		t.Fatal("available Java adapter lacks advertised contract")
	}
}
