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

func browserInteractionOutcomeV4Sync(outcome protocol.BrowserInteractionOutcome, sync browserInteractionSync) *protocol.BrowserInteractionOutcome {
	if outcome.Declared == "" {
		return nil
	}
	deadlineMS := outcome.DeadlineMS
	if deadlineMS <= 0 {
		deadlineMS = 1
	}
	out := protocol.BrowserInteractionOutcome{
		Kind:       protocol.InteractionOutcomeKindSync,
		Declared:   outcome.Declared,
		DeadlineMS: deadlineMS,
		Observed:   protocol.InteractionOutcomeNotReached,
	}
	if !supportedBrowserOutcomeWait(outcome.Declared) {
		out.Observed = protocol.InteractionOutcomeUnsupported
		out.Reason = "unsupported_wait"
		return &out
	}
	if !sync.reached {
		out.Reason = "action_failed"
		return &out
	}
	if sync.contextErr != nil || errors.Is(sync.err, context.Canceled) {
		out.Reason = "context_canceled"
		return &out
	}
	if sync.clock == nil || !sync.clock.Valid() || sync.clock.BudgetMS != deadlineMS {
		out.Reason = "clock_unavailable"
		return &out
	}
	if sync.err == nil {
		out.Clock = sync.clock.Clone()
		if !out.Clock.ObservedAt.After(out.Clock.DeadlineAt) {
			out.Observed = protocol.InteractionOutcomeMet
		} else {
			out.Observed = protocol.InteractionOutcomeMissed
			out.Reason = "deadline_missed"
		}
		return &out
	}
	if errors.Is(sync.err, playwright.ErrTimeout) || errors.Is(sync.err, context.DeadlineExceeded) {
		if !sync.clock.ObservedAt.Before(sync.clock.DeadlineAt) {
			out.Observed = protocol.InteractionOutcomeMissed
			out.Reason = "deadline_missed"
			out.Clock = sync.clock.Clone()
		} else {
			out.Reason = "clock_skew"
		}
		return &out
	}
	out.Reason = "wait_error"
	return &out
}

func browserInteractionOutcomesV4(syncBase protocol.BrowserInteractionOutcome, sync browserInteractionSync, dialogOutcome, downloadOutcome *protocol.BrowserInteractionOutcome, assertionOutcomes []protocol.BrowserInteractionOutcome, navOutcome *protocol.BrowserInteractionOutcome) []protocol.BrowserInteractionOutcome {
	var outcomes []protocol.BrowserInteractionOutcome
	appendBounded := func(o *protocol.BrowserInteractionOutcome) {
		if o == nil || len(outcomes) >= protocol.MaxBrowserInteractionOutcomes {
			return
		}
		outcomes = append(outcomes, o.Clone())
	}
	appendBounded(browserInteractionOutcomeV4Sync(syncBase, sync))
	appendBounded(dialogOutcome)
	appendBounded(downloadOutcome)
	for i := range assertionOutcomes {
		appendBounded(&assertionOutcomes[i])
	}
	appendBounded(navOutcome)
	if len(outcomes) == 0 {
		outcomes = []protocol.BrowserInteractionOutcome{{
			Kind:     protocol.InteractionOutcomeKindSync,
			Observed: protocol.InteractionOutcomeNotDeclared,
		}}
	}
	return outcomes
}
