package enterprisearchive

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestTruClientBrowserPreferencesRetainDeclarationsWithoutValues(t *testing.T) {
	content := "// user_pref(\"commented\", true);\n/* pref(\"also.commented\", 3); */\nuser_pref(\"browser.choice\", \"private-first\");\nuser_pref(\"browser.choice\", \"private-last//value\"); user_pref('network.limit', 3);\n"
	file := inspectTruClientPreferenceFile("Profile/user.js", content)
	if file.Declarations != 3 || file.Unparsed || len(file.Settings) != 2 {
		t.Fatalf("inventory=%+v", file)
	}
	row := file.Settings[0]
	if row.Key != "browser.choice" || len(row.DeclarationLines) != 2 || row.DeclarationLines[0] != 3 || row.LastParsedDeclarationLine != 4 || row.Status != RuntimeSettingDispositionCompatibilityOnly {
		t.Fatalf("declaration evidence=%+v", row)
	}
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{"private-first", "private-last", "commented"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("inventory disclosed a source value or fabricated a commented key")
		}
	}
}

func TestTruClientBrowserPreferenceUnknownAndBoundedInputsRemainGated(t *testing.T) {
	for _, source := range []string{
		`user_pref("known", true); runSomething(); user_pref("later", false);`,
		`user_pref("dynamic", computeValue());`,
		`user_pref("quoted", "user_pref(\"fabricated\", true);"); arbitraryCode();`,
		`/* unclosed comment`,
		strings.Repeat(" ", truClientBrowserProfileMaxBytes+1),
		strings.Repeat(`user_pref("known", true);`, truClientBrowserProfileMaxDeclarations+1),
	} {
		file := inspectTruClientPreferenceFile("user.js", source)
		if !file.Unparsed {
			t.Fatalf("unsupported source was reported complete")
		}
		for _, row := range file.Settings {
			if row.Key == "fabricated" {
				t.Fatal("string content fabricated a preference")
			}
		}
		result := &ConvertResult{Script: &dsl.Script{Protocol: "browser"}}
		applyTruClientBrowserProfileReview(result, map[string]string{"user.js": source})
		if len(result.Script.Init) != 1 || result.Script.Init[0].Metadata["execution_blocker"] == nil {
			t.Fatal("unknown preferences did not retain an execution gate")
		}
	}
}

