package trueclient

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// publishedTruClientSteps is the authoritative published TruClient step toolbox
// (OpenText VuGen 25.1 "TruClient step arguments" + the flow/structural steps),
// each pinned to its resolved coverage scope. This is the classification guard:
// any reclassification must be a deliberate edit here AND in support.go.
var publishedTruClientSteps = map[string]struct {
	canonical string
	status    ScopeStatus
}{
	// object steps
	"Mouse Down":        {"mouse_button", ScopeReviewOnly},
	"Mouse Up":          {"mouse_button", ScopeReviewOnly},
	"Mouse Over":        {"hover", ScopeSupported},
	"Click":             {"click", ScopeSupported},
	"Double Click":      {"click", ScopeSupported},
	"Drag":              {"drag_and_drop", ScopeSupported},
	"Drag To":           {"drag_and_drop", ScopeSupported},
	"Get Property":      {"get_property", ScopeReviewOnly},
	"Scroll":            {"scroll", ScopeSupported},
	"Upload":            {"upload", ScopeSupported},
	"Verify":            {"verify", ScopeSupported},
	"Wait for Property": {"wait_for_property", ScopeSupported},
	"Press Key":         {"press_key", ScopeSupported},
	"Type":              {"set_type", ScopeSupported},
	"Set":               {"set_type", ScopeSupported},
	"Select":            {"select", ScopeSupported},
	"Multi Select":      {"multi_select", ScopeSupported},
	"Seek":              {"seek", ScopeReviewOnly},
	"Set Day":           {"set_day", ScopeReviewOnly},
	// browser-role steps
	"Activate":                 {"switch_tab", ScopeSupported},
	"Activate Tab":             {"switch_tab", ScopeSupported},
	"Close Tab":                {"switch_tab", ScopeSupported},
	"Add Tab":                  {"switch_tab", ScopeSupported},
	"Navigate":                 {"navigate", ScopeSupported},
	"Go Back":                  {"go_back", ScopeReviewOnly},
	"Go Forward":               {"go_forward", ScopeReviewOnly},
	"Resize":                   {"resize", ScopeReviewOnly},
	"Dialog - Confirm":         {"handle_dialog", ScopeSupported},
	"Dialog Prompt":            {"handle_dialog", ScopeSupported},
	"Dialog - Authenticate":    {"dialog_secure", ScopeReviewOnly},
	"Dialog - Prompt Password": {"dialog_secure", ScopeReviewOnly},
	// non-object steps
	"Evaluate JavaScript":    {"evaluate_javascript", ScopeReviewOnly},
	"Evaluate C":             {"evaluate_c", ScopeReviewOnly},
	"Call Function":          {"call_function", ScopeReviewOnly},
	"Catch Error":            {"catch_error", ScopeReviewOnly},
	"For Loop":               {"control_loop", ScopeSupported},
	"Generic API Action":     {"generic_api_action", ScopeReviewOnly},
	"If Block":               {"control_if", ScopeSupported},
	"Rendezvous":             {"rendezvous", ScopeSupported},
	"Verify PDF Content":     {"verify_pdf", ScopeReviewOnly},
	"Wait":                   {"wait", ScopeSupported},
	"Wait for File Download": {"wait_for_download", ScopeReviewOnly},
	// flow / structural
	"Start Transaction": {"start_transaction", ScopeSupported},
	"End Transaction":   {"end_transaction", ScopeSupported},
	"Else":              {"control_if", ScopeSupported},
	"Break":             {"control_transfer", ScopeSupported},
	"Continue":          {"control_transfer", ScopeSupported},
}

// TestLookupStepScopeResolvesEveryPublishedStep — the classification backbone:
// every published toolbox step resolves to its pinned canonical scope + status,
// none lands as "unknown".
func TestLookupStepScopeResolvesEveryPublishedStep(t *testing.T) {
	for name, want := range publishedTruClientSteps {
		scope, ok := LookupStepScope(name)
		if !ok {
			t.Errorf("published step %q does not resolve to any step scope", name)
			continue
		}
		if scope.CanonicalName != want.canonical || scope.Status != want.status {
			t.Errorf("step %q resolves to (%s,%s), want (%s,%s)", name, scope.CanonicalName, scope.Status, want.canonical, want.status)
		}
		if scope.APEXLoadShape == "" {
			t.Errorf("step %q scope has no APEXLoadShape", name)
		}
	}
}

