package trueclient

import (
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/selector"
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

func (argument *truArgument) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	argument.Value = truArgumentValueString(raw["value"])
	return nil
}

func truArgumentValueString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		if encoded, err := json.Marshal(typed); err == nil {
			return string(encoded)
		}
		return fmt.Sprint(typed)
	}
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

func findDefaultXML(files map[string]string) (string, string, bool) {
	for name, content := range files {
		if strings.EqualFold(filepath.Base(name), "default.xml") {
			return name, content, true
		}
	}
	return "", "", false
}

func decodeXMLContent(content string) string {
	data := []byte(content)
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		data = data[2:]
	}
	if !strings.ContainsRune(content, '\x00') {
		return content
	}
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	words := make([]uint16, 0, len(data)/2)
	for index := 0; index+1 < len(data); index += 2 {
		words = append(words, binary.LittleEndian.Uint16(data[index:index+2]))
	}
	return string(utf16.Decode(words))
}

func normalizeXMLDeclaration(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "<?xml") {
		return content
	}
	content = strings.Replace(content, `encoding="utf-16"`, `encoding="utf-8"`, 1)
	content = strings.Replace(content, `encoding="UTF-16"`, `encoding="utf-8"`, 1)
	content = strings.Replace(content, `encoding='utf-16'`, `encoding='utf-8'`, 1)
	content = strings.Replace(content, `encoding='UTF-16'`, `encoding='utf-8'`, 1)
	return content
}

func flattenCandidateSteps(steps []truXMLStep) []truXMLStep {
	flattened := make([]truXMLStep, 0)
	var visit func(step truXMLStep)
	visit = func(step truXMLStep) {
		action := strings.TrimSpace(step.Action)
		if action != "" && !isStructuralAction(action) {
			if isControlAction(action) {
				copy := step
				copy.Steps = flattenCandidateSteps(step.Steps)
				flattened = append(flattened, copy)
				return
			}
			flattened = append(flattened, step)
		}
		for _, child := range step.Steps {
			visit(child)
		}
	}
	for _, step := range steps {
		visit(step)
	}
	return flattened
}

func isStructuralAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", "action", "topstep", "init block", "run block", "end block", "handlers", "block", "default", "call action":
		return true
	default:
		return false
	}
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
		switch controlKind(actionName) {
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
			actions = append(actions, reviewGatedControlTransferAction(step))
			result.Warnings = append(result.Warnings, fmt.Sprintf("TruClient %s step %q requires manual review because APEX Load control blocks do not support break/continue.", actionName, stepDisplayName(step, actionName)))
			result.Stats.Skipped++
			result.Stats.UnsupportedSteps = appendIfMissing(result.Stats.UnsupportedSteps, actionName)
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
		result.Stats.ConvertedActions++
	}
	return out, nil
}

func convertControlIfStep(step truXMLStep, thenSteps []truXMLStep, elseSteps []truXMLStep, result *ConvertResult, state *truConvertState) (dsl.Action, error) {
	thenState := *state
	thenActions, err := convertStepSequence(thenSteps, result, &thenState)
	if err != nil {
		return dsl.Action{}, err
	}
	state.baseURL = firstNonEmptyString(state.baseURL, thenState.baseURL)
	condition, recognized, source := truConditionFromStep(step)
	metadata := controlMetadata(step, "truclient_control_if", source)
	if !recognized {
		metadata["review_required"] = true
		metadata["promotion_blocker"] = true
		metadata["review_reason"] = "TruClient control condition could not be translated deterministically; branch contents are preserved in source order and require review."
		result.Warnings = append(result.Warnings, fmt.Sprintf("TruClient If step %q condition %q could not be translated; preserved as a review-gated always-true control block.", stepDisplayName(step, "If"), firstNonEmptyString(source, step.Arguments)))
		if len(elseSteps) > 0 {
			fallbackState := *state
			elseActions, err := convertStepSequence(elseSteps, result, &fallbackState)
			if err != nil {
				return dsl.Action{}, err
			}
			thenActions = append(thenActions, elseActions...)
			state.baseURL = firstNonEmptyString(state.baseURL, fallbackState.baseURL)
			elseSteps = nil
		}
		condition = nil
	}
	wrapper := dsl.Action{
		Name:     stepDisplayName(step, "If"),
		Type:     "control_if",
		Metadata: metadata,
		Control: &dsl.ControlFlow{
			Condition: condition,
			Steps:     thenActions,
		},
	}
	if recognized && len(elseSteps) > 0 {
		elseState := *state
		elseActions, err := convertStepSequence(elseSteps, result, &elseState)
		if err != nil {
			return dsl.Action{}, err
		}
		wrapper.Control.Else = elseActions
		state.baseURL = firstNonEmptyString(state.baseURL, elseState.baseURL)
	}
	if len(wrapper.Control.Steps) == 0 && len(wrapper.Control.Else) == 0 {
		wrapper.Control.Steps = []dsl.Action{reviewGatedEmptyControlAction(step)}
	}
	return wrapper, nil
}

func convertDanglingElseStep(step truXMLStep, result *ConvertResult, state *truConvertState) (dsl.Action, error) {
	children := step.Steps
	if controlKind(step.Action) == truControlElseIf {
		step.Action = "If"
		return convertControlIfStep(step, children, nil, result, state)
	}
	childState := *state
	actions, err := convertStepSequence(children, result, &childState)
	if err != nil {
		return dsl.Action{}, err
	}
	state.baseURL = firstNonEmptyString(state.baseURL, childState.baseURL)
	metadata := controlMetadata(step, "truclient_dangling_else", "")
	metadata["review_required"] = true
	metadata["review_reason"] = "Else step was not adjacent to a recognized If step; preserved as a review-gated always-true block."
	result.Warnings = append(result.Warnings, fmt.Sprintf("TruClient Else step %q was not attached to a preceding If and requires review.", stepDisplayName(step, "Else")))
	return dsl.Action{
		Name:     stepDisplayName(step, "Else"),
		Type:     "control_if",
		Metadata: metadata,
		Control: &dsl.ControlFlow{
			Steps: actions,
		},
	}, nil
}

