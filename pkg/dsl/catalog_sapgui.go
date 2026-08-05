package dsl

import (
	"fmt"
	"strings"
)

func validateSAPGUIActionIssues(index int, action Action) []string {
	spec, ok := protocolActionSpec("sap_gui", action.Type)
	if !ok {
		return []string{fmt.Sprintf("actions[%d] %q: unsupported SAP GUI action type %q", index, action.Name, action.Type)}
	}
	if spec.Validate != nil {
		return spec.Validate(index, action)
	}
	return nil
}

func sapGUIActionSpecs() []ActionSpec {
	controlKinds := []string{"text", "combo", "checkbox", "radio", "button", "grid", "table", "tree", "menu", "shell", "unknown"}
	specs := []ActionSpec{
		{
			Type:      "sapgui_connect",
			Protocols: []string{"sap_gui"},
			Summary:   "Connects to a SAP GUI application server or connection entry.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Connection string or SAP GUI connection entry."},
				{Name: "params.system", Kind: FieldKindString, Doc: "Optional SAP system identifier."},
			},
		},
		{
			Type:      "sapgui_logon",
			Protocols: []string{"sap_gui"},
			Summary:   "Logs on to a SAP GUI session.",
			Fields: []FieldSpec{
				{Name: "params.client", Kind: FieldKindString, Doc: "SAP client number."},
				{Name: "params.user", Kind: FieldKindString, Doc: "Recorded username or template."},
				{Name: "params.password", Kind: FieldKindString, Doc: "Password secret template or retained imported value."},
				{Name: "params.language", Kind: FieldKindString, Doc: "SAP logon language."},
			},
		},
		{
			Type:      "sapgui_set",
			Protocols: []string{"sap_gui"},
			Summary:   "Sets a value on a SAP GUI control.",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "Full SAP GUI control ID."},
				{Name: "value", Required: true, Kind: FieldKindString, Doc: "Value to set."},
				{Name: "params.control", Kind: FieldKindEnum, Enum: controlKinds, Doc: "Recorded control family."},
				{Name: "params.description", Kind: FieldKindString, Doc: "Recorded control description."},
			},
			Validate: sapGUIRequireTargetAnd("value"),
		},
		{
			Type:         "sapgui_press",
			Protocols:    []string{"sap_gui"},
			Summary:      "Presses a button or sends a virtual key.",
			RequireOneOf: [][]string{{"target", "params.vkey"}},
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Button/control ID when pressing a control."},
				{Name: "params.vkey", Kind: FieldKindInt, Doc: "SAP GUI virtual key code."},
				{Name: "params.description", Kind: FieldKindString, Doc: "Recorded control description."},
			},
			Validate: func(index int, action Action) []string {
				if strings.TrimSpace(action.Target) == "" {
					if _, ok := actionParamIntValue(action.Params, "vkey"); !ok {
						return []string{fmt.Sprintf("actions[%d] %q: sapgui_press requires target or params.vkey", index, action.Name)}
					}
				}
				return nil
			},
		},
		{
			Type:         "sapgui_select",
			Protocols:    []string{"sap_gui"},
			Summary:      "Selects an item by text, index, or ID.",
			RequireOneOf: [][]string{{"value", "params.text", "params.index", "params.id"}},
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "Full SAP GUI control ID."},
				{Name: "value", Kind: FieldKindString, Doc: "Selected text/value."},
				{Name: "params.text", Kind: FieldKindString, Doc: "Selected text."},
				{Name: "params.index", Kind: FieldKindInt, Doc: "Selected zero-based index."},
				{Name: "params.id", Kind: FieldKindString, Doc: "Selected SAP item ID."},
			},
			Validate: func(index int, action Action) []string {
				if strings.TrimSpace(action.Target) == "" {
					return []string{fmt.Sprintf("actions[%d] %q: sapgui_select requires target", index, action.Name)}
				}
				if strings.TrimSpace(action.Value) != "" {
					return nil
				}
				if value, ok := actionParamStringValue(action.Params, "text"); ok && value != "" {
					return nil
				}
				if _, ok := actionParamIntValue(action.Params, "index"); ok {
					return nil
				}
				if value, ok := actionParamStringValue(action.Params, "id"); ok && value != "" {
					return nil
				}
				return []string{fmt.Sprintf("actions[%d] %q: sapgui_select requires value, params.text, params.index, or params.id", index, action.Name)}
			},
		},
		{
			Type:      "sapgui_window",
			Protocols: []string{"sap_gui"},
			Summary:   "Activates, maximizes, resizes, or closes a SAP GUI window.",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "Window ID such as wnd[0]."},
				{Name: "params.method", Kind: FieldKindEnum, Enum: []string{"activate", "maximize", "resize", "close"}, Doc: "Window operation."},
			},
			Validate: sapGUIRequireTarget(),
		},
		sapGUIFamilySpec("sapgui_grid", []string{"select_row", "select_cell", "double_click_cell", "press_toolbar_button", "set_cell", "sort", "filter"}),
		sapGUIFamilySpec("sapgui_table", []string{"select_row", "set_cell", "get_cell", "press_cell", "scroll"}),
		sapGUIFamilySpec("sapgui_tree", []string{"select_node", "expand_node", "collapse_node", "double_click_node"}),
		{
			Type:      "sapgui_sync",
			Protocols: []string{"sap_gui"},
			Summary:   "Waits for SAP GUI status, screen change, or session-idle synchronization.",
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Optional control/status target."},
				{Name: "params.wait_for", Kind: FieldKindEnum, Enum: []string{"status_bar", "screen_change", "session_idle"}, Doc: "Synchronization mode."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Timeout in milliseconds."},
			},
		},
		{
			Type:      "sapgui_verify",
			Protocols: []string{"sap_gui"},
			Summary:   "Verifies a SAP GUI control value or status text.",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "Control or status bar ID."},
				{Name: "value", Required: true, Kind: FieldKindString, Doc: "Expected value/text."},
				{Name: "params.method", Kind: FieldKindEnum, Enum: []string{"equals", "contains", "matches"}, Doc: "Comparison method."},
			},
			Validate: sapGUIRequireTargetAnd("value"),
		},
		{
			Type:         "sapgui_call",
			Protocols:    []string{"sap_gui"},
			Summary:      "Preserves a raw SAP GUI scripting call for review-gated replay.",
			RequireOneOf: [][]string{{"target", "params.method"}},
			Fields: []FieldSpec{
				{Name: "target", Kind: FieldKindString, Doc: "Control ID or raw receiver path."},
				{Name: "params.method", Kind: FieldKindString, Doc: "Raw SAP GUI scripting method/property."},
				{Name: "params.args", Kind: FieldKindArray, Doc: "Raw argument list."},
			},
			Validate: func(index int, action Action) []string {
				method, _ := actionParamStringValue(action.Params, "method")
				if strings.TrimSpace(action.Target) == "" && strings.TrimSpace(method) == "" {
					return []string{fmt.Sprintf("actions[%d] %q: sapgui_call requires target or params.method", index, action.Name)}
				}
				return nil
			},
		},
	}
	for index := range specs {
		specs[index].ExecutionPosture = "external_runner_required"
		specs[index].ExecutionNote = "Authorable and importable, but not executable in the bundled runtime. Launch requires a qualified Windows SAP GUI runner, which is not shipped with APEX Load."
	}
	return specs
}

