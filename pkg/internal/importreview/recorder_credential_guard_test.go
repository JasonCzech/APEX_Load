package importreview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
)

const recorderKeptValue = "Synth-K3pt-Value-001"

const recorderGeneratedLocator = "#brandBand_2 > div > one-record-home-flexipage2 forcegenerated-flexipage_account_rec_l_8f3b2c1d9e7a6b5c4d3e2f1a__js > slot >> visible=true"

func recorderHover(selectorValue string) dsl.Action {
	return dsl.Action{Name: "Hover", Type: "browser_hover", Selector: selectorValue, Target: selectorValue}
}

func recorderMaskedPasswordFill() dsl.Action {
	return dsl.Action{
		Name: "Fill password", Type: "browser_fill", Selector: "#password", Target: "#password", Value: "{{env.PW}}",
		Metadata: map[string]any{"source_masked_secret": true},
	}
}

func recorderCredentialScript() *dsl.Script {
	script := &dsl.Script{
		Name: "Recorder credential guard", Version: "1", Protocol: "browser",
		Config: dsl.ScriptConfig{BaseURL: "https://app.example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:    "browser_recorder",
			Preserved: map[string]any{"recorded_steps": 12},
		}},
		Init: []dsl.Action{
			{Name: "Hover sign-in", Type: "browser_hover", Selector: "#marketing", Target: "#marketing", Metadata: map[string]any{"salesforce_auth": true}},
		},
		Actions: []dsl.Action{
			recorderHover("#login_form"),
			recorderHover("#login_form"),
			{Name: "Fill username", Type: "browser_fill", Selector: "#username", Target: "#username", Value: "synthetic-user@example.test"},
			recorderMaskedPasswordFill(),
			recorderMaskedPasswordFill(),
			{Name: "Click main", Type: "browser_click", Selector: "#main", Target: "#main"},
			recorderMaskedPasswordFill(),
			{Name: "Hover record", Type: "browser_hover", Selector: recorderGeneratedLocator, Target: recorderGeneratedLocator,
				Metadata: map[string]any{selector.MetadataCandidatesKey: []any{
					map[string]any{"kind": "css", "value": recorderGeneratedLocator, "confidence": float64(68), "reason": "Computed CSS path captured at recording time."},
				}}},
			{Name: "Fill code", Type: "browser_fill", Selector: "#code", Target: "#code", Value: recorderKeptValue,
				Metadata: map[string]any{"source_marked_secret": true}},
			{Name: "Click Login", Type: "browser_click", Selector: "#Login", Target: "#Login"},
		},
	}
	MarkCredentialPreservingImportScript(script)
	return script
}

func recorderActionIndex(t *testing.T, actions []dsl.Action, name string) int {
	t.Helper()
	for index, action := range actions {
		if action.Name == name {
			return index
		}
	}
	t.Fatalf("no action named %q", name)
	return -1
}

func saveUnredactedRecorderEdit(t *testing.T, prior *dsl.Script, edit func(*dsl.Script)) (*dsl.Script, error) {
	t.Helper()
	candidate := CloneScriptDefinition(prior)
	if candidate == nil {
		t.Fatal("CloneScriptDefinition failed")
	}
	edit(candidate)
	return PreparePersistentLibraryScript(candidate, prior)
}

func TestRecorderProjectionKeepsFlagsAndSelectorCandidates(t *testing.T) {
	masked := MaskScriptSecrets(recorderCredentialScript())
	if masked == nil {
		t.Fatal("MaskScriptSecrets failed")
	}
	if got := masked.Init[0].Metadata["salesforce_auth"]; got != true {
		t.Fatalf("init salesforce_auth = %#v, want the flag kept", got)
	}
	password := recorderActionIndex(t, masked.Actions, "Fill password")
	if got := masked.Actions[password].Metadata["source_masked_secret"]; got != true {
		t.Fatalf("source_masked_secret = %#v, want the flag kept", got)
	}
	hover := masked.Actions[recorderActionIndex(t, masked.Actions, "Hover record")]
	candidates, _ := hover.Metadata[selector.MetadataCandidatesKey].([]any)
	if len(candidates) != 1 || candidates[0].(map[string]any)["value"] != recorderGeneratedLocator {
		t.Fatalf("selector candidates = %#v, want the locator kept", hover.Metadata[selector.MetadataCandidatesKey])
	}
	if got := masked.Actions[recorderActionIndex(t, masked.Actions, "Fill code")].Value; got != MaskedScriptSecret {
		t.Fatalf("marked-secret fill = %q, want it masked", got)
	}
}

