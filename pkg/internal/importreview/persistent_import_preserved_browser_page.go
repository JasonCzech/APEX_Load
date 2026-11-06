package importreview

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/selector"
)

// A browser recording that keeps a captured value (a fill marked as a secret)
// types it into whatever page its earlier steps reach, and any page script
// step, anywhere in the script, can read it while it is in the field. For such
// a value "where the credential is sent" is the page flow around it, which
// the per-step context does not cover. An editor who cannot read the value
// could otherwise retarget the navigation before the fill, add a click on
// another link, add a script step that reads the field, or point the fill's
// self-heal fallbacks at another element.
//
// So while a browser script keeps a credential in one of its steps, its page
// flow is pinned: every step that can change the page, frame or window, run
// page script, or steer self-heal must survive unchanged and in order. Steps
// that only observe stay free, and hovers, scrolls and fills that keep
// nothing may be deleted (which only removes behaviour) but not added or
// edited. A recording that keeps no credential, the common case (a masked
// password becomes an {{env.*}} template), never reaches this check.

// browserPageValueLeaf reports a kept value the page receives: one in a
// browser or engine step's own fields (a fill's value, a script, a URL), not
// in its metadata, which no step sends, and not in a step that only matches or
// observes windows and pages (a window switch's URL, a wait). A value the
// page never receives cannot be typed into another page or read from it.
func browserPageValueLeaf(leafPath, actionPath string, actionContext any, seededSession bool) bool {
	if strings.HasPrefix(leafPath, actionPath+"/metadata") {
		// A seeded session opens a step's recorded page in place of the
		// sign-in (pkg/engine RecordedSignInSummary), so that URL is one a
		// page receives.
		return seededSession && strings.HasPrefix(leafPath, actionPath+"/metadata/source_url")
	}
	action, _ := actionContext.(map[string]any)
	switch kind := canonicalImportStepType(action); {
	case kind == "browser_switch_window", kind == "browser_close_window":
		return false
	default:
		return classifyBrowserPageStep(kind) != browserStepInert
	}
}

// browserStepClass is how the page-flow pin treats a step.
type browserStepClass uint8

const (
	// browserStepPinned must survive unchanged and in order.
	browserStepPinned browserStepClass = iota
	// browserStepInert only observes; it can be added, edited or deleted.
	browserStepInert
	// browserStepDroppable may be deleted but not added or edited: deleting
	// it only removes behaviour, but a new or edited one can change the page
	// or field the kept value reaches, or seed the self-heal cache.
	browserStepDroppable
)

// browserStepClasses classifies every browser_* catalog type. A type that is
// not listed is pinned (TestImportBrowserPageStepClassesCoverCatalog keeps
// this list complete).
var browserStepClasses = map[string]browserStepClass{
	// Observe only: they change no page and send nothing. A screenshot can
	// show a field a kept value was typed into; imported values are not run
	// evidence secrets (a standing decision), so they are not suppressed.
	"browser_wait":          browserStepInert,
	"browser_wait_response": browserStepInert,
	"browser_ws_wait":       browserStepInert,
	"browser_ws_assert":     browserStepInert,
	"browser_screenshot":    browserStepInert,

	"browser_hover":  browserStepDroppable,
	"browser_scroll": browserStepDroppable,
	"browser_fill":   browserStepDroppable,

	"browser_navigate":        browserStepPinned,
	"browser_back":            browserStepPinned,
	"browser_forward":         browserStepPinned,
	"browser_reload":          browserStepPinned,
	"browser_open_window":     browserStepPinned,
	"browser_switch_window":   browserStepPinned,
	"browser_close_window":    browserStepPinned,
	"browser_switch_frame":    browserStepPinned,
	"browser_click":           browserStepPinned,
	"browser_dblclick":        browserStepPinned,
	"browser_tap":             browserStepPinned,
	"browser_long_press":      browserStepPinned,
	"browser_swipe":           browserStepPinned,
	"browser_check":           browserStepPinned,
	"browser_select":          browserStepPinned,
	"browser_press_key":       browserStepPinned,
	"browser_set_files":       browserStepPinned,
	"browser_drag_and_drop":   browserStepPinned,
	"browser_download":        browserStepPinned,
	"browser_handle_dialog":   browserStepPinned,
	"browser_set_geolocation": browserStepPinned,
	"browser_evaluate_js":     browserStepPinned,
}

