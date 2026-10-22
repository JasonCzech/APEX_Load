package generatorrpc

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/protobuf/proto"
)

// The largest joint publication a generator can build, every bound at its
// limit (JointMaxKeys tuples with JointMaxKeyBytes keys, the other bucket,
// TDigestMaxCentroids digests, DurationSketchMaxBuckets sketches, the
// longest projected dimensions and the widest scalars), fits the
// controller's joint envelope, so its joint check never refuses a valid
// publication whole for its size. The tuples go through the same group merge
// a generator publishes from.
func TestJointLargestPublicationFitsTheEnvelope(t *testing.T) {
	// One full-size distribution shape: every wire tuple shares it, which
	// proto.Size counts per tuple without allocating 513 copies.
	digest := &pb.TDigestSnapshot{Compression: 200, TotalWeight: math.MaxFloat64, Min: 1, Max: math.MaxFloat64, Count: math.MaxInt64}
	for i := range engine.TDigestMaxCentroids {
		digest.Centroids = append(digest.Centroids, &pb.TDigestCentroid{Mean: math.MaxFloat64 - float64(i), Weight: math.MaxFloat64})
	}
	sketch := &pb.DurationSketchSnapshot{Version: engine.DurationSketchSnapshotVersion, Alpha: 0.01, Count: math.MaxUint64, Sum: math.MaxFloat64, Min: 1, Max: math.MaxFloat64, ZeroCount: math.MaxUint64, Collapsed: true}
	for i := range engine.DurationSketchMaxBuckets {
		sketch.Bins = append(sketch.Bins, &pb.DurationSketchBin{Index: math.MinInt32 + int32(i), Count: math.MaxUint64})
	}
	long := time.Duration(math.MaxInt64)
	metrics := func(name string) *engine.ActionMetrics {
		return &engine.ActionMetrics{Name: name, Count: math.MaxInt64, Errors: math.MaxInt64, AvgDuration: long, P50: long, P90: long, P95: long, P99: long}
	}
	// A projected property category may carry the longest admitted name.
	dimensions := []string{"browser_platform", "network_profile", "property:" + strings.Repeat("p", 128)}
	if err := (engine.JointProjection{Population: "transaction", Dimensions: dimensions}).Validate(); err != nil {
		t.Fatal(err)
	}
	snapshot := &engine.JointMetricsSnapshot{Version: engine.JointEvidenceVersion, Population: "transaction", Dimensions: dimensions, Tuples: map[string]*engine.ActionMetrics{},
		Other: metrics("other"), OmittedTuples: math.MaxInt64, Incomplete: true}
	for i := range engine.JointMaxKeys {
		// Labels of escaped quotes (two bytes each once encoded) fill the key
		// up to its limit, each within the label limit.
		suffix := fmt.Sprintf("%010d", i)
		room := engine.JointMaxKeyBytes - len(`["`) - len(`","`)*2 - len(`"]`)
		labels := make([]string, 3)
		for n := range labels {
			quotes := max(0, min(engine.JointMaxLabelBytes-len(suffix), (room-len(suffix))/2))
			labels[n] = strings.Repeat(`"`, quotes) + suffix
			room -= 2*quotes + len(suffix)
		}
		key, ok := engine.JointTupleKey(labels)
		if !ok || len(key) < engine.JointMaxKeyBytes-1 {
			t.Fatalf("key %d: %d bytes of %d, ok %v", i, len(key), engine.JointMaxKeyBytes, ok)
		}
		snapshot.Tuples[key] = metrics(key)
	}
	aggregates, info := jointAggregatesFromEngine(snapshot)
	merged := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"group": {Joint: aggregates, JointInfo: info}})
	if len(merged.Joint) != engine.JointMaxKeys || merged.JointInfo == nil || merged.JointInfo.Other == nil {
		t.Fatalf("merged %d tuples, info %+v", len(merged.Joint), merged.JointInfo)
	}
	wire := make([]*pb.MetricAggregate, 0, len(merged.Joint))
	for i := range merged.Joint {
		aggregate := metricAggregateToProto(&merged.Joint[i])
		aggregate.DurationDigest, aggregate.DurationSketch = digest, sketch
		wire = append(wire, aggregate)
	}
	protoInfo := jointInfoToProto(merged.JointInfo)
	protoInfo.Other.DurationDigest, protoInfo.Other.DurationSketch = digest, sketch
	size := proto.Size(&pb.MetricsBatch{JointMetricsInfo: protoInfo, JointMetricsV4: wire})
	if size > maxJointEncodedBytes {
		t.Fatalf("the largest publication encodes to %d bytes, past the %d-byte envelope", size, maxJointEncodedBytes)
	}
	if !admitJointPublicationProto(protoInfo, wire, maxJointEncodedBytes) {
		t.Fatal("the largest valid publication is refused")
	}
	t.Logf("largest joint publication: %d bytes (%.1f%% of the envelope)", size, 100*float64(size)/maxJointEncodedBytes)
}

// A joint tuple is published with only the fields joint evidence reads, even
// when the engine's metrics carry diagnostics, including after the group
// merge a generator publishes from.
func TestJointAggregateCarriesOnlyJointFields(t *testing.T) {
	metrics := &engine.ActionMetrics{Name: `["login"]`, Count: 3, Errors: 1, AvgDuration: 20 * time.Millisecond, P99: 30 * time.Millisecond,
		BytesSent: 100, BytesRecv: 200, AvgDNSMs: 1, CorrelationIDs: []string{"c"}, Exemplars: []engine.TraceExemplar{{TraceID: "t"}},
		ServerTelemetry: &engine.ServerTelemetrySummary{}, DependencyType: "java_http", DependencyTarget: "orders"}
	aggregates, info := jointAggregatesFromEngine(&engine.JointMetricsSnapshot{Version: engine.JointEvidenceVersion, Population: "action", Dimensions: []string{"name"},
		Tuples: map[string]*engine.ActionMetrics{metrics.Name: metrics}})
	want := MetricAggregate{Name: `["login"]`, Count: 3, Errors: 1, AvgMs: 20, P99Ms: 30}
	if len(aggregates) != 1 || fmt.Sprintf("%+v", aggregates[0]) != fmt.Sprintf("%+v", want) {
		t.Fatalf("aggregate %+v, want %+v", aggregates, want)
	}
	merged := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": {Joint: aggregates, JointInfo: info}, "b": {Joint: aggregates, JointInfo: info}})
	if len(merged.Joint) != 1 {
		t.Fatalf("merged %+v", merged.Joint)
	}
	got := merged.Joint[0]
	if got.Count != 6 || got.Errors != 2 || len(got.Exemplars) != 0 || got.JavaHTTP != nil || got.Messaging != nil || got.BytesSent != 0 || got.AvgDNSMs != 0 || got.DependencyType != "" {
		t.Fatalf("merged tuple carries fields joint evidence does not read: %+v", got)
	}
}
