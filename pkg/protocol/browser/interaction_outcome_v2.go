package browser

import (
	"context"
	"errors"
	"strings"

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
	if !supportedBrowserOutcomeWait(outcome.Declared) {
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

func supportedBrowserOutcomeWait(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "network_idle", "networkidle", "load", "domcontentloaded", "element_visible", "visible", "duration", "sleep", "dom_stable":
		return true
	default:
		return false
	}
}

// V3 uses the context state captured immediately when the supported wait
// returned. Later capture, post-action delay or cancellation cannot rewrite it.
// An invalid clock or a success first observed after its deadline cannot prove
// the authored condition was satisfied within its observation window.
func browserInteractionOutcomeV3(outcome protocol.BrowserInteractionOutcome, sync browserInteractionSync) protocol.BrowserInteractionOutcome {
	if outcome.Declared == "" {
		return outcome
	}
	outcome.Observed = protocol.InteractionOutcomeNotReached
	outcome.Clock = sync.clock.Clone()
	if outcome.Clock == nil || !supportedBrowserOutcomeWait(outcome.Declared) || !sync.reached || sync.contextErr != nil || errors.Is(sync.err, context.Canceled) {
		return outcome
	}
	if sync.err == nil {
		if !outcome.Clock.ObservedAt.After(outcome.Clock.DeadlineAt) {
			outcome.Observed = protocol.InteractionOutcomeMet
		}
	} else if errors.Is(sync.err, playwright.ErrTimeout) || errors.Is(sync.err, context.DeadlineExceeded) {
		if !outcome.Clock.ObservedAt.Before(outcome.Clock.DeadlineAt) {
			outcome.Observed = protocol.InteractionOutcomeMissed
		}
	}
	return outcome
}
