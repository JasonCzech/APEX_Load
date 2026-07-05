package browser

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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

const (
	browserSelfHealOff        = "off"
	browserSelfHealReplayOnly = "replay_only"
	browserSelfHealAlways     = "always"
	browserRunModeReplay      = "replay"
	browserRunModeLoad        = "load"
)

type selectorSelfHealStats struct {
	Attempts      int
	ProbeFailures int
	CacheHit      bool
}

func (s selectorSelfHealStats) budgetSpent() int {
	return s.Attempts + s.ProbeFailures
}

// runWithSelectorSelfHeal executes a selector-driven action and, when it fails
// and the action carries ranked selector candidates (recorded by any capture
// surface under source_selector_fallbacks), retries with the strongest usable
// alternates. A successful heal is recorded as structured protocol evidence so
// the diagnose endpoint and dashboard can suggest promoting the fallback.
func (b *Browser) runWithSelectorSelfHeal(ctx context.Context, action *protocol.Action, result *protocol.Result, primary string, run func(string) error) error {
	stats := selectorSelfHealStats{}
	key := selfHealActionIdentity(action, primary)
	tried := make(map[string]struct{})
	if action != nil && b.selectorSelfHealEnabled() {
		if cached, ok := b.cachedSelfHealCandidate(key); ok {
			stats.CacheHit = true
			if healed, ok := b.trySelfHealCandidate(ctx, action, primary, cached, run, &stats, tried, false); ok {
				b.recordSelfHeal(action, result, primary, healed, cached, stats)
				return nil
			}
			b.forgetSelfHealCandidate(key)
		}
	}

	err := run(primary)
	if err == nil || ctx.Err() != nil || action == nil {
		return err
	}
	if !b.selectorSelfHealEnabled() || !isSelectorNotFoundError(err) {
		return err
	}

	if !b.selectorSelfHealFallbacksEnabled() {
		b.recordSelfHealMetrics(action, result, primary, false, stats)
		return err
	}

	fallbacks := selector.FromMetadataValue(action.Metadata[selector.MetadataFallbacksKey])
	if len(fallbacks) == 0 {
		fallbacks = selector.FallbacksForPrimary(selector.FromMetadataValue(action.Metadata[selector.MetadataCandidatesKey]), primary)
	}
	if len(fallbacks) == 0 {
		b.recordSelfHealMetrics(action, result, primary, false, stats)
		return err
	}
	for _, candidate := range fallbacks {
		if stats.budgetSpent() >= maxHealAttempts || ctx.Err() != nil {
			break
		}
		if healed, ok := b.trySelfHealCandidate(ctx, action, primary, candidate, run, &stats, tried, true); ok {
			b.storeSelfHealCandidate(key, candidate)
			b.recordSelfHeal(action, result, primary, healed, candidate, stats)
			return nil
		}
	}
	b.recordSelfHealMetrics(action, result, primary, false, stats)
	return err
}

func (b *Browser) trySelfHealCandidate(
	ctx context.Context,
	action *protocol.Action,
	primary string,
	candidate selector.Candidate,
	run func(string) error,
	stats *selectorSelfHealStats,
	tried map[string]struct{},
	probe bool,
) (string, bool) {
	engineSelector, ok := selector.Usable(candidate)
	if !ok || strings.TrimSpace(engineSelector) == strings.TrimSpace(primary) {
		return "", false
	}
	if tried != nil {
		if _, ok := tried[engineSelector]; ok {
			return "", false
		}
		tried[engineSelector] = struct{}{}
	}
	if probe && !b.selectorPresent(ctx, action, engineSelector) {
		stats.ProbeFailures++
		return "", false
	}
	stats.Attempts++
	if healErr := run(engineSelector); healErr == nil {
		return engineSelector, true
	}
	return "", false
}

// selectorPresent probes a candidate with a short visibility wait, honoring the
// action's frame context.
func (b *Browser) selectorPresent(ctx context.Context, action *protocol.Action, engineSelector string) bool {
	if locator, ok := b.actionLocator(action, engineSelector); ok {
		return b.waitLocatorVisible(ctx, locator, healProbeTimeout) == nil
	}
	page := b.currentPage()
	if page == nil {
		return false
	}
	return b.waitLocatorVisible(ctx, page.Locator(engineSelector), healProbeTimeout) == nil
}

