package dsl

import "fmt"

func validateJavaVuserScriptIssues(script *Script) []string {
	var issues []string
	cfg := script.Config.JavaVuser
	if cfg.MaxProcesses < 0 {
		issues = append(issues, "config.java_vuser.max_processes must be zero or greater")
	}
	if cfg.StartupTimeoutMs < 0 {
		issues = append(issues, "config.java_vuser.startup_timeout_ms must be zero or greater")
	} else if cfg.StartupTimeoutMs > MaxConfiguredProtocolTimeoutMS {
		issues = append(issues, fmt.Sprintf("config.java_vuser.startup_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	}
	if cfg.ActionTimeoutMs < 0 {
		issues = append(issues, "config.java_vuser.action_timeout_ms must be zero or greater")
	} else if cfg.ActionTimeoutMs > MaxConfiguredProtocolTimeoutMS {
		issues = append(issues, fmt.Sprintf("config.java_vuser.action_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	}
	return issues
}