// engineInertStepTypes are engine steps that only mark or report and never
// touch the page. Every other engine step (control flow, other source_*,
// native_javascript, live tables) is pinned.
var engineInertStepTypes = map[string]struct{}{
	"source_transaction": {},
	"source_timer":       {},
	"source_message":     {},
	"error_message":      {},
	"rendezvous":         {},
}

// browserRuntimeMetadataKeys are the step metadata entries the browser
// runtime reads to choose a step's element, frame or page: self-heal
// candidates (pkg/protocol/browser/self_heal.go), frame scope
// (frame_actions.go frameSelectorsForAction), the self-heal cache identity,
// and the recorded sign-in tag (pkg/engine/recorded_sign_in.go). A new
// runtime read of step metadata must add its key here.
var browserRuntimeMetadataKeys = []string{
	selector.MetadataFallbacksKey, selector.MetadataCandidatesKey, "source_target_selector_candidates",
	"frame_selectors", "iframe_selectors", "frame_path", "iframe_path", "source_frame_path",
	"frame_selector", "source_frame_context", "frame_context",
	"recorder_step_seq", "source_step_id", "source_step_index", "source_action", "source_url",
	"salesforce_auth",
	// Read by run-evidence redaction (pkg/engine imported_credential_redaction.go)
	// and by masking: clearing one on a step that keeps a value changes how it
	// is handled.
	"source_marked_secret", "source_masked_secret",
	// The run-logic group a step belongs to (dsl.RunLogicActionMetadataKey).
	dsl.RunLogicActionMetadataKey,
}

// browserRuntimeMetadata is the part of a browser step's metadata its runtime
// reads, or nil for a step that is not a browser step or holds none of it.
func browserRuntimeMetadata(action map[string]any) map[string]any {
	if !strings.HasPrefix(canonicalImportStepType(action), "browser_") {
		return nil
	}
	metadata, _ := action["metadata"].(map[string]any)
	if len(metadata) == 0 {
		return nil
	}
	held := make(map[string]any)
	for _, key := range browserRuntimeMetadataKeys {
		if value, exists := metadata[key]; exists {
			held[key] = value
		}
	}
	if len(held) == 0 {
		return nil
	}
	return held
}

func canonicalImportStepType(action map[string]any) string {
	return dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(StringJSONValue(action["type"]))))
}

// importStepContext is what a step does, for comparing steps by content: its
// wire context without the fields that only attribute, pace or time it.
func importStepContext(action map[string]any) map[string]any {
	context := ImportActionWireContext(action)
	for field := range context {
		switch NormalizeScriptFieldName(field) {
		case "attribution", "pacingids", "timeoutms":
			delete(context, field)
		}
	}
	return context
}

func classifyBrowserPageStep(kind string) browserStepClass {
	if class, known := browserStepClasses[kind]; known {
		return class
	}
	if _, inert := engineInertStepTypes[kind]; inert {
		return browserStepInert
	}
	return browserStepPinned
}

// browserPageStep is one entry of a script's page flow.
type browserPageStep struct {
	// path is the step's context path; empty for the synthetic sign-in entry.
	path      string
	droppable bool
	entry     map[string]any
	// keptValue: the step is pinned because its own fields hold a kept value.
	// Binding that value (or, for a droppable type, deleting the step) is not
	// a change to the page flow the other kept values travel.
	keptValue     bool
	baseDroppable bool
}

