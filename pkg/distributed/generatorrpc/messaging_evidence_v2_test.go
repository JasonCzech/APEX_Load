package generatorrpc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func testMessagingV2Diagnostic(value float64) *protocol.MessagingDiagnostics {
	return &protocol.MessagingDiagnostics{Version: 2, Durations: []protocol.MessagingDurationObservation{{Metric: "producer_ack", ValueMs: &value}}}
}

func TestMessagingV2RawWireAndClone(t *testing.T) {
	raw := &protocol.ServerTelemetry{MessagesSent: 1, Messaging: testMessagingV2Diagnostic(0)}
	dto := cloneServerTelemetry(raw)
	*dto.Messaging.Durations[0].ValueMs = 10
	if *raw.Messaging.Durations[0].ValueMs != 0 {
		t.Fatal("raw diagnostic pointer aliased")
	}
	dto = cloneServerTelemetry(raw)
	batch := (&Service{}).metricsBatchForMetrics("run", 1, GeneratorStats{}, []TransactionMetric{{Name: "send", ServerTelemetry: dto}}, metricsAggregateSet{}, false, nil, "running", "", nil)
	got, err := (&MetricsStream{stream: oneTimingBatchStream{batch: batch}}).Receive()
	if err != nil {
		t.Fatal(err)
	}
	d := got.Transactions[0].ServerTelemetry.Messaging
	if !d.Valid() || *d.Durations[0].ValueMs != 0 {
		t.Fatalf("measured zero lost: %+v", d)
	}
	restored := got.Transactions[0].ServerTelemetry.MessagingTelemetry()
	*restored.Messaging.Durations[0].ValueMs = 12
	if *d.Durations[0].ValueMs != 0 {
		t.Fatal("DTO projection aliased")
	}
}

