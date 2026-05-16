package trueclient

import (
	"testing"
	"unicode/utf16"

	"github.com/jasonczech/stormrunner/pkg/dsl"
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
    <step type="testObject" action="Set / Type" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#username\&quot;&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;\&quot;stormrunner\&quot;&quot;}}" />
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
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#username" || action.Value != "stormrunner" {
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

func TestConvertArchiveMapsWaitAndPreservesTransactions(t *testing.T) {
	result, err := ConvertArchive(map[string]string{
		"default.xml": encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="util" action="Start Transaction" index="1" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;Login\&quot;&quot;}}" />
    <step type="testObject" action="Navigate" index="2" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="testObject" action="Set / Type" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#username\&quot;&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;\&quot;stormrunner\&quot;&quot;}}" />
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

func encodeUTF16LE(text string) string {
	words := utf16.Encode([]rune(text))
	bytes := make([]byte, 0, len(words)*2)
	for _, word := range words {
		bytes = append(bytes, byte(word), byte(word>>8))
	}
	return string(bytes)
}
