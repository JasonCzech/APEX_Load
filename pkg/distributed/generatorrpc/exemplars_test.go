package generatorrpc

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestMetricAggregateCarriesTraceExemplars(t *testing.T) {
	at := time.UnixMilli(1_790_000_000_000).UTC()
	exemplars := []engine.TraceExemplar{
		{TraceID: fmt.Sprintf("%032x", 1), SpanID: fmt.Sprintf("%016x", 1), DurationMs: 812.5, Success: true, Reason: engine.ExemplarSlowestSuccess, Timestamp: at},
		{TraceID: fmt.Sprintf("%032x", 2), DurationMs: 15, StatusCode: 503, Reason: engine.ExemplarFirstError, Timestamp: at},
	}
	decoded := metricAggregateFromProto(metricAggregateToProto(&MetricAggregate{Name: "checkout", Count: 2, Exemplars: exemplars}))
	if !reflect.DeepEqual(decoded.Exemplars, exemplars) {
		t.Fatalf("round trip = %+v", decoded.Exemplars)
	}
	// Malformed or excess exemplars from a peer are dropped, never repaired.
	wire := &pb.MetricAggregate{Name: "checkout", Count: 1, Exemplars: []*pb.TraceExemplar{
		{TraceId: strings.Repeat("0", 32), Reason: engine.ExemplarSample},
		{TraceId: "not-hex", Reason: engine.ExemplarSample},
		{TraceId: fmt.Sprintf("%032x", 3), Reason: "favourite"},
		{TraceId: fmt.Sprintf("%032x", 4), Reason: engine.ExemplarSample, DurationMs: -1},
		{TraceId: fmt.Sprintf("%032x", 5), Reason: engine.ExemplarSample},
		{TraceId: fmt.Sprintf("%032x", 6), Reason: engine.ExemplarSample},
	}}
	kept := metricAggregateFromProto(wire).Exemplars
	if len(kept) != 1 || kept[0].TraceID != fmt.Sprintf("%032x", 5) {
		t.Fatalf("kept = %+v", kept)
	}
}

func TestGeneratorAdvertisesExemplars(t *testing.T) {
	aggregates := engineActionMetricsToAggregates(map[string]*engine.ActionMetrics{"checkout": {Name: "checkout", Count: 1, Exemplars: []engine.TraceExemplar{{TraceID: fmt.Sprintf("%032x", 7), Reason: engine.ExemplarSample}}}})
	if len(aggregates) != 1 || len(aggregates[0].Exemplars) != 1 {
		t.Fatalf("aggregates = %+v", aggregates)
	}
}
