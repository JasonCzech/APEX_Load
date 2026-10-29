package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/testlog"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/selector"
)

func TestSelectorSelfHealCacheIsBounded(t *testing.T) {
	b := &Browser{}
	b.applyDefaultConfig()
	for index := 0; index < maxSelfHealCacheEntries+100; index++ {
		b.storeSelfHealCandidate(fmt.Sprintf("action-%d", index), selector.Candidate{Kind: "css", Value: "#fallback"})
	}
	b.selfHealMu.Lock()
	size := len(b.selfHealCache)
	b.selfHealMu.Unlock()
	if size > maxSelfHealCacheEntries {
		t.Fatalf("self-heal cache size = %d, max %d", size, maxSelfHealCacheEntries)
	}

	oversized := strings.Repeat("x", maxSelfHealCacheKeyBytes+1)
	b.storeSelfHealCandidate(oversized, selector.Candidate{Kind: "css", Value: "#oversized"})
	if _, ok := b.cachedSelfHealCandidate(oversized); ok {
		t.Fatal("oversized self-heal cache key was retained")
	}
}

func TestSelectorSelfHealSkipsNonSelectorErrors(t *testing.T) {
	b := &Browser{}
	b.applyDefaultConfig()

	calls := 0
	result := &protocol.Result{}
	err := b.runWithSelectorSelfHeal(context.Background(), selfHealTestAction(), result, "#missing", func(string) error {
		calls++
		return errors.New("application handler failed")
	})
	if err == nil {
		t.Fatal("expected original error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want only primary attempt", calls)
	}
	if result.ProtocolEvidence != nil {
		t.Fatalf("ProtocolEvidence = %+v, want none for non-selector error", result.ProtocolEvidence)
	}
}

func TestSelectorSelfHealOffDisablesRetry(t *testing.T) {
	b := &Browser{}
	b.applyDefaultConfig()
	b.selfHealMode = browserSelfHealOff

	calls := 0
	result := &protocol.Result{}
	err := b.runWithSelectorSelfHeal(context.Background(), selfHealTestAction(), result, "#missing", func(string) error {
		calls++
		return errors.New(`Timeout 1000ms exceeded waiting for locator("#missing")`)
	})
	if err == nil {
		t.Fatal("expected original error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want only primary attempt when self-heal is off", calls)
	}
	if result.ProtocolEvidence != nil {
		t.Fatalf("ProtocolEvidence = %+v, want none when self-heal is off", result.ProtocolEvidence)
	}
}

func TestSelectorSelfHealUsesCachedCandidateBeforePrimary(t *testing.T) {
	b := &Browser{}
	b.applyDefaultConfig()
	action := selfHealTestAction()
	cached := selector.Candidate{Kind: "css", Value: "#fallback", Confidence: 95}
	b.storeSelfHealCandidate(selfHealActionIdentity(action, "#missing"), cached)

	var calls []string
	result := &protocol.Result{}
	err := b.runWithSelectorSelfHeal(context.Background(), action, result, "#missing", func(selector string) error {
		calls = append(calls, selector)
		if selector == "#fallback" {
			return nil
		}
		return errors.New(`Timeout 1000ms exceeded waiting for locator("#missing")`)
	})
	if err != nil {
		t.Fatalf("runWithSelectorSelfHeal returned error: %v", err)
	}
	if len(calls) != 1 || calls[0] != "#fallback" {
		t.Fatalf("calls = %v, want cached fallback only", calls)
	}
	metrics, ok := result.ProtocolEvidence["selector_self_heal_metrics"].(map[string]any)
	if !ok {
		t.Fatalf("selector_self_heal_metrics = %#v", result.ProtocolEvidence["selector_self_heal_metrics"])
	}
	if metrics["cache_hit"] != true || metrics["attempts"] != 1 || metrics["success"] != true {
		t.Fatalf("metrics = %#v, want cache hit success with one attempt", metrics)
	}
}