func convertControlLoopStep(step truXMLStep, result *ConvertResult, state *truConvertState) (dsl.Action, error) {
	childState := *state
	actions, err := convertStepSequence(step.Steps, result, &childState)
	if err != nil {
		return dsl.Action{}, err
	}
	state.baseURL = firstNonEmptyString(state.baseURL, childState.baseURL)
	count, countOK := truLoopCount(step)
	condition, condOK, source := truConditionFromStep(step)
	metadata := controlMetadata(step, "truclient_control_loop", source)
	control := &dsl.ControlFlow{Steps: actions}
	switch {
	case countOK:
		control.Count = count
		control.MaxIterations = count
	case condOK:
		control.Condition = condition
		control.MaxIterations = 1000
	default:
		control.Count = 1
		control.MaxIterations = 1
		metadata["review_required"] = true
		metadata["promotion_blocker"] = true
		metadata["review_reason"] = "TruClient loop bound or condition could not be translated deterministically; body runs once and requires review."
		result.Warnings = append(result.Warnings, fmt.Sprintf("TruClient loop step %q condition/count %q could not be translated; preserved as a review-gated one-pass control block.", stepDisplayName(step, "Loop"), firstNonEmptyString(source, step.Arguments)))
	}
	if len(control.Steps) == 0 {
		control.Steps = []dsl.Action{reviewGatedEmptyControlAction(step)}
	}
	return dsl.Action{
		Name:     stepDisplayName(step, "Loop"),
		Type:     "control_loop",
		Metadata: metadata,
		Control:  control,
	}, nil
}

func controlKind(action string) truControlKind {
	normalized := normalizeControlAction(action)
	switch normalized {
	case "if", "if block", "if exists", "if object exists", "if element exists":
		return truControlIf
	case "else":
		return truControlElse
	case "else if", "elseif":
		return truControlElseIf
	case "for", "loop", "repeat", "repeat loop", "for loop", "while", "do while":
		return truControlLoop
	case "break":
		return truControlBreak
	case "continue":
		return truControlContinue
	default:
		return truControlNone
	}
}

func isControlAction(action string) bool {
	return controlKind(action) != truControlNone
}

func normalizeControlAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	action = strings.ReplaceAll(action, "_", " ")
	action = strings.ReplaceAll(action, "-", " ")
	return strings.Join(strings.Fields(action), " ")
}

func splitControlElseChildren(children []truXMLStep) ([]truXMLStep, []truXMLStep) {
	thenSteps := make([]truXMLStep, 0, len(children))
	for index, child := range children {
		kind := controlKind(child.Action)
		if kind == truControlElse || kind == truControlElseIf {
			if kind == truControlElseIf {
				child.Action = "If"
				return thenSteps, []truXMLStep{child}
			}
			return thenSteps, append([]truXMLStep(nil), children[index].Steps...)
		}
		thenSteps = append(thenSteps, child)
	}
	return thenSteps, nil
}

func controlMetadata(step truXMLStep, source string, sourceCondition string) map[string]any {
	metadata := map[string]any{
		"source":        source,
		"source_action": strings.TrimSpace(step.Action),
	}
	if id := strings.TrimSpace(step.StepID); id != "" {
		metadata["source_step_id"] = id
	}
	if index := strings.TrimSpace(step.Index); index != "" {
		metadata["source_step_index"] = index
	}
	if sourceCondition != "" {
		metadata["source_condition"] = sourceCondition
	}
	return metadata
}

func truConditionFromStep(step truXMLStep) (*dsl.Condition, bool, string) {
	source := truControlExpression(step)
	if source == "" {
		switch controlKind(step.Action) {
		case truControlIf:
			return nil, true, ""
		default:
			return nil, false, ""
		}
	}
	if condition, ok := simpleTruParamCondition(source); ok {
		return condition, true, source
	}
	if expr, ok := restrictedTruJavaScriptCondition(source); ok {
		return &dsl.Condition{Expr: expr}, true, source
	}
	return nil, false, source
}

func truControlExpression(step truXMLStep) string {
	value, err := parseFirstArgumentValue(step.Arguments,
		"Condition", "condition", "Expression", "expression", "If Condition", "if_condition",
		"While Condition", "while_condition", "JavaScript", "javascript", "Code", "code",
		"Object Exists", "object_exists", "Value", "value")
	if err != nil {
		return ""
	}
	return trimQuotedValue(value)
}

func simpleTruParamCondition(source string) (*dsl.Condition, bool) {
	matches := truParamComparisonRe.FindStringSubmatch(strings.TrimSpace(source))
	if len(matches) != 4 {
		return nil, false
	}
	op := normalizeTruConditionOp(matches[2])
	if op == "" {
		return nil, false
	}
	return &dsl.Condition{
		Left:  "{{var." + matches[1] + "}}",
		Op:    op,
		Right: strings.Trim(strings.TrimSpace(matches[3]), `"'`),
	}, true
}

func normalizeTruConditionOp(op string) string {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "=", "==", "eq":
		return "eq"
	case "!=", "ne":
		return "ne"
	case ">", "gt":
		return "gt"
	case "<", "lt":
		return "lt"
	case ">=", "gte":
		return "gte"
	case "<=", "lte":
		return "lte"
	case "contains":
		return "contains"
	case "not_contains":
		return "not_contains"
	default:
		return ""
	}
}

func restrictedTruJavaScriptCondition(source string) (string, bool) {
	expr := strings.TrimSpace(source)
	if expr == "" {
		return "", false
	}
	expr = strings.TrimSuffix(expr, ";")
	expr = strings.ReplaceAll(expr, "===", "==")
	expr = strings.ReplaceAll(expr, "!==", "!=")
	expr = truLRGetParamRe.ReplaceAllString(expr, "var.$1")
	if strings.Contains(expr, "LR.") || strings.Contains(expr, "lr.") {
		return "", false
	}
	if strings.ContainsAny(expr, "{};") {
		return "", false
	}
	expr = strings.ReplaceAll(expr, "&&", "&&")
	expr = strings.ReplaceAll(expr, "||", "||")
	if dsl.ValidateConditionExpression(expr) != nil {
		return "", false
	}
	return expr, true
}

