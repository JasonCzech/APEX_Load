package importreview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
)

// recorderKeptValue is the captured value of a fill the operator marked as a
// secret: the saved recording keeps it for replay and every read masks it.
const recorderKeptValue = "Synth-K3pt-Value-001"

// recorderGeneratedLocator is a Lightning CSS path whose generated element
// name the free-text scrubber reads as a token.
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

// recorderCredentialScript is a saved browser recording shaped like a guided
// Salesforce capture: repeated hovers, a password typed in bursts (one masked
// fill per burst, all the same step identity), a long generated selector, a
// sign-in step tagged for init, and one fill marked as a secret whose captured
// value the recording keeps.
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

// reconcileRecorderEdit runs a masked read-modify-write the way a save or a
// debug start of an edited copy does.
func reconcileRecorderEdit(t *testing.T, prior *dsl.Script, edit func(*dsl.Script)) (*dsl.Script, error) {
	t.Helper()
	candidate := MaskScriptSecrets(prior)
	if candidate == nil {
		t.Fatal("MaskScriptSecrets failed")
	}
	edit(candidate)
	if err := ReconcileMaskedScriptSecretsWithTrust(candidate, prior, false); err != nil {
		return candidate, err
	}
	return candidate, ValidatePreservedImportCredentialEditsWithOptions(candidate, prior, PreservedImportEditOptions{})
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

// Deleting steps before the kept credential, the repeated password bursts
// among them, and switching the browser to headed with screenshots on error
// must neither refuse the edit nor move the kept value.
func TestRecorderEditDeletingStepsAndChangingBrowserSettingsIsAccepted(t *testing.T) {
	prior := recorderCredentialScript()
	candidate, err := reconcileRecorderEdit(t, prior, func(script *dsl.Script) {
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
	})
	if err != nil {
		t.Fatalf("edit refused: %v", err)
	}
	code := candidate.Actions[recorderActionIndex(t, candidate.Actions, "Fill code")]
	if code.Value != recorderKeptValue {
		t.Fatalf("kept value restored as %q, want it on its own step", code.Value)
	}
	for _, action := range candidate.Actions {
		if action.Name != "Fill code" && strings.Contains(action.Value, recorderKeptValue) {
			t.Fatalf("kept value restored into %q", action.Name)
		}
	}
}

// Clearing or removing the mark that keeps a captured value masked would
// show the value on the next read.
func TestRecorderEditClearingSecretMarkIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{name: "cleared", edit: func(metadata map[string]any) { metadata["source_marked_secret"] = false }},
		{name: "removed", edit: func(metadata map[string]any) { delete(metadata, "source_marked_secret") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior := recorderCredentialScript()
			_, err := reconcileRecorderEdit(t, prior, func(script *dsl.Script) {
				tc.edit(script.Actions[recorderActionIndex(t, script.Actions, "Fill code")].Metadata)
			})
			if err == nil || !strings.Contains(err.Error(), "would show the imported credential") {
				t.Fatalf("err = %v, want the disclosure refused", err)
			}
			if strings.Contains(err.Error(), recorderKeptValue) {
				t.Fatal("the refusal quotes the kept value")
			}
		})
	}
}

func TestRecorderEditKeepsCredentialRoutingPinned(t *testing.T) {
	prior := recorderCredentialScript()
	_, err := reconcileRecorderEdit(t, prior, func(script *dsl.Script) {
		script.Config.Browser.Proxy.Server = "http://proxy.example.test:3128"
	})
	if err == nil || !strings.Contains(err.Error(), "execution context") {
		t.Fatalf("err = %v, want a proxy change refused while a captured value is kept", err)
	}
}

func TestRecorderEditCannotCopyOrAddCredentials(t *testing.T) {
	prior := recorderCredentialScript()
	_, err := reconcileRecorderEdit(t, prior, func(script *dsl.Script) {
		copied := script.Actions[recorderActionIndex(t, script.Actions, "Fill code")]
		copied.Name, copied.Selector, copied.Target = "Fill search", "#search", "#search"
		script.Actions = append(script.Actions, copied)
	})
	if err == nil {
		t.Fatal("copying the masked value into a new step was accepted")
	}
	_, err = reconcileRecorderEdit(t, prior, func(script *dsl.Script) {
		script.Actions = append(script.Actions, dsl.Action{
			Name: "Fill pin", Type: "browser_fill", Selector: "#pin", Target: "#pin", Value: "Synth-New-Literal-002",
			Metadata: map[string]any{"source_marked_secret": true},
		})
	})
	if err == nil || !strings.Contains(err.Error(), "introduces new executable credentials") {
		t.Fatalf("err = %v, want a new marked literal refused", err)
	}
}

// keptDuplicateFills is a step list whose two kept credentials share one step
// identity and carry no import_action_uid, after an unrelated first step (a
// hover, which a recording that keeps a value may delete).
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

// Without a step identity the kept steps cannot be matched once the list
// changes length; the refusal says so rather than calling them new.
func TestUnmatchedStepsAfterDeletionReportMatchFailure(t *testing.T) {
	prior := keptDuplicateFills()
	candidate := CloneScriptDefinition(prior)
	candidate.Actions = candidate.Actions[1:]
	err := ValidatePreservedImportCredentialEditsWithOptions(candidate, prior, PreservedImportEditOptions{})
	if err == nil || !strings.Contains(err.Error(), "could not match the edited steps of actions") {
		t.Fatalf("err = %v, want the unmatched steps reported", err)
	}
}

// With the identity a saved recording now carries, the same deletion matches
// each kept step to its stored self.
func TestStampedStepsMatchAfterDeletion(t *testing.T) {
	prior := keptDuplicateFills()
	EnsureScriptImportActionUIDs(prior)
	candidate := CloneScriptDefinition(prior)
	candidate.Actions = candidate.Actions[1:]
	if err := ValidatePreservedImportCredentialEditsWithOptions(candidate, prior, PreservedImportEditOptions{}); err != nil {
		t.Fatalf("edit refused: %v", err)
	}
	swapped := CloneScriptDefinition(candidate)
	swapped.Actions[0].Value, swapped.Actions[1].Value = swapped.Actions[1].Value, swapped.Actions[0].Value
	if err := ValidatePreservedImportCredentialEditsWithOptions(swapped, prior, PreservedImportEditOptions{}); err == nil {
		t.Fatal("swapping the kept values between steps was accepted")
	}
}

// A recording saved before conversions stamped step identities gets them on its
// first checked save, and a later deletion before its repeated kept fills then
// matches each one to its stored self.
func TestRecorderSaveBackfillsStepIdentities(t *testing.T) {
	prior := keptDuplicateFills()
	edited := MaskScriptSecrets(prior)
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
	if err := ValidatePreservedImportCredentialEditsWithOptions(next, saved, PreservedImportEditOptions{}); err != nil {
		t.Fatalf("deletion after the backfill refused: %v", err)
	}
}

// A text locator in a candidate list that quotes a kept value is masked there
// too: only the locator shape is kept as recorded.
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
