package trueclient

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type ConvertOptions struct {
	ScriptName           string
	BaseURL              string
	PreserveTransactions bool
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
	Script   *dsl.Script
	Warnings []string
	Stats    ConvertStats
}

type truScriptDocument struct {
	XMLName     xml.Name           `xml:"truScript"`
	Steps       []truXMLStep       `xml:"step"`
	TestObjects []truXMLTestObject `xml:"testObjects>testObject"`
}

type truXMLStep struct {
	Type       string       `xml:"type,attr"`
	StepID     string       `xml:"stepId,attr"`
	Action     string       `xml:"action,attr"`
	ActionName string       `xml:"actionName,attr"`
	TestObject string       `xml:"testObject,attr"`
	Arguments  string       `xml:"arguments,attr"`
	Index      string       `xml:"index,attr"`
	Steps      []truXMLStep `xml:"step"`
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
	Value string `json:"value"`
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
	baseURL = state.baseURL

	if len(result.Script.Actions) == 0 {
		return nil, fmt.Errorf("no supported TruClient steps found in %s", filepath.Base(xmlName))
	}

	result.Script.Config.BaseURL = baseURL
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
		flattened = append(flattened, step)
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
		actionName := strings.TrimSpace(step.Action)
		if actionName == "" {
			continue
		}
		result.Stats.TotalSteps++
		switch kind := controlKind(actionName); kind {
		case truControlIf:
			thenSteps, childElseSteps := splitControlElseChildren(step.Steps)
			elseSteps := childElseSteps
			consumeNextElse := false
			if len(elseSteps) == 0 && i+1 < len(steps) {
				nextKind := controlKind(steps[i+1].Action)
				if nextKind == truControlElse || nextKind == truControlElseIf {
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
		}
	}
	if state.currentWindowContext != "" {
		actions = append(actions, switchWindowAction("", "Switch back to primary window"))
		state.currentWindowContext = ""
	}
	return actions, nil
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
		if err != nil {
			return nil, err
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
		return nil, err
	}
	if len(warnings) > 0 {
		result.Warnings = append(result.Warnings, warnings...)
		if !unsupported && strings.EqualFold(strings.TrimSpace(step.Action), "Evaluate JavaScript") {
			result.Stats.Skipped++
			result.Stats.UnsupportedSteps = appendIfMissing(result.Stats.UnsupportedSteps, "Evaluate JavaScript")
		}
	}
	if unsupported {
		result.Stats.Skipped++
		if actionName := strings.TrimSpace(step.Action); actionName != "" {
			result.Stats.UnsupportedSteps = appendIfMissing(result.Stats.UnsupportedSteps, actionName)
		}
		return nil, nil
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