func truLoopCount(step truXMLStep) (int, bool) {
	value, err := parseFirstArgumentValue(step.Arguments, "Count", "count", "Iterations", "iterations", "Loop Count", "loop_count", "Times", "times")
	if err != nil {
		return 0, false
	}
	value = trimQuotedValue(value)
	if value == "" {
		return 0, false
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 0 {
		return 0, false
	}
	return count, true
}

func reviewGatedControlTransferAction(step truXMLStep) dsl.Action {
	actionName := strings.TrimSpace(step.Action)
	return dsl.Action{
		Name: stepDisplayName(step, actionName),
		Type: "source_review",
		Params: map[string]any{
			"function": actionName,
			"args":     []any{step.Arguments},
		},
		Metadata: controlMetadata(step, "truclient_control_transfer", ""),
	}
}

func reviewGatedEmptyControlAction(step truXMLStep) dsl.Action {
	return dsl.Action{
		Name: stepDisplayName(step, "Review control block"),
		Type: "source_review",
		Params: map[string]any{
			"function": strings.TrimSpace(step.Action),
			"args":     []any{step.Arguments},
		},
		Metadata: map[string]any{
			"source":          "truclient_empty_control_block",
			"review_required": true,
		},
	}
}

func promoteFrameContext(action *dsl.Action) {
	if action == nil || action.Metadata == nil {
		return
	}
	frame := firstNonEmptyString(stringFromAny(action.Metadata["source_frame_context"]), stringFromAny(action.Metadata["frame_context"]))
	selector := normalizeTruClientFrameSelector(frame)
	if selector == "" {
		return
	}
	if action.Params == nil {
		action.Params = map[string]any{}
	}
	action.Params["frame_selector"] = selector
	action.Metadata["frame_selector"] = selector
}

func switchWindowForAction(action *dsl.Action, state *truConvertState) (dsl.Action, bool) {
	if action == nil || state == nil || action.Metadata == nil {
		return dsl.Action{}, false
	}
	context := firstNonEmptyString(stringFromAny(action.Metadata["source_window_context"]), stringFromAny(action.Metadata["window_context"]))
	if context == state.currentWindowContext {
		return dsl.Action{}, false
	}
	state.currentWindowContext = context
	return switchWindowAction(context, "Switch window"), true
}

func switchWindowAction(context string, name string) dsl.Action {
	action := dsl.Action{
		Name: name,
		Type: "browser_switch_window",
		Metadata: map[string]any{
			"source": "truclient_window_context",
		},
	}
	context = strings.TrimSpace(context)
	if context == "" {
		action.Params = map[string]any{"index": 0}
		return action
	}
	if index, err := strconv.Atoi(context); err == nil {
		action.Params = map[string]any{"index": index}
		return action
	}
	action.Target = context
	action.Params = map[string]any{"target": context}
	return action
}

func normalizeTruClientFrameSelector(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "iframe") || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "[") || strings.HasPrefix(lower, "css=") || strings.HasPrefix(lower, "xpath=") || strings.HasPrefix(value, "//") {
		return value
	}
	escaped := strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
	return fmt.Sprintf(`iframe[name="%s"], iframe[id="%s"], iframe[title="%s"]`, escaped, escaped, escaped)
}

func convertStep(step truXMLStep, descriptors []ObjectDescriptor) ([]dsl.Action, []string, bool, error) {
	switch strings.TrimSpace(step.Action) {
	case "Navigate":
		location, err := parseArgumentValue(step.Arguments, "Location")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Navigate step arguments: %w", err)
		}
		location = trimQuotedValue(location)
		if location == "" {
			return nil, nil, false, fmt.Errorf("TruClient Navigate step is missing a Location value")
		}
		return []dsl.Action{{
			Name:   stepDisplayName(step, "Navigate"),
			Type:   "browser_navigate",
			Target: location,
		}}, nil, false, nil
	case "Click", "Double Click", "Double-click", "DoubleClick", "Submit", "Check", "Uncheck", "Select Radio", "Select Checkbox", "Radio", "Checkbox":
		action, err := convertClickLikeStep(step, descriptors)
		if err != nil {
			return nil, nil, false, err
		}
		var warnings []string
		if reviewRequired(action.Metadata) {
			warnings = append(warnings, reviewGatedClickWarning(step, action.Metadata))
		}
		return []dsl.Action{action}, warnings, false, nil
	case "Hover", "Mouse Over", "MouseOver":
		selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Hover step arguments: %w", err)
		}
		selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
		if selector == "" {
			return nil, nil, false, fmt.Errorf("TruClient Hover step is missing a selector value")
		}
		return []dsl.Action{{
			Name:     stepDisplayName(step, "Hover"),
			Type:     "browser_hover",
			Target:   selector,
			Selector: selector,
		}}, nil, false, nil
	case "Set / Type", "Set", "Type":
		if strings.EqualFold(strings.TrimSpace(step.Action), "Set") {
			if action, ok, err := convertSetFilesStep(step, descriptors); err != nil {
				return nil, nil, false, err
			} else if ok {
				return []dsl.Action{action}, nil, false, nil
			}
		}
		selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Set / Type step selector arguments: %w", err)
		}
		value, err := parseFirstArgumentValue(step.Arguments, "Value", "value", "Text", "text")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Set / Type step value arguments: %w", err)
		}
		selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
		value = trimQuotedValue(value)
		if selector == "" {
			return nil, nil, false, fmt.Errorf("TruClient Set / Type step is missing a selector value")
		}
		if value == "" {
			return nil, nil, false, fmt.Errorf("TruClient Set / Type step is missing a value")
		}
		return []dsl.Action{{
			Name:     stepDisplayName(step, "Set / Type"),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}}, nil, false, nil
	case "Select":
		selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Select step selector arguments: %w", err)
		}
		value, err := parseFirstArgumentValue(step.Arguments, "Value", "value", "Text", "text", "VisibleText", "visible_text", "Selected Value", "selected value", "SelectedValue", "selectedvalue", "Option", "option", "Selection", "selection")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Select step value arguments: %w", err)
		}
		selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
		value = trimQuotedValue(value)
		if selector == "" {
			action := unresolvedBrowserStepAction(step, "Select", "missing_selector")
			return []dsl.Action{action}, []string{reviewGatedStepWarning(step, action.Metadata)}, false, nil
		}
		if value == "" {
			action := unresolvedBrowserStepAction(step, "Select", "missing_select_value")
			action.Metadata["source_selector"] = selector
			return []dsl.Action{action}, []string{reviewGatedStepWarning(step, action.Metadata)}, false, nil
		}
		return []dsl.Action{{
			Name:     stepDisplayName(step, "Select"),
			Type:     "browser_select",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}}, nil, false, nil
	case "Press Key":
		action, err := convertPressKeyStep(step, descriptors)
		if err != nil {
			return nil, nil, false, err
		}
		return []dsl.Action{action}, nil, false, nil
	case "Scroll":
		action, err := convertScrollStep(step)
		if err != nil {
			return nil, nil, false, err
		}
		return []dsl.Action{action}, nil, false, nil
	case "Wait":
		waitAction, ok, err := convertWaitStep(step, descriptors)
		if err != nil {
			return nil, nil, false, err
		}
		if ok {
			return []dsl.Action{waitAction}, nil, false, nil
		}
		return nil, []string{fmt.Sprintf("TruClient step %q is not supported yet and was skipped during import", strings.TrimSpace(step.Action))}, true, nil
	case "Evaluate JavaScript":
		code, err := parseArgumentValue(step.Arguments, "Code")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Evaluate JavaScript arguments: %w", err)
		}
		actions, unsupportedLines := convertEvaluateJavaScript(code, stepDisplayName(step, "Evaluate JavaScript"))
		if len(actions) == 0 {
			warnings := evaluateJavaScriptUnsupportedWarnings(unsupportedLines)
			if len(warnings) == 0 {
				warnings = append(warnings, fmt.Sprintf("TruClient step %q is not supported yet and was skipped during import", strings.TrimSpace(step.Action)))
			}
			return nil, warnings, true, nil
		}
		warnings := evaluateJavaScriptUnsupportedWarnings(unsupportedLines)
		return actions, warnings, false, nil
	default:
		actionName := strings.TrimSpace(step.Action)
		if actionName == "" {
			return nil, nil, false, nil
		}
		return nil, []string{fmt.Sprintf("TruClient step %q is not supported yet and was skipped during import", actionName)}, true, nil
	}
}

