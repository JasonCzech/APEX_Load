package dsl

import (
	"strings"
)

func validateSAPGUIScriptIssues(script *Script) []string {
	var issues []string
	cfg := script.Config.SAPGUI
	if cfg.SyncTimeoutMs < 0 {
		issues = append(issues, "config.sapgui.sync_timeout_ms must be zero or greater")
	}
	if cfg.SessionBehavior != "" && !IsSAPGUISessionBehavior(cfg.SessionBehavior) {
		issues = append(issues, "config.sapgui.session_behavior must be attach, new_session, or reuse")
	}
	if cfg.Password != "" && !strings.HasPrefix(cfg.Password, "{{secret.") &&
		!sapGUIImportedCredentialPreservationEnabled(script) {
		issues = append(issues, "config.sapgui.password must be a secret template")
	}
	return issues
}

func sapGUIImportedCredentialPreservationEnabled(script *Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return false
	}
	policy, _ := script.Metadata.Import.Preserved[ImportPreservedCredentialPolicyKey].(string)
	return strings.EqualFold(strings.TrimSpace(policy), ImportPreservedCredentialPolicyPreserve)
}

// IsSAPGUISessionBehavior identifies the bounded runtime configuration vocabulary.
// These selectors are configuration, not session credentials.
func IsSAPGUISessionBehavior(value string) bool {
	return isAllowedValue(value, "attach", "new_session", "reuse")
}
