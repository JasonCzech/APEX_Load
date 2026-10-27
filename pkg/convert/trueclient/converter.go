package trueclient

import (
	"encoding/xml"
	"fmt"
	filepath "path"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

type ConvertOptions struct {
	ScriptName               string
	BaseURL                  string
	PreserveTransactions     bool
	PreserveUnsupportedSteps bool
}

type ConvertStats struct {
	TotalSteps       int
	ConvertedActions int
	Skipped          int
	Transactions     int
	UnsupportedSteps []string
	// ReviewOnlySteps counts published toolbox steps that were preserved as
	// review-only source_review actions (not executable, but NOT silently
	// skipped). Distinct from Skipped/UnsupportedSteps, which are reserved for
	// genuinely non-toolbox steps.
	ReviewOnlySteps     int
	ReviewOnlyStepNames []string
}

type ConvertResult struct {
	Script        *dsl.Script
	SourceXMLFile string
	Warnings      []string
	Stats         ConvertStats
}

type truScriptDocument struct {
	XMLName      xml.Name            `xml:"truScript"`
	Steps        []truXMLStep        `xml:"step"`
	TestObjects  []truXMLTestObject  `xml:"testObjects>testObject"`
	Transactions []truXMLTransaction `xml:"transactions>transaction"`
}

type truXMLStep struct {
	Type                 string       `xml:"type,attr"`
	StepID               string       `xml:"stepId,attr"`
	Action               string       `xml:"action,attr"`
	ActionName           string       `xml:"actionName,attr"`
	TestObject           string       `xml:"testObject,attr"`
	Arguments            string       `xml:"arguments,attr"`
	Index                string       `xml:"index,attr"`
	Steps                []truXMLStep `xml:"step"`
	Disabled             bool         `xml:"disabled,attr"`
	Optional             bool         `xml:"optional,attr"`
	Unfinalized          bool         `xml:"unfinalized,attr"`
	EndEvent             string       `xml:"endEvent,attr"`
	EndEventArgs         string       `xml:"endEventArgs,attr"`
	ObjectTimeout        string       `xml:"objectTimeout,attr"`
	ManualDuration       string       `xml:"manualDuration,attr"`
	RecordedDuration     string       `xml:"recDuration,attr"`
	AdditionalAttributes []xml.Attr   `xml:",any,attr"`
}

type truXMLTestObject struct {
	TestObjID    string           `xml:"testObjId,attr"`
	AutoName     string           `xml:"autoName,attr"`
	FallbackName string           `xml:"fallbackName,attr"`
	Roles        []string         `xml:"roles>role"`
	Idents       []truXMLObjectID `xml:"idents>ident"`
}

type truXMLObjectID struct {
	Type    string `xml:"type,attr"`
	Content string `xml:",chardata"`
}

type truArgument struct {
	Value          string `json:"value"`
	Param          bool   `json:"param,omitempty"`
	EvalJavaScript bool   `json:"evalJavaScript,omitempty"`
}

var (
	reJQueryVal             = regexp.MustCompile(`^\$\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.val\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)$`)
	reJQueryClick           = regexp.MustCompile(`^\$\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.click\(\s*\)$`)
	reJQueryTriggerClick    = regexp.MustCompile(`^\$\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.trigger\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)$`)
	reJQueryAttrValue       = regexp.MustCompile(`^\$\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.attr\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*,\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)$`)
	reQuerySelectorClick    = regexp.MustCompile(`^document\.querySelector\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.click\(\s*\)$`)
	reQuerySelectorValue    = regexp.MustCompile(`^document\.querySelector\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reQuerySelectorAllClick = regexp.MustCompile(`^document\.querySelectorAll\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)(?:\s*\[\s*0\s*\]|\s*\.item\(\s*0\s*\))\.click\(\s*\)$`)
	reQuerySelectorAllValue = regexp.MustCompile(`^document\.querySelectorAll\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)(?:\s*\[\s*0\s*\]|\s*\.item\(\s*0\s*\))\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reElementByIDClick      = regexp.MustCompile(`^document\.getElementById\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.click\(\s*\)$`)
	reElementByIDValue      = regexp.MustCompile(`^document\.getElementById\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reElementsByClassClick  = regexp.MustCompile(`^document\.getElementsByClassName\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)(?:\s*\[\s*0\s*\]|\s*\.item\(\s*0\s*\))\.click\(\s*\)$`)
	reElementsByClassValue  = regexp.MustCompile(`^document\.getElementsByClassName\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)(?:\s*\[\s*0\s*\]|\s*\.item\(\s*0\s*\))\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reElementsByTagClick    = regexp.MustCompile(`^document\.getElementsByTagName\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)(?:\s*\[\s*0\s*\]|\s*\.item\(\s*0\s*\))\.click\(\s*\)$`)
	reElementsByTagValue    = regexp.MustCompile(`^document\.getElementsByTagName\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)(?:\s*\[\s*0\s*\]|\s*\.item\(\s*0\s*\))\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reElementsByNameClick   = regexp.MustCompile(`^document\.getElementsByName\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)(?:\s*\[\s*0\s*\]|\s*\.item\(\s*0\s*\))\.click\(\s*\)$`)
	reElementsByNameValue   = regexp.MustCompile(`^document\.getElementsByName\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)(?:\s*\[\s*0\s*\]|\s*\.item\(\s*0\s*\))\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reFormElementClick      = regexp.MustCompile(`^document\.forms\s*\[\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*'|\d+)\s*\]\.elements\s*\[\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\]\.click\(\s*\)$`)
	reFormElementValue      = regexp.MustCompile(`^document\.forms\s*\[\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*'|\d+)\s*\]\.elements\s*\[\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\]\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reWindowScroll          = regexp.MustCompile(`^window\.scroll(?:To|By)\(\s*(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)\s*\)$`)
	reLocationAssign        = regexp.MustCompile(`^(?:window\.)?location(?:\.href)?\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reLocationMethod        = regexp.MustCompile(`^(?:window\.)?location\.(?:assign|replace)\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)$`)
	reSimpleCSSClassToken   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
	reSimpleHTMLTagToken    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)
)