func convertPressKeyStep(step truXMLStep, descriptors []ObjectDescriptor) (dsl.Action, error) {
	keyName, err := parseFirstArgumentValue(step.Arguments, "Key Name", "KeyName", "Key", "key")
	if err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Press Key arguments: %w", err)
	}
	keyName = trimQuotedValue(keyName)
	if keyName == "" {
		return dsl.Action{}, fmt.Errorf("TruClient Press Key step is missing a key name")
	}
	selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
	if err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Press Key selector arguments: %w", err)
	}
	selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
	action := dsl.Action{
		Name:  stepDisplayName(step, "Press Key"),
		Type:  "browser_press_key",
		Value: keyName,
		Metadata: map[string]any{
			"source":     "truclient_press_key",
			"source_key": keyName,
		},
	}
	if selector != "" {
		action.Target = selector
		action.Selector = selector
	}
	return action, nil
}

func convertSetFilesStep(step truXMLStep, descriptors []ObjectDescriptor) (dsl.Action, bool, error) {
	rawPath, err := parseFirstArgumentValue(step.Arguments, "Path", "path", "File", "file", "File Path", "file_path")
	if err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Set file path arguments: %w", err)
	}
	path := trimQuotedValue(rawPath)
	if path == "" {
		return dsl.Action{}, false, nil
	}
	selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
	if err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Set file selector arguments: %w", err)
	}
	selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
	if selector == "" {
		return dsl.Action{}, false, fmt.Errorf("TruClient Set file step is missing a selector value")
	}
	return dsl.Action{
		Name:     stepDisplayName(step, "Set File"),
		Type:     "browser_set_files",
		Target:   selector,
		Selector: selector,
		Value:    path,
		Metadata: map[string]any{
			"source":           "truclient_set_file",
			"source_file_path": path,
		},
	}, true, nil
}

func convertClickLikeStep(step truXMLStep, descriptors []ObjectDescriptor) (dsl.Action, error) {
	actionName := strings.TrimSpace(step.Action)
	selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
	if err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient %s step arguments: %w", actionName, err)
	}
	selector = resolveSelector(step, descriptors, trimQuotedValue(selector))
	if selector == "" {
		if x, y, ok := stepCoordinates(step); ok {
			return coordinateClickAction(step, actionName, x, y), nil
		}
		return unresolvedClickAction(step, actionName), nil
	}
	action := dsl.Action{
		Name:     stepDisplayName(step, firstNonEmptyString(actionName, "Click")),
		Type:     "browser_click",
		Target:   selector,
		Selector: selector,
	}
	params := map[string]any{}
	metadata := map[string]any{
		"source": "truclient_" + stableActionToken(actionName),
	}
	switch strings.ToLower(strings.ReplaceAll(actionName, "-", " ")) {
	case "double click", "doubleclick":
		params["click_count"] = 2
	case "check", "select checkbox", "checkbox", "select radio", "radio":
		params["checked"] = true
	case "uncheck":
		params["checked"] = false
	case "submit":
		metadata["submit"] = true
	}
	if len(params) > 0 {
		action.Params = params
	}
	action.Metadata = metadata
	return action, nil
}

func unresolvedClickAction(step truXMLStep, actionName string) dsl.Action {
	return unresolvedBrowserStepAction(step, firstNonEmptyString(actionName, "Click"), "missing_selector")
}

func unresolvedBrowserStepAction(step truXMLStep, fallback string, reason string) dsl.Action {
	actionName := firstNonEmptyString(strings.TrimSpace(step.Action), fallback)
	return dsl.Action{
		Name:   stepDisplayName(step, fallback),
		Type:   "browser_evaluate_js",
		Target: "(() => null)()",
		Metadata: map[string]any{
			"source":          "truclient_" + stableActionToken(actionName),
			"review_required": true,
			"review_reason":   reason,
		},
	}
}

func reviewGatedClickWarning(step truXMLStep, metadata map[string]any) string {
	actionName := firstNonEmptyString(strings.TrimSpace(step.Action), "Click")
	displayName := stepDisplayName(step, "Click")
	switch stringFromAny(metadata["review_reason"]) {
	case "selectorless_coordinate_click":
		return fmt.Sprintf("TruClient %s step %q was converted as a review-gated coordinate click because no selector was available", actionName, displayName)
	case "missing_selector":
		return fmt.Sprintf("TruClient %s step %q has no selector or coordinate fallback; a review-gated placeholder was preserved for manual repair", actionName, displayName)
	default:
		return fmt.Sprintf("TruClient %s step %q was converted with a review gate", actionName, displayName)
	}
}

