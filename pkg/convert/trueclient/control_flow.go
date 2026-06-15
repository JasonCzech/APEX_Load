package trueclient

// control_flow.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

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
