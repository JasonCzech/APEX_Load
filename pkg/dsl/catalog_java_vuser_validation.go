package dsl

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

const (
	maxJavaVuserPathLength       = javarunner.MaxClasspathEntryBytes
	maxJavaVuserClasspathEntries = javarunner.MaxClasspathEntries
)

func validateJavaVuserScriptIssues(script *Script) []string {
	return validateJavaVuserScriptIssuesWithPolicy(script, false)
}

func validateJavaVuserScriptIssuesWithPolicy(script *Script, allowPreservedCredentials bool) []string {
	var issues []string
	cfg := script.Config.JavaVuser
	hasPhaseLifecycle, hasLegacyLifecycle := javaVuserLifecycleModes(script)
	if hasPhaseLifecycle && hasLegacyLifecycle {
		issues = append(issues, "java_vuser scripts must not mix persistent phase methods with legacy full_lifecycle actions")
	}
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
	selection := normalizeJavaVuserJDKSelection(cfg.JDKSelection)
	if selection == "" {
		issues = append(issues, "config.java_vuser.jdk_selection must be internal or specified")
	}
	if len(cfg.JDKHome) > maxJavaVuserPathLength {
		issues = append(issues, fmt.Sprintf("config.java_vuser.jdk_home must not exceed %d bytes", maxJavaVuserPathLength))
	}
	if containsJavaVuserControlCharacter(cfg.JDKHome) {
		issues = append(issues, "config.java_vuser.jdk_home must not contain control characters")
	}
	if selection == "specified" {
		if cfg.JDKHome == "" {
			issues = append(issues, "config.java_vuser.jdk_home is required when jdk_selection is specified")
		} else if !isPortableAbsolutePath(cfg.JDKHome) {
			issues = append(issues, "config.java_vuser.jdk_home must be an absolute Unix, Windows drive, or UNC path when jdk_selection is specified")
		}
	}
	for _, issue := range javarunner.ValidateClasspath(cfg.Classpath) {
		if issue.Index < 0 {
			issues = append(issues, fmt.Sprintf("config.java_vuser.classpath must not contain more than %d entries", maxJavaVuserClasspathEntries))
			continue
		}
		switch issue.Category {
		case javarunner.ClasspathEmpty:
			issues = append(issues, fmt.Sprintf("config.java_vuser.classpath[%d] must not be empty", issue.Index))
		case javarunner.ClasspathEntryTooLong:
			issues = append(issues, fmt.Sprintf("config.java_vuser.classpath[%d] must not exceed %d bytes", issue.Index, maxJavaVuserPathLength))
		case javarunner.ClasspathControlCharacter:
			issues = append(issues, fmt.Sprintf("config.java_vuser.classpath[%d] must not contain control characters", issue.Index))
		case javarunner.ClasspathWildcard:
			issues = append(issues, fmt.Sprintf("config.java_vuser.classpath[%d] must identify an exact file or directory and must not contain wildcards", issue.Index))
		case javarunner.ClasspathListSeparator:
			issues = append(issues, fmt.Sprintf("config.java_vuser.classpath[%d] must be a single path and must not contain the operating-system path list separator", issue.Index))
		default:
			issues = append(issues, fmt.Sprintf("config.java_vuser.classpath[%d] is invalid (%s)", issue.Index, issue.Category))
		}
	}
	vmParameterIssues := javarunner.ValidateVMParameters(cfg.AdditionalVMParameters)
	if allowPreservedCredentials {
		vmParameterIssues = javarunner.ValidateVMParametersForExecution(cfg.AdditionalVMParameters)
	}
	for _, issue := range vmParameterIssues {
		if issue.Index < 0 {
			issues = append(issues, fmt.Sprintf("config.java_vuser.additional_vm_parameters is invalid (%s)", issue.Category))
			continue
		}
		issues = append(issues, fmt.Sprintf("config.java_vuser.additional_vm_parameters[%d] is invalid (%s)", issue.Index, issue.Category))
	}
	return issues
}

func javaVuserLifecycleModes(script *Script) (hasPhase, hasLegacy bool) {
	if script == nil {
		return false, false
	}
	for _, actions := range [][]Action{script.Init, script.Actions, script.Teardown} {
		phase, legacy := javaVuserActionLifecycleModes(actions)
		hasPhase = hasPhase || phase
		hasLegacy = hasLegacy || legacy
	}
	return hasPhase, hasLegacy
}

func javaVuserActionLifecycleModes(actions []Action) (hasPhase, hasLegacy bool) {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			method, _ := actionParamStringValue(action.Params, "method")
			if strings.TrimSpace(method) == "" || strings.EqualFold(strings.TrimSpace(method), "full_lifecycle") {
				hasLegacy = true
			} else {
				hasPhase = true
			}
		}
		if action.Control == nil {
			continue
		}
		for _, branch := range action.Control.ActionBranches() {
			branchPhase, branchLegacy := javaVuserActionLifecycleModes(branch.Actions)
			hasPhase = hasPhase || branchPhase
			hasLegacy = hasLegacy || branchLegacy
		}
	}
	return hasPhase, hasLegacy
}

func normalizeJavaVuserJDKSelection(value string) string {
	switch value {
	case "", "internal":
		return "internal"
	case "specified":
		return "specified"
	default:
		return ""
	}
}

func containsJavaVuserControlCharacter(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

// isPortableAbsolutePath intentionally does not use filepath.IsAbs: imports
// are validated on the controller but may execute on a generator with a
// different operating system. Native existence and syntax checks happen in
// the selected Java executor.
func isPortableAbsolutePath(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '/' {
		return true
	}
	if len(value) >= 2 && value[0] == '\\' && value[1] == '\\' {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}
