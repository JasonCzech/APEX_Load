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

	"github.com/jasonczech/stormrunner/pkg/dsl"
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
	XMLName xml.Name     `xml:"truScript"`
	Steps   []truXMLStep `xml:"step"`
}

type truXMLStep struct {
	Type       string       `xml:"type,attr"`
	StepID     string       `xml:"stepId,attr"`
	Action     string       `xml:"action,attr"`
	ActionName string       `xml:"actionName,attr"`
	Arguments  string       `xml:"arguments,attr"`
	Index      string       `xml:"index,attr"`
	Steps      []truXMLStep `xml:"step"`
}

type truArgument struct {
	Value string `json:"value"`
}

var (
	reJQueryVal           = regexp.MustCompile(`^\$\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.val\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)$`)
	reJQueryClick         = regexp.MustCompile(`^\$\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.click\(\s*\)$`)
	reQuerySelectorClick  = regexp.MustCompile(`^document\.querySelector\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.click\(\s*\)$`)
	reQuerySelectorValue  = regexp.MustCompile(`^document\.querySelector\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reElementByIDClick    = regexp.MustCompile(`^document\.getElementById\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.click\(\s*\)$`)
	reElementByIDValue    = regexp.MustCompile(`^document\.getElementById\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reElementsByNameClick = regexp.MustCompile(`^document\.getElementsByName\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\s*\[\s*0\s*\]\.click\(\s*\)$`)
	reElementsByNameValue = regexp.MustCompile(`^document\.getElementsByName\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)\s*\[\s*0\s*\]\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reFormElementClick    = regexp.MustCompile(`^document\.forms\s*\[\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*'|\d+)\s*\]\.elements\s*\[\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\]\.click\(\s*\)$`)
	reFormElementValue    = regexp.MustCompile(`^document\.forms\s*\[\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*'|\d+)\s*\]\.elements\s*\[\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\]\.value\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reWindowScroll        = regexp.MustCompile(`^window\.scroll(?:To|By)\(\s*(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)\s*\)$`)
	reLocationAssign      = regexp.MustCompile(`^(?:window\.)?location(?:\.href)?\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')$`)
	reLocationMethod      = regexp.MustCompile(`^(?:window\.)?location\.(?:assign|replace)\(\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*')\s*\)$`)
)

// ConvertArchive converts a TruClient Mobile Web archive into a Stormrunner browser script.
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
			GeneratedBy:   "stormrunner-truclient-converter",
			GeneratedFrom: "loadrunner",
			Config:        dsl.ScriptConfig{},
			Actions:       make([]dsl.Action, 0),
		},
		Warnings: make([]string, 0),
	}

	baseURL := strings.TrimSpace(opts.BaseURL)
	currentTransaction := ""
	for _, step := range executionSteps(doc.Steps) {
		result.Stats.TotalSteps++

		if started, transactionName, err := parseTransactionBoundary(step); err != nil {
			return nil, err
		} else if transactionName != "" {
			if started {
				currentTransaction = transactionName
				result.Stats.Transactions++
			} else {
				currentTransaction = ""
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(step.Action), "Verify") {
			assertion, ok, err := convertVerifyStep(step)
			if err != nil {
				return nil, err
			}
			if !ok {
				result.Warnings = append(result.Warnings, "TruClient step \"Verify\" is not supported yet and was skipped during import")
				result.Stats.Skipped++
				result.Stats.UnsupportedSteps = appendIfMissing(result.Stats.UnsupportedSteps, "Verify")
				continue
			}
			if len(result.Script.Actions) == 0 {
				result.Warnings = append(result.Warnings, "TruClient Verify step could not be attached because no preceding browser action was imported")
				result.Stats.Skipped++
				continue
			}
			last := &result.Script.Actions[len(result.Script.Actions)-1]
			last.Assertions = append(last.Assertions, assertion)
			continue
		}

		actions, warnings, unsupported, err := convertStep(step)
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
			continue
		}
		if len(actions) == 0 {
			continue
		}

		for _, action := range actions {
			if opts.PreserveTransactions && currentTransaction != "" {
				action.Name = sanitizeTransactionActionName(currentTransaction, action.Name)
			}
			if baseURL == "" && action.Type == "browser_navigate" {
				baseURL = detectBaseURL(action.Target)
			}
			result.Script.Actions = append(result.Script.Actions, action)
			result.Stats.ConvertedActions++
		}
	}

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
		flattened = append(flattened, step)
	}
	for _, child := range step.Steps {
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

func convertStep(step truXMLStep) ([]dsl.Action, []string, bool, error) {
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
	case "Click":
		selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Click step arguments: %w", err)
		}
		selector = trimQuotedValue(selector)
		if selector == "" {
			return nil, nil, false, fmt.Errorf("TruClient Click step is missing a selector value")
		}
		return []dsl.Action{{
			Name:     stepDisplayName(step, "Click"),
			Type:     "browser_click",
			Target:   selector,
			Selector: selector,
		}}, nil, false, nil
	case "Set / Type", "Set", "Type":
		selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Set / Type step selector arguments: %w", err)
		}
		value, err := parseFirstArgumentValue(step.Arguments, "Value", "value", "Text", "text")
		if err != nil {
			return nil, nil, false, fmt.Errorf("parse TruClient Set / Type step value arguments: %w", err)
		}
		selector = trimQuotedValue(selector)
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
		selector = trimQuotedValue(selector)
		value = trimQuotedValue(value)
		if selector == "" {
			return nil, nil, false, fmt.Errorf("TruClient Select step is missing a selector value")
		}
		if value == "" {
			return nil, nil, false, fmt.Errorf("TruClient Select step is missing a value")
		}
		return []dsl.Action{{
			Name:     stepDisplayName(step, "Select"),
			Type:     "browser_select",
			Target:   selector,
			Selector: selector,
			Value:    value,
		}}, nil, false, nil
	case "Scroll":
		action, err := convertScrollStep(step)
		if err != nil {
			return nil, nil, false, err
		}
		return []dsl.Action{action}, nil, false, nil
	case "Wait":
		waitAction, ok, err := convertWaitStep(step)
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
			return nil, []string{fmt.Sprintf("TruClient step %q is not supported yet and was skipped during import", strings.TrimSpace(step.Action))}, true, nil
		}
		warnings := make([]string, 0, len(unsupportedLines))
		for _, line := range unsupportedLines {
			warnings = append(warnings, fmt.Sprintf("TruClient Evaluate JavaScript statement %q was skipped during import", line))
		}
		return actions, warnings, false, nil
	default:
		actionName := strings.TrimSpace(step.Action)
		if actionName == "" {
			return nil, nil, false, nil
		}
		return nil, []string{fmt.Sprintf("TruClient step %q is not supported yet and was skipped during import", actionName)}, true, nil
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

func convertWaitStep(step truXMLStep) (dsl.Action, bool, error) {
	timeoutMs := parseTimeoutMs(step.Arguments)
	if selector, err := parseFirstArgumentValue(step.Arguments, "Selector", "selector", "Target", "target", "Element", "element"); err != nil {
		return dsl.Action{}, false, fmt.Errorf("parse TruClient Wait step selector arguments: %w", err)
	} else if selector = trimQuotedValue(selector); selector != "" {
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
	for _, statement := range statements {
		action, ok := convertJavaScriptStatement(statement, baseName, len(actions)+1)
		if !ok {
			unsupported = append(unsupported, statement)
			continue
		}
		actions = append(actions, action)
	}
	return actions, unsupported
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
