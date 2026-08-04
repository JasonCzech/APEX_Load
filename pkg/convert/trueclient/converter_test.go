package trueclient

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertArchiveParsesUTF16DefaultXML(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/catalog\&quot;&quot;}}" />
		<step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;$(\&quot;input[name='q']\&quot;).val(\&quot;QAInsights\&quot;); //Enter the search keyword\ndocument.querySelector('#menu').click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Catalog Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if result.Script.Name != "Catalog Flow" {
		t.Fatalf("script name = %q, want Catalog Flow", result.Script.Name)
	}
	if result.Script.Protocol != "browser" {
		t.Fatalf("script protocol = %q, want browser", result.Script.Protocol)
	}
	if result.Script.Config.BaseURL != "https://m.example.com" {
		t.Fatalf("base url = %q, want https://m.example.com", result.Script.Config.BaseURL)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[0]; action.Type != "browser_navigate" || action.Target != "https://m.example.com/catalog" {
		t.Fatalf("action = %#v, want browser navigate to catalog", action)
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "input[name='q']" || action.Value != "QAInsights" {
		t.Fatalf("action = %#v, want browser fill for search input", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "#menu" {
		t.Fatalf("action = %#v, want browser click for menu selector", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
	issues := dsl.ValidateScriptIssues(result.Script)
	if len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

func TestConvertArchiveMapsQuerySelectorValueAssignment(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.querySelector('#displayName').value = 'APEX Load QA'&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Profile Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#displayName" || action.Value != "APEX Load QA" {
		t.Fatalf("action = %#v, want browser fill for #displayName", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=2 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
	issues := dsl.ValidateScriptIssues(result.Script)
	if len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

func TestConvertArchiveMapsClickLikeWeb2Steps(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://shop.example.com/cart\&quot;&quot;}}" />
    <step type="testObject" action="Double Click" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#quantity\&quot;&quot;}}" />
    <step type="testObject" action="Submit" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;form#cart\&quot;&quot;}}" />
    <step type="testObject" action="Check" index="4" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#terms\&quot;&quot;}}" />
    <step type="testObject" action="Uncheck" index="5" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#newsletter\&quot;&quot;}}" />
    <step type="testObject" action="Select Radio" index="6" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;[name='shipping'][value='express']\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Click-like Web2 Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 6 {
		t.Fatalf("actions len = %d, want 6", len(result.Script.Actions))
	}
	assertBrowserClickAction(t, result.Script.Actions[1], "#quantity", "truclient_double_click", map[string]any{"click_count": 2})
	assertBrowserClickAction(t, result.Script.Actions[2], "form#cart", "truclient_submit", nil)
	if result.Script.Actions[2].Metadata["submit"] != true {
		t.Fatalf("submit metadata = %#v, want submit=true", result.Script.Actions[2].Metadata)
	}
	assertBrowserClickAction(t, result.Script.Actions[3], "#terms", "truclient_check", map[string]any{"checked": true})
	assertBrowserClickAction(t, result.Script.Actions[4], "#newsletter", "truclient_uncheck", map[string]any{"checked": false})
	assertBrowserClickAction(t, result.Script.Actions[5], "[name='shipping'][value='express']", "truclient_select_radio", map[string]any{"checked": true})
	if result.Stats.TotalSteps != 6 || result.Stats.ConvertedActions != 6 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=6 converted=6 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 || len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("warnings=%#v unsupported=%#v, want none", result.Warnings, result.Stats.UnsupportedSteps)
	}
}

func TestConvertArchivePreservesIfElseControlFlow(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="control" action="If" index="1" arguments="{&quot;Condition&quot;:{&quot;value&quot;:&quot;\&quot;{role} == 'admin'\&quot;&quot;}}">
      <step type="testObject" action="Click" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#admin\&quot;&quot;}}" />
    </step>
    <step type="control" action="Else" index="3">
      <step type="testObject" action="Click" index="4" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#guest\&quot;&quot;}}" />
    </step>
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Control Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one control_if wrapper", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Type != "control_if" || action.Control == nil || action.Control.Condition == nil {
		t.Fatalf("action = %#v, want control_if with condition", action)
	}
	if action.Control.Condition.Left != "{{var.role}}" || action.Control.Condition.Op != "eq" || action.Control.Condition.Right != "admin" {
		t.Fatalf("condition = %#v, want role eq admin", action.Control.Condition)
	}
	if len(action.Control.Steps) != 1 || action.Control.Steps[0].Type != "browser_click" || action.Control.Steps[0].Selector != "#admin" {
		t.Fatalf("then steps = %#v, want admin click", action.Control.Steps)
	}
	if len(action.Control.Else) != 1 || action.Control.Else[0].Type != "browser_click" || action.Control.Else[0].Selector != "#guest" {
		t.Fatalf("else steps = %#v, want guest click", action.Control.Else)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

func TestConvertArchivePreservesCountLoopControlFlow(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="control" action="Loop" index="1" arguments="{&quot;Count&quot;:{&quot;value&quot;:&quot;3&quot;}}">
      <step type="testObject" action="Click" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#retry\&quot;&quot;}}" />
    </step>
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Loop Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one control_loop wrapper", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Type != "control_loop" || action.Control == nil || action.Control.Count != 3 || action.Control.MaxIterations != 3 {
		t.Fatalf("action = %#v, want count=3 control_loop", action)
	}
	if len(action.Control.Steps) != 1 || action.Control.Steps[0].Type != "browser_click" || action.Control.Steps[0].Selector != "#retry" {
		t.Fatalf("loop steps = %#v, want nested retry click", action.Control.Steps)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

func TestConvertArchiveEmitsControlBreakAndContinue(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="control" action="Loop" index="1" arguments="{&quot;Count&quot;:{&quot;value&quot;:&quot;5&quot;}}">
      <step type="testObject" action="Click" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#retry\&quot;&quot;}}" />
      <step type="control" action="If" index="3" arguments="{&quot;Condition&quot;:{&quot;value&quot;:&quot;\&quot;done\&quot;&quot;}}">
        <step type="control" action="Continue" index="4" />
      </step>
      <step type="control" action="Break" index="5" />
    </step>
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Transfer Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "control_loop" {
		t.Fatalf("actions = %#v, want one control_loop wrapper", result.Script.Actions)
	}
	steps := result.Script.Actions[0].Control.Steps
	// Click, If{Continue}, Break.
	if len(steps) != 3 {
		t.Fatalf("loop steps = %#v, want 3 (click, if, break)", steps)
	}
	if steps[1].Type != "control_if" || len(steps[1].Control.Steps) != 1 || steps[1].Control.Steps[0].Type != "control_continue" {
		t.Fatalf("nested if = %#v, want a control_continue inside", steps[1])
	}
	if steps[2].Type != "control_break" {
		t.Fatalf("final step = %#v, want control_break", steps[2])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
	// Break/continue are now first-class conversions, not skipped review items.
	for _, w := range result.Warnings {
		if strings.Contains(w, "break/continue") || strings.Contains(w, "do not support") {
			t.Fatalf("unexpected break/continue downgrade warning: %q", w)
		}
	}
}

func TestConvertArchiveReviewGatesUnrecognizedIfConditionWithoutFlattening(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="control" action="If" index="1" arguments="{&quot;Condition&quot;:{&quot;value&quot;:&quot;\&quot;document.querySelector('#x').offsetWidth > 0\&quot;&quot;}}">
      <step type="testObject" action="Click" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#then\&quot;&quot;}}" />
    </step>
    <step type="control" action="Else" index="3">
      <step type="testObject" action="Click" index="4" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#else\&quot;&quot;}}" />
    </step>
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Unrecognized Control"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "control_if" {
		t.Fatalf("actions = %#v, want one review-gated control_if wrapper", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Metadata["review_required"] != true || action.Control.Condition != nil {
		t.Fatalf("action = %#v, want always-true review-gated wrapper", action)
	}
	if len(action.Control.Steps) != 2 || action.Control.Steps[0].Selector != "#then" || action.Control.Steps[1].Selector != "#else" {
		t.Fatalf("steps = %#v, want old flattened execution order preserved inside wrapper", action.Control.Steps)
	}
	if len(action.Control.Else) != 0 {
		t.Fatalf("else = %#v, want fallback branch merged into review-gated steps", action.Control.Else)
	}
}

func TestConvertArchiveMapsJQueryAttrAndTriggerJavaScript(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/preferences\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;$('#email').attr('value', 'qa@example.com');\n$('#save').trigger('click')&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "jQuery Attr Trigger Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#email" || action.Value != "qa@example.com" || action.Metadata["source"] != "truclient_jquery_attr" {
		t.Fatalf("action[1] = %#v, want browser fill from jQuery attr value", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "#save" || action.Metadata["source"] != "truclient_jquery_trigger" {
		t.Fatalf("action[2] = %#v, want browser click from jQuery trigger click", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveMapsQuerySelectorAllJavaScript(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.querySelectorAll('input.display-name')[0].value = 'APEX Load QA';\ndocument.querySelectorAll('button.save')[0].click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Query Selector All Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "input.display-name" || action.Value != "APEX Load QA" {
		t.Fatalf("action[1] = %#v, want browser fill for first querySelectorAll match", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "button.save" {
		t.Fatalf("action[2] = %#v, want browser click for first querySelectorAll match", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveMapsCollectionItemJavaScript(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.getElementsByName('displayName').item(0).value = 'APEX Load QA';\ndocument.querySelectorAll('button.save').item(0).click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Collection Item Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != `[name="displayName"]` || action.Value != "APEX Load QA" {
		t.Fatalf("action[1] = %#v, want browser fill for first named item", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "button.save" {
		t.Fatalf("action[2] = %#v, want browser click for first querySelectorAll item", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveMapsGetElementByIdJavaScript(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.getElementById('username').value = 'apexload';\ndocument.getElementById('submit').click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Element ID JavaScript"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#username" || action.Value != "apexload" {
		t.Fatalf("action[1] = %#v, want browser fill for #username", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "#submit" {
		t.Fatalf("action[2] = %#v, want browser click for #submit", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveMapsGetElementsByNameJavaScript(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/search\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.getElementsByName('q')[0].value = 'apexload';\ndocument.getElementsByName('submitSearch')[0].click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Element Name JavaScript"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != `[name="q"]` || action.Value != "apexload" {
		t.Fatalf("action[1] = %#v, want browser fill for name q", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != `[name="submitSearch"]` {
		t.Fatalf("action[2] = %#v, want browser click for name submitSearch", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveMapsFormElementJavaScript(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.forms['login'].elements['username'].value = 'apexload';\ndocument.forms[0].elements['submit'].click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Form Element JavaScript"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != `[name="username"]` || action.Value != "apexload" {
		t.Fatalf("action[1] = %#v, want browser fill for username form element", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != `[name="submit"]` {
		t.Fatalf("action[2] = %#v, want browser click for submit form element", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveMapsGetElementsByClassNameJavaScript(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/search\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.getElementsByClassName('search-field primary')[0].value = 'apexload';\ndocument.getElementsByClassName('search-submit')[0].click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Element Class JavaScript"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != ".search-field.primary" || action.Value != "apexload" {
		t.Fatalf("action[1] = %#v, want browser fill for class search field", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != ".search-submit" {
		t.Fatalf("action[2] = %#v, want browser click for class search submit", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveMapsGetElementsByTagNameJavaScript(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/search\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.getElementsByTagName('input')[0].value = 'apexload';\ndocument.getElementsByTagName('button')[0].click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Element Tag JavaScript"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "input" || action.Value != "apexload" {
		t.Fatalf("action[1] = %#v, want browser fill for first input tag", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "button" {
		t.Fatalf("action[2] = %#v, want browser click for first button tag", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveLeavesUnsafeTagNameJavaScriptForReview(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/search\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.getElementsByTagName('input.primary')[0].click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Unsafe Tag JavaScript"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2 actions", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_evaluate_js" || action.Metadata["review_required"] != true || action.Metadata["compatibility_gate"] != "trueclient2_javascript_review" {
		t.Fatalf("action[1] = %#v, want review-gated browser_evaluate_js", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=2 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveLeavesUnsafeClassNameJavaScriptForReview(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/search\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.getElementsByClassName('1bad')[0].click()&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Unsafe Class JavaScript"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2 actions", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_evaluate_js" || action.Metadata["review_required"] != true || action.Metadata["compatibility_gate"] != "trueclient2_javascript_review" {
		t.Fatalf("action[1] = %#v, want review-gated browser_evaluate_js", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=2 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveIgnoresStructuralRunLogicSteps(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="top" action="TopStep">
    <step type="block" action="Init Block" actionName="Init">
      <step type="control" action="Call Action" arguments="{&quot;Action Name&quot;:{&quot;value&quot;:&quot;Action&quot;}}" />
    </step>
    <step type="block" action="action" actionName="Action">
      <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/home\&quot;&quot;}}" />
    </step>
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Run Logic Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions len = %d, want 1", len(result.Script.Actions))
	}
	if action := result.Script.Actions[0]; action.Type != "browser_navigate" || action.Target != "https://m.example.com/home" {
		t.Fatalf("action = %#v, want browser navigate to home", action)
	}
	if result.Stats.TotalSteps != 1 || result.Stats.ConvertedActions != 1 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=1 converted=1 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveUsesRunLogicReferencesOnly(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="top" action="TopStep">
    <step type="block" action="Run Block" actionName="Run">
      <step type="control" action="Call Action" arguments="{&quot;Action Name&quot;:{&quot;value&quot;:&quot;PrimaryAction&quot;}}" />
    </step>
    <step type="block" action="action" actionName="PrimaryAction">
      <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/home\&quot;&quot;}}" />
    </step>
    <step type="block" action="action" actionName="UnusedAction">
      <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/admin\&quot;&quot;}}" />
    </step>
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Referenced Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions len = %d, want 1", len(result.Script.Actions))
	}
	if action := result.Script.Actions[0]; action.Type != "browser_navigate" || action.Target != "https://m.example.com/home" {
		t.Fatalf("action = %#v, want only referenced PrimaryAction navigate", action)
	}
	if result.Stats.TotalSteps != 1 || result.Stats.ConvertedActions != 1 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=1 converted=1 skipped=0", result.Stats)
	}
}

func TestConvertArchiveEmitsNestedCalledActionOnce(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="top" action="TopStep">
    <step type="block" action="Run Block" actionName="Run">
      <step type="control" action="Call Action" arguments="{&quot;Action Name&quot;:{&quot;value&quot;:&quot;Outer&quot;}}" />
    </step>
    <step type="block" action="action" actionName="Outer">
      <step type="control" action="Call Action" arguments="{&quot;Action Name&quot;:{&quot;value&quot;:&quot;Inner&quot;}}" />
    </step>
    <step type="block" action="action" actionName="Inner">
      <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/once\&quot;&quot;}}" />
    </step>
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Nested Action Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Target != "https://m.example.com/once" {
		t.Fatalf("actions = %#v, want called Inner action exactly once", result.Script.Actions)
	}
}

func TestConvertArchiveMapsDirectClickAndTypeSteps(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="testObject" action="Set / Type" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#username\&quot;&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;\&quot;apexload\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Login Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#username" || action.Value != "apexload" {
		t.Fatalf("action = %#v, want browser fill for #username", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "#submit" {
		t.Fatalf("action = %#v, want browser click for #submit", action)
	}
	if result.Stats.TotalSteps != 3 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=3 converted=3 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveReviewGatesSelectorlessCoordinateClick(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Click" index="1" arguments="{&quot;X Coordinate&quot;:{&quot;value&quot;:60},&quot;Y Coordinate&quot;:{&quot;value&quot;:192}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Coordinate Click"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions len = %d, want 1", len(result.Script.Actions))
	}
	action := result.Script.Actions[0]
	if action.Type != "browser_evaluate_js" || !strings.Contains(action.Target, "document.elementFromPoint(x, y)") {
		t.Fatalf("action = %#v, want review-gated coordinate click JS", action)
	}
	if action.Metadata["review_required"] != true || action.Metadata["review_reason"] != "selectorless_coordinate_click" {
		t.Fatalf("metadata = %#v, want selectorless coordinate review gate", action.Metadata)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "coordinate click") {
		t.Fatalf("warnings = %#v, want coordinate click warning", result.Warnings)
	}
	issues := dsl.ValidateScriptIssues(result.Script)
	if len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

func TestConvertArchiveReviewGatesSelectorlessClickWithoutCoordinates(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Click" index="1" arguments="{}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Unresolved Click"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Type != "browser_evaluate_js" || action.Target != "(() => null)()" || action.Metadata["review_reason"] != "missing_selector" {
		t.Fatalf("action = %#v, want review placeholder for missing selector", action)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "review-gated") {
		t.Fatalf("warnings = %#v, want review-gated warning", result.Warnings)
	}
}

func TestConvertArchiveMapsSelectStep(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="testObject" action="Select" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#country\&quot;&quot;},&quot;VisibleText&quot;:{&quot;value&quot;:&quot;\&quot;United States\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Select Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_select" || action.Selector != "#country" || action.Value != "United States" {
		t.Fatalf("action = %#v, want browser_select for #country", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=2 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveReviewGatesSelectWithoutValue(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Select" index="1" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;#quantity&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Select Review"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Type != "browser_evaluate_js" || action.Metadata["review_reason"] != "missing_select_value" || action.Metadata["source_selector"] != "#quantity" {
		t.Fatalf("action = %#v, want review placeholder for missing select value", action)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "selected value") {
		t.Fatalf("warnings = %#v, want selected value warning", result.Warnings)
	}
}

func TestConvertArchiveMapsDirectScrollStep(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/catalog\&quot;&quot;}}" />
    <step type="testObject" action="Scroll" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#product-list\&quot;&quot;},&quot;Direction&quot;:{&quot;value&quot;:&quot;\&quot;down\&quot;&quot;},&quot;Distance&quot;:{&quot;value&quot;:&quot;450px&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Scroll Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	scroll := result.Script.Actions[1]
	if scroll.Type != "browser_swipe" || scroll.Selector != "#product-list" {
		t.Fatalf("scroll action = %#v, want browser_swipe on #product-list", scroll)
	}
	if scroll.Params["direction"] != "down" || scroll.Params["distance"] != 450 {
		t.Fatalf("scroll params = %#v, want direction=down distance=450", scroll.Params)
	}
	if scroll.Metadata["source"] != "truclient_scroll" {
		t.Fatalf("scroll metadata = %#v, want source truclient_scroll", scroll.Metadata)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=2 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveMapsWaitAndPreservesTransactions(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="util" action="Start Transaction" index="1" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;Login\&quot;&quot;}}" />
    <step type="testObject" action="Navigate" index="2" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="testObject" action="Set / Type" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#username\&quot;&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;\&quot;apexload\&quot;&quot;}}" />
    <step type="util" action="Wait" index="4" arguments="{&quot;Duration&quot;:{&quot;value&quot;:&quot;500&quot;}}" />
    <step type="testObject" action="Click" index="5" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
    <step type="util" action="End Transaction" index="6" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;Login\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Login Flow", PreserveTransactions: true})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 4 {
		t.Fatalf("actions len = %d, want 4", len(result.Script.Actions))
	}
	if action := result.Script.Actions[0]; action.Name != "Login_Navigate" || action.Type != "browser_navigate" {
		t.Fatalf("action[0] = %#v, want Login_Navigate browser_navigate", action)
	}
	if action := result.Script.Actions[2]; action.Name != "Login_Wait" || action.Type != "browser_wait" || action.WaitFor == nil || action.WaitFor.Type != "duration" || action.WaitFor.Value != "500" {
		t.Fatalf("action[2] = %#v, want Login_Wait browser_wait duration 500", action)
	}
	if action := result.Script.Actions[3]; action.Name != "Login_Click" || action.Type != "browser_click" || action.Selector != "#submit" {
		t.Fatalf("action[3] = %#v, want Login_Click browser_click for #submit", action)
	}
	if result.Stats.TotalSteps != 6 || result.Stats.ConvertedActions != 4 || result.Stats.Skipped != 0 || result.Stats.Transactions != 1 {
		t.Fatalf("stats = %#v, want total=6 converted=4 skipped=0 transactions=1", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
	issues := dsl.ValidateScriptIssues(result.Script)
	if len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

func TestConvertArchiveMapsVerifyToPreviousActionAssertions(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="util" action="Verify" index="2" arguments="{&quot;Title&quot;:{&quot;value&quot;:&quot;\&quot;Login Page\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
    <step type="util" action="Verify" index="4" arguments="{&quot;Text&quot;:{&quot;value&quot;:&quot;\&quot;Saved apexload\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Verify Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if assertions := result.Script.Actions[0].Assertions; len(assertions) != 1 || assertions[0].Type != "page_title" || assertions[0].Value != "Login Page" {
		t.Fatalf("action[0].assertions = %#v, want page_title Login Page", assertions)
	}
	if assertions := result.Script.Actions[1].Assertions; len(assertions) != 1 || assertions[0].Type != "body_contains" || assertions[0].Value != "Saved apexload" {
		t.Fatalf("action[1].assertions = %#v, want body_contains Saved apexload", assertions)
	}
	if result.Stats.TotalSteps != 4 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=4 converted=2 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveWarnsWhenVerifyHasNoPrecedingImportedAction(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="util" action="Verify" index="1" arguments="{&quot;Title&quot;:{&quot;value&quot;:&quot;\&quot;Login Page\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Verify Without Prior Action"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions len = %d, want 1", len(result.Script.Actions))
	}
	if len(result.Script.Actions[0].Assertions) != 0 {
		t.Fatalf("action[0].assertions = %#v, want none", result.Script.Actions[0].Assertions)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 1 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=1 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "could not be attached") {
		t.Fatalf("warnings = %#v, want unattached verify warning", result.Warnings)
	}
}

func TestConvertArchiveMapsEvaluateJavaScriptScrollToSwipe(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.querySelector('#displayName').value = 'APEX Load QA';\nwindow.scrollTo(0, 400)&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Mixed Evaluate JavaScript"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#displayName" || action.Value != "APEX Load QA" {
		t.Fatalf("action = %#v, want browser_fill for #displayName", action)
	}
	scroll := result.Script.Actions[2]
	if scroll.Type != "browser_swipe" || scroll.Selector != "body" {
		t.Fatalf("scroll action = %#v, want browser_swipe on body", scroll)
	}
	if scroll.Params["direction"] != "up" || scroll.Params["distance"] != 400 {
		t.Fatalf("scroll params = %#v, want direction=up distance=400", scroll.Params)
	}
	if scroll.Metadata["source_script"] != "window.scrollTo(0, 400)" {
		t.Fatalf("source_script = %#v, want original scroll statement", scroll.Metadata["source_script"])
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertEvaluateJavaScriptMapsScrollDirection(t *testing.T) {
	tests := []struct {
		statement     string
		wantDirection string
		wantDistance  int
	}{
		{statement: "window.scrollTo(0, 400)", wantDirection: "up", wantDistance: 400},
		{statement: "window.scrollBy(0, -250)", wantDirection: "down", wantDistance: 250},
		{statement: "window.scrollBy(300, 0)", wantDirection: "left", wantDistance: 300},
		{statement: "window.scrollBy(-125, 0)", wantDirection: "right", wantDistance: 125},
	}

	for _, tc := range tests {
		action, ok := convertJavaScriptStatement(tc.statement, "Evaluate JavaScript", 1)
		if !ok {
			t.Fatalf("convertJavaScriptStatement(%q) = false, want true", tc.statement)
		}
		if action.Type != "browser_swipe" {
			t.Fatalf("action type for %q = %q, want browser_swipe", tc.statement, action.Type)
		}
		if action.Params["direction"] != tc.wantDirection || action.Params["distance"] != tc.wantDistance {
			t.Fatalf("params for %q = %#v, want direction=%s distance=%d", tc.statement, action.Params, tc.wantDirection, tc.wantDistance)
		}
	}
}

func TestSplitJavaScriptStatementsPreservesURLStringLiterals(t *testing.T) {
	statements := splitJavaScriptStatements("window.location.href = 'https://m.example.com/cart'; // redirect to cart\n$('#q').val('apexload')")
	if len(statements) != 2 {
		t.Fatalf("statements = %#v, want 2 statements", statements)
	}
	if statements[0] != "window.location.href = 'https://m.example.com/cart'" {
		t.Fatalf("first statement = %q, want full URL assignment", statements[0])
	}
	if statements[1] != "$('#q').val('apexload')" {
		t.Fatalf("second statement = %q, want jQuery value call", statements[1])
	}
}

func TestConvertArchiveMapsEvaluateJavaScriptLocationNavigation(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;window.location.href = 'https://m.example.com/cart';\nlocation.assign('/checkout')&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Navigation JavaScript"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_navigate" || action.Target != "https://m.example.com/cart" {
		t.Fatalf("action[1] = %#v, want browser navigate to cart", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_navigate" || action.Target != "/checkout" {
		t.Fatalf("action[2] = %#v, want browser navigate to /checkout", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=3 skipped=0", result.Stats)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestConvertArchiveMapsHoverStep(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/menu\&quot;&quot;}}" />
    <step type="testObject" action="Hover" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#account-menu\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Hover Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_hover" || action.Selector != "#account-menu" || action.Target != "#account-menu" {
		t.Fatalf("action[1] = %#v, want browser_hover on #account-menu", action)
	}
	if result.Stats.TotalSteps != 2 || result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want total=2 converted=2 skipped=0", result.Stats)
	}
	if len(result.Warnings) != 0 || len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("warnings=%#v unsupported=%#v, want none", result.Warnings, result.Stats.UnsupportedSteps)
	}
}

func TestConvertArchiveAppliesObjectRepositorySelector(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"Web2/ObjectRepository/MainObjects.xml": `<objects><object id="cart-button" name="Cart Button" selector="[data-testid='cart']" data-testid="add-to-cart" /></objects>`,
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://shop.example.com/cart\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="2" arguments="{&quot;Object ID&quot;:{&quot;value&quot;:&quot;\&quot;cart-button\&quot;&quot;},&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#cart-fallback\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Object Repository Selector Flow"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	click := result.Script.Actions[1]
	if click.Type != "browser_click" || click.Selector != `[data-testid="add-to-cart"]` || click.Target != `[data-testid="add-to-cart"]` {
		t.Fatalf("click action = %#v, want highest-confidence object-repository data-testid selector", click)
	}
	if confidence, ok := click.Metadata["source_selector_confidence"].(int); !ok || confidence != 95 {
		t.Fatalf("source_selector_confidence = %#v, want 95", click.Metadata["source_selector_confidence"])
	}
	fallbacks, ok := click.Metadata["source_selector_fallbacks"].([]SelectorCandidate)
	if !ok || len(fallbacks) == 0 {
		t.Fatalf("source_selector_fallbacks = %#v, want preserved fallback candidates", click.Metadata["source_selector_fallbacks"])
	}
	if !containsSelectorCandidate(fallbacks, "css", "#cart-fallback", 85) {
		t.Fatalf("fallbacks = %#v, want preserved inline #cart-fallback candidate", fallbacks)
	}
}

// TestConvertArchiveWeb2SPACheckoutFixture is the baseline web2_spa_checkout fixture: a
// modern single-page checkout flow that exercises object-repository selector ranking,
// parameterized input, transaction preservation, and verify-to-assertion mapping in one
// realistic TruClient 2.0 - Web archive.
func TestConvertArchiveWeb2SPACheckoutFixture(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"Web2/ObjectRepository/Checkout.xml": `<objects>
  <object id="qty" name="Quantity" data-testid="qty-input" />
  <object id="add-to-cart" name="Add To Cart" data-testid="add-to-cart-btn" />
  <object id="place-order" name="Place Order" selector="button.place-order" />
</objects>`,
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://shop.example.com/cart\&quot;&quot;}}" />
    <step type="transaction" action="Start Transaction" index="2" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;Checkout\&quot;&quot;}}" />
    <step type="testObject" action="Set / Type" index="3" arguments="{&quot;Object ID&quot;:{&quot;value&quot;:&quot;\&quot;qty\&quot;&quot;},&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#qty-fallback\&quot;&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;\&quot;{quantity}\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="4" arguments="{&quot;Object ID&quot;:{&quot;value&quot;:&quot;\&quot;add-to-cart\&quot;&quot;},&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#cart-fallback\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="5" arguments="{&quot;Object ID&quot;:{&quot;value&quot;:&quot;\&quot;place-order\&quot;&quot;}}" />
    <step type="testObject" action="Wait" index="6" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;[data-testid='order-confirmation']\&quot;&quot;}}" />
    <step type="testObject" action="Verify" index="7" arguments="{&quot;Text&quot;:{&quot;value&quot;:&quot;\&quot;Order Confirmed\&quot;&quot;}}" />
    <step type="transaction" action="End Transaction" index="8" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;Checkout\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Web2 SPA Checkout", PreserveTransactions: false})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if result.Stats.Transactions != 1 {
		t.Fatalf("transactions = %d, want 1", result.Stats.Transactions)
	}
	// Navigate, fill, click, click, wait (Verify folds into the preceding wait action).
	if len(result.Script.Actions) != 5 {
		t.Fatalf("actions len = %d, want 5: %#v", len(result.Script.Actions), result.Script.Actions)
	}

	fill := result.Script.Actions[1]
	if fill.Type != "browser_fill" || fill.Selector != `[data-testid="qty-input"]` || fill.Value != "{quantity}" {
		t.Fatalf("fill action = %#v, want object-repository qty selector and parameter value", fill)
	}
	if fill.Metadata["source_transaction"] != "Checkout" {
		t.Fatalf("fill source_transaction = %#v, want Checkout", fill.Metadata["source_transaction"])
	}

	addToCart := result.Script.Actions[2]
	if addToCart.Type != "browser_click" || addToCart.Selector != `[data-testid="add-to-cart-btn"]` {
		t.Fatalf("add-to-cart action = %#v, want object-repository data-testid selector", addToCart)
	}

	// The object's logical id doubles as a high-confidence HTML id selector candidate
	// (#place-order @90), which outranks the css selector candidate (button.place-order @88).
	placeOrder := result.Script.Actions[3]
	if placeOrder.Type != "browser_click" || placeOrder.Selector != "#place-order" {
		t.Fatalf("place-order action = %#v, want object-repository id selector", placeOrder)
	}

	wait := result.Script.Actions[4]
	if wait.Type != "browser_wait" || len(wait.Assertions) != 1 || wait.Assertions[0].Type != "body_contains" || wait.Assertions[0].Value != "Order Confirmed" {
		t.Fatalf("wait action = %#v, want verify folded into body_contains assertion", wait)
	}
	if len(result.Stats.UnsupportedSteps) != 0 {
		t.Fatalf("unsupported steps = %#v, want none", result.Stats.UnsupportedSteps)
	}
}

// TestConvertArchiveWeb2ObjectRepositoryVariantsFixture is the web2_object_repository_variants
// fixture: it proves the highest-confidence candidate is applied across distinct locator kinds
// (data-testid attribute, css selector, and xpath) while preserving the rest as fallbacks.
func TestConvertArchiveWeb2ObjectRepositoryVariantsFixture(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"Web2/ObjectRepository/Variants.xml": `<objects>
  <object id="search" name="Search" data-testid="search-input" />
  <object name="Filter" xpath="//select[@name='filter']" />
  <object name="Login Button" selector="form.login button" />
</objects>`,
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://app.example.com\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="2" arguments="{&quot;Object ID&quot;:{&quot;value&quot;:&quot;\&quot;search\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="3" arguments="{&quot;Object ID&quot;:{&quot;value&quot;:&quot;\&quot;Filter\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="4" arguments="{&quot;Object ID&quot;:{&quot;value&quot;:&quot;\&quot;Login Button\&quot;&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Web2 Object Repository Variants"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 4 {
		t.Fatalf("actions len = %d, want 4", len(result.Script.Actions))
	}
	cases := []struct {
		index    int
		selector string
	}{
		{1, `[data-testid="search-input"]`},
		{2, `xpath=//select[@name='filter']`},
		{3, `form.login button`},
	}
	for _, tc := range cases {
		action := result.Script.Actions[tc.index]
		if action.Type != "browser_click" || action.Selector != tc.selector {
			t.Fatalf("action[%d] = %#v, want selector %q", tc.index, action, tc.selector)
		}
	}
}

func TestConvertArchiveAppliesEmbeddedTestObjectSelectors(t *testing.T) {
	files := map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript platform="TruClient Browser118.0" engineVersion="2024.3.0.53">
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://advantage.example.com\&quot;&quot;}}" />
    <step type="testObject" testObject="testObj:{menu-search}" action="Click" index="2" arguments="{&quot;X Coordinate&quot;:{&quot;value&quot;:4},&quot;Y Coordinate&quot;:{&quot;value&quot;:13}}" />
  </step>
  <testObjects>
    <testObject testObjId="testObj:{menu-search}" platform="web" fallbackName="Search button">
      <roles><role>element</role><role>button</role></roles>
      <idents active="Electors">
        <ident type="XPath"><![CDATA[{"implData":{"value":"(//svg[@id='menuSearch'])[1]"}}]]></ident>
        <ident type="Electors"><![CDATA[{"implData":{"electors":[{"id":"tagName","value":"SVG"},{"id":"id","value":{"id":"menuSearch","isValidWord":true}}]}}]]></ident>
      </idents>
    </testObject>
  </testObjects>
</truScript>`),
	}
	result, err := ConvertArchive(files, ConvertOptions{ScriptName: "Embedded Objects"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	click := result.Script.Actions[1]
	if click.Type != "browser_click" || click.Selector != "#menuSearch" || click.Target != "#menuSearch" {
		t.Fatalf("click action = %#v, want embedded object id selector", click)
	}
	if click.Metadata["source_object_descriptor_id"] != "testObj:{menu-search}" {
		t.Fatalf("source_object_descriptor_id = %#v, want embedded test object id", click.Metadata["source_object_descriptor_id"])
	}
	if confidence, ok := click.Metadata["source_selector_confidence"].(int); !ok || confidence != 90 {
		t.Fatalf("source_selector_confidence = %#v, want 90", click.Metadata["source_selector_confidence"])
	}
	fallbacks, ok := click.Metadata["source_selector_fallbacks"].([]SelectorCandidate)
	if !ok || !containsSelectorCandidate(fallbacks, "xpath", "(//svg[@id='menuSearch'])[1]", 58) {
		t.Fatalf("source_selector_fallbacks = %#v, want embedded XPath fallback", click.Metadata["source_selector_fallbacks"])
	}

	inspection, err := InspectArchive(files, "TruClient 2.0 - Web")
	if err != nil {
		t.Fatalf("InspectArchive returned error: %v", err)
	}
	if len(inspection.ObjectDescriptors) != 1 || inspection.ObjectDescriptors[0].ID != "testObj:{menu-search}" {
		t.Fatalf("object descriptors = %#v, want embedded test object descriptor", inspection.ObjectDescriptors)
	}
	if len(inspection.Steps) != 2 || inspection.Steps[1].ObjectDescriptorID != "testObj:{menu-search}" || !containsSelectorCandidate(inspection.Steps[1].SelectorCandidates, "css", "#menuSearch", 90) {
		t.Fatalf("inspection steps = %#v, want click step matched to embedded selector candidates", inspection.Steps)
	}
}

func TestConvertArchiveMapsEmbeddedSetPathToFileUpload(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript platform="Chromium24" engineVersion="2024.3.0.53">
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://westwind.example.com/upload\&quot;&quot;}}" />
    <step type="testObject" testObject="testObj:{upload-input}" action="Set" index="2" arguments="{&quot;Path&quot;:{&quot;value&quot;:&quot;\&quot;C:\\Users\\qa\\upload.png\&quot;&quot;}}" />
  </step>
  <testObjects>
    <testObject testObjId="testObj:{upload-input}" platform="web" autoName="File Upload" fallbackName="Upload">
      <roles><role>element</role><role>input</role></roles>
      <idents active="Electors">
        <ident type="XPath"><![CDATA[{"implData":{"value":"(//input[@type='file' and @name='upload'])[1]"}}]]></ident>
        <ident type="Electors"><![CDATA[{"implData":{"electors":[{"id":"id","value":{"id":"uploadFile","isValidWord":true}},{"id":"name","value":"upload"},{"id":"input/type","value":"file"}]}}]]></ident>
      </idents>
    </testObject>
  </testObjects>
</truScript>`),
	}, ConvertOptions{ScriptName: "Embedded File Upload"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	upload := result.Script.Actions[1]
	if upload.Type != "browser_set_files" || upload.Selector != "#uploadFile" || upload.Target != "#uploadFile" {
		t.Fatalf("upload action = %#v, want browser_set_files with embedded id selector", upload)
	}
	if upload.Value != `C:\Users\qa\upload.png` {
		t.Fatalf("upload value = %q, want Windows file path preserved", upload.Value)
	}
	if upload.Metadata["source"] != "truclient_set_file" || upload.Metadata["source_object_descriptor_id"] != "testObj:{upload-input}" {
		t.Fatalf("upload metadata = %#v, want file upload source and descriptor id", upload.Metadata)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

func TestConvertArchiveMapsEmbeddedPressKey(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript platform="TruClient Browser118.0" engineVersion="2024.3.0.53">
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://advantage.example.com/search\&quot;&quot;}}" />
    <step type="testObject" testObject="testObj:{search-box}" action="Type" index="2" arguments="{&quot;Value&quot;:{&quot;value&quot;:&quot;hp&quot;}}" />
    <step type="testObject" testObject="testObj:{search-box}" action="Press Key" index="3" arguments="{&quot;Key Name&quot;:{&quot;value&quot;:&quot;Enter&quot;}}" />
  </step>
  <testObjects>
    <testObject testObjId="testObj:{search-box}" platform="web" autoName="Search">
      <roles><role>element</role><role>focusable</role><role>textbox</role></roles>
      <idents active="Electors">
        <ident type="XPath"><![CDATA[{"implData":{"value":"(//input[@name='q'])[1]"}}]]></ident>
        <ident type="Electors"><![CDATA[{"implData":{"electors":[{"id":"id","value":{"id":"searchInput","isValidWord":true}},{"id":"name","value":"q"}]}}]]></ident>
      </idents>
    </testObject>
  </testObjects>
</truScript>`),
	}, ConvertOptions{ScriptName: "Embedded Press Key"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	fill := result.Script.Actions[1]
	if fill.Type != "browser_fill" || fill.Selector != "#searchInput" || fill.Value != "hp" {
		t.Fatalf("fill action = %#v, want embedded textbox fill", fill)
	}
	press := result.Script.Actions[2]
	if press.Type != "browser_press_key" || press.Selector != "#searchInput" || press.Value != "Enter" {
		t.Fatalf("press action = %#v, want selector-scoped browser_press_key Enter", press)
	}
	if press.Metadata["source"] != "truclient_press_key" || press.Metadata["source_object_descriptor_id"] != "testObj:{search-box}" {
		t.Fatalf("press metadata = %#v, want press-key source and descriptor id", press.Metadata)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

// TestConvertArchiveWeb2IframeWindowFixture is the web2_iframe_window fixture: the importer
// preserves frame/window context as action metadata and the inspector flags replay diagnostics
// so browser protocol replay can prove the context transition before production promotion.
func TestConvertArchiveWeb2IframeWindowFixture(t *testing.T) {
	files := map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://app.example.com\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#pay\&quot;&quot;},&quot;Frame&quot;:{&quot;value&quot;:&quot;\&quot;checkout-frame\&quot;&quot;},&quot;Window&quot;:{&quot;value&quot;:&quot;\&quot;primary-tab\&quot;&quot;}}" />
  </step>
</truScript>`),
	}
	result, err := ConvertArchive(files, ConvertOptions{ScriptName: "Web2 Iframe Window"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	var click dsl.Action
	var foundClick bool
	var foundSwitch bool
	for _, action := range result.Script.Actions {
		switch action.Type {
		case "browser_switch_window":
			foundSwitch = true
		case "browser_click":
			click = action
			foundClick = true
		}
	}
	if !foundSwitch {
		t.Fatalf("actions = %#v, want browser_switch_window before window-scoped click", result.Script.Actions)
	}
	if !foundClick {
		t.Fatalf("actions = %#v, want browser_click", result.Script.Actions)
	}
	if click.Metadata["source_frame_context"] != "checkout-frame" || click.Metadata["source_window_context"] != "primary-tab" {
		t.Fatalf("click metadata = %#v, want preserved frame/window context", click.Metadata)
	}
	if click.Params["frame_selector"] != `iframe[name="checkout-frame"], iframe[id="checkout-frame"], iframe[title="checkout-frame"]` {
		t.Fatalf("click params = %#v, want promoted frame_selector", click.Params)
	}

	inspection, err := InspectArchive(files, "TruClient 2.0 - Web")
	if err != nil {
		t.Fatalf("InspectArchive returned error: %v", err)
	}
	foundFrameResolved := false
	foundWindowResolved := false
	for _, step := range inspection.Steps {
		for _, diagnostic := range step.ReplayDiagnostics {
			if diagnostic.Code == "frame_context_resolved" {
				foundFrameResolved = true
			}
			if diagnostic.Code == "window_context_resolved" {
				foundWindowResolved = true
			}
		}
	}
	if !foundFrameResolved || !foundWindowResolved {
		t.Fatalf("inspection steps = %#v, want resolved frame/window diagnostics for the context-scoped step", inspection.Steps)
	}
}

// TestConvertArchiveWeb2CustomJavaScriptAPIFixture is the web2_custom_javascript_api fixture:
// decomposable jQuery/DOM helpers become native browser actions, while custom JavaScript that
// cannot be decomposed is preserved as an executable-but-review-gated browser_evaluate_js step.
func TestConvertArchiveWeb2CustomJavaScriptAPIFixture(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://app.example.com\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;$('#coupon').val('SAVE10');\ndocument.querySelector('#apply').click();\nwindow.MyApp.checkout({express: true})&quot;}}" />
  </step>
</truScript>`),
	}, ConvertOptions{ScriptName: "Web2 Custom JavaScript API"})
	if err != nil {
		t.Fatalf("ConvertArchive returned error: %v", err)
	}
	// Navigate + decomposed fill + decomposed click + non-decomposable evaluate_js.
	if len(result.Script.Actions) != 4 {
		t.Fatalf("actions len = %d, want 4: %#v", len(result.Script.Actions), result.Script.Actions)
	}
	if fill := result.Script.Actions[1]; fill.Type != "browser_fill" || fill.Selector != "#coupon" || fill.Value != "SAVE10" {
		t.Fatalf("action[1] = %#v, want decomposed browser_fill", fill)
	}
	if click := result.Script.Actions[2]; click.Type != "browser_click" || click.Selector != "#apply" {
		t.Fatalf("action[2] = %#v, want decomposed browser_click", click)
	}
	if evalAction := result.Script.Actions[3]; evalAction.Type != "browser_evaluate_js" || evalAction.Target != "window.MyApp.checkout({express: true})" || evalAction.Metadata["review_required"] != true {
		t.Fatalf("action[3] = %#v, want executable browser_evaluate_js fallback", evalAction)
	}
}

func encodeUTF16LE(text string) string {
	words := utf16.Encode([]rune(text))
	bytes := make([]byte, 0, len(words)*2)
	for _, word := range words {
		bytes = append(bytes, byte(word), byte(word>>8))
	}
	return string(bytes)
}

func assertBrowserClickAction(t *testing.T, action dsl.Action, selector string, source string, params map[string]any) {
	t.Helper()
	if action.Type != "browser_click" || action.Selector != selector || action.Target != selector {
		t.Fatalf("action = %#v, want browser_click on %s", action, selector)
	}
	if action.Metadata["source"] != source {
		t.Fatalf("action metadata = %#v, want source=%s", action.Metadata, source)
	}
	for key, want := range params {
		if action.Params[key] != want {
			t.Fatalf("action params = %#v, want %s=%#v", action.Params, key, want)
		}
	}
}
