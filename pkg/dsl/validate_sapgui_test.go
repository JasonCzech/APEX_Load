package dsl

import (
	"strings"
	"testing"
)

func TestValidateSAPGUIScriptAcceptsTypedActionsWithoutBaseURL(t *testing.T) {
	script := &Script{
		Name:     "SAPGUI",
		Protocol: "sap_gui",
		Config: ScriptConfig{
			SAPGUI: SAPGUIConfig{
				ConnectionString: "SAP_SYSTEM",
				Client:           "001",
				Username:         "perfuser",
				Password:         "{{secret.sapgui_password}}",
				SessionBehavior:  "attach",
				SyncTimeoutMs:    30000,
			},
		},
		Actions: []Action{
			{Name: "Logon", Type: "sapgui_logon", Params: map[string]any{"client": "001", "user": "perfuser", "password": "{{secret.sapgui_password}}"}},
			{Name: "SetUser", Type: "sapgui_set", Target: "wnd[0]/usr/txtRSYST-BNAME", Value: "perfuser", Params: map[string]any{"control": "text"}},
			{Name: "Enter", Type: "sapgui_press", Params: map[string]any{"vkey": 0}},
			{Name: "GridCell", Type: "sapgui_grid", Target: "wnd[0]/usr/cntlGRID/shellcont/shell", Value: "AA", Params: map[string]any{"method": "set_cell", "row": 0, "column": "CARRID"}},
			{Name: "Idle", Type: "sapgui_sync", Params: map[string]any{"wait_for": "session_idle", "timeout_ms": 30000}},
		},
	}

	if issues := ValidateScriptIssues(script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

func TestValidateSAPGUIRequiresSecretPassword(t *testing.T) {
	script := &Script{
		Name:     "SAPGUI",
		Protocol: "sap_gui",
		Config:   ScriptConfig{SAPGUI: SAPGUIConfig{Password: "plain-text"}},
		Actions:  []Action{{Name: "SetUser", Type: "sapgui_set", Target: "wnd[0]/usr/txtRSYST-BNAME", Value: "perfuser"}},
	}

	issues := ValidateScriptIssues(script)
	found := false
	for _, issue := range issues {
		if strings.Contains(issue, "config.sapgui.password") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("issues = %v, want SAP GUI password secret issue", issues)
	}
}

func TestValidateSAPGUIAllowsTrustedImportedPassword(t *testing.T) {
	script := &Script{
		Name:     "Imported SAPGUI",
		Protocol: "sap_gui",
		Metadata: &ScriptMetadata{Import: &ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				ImportPreservedCredentialPolicyKey: ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Config:  ScriptConfig{SAPGUI: SAPGUIConfig{Password: "recorded-password"}},
		Actions: []Action{{Name: "SetUser", Type: "sapgui_set", Target: "wnd[0]/usr/txtRSYST-BNAME", Value: "perfuser"}},
	}

	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("issues = %v, want retained imported password accepted", issues)
	}
}
