package browser

import (
	"context"
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

func TestBrowserInteractionV2OutcomeDoesNotInventDeadlineFailures(t *testing.T) {
	for _, tc := range []struct {
		name, declared    string
		reached, canceled bool
		err               error
		want              string
	}{
		{"met", "visible", true, false, nil, protocol.InteractionOutcomeMet},
		{"deadline", "visible", true, false, playwright.ErrTimeout, protocol.InteractionOutcomeMissed},
		{"local deadline", "visible", true, false, context.DeadlineExceeded, protocol.InteractionOutcomeMissed},
		{"run canceled", "visible", true, true, playwright.ErrTimeout, protocol.InteractionOutcomeNotReached},
		{"canceled error", "visible", true, false, context.Canceled, protocol.InteractionOutcomeNotReached},
		{"closed", "visible", true, false, playwright.ErrTargetClosed, protocol.InteractionOutcomeNotReached},
		{"bad selector", "visible", true, false, errors.New("invalid selector"), protocol.InteractionOutcomeNotReached},
		{"unknown wait", "invented", true, false, nil, protocol.InteractionOutcomeNotReached},
		{"not reached", "visible", false, false, nil, protocol.InteractionOutcomeNotReached},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			got := browserInteractionOutcomeV2(ctx, protocol.BrowserInteractionOutcome{Declared: tc.declared, DeadlineMS: 1000}, browserInteractionSync{reached: tc.reached, err: tc.err})
			if got.Observed != tc.want {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