// ConvertArchive converts a TruClient Mobile Web archive into a APEX Load browser script.
// The current MVP maps common browser steps and preserves unsupported steps as warnings.
func ConvertArchive(files map[string]string, opts ConvertOptions) (*ConvertResult, error) {
	xmlName, xmlContent, ok := findDefaultXML(files)
	if !ok {
		return nil, fmt.Errorf("no TruClient default.xml found in the ZIP archive")
	}

	decoded := decodeXMLContent(xmlContent)
	decoded = normalizeXMLDeclaration(decoded)
	var doc truScriptDocument
	if err := xml.Unmarshal([]byte(decoded), &doc); err != nil {
		return nil, fmt.Errorf("parse TruClient %s: %w", filepath.Base(xmlName), err)
	}

	result := &ConvertResult{
		SourceXMLFile: xmlName,
		Script: &dsl.Script{
			Name:          opts.ScriptName,
			Version:       "1.0.0",
			Protocol:      "browser",
			GeneratedBy:   "apexload-truclient-converter",
			GeneratedFrom: "enterprise_script_archive",
			Config:        dsl.ScriptConfig{},
			Actions:       make([]dsl.Action, 0),
		},
		Warnings: make([]string, 0),
	}

	descriptors := uniqueObjectDescriptors(append(collectObjectDescriptors(files), embeddedObjectDescriptorsFromDocument(xmlName, doc)...))
	baseURL := strings.TrimSpace(opts.BaseURL)
	state := truConvertState{
		opts:                 opts,
		descriptors:          descriptors,
		baseURL:              baseURL,
		currentWindowContext: "",
	}
	actions, err := convertStepSequence(executionSteps(doc.Steps), result, &state)
	if err != nil {
		return nil, err
	}
	result.Script.Actions = append(result.Script.Actions, actions...)
	applyXMLAnchoredTransactions(result, doc.Transactions, opts.PreserveTransactions)
	baseURL = state.baseURL

	if len(result.Script.Actions) == 0 {
		return nil, fmt.Errorf("no supported TruClient steps found in %s", filepath.Base(xmlName))
	}

	result.Script.Config.BaseURL = baseURL
	_ = dslwalk.WalkActions(result.Script, func(ref dslwalk.ActionRef) error {
		if ref.Action.Metadata == nil {
			ref.Action.Metadata = map[string]any{}
		}
		ref.Action.Metadata["source_file"] = xmlName
		return nil
	})
	count := 0
	for name := range files {
		if strings.EqualFold(filepath.Base(name), "default.xml") {
			count++
		}
	}
	if count > 1 {
		gate := dsl.Action{Name: "Select source TruClient XML", Type: "source_review", Metadata: map[string]any{"source_file": xmlName}}
		applyTruClientSemanticGate(&gate, "truclient_ambiguous_source_xml_requires_review", "Multiple source default.xml documents are present; the deterministic authoring draft requires an explicit source-project selection.")
		result.Script.Init = append(result.Script.Init, gate)
	}
	return result, nil
}

