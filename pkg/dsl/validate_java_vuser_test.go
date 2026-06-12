package dsl

import (
	"strings"
	"testing"
)

func TestValidateJavaVuserScriptAcceptsRunnerActionWithoutBaseURL(t *testing.T) {
	script := &Script{
		Name:     "Java Vuser",
		Protocol: "java_vuser",
		Config: ScriptConfig{
			JavaVuser: JavaVuserConfig{
				Enabled:         false,
				HarnessJar:      "runners/java_vuser/apexload-java-vuser-runner.jar",
				MaxProcesses:    25,
				ActionTimeoutMs: 30000,
			},
		},
		Actions: []Action{{
			Name:   "Run Java Lifecycle",
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "full_lifecycle"},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v, want no issues", issues)
	}
}

func TestValidateJavaVuserScriptRejectsInvalidMethodAndProcessLimit(t *testing.T) {
	script := &Script{
		Name:     "Java Vuser",
		Protocol: "java_vuser",
		Config:   ScriptConfig{JavaVuser: JavaVuserConfig{MaxProcesses: -1}},
		Actions: []Action{{
			Name:   "Run Java Lifecycle",
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "mystery"},
		}},
	}
	issues := ValidateScriptIssues(script)
	joined := strings.Join(issues, "\n")
	if !strings.Contains(joined, "config.java_vuser.max_processes") || !strings.Contains(joined, "params.method must be") {
		t.Fatalf("issues = %v, want max_processes and method validation", issues)
	}
}
