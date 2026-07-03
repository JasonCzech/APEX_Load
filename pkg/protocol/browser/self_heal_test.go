package browser

import (
	"context"
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/selector"
)

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