func reviewGatedStepWarning(step truXMLStep, metadata map[string]any) string {
	actionName := firstNonEmptyString(strings.TrimSpace(step.Action), "step")
	displayName := stepDisplayName(step, actionName)
	switch stringFromAny(metadata["review_reason"]) {
	case "missing_selector":
		return fmt.Sprintf("TruClient %s step %q has no selector; a review-gated placeholder was preserved for manual repair", actionName, displayName)
	case "missing_select_value":
		return fmt.Sprintf("TruClient %s step %q has no selected value; a review-gated placeholder was preserved for manual repair", actionName, displayName)
	default:
		return fmt.Sprintf("TruClient %s step %q was converted with a review gate", actionName, displayName)
	}
}

func reviewRequired(metadata map[string]any) bool {
	value, _ := metadata["review_required"].(bool)
	return value
}

func stepCoordinates(step truXMLStep) (int, int, bool) {
	values, err := parseArgumentValues(step.Arguments)
	if err != nil {
		return 0, 0, false
	}
	x := parseSignedInt(trimArgumentValue(values, "X Coordinate", "X", "x", "ClientX", "client_x"))
	y := parseSignedInt(trimArgumentValue(values, "Y Coordinate", "Y", "y", "ClientY", "client_y"))
	if x == 0 && y == 0 {
		return 0, 0, false
	}
	return x, y, true
}

func coordinateClickAction(step truXMLStep, actionName string, x int, y int) dsl.Action {
	script := fmt.Sprintf(`(() => {
  const x = %d;
  const y = %d;
  const el = document.elementFromPoint(x, y);
  if (!el) {
    throw new Error("No element found at TruClient coordinate click " + x + "," + y);
  }
  el.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, clientX: x, clientY: y }));
  return el.tagName;
})()`, x, y)
	return dsl.Action{
		Name:   stepDisplayName(step, firstNonEmptyString(actionName, "Click")),
		Type:   "browser_evaluate_js",
		Target: script,
		Params: map[string]any{
			"x": x,
			"y": y,
		},
		Metadata: map[string]any{
			"source":                  "truclient_" + stableActionToken(actionName),
			"review_required":         true,
			"review_reason":           "selectorless_coordinate_click",
			"source_coordinate_click": true,
		},
	}
}

func convertScrollStep(step truXMLStep) (dsl.Action, error) {
	selector := "body"
	if rawSelector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step selector arguments: %w", err)
	} else if rawSelector = trimQuotedValue(rawSelector); rawSelector != "" {
		selector = rawSelector
	}

	direction := ""
	if rawDirection, err := parseFirstArgumentValue(step.Arguments, "Direction", "direction"); err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step direction arguments: %w", err)
	} else if rawDirection = strings.ToLower(strings.TrimSpace(trimQuotedValue(rawDirection))); rawDirection != "" {
		direction = normalizeScrollDirection(rawDirection)
	}

	distance := 0
	if rawDistance, err := parseFirstArgumentValue(step.Arguments, "Distance", "distance", "Amount", "amount", "Pixels", "pixels"); err != nil {
		return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step distance arguments: %w", err)
	} else {
		distance = parsePositiveInt(trimQuotedValue(rawDistance))
	}

	if direction == "" {
		x := 0
		y := 300
		if rawX, err := parseFirstArgumentValue(step.Arguments, "X", "x", "DeltaX", "delta_x", "Horizontal", "horizontal"); err != nil {
			return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step X arguments: %w", err)
		} else if parsed := parseSignedInt(trimQuotedValue(rawX)); parsed != 0 {
			x = parsed
		}
		if rawY, err := parseFirstArgumentValue(step.Arguments, "Y", "y", "DeltaY", "delta_y", "Vertical", "vertical"); err != nil {
			return dsl.Action{}, fmt.Errorf("parse TruClient Scroll step Y arguments: %w", err)
		} else if parsed := parseSignedInt(trimQuotedValue(rawY)); parsed != 0 {
			y = parsed
		}
		direction, distance = scrollGesture(float64(x), float64(y))
	}
	if direction == "" {
		direction = "up"
	}
	if distance <= 0 {
		distance = 300
	}

	return dsl.Action{
		Name:     stepDisplayName(step, "Scroll"),
		Type:     "browser_swipe",
		Target:   selector,
		Selector: selector,
		Params: map[string]any{
			"direction": direction,
			"distance":  distance,
		},
		Metadata: map[string]any{
			"source": "truclient_scroll",
		},
	}, nil
}

func attachSourceStepMetadata(action *dsl.Action, step truXMLStep, transaction string, descriptors []ObjectDescriptor) {
	if action == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	if _, ok := action.Metadata["source"]; !ok {
		action.Metadata["source"] = "truclient_" + stableActionToken(step.Action)
	}
	if value := strings.TrimSpace(step.StepID); value != "" {
		action.Metadata["source_step_id"] = value
	}
	if value := strings.TrimSpace(step.Index); value != "" {
		action.Metadata["source_step_index"] = value
	}
	if value := strings.TrimSpace(step.Action); value != "" {
		action.Metadata["source_action"] = value
	}
	if value := strings.TrimSpace(step.ActionName); value != "" {
		action.Metadata["source_action_name"] = value
	}
	if value := stepObjectDescriptorID(step); value != "" {
		action.Metadata["source_object_descriptor_id"] = value
	}
	if candidates, _ := rankedSelectorCandidates(step, descriptors); len(candidates) > 0 {
		action.Metadata["source_selector_candidates"] = candidates
		action.Metadata["source_selector_confidence"] = candidates[0].Confidence
		if len(candidates) > 1 {
			action.Metadata["source_selector_fallbacks"] = candidates[1:]
		}
	}
	if values, err := parseArgumentValues(step.Arguments); err == nil {
		if value := trimArgumentValue(values, "Frame", "Frame ID", "FrameContext", "Iframe", "Iframe ID"); value != "" {
			action.Metadata["source_frame_context"] = value
		}
		if value := trimArgumentValue(values, "Window", "Window ID", "WindowContext", "Tab", "Browser Window"); value != "" {
			action.Metadata["source_window_context"] = value
		}
		if refs := parameterReferences(values); len(refs) > 0 {
			action.Metadata["source_parameter_refs"] = refs
		}
		if refs := snapshotReferences(values); len(refs) > 0 {
			action.Metadata["source_snapshot_refs"] = refs
		}
	}
	if value := strings.TrimSpace(transaction); value != "" {
		action.Metadata["source_transaction"] = value
	}
}

