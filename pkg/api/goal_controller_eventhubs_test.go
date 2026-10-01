package api

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func TestEvaluateGoalControllerEventHubsEndToEndLatencyBreach(t *testing.T) {
	maxE2E := 200.0
	now := time.Date(2026, time.June, 21, 12, 0, 45, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 20, &LiveMetrics{
		TPS: 500,
		Transactions: map[string]*TxMetrics{
			"eventhubs_produce telemetry": {
				Name:            "eventhubs_produce telemetry",
				DependencyType:  "eventhubs",
				ServerTelemetry: &ServerTelemetrySummary{AvgEndToEndLatencyMs: 350},
			},
		},
	}, &GoalConfig{
		MaxEndToEndLatencyMs: &maxE2E,
		StopOnSLABreach:      true,
		SLABreachWindow:      "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop {
		t.Fatal("expected sustained E2E latency breach to stop")
	}
	if update.Status == nil || !update.Status.EndToEndLatencyBreached {
		t.Fatalf("expected EndToEndLatencyBreached, got %#v", update.Status)
	}
	if update.StopReason != "Event Hubs end-to-end latency SLA breached" {
		t.Fatalf("unexpected stop reason: %q", update.StopReason)
	}
}

func TestEvaluateGoalControllerEventHubsPartitionSkewBreach(t *testing.T) {
	maxSkew := 50.0
	now := time.Date(2026, time.June, 21, 12, 1, 0, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 20, &LiveMetrics{
		TPS: 500,
		Transactions: map[string]*TxMetrics{
			"eventhubs_produce telemetry": {
				Name:            "eventhubs_produce telemetry",
				DependencyType:  "eventhubs",
				ServerTelemetry: &ServerTelemetrySummary{Messaging: &protocol.MessagingSummary{Dimensions: []protocol.MessagingDimension{{Source: "hub", Partition: "0", Operations: 1, Sent: 9}, {Source: "hub", Partition: "1", Operations: 1, Sent: 1}}}},
			},
		},
	}, &GoalConfig{
		MaxPartitionSkewPct: &maxSkew,
		StopOnSLABreach:     true,
		SLABreachWindow:     "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop || update.Status == nil || !update.Status.PartitionSkewBreached {
		t.Fatalf("expected partition skew breach to stop, got %#v", update.Status)
	}
	if update.StopReason != "Event Hubs partition skew SLA breached" {
		t.Fatalf("unexpected stop reason: %q", update.StopReason)
	}
}

func TestEvaluateGoalControllerThrottledRequestsBreachIsAggregate(t *testing.T) {
	maxThrottle := 5
	now := time.Date(2026, time.June, 21, 12, 2, 0, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	// Two producer transactions each below the threshold individually (3 + 4),
	// but the run-total (7) exceeds it — the SLA binds on the aggregate.
	update := evaluateGoalController(now, 20, &LiveMetrics{
		TPS: 500,
		Transactions: map[string]*TxMetrics{
			"eventhubs_produce a": {Name: "a", DependencyType: "eventhubs", ServerTelemetry: &ServerTelemetrySummary{TotalThrottledRequests: 3}},
			"eventhubs_produce b": {Name: "b", DependencyType: "eventhubs", ServerTelemetry: &ServerTelemetrySummary{TotalThrottledRequests: 4}},
		},
	}, &GoalConfig{
		MaxThrottledRequests: &maxThrottle,
		StopOnSLABreach:      true,
		SLABreachWindow:      "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop || update.Status == nil || !update.Status.ThrottledRequestsBreached {
		t.Fatalf("expected aggregate throttle breach to stop, got %#v", update.Status)
	}
	if update.StopReason != "Event Hubs throttled-request SLA breached" {
		t.Fatalf("unexpected stop reason: %q", update.StopReason)
	}
}

func TestEvaluateGoalControllerEventHubsNoBreachUnderThreshold(t *testing.T) {
	maxThrottle := 5
	maxE2E := 200.0
	maxSkew := 50.0
	now := time.Date(2026, time.June, 21, 12, 3, 0, 0, time.UTC)

	update := evaluateGoalController(now, 20, &LiveMetrics{
		TPS: 500,
		Transactions: map[string]*TxMetrics{
			"eventhubs_produce telemetry": {
				Name:           "eventhubs_produce telemetry",
				DependencyType: "eventhubs",
				ServerTelemetry: &ServerTelemetrySummary{
					TotalThrottledRequests: 2,
					AvgEndToEndLatencyMs:   100,
					PartitionSkewPct:       10,
				},
			},
		},
	}, &GoalConfig{
		MaxThrottledRequests: &maxThrottle,
		MaxEndToEndLatencyMs: &maxE2E,
		MaxPartitionSkewPct:  &maxSkew,
		StopOnSLABreach:      true,
	}, &goalControllerState{})

	if update.Status == nil {
		t.Fatal("nil status")
	}
	if update.Status.ThrottledRequestsBreached || update.Status.EndToEndLatencyBreached || update.Status.PartitionSkewBreached {
		t.Fatalf("did not expect any EH breach under thresholds, got %#v", update.Status)
	}
	if update.ShouldStop {
		t.Fatal("did not expect stop when under all thresholds")
	}
}

func TestCloneGoalConfigPreservesEventHubsThresholds(t *testing.T) {
	maxThrottle := 5
	maxE2E := 200.0
	maxSkew := 50.0
	original := &GoalConfig{
		MaxThrottledRequests: &maxThrottle,
		MaxEndToEndLatencyMs: &maxE2E,
		MaxPartitionSkewPct:  &maxSkew,
	}
	clone := cloneGoalConfig(original)
	if clone.MaxThrottledRequests == original.MaxThrottledRequests {
		t.Fatal("MaxThrottledRequests pointer must be deep-copied")
	}
	if *clone.MaxThrottledRequests != 5 || *clone.MaxEndToEndLatencyMs != 200 || *clone.MaxPartitionSkewPct != 50 {
		t.Fatalf("clone values wrong: %+v", clone)
	}
	if !hasGoalConfig(clone) {
		t.Fatal("hasGoalConfig should be true with EH thresholds set")
	}
}
