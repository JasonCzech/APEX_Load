package enterprisearchive

import (
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type sapGUIImportState struct {
	optionalDepth int
}

func ConvertSAPGUI(actions []*ParsedAction, opts ConvertOptions, detected DetectedProtocol, files map[string]string) (*ConvertResult, error) {
	detected.ImportMode = ImportModeExternalRunnerRequired
	detected.APEXLoadProtocol = ""
	detected.RunnerRequirement = "windows_sap_gui_runner"
	detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, "Windows SAP GUI runner bridge evidence", "SAP GUI Scripting enabled on runner host")

	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append([]string(nil), detected.Warnings...),
		Script: &dsl.Script{
			Name:          firstNonEmpty(opts.ScriptName, "Converted_SAP_GUI_Script"),
			Version:       "1.0.0",
			Protocol:      "sap_gui",
			GeneratedBy:   "apexload-enterprise-script-archive-sapgui-importer",
			GeneratedFrom: "enterprise_script_archive",
			Config: dsl.ScriptConfig{
				SAPGUI: dsl.SAPGUIConfig{
					Password:        "{{secret.sapgui_password}}",
					SessionBehavior: "attach",
					SyncTimeoutMs:   30000,
				},
			},
		},
	}
	state := &sapGUIImportState{}
	for _, parsed := range actions {
		if parsed == nil {
			continue
		}
		for _, fn := range parsed.Functions {
			result.Stats.TotalFunctions++
			if fn.Phase == "" {
				fn.Phase = parsed.Phase
			}
			converted, mode, ok := convertSAPGUIFunction(result, state, fn)
			if !ok {
				converted, mode = sapGUIFallbackAction(fn)
			}
			if converted.Type == "" {
				continue
			}
			appendSAPGUIAction(result, fn, converted, mode)
		}
	}
	if len(result.Script.Actions) == 0 && len(result.Script.Init) == 0 && len(result.Script.Teardown) == 0 {
		result.Script.Actions = append(result.Script.Actions, dsl.Action{
			Name: "SAP_GUI_Review",
			Type: "source_review",
			Params: map[string]any{
				"function": "sap_gui",
				"args":     []any{"No executable SAP GUI actions were recognized."},
			},
			Metadata: map[string]any{"review_reason": "no_mapped_sapgui_functions"},
		})
	}
	structureSourceControlFlow(result)
	result.ReviewArtifact = NewReviewArtifact(detected, files, actions, map[string]any{
		"source_script_type":  "SAP GUI",
		"source_profile":      "SAP GUI desktop",
		"runner_requirement":  "windows_sap_gui_runner",
		"external_execution":  true,
		"script_draft_status": "available",
	})
	result.Warnings = append(result.Warnings, "SAP GUI import produced a DSL draft, but execution requires the Windows SAP GUI runner bridge.")
	return result, nil
}

