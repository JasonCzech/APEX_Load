package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

func TestEvaluateGoalControllerRedeliveryRateBreach(t *testing.T) {
	maxRate := 5.0
	now := time.Date(2026, time.June, 21, 12, 0, 0, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 20, &LiveMetrics{
		TPS: 100,
		Transactions: map[string]*TxMetrics{
			"jms_consume orders": {
				Name:            "jms_consume orders",
				DependencyType:  "jms_consume",
				ServerTelemetry: &ServerTelemetrySummary{RedeliveryRatePct: 12},
			},
		},
	}, &GoalConfig{
		MaxRedeliveryRatePct: &maxRate,
		StopOnSLABreach:      true,
		SLABreachWindow:      "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop || update.Status == nil || !update.Status.RedeliveryRateBreached {
		t.Fatalf("expected redelivery-rate breach to stop, got %#v", update.Status)
	}
	if update.StopReason != "messaging redelivery rate SLA breached" {
		t.Fatalf("unexpected stop reason: %q", update.StopReason)
	}
}

func TestEvaluateGoalControllerDeadLetterRateBreach(t *testing.T) {
	maxRate := 1.0
	now := time.Date(2026, time.June, 21, 12, 1, 0, 0, time.UTC)
	breachStarted := now.Add(-31 * time.Second)

	update := evaluateGoalController(now, 20, &LiveMetrics{
		TPS: 100,
		Transactions: map[string]*TxMetrics{
			"kafka_consume t": {
				Name:            "kafka_consume t",
				DependencyType:  "kafka_consumer_group",
				ServerTelemetry: &ServerTelemetrySummary{DeadLetterRatePct: 4},
			},
		},
	}, &GoalConfig{
		MaxDeadLetterRatePct: &maxRate,
		StopOnSLABreach:      true,
		SLABreachWindow:      "30s",
	}, &goalControllerState{breachStartedAt: &breachStarted})

	if !update.ShouldStop || update.Status == nil || !update.Status.DeadLetterRateBreached {
		t.Fatalf("expected dead-letter-rate breach to stop, got %#v", update.Status)
	}
	if update.StopReason != "messaging dead-letter rate SLA breached" {
		t.Fatalf("unexpected stop reason: %q", update.StopReason)
	}
}

func TestEvaluateGoalControllerRatesNoBreachUnderThreshold(t *testing.T) {
	maxRedeliver := 10.0
	maxDLQ := 5.0
	now := time.Date(2026, time.June, 21, 12, 2, 0, 0, time.UTC)
	update := evaluateGoalController(now, 20, &LiveMetrics{
		TPS: 100,
		Transactions: map[string]*TxMetrics{
			"jms_consume orders": {
				DependencyType:  "jms_consume",
				ServerTelemetry: &ServerTelemetrySummary{RedeliveryRatePct: 2, DeadLetterRatePct: 1},
			},
		},
	}, &GoalConfig{MaxRedeliveryRatePct: &maxRedeliver, MaxDeadLetterRatePct: &maxDLQ, StopOnSLABreach: true}, &goalControllerState{})
	if update.Status.RedeliveryRateBreached || update.Status.DeadLetterRateBreached || update.ShouldStop {
		t.Fatalf("did not expect a breach under thresholds, got %#v", update.Status)
	}
}

func TestCloneGoalConfigPreservesRateThresholds(t *testing.T) {
	r, d := 5.0, 1.0
	clone := cloneGoalConfig(&GoalConfig{MaxRedeliveryRatePct: &r, MaxDeadLetterRatePct: &d})
	if clone.MaxRedeliveryRatePct == &r || clone.MaxDeadLetterRatePct == &d {
		t.Fatal("rate thresholds must be deep-copied")
	}
	if *clone.MaxRedeliveryRatePct != 5 || *clone.MaxDeadLetterRatePct != 1 || !hasGoalConfig(clone) {
		t.Fatalf("clone wrong: %+v", clone)
	}
}

// TestMergeDistributedReliabilityRatesPostMerge confirms rates are recomputed
// from pooled counts across generators, not averaged from per-generator rates.
func TestMergeDistributedReliabilityRatesPostMerge(t *testing.T) {
	m := &TxMetrics{}
	// Generator A: 60 received, 6 redelivered. Generator B: 40 received, 0.
	for i := 0; i < 60; i++ {
		mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesReceived: 1, ResponsePayloadKB: 1})
	}
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{RedeliveryCount: 6})
	for i := 0; i < 40; i++ {
		mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesReceived: 1, ResponsePayloadKB: 1})
	}
	finalizeAPIServerTelemetrySummary(m.ServerTelemetry)
	// Pooled: 6 / 100 = 6%, not (10% + 0%)/2 = 5%.
	if got := m.ServerTelemetry.RedeliveryRatePct; got < 5.99 || got > 6.01 {
		t.Fatalf("pooled RedeliveryRatePct = %v, want 6 (recomputed from pooled counts)", got)
	}
}
