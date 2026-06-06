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
	if action := result.Script.Actions[1]; action.Type != "browser_evaluate_js" {
		t.Fatalf("action[1] type = %s, want browser_evaluate_js", action.Type)
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
	if action := result.Script.Actions[1]; action.Type != "browser_evaluate_js" {
		t.Fatalf("action[1] type = %s, want browser_evaluate_js", action.Type)
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
