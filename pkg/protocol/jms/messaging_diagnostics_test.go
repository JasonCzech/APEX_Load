package jms

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
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
			if err != nil || result.ServerTelemetry.AcknowledgementMs == nil || *result.ServerTelemetry.AcknowledgementMs < 0 || result.ServerTelemetry.DeadLetteredCount != 1 {
				t.Fatal("settlement evidence lost", err, result)
			}
		}
	}
}