func TestSelectorSelfHealReplayOnlyDoesNotProbeFallbacksDuringLoad(t *testing.T) {
	b := &Browser{}
	b.applyDefaultConfig()
	b.runMode = browserRunModeLoad

	calls := 0
	result := &protocol.Result{}
	err := b.runWithSelectorSelfHeal(context.Background(), selfHealTestAction(), result, "#missing", func(string) error {
		calls++
		return errors.New(`Timeout 1000ms exceeded waiting for locator("#missing")`)
	})
	if err == nil {
		t.Fatal("expected original selector error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want only primary attempt during load replay-only mode", calls)
	}
	metrics, ok := result.ProtocolEvidence["selector_self_heal_metrics"].(map[string]any)
	if !ok {
		t.Fatalf("selector_self_heal_metrics = %#v", result.ProtocolEvidence["selector_self_heal_metrics"])
	}
	if metrics["attempts"] != 0 || metrics["probe_failures"] != 0 || metrics["success"] != false {
		t.Fatalf("metrics = %#v, want no fallback attempts under load replay-only mode", metrics)
	}
}

func TestSelectorSelfHealProbeFailuresConsumeAttemptBudget(t *testing.T) {
	b := &Browser{}
	b.applyDefaultConfig()
	action := selfHealTestAction()
	action.Metadata[selector.MetadataFallbacksKey] = []selector.Candidate{
		{Kind: "css", Value: "#missing-1", Confidence: 90},
		{Kind: "css", Value: "#missing-2", Confidence: 89},
		{Kind: "css", Value: "#missing-3", Confidence: 88},
		{Kind: "css", Value: "#missing-4", Confidence: 87},
	}

	result := &protocol.Result{}
	err := b.runWithSelectorSelfHeal(context.Background(), action, result, "#missing", func(string) error {
		return errors.New(`Timeout 1000ms exceeded waiting for locator("#missing")`)
	})
	if err == nil {
		t.Fatal("expected original selector error")
	}
	metrics, ok := result.ProtocolEvidence["selector_self_heal_metrics"].(map[string]any)
	if !ok {
		t.Fatalf("selector_self_heal_metrics = %#v", result.ProtocolEvidence["selector_self_heal_metrics"])
	}
	if got := metrics["probe_failures"]; got != maxHealAttempts {
		t.Fatalf("probe_failures = %#v, want %d", got, maxHealAttempts)
	}
	if got := metrics["attempts"]; got != 0 {
		t.Fatalf("attempts = %#v, want no run attempts when probes all fail", got)
	}
}

func TestSelectorSelfHealBudgetSpentIncludesProbeFailures(t *testing.T) {
	stats := selectorSelfHealStats{Attempts: 1, ProbeFailures: 2}
	if got := stats.budgetSpent(); got != 3 {
		t.Fatalf("budgetSpent = %d, want 3", got)
	}
}

func TestRecordSelfHealRedactsRuntimeSecretSelectors(t *testing.T) {
	const secret = "short-source-secret"

	b := &Browser{}
	b.SetRuntimeTextRedactor(func(value string) string {
		return strings.ReplaceAll(value, secret, "[REDACTED]")
	})
	action := &protocol.Action{
		Name:             "Click account",
		Selector:         `[data-token='` + secret + `']`,
		RedactedSelector: `[data-token='[REDACTED]']`,
	}
	result := &protocol.Result{}

	var logBuf bytes.Buffer
	testlog.SetDefault(t, slog.New(slog.NewTextHandler(&logBuf, nil)))

	b.recordSelfHeal(
		action,
		result,
		action.Selector,
		`#fallback-`+secret,
		selector.Candidate{Kind: "css", Confidence: 95},
		selectorSelfHealStats{Attempts: 1},
	)
	failedResult := &protocol.Result{}
	b.recordSelfHealMetrics(action, failedResult, action.Selector, false, selectorSelfHealStats{})

	evidence, err := json.Marshal(map[string]any{
		"successful_heal": result.ProtocolEvidence,
		"failed_heal":     failedResult.ProtocolEvidence,
	})
	if err != nil {
		t.Fatalf("marshal self-heal evidence: %v", err)
	}
	for label, value := range map[string]string{
		"log":      logBuf.String(),
		"evidence": string(evidence),
	} {
		if strings.Contains(value, secret) {
			t.Fatalf("%s leaked runtime selector secret: %s", label, value)
		}
		if !strings.Contains(value, "[REDACTED]") {
			t.Fatalf("%s missing redaction marker: %s", label, value)
		}
	}
}