func convertSAPGUIFunction(result *ConvertResult, state *sapGUIImportState, fn ParsedFunction) (dsl.Action, SourceFunctionSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	if strings.HasPrefix(name, "lr_") {
		action, mode, ok := convertSourceFunctionAction(fn)
		return action, mode, ok
	}
	if name == "sapgui_begin_optional" {
		state.optionalDepth++
		appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.sapgui_optional_blocks", "begin_optional", "control", "contributor", "Opened a SAP GUI optional block; subsequent mapped actions carry params.optional=true.", SourceFunctionSupportExternalRequired))
		recordSourceFunctionSupport(result, SourceFunctionSupportExternalRequired)
		return dsl.Action{}, SourceFunctionSupportExternalRequired, true
	}
	if name == "sapgui_end_optional" {
		if state.optionalDepth > 0 {
			state.optionalDepth--
		}
		appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.sapgui_optional_blocks", "end_optional", "control", "contributor", "Closed a SAP GUI optional block.", SourceFunctionSupportExternalRequired))
		recordSourceFunctionSupport(result, SourceFunctionSupportExternalRequired)
		return dsl.Action{}, SourceFunctionSupportExternalRequired, true
	}

	action := sapGUIBaseAction(fn, state)
	switch name {
	case "sapgui_open_connection", "sapgui_open_connection_ex":
		action.Type = "sapgui_connect"
		action.Target = sapGUIArg(fn, 0, "ConnectionString", "Connection")
		result.Script.Config.SAPGUI.ConnectionString = firstNonEmpty(result.Script.Config.SAPGUI.ConnectionString, action.Target)
	case "sapgui_logon":
		action.Type = "sapgui_logon"
		if connection := sapGUILogonConnection(fn); connection != "" {
			result.Script.Config.SAPGUI.ConnectionString = firstNonEmpty(result.Script.Config.SAPGUI.ConnectionString, connection)
			action.Params["connection_string"] = connection
		}
		action.Params["client"] = sapGUILogonClient(fn)
		action.Params["user"] = sapGUILogonUser(fn)
		action.Params["password"] = "{{secret.sapgui_password}}"
		action.Params["language"] = sapGUILogonLanguage(fn)
		result.Script.Config.SAPGUI.Client = stringParam(action.Params, "client")
		result.Script.Config.SAPGUI.Username = stringParam(action.Params, "user")
		result.Script.Config.SAPGUI.Password = "{{secret.sapgui_password}}"
		result.Script.Config.SAPGUI.Language = stringParam(action.Params, "language")
	case "sapgui_set_text", "sapgui_set_password", "sapgui_set_ok_code", "sapgui_set_property":
		action.Type = "sapgui_set"
		action.Target = sapGUIArg(fn, 0, "Id", "ID", "Target")
		action.Value = sapGUIArg(fn, 1, "Value", "Text")
		action.Params["control"] = sapGUIControlKind(name, action.Target)
		if name == "sapgui_set_password" {
			action.Value = "{{secret.sapgui_password}}"
			action.Metadata["secret_replaced"] = true
		}
	case "sapgui_send_vkey":
		action.Type = "sapgui_press"
		if vkey, ok := sapGUIIntArg(fn, 0, "VKey", "Key"); ok {
			action.Params["vkey"] = vkey
		}
	case "sapgui_press_button", "sapgui_press_toolbar_button", "sapgui_press_menu", "sapgui_select_menu", "sapgui_click", "sapgui_double_click", "sapgui_right_click":
		action.Type = "sapgui_press"
		action.Target = sapGUIArg(fn, 0, "Id", "ID", "Target")
		action.Params["method"] = strings.TrimPrefix(name, "sapgui_")
	case "sapgui_select_combobox_item", "sapgui_select_context_menu_item", "sapgui_select_file", "sapgui_select_list_item", "sapgui_select_tab", "sapgui_select_radio_button":
		action.Type = "sapgui_select"
		action.Target = sapGUIArg(fn, 0, "Id", "ID", "Target")
		action.Value = sapGUIArg(fn, 1, "Value", "Text", "Item")
	case "sapgui_select_checkbox", "sapgui_unselect_checkbox":
		action.Type = "sapgui_set"
		action.Target = sapGUIArg(fn, 0, "Id", "ID", "Target")
		action.Value = strconv.FormatBool(name == "sapgui_select_checkbox")
		action.Params["control"] = "checkbox"
	case "sapgui_window_maximize", "sapgui_window_restore", "sapgui_window_resize", "sapgui_window_close", "sapgui_select_window":
		action.Type = "sapgui_window"
		action.Target = firstNonEmpty(sapGUIArg(fn, 0, "Id", "ID", "Target"), "wnd[0]")
		action.Params["method"] = sapGUIWindowMethod(name)
	case "sapgui_sync", "sapgui_wait_for_window", "sapgui_wait_for_status_bar", "sapgui_wait_for_session_idle", "sapgui_wait_for_screen_change":
		action.Type = "sapgui_sync"
		action.Target = sapGUIArg(fn, 0, "Id", "ID", "Target")
		action.Params["wait_for"] = sapGUISyncMode(name)
		if timeout, ok := sapGUIIntArg(fn, 1, "Timeout", "TimeoutMs"); ok {
			action.Params["timeout_ms"] = timeout
		}
	case "sapgui_status_bar_get_text", "sapgui_status_bar_check_text", "sapgui_verify_text", "sapgui_verify_property":
		action.Type = "sapgui_verify"
		action.Target = firstNonEmpty(sapGUIArg(fn, 0, "Id", "ID", "Target"), "wnd[0]/sbar")
		action.Value = sapGUIArg(fn, 1, "Value", "Text", "Expected")
		action.Params["method"] = "contains"
	default:
		if strings.HasPrefix(name, "sapgui_grid_") {
			action.Type = "sapgui_grid"
			action.Target = sapGUIArg(fn, 0, "Id", "ID", "Target")
			action.Params["method"] = strings.TrimPrefix(name, "sapgui_grid_")
			action.Value = sapGUIArg(fn, 3, "Value", "Text")
			addSAPGUIRowColumn(&action, fn)
		} else if strings.HasPrefix(name, "sapgui_table_") {
			action.Type = "sapgui_table"
			action.Target = sapGUIArg(fn, 0, "Id", "ID", "Target")
			action.Params["method"] = strings.TrimPrefix(name, "sapgui_table_")
			action.Value = sapGUIArg(fn, 3, "Value", "Text")
			addSAPGUIRowColumn(&action, fn)
		} else if strings.HasPrefix(name, "sapgui_tree_") {
			action.Type = "sapgui_tree"
			action.Target = sapGUIArg(fn, 0, "Id", "ID", "Target")
			action.Params["method"] = strings.TrimPrefix(name, "sapgui_tree_")
			action.Value = sapGUIArg(fn, 1, "Value", "Node", "Text")
		} else {
			return dsl.Action{}, "", false
		}
	}
	if action.Type == "" {
		return dsl.Action{}, "", false
	}
	return action, SourceFunctionSupportExternalRequired, true
}