// importBrowserPageSteps lists the page flow of a projected script: every
// step that is not inert, in walk order (init, actions, teardown, control
// bodies), with where it sits (container path, indices wildcarded), what it
// does (importStepContext) and its control block. A step whose context holds
// a masked value is pinned whatever its type, so the kept step is a fixed
// marker that cannot be moved. When the session strategy skips the recorded
// sign-in, a synthetic entry pins which init steps it skips and the page it
// opens instead, which the engine derives from step metadata.
func importBrowserPageSteps(script map[string]any) ([]browserPageStep, map[string][]browserPageStep, error) {
	var steps []browserPageStep
	walkImportActions(script, func(path string, action map[string]any) {
		context := importStepContext(action)
		class := classifyBrowserPageStep(canonicalImportStepType(action))
		// A kept value in a step's own fields fixes the step in place; one in
		// its metadata (a recorded page URL, a locator) leaves a hover,
		// scroll or fill droppable.
		fields := make(map[string]any, len(context))
		for key, value := range context {
			if NormalizeScriptFieldName(key) != "metadata" {
				fields[key] = value
			}
		}
		baseDroppable := class == browserStepDroppable
		keptValue := false
		if jsonValueContainsMaskedSentinel(fields) {
			keptValue, class = true, browserStepPinned
		}
		if class == browserStepInert {
			return
		}
		entry := map[string]any{"in": pageFlowPathPattern(parentImportContextPath(path)), "context": context}
		if block := pageFlowControlBlock(action); len(block) > 0 {
			entry["control"] = block
		}
		steps = append(steps, browserPageStep{path: path, droppable: class == browserStepDroppable, entry: entry, keptValue: keptValue, baseDroppable: baseDroppable})
	})
	config, _ := script["config"].(map[string]any)
	runLogic, _ := config["run_logic"].(map[string]any)
	blocks := runLogic["blocks"]
	seeds := dsl.BrowserSessionSeedsContext(browserSessionStrategy(script))
	if !seeds && isEmptyJSONValue(blocks) {
		return steps, nil, nil
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, nil, err
	}
	var decoded dsl.Script
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, nil, err
	}
	if seeds {
		steps = append(steps, browserPageStep{entry: map[string]any{"sign_in": engine.RecordedSignInSummary(&decoded)}})
	}
	// Run logic runs groups of top-level steps in its block order at run time
	// (dsl.RunLogicOrder), so the order a kept value meets its pages is the
	// blocks and which pinned steps each group runs, resolved the way the
	// engine resolves them (a step's run_logic_action tag, else its name).
	if !isEmptyJSONValue(blocks) {
		referenced := map[string]bool{}
		var collect func([]dsl.RunLogicBlock)
		collect = func(blocks []dsl.RunLogicBlock) {
			for _, block := range blocks {
				for _, child := range block.Children {
					if name := strings.ToLower(strings.TrimSpace(child.Action)); name != "" {
						referenced[name] = true
					}
					if child.Block != nil {
						collect([]dsl.RunLogicBlock{*child.Block})
					}
				}
			}
		}
		collect(decoded.Config.RunLogic.Blocks)
		// The page-flow entries under each top-level step, in walk order.
		byTop := map[int][]browserPageStep{}
		for _, step := range steps {
			rest, ok := strings.CutPrefix(step.path, "/actions/")
			if !ok {
				continue
			}
			index, _, _ := strings.Cut(rest, "/")
			if top, err := strconv.Atoi(index); err == nil {
				byTop[top] = append(byTop[top], step)
			}
		}
		groups := map[string][]browserPageStep{}
		var names []string
		for group, indexes := range dsl.RunLogicActionGroups(decoded.Actions) {
			// A group no block runs does not run at all.
			if !referenced[strings.ToLower(strings.TrimSpace(group))] {
				continue
			}
			names = append(names, group)
			// In run order: the group's steps by index, then each step's
			// entries in walk order. Droppable ones stay droppable within
			// the group; none may move into another group.
			var members []browserPageStep
			for _, index := range indexes {
				top := "/actions/" + strconv.Itoa(index)
				for _, step := range byTop[index] {
					member := step
					member.entry = map[string]any{"at": pageFlowPathPattern(strings.TrimPrefix(step.path, top)), "step": step.entry}
					members = append(members, member)
				}
			}
			groups[group] = members
		}
		sort.Strings(names)
		steps = append(steps, browserPageStep{entry: map[string]any{"run_logic": map[string]any{"blocks": blocks, "groups": names}}})
		return steps, groups, nil
	}
	return steps, nil, nil
}

func isEmptyJSONValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, child := range typed {
			if !isEmptyJSONValue(child) {
				return false
			}
		}
		return true
	case []any:
		return len(typed) == 0
	case string:
		return typed == ""
	case bool:
		return !typed
	case float64:
		return typed == 0
	}
	return false
}

// browserSessionSeeds reports a session strategy that skips the recorded
// sign-in and opens its landing page instead.
func browserSessionSeeds(script map[string]any) bool {
	return dsl.BrowserSessionSeedsContext(browserSessionStrategy(script))
}

func browserSessionStrategy(script map[string]any) string {
	config, _ := script["config"].(map[string]any)
	browser, _ := config["browser"].(map[string]any)
	session, _ := browser["session"].(map[string]any)
	return strings.TrimSpace(StringJSONValue(session["strategy"]))
}

func jsonValueContainsMaskedSentinel(value any) bool {
	payload, err := json.Marshal(value)
	return err != nil || strings.Contains(string(payload), MaskedScriptSecret)
}

func parentImportContextPath(path string) string {
	if slash := strings.LastIndex(path, "/"); slash >= 0 {
		return path[:slash]
	}
	return ""
}

// pageFlowStepLists are the containers whose numeric children are step
// positions. Only those indices are wildcarded in the page flow; a switch
// case or parallel branch index stays literal, so a step moved into another
// case or branch is a change.
var pageFlowStepLists = map[string]struct{}{
	"init": {}, "actions": {}, "teardown": {}, "steps": {}, "else": {}, "default": {}, "requests": {},
}

func pageFlowPathPattern(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if index == 0 || segment == "" || strings.Trim(segment, "0123456789") != "" {
			continue
		}
		if _, stepList := pageFlowStepLists[NormalizeScriptFieldName(segments[index-1])]; stepList {
			segments[index] = "*"
		}
	}
	return strings.Join(segments, "/")
}

// pageFlowControlBlock is a control step's own settings plus, in order, each
// switch case's and parallel branch's settings without their steps: which
// case matches decides which pinned steps run.
func pageFlowControlBlock(action map[string]any) map[string]any {
	block := importControlBlock(action)
	control, _ := action["control"].(map[string]any)
	for _, key := range []string{"cases", "branches"} {
		arms, _ := control[key].([]any)
		if len(arms) == 0 {
			continue
		}
		heads := make([]any, 0, len(arms))
		for _, arm := range arms {
			fields, _ := arm.(map[string]any)
			head := make(map[string]any, len(fields))
			for field, value := range fields {
				if NormalizeScriptFieldName(field) != "steps" {
					head[field] = value
				}
			}
			heads = append(heads, head)
		}
		block[key] = heads
	}
	return block
}