// collectObjectDescriptors parses every object-repository file in the archive into
// ranked object descriptors so the converter can apply repository-quality selectors
// instead of relying on the inline locator captured in each step.
func collectObjectDescriptors(files map[string]string) []ObjectDescriptor {
	descriptors := make([]ObjectDescriptor, 0)
	for name, content := range files {
		if classifyArchiveFile(name) == "object_repository" {
			descriptors = append(descriptors, parseObjectRepositoryDescriptors(name, content)...)
		}
	}
	return uniqueObjectDescriptors(descriptors)
}

// resolveSelector returns the best selector for a step. When an object-repository
// descriptor matches the step, the highest-confidence css/xpath candidate (inline +
// descriptor, ranked by confidence) is used; otherwise the inline selector is returned
// unchanged so archives without object repositories behave exactly as before.
func resolveSelector(step truXMLStep, descriptors []ObjectDescriptor, inlineSelector string) string {
	candidates, matched := rankedSelectorCandidates(step, descriptors)
	if matched {
		if best := playwrightSelectorFromCandidates(candidates); best != "" {
			return best
		}
	}
	return inlineSelector
}

// rankedSelectorCandidates combines the inline locators captured on the step with the
// candidates derived from a matching object-repository descriptor, returning them ranked
// by confidence. The boolean reports whether an object descriptor matched the step.
func rankedSelectorCandidates(step truXMLStep, descriptors []ObjectDescriptor) ([]SelectorCandidate, bool) {
	values, _ := parseArgumentValues(step.Arguments)
	candidates := selectorCandidatesFromValues(values)
	matched := false
	if descriptor := matchObjectDescriptor(descriptors, stepObjectDescriptorIDFromValues(step, values), step); descriptor != nil {
		matched = true
		candidates = append(candidates, descriptor.SelectorCandidates...)
	}
	return uniqueSelectorCandidates(candidates), matched
}

// playwrightSelectorFromCandidates returns the highest-confidence candidate that is
// directly usable as a Playwright selector string (css or xpath). Role/text candidates
// are preserved in metadata for review but are not promoted automatically because they
// are not unambiguous selector strings.
func playwrightSelectorFromCandidates(candidates []SelectorCandidate) string {
	return selector.PlaywrightSelector(candidates)
}

// normalizeXPathSelector prefixes a bare XPath expression with the explicit Playwright
// "xpath=" engine prefix so it is never mistaken for a CSS selector.
func normalizeXPathSelector(value string) string {
	return selector.NormalizeXPath(value)
}

func normalizeScrollDirection(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "up", "north":
		return "up"
	case "down", "south":
		return "down"
	case "left", "west":
		return "left"
	case "right", "east":
		return "right"
	default:
		return ""
	}
}

func convertWaitStep(step truXMLStep, descriptors []ObjectDescriptor) (dsl.Action, bool, error) {
	timeoutMs := parseTimeoutMs(step.Arguments)
	if selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Wait step selector arguments: %w", err)
	} else if selector = resolveSelector(step, descriptors, trimQuotedValue(selector)); selector != "" {
		return dsl.Action{
			Name:     stepDisplayName(step, "Wait"),
			Type:     "browser_wait",
			Target:   selector,
			Selector: selector,
			WaitFor: &dsl.WaitCondition{
				Type:      "visible",
				Value:     selector,
				TimeoutMs: timeoutMs,
			},
		}, true, nil
	}
	if text, err := parseFirstArgumentValue(step.Arguments, "Text", "text", "Value", "value"); err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Wait step text arguments: %w", err)
	} else if text = trimQuotedValue(text); text != "" {
		return dsl.Action{
			Name: stepDisplayName(step, "Wait"),
			Type: "browser_wait",
			WaitFor: &dsl.WaitCondition{
				Type:      "text",
				Value:     text,
				TimeoutMs: timeoutMs,
			},
		}, true, nil
	}
	if delay, err := parseFirstArgumentValue(step.Arguments, "Duration", "duration", "Time", "time", "Timeout", "timeout"); err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Wait step duration arguments: %w", err)
	} else if delay = trimQuotedValue(delay); delay != "" {
		return dsl.Action{
			Name: stepDisplayName(step, "Wait"),
			Type: "browser_wait",
			WaitFor: &dsl.WaitCondition{
				Type:      "duration",
				Value:     delay,
				TimeoutMs: timeoutMs,
			},
		}, true, nil
	}
	return dsl.Action{}, false, nil
}

func convertVerifyStep(step truXMLStep) (dsl.Assertion, bool, error) {
	if text, err := parseFirstArgumentValue(step.Arguments, "Text", "text", "Value", "value"); err != nil {
		return dsl.Assertion{}, false, fmt.Errorf("parse TruClient Verify step text arguments: %w", err)
	} else if text = trimQuotedValue(text); text != "" {
		if selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err == nil && trimQuotedValue(selector) != "" {
			return dsl.Assertion{Type: "element_text_equals", Value: trimQuotedValue(selector), Expected: text}, true, nil
		}
		return dsl.Assertion{Type: "body_contains", Value: text}, true, nil
	}
	if title, err := parseFirstArgumentValue(step.Arguments, "Title", "title", "Page Title", "page_title"); err != nil {
		return dsl.Assertion{}, false, fmt.Errorf("parse TruClient Verify step title arguments: %w", err)
	} else if title = trimQuotedValue(title); title != "" {
		return dsl.Assertion{Type: "page_title", Value: title}, true, nil
	}
	if target, err := parseFirstArgumentValue(step.Arguments, "URL", "Url", "url", "Location", "location"); err != nil {
		return dsl.Assertion{}, false, fmt.Errorf("parse TruClient Verify step URL arguments: %w", err)
	} else if target = trimQuotedValue(target); target != "" {
		return dsl.Assertion{Type: "url_contains", Value: target}, true, nil
	}
	if selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err == nil && trimQuotedValue(selector) != "" {
		return dsl.Assertion{Type: "element_visible", Value: trimQuotedValue(selector)}, true, nil
	}
	return dsl.Assertion{}, false, nil
}