func (b *Browser) recordSelfHeal(action *protocol.Action, result *protocol.Result, primary string, healed string, candidate selector.Candidate, stats selectorSelfHealStats) {
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
	b.recordSelfHealMetrics(action, result, primary, true, stats)
	evidence := map[string]any{
		"action":           action.Name,
		"primary_selector": primary,
		"healed_selector":  healed,
		"candidate_kind":   candidate.Kind,
		"confidence":       candidate.Confidence,
		"reason":           candidate.Reason,
	}
	if seq := actionMetadataString(action.Metadata, "recorder_step_seq"); seq != "" {
		evidence["recorder_step_seq"] = seq
	}
	if repoID := actionMetadataString(action.Metadata, "object_ref_repository"); repoID != "" {
		evidence["object_ref_repository"] = repoID
	}
	if logicalName := actionMetadataString(action.Metadata, "object_ref_logical_name"); logicalName != "" {
		evidence["object_ref_logical_name"] = logicalName
	}
	result.ProtocolEvidence["selector_self_heal"] = evidence
}

func (b *Browser) recordSelfHealMetrics(action *protocol.Action, result *protocol.Result, primary string, success bool, stats selectorSelfHealStats) {
	if result == nil {
		return
	}
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	metrics := map[string]any{
		"action":           action.Name,
		"primary_selector": primary,
		"attempts":         stats.Attempts,
		"probe_failures":   stats.ProbeFailures,
		"cache_hit":        stats.CacheHit,
		"success":          success,
	}
	result.ProtocolEvidence["selector_self_heal_metrics"] = metrics
	result.ProtocolEvidence["self_heal_metrics"] = metrics
}

func (b *Browser) selectorSelfHealEnabled() bool {
	switch b.selfHealMode {
	case browserSelfHealOff:
		return false
	case browserSelfHealReplayOnly, browserSelfHealAlways, "":
		return true
	default:
		return false
	}
}

func (b *Browser) selectorSelfHealFallbacksEnabled() bool {
	switch b.selfHealMode {
	case browserSelfHealOff:
		return false
	case browserSelfHealAlways:
		return true
	case browserSelfHealReplayOnly, "":
		return b.runMode != browserRunModeLoad
	default:
		return false
	}
}

func normalizeBrowserSelfHealMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "replay", "replay_only", "replay-only", "memoized":
		return browserSelfHealReplayOnly
	case "always", "on", "true", "enabled":
		return browserSelfHealAlways
	case "off", "false", "disabled", "none":
		return browserSelfHealOff
	default:
		return ""
	}
}

func normalizeBrowserRunMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "replay", "debug", "shakeout":
		return browserRunModeReplay
	case "load", "run", "vuser":
		return browserRunModeLoad
	default:
		return ""
	}
}

func isSelectorNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "strict mode violation") {
		return false
	}
	if strings.Contains(msg, "selector not found") || strings.Contains(msg, "element not found") || strings.Contains(msg, "no node found") {
		return true
	}
	if strings.Contains(msg, "waiting for locator") || strings.Contains(msg, "locator(") {
		return true
	}
	return strings.Contains(msg, "timeout")
}

func selfHealActionIdentity(action *protocol.Action, primary string) string {
	if action == nil {
		return strings.TrimSpace(primary)
	}
	parts := []string{strings.TrimSpace(action.Type), strings.TrimSpace(primary)}
	for _, key := range []string{"recorder_step_seq", "source_step_id", "source_step_index", "source_action", "source_url"} {
		if value := actionMetadataString(action.Metadata, key); value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	if len(parts) == 2 {
		parts = append(parts, strings.TrimSpace(action.Name), strings.TrimSpace(action.Target), strings.TrimSpace(action.Selector))
	}
	return strings.Join(parts, "\x00")
}

func (b *Browser) cachedSelfHealCandidate(key string) (selector.Candidate, bool) {
	if strings.TrimSpace(key) == "" {
		return selector.Candidate{}, false
	}
	b.selfHealMu.Lock()
	defer b.selfHealMu.Unlock()
	if b.selfHealCache == nil {
		return selector.Candidate{}, false
	}
	candidate, ok := b.selfHealCache[key]
	return candidate, ok
}

func (b *Browser) storeSelfHealCandidate(key string, candidate selector.Candidate) {
	if strings.TrimSpace(key) == "" {
		return
	}
	b.selfHealMu.Lock()
	defer b.selfHealMu.Unlock()
	if b.selfHealCache == nil {
		b.selfHealCache = make(map[string]selector.Candidate)
	}
	b.selfHealCache[key] = candidate
}

func (b *Browser) forgetSelfHealCandidate(key string) {
	if strings.TrimSpace(key) == "" {
		return
	}
	b.selfHealMu.Lock()
	defer b.selfHealMu.Unlock()
	delete(b.selfHealCache, key)
}

func actionMetadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