// alignBrowserPageSteps checks that candidate is prior with only droppable
// entries removed. It returns the matched (candidate, prior) step paths, and
// on a change the candidate or prior path where it was found. Greedy matching
// is exact here: two equal entries are of one type, so of one class.
func alignBrowserPageSteps(candidate, prior []browserPageStep) (pairs [][2]string, changedAt string, changed bool) {
	next := 0
	for _, step := range candidate {
		for {
			if next >= len(prior) {
				return nil, step.path, true
			}
			if reflect.DeepEqual(step.entry, prior[next].entry) || entryDropsOnlyLocatorLists(step.entry, prior[next].entry) ||
				prior[next].keptValue && !step.keptValue && keptValueBindable(prior[next].entry) &&
					(pageEntryBindsKeptValue(step.entry, prior[next].entry) || pageEntryBindsKeptValue(step.entry, priorWithoutDroppedLocatorLists(step.entry, prior[next].entry))) {
				pairs = append(pairs, [2]string{step.path, prior[next].path})
				next++
				break
			}
			if !prior[next].droppable && !(prior[next].keptValue && prior[next].baseDroppable) {
				// An added or edited step lands here too: whatever the
				// candidate holds instead does not match this pinned one.
				return nil, firstNonEmptyImportPath(step.path, prior[next].path), true
			}
			next++
		}
	}
	for ; next < len(prior); next++ {
		if !prior[next].droppable && !(prior[next].keptValue && prior[next].baseDroppable) {
			return nil, prior[next].path, true
		}
	}
	return pairs, "", false
}