func executionSteps(steps []truXMLStep) []truXMLStep {
	actionDefs := collectActionDefinitions(steps)
	sequence := collectActionSequence(steps)
	if len(sequence) == 0 {
		return flattenCandidateSteps(steps)
	}

	executed := make([]truXMLStep, 0)
	for _, actionName := range sequence {
		block, ok := actionDefs[actionName]
		if !ok {
			continue
		}
		executed = append(executed, flattenActionBlock(block, actionDefs, nil)...)
	}
	if len(executed) == 0 {
		return flattenCandidateSteps(steps)
	}
	return executed
}

func collectActionDefinitions(steps []truXMLStep) map[string]truXMLStep {
	definitions := make(map[string]truXMLStep)
	var visit func(step truXMLStep)
	visit = func(step truXMLStep) {
		if strings.EqualFold(strings.TrimSpace(step.Action), "action") {
			name := strings.TrimSpace(step.ActionName)
			if name != "" {
				definitions[name] = step
			}
		}
		for _, child := range step.Steps {
			visit(child)
		}
	}
	for _, step := range steps {
		visit(step)
	}
	return definitions
}

func collectActionSequence(steps []truXMLStep) []string {
	sequence := make([]string, 0)
	var visit func(step truXMLStep)
	visit = func(step truXMLStep) {
		if step.Disabled {
			return
		}
		// Calls inside action definitions are expanded when their owning action is
		// flattened. Treating them as top-level run-logic references executes the
		// called action a second time.
		if strings.EqualFold(strings.TrimSpace(step.Action), "action") {
			return
		}
		if strings.EqualFold(strings.TrimSpace(step.Action), "Call Action") {
			actionName, err := parseArgumentValue(step.Arguments, "Action Name")
			if err == nil {
				actionName = trimQuotedValue(actionName)
				if actionName != "" {
					sequence = append(sequence, actionName)
				}
			}
		}
		for _, child := range step.Steps {
			visit(child)
		}
	}
	for _, step := range steps {
		visit(step)
	}
	return sequence
}

func flattenActionBlock(step truXMLStep, definitions map[string]truXMLStep, stack map[string]bool) []truXMLStep {
	if step.Disabled {
		return []truXMLStep{step}
	}
	name := strings.TrimSpace(step.ActionName)
	if stack == nil {
		stack = make(map[string]bool)
	}
	if name != "" {
		if stack[name] {
			return nil
		}
		stack[name] = true
		defer delete(stack, name)
	}

	flattened := make([]truXMLStep, 0)
	for _, child := range step.Steps {
		flattened = append(flattened, flattenExecutableSteps(child, definitions, stack)...)
	}
	return flattened
}