func parseTransactionBoundary(step truXMLStep) (bool, string, error) {
	switch strings.TrimSpace(step.Action) {
	case "Start Transaction":
		name, err := parseFirstArgumentValue(step.Arguments, "Transaction Name", "Name", "name", "Value", "value")
		if err != nil {
			return false, "", fmt.Errorf("parse TruClient Start Transaction step arguments: %w", err)
		}
		name = trimQuotedValue(name)
		if name == "" {
			name = stepDisplayName(step, "Start Transaction")
		}
		return true, name, nil
	case "End Transaction":
		name, err := parseFirstArgumentValue(step.Arguments, "Transaction Name", "Name", "name", "Value", "value")
		if err != nil {
			return false, "", fmt.Errorf("parse TruClient End Transaction step arguments: %w", err)
		}
		name = trimQuotedValue(name)
		if name == "" {
			name = stepDisplayName(step, "End Transaction")
		}
		return false, name, nil
	default:
		return false, "", nil
	}
}

func sanitizeTransactionActionName(transaction, name string) string {
	transaction = strings.TrimSpace(transaction)
	name = strings.TrimSpace(name)
	if transaction == "" {
		return name
	}
	sanitizedTransaction := strings.NewReplacer(" ", "_", "/", "_", "-", "_").Replace(transaction)
	if name == "" {
		return sanitizedTransaction
	}
	return sanitizedTransaction + "_" + name
}

func parseTimeoutMs(raw string) int {
	value, err := parseFirstArgumentValue(raw, "Timeout", "timeout", "TimeoutMs", "timeout_ms")
	if err != nil {
		return 0
	}
	value = trimQuotedValue(value)
	if value == "" {
		return 0
	}
	value = strings.TrimSuffix(strings.ToLower(value), "ms")
	var timeout int
	if _, err := fmt.Sscanf(value, "%d", &timeout); err != nil || timeout <= 0 {
		return 0
	}
	return timeout
}

func parsePositiveInt(value string) int {
	parsed := parseSignedInt(value)
	if parsed < 0 {
		return -parsed
	}
	return parsed
}

func parseSignedInt(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	value = strings.TrimSuffix(strings.ToLower(value), "px")
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func convertEvaluateJavaScript(code string, baseName string) ([]dsl.Action, []string) {
	statements := splitJavaScriptStatements(code)
	actions := make([]dsl.Action, 0, len(statements))
	unsupported := make([]string, 0)
	for i, statement := range statements {
		action, ok := convertJavaScriptStatement(statement, baseName, len(actions)+1)
		if !ok {
			actions = append(actions, dsl.Action{
				Name:   fmt.Sprintf("%s - Statement %d", baseName, i+1),
				Type:   "browser_evaluate_js",
				Target: statement,
				Metadata: map[string]any{
					"source":             "truclient_evaluate_javascript",
					"source_script":      statement,
					"review_required":    true,
					"review_gate":        "javascript",
					"review_reason":      "TruClient JavaScript could not be decomposed into native browser actions; replay and approval are required before promotion.",
					"promotion_blocker":  true,
					"compatibility_gate": "trueclient2_javascript_review",
				},
			})
			continue
		}
		actions = append(actions, action)
	}
	return actions, unsupported
}

func evaluateJavaScriptUnsupportedWarnings(unsupportedLines []string) []string {
	warnings := make([]string, 0, len(unsupportedLines))
	for _, line := range unsupportedLines {
		warnings = append(warnings, fmt.Sprintf("TruClient Evaluate JavaScript statement %q was skipped during import", line))
	}
	return warnings
}

func splitJavaScriptStatements(code string) []string {
	lines := strings.Split(code, "\n")
	parts := make([]string, 0)
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(stripLineComment(line))
		if trimmedLine == "" {
			continue
		}
		for _, statement := range strings.Split(trimmedLine, ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			parts = append(parts, statement)
		}
	}
	return parts
}

func stripLineComment(line string) string {
	var quote rune
	escaped := false
	for index, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '/' && index+1 < len(line) && line[index+1] == '/' {
			return line[:index]
		}
	}
	return line
}

