package engine

import "testing"

func TestAttachSelfHealSuggestionPromotesSelector(t *testing.T) {
	event := &ReplayActionEvent{
		EventID:         "evt-7",
		EventIndex:      7,
		Phase:           "actions",
		ActionName:      "Click checkout",
		ActionType:      "browser_click",
		Success:         true,
		SourceStepIndex: 3,
		ProtocolEvidence: map[string]any{
			"selector_self_heal": map[string]any{
				"primary_selector":  "#old",
				"healed_selector":   "[data-testid=\"checkout\"]",
				"confidence":        0.92,
				"recorder_step_seq": 3,
			},
		},
	}

	attachSelfHealSuggestion(event)

	if len(event.RepairSuggestions) != 1 {
		t.Fatalf("repair suggestions = %+v, want one promotion", event.RepairSuggestions)
	}
	suggestion := event.RepairSuggestions[0]
	if suggestion.DraftPatchType != "promote_selector" {
		t.Fatalf("DraftPatchType = %q, want promote_selector", suggestion.DraftPatchType)
	}
	if suggestion.Action != "Click checkout" || suggestion.Confidence != 0.92 {
		t.Fatalf("suggestion = %+v", suggestion)
	}
	if suggestion.Fields["selector"] != `[data-testid="checkout"]` {
		t.Errorf("selector field = %q", suggestion.Fields["selector"])
	}
	if suggestion.Fields["previous_selector"] != "#old" {
		t.Errorf("previous selector = %q", suggestion.Fields["previous_selector"])
	}
	if suggestion.Fields["recorder_step_seq"] != "3" {
		t.Errorf("recorder_step_seq = %q", suggestion.Fields["recorder_step_seq"])
	}
}

func TestAttachSelfHealSuggestionRequiresPassingActionAndEvidence(t *testing.T) {
	tests := []struct {
		name  string
		event *ReplayActionEvent
	}{
		{
			name: "failed action",
			event: &ReplayActionEvent{
				Success:          false,
				ProtocolEvidence: map[string]any{"selector_self_heal": map[string]any{"healed_selector": "#ok"}},
			},
		},
		{
			name: "missing healed selector",
			event: &ReplayActionEvent{
				Success:          true,
				ProtocolEvidence: map[string]any{"selector_self_heal": map[string]any{"primary_selector": "#old"}},
			},
		},
		{
			name:  "unchanged healed selector",
			event: &ReplayActionEvent{Success: true, ProtocolEvidence: map[string]any{"selector_self_heal": map[string]any{"primary_selector": "#same", "healed_selector": "#same"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attachSelfHealSuggestion(tt.event)
			if len(tt.event.RepairSuggestions) != 0 {
				t.Fatalf("repair suggestions = %+v, want none", tt.event.RepairSuggestions)
			}
		})
	}
}

func TestBrowserFailureDiagnosisUsesFailureEvidence(t *testing.T) {
	for _, tc := range []struct{ name, message, want string }{
		{"text contains", `element "#result" text "Hello operator" does not contain "Welcome operator"`, "assertion"},
		{"text equals", `element "#result" text = "Hello", want "Welcome"`, "assertion"},
		{"locator", `locator #result not found`, "selector"},
		{"locator named expected", `locator #expected not found`, "selector"},
		{"wait", `navigation timed out`, "wait_timing"},
		{"no misleading selector from page text", `browser page is not initialized`, "runtime_environment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := ReplayActionEvent{ActionType: "browser_wait", Selector: "#element-expected", Body: "element selector expected", ErrorMessage: tc.message}
			diagnosis := classifyReplayFailure(event)
			if diagnosis.Category != tc.want {
				t.Fatalf("got %s, want %s", diagnosis.Category, tc.want)
			}
			for _, suggestion := range replayRepairSuggestionsForDiagnosis(event, diagnosis) {
				if suggestion.DraftPatchType == "update_selector" || suggestion.Confidence != 0 {
					t.Fatalf("unsupported repair: %+v", suggestion)
				}
			}
		})
	}
}
