package generatorrpc

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"testing"
)

func TestPropertyAnalyticsCapabilityAndMerge(t *testing.T) {
	script := &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{PropertyAnalytics: []dsl.PropertyAnalytics{{Name: "kind", Extraction: "kind", Values: []string{"purchase"}}}}}}
	if !strings.Contains(requiredRuntimeFeature(script), dsl.FeaturePropertyAnalyticsV1) {
		t.Fatal("old peer could silently drop requested property evidence")
	}
	merged := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"a": {Properties: []MetricAggregate{{Name: "kind=purchase", Count: 2}}}, "b": {Properties: []MetricAggregate{{Name: "kind=purchase", Count: 3}}}})
	if len(merged.Properties) != 1 || merged.Properties[0].Count != 5 {
		t.Fatalf("merge: %+v", merged.Properties)
	}
}

func TestObservedZeroLagSurvivesDistributedEncoding(t *testing.T) {
	telemetry := cloneServerTelemetry(&protocol.ServerTelemetry{ConsumerLagObserved: true, ConsumerOffsetLagObserved: true})
	batch := (&Service{}).metricsBatchForMetrics("run", 1, GeneratorStats{}, []TransactionMetric{{Name: "Consume", ServerTelemetry: telemetry}}, metricsAggregateSet{}, false, nil, "running", "", nil)
	got := batch.Transactions[0].ServerTelemetry
	if !got.GetConsumerLagObserved() || !got.GetConsumerOffsetLagObserved() || got.GetConsumerLagMs() != 0 || got.GetConsumerOffsetLag() != 0 {
		t.Fatalf("observed zero lost: %+v", got)
	}
}