func sapGUIBaseAction(fn ParsedFunction, state *sapGUIImportState) dsl.Action {
	action := dsl.Action{
		Name:   sanitizeName(firstNonEmpty(fn.StepName, firstNonEmptyArg(fn, "Name"), fn.Name)),
		Params: map[string]any{},
		Metadata: map[string]any{
			"source_function": fn.Name,
			"source_file":     fn.Filename,
			"source_line":     fn.LineNumber,
			"source_protocol": "sap_gui",
		},
	}
	if state.optionalDepth > 0 {
		action.Params["optional"] = true
	}
	if description := firstNonEmptyArg(fn, "Description"); description != "" {
		action.Params["description"] = description
	}
	return action
}

func sapGUIFallbackAction(fn ParsedFunction) (dsl.Action, SourceFunctionSupportMode) {
	if def, ok := lookupSourceFunction(fn.Name); ok {
		action := convertGenericSourceAction(fn, "source_review", def)
		action.Metadata["runner_requirement"] = "windows_sap_gui_runner"
		return action, def.SupportMode
	}
	action := dsl.Action{
		Name:   sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "sapgui_call")),
		Type:   "custom_call",
		Params: map[string]any{"function": fn.Name, "args": rawArgsAsAny(fn.RawArgs), "support_mode": string(SourceFunctionSupportCustomReview)},
		Metadata: map[string]any{
			"source_function":     fn.Name,
			"source_file":         fn.Filename,
			"source_line":         fn.LineNumber,
			"source_protocol":     "sap_gui",
			"review_required":     true,
			"runner_requirement":  "windows_sap_gui_runner",
			"source_support_mode": string(SourceFunctionSupportCustomReview),
		},
	}
	return action, SourceFunctionSupportCustomReview
}

func appendSAPGUIAction(result *ConvertResult, fn ParsedFunction, action dsl.Action, mode SourceFunctionSupportMode) {
	actionIndex := actionCountForPhase(result.Script, fn.Phase)
	appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, sourceReviewActionPath(fn.Phase, actionIndex), action.Name, "action", statusForSupportMode(mode), "Mapped SAP GUI source function to a DSL draft action; execution requires the Windows SAP GUI runner.", mode))
	appendActionForPhase(result.Script, fn.Phase, action)
	result.Stats.ConvertedActions++
	recordSourceFunctionSupport(result, mode)
}

func sapGUIArg(fn ParsedFunction, index int, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(firstNonEmptyArg(fn, key)); value != "" {
			return convertParamReferences(value)
		}
	}
	if index >= 0 && index < len(fn.RawArgs) {
		return convertParamReferences(cleanConfigValue(fn.RawArgs[index]))
	}
	return ""
}

func sapGUILogonConnection(fn ParsedFunction) string {
	if value := sapGUIConnectionFromRawBlock(fn.RawBlock); value != "" {
		return convertParamReferences(value)
	}
	for key, value := range fn.Args {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		upperKey := strings.ToUpper(key)
		if upperKey == "ASHOST" || upperKey == "MSHOST" || strings.HasPrefix(upperKey, "SAPGUI") {
			return convertParamReferences(key + "=" + value)
		}
	}
	for _, arg := range fn.RawArgs {
		value := strings.TrimSpace(cleanConfigValue(arg))
		if value == "" {
			continue
		}
		upper := strings.ToUpper(value)
		if strings.Contains(upper, "ASHOST=") || strings.Contains(upper, "MSHOST=") || strings.Contains(upper, "SAPGUI") {
			return convertParamReferences(value)
		}
	}
	return sapGUIArg(fn, -1, "ConnectionString", "Connection")
}

func sapGUIConnectionFromRawBlock(raw string) string {
	upper := strings.ToUpper(raw)
	for _, marker := range []string{"ASHOST=", "MSHOST=", "SAPGUI"} {
		idx := strings.Index(upper, marker)
		if idx < 0 {
			continue
		}
		start := strings.LastIndexAny(raw[:idx], "\"'")
		if start < 0 {
			continue
		}
		quote := raw[start]
		rest := raw[idx:]
		end := strings.IndexByte(rest, quote)
		if end < 0 {
			continue
		}
		return strings.TrimSpace(raw[start+1 : idx+end])
	}
	return ""
}