func flattenExecutableSteps(step truXMLStep, definitions map[string]truXMLStep, stack map[string]bool) []truXMLStep {
	if step.Disabled {
		return []truXMLStep{step}
	}
	if strings.EqualFold(strings.TrimSpace(step.Type), "alternative") {
		return []truXMLStep{step}
	}
	if strings.EqualFold(strings.TrimSpace(step.Action), "Call Action") {
		actionName, err := parseArgumentValue(step.Arguments, "Action Name")
		if err == nil {
			actionName = trimQuotedValue(actionName)
			if block, ok := definitions[actionName]; ok {
				return flattenActionBlock(block, definitions, stack)
			}
		}
		return nil
	}

	flattened := make([]truXMLStep, 0)
	action := strings.TrimSpace(step.Action)
	if action != "" && !isStructuralAction(action) {
		if isControlAction(action) {
			copy := step
			copy.Steps = flattenExecutableChildren(step.Steps, definitions, stack)
			return []truXMLStep{copy}
		}
		copy := step
		copy.Steps = nil // Children are expanded below; inspection must not count them twice.
		flattened = append(flattened, copy)
	}
	for _, child := range step.Steps {
		flattened = append(flattened, flattenExecutableSteps(child, definitions, stack)...)
	}
	return flattened
}

func flattenExecutableChildren(steps []truXMLStep, definitions map[string]truXMLStep, stack map[string]bool) []truXMLStep {
	flattened := make([]truXMLStep, 0)
	for _, child := range steps {
		flattened = append(flattened, flattenExecutableSteps(child, definitions, stack)...)
	}
	return flattened
}

type truConvertState struct {
	opts                 ConvertOptions
	descriptors          []ObjectDescriptor
	baseURL              string
	currentTransaction   string
	currentWindowContext string
}

type truControlKind string

const (
	truControlNone     truControlKind = ""
	truControlIf       truControlKind = "if"
	truControlElse     truControlKind = "else"
	truControlElseIf   truControlKind = "else_if"
	truControlLoop     truControlKind = "loop"
	truControlBreak    truControlKind = "break"
	truControlContinue truControlKind = "continue"
)

var (
	truParamComparisonRe = regexp.MustCompile(`^\s*\{([A-Za-z_][A-Za-z0-9_.-]*)\}\s*(==|!=|>=|<=|>|<|=|eq|ne|gt|lt|gte|lte|contains|not_contains)\s*(.+?)\s*$`)
	truLRGetParamRe      = regexp.MustCompile(`(?i)\bLR\.getParam(?:eter)?\(\s*["']([A-Za-z_][A-Za-z0-9_.-]*)["']\s*\)`)
)