func convertJavaScriptStatement(statement string, baseName string, position int) (dsl.Action, bool) {
	if matches := reJQueryVal.FindStringSubmatch(statement); len(matches) == 3 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}, true
	}
	if matches := reJQueryAttrValue.FindStringSubmatch(statement); len(matches) == 4 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		attribute, ok := decodeJSStringLiteral(matches[2])
		if !ok || !strings.EqualFold(strings.TrimSpace(attribute), "value") {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[3])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_jquery_attr",
			},
		}, true
	}
	if matches := reQuerySelectorValue.FindStringSubmatch(statement); len(matches) == 3 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}, true
	}
	if matches := reQuerySelectorAllValue.FindStringSubmatch(statement); len(matches) == 3 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok || strings.TrimSpace(selector) == "" {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector = strings.TrimSpace(selector)
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_query_selector_all",
			},
		}, true
	}
	if matches := reElementByIDValue.FindStringSubmatch(statement); len(matches) == 3 {
		id, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementIDSelector(id)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}, true
	}
	if matches := reElementsByNameValue.FindStringSubmatch(statement); len(matches) == 3 {
		name, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementNameSelector(name)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}, true
	}
	if matches := reElementsByClassValue.FindStringSubmatch(statement); len(matches) == 3 {
		className, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementClassSelector(className)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_class_element",
			},
		}, true
	}
	if matches := reElementsByTagValue.FindStringSubmatch(statement); len(matches) == 3 {
		tagName, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementTagSelector(tagName)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_tag_element",
			},
		}, true
	}
	if matches := reFormElementValue.FindStringSubmatch(statement); len(matches) == 4 {
		name, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		value, ok := decodeJSStringLiteral(matches[3])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementNameSelector(name)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Fill %d", baseName, position),
			Type:     "browser_fill",
			Target:   selector,
			Selector: selector,
			Value:    value,
			Metadata: map[string]any{
				"source": "truclient_form_element",
			},
		}, true
	}
	if matches := reJQueryClick.FindStringSubmatch(statement); len(matches) == 2 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
		}, true
	}
	if matches := reJQueryTriggerClick.FindStringSubmatch(statement); len(matches) == 3 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		eventName, ok := decodeJSStringLiteral(matches[2])
		if !ok || !strings.EqualFold(strings.TrimSpace(eventName), "click") {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_jquery_trigger",
			},
		}, true
	}
	if matches := reQuerySelectorClick.FindStringSubmatch(statement); len(matches) == 2 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
		}, true
	}
	if matches := reQuerySelectorAllClick.FindStringSubmatch(statement); len(matches) == 2 {
		selector, ok := decodeJSStringLiteral(matches[1])
		if !ok || strings.TrimSpace(selector) == "" {
			return dsl.Action{}, false
		}
		selector = strings.TrimSpace(selector)
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_query_selector_all",
			},
		}, true
	}
	if matches := reElementByIDClick.FindStringSubmatch(statement); len(matches) == 2 {
		id, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementIDSelector(id)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
		}, true
	}
	if matches := reElementsByNameClick.FindStringSubmatch(statement); len(matches) == 2 {
		name, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementNameSelector(name)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
		}, true
	}
	if matches := reElementsByClassClick.FindStringSubmatch(statement); len(matches) == 2 {
		className, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementClassSelector(className)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_class_element",
			},
		}, true
	}
	if matches := reElementsByTagClick.FindStringSubmatch(statement); len(matches) == 2 {
		tagName, ok := decodeJSStringLiteral(matches[1])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementTagSelector(tagName)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_tag_element",
			},
		}, true
	}
	if matches := reFormElementClick.FindStringSubmatch(statement); len(matches) == 3 {
		name, ok := decodeJSStringLiteral(matches[2])
		if !ok {
			return dsl.Action{}, false
		}
		selector := elementNameSelector(name)
		if selector == "" {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Click %d", baseName, position),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
			Metadata: map[string]any{
				"source": "truclient_form_element",
			},
		}, true
	}
	if matches := reWindowScroll.FindStringSubmatch(statement); len(matches) == 3 {
		x, xErr := strconv.ParseFloat(matches[1], 64)
		y, yErr := strconv.ParseFloat(matches[2], 64)
		if xErr != nil || yErr != nil {
			return dsl.Action{}, false
		}
		direction, distance := scrollGesture(x, y)
		if distance <= 0 {
			return dsl.Action{}, false
		}
		return dsl.Action{
			Name:     fmt.Sprintf("%s Scroll %d", baseName, position),
			Type:     "browser_swipe",
			Target:   "body",
			Selector: "body",
			Params: map[string]any{
				"direction": direction,
				"distance":  distance,
			},
			Metadata: map[string]any{
				"source":        "truclient_evaluate_javascript",
				"source_script": statement,
			},
		}, true
	}
	if action, ok := convertJavaScriptLocationNavigation(statement, baseName, position); ok {
		return action, true
	}
	return dsl.Action{}, false
}

func elementIDSelector(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return "#" + strings.ReplaceAll(id, `"`, `\"`)
}

func elementNameSelector(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return `[name="` + strings.ReplaceAll(name, `"`, `\"`) + `"]`
}

func elementClassSelector(className string) string {
	tokens := strings.Fields(strings.TrimSpace(className))
	if len(tokens) == 0 {
		return ""
	}
	for _, token := range tokens {
		if !reSimpleCSSClassToken.MatchString(token) {
			return ""
		}
	}
	return "." + strings.Join(tokens, ".")
}

func elementTagSelector(tagName string) string {
	tagName = strings.ToLower(strings.TrimSpace(tagName))
	if tagName == "" || !reSimpleHTMLTagToken.MatchString(tagName) {
		return ""
	}
	return tagName
}

func convertJavaScriptLocationNavigation(statement string, baseName string, position int) (dsl.Action, bool) {
	matches := reLocationAssign.FindStringSubmatch(statement)
	if len(matches) != 2 {
		matches = reLocationMethod.FindStringSubmatch(statement)
	}
	if len(matches) != 2 {
		return dsl.Action{}, false
	}
	target, ok := decodeJSStringLiteral(matches[1])
	if !ok || strings.TrimSpace(target) == "" {
		return dsl.Action{}, false
	}
	return dsl.Action{
		Name:   fmt.Sprintf("%s Navigate %d", baseName, position),
		Type:   "browser_navigate",
		Target: strings.TrimSpace(target),
		Metadata: map[string]any{
			"source":        "truclient_evaluate_javascript",
			"source_script": statement,
		},
	}, true
}

func scrollGesture(x, y float64) (string, int) {
	if absFloat(y) >= absFloat(x) {
		if y > 0 {
			return "up", int(absFloat(y))
		}
		if y < 0 {
			return "down", int(absFloat(y))
		}
		return "", 0
	}
	if x > 0 {
		return "left", int(absFloat(x))
	}
	if x < 0 {
		return "right", int(absFloat(x))
	}
	return "", 0
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func decodeJSStringLiteral(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 2 {
		return "", false
	}
	quote := trimmed[0]
	if (quote != '\'' && quote != '"') || trimmed[len(trimmed)-1] != quote {
		return "", false
	}
	inner := trimmed[1 : len(trimmed)-1]
	replacer := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\'`, `'`, `\n`, "\n", `\t`, "\t")
	return replacer.Replace(inner), true
}

func parseArgumentValue(raw string, key string) (string, error) {
	values, err := parseArgumentValues(raw)
	if err != nil {
		return "", err
	}
	return values[key].Value, nil
}

func parseFirstArgumentValue(raw string, keys ...string) (string, error) {
	values, err := parseArgumentValues(raw)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if value, ok := values[key]; ok && strings.TrimSpace(value.Value) != "" {
			return value.Value, nil
		}
		for existingKey, existingValue := range values {
			if strings.EqualFold(existingKey, key) && strings.TrimSpace(existingValue.Value) != "" {
				return existingValue.Value, nil
			}
		}
	}
	return "", nil
}

func parseArgumentValues(raw string) (map[string]truArgument, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]truArgument{}, nil
	}
	values := make(map[string]truArgument)
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func trimQuotedValue(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, "\"")
	return strings.TrimSpace(trimmed)
}

func stepDisplayName(step truXMLStep, fallback string) string {
	if name := strings.TrimSpace(step.ActionName); name != "" {
		return name
	}
	if action := strings.TrimSpace(step.Action); action != "" {
		return action
	}
	return fallback
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stableActionToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "action"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	token := strings.Trim(b.String(), "_")
	if token == "" {
		return "action"
	}
	return token
}

func detectBaseURL(target string) string {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func appendIfMissing(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
