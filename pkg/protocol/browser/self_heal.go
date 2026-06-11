package browser

import (
	"context"
	"log/slog"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/selector"
)

// healProbeTimeout bounds the visibility probe per fallback candidate so a
// failing action with several absent alternates cannot multiply the action
// timeout.
const healProbeTimeout = 2 * time.Second

// maxHealAttempts caps how many ranked fallbacks one action will try.
const maxHealAttempts = 3

// runWithSelectorSelfHeal executes a selector-driven action and, when it fails
// and the action carries ranked selector candidates (recorded by any capture
// surface under source_selector_fallbacks), retries with the strongest usable
// alternates. A successful heal is recorded as structured protocol evidence so
// the diagnose endpoint and dashboard can suggest promoting the fallback.
func (b *Browser) runWithSelectorSelfHeal(ctx context.Context, action *protocol.Action, result *protocol.Result, primary string, run func(string) error) error {
	err := run(primary)
	if err == nil || ctx.Err() != nil || action == nil {
		return err
	}
	fallbacks := selector.FromMetadataValue(action.Metadata[selector.MetadataFallbacksKey])
	if len(fallbacks) == 0 {
		return err
	}
	attempts := 0
	for _, candidate := range fallbacks {
		if attempts >= maxHealAttempts || ctx.Err() != nil {
			break
		}
		engineSelector, ok := selector.Usable(candidate)
		if !ok || engineSelector == primary {
			continue
		}
		attempts++
		if !b.selectorPresent(action, engineSelector) {
			continue
		}
		if healErr := run(engineSelector); healErr == nil {
			b.recordSelfHeal(action, result, primary, engineSelector, candidate)
			return nil
		}
	}
	return err
}

// selectorPresent probes a candidate with a short visibility wait, honoring the
// action's frame context.
func (b *Browser) selectorPresent(action *protocol.Action, engineSelector string) bool {
	if locator, ok := b.actionLocator(action, engineSelector); ok {
		return b.waitLocatorVisible(locator, healProbeTimeout) == nil
	}
	if b.pwPage == nil {
		return false
	}
	return b.waitLocatorVisible(b.pwPage.Locator(engineSelector), healProbeTimeout) == nil
}

func (b *Browser) recordSelfHeal(action *protocol.Action, result *protocol.Result, primary string, healed string, candidate selector.Candidate) {
	slog.Warn("browser selector self-healed",
		"action", action.Name,
		"primary_selector", primary,
		"healed_selector", healed,
		"candidate_kind", candidate.Kind,
		"candidate_confidence", candidate.Confidence)
	if result == nil {
		return
	}
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence["selector_self_heal"] = map[string]any{
		"action":           action.Name,
		"primary_selector": primary,
		"healed_selector":  healed,
		"candidate_kind":   candidate.Kind,
		"confidence":       candidate.Confidence,
		"reason":           candidate.Reason,
	}
}