func convertStepSequence(steps []truXMLStep, result *ConvertResult, state *truConvertState) ([]dsl.Action, error) {
	actions := make([]dsl.Action, 0, len(steps))
	for i := 0; i < len(steps); i++ {
		step := steps[i]
		if step.Disabled {
			count := countDisabledExecutableSteps(step)
			result.Stats.TotalSteps += count
			result.Stats.Skipped += count
			continue
		}
		actionName := strings.TrimSpace(step.Action)
		if actionName == "" {
			continue
		}
		result.Stats.TotalSteps++
		if strings.EqualFold(strings.TrimSpace(step.Type), "alternative") {
			count := countDisabledExecutableSteps(step)
			marker := reviewOnlyStepAction(step, StepScope{Status: ScopeReviewOnly, DisplayName: "Review source alternatives", CanonicalName: "alternative"})
			attachSourceStepMetadata(&marker, step, state.currentTransaction, state.descriptors)
			marker.Metadata["source_step_graph"] = step
			applyTruClientSemanticGate(&marker, "truclient_alternative_selection_requires_review", "The source alternative-selection graph and active-step contract require an explicit native replacement.")
			actions = append(actions, marker)
			result.Stats.TotalSteps += count - 1
			result.Stats.ReviewOnlySteps += count
			continue
		}
		switch kind := controlKind(actionName); kind {
		case truControlIf:
			thenSteps, childElseSteps := splitControlElseChildren(step.Steps)
			elseSteps := childElseSteps
			consumeNextElse := false
			if len(elseSteps) == 0 && i+1 < len(steps) {
				nextKind := controlKind(steps[i+1].Action)
				if !steps[i+1].Disabled && (nextKind == truControlElse || nextKind == truControlElseIf) {
					elseStep := steps[i+1]
					if nextKind == truControlElseIf {
						elseStep.Action = "If"
						elseSteps = []truXMLStep{elseStep}
					} else {
						elseSteps = append([]truXMLStep(nil), elseStep.Steps...)
					}
					consumeNextElse = true
				}
			}
			action, err := convertControlIfStep(step, thenSteps, elseSteps, result, state)
			if err != nil {
				return nil, err
			}
			if consumeNextElse {
				applyTruClientStepContract(&action, steps[i+1])
				action.Metadata["source_else_step"] = steps[i+1]
			}
			for _, child := range step.Steps {
				kind := controlKind(child.Action)
				if !child.Disabled && (kind == truControlElse || kind == truControlElseIf) {
					applyTruClientStepContract(&action, child)
					action.Metadata["source_else_step"] = child
				}
			}
			actions = append(actions, action)
			result.Stats.ConvertedActions++
			if consumeNextElse {
				i++
			}
		case truControlElse, truControlElseIf:
			action, err := convertDanglingElseStep(step, result, state)
			if err != nil {
				return nil, err
			}
			actions = append(actions, action)
			result.Stats.ConvertedActions++
		case truControlLoop:
			action, err := convertControlLoopStep(step, result, state)
			if err != nil {
				return nil, err
			}
			actions = append(actions, action)
			result.Stats.ConvertedActions++
		case truControlBreak, truControlContinue:
			actions = append(actions, controlTransferAction(step, kind))
			result.Stats.ConvertedActions++
		default:
			converted, err := convertExecutableStep(step, result, state, len(actions) > 0, &actions)
			if err != nil {
				return nil, err
			}
			if converted != nil {
				actions = append(actions, converted...)
			}
			if len(actions) > 0 && i+1 < len(steps) && canCoalesceTruClientDownload(&actions[len(actions)-1], steps[i+1], state) {
				if err := coalesceTruClientDownloadStep(&actions[len(actions)-1], steps[i+1], result, state); err != nil {
					return nil, err
				}
				i++
			}
		}
	}
	if state.currentWindowContext != "" {
		actions = append(actions, switchWindowAction("", "Switch back to primary window"))
		state.currentWindowContext = ""
	}
	return actions, nil
}

func canCoalesceTruClientDownload(clickAction *dsl.Action, nextStep truXMLStep, state *truConvertState) bool {
	if clickAction == nil || nextStep.Disabled || strings.EqualFold(strings.TrimSpace(nextStep.Type), "alternative") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(nextStep.Action), "Wait for File Download") {
		return false
	}
	if clickAction.Type != "browser_click" || reviewRequired(clickAction.Metadata) {
		return false
	}
	if strings.TrimSpace(clickAction.Selector) == "" && strings.TrimSpace(clickAction.Target) == "" {
		return false
	}
	if strings.TrimSpace(nextStep.TestObject) != "" {
		return false
	}
	if sel, err := parseFirstArgumentValue(nextStep.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err != nil || trimQuotedValue(sel) != "" {
		return false
	}
	if nextEvent := strings.TrimSpace(nextStep.EndEvent); clickAction.Sync != nil && nextEvent != "" && !strings.EqualFold(nextEvent, "Action completed") {
		return false
	}
	if state != nil {
		if _, err := parseArgumentValues(nextStep.Arguments); err != nil && !state.opts.PreserveUnsupportedSteps {
			return false
		}
	}
	return true
}