func TestMessagingV2RejectsMalformedWireWithoutSilentFallback(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		version   uint32
	}{
		{"missing", "", 2}, {"malformed", "{", 2}, {"unsupported", "{}", 3},
		{"zero version", messagingDiagnosticsJSON(testMessagingV2Diagnostic(1)), 0},
		{"oversized", strings.Repeat("x", maxMessagingDiagnosticsBytes+1), 2},
		{"invalid population", `{"version":2,"durations":[{"metric":"server_time","value_ms":1}]}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := messagingDiagnosticsFromJSON(tc.raw, tc.version)
			if d == nil || d.Valid() {
				t.Fatal("invalid wire silently absent or accepted")
			}
			var s *protocol.MessagingSummary
			protocol.MergeMessagingTelemetry(&s, &protocol.ServerTelemetry{MessagesSent: 1, Messaging: d}, "topic", "kafka")
			if s == nil || !s.Incomplete || s.Dimensions[0].Sent != 1 {
				t.Fatalf("known legacy evidence or invalid coverage lost: %+v", s)
			}
		})
	}
	if messagingDiagnosticsFromJSON("", 0) != nil {
		t.Fatal("legacy absence manufactured evidence")
	}
}

func TestMessagingV2AggregateRoundTripAndMixedCoverage(t *testing.T) {
	var s *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&s, &protocol.ServerTelemetry{MessagesSent: 1, Messaging: testMessagingV2Diagnostic(20)}, "topic", "kafka", "group")
	values := engineActionMetricsToAggregates(map[string]*engine.ActionMetrics{"send": {Name: "send", Count: 1, ServerTelemetry: &engine.ServerTelemetrySummary{Messaging: s}}})
	if len(values) != 1 || values[0].MessagingVersion != 2 {
		t.Fatalf("wrong captured version: %+v", values)
	}
	got := metricAggregateFromProto(metricAggregateToProto(&values[0]))
	if got.MessagingVersion != 2 || !got.Messaging.Valid() || got.Messaging.Dimensions[0].Evidence.Durations[0].Distribution.Count != 1 {
		t.Fatalf("v2 evidence lost: %+v", got)
	}
	var legacy *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&legacy, &protocol.ServerTelemetry{MessagesSent: 1}, "topic", "kafka", "group")
	merged := mergeMetricAggregates([]MetricAggregate{values[0], {Name: "send", Count: 1, MessagingVersion: 1, Messaging: legacy}})
	if merged.MessagingVersion != 2 || !merged.Messaging.Incomplete || merged.Messaging.Dimensions[0].Sent != 2 {
		t.Fatalf("mixed cohort falsely qualified: %+v", merged)
	}
	if d := messagingSummaryFromJSON(messagingSummaryJSON(s), 1); d == nil || !d.Incomplete {
		t.Fatal("v2 payload accepted as v1 wire")
	}
	if d := messagingSummaryFromJSON(messagingSummaryJSON(s), 3); d == nil || !d.Incomplete {
		t.Fatal("unknown wire dropped qualification gap")
	}
}

func TestMessagingV2EmptyClaimedSummaryPoisonsCohortCoverage(t *testing.T) {
	var good *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&good, &protocol.ServerTelemetry{MessagesSent: 1, Messaging: testMessagingV2Diagnostic(2)}, "topic", "kafka")
	for _, version := range []uint32{2, 3} {
		absent := messagingSummaryFromJSON("", version)
		if absent == nil || !absent.Incomplete {
			t.Fatalf("version %d absence ignored", version)
		}
		merged := mergeMetricAggregates([]MetricAggregate{{Name: "send", Count: 1, MessagingVersion: 2, Messaging: good}, {Name: "send", Count: 1, MessagingVersion: version, Messaging: absent}})
		if merged.Messaging == nil || !merged.Messaging.Incomplete || merged.Messaging.Dimensions[0].Sent != 1 {
			t.Fatalf("partial cohort qualified: %+v", merged)
		}
	}
	for _, version := range []uint32{0, 1} {
		if messagingSummaryFromJSON("", version) != nil {
			t.Fatalf("legacy absence manufactured v%d evidence", version)
		}
	}
}

func TestMessagingV2OversizedHistogramPoisonsWireCoverage(t *testing.T) {
	var good *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&good, &protocol.ServerTelemetry{MessagesSent: 1, Messaging: testMessagingV2Diagnostic(0)}, "topic", "kafka")
	var wire map[string]any
	if err := json.Unmarshal([]byte(messagingSummaryJSON(good)), &wire); err != nil {
		t.Fatal(err)
	}
	dimension := wire["dimensions"].([]any)[0].(map[string]any)
	duration := dimension["evidence"].(map[string]any)["durations"].([]any)[0].(map[string]any)
	distribution := duration["distribution_ms"].(map[string]any)
	distribution["buckets"] = append(distribution["buckets"].([]any), float64(1))
	poisoned, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	rejected := messagingSummaryFromJSON(string(poisoned), 2)
	if rejected == nil || !rejected.Incomplete || len(rejected.Dimensions) != 0 {
		t.Fatalf("oversized histogram silently truncated: %+v", rejected)
	}
	merged := mergeMetricAggregates([]MetricAggregate{{Name: "send", Count: 1, MessagingVersion: 2, Messaging: good}, {Name: "send", Count: 1, MessagingVersion: 2, Messaging: rejected}})
	if merged.Messaging == nil || !merged.Messaging.Incomplete || len(merged.Messaging.Dimensions) != 1 || merged.Messaging.Dimensions[0].Sent != 1 {
		t.Fatalf("invalid contributor lost coverage/known observations: %+v", merged.Messaging)
	}
}

func TestMessagingV2NullCounterDoesNotBecomeMeasuredZero(t *testing.T) {
	const raw = `{"version":2,"counters":[{"metric":"send_failure","population":"send_attempt","scope":"client_action","eligible":1,"observed":1,"count":null}]}`
	if got := messagingDiagnosticsFromJSON(raw, 2); got == nil || got.Valid() {
		t.Fatalf("null counter became measured zero: %+v", got)
	}
	const summary = `{"dimensions":[{"protocol":"kafka","source":"events","operations":1,"evidence":{"operations":1,"counters":[{"metric":"send_failure","population":"send_attempt","scope":"client_action","eligible":1,"observed":1,"count":null}]}}]}`
	got := messagingSummaryFromJSON(summary, 2)
	if got == nil || !got.Incomplete || len(got.Dimensions) != 0 {
		t.Fatalf("null aggregate counter became measured zero: %+v", got)
	}
	if valid := messagingSummaryFromJSON(strings.Replace(summary, "null", "0", 1), 2); valid == nil || !valid.Valid() || valid.Incomplete {
		t.Fatalf("measured zero rejected: %+v", valid)
	}
}

func TestMessagingV2OmittedCounterDoesNotBecomeMeasuredZero(t *testing.T) {
	const raw = `{"version":2,"counters":[{"metric":"send_failure","population":"send_attempt","scope":"client_action","eligible":1,"observed":1}]}`
	if got := messagingDiagnosticsFromJSON(raw, 2); got == nil || got.Valid() {
		t.Fatalf("omitted counter became measured zero: %+v", got)
	}
	const summary = `{"dimensions":[{"protocol":"kafka","source":"events","operations":1,"evidence":{"operations":1,"counters":[{"metric":"send_failure","population":"send_attempt","scope":"client_action","eligible":1,"observed":1}]}}]}`
	if got := messagingSummaryFromJSON(summary, 2); got == nil || !got.Incomplete || len(got.Dimensions) != 0 {
		t.Fatalf("omitted aggregate counter became measured zero: %+v", got)
	}
}