func sapGUIFamilySpec(actionType string, methods []string) ActionSpec {
	return ActionSpec{
		Type:      actionType,
		Protocols: []string{"sap_gui"},
		Summary:   "Executes a SAP GUI " + strings.TrimPrefix(actionType, "sapgui_") + " operation.",
		Fields: []FieldSpec{
			{Name: "target", Required: true, Kind: FieldKindString, Doc: "Full SAP GUI control ID."},
			{Name: "value", Kind: FieldKindString, Doc: "Operation value, when applicable."},
			{Name: "params.method", Kind: FieldKindEnum, Enum: methods, Doc: "Typed operation."},
			{Name: "params.row", Kind: FieldKindInt, Doc: "Row index."},
			{Name: "params.column", Kind: FieldKindString, Doc: "Column name or ID."},
		},
		Validate: sapGUIRequireTarget(),
	}
}

func sapGUIRequireTarget() func(int, Action) []string {
	return func(index int, action Action) []string {
		if strings.TrimSpace(action.Target) == "" {
			return []string{fmt.Sprintf("actions[%d] %q: %s requires target", index, action.Name, action.Type)}
		}
		return nil
	}
}

func sapGUIRequireTargetAnd(field string) func(int, Action) []string {
	return func(index int, action Action) []string {
		var issues []string
		if strings.TrimSpace(action.Target) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: %s requires target", index, action.Name, action.Type))
		}
		if field == "value" && strings.TrimSpace(action.Value) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: %s requires value", index, action.Name, action.Type))
		}
		return issues
	}
}