// pageEntryBindsKeptValue reports a candidate step that is the prior step with
// its kept values replaced (bound to a runtime secret or cleared) and nothing
// else changed but the marks that recorded the value as captured.
func pageEntryBindsKeptValue(candidate, prior any) bool {
	switch typed := prior.(type) {
	case string:
		if strings.Contains(typed, MaskedScriptSecret) {
			text, ok := candidate.(string)
			return ok && stringBindsMaskedSpans(text, typed)
		}
		return candidate == prior
	case map[string]any:
		other, ok := candidate.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range typed {
			counterpart, present := other[key]
			if !present {
				if keptValueMarkKey(key) {
					continue
				}
				// Metadata that held only the marks is dropped with them.
				if nested, ok := value.(map[string]any); ok && pageEntryBindsKeptValue(map[string]any{}, nested) {
					continue
				}
				return false
			}
			if !pageEntryBindsKeptValue(counterpart, value) {
				return false
			}
		}
		for key := range other {
			if _, present := typed[key]; !present && !keptValueMarkKey(key) {
				return false
			}
		}
		return true
	case []any:
		other, ok := candidate.([]any)
		if !ok || len(other) != len(typed) {
			return false
		}
		for index := range typed {
			if !pageEntryBindsKeptValue(other[index], typed[index]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(candidate, prior)
}

// entryDropsOnlyLocatorLists reports a candidate that is the prior step with
// whole recorded selector-candidate or fallback lists removed. That only
// narrows where the step can self-heal, and is how a recorded locator that
// quotes a kept value is dropped when the value is bound.
func entryDropsOnlyLocatorLists(candidate, prior map[string]any) bool {
	trimmed := priorWithoutDroppedLocatorLists(candidate, prior)
	return trimmed != nil && reflect.DeepEqual(candidate, trimmed)
}

// priorWithoutDroppedLocatorLists is prior without the recorded locator
// lists the candidate no longer has, or nil when it dropped none.
func priorWithoutDroppedLocatorLists(candidate, prior map[string]any) map[string]any {
	// A run-logic group member is wrapped as {at, step}: compare its step at
	// the same place.
	if priorStep, wrapped := prior["step"].(map[string]any); wrapped {
		candidateStep, _ := candidate["step"].(map[string]any)
		if !reflect.DeepEqual(candidate["at"], prior["at"]) {
			return nil
		}
		trimmed := priorWithoutDroppedLocatorLists(candidateStep, priorStep)
		if trimmed == nil {
			return nil
		}
		rebuilt := make(map[string]any, len(prior))
		for key, value := range prior {
			rebuilt[key] = value
		}
		rebuilt["step"] = trimmed
		return rebuilt
	}
	priorContext, _ := prior["context"].(map[string]any)
	priorMetadata, _ := priorContext["metadata"].(map[string]any)
	if len(priorMetadata) == 0 {
		return nil
	}
	candidateContext, _ := candidate["context"].(map[string]any)
	candidateMetadata, _ := candidateContext["metadata"].(map[string]any)
	trimmed := make(map[string]any, len(priorMetadata))
	dropped := false
	for key, value := range priorMetadata {
		if _, kept := candidateMetadata[key]; !kept && selectorLocatorListKey(key) {
			dropped = true
			continue
		}
		trimmed[key] = value
	}
	if !dropped {
		return nil
	}
	context := make(map[string]any, len(priorContext))
	for key, value := range priorContext {
		context[key] = value
	}
	if len(trimmed) == 0 {
		delete(context, "metadata")
	} else {
		context["metadata"] = trimmed
	}
	rebuilt := make(map[string]any, len(prior))
	for key, value := range prior {
		rebuilt[key] = value
	}
	rebuilt["context"] = context
	return rebuilt
}

func selectorLocatorListKey(key string) bool {
	switch key {
	case selector.MetadataCandidatesKey, selector.MetadataFallbacksKey, "source_target_selector_candidates":
		return true
	}
	return false
}

// keptValueBindable reports a pinned step whose kept value can be bound on
// its own while other kept values remain: only a value the step types or
// picks (a fill, select or key press value, or a select's values). Bound
// there, whatever the binding resolves to is typed into the same pinned
// field. Anywhere else (a URL, a locator, a page script) a binding the
// editor controls could choose where the step goes or what it runs, so those
// kept values are bound together with the rest in one save.
func keptValueBindable(entry map[string]any) bool {
	if step, wrapped := entry["step"].(map[string]any); wrapped {
		entry = step
	}
	context, _ := entry["context"].(map[string]any)
	rest := make(map[string]any, len(context))
	for key, value := range context {
		switch NormalizeScriptFieldName(key) {
		case "value", "type":
			continue
		case "params":
			if params, ok := value.(map[string]any); ok {
				trimmed := make(map[string]any, len(params))
				for name, param := range params {
					if NormalizeScriptFieldName(name) != "values" {
						trimmed[name] = param
					}
				}
				value = trimmed
			}
		case "metadata":
			// Recorded locator lists may be dropped with the binding.
			if metadata, ok := value.(map[string]any); ok {
				trimmed := make(map[string]any, len(metadata))
				for name, entry := range metadata {
					if !selectorLocatorListKey(name) {
						trimmed[name] = entry
					}
				}
				value = trimmed
			}
		}
		rest[key] = value
	}
	return !jsonValueContainsMaskedSentinel(rest)
}

// keptValueBinding is what a kept value may become: a runtime binding.
var keptValueBinding = regexp.MustCompile(`^\{\{\s*(?:secret|env)\.[A-Za-z0-9_.-]+\s*\}\}$`)

// stringBindsMaskedSpans reports a candidate equal to prior outside its
// masked spans, each span replaced by a runtime binding or removed. Anything
// else (a retargeted URL, a rewritten page script) is a change.
func stringBindsMaskedSpans(candidate, prior string) bool {
	pieces := strings.Split(prior, MaskedScriptSecret)
	if !strings.HasPrefix(candidate, pieces[0]) {
		return false
	}
	rest := candidate[len(pieces[0]):]
	for index, piece := range pieces[1:] {
		last := index == len(pieces)-2
		var span string
		if last {
			if !strings.HasSuffix(rest, piece) || len(rest) < len(piece) {
				return false
			}
			span, rest = rest[:len(rest)-len(piece)], ""
		} else {
			at := strings.Index(rest, piece)
			if piece == "" || at < 0 {
				// Adjacent spans cannot be told apart; refuse rather than guess.
				return false
			}
			span, rest = rest[:at], rest[at+len(piece):]
		}
		if span != "" && !keptValueBinding.MatchString(span) {
			return false
		}
	}
	return true
}

func keptValueMarkKey(key string) bool {
	switch NormalizeScriptFieldName(key) {
	case "sourcemarkedsecret", "sourcemaskedsecret":
		return true
	}
	return false
}

func firstNonEmptyImportPath(paths ...string) string {
	for _, path := range paths {
		if path != "" {
			return path
		}
	}
	return ""
}