func coalesceTruClientDownloadStep(clickAction *dsl.Action, downloadStep truXMLStep, result *ConvertResult, state *truConvertState) error {
	result.Stats.TotalSteps++
	clickAction.Type = "browser_download"
	if clickAction.Metadata == nil {
		clickAction.Metadata = map[string]any{}
	}
	clickAction.Metadata["source_download_step"] = downloadStep
	if id := strings.TrimSpace(downloadStep.StepID); id != "" {
		clickAction.Metadata["source_download_step_id"] = id
	}
	if err := applyTruClientDownloadArguments(clickAction, downloadStep); err != nil {
		if !state.opts.PreserveUnsupportedSteps {
			return err
		}
		applyTruClientSemanticGate(clickAction, "truclient_step_arguments_require_review", "The recorded browser download step arguments require an explicit replacement.")
		result.Warnings = append(result.Warnings, "TruClient step arguments were preserved for review instead of guessing executable behavior.")
	}
	applyTruClientStepContract(clickAction, downloadStep)
	if clickAction.Sync == nil {
		applyTruClientCompletionContract(clickAction, downloadStep, state.descriptors)
	}
	return nil
}

func convertExecutableStep(step truXMLStep, result *ConvertResult, state *truConvertState, hasPriorAction bool, sequence *[]dsl.Action) ([]dsl.Action, error) {
	if started, transactionName, err := parseTransactionBoundary(step); err != nil {
		return nil, err
	} else if transactionName != "" {
		if started {
			state.currentTransaction = transactionName
			result.Stats.Transactions++
		} else {
			state.currentTransaction = ""
		}
		return nil, nil
	}
	if strings.EqualFold(strings.TrimSpace(step.Action), "Catch Error") {
		// No control_try exists in the DSL; preserve a review marker AND convert
		// the protected child steps in source order so they are not lost.
		return convertCatchErrorStep(step, result, state)
	}
	if strings.EqualFold(strings.TrimSpace(step.Action), "Verify") {
		assertion, ok, err := convertVerifyStep(step)
		if err != nil && !state.opts.PreserveUnsupportedSteps {
			return nil, err
		}
		if state.opts.PreserveUnsupportedSteps {
			marker := reviewOnlyStepAction(step, StepScope{Status: ScopeReviewOnly, DisplayName: "Review verification", CanonicalName: "verify"})
			attachSourceStepMetadata(&marker, step, state.currentTransaction, state.descriptors)
			// Retain exact assertion evidence on a marker. Source Verify is a
			// separate step; attaching it to a predecessor cannot prove its timing.
			if err == nil && ok {
				marker.Metadata["source_assertion"] = assertion
			}
			applyTruClientSemanticGate(&marker, "truclient_verification_step_requires_review", "Source verification timing, assertion target, and optional behavior require an explicit native verification step.")
			result.Stats.ReviewOnlySteps++
			return []dsl.Action{marker}, nil
		}
		if !ok {
			result.Warnings = append(result.Warnings, "TruClient step \"Verify\" could not be mapped to an assertion and was skipped")
			result.Stats.UnsupportedSteps = appendIfMissing(result.Stats.UnsupportedSteps, "Verify")
			return nil, nil
		}
		if sequence != nil && len(*sequence) > 0 {
			last := len(*sequence) - 1
			(*sequence)[last].Assertions = append((*sequence)[last].Assertions, assertion)
		} else if hasPriorAction {
			result.Warnings = append(result.Warnings, "TruClient Verify step could not be attached inside its control block because no preceding browser action was imported")
		} else {
			result.Warnings = append(result.Warnings, "TruClient Verify step could not be attached because no preceding browser action was imported")
		}
		return nil, nil
	}

	actions, warnings, unsupported, err := convertStep(step, state.descriptors)
	if err != nil {
		if !state.opts.PreserveUnsupportedSteps {
			return nil, err
		}
		actions = []dsl.Action{unresolvedBrowserStepAction(step, step.Action, "invalid_step_arguments")}
		applyTruClientSemanticGate(&actions[0], "truclient_step_arguments_require_review", "The recorded browser step arguments require an explicit replacement.")
		warnings = []string{"TruClient step arguments were preserved for review instead of guessing executable behavior."}
	}
	if len(warnings) > 0 {
		result.Warnings = append(result.Warnings, warnings...)
		if !unsupported && strings.EqualFold(strings.TrimSpace(step.Action), "Evaluate JavaScript") {
			result.Stats.Skipped++
			result.Stats.UnsupportedSteps = appendIfMissing(result.Stats.UnsupportedSteps, "Evaluate JavaScript")
		}
	}
	if unsupported {
		if actionName := strings.TrimSpace(step.Action); actionName != "" {
			result.Stats.UnsupportedSteps = appendIfMissing(result.Stats.UnsupportedSteps, actionName)
		}
		if !state.opts.PreserveUnsupportedSteps {
			result.Stats.Skipped++
			return nil, nil
		}
		marker := reviewOnlyStepAction(step, StepScope{CanonicalName: stableActionToken(step.Action), Status: ScopeReviewOnly})
		applyTruClientSemanticGate(&marker, "truclient_unsupported_step_requires_review", "The source step has no verified native browser equivalent.")
		actions = []dsl.Action{marker}
	}
	if len(actions) == 0 {
		return nil, nil
	}

	out := make([]dsl.Action, 0, len(actions)+1)
	for _, action := range actions {
		if state.opts.PreserveTransactions && state.currentTransaction != "" {
			action.Name = sanitizeTransactionActionName(state.currentTransaction, action.Name)
		}
		attachSourceStepMetadata(&action, step, state.currentTransaction, state.descriptors)
		applyTruClientCompletionContract(&action, step, state.descriptors)
		promoteFrameContext(&action)
		if switchAction, ok := switchWindowForAction(&action, state); ok {
			out = append(out, switchAction)
			result.Stats.ConvertedActions++
		}
		if state.baseURL == "" && action.Type == "browser_navigate" {
			state.baseURL = detectBaseURL(action.Target)
		}
		out = append(out, action)
		if action.Type == "source_review" {
			// Preserved review-only toolbox step: counted distinctly so it is
			// neither overstated as a converted action nor lost in Skipped.
			result.Stats.ReviewOnlySteps++
			if name := strings.TrimSpace(step.Action); name != "" {
				result.Stats.ReviewOnlyStepNames = appendIfMissing(result.Stats.ReviewOnlyStepNames, name)
			}
		} else {
			result.Stats.ConvertedActions++
		}
	}
	return out, nil
}

// convertCatchErrorStep preserves a TruClient "Catch Error" step as a review
// marker (no control_try in the DSL) while still converting the protected child
// steps in source order, so the guarded body is not silently dropped.
func convertCatchErrorStep(step truXMLStep, result *ConvertResult, state *truConvertState) ([]dsl.Action, error) {
	scope, _ := LookupStepScope("Catch Error")
	marker := reviewOnlyStepAction(step, scope)
	attachSourceStepMetadata(&marker, step, state.currentTransaction, state.descriptors)
	result.Stats.ReviewOnlySteps++
	result.Stats.ReviewOnlyStepNames = appendIfMissing(result.Stats.ReviewOnlyStepNames, "Catch Error")

	childState := *state
	childActions, err := convertStepSequence(step.Steps, result, &childState)
	if err != nil {
		return nil, err
	}
	state.baseURL = firstNonEmptyString(state.baseURL, childState.baseURL)

	out := make([]dsl.Action, 0, len(childActions)+1)
	out = append(out, marker)
	out = append(out, childActions...)
	return out, nil
}
