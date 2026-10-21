package browser

import (
	"context"
	"errors"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

// Only an evaluated supported wait can establish its authored outcome. A
// canceled run, vanished page or selector/configuration error is not a missed
// target deadline; historical v1 interpretation is retained by its reader.
func browserInteractionOutcomeV2(ctx context.Context, outcome protocol.BrowserInteractionOutcome, sync browserInteractionSync) protocol.BrowserInteractionOutcome {
	if outcome.Declared == "" {
		return outcome
	}
	outcome.Observed = protocol.InteractionOutcomeNotReached
	switch outcome.Declared {
	case "network_idle", "networkidle", "load", "domcontentloaded", "element_visible", "visible", "duration", "sleep", "dom_stable":
	default:
		return outcome
	}
	if !sync.reached || ctx.Err() != nil || errors.Is(sync.err, context.Canceled) {
		return outcome
	}
	if sync.err == nil {
		outcome.Observed = protocol.InteractionOutcomeMet
	} else if errors.Is(sync.err, playwright.ErrTimeout) || errors.Is(sync.err, context.DeadlineExceeded) {
		outcome.Observed = protocol.InteractionOutcomeMissed
	}
	return outcome
}