func TestTruClientBrowserProfileRuntimeRowsPreservePerFileScopeAndGate(t *testing.T) {
	files := map[string]string{
		"Profile/user.js":                       `user_pref("security.csp.enable", true); user_pref("security.csp.enable", false);`,
		"Profile/user_modified_perfs.json":      `{"security.csp.enable":{"default":true,"locked":false,"type":"bool","value":true}}`,
		"Profile/TCIEAuthenticationFilters.ini": "[General]\nFilterType=private-policy\n[AllowList]\n[DenyList]\n",
	}
	result := &ConvertResult{Script: &dsl.Script{Protocol: "browser"}}
	applyTruClientBrowserProfileReview(result, files)
	inventory := result.Script.Metadata.Import.Preserved[truClientBrowserProfileKey].(truClientBrowserProfileInventory)
	if len(inventory.Files) != 3 || inventory.Files[1].Declarations != 2 || inventory.Files[2].Declarations != 1 {
		t.Fatalf("per-file inventory=%+v", inventory)
	}
	runtimeFiles := map[string]map[string]map[string]string{}
	dispositions := map[string]map[string]map[string]RuntimeSettingDisposition{}
	appendTruClientBrowserProfileRuntimeEvidence(result.Script.Metadata.Import.Preserved, runtimeFiles, dispositions)
	count := 0
	for name, sections := range dispositions {
		for section, keys := range sections {
			for key, row := range keys {
				count++
				if row.Status != RuntimeSettingDispositionCompatibilityOnly || len(row.Fields) > 0 || !strings.HasPrefix(runtimeFiles[name][section][key], "Preserved in source") {
					t.Fatal("profile evidence incorrectly claims native support")
				}
			}
		}
	}
	if count != 3 || len(result.SourceReview.Mappings) != 3 {
		t.Fatalf("rows=%d mappings=%d", count, len(result.SourceReview.Mappings))
	}
	encoded, _ := json.Marshal(result.Script)
	if strings.Contains(string(encoded), "private-policy") {
		t.Fatal("authentication filter value leaked into inventory")
	}
	var resumed dsl.Script
	if err := json.Unmarshal(encoded, &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.Init[0].Metadata["execution_blocker"] == nil {
		t.Fatal("profile review gate lost after serialization")
	}
}

func TestTruClientMalformedOverrideRemainsSourceScoped(t *testing.T) {
	for _, text := range []string{`[]`, `null`, `{"security.csp.enable":false}`, `{invalid`} {
		file := inspectTruClientPreferenceOverrides("user_modified_perfs.json", text)
		if !file.Unparsed {
			t.Fatal("invalid override source reported fully understood")
		}
	}
}

func TestTruClientPartialAndDefaultBranchPreferencesNeverClaimEffectiveValues(t *testing.T) {
	for _, text := range []string{
		`user_pref("x", true); mutatePrefs(); user_pref("x", false);`,
		`user_pref("x", true); pref("x", false);`,
	} {
		file := inspectTruClientPreferenceFile("user.js", text)
		if !file.Unparsed || file.Declarations != 1 || file.Settings[0].LastParsedDeclarationLine != 1 {
			t.Fatal("partial declaration evidence is incorrect")
		}
		encoded, _ := json.Marshal(file)
		if strings.Contains(string(encoded), "within_file_effective") || strings.Contains(file.Settings[0].Note, "is effective") {
			t.Fatal("partial or mixed branches incorrectly claim runtime effect")
		}
	}
}

func TestTruClientOverrideAndAuthenticationProvenanceRetainExactLinesAndDuplicates(t *testing.T) {
	file := inspectTruClientPreferenceOverrides("user_modified_perfs.json", "{\n\"security.csp.enable\":{\"value\":true},\n\"security.csp.enable\":{\"value\":false}\n}")
	if !file.Unparsed || file.Declarations != 2 || len(file.Settings) != 1 || file.Settings[0].DeclarationLines[0] != 2 || file.Settings[0].DeclarationLines[1] != 3 {
		t.Fatalf("duplicate override provenance=%+v", file)
	}
	file = inspectTruClientPreferenceOverrides("user_modified_perfs.json", "{\n\"security.csp.enable\":{\"value\":true,\"value\":false}\n}")
	if !file.Unparsed || file.Declarations != 1 || file.Settings[0].DeclarationLines[0] != 2 {
		t.Fatal("duplicate override subfields were silently accepted")
	}
	file = inspectTruClientAuthenticationFilters("TCIEAuthenticationFilters.ini", "[General]\nFilterType=first\n; comment\nFilterType=second\n[AllowList]\n[DenyList]\n")
	if file.Unparsed || file.Declarations != 2 || len(file.Settings) != 1 || file.Settings[0].DeclarationLines[0] != 2 || file.Settings[0].DeclarationLines[1] != 4 {
		t.Fatalf("INI provenance=%+v", file)
	}
}

func TestTruClientAuthenticationListsRetainValueFreeUnparsedEntryEvidence(t *testing.T) {
	file := inspectTruClientAuthenticationFilters("TCIEAuthenticationFilters.ini", "[General]\nFilterType=policy\n[AllowList]\nprivatefirst\nprivatesecond\n[DenyList]\nprivatethird\n")
	if !file.Unparsed || file.Declarations != 1 || len(file.Settings) != 1 || len(file.ListEntries) != 3 {
		t.Fatalf("list accounting=%+v", file)
	}
	if file.ListEntries[0].Section != "AllowList" || file.ListEntries[0].SourceLine != 4 || file.ListEntries[2].Section != "DenyList" || file.ListEntries[2].SourceLine != 7 {
		t.Fatal("list-entry source provenance missing")
	}
	encoded, _ := json.Marshal(file)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("raw source filter entries leaked into public inventory")
	}
	result := &ConvertResult{Script: &dsl.Script{Protocol: "browser"}}
	applyTruClientBrowserProfileReview(result, map[string]string{"TCIEAuthenticationFilters.ini": "[General]\nFilterType=policy\n[AllowList]\nprivatefirst\n"})
	if result.Script.Init[0].Metadata["execution_blocker"] == nil {
		t.Fatal("unparsed policy entries did not retain replay gate")
	}
	file = inspectTruClientAuthenticationFilters("TCIEAuthenticationFilters.ini", "[General]\nFilterType=policy\n[AllowList]\n"+strings.Repeat("private-entry?query=value\n", truClientBrowserProfileMaxDeclarations))
	if !file.Unparsed || file.Declarations != 1 || len(file.ListEntries) != truClientBrowserProfileMaxDeclarations-1 {
		t.Fatal("policy assignments and list records escaped the shared record bound")
	}
	encoded, _ = json.Marshal(file)
	if strings.Contains(string(encoded), "private-entry") {
		t.Fatal("policy record with '=' was mistaken for a disclosed setting key")
	}
}

func TestPlannedTruClientProfileSettingsReachRuntimeReviewPayload(t *testing.T) {
	result, err := ConvertZip(zipFilesForTest(t, map[string]string{
		"Review/Review.usr":               "[General]\nType=TruClientWeb\n",
		"Review/default.cfg":              "[TruClient]\nStepTimeout=180\n",
		"Review/default.xml":              `<truScript><step action="Navigate" stepId="first" arguments='{&quot;Location&quot;:{&quot;value&quot;:&quot;https://example.invalid/&quot;}}'/></truScript>`,
		"Review/user.js":                  `user_pref("browser.choice", "retained-only");`,
		"Review/user_modified_perfs.json": `{"browser.choice":{"default":"other","locked":false,"type":"string","value":"override-only"}}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	preserved := result.Script.Metadata.Import.Preserved
	runtimeFiles := preserved["runtime_files"].(map[string]map[string]map[string]string)
	dispositions := preserved["runtime_setting_dispositions"].(map[string]map[string]map[string]RuntimeSettingDisposition)
	if runtimeFiles["user.js"]["Source browser preferences"]["browser.choice"] != "Preserved in source (1 declarations)" || dispositions["user.js"]["Source browser preferences"]["browser.choice"].Status != RuntimeSettingDispositionCompatibilityOnly {
		t.Fatal("browser preferences missing from existing Runtime review contract")
	}
	if dispositions["default.cfg"]["TruClient"]["StepTimeout"].Status != RuntimeSettingDispositionMapped {
		t.Fatal("CFG mapping changed while appending profile evidence")
	}
	if len(result.Script.Init) != 2 {
		t.Fatalf("durable gates=%d want planned profile and browser preferences", len(result.Script.Init))
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("invalid draft=%v", issues)
	}
}
