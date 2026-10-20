package generatorrpc

import (
	"reflect"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/protobuf/proto"
)

func TestJavaHTTPActionSummarySurvivesActualEngineAndMetricsStream(t *testing.T) {
	collector := engine.NewMetricsCollector(16)
	collector.Start(time.Hour)
	collector.Record(&protocol.Result{ActionName: "java-call", Success: true, Duration: time.Millisecond, ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: javaWireDiagnostic()}})
	collector.RecordTransactionResult(&protocol.Result{ActionName: "wrapper", BusinessTransaction: true, Success: true, Duration: time.Millisecond, ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: javaWireDiagnostic()}})
	collector.Stop()
	snapshot, ok := collector.LatestSnapshot()
	if !ok {
		t.Fatal("no final engine snapshot")
	}
	values := engineActionMetricsToAggregates(snapshot.ByAction)
	var action *MetricAggregate
	for i := range values {
		if values[i].Name == "java-call" {
			action = &values[i]
		}
	}
	if action == nil || action.JavaHTTP == nil || action.JavaHTTP.Samples != 1 || action.JavaHTTP.Incomplete {
		t.Fatal("actual Java action summary missing", values)
	}
	for _, value := range engineActionMetricsToAggregates(snapshot.ByTransaction) {
		if value.JavaHTTP != nil {
			t.Fatal("synthetic transaction duplicated action diagnostics", value.Name)
		}
	}
	wire := &pb.MetricsBatch{ActionMetrics: metricAggregatesToProto(values)}
	data, err := proto.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.MetricsBatch
	if err = proto.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	got, err := (&MetricsStream{stream: oneTimingBatchStream{batch: &decoded}}).Receive()
	if err != nil {
		t.Fatal(err)
	}
	var restored *protocol.JavaHTTPSummary
	for _, value := range got.ActionMetrics {
		if value.Name == "java-call" {
			restored = value.JavaHTTP
		}
	}
	if !reflect.DeepEqual(restored, action.JavaHTTP) {
		t.Fatal("action publication changed sufficient statistics", restored, action.JavaHTTP)
	}
	*action.JavaHTTP = protocol.JavaHTTPSummary{}
	if restored.Samples != 1 {
		t.Fatal("wire summary aliases producer")
	}
}

func TestJavaHTTPActionSummaryMixedPeersAndInvalidShapesStayIncomplete(t *testing.T) {
	var s *protocol.JavaHTTPSummary
	protocol.MergeJavaHTTPDiagnostic(&s, javaWireDiagnostic())
	for _, reverse := range []bool{false, true} {
		values := []MetricAggregate{{Name: "call", Count: 1, JavaHTTP: s}, {Name: "call", Count: 1}}
		if reverse {
			values[0], values[1] = values[1], values[0]
		}
		merged := mergeMetricAggregates(values)
		if merged.Count != 2 || merged.JavaHTTP == nil || !merged.JavaHTTP.Incomplete || merged.JavaHTTP.Samples != 1 || merged.JavaHTTP.RequestDecoded.Count != 1 {
			t.Fatal("mixed peers diluted zero or qualified missing data", merged)
		}
	}
	for _, mutate := range []func(*pb.JavaHTTPSummary){
		func(p *pb.JavaHTTPSummary) { p.Version = 2 }, func(p *pb.JavaHTTPSummary) { p.EnclosingMs = nil },
		func(p *pb.JavaHTTPSummary) { p.Base64DecodeMs.Buckets = make([]int64, 145) }, func(p *pb.JavaHTTPSummary) { p.Base64DecodeMs.Buckets = []int64{0} },
		func(p *pb.JavaHTTPSummary) { p.RequestDecoded.SumBytes = -1 }, func(p *pb.JavaHTTPSummary) { p.Base64Work.Samples = 99 },
	} {
		p := javaHTTPSummaryToProto(s)
		mutate(p)
		got := javaHTTPSummaryFromProto(p)
		if got == nil || !got.Incomplete || got.Samples != 0 || !got.Valid() {
			t.Fatal("invalid publication became absent or measured", got)
		}
	}
	if javaHTTPSummaryFromProto(nil) != nil {
		t.Fatal("legacy absence became zero summary")
	}
	if got := javaHTTPSummaryForCount(s, 0); got == nil || !got.Incomplete || got.Samples != 0 {
		t.Fatal("diagnostics exceeded authoritative action count", got)
	}
}

func TestJavaHTTPGeneratorIntervalArchiveProjection(t *testing.T) {
	var s *protocol.JavaHTTPSummary
	protocol.MergeJavaHTTPDiagnostic(&s, javaWireDiagnostic())
	batch := &pb.MetricsBatch{ActionMetrics: []*pb.MetricAggregate{metricAggregateToProto(&MetricAggregate{Name: "request", Count: 1, JavaHTTP: s})}}
	evidence := archiveJavaHTTPFromBatch(batch)
	if evidence == nil || evidence.Incomplete || evidence.Actions["request"].Diagnostics.Samples != 1 {
		t.Fatal("generator action archive missing", evidence)
	}
	rows := toParquetSummaries([]archiveIntervalSummary{{JavaHTTP: evidence}})
	if len(rows) != 1 || !reflect.DeepEqual(protocol.ParseJavaHTTPActionsJSON(rows[0].JavaHTTPJSON), evidence) {
		t.Fatal("generator parquet projection lost diagnostics")
	}
	if archiveJavaHTTPFromBatch(&pb.MetricsBatch{}) != nil {
		t.Fatal("old peer invented diagnostics")
	}
}