func sapGUILogonClient(fn ParsedFunction) string {
	if value := sapGUIArg(fn, -1, "Client"); value != "" {
		return value
	}
	if len(fn.RawArgs) >= 4 && looksLikeSAPClient(fn.RawArgs[0]) {
		return convertParamReferences(cleanConfigValue(fn.RawArgs[0]))
	}
	return ""
}

func sapGUILogonUser(fn ParsedFunction) string {
	if value := sapGUIArg(fn, -1, "User", "Username"); value != "" {
		return value
	}
	if len(fn.RawArgs) >= 4 && looksLikeSAPClient(fn.RawArgs[0]) {
		return convertParamReferences(cleanConfigValue(fn.RawArgs[1]))
	}
	return ""
}

func sapGUILogonLanguage(fn ParsedFunction) string {
	if value := sapGUIArg(fn, -1, "Language"); value != "" {
		return value
	}
	if len(fn.RawArgs) >= 4 && looksLikeSAPClient(fn.RawArgs[0]) {
		return convertParamReferences(cleanConfigValue(fn.RawArgs[3]))
	}
	return ""
}

func looksLikeSAPClient(value string) bool {
	value = strings.TrimSpace(cleanConfigValue(value))
	if len(value) != 3 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func sapGUIIntArg(fn ParsedFunction, index int, keys ...string) (int, bool) {
	raw := sapGUIArg(fn, index, keys...)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil
}

func sapGUIWindowMethod(name string) string {
	switch {
	case strings.Contains(name, "maximize"):
		return "maximize"
	case strings.Contains(name, "resize"):
		return "resize"
	case strings.Contains(name, "close"):
		return "close"
	default:
		return "activate"
	}
}

func sapGUISyncMode(name string) string {
	switch {
	case strings.Contains(name, "status"):
		return "status_bar"
	case strings.Contains(name, "screen"):
		return "screen_change"
	default:
		return "session_idle"
	}
}

func sapGUIControlKind(name string, target string) string {
	lower := strings.ToLower(name + " " + target)
	switch {
	case strings.Contains(lower, "chk"):
		return "checkbox"
	case strings.Contains(lower, "cmb"):
		return "combo"
	case strings.Contains(lower, "rad"):
		return "radio"
	case strings.Contains(lower, "btn"):
		return "button"
	case strings.Contains(lower, "grid"):
		return "grid"
	case strings.Contains(lower, "table"):
		return "table"
	case strings.Contains(lower, "tree"):
		return "tree"
	case strings.Contains(lower, "txt") || strings.Contains(lower, "ctxt"):
		return "text"
	default:
		return "unknown"
	}
}

func addSAPGUIRowColumn(action *dsl.Action, fn ParsedFunction) {
	if action == nil {
		return
	}
	if row, ok := sapGUIIntArg(fn, 1, "Row"); ok {
		action.Params["row"] = row
	}
	if column := sapGUIArg(fn, 2, "Column", "Col"); column != "" {
		action.Params["column"] = column
	}
}

func stringParam(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return value
}

func rawArgsAsAny(args []string) []any {
	out := make([]any, 0, len(args))
	for _, arg := range args {
		out = append(out, convertParamReferences(cleanConfigValue(arg)))
	}
	return out
}

func isSAPGUIProtocolDetected(detected DetectedProtocol) bool {
	return detected.CanonicalSourceProtocol == "sap_gui"
}

func isSAPGUIProject(projectConfig *ProjectConfig) bool {
	if projectConfig == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(projectConfig.ScriptType), "SAP GUI") ||
		strings.EqualFold(strings.TrimSpace(projectConfig.ScriptType), "SAPGUI")
}

func hasSAPGUIFunctions(actions []*ParsedAction) bool {
	for _, parsed := range actions {
		if parsed == nil {
			continue
		}
		for _, fn := range parsed.Functions {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(fn.Name)), "sapgui_") {
				return true
			}
		}
	}
	return false
}

func sapGUINoParseReview(files map[string]string, detected DetectedProtocol) *ConvertResult {
	detected.ImportMode = ImportModeExternalRunnerRequired
	detected.RunnerRequirement = "windows_sap_gui_runner"
	return newEnterpriseArchiveReviewResult(detected, files, nil, map[string]any{
		"reason":             "no_parseable_sapgui_actions",
		"runner_requirement": "windows_sap_gui_runner",
	})
}
