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
	if cfg.SessionBehavior != "" && !isAllowedValue(cfg.SessionBehavior, "attach", "new_session", "reuse") {
		issues = append(issues, "config.sapgui.session_behavior must be attach, new_session, or reuse")
	}
	if cfg.Password != "" && !strings.HasPrefix(cfg.Password, "{{secret.") {
		issues = append(issues, "config.sapgui.password must be a secret template")
	}
	return issues
}
