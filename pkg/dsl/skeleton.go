package dsl

import "strings"

// SkeletonAction returns a minimal action populated from the catalog contract.
// Callers are expected to replace placeholder values with workflow-specific
// examples, but required fields and one-of groups come from the single source
// of truth used by validation, schema generation, and editor metadata.
func SkeletonAction(actionType string) Action {
	spec, ok := SpecForActionType(actionType)
	if !ok {
		trimmed := strings.TrimSpace(actionType)
		return Action{Name: trimmed, Type: trimmed}
	}
	action := Action{Name: spec.Type, Type: spec.Type}
	for _, field := range spec.Fields {
		if field.Required {
			setSkeletonActionField(&action, field, spec.Type)
		}
	}
	for _, group := range spec.RequireOneOf {
		for _, fieldName := range group {
			if skeletonActionFieldIsSet(action, fieldName) {
				break
			}
			if field, ok := specFieldByName(spec, fieldName); ok {
				setSkeletonActionField(&action, field, spec.Type)
				break
			}
		}
	}
	return action
}

func specFieldByName(spec ActionSpec, name string) (FieldSpec, bool) {
	for _, field := range spec.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return FieldSpec{Name: name, Kind: FieldKindString}, true
}

func setSkeletonActionField(action *Action, field FieldSpec, actionType string) {
	if action == nil {
		return
	}
	if strings.HasPrefix(field.Name, "params.") {
		if action.Params == nil {
			action.Params = map[string]any{}
		}
		key := strings.TrimPrefix(field.Name, "params.")
		action.Params[key] = skeletonValue(field, actionType)
		return
	}
	switch field.Name {
	case "target":
		action.Target = skeletonStringValue(field.Name, actionType)
	case "selector":
		action.Selector = skeletonStringValue(field.Name, actionType)
	case "value":
		action.Value = skeletonStringValue(field.Name, actionType)
	case "body":
		action.Body = skeletonStringValue(field.Name, actionType)
	case "body_file":
		action.BodyFile = skeletonStringValue(field.Name, actionType)
	case "headers":
		action.Headers = map[string]string{}
	case "control":
		action.Control = skeletonControlFlow(actionType)
	case "wait_for":
		action.WaitFor = &WaitCondition{Type: "selector", Value: "[data-testid='ready']", TimeoutMs: 5000}
	}
}

func skeletonActionFieldIsSet(action Action, name string) bool {
	if strings.HasPrefix(name, "params.") {
		if action.Params == nil {
			return false
		}
		_, ok := action.Params[strings.TrimPrefix(name, "params.")]
		return ok
	}
	switch name {
	case "target":
		return strings.TrimSpace(action.Target) != ""
	case "selector":
		return strings.TrimSpace(action.Selector) != ""
	case "value":
		return strings.TrimSpace(action.Value) != ""
	case "body":
		return strings.TrimSpace(action.Body) != ""
	case "body_file":
		return strings.TrimSpace(action.BodyFile) != ""
	case "headers":
		return action.Headers != nil
	case "control":
		return action.Control != nil
	case "wait_for":
		return action.WaitFor != nil
	default:
		return false
	}
}

func skeletonValue(field FieldSpec, actionType string) any {
	if field.Name == "params.requests" {
		return []any{map[string]any{"name": "Request", "method": "GET", "target": "/"}}
	}
	switch field.Kind {
	case FieldKindInt:
		return 1
	case FieldKindFloat:
		return 1.0
	case FieldKindBool:
		return true
	case FieldKindArray:
		return []any{}
	case FieldKindObject:
		return map[string]any{}
	case FieldKindEnum:
		if len(field.Enum) > 0 {
			return field.Enum[0]
		}
		return "value"
	default:
		return skeletonStringValue(field.Name, actionType)
	}
}

func skeletonStringValue(fieldName string, actionType string) string {
	switch fieldName {
	case "target":
		switch actionType {
		case "grpc_unary", "grpc_server_stream", "grpc_client_stream", "grpc_bidi_stream":
			return "package.Service/Method"
		case "ws_connect":
			return "/ws"
		default:
			return "/"
		}
	case "selector":
		return "[data-testid='element']"
	case "value":
		return "value"
	case "body":
		return "{}"
	case "body_file":
		return "payloads/body.txt"
	default:
		return "value"
	}
}

func skeletonControlFlow(actionType string) *ControlFlow {
	control := &ControlFlow{Steps: []Action{SkeletonAction("GET")}}
	if actionType == "control_loop" {
		control.Count = 1
	}
	return control
}