func TestSelectorSelfHealDebuggerEvidenceDoesNotChangeLogs(t *testing.T) {
	const secret = "private-selector"
	var logBuf bytes.Buffer
	testlog.SetDefault(t, slog.New(slog.NewTextHandler(&logBuf, nil)))
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprintf("debug=%t", debug), func(t *testing.T) {
			b := &Browser{unredactedDebug: debug}
			b.applyDefaultConfig()
			b.SetRuntimeTextRedactor(func(value string) string { return strings.ReplaceAll(value, secret, "[REDACTED]") })
			action := selfHealTestAction()
			action.Name = "action-" + secret
			action.Selector = "#primary-" + secret
			action.RedactedSelector = "#primary-[REDACTED]"
			candidate := selector.Candidate{Kind: "css", Value: "#healed-" + secret, Confidence: 95}
			b.storeSelfHealCandidate(selfHealActionIdentity(action, action.Selector), candidate)
			result := &protocol.Result{}
			if err := b.runWithSelectorSelfHeal(context.Background(), action, result, action.Selector, func(value string) error {
				if value == candidate.Value {
					return nil
				}
				return errors.New("selector not found")
			}); err != nil {
				t.Fatal(err)
			}
			heal := result.ProtocolEvidence["selector_self_heal"].(map[string]any)
			metrics := result.ProtocolEvidence["selector_self_heal_metrics"].(map[string]any)
			for _, value := range []string{heal["primary_selector"].(string), heal["healed_selector"].(string), metrics["primary_selector"].(string)} {
				if strings.Contains(value, secret) != debug {
					t.Fatalf("debug=%v evidence value = %q", debug, value)
				}
			}
			failed := &protocol.Result{}
			b.recordSelfHealMetrics(action, failed, action.Selector, false, selectorSelfHealStats{})
			if strings.Contains(failed.ProtocolEvidence["self_heal_metrics"].(map[string]any)["primary_selector"].(string), secret) != debug {
				t.Fatal("failure metrics did not respect debugger disclosure")
			}
		})
	}
	if strings.Contains(logBuf.String(), secret) {
		t.Fatalf("self-heal log leaked selector or action: %s", logBuf.String())
	}
}

func TestNormalizeBrowserSelfHealMode(t *testing.T) {
	tests := map[string]string{
		"":            browserSelfHealReplayOnly,
		"replay-only": browserSelfHealReplayOnly,
		"always":      browserSelfHealAlways,
		"on":          browserSelfHealAlways,
		"off":         browserSelfHealOff,
		"disabled":    browserSelfHealOff,
		"mystery":     "",
	}
	for input, want := range tests {
		if got := normalizeBrowserSelfHealMode(input); got != want {
			t.Fatalf("normalizeBrowserSelfHealMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWaitActionUsesActionSelector(t *testing.T) {
	if !waitActionUsesActionSelector(&protocol.Action{Selector: "#ready"}) {
		t.Fatal("selector-only wait should use action selector")
	}
	if !waitActionUsesActionSelector(&protocol.Action{Selector: "#ready", WaitFor: &protocol.WaitCondition{Type: "visible"}}) {
		t.Fatal("visible wait with empty value should use action selector")
	}
	if waitActionUsesActionSelector(&protocol.Action{Selector: "#ready", WaitFor: &protocol.WaitCondition{Type: "text", Value: "Ready"}}) {
		t.Fatal("text wait with explicit value should not use action selector")
	}
	if waitActionUsesActionSelector(&protocol.Action{Selector: "#ready", WaitFor: &protocol.WaitCondition{Type: "hidden"}}) {
		t.Fatal("hidden wait should not use visible self-heal fallback")
	}
}

func selfHealTestAction() *protocol.Action {
	return &protocol.Action{
		Name:     "Click Missing",
		Type:     "browser_click",
		Selector: "#missing",
		Metadata: map[string]any{
			selector.MetadataFallbacksKey: []selector.Candidate{
				{Kind: "css", Value: "#fallback", Confidence: 90},
			},
		},
	}
}
