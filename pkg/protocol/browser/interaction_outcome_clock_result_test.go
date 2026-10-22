package browser

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
	"testing"
	"time"
)

func TestBrowserOutcomeClockFreezesResultBeforeLaterCancellation(t *testing.T) {
	now := time.Now().UTC()
	valid := &protocol.BrowserOutcomeClock{Version: 1, StartedAt: now, DeadlineAt: now.Add(time.Second), ObservedAt: now.Add(10 * time.Millisecond), BudgetMS: 1000, ElapsedMS: 10}
	expired := *valid
	expired.ObservedAt = now.Add(2 * time.Second)
	expired.ElapsedMS = 2000
	for _, tc := range []struct {
		name        string
		reached     bool
		ctxErr, err error
		clock       *protocol.BrowserOutcomeClock
		want        string
	}{
		{"met", true, nil, nil, valid, protocol.InteractionOutcomeMet},
		{"missed", true, nil, playwright.ErrTimeout, &expired, protocol.InteractionOutcomeMissed},
		{"early adapter timeout", true, nil, playwright.ErrTimeout, valid, protocol.InteractionOutcomeNotReached},
		{"parent canceled", true, context.Canceled, nil, valid, protocol.InteractionOutcomeNotReached},
		{"parent expired", true, context.DeadlineExceeded, context.DeadlineExceeded, valid, protocol.InteractionOutcomeNotReached},
		{"configuration error", true, nil, errors.New("invalid selector"), valid, protocol.InteractionOutcomeNotReached},
		{"not reached", false, nil, nil, valid, protocol.InteractionOutcomeNotReached},
		{"clock unavailable", true, nil, nil, nil, protocol.InteractionOutcomeNotReached},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			sync := browserInteractionSync{reached: tc.reached, err: tc.err, contextErr: tc.ctxErr, clock: tc.clock}
			outcome := protocol.BrowserInteractionOutcome{Declared: "duration", DeadlineMS: 1000}
			got := browserInteractionOutcomeV3(outcome, sync)
			if got.Observed != tc.want {
				t.Fatalf("got %s want %s", got.Observed, tc.want)
			}
			if legacy := browserInteractionOutcomeV2(canceled, outcome, sync); legacy.Observed != protocol.InteractionOutcomeNotReached {
				t.Fatal("changed legacy semantics")
			}
		})
	}
	late := *valid
	late.ObservedAt = now.Add(2 * time.Second)
	late.ElapsedMS = 2000
	got := browserInteractionOutcomeV3(protocol.BrowserInteractionOutcome{Declared: "duration", DeadlineMS: 1000}, browserInteractionSync{reached: true, clock: &late})
	if got.Observed != protocol.InteractionOutcomeNotReached || got.Clock == nil {
		t.Fatal("late success invented a before-deadline result")
	}
}