func TestRecorderEditDeletingStepsAndChangingBrowserSettingsIsAccepted(t *testing.T) {
	prior := recorderCredentialScript()
	candidate, err := saveUnredactedRecorderEdit(t, prior, func(script *dsl.Script) {
		kept := script.Actions[:0]
		for index, action := range script.Actions {
			if action.Type == "browser_hover" || index == 3 {
				continue
			}
			kept = append(kept, action)
		}
		script.Actions = kept
		script.Init = nil
		headed := false
		script.Config.Browser.Headless = &headed
		script.Config.Browser.CaptureScreenshots = "on_error"
		script.Config.Browser.Proxy.Server = "http://proxy.example.test:3128"
	})
	if err != nil {
		t.Fatalf("edit refused: %v", err)
	}
	code := candidate.Actions[recorderActionIndex(t, candidate.Actions, "Fill code")]
	if code.Value != recorderKeptValue {
		t.Fatalf("kept value = %q, want %q", code.Value, recorderKeptValue)
	}
	if candidate.Config.Browser.Proxy.Server != "http://proxy.example.test:3128" {
		t.Fatalf("proxy server = %q, want updated proxy server", candidate.Config.Browser.Proxy.Server)
	}
}

func TestRecorderEditAllowsSecretMarkProxyAndCredentialEdits(t *testing.T) {
	prior := recorderCredentialScript()
	saved, err := saveUnredactedRecorderEdit(t, prior, func(script *dsl.Script) {
		delete(script.Actions[recorderActionIndex(t, script.Actions, "Fill code")].Metadata, "source_marked_secret")
		script.Actions = append(script.Actions, dsl.Action{
			Name: "Fill pin", Type: "browser_fill", Selector: "#pin", Target: "#pin", Value: "Synth-New-Literal-002",
			Metadata: map[string]any{"source_marked_secret": true},
		})
	})
	if err != nil {
		t.Fatalf("expected recorder edits to succeed, got %v", err)
	}
	if got := saved.Actions[len(saved.Actions)-1].Value; got != "Synth-New-Literal-002" {
		t.Fatalf("new fill value = %q, want Synth-New-Literal-002", got)
	}
}

func keptDuplicateFills() *dsl.Script {
	kept := func(value string) dsl.Action {
		return dsl.Action{Name: "Fill code", Type: "browser_fill", Selector: "#code", Target: "#code", Value: value,
			Metadata: map[string]any{"source_marked_secret": true}}
	}
	script := &dsl.Script{
		Name: "Duplicate kept fills", Version: "1", Protocol: "browser",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "browser_recorder"}},
		Actions: []dsl.Action{
			{Name: "Hover start", Type: "browser_hover", Selector: "#start", Target: "#start"},
			kept("Synth-K3pt-Value-101"),
			kept("Synth-K3pt-Value-102"),
		},
	}
	MarkCredentialPreservingImportScript(script)
	return script
}

func TestRecorderSaveBackfillsStepIdentities(t *testing.T) {
	prior := keptDuplicateFills()
	edited := CloneScriptDefinition(prior)
	edited.Actions[0].Name = "Hover start (renamed)"
	saved, err := PreparePersistentLibraryScript(edited, prior)
	if err != nil {
		t.Fatalf("save refused: %v", err)
	}
	seen := map[string]bool{}
	for _, action := range saved.Actions {
		uid, _ := action.Metadata[dsl.ImportActionUIDMetadataKey].(string)
		if !dsl.IsValidImportActionUID(uid) || seen[uid] {
			t.Fatalf("saved step %q has import_action_uid %#v, want a unique stamped identity", action.Name, action.Metadata[dsl.ImportActionUIDMetadataKey])
		}
		seen[uid] = true
	}
	next := CloneScriptDefinition(saved)
	next.Actions = next.Actions[1:]
	next.Actions[0].Value, next.Actions[1].Value = next.Actions[1].Value, next.Actions[0].Value
	if _, err := PreparePersistentLibraryScript(next, saved); err != nil {
		t.Fatalf("deletion and value swap after backfill refused: %v", err)
	}
}

func TestRecorderProjectionMasksKeptValueInsideCandidates(t *testing.T) {
	script := recorderCredentialScript()
	click := &script.Actions[recorderActionIndex(t, script.Actions, "Click Login")]
	click.Metadata = map[string]any{selector.MetadataCandidatesKey: []any{
		map[string]any{"kind": "text", "value": `text="Your code is ` + recorderKeptValue + `"`, "confidence": float64(62), "reason": "Visible text " + recorderKeptValue},
	}}
	masked := MaskScriptSecrets(script)
	payload := masked.Actions[recorderActionIndex(t, masked.Actions, "Click Login")].Metadata[selector.MetadataCandidatesKey]
	if text := strings.TrimSpace(fmt.Sprint(payload)); strings.Contains(text, recorderKeptValue) || !strings.Contains(text, MaskedScriptSecret) {
		t.Fatalf("candidates = %s, want the kept value masked inside them", text)
	}
}
