package jms

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

type diagnosticSettlementAdapter struct {
	brokerAdapter
	settlementAdapter
	err   error
	calls int
}

func (a *diagnosticSettlementAdapter) Settle(context.Context, string, string, settleOptions) error {
	a.calls++
	return a.err
}
func TestMessagingSettlementTimingOnlyOnSuccessfulAdapterAcknowledgement(t *testing.T) {
	for _, failed := range []bool{false, true} {
		a := &diagnosticSettlementAdapter{}
		if failed {
			a.err = errors.New("broker unavailable")
		}
		j := &JMS{adapter: a}
		result := &protocol.Result{Extractions: map[string]string{}}
		err := j.settleAdvanced(context.Background(), &protocol.Action{Type: "jms_settle", Params: map[string]any{"settlement_token": "opaque", "disposition": "dead_letter"}}, result)
		if a.calls != 1 {
			t.Fatal("unexpected extra traffic")
		}
		if failed {
			if err == nil || result.ServerTelemetry != nil {
				t.Fatal("failed acknowledgement invented")
			}
		} else {
			if err != nil || result.ServerTelemetry.AcknowledgementMs == nil || *result.ServerTelemetry.AcknowledgementMs < 0 || result.ServerTelemetry.DeadLetteredCount != 1 || !result.ServerTelemetry.Messaging.Valid() || result.ServerTelemetry.Messaging.Durations[0].Metric != "settlement" {
				t.Fatal("settlement evidence lost", err, result)
			}
		}
	}
}

func TestMessagingJMSMessageAgeAvailabilityIncludesObservedZero(t *testing.T) {
	observedAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, sample := range []struct {
		name      string
		createdAt time.Time
		observed  bool
		lag       float64
	}{{"zero", observedAt, true, 0}, {"positive", observedAt.Add(-20 * time.Millisecond), true, 20}, {"missing", time.Time{}, false, 0}, {"future", observedAt.Add(time.Second), false, 0}} {
		t.Run(sample.name, func(t *testing.T) {
			result := &protocol.Result{StartTime: observedAt.Add(-time.Millisecond), Duration: time.Millisecond, Extractions: map[string]string{}}
			tel := finalizeJMSServerTelemetry(result, message{}, false, message{CreatedAt: sample.createdAt}, true)
			if tel.ConsumerLagObserved != sample.observed || tel.ConsumerLagMs != sample.lag || !tel.Messaging.Valid() || tel.Messaging.Durations[0].ValueMs != nil {
				t.Fatalf("age availability: %+v", tel)
			}
			var summary *protocol.MessagingSummary
			protocol.MergeMessagingTelemetry(&summary, tel, "queue", "jms_queue", "group")
			mean, available := summary.Dimensions[0].Age.Mean()
			if available != sample.observed || mean != sample.lag {
				t.Fatalf("observed moments: %+v", summary)
			}
		})
	}
}

func TestMessagingJMSMockProducerDoesNotInventBrokerAcknowledgement(t *testing.T) {
	telemetry := finalizeJMSServerTelemetry(&protocol.Result{}, message{}, true, message{}, false)
	if !telemetry.Messaging.Valid() || telemetry.Messaging.Durations[0].Metric != "producer_ack" || telemetry.Messaging.Durations[0].ValueMs != nil || telemetry.Messaging.Durations[0].UnavailableReason != "not_observed" {
		t.Fatalf("mock/local completion promoted: %+v", telemetry)
	}
}