func convertTruClientSteps(t *testing.T, innerSteps string) *ConvertResult {
	t.Helper()
	doc := `<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
` + innerSteps + `
  </step>
</truScript>`
	result, err := ConvertArchive(map[string]string{"default.xml": encodeUTF16LE(doc)}, ConvertOptions{ScriptName: "Completeness"})
	if err != nil {
		t.Fatalf("ConvertArchive: %v", err)
	}
	return result
}

// truArg builds a TruClient JSON arguments attribute from key/value pairs,
// applying the quoted-value convention (the value is wrapped in escaped quotes).
func truArg(pairs ...string) string {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"%s":{"value":"\"%s\""}`, pairs[i], pairs[i+1])
	}
	b.WriteString("}")
	return b.String()
}

func truStepXML(action, args string) string {
	if args == "" {
		args = "{}"
	}
	return fmt.Sprintf(`    <step type="testObject" action=%q arguments='%s' />`, action, args)
}

const truNavigateAnchor = `    <step type="testObject" action="Navigate" arguments='{"Location":{"value":"\"https://example.test/\""}}' />`

// TestConvertMappableStepsEmitExpectedActions is the consistency guard: every
// step that maps to an executable browser/engine action produces that action
// type, is not silently skipped, and is not flagged unsupported.
func TestConvertMappableStepsEmitExpectedActions(t *testing.T) {
	cases := []struct {
		action   string
		args     string
		wantType string
	}{
		{"Navigate", truArg("Location", "https://example.test/x"), "browser_navigate"},
		{"Click", truArg("Selector", "#a"), "browser_click"},
		{"Double Click", truArg("Selector", "#a"), "browser_click"},
		{"Hover", truArg("Selector", "#a"), "browser_hover"},
		{"Mouse Over", truArg("Selector", "#a"), "browser_hover"},
		{"Set", truArg("Selector", "#a", "Value", "hello"), "browser_fill"},
		{"Type", truArg("Selector", "#a", "Value", "hello"), "browser_fill"},
		{"Select", truArg("Selector", "#a", "Value", "two"), "browser_select"},
		{"Multi Select", truArg("Selector", "#a", "Value", "one,two"), "browser_select"},
		{"Press Key", truArg("Key Name", "Enter"), "browser_press_key"},
		{"Scroll", truArg("Direction", "down"), "browser_swipe"},
		{"Wait", truArg("Selector", "#a"), "browser_wait"},
		{"Drag", truArg("Selector", "#a", "Drop Target", "#b"), "browser_drag_and_drop"},
		{"Drag To", truArg("Selector", "#a", "Drop Target", "#b"), "browser_drag_and_drop"},
		{"Upload", truArg("Selector", "#a", "Path", "/tmp/f.txt"), "browser_set_files"},
		{"Dialog - Confirm", truArg("Action", "accept"), "browser_handle_dialog"},
		{"Dialog Prompt", truArg("Action", "accept", "Text", "hi"), "browser_handle_dialog"},
		{"Rendezvous", truArg("Name", "Sync"), "rendezvous"},
		{"Activate Tab", truArg("Tab", "2"), "browser_switch_window"},
		{"Activate", truArg("Tab", "1"), "browser_switch_window"},
		{"Add Tab", truArg("Tab", "1"), "browser_switch_window"},
		{"Close Tab", truArg("Tab", "1"), "browser_switch_window"},
		{"Wait for Property", truArg("Selector", "#a", "Property", "visible"), "browser_wait"},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			result := convertTruClientSteps(t, truNavigateAnchor+"\n"+truStepXML(tc.action, tc.args))
			if !actionsContainType(result.Script.Actions, tc.wantType) {
				t.Fatalf("%s did not produce a %s action: types=%v", tc.action, tc.wantType, actionTypeList(result.Script.Actions))
			}
			if containsString(result.Stats.UnsupportedSteps, tc.action) {
				t.Fatalf("%s was flagged unsupported: %v", tc.action, result.Stats.UnsupportedSteps)
			}
			for _, a := range result.Script.Actions {
				if a.Type == "source_review" && strings.EqualFold(stringFromAny(a.Metadata["source_action"]), tc.action) {
					t.Fatalf("%s degraded to source_review instead of %s", tc.action, tc.wantType)
				}
			}
		})
	}
}

// TestConvertReviewOnlyStepsPreservedNotSkipped — review-only toolbox steps are
// preserved as source_review actions (counted in ReviewOnlySteps), never lost in
// Skipped/UnsupportedSteps.
func TestConvertReviewOnlyStepsPreservedNotSkipped(t *testing.T) {
	steps := []string{
		"Evaluate C", "Generic API Action", "Call Function", "Get Property",
		"Set Day", "Seek", "Resize", "Go Back", "Go Forward",
		"Verify PDF Content", "Wait for File Download", "Mouse Down", "Mouse Up",
		"Dialog - Authenticate", "Dialog - Prompt Password",
	}
	for _, action := range steps {
		t.Run(action, func(t *testing.T) {
			result := convertTruClientSteps(t, truNavigateAnchor+"\n"+truStepXML(action, truArg("Value", "x")))
			if !actionsContainType(result.Script.Actions, "source_review") {
				t.Fatalf("%s did not produce a preserved source_review action: types=%v", action, actionTypeList(result.Script.Actions))
			}
			if result.Stats.ReviewOnlySteps == 0 {
				t.Fatalf("%s did not increment ReviewOnlySteps: %#v", action, result.Stats)
			}
			if containsString(result.Stats.UnsupportedSteps, action) {
				t.Fatalf("%s was flagged unsupported instead of review-only: %v", action, result.Stats.UnsupportedSteps)
			}
		})
	}
}

// TestConvertCatchErrorPreservesBody — Catch Error is review-only but its
// protected child steps are still converted (not silently dropped).
func TestConvertCatchErrorPreservesBody(t *testing.T) {
	inner := truNavigateAnchor + "\n" +
		`    <step type="control" action="Catch Error">` + "\n" +
		truStepXML("Click", truArg("Selector", "#protected")) + "\n" +
		`    </step>`
	result := convertTruClientSteps(t, inner)
	if !actionsContainType(result.Script.Actions, "source_review") {
		t.Fatalf("Catch Error marker missing: types=%v", actionTypeList(result.Script.Actions))
	}
	if !actionsContainType(result.Script.Actions, "browser_click") {
		t.Fatalf("Catch Error protected body (Click) was lost: types=%v", actionTypeList(result.Script.Actions))
	}
	if result.Stats.ReviewOnlySteps == 0 {
		t.Fatalf("Catch Error did not count as review-only: %#v", result.Stats)
	}
}

// TestConvertUnknownStepIsLabeledSkip — a genuinely non-toolbox step still takes
// the clearly-labeled skip path (so the two paths stay distinguishable).
func TestConvertUnknownStepIsLabeledSkip(t *testing.T) {
	result := convertTruClientSteps(t, truNavigateAnchor+"\n"+truStepXML("Frobnicate Widget", truArg("Value", "x")))
	if result.Stats.Skipped == 0 || !containsString(result.Stats.UnsupportedSteps, "Frobnicate Widget") {
		t.Fatalf("unknown step should be a labeled skip: stats=%#v", result.Stats)
	}
	if actionsContainType(result.Script.Actions, "source_review") {
		t.Fatalf("unknown non-toolbox step should not be preserved as source_review")
	}
}

func actionsContainType(actions []dsl.Action, t string) bool {
	for _, a := range actions {
		if a.Type == t {
			return true
		}
	}
	return false
}

func actionTypeList(actions []dsl.Action) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, a.Type)
	}
	return out
}
