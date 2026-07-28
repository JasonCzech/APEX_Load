package dsl

import (
	"path/filepath"
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

func TestValidateJavaVuserScriptAcceptsLifecycleAliases(t *testing.T) {
	// Mixed-case spellings are accepted case-insensitively; the runtime
	// canonicalizes them before dispatch so validated scripts cannot fail with an
	// unsupported-lifecycle-method error at execution time.
	for _, method := range []string{"init", "vuser_init", "initialize", "action", "Action", "run", "end", "vuser_end", "teardown", "full_lifecycle", "Init", "RUN", "Vuser_End", "FULL_LIFECYCLE"} {
		t.Run(method, func(t *testing.T) {
			action := Action{
				Name:   "Run Java " + method,
				Type:   "java_vuser_run",
				Target: "Actions",
				Params: map[string]any{"method": method},
			}
			if issues := validateJavaVuserActionIssues(0, action); len(issues) != 0 {
				t.Fatalf("validateJavaVuserActionIssues(%q) = %v, want no issues", method, issues)
			}
		})
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

func TestValidateJavaVuserScriptRejectsMixedLifecycleModes(t *testing.T) {
	script := &Script{
		Name:     "Mixed Java Vuser",
		Protocol: "java_vuser",
		Init: []Action{{
			Name: "Init", Type: "java_vuser_run", Target: "Actions",
			Params: map[string]any{"method": "init"},
		}},
		Actions: []Action{{
			Name: "Conditional legacy call",
			Type: "control_if",
			Control: &ControlFlow{
				Steps: []Action{{
					Name: "Legacy", Type: "java_vuser_run", Target: "Actions",
					Params: map[string]any{"method": "full_lifecycle"},
				}},
			},
		}},
	}
	issues := ValidateScriptIssues(script)
	if joined := strings.Join(issues, "\n"); !strings.Contains(joined, "must not mix persistent phase methods with legacy full_lifecycle") {
		t.Fatalf("issues = %v, want mixed lifecycle rejection", issues)
	}
}

func TestValidateJavaVuserVMSettings(t *testing.T) {
	boolValue := func(value bool) *bool { return &value }
	tests := []struct {
		name string
		cfg  JavaVuserConfig
		want string
	}{
		{name: "defaults", cfg: JavaVuserConfig{}},
		{name: "specified unix home", cfg: JavaVuserConfig{JDKSelection: "specified", JDKHome: "/opt/jdks/temurin-21"}},
		{name: "specified windows home", cfg: JavaVuserConfig{JDKSelection: "specified", JDKHome: `C:\Program Files\Java\jdk-21`}},
		{name: "specified UNC home", cfg: JavaVuserConfig{JDKSelection: "specified", JDKHome: `\\server\jdks\jdk-21`}},
		{name: "presence booleans", cfg: JavaVuserConfig{UseXBootClasspath: boolValue(false), DedicatedClassLoader: boolValue(true)}},
		{name: "unknown selection", cfg: JavaVuserConfig{JDKSelection: "automatic"}, want: "jdk_selection must be internal or specified"},
		{name: "specified missing home", cfg: JavaVuserConfig{JDKSelection: "specified"}, want: "jdk_home is required"},
		{name: "specified relative home", cfg: JavaVuserConfig{JDKSelection: "specified", JDKHome: "jdks/21"}, want: "must be an absolute Unix, Windows drive, or UNC path"},
		{name: "home control character", cfg: JavaVuserConfig{JDKHome: "/opt/jdk\n21"}, want: "jdk_home must not contain control characters"},
		{name: "home unicode control character", cfg: JavaVuserConfig{JDKHome: "/opt/jdk\u008521"}, want: "jdk_home must not contain control characters"},
		{name: "empty classpath entry", cfg: JavaVuserConfig{Classpath: []string{"/classes", ""}}, want: "classpath[1] must not be empty"},
		{name: "classpath control character", cfg: JavaVuserConfig{Classpath: []string{"/classes\nother"}}, want: "classpath[0] must not contain control characters"},
		{name: "classpath wildcard", cfg: JavaVuserConfig{Classpath: []string{"/classes/*.jar"}}, want: "classpath[0] must identify an exact file or directory"},
		{name: "classpath list separator", cfg: JavaVuserConfig{Classpath: []string{"/opt/app" + string(filepath.ListSeparator) + "/opt/evil"}}, want: "classpath[0] must be a single path and must not contain the operating-system path list separator"},
		{name: "windows drive classpath survives cross-os import", cfg: JavaVuserConfig{Classpath: []string{`C:\lib\a.jar`}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := validateJavaVuserScriptIssues(&Script{Config: ScriptConfig{JavaVuser: test.cfg}})
			joined := strings.Join(issues, "\n")
			if test.want == "" && len(issues) != 0 {
				t.Fatalf("issues = %v, want none", issues)
			}
			if test.want != "" && !strings.Contains(joined, test.want) {
				t.Fatalf("issues = %v, want an issue containing %q", issues, test.want)
			}
		})
	}
}

func TestValidateJavaVuserVMSettingsEnforcesCollectionBounds(t *testing.T) {
	classpath := make([]string, maxJavaVuserClasspathEntries+1)
	for index := range classpath {
		classpath[index] = "/classes"
	}
	issues := validateJavaVuserScriptIssues(&Script{Config: ScriptConfig{JavaVuser: JavaVuserConfig{Classpath: classpath}}})
	if joined := strings.Join(issues, "\n"); !strings.Contains(joined, "classpath must not contain more than 256 entries") {
		t.Fatalf("issues = %v, want classpath collection bound", issues)
	}
}

func TestValidateJavaVuserVMParametersUsesValueFreeStructuralErrors(t *testing.T) {
	const secret = "do-not-expose-this-secret"
	issues := validateJavaVuserScriptIssues(&Script{Config: ScriptConfig{JavaVuser: JavaVuserConfig{
		AdditionalVMParameters: []string{
			"-Xmx512m",
			"-Dlabel=hello world",
			"-Dapexload.secret=" + secret,
			"-Dservice.password={{secret.java_service_password}}",
			"-Dservice.token=literal-sensitive-value",
			"-javaagent:/tmp/agent.jar",
			"--class-path=/tmp/classes",
			"@/tmp/args",
		},
	}}})
	joined := strings.Join(issues, "\n")
	for _, category := range []string{
		"reserved_property",
		"template_forbidden",
		"sensitive_property",
		"agent_hook",
		"classpath_override",
		"argument_file",
	} {
		if !strings.Contains(joined, category) {
			t.Fatalf("issues = %v, want category %q", issues, category)
		}
	}
	if strings.Contains(joined, secret) || strings.Contains(joined, "literal-sensitive-value") ||
		strings.Contains(joined, "java_service_password") {
		t.Fatalf("issues exposed rejected VM parameter value: %s", joined)
	}
}

func TestTrustedImportValidationAllowsOnlySensitiveJavaVMArguments(t *testing.T) {
	script := &Script{
		Name: "Imported Java Vuser", Version: "1", Protocol: "java_vuser",
		Config: ScriptConfig{JavaVuser: JavaVuserConfig{
			AdditionalVMParameters: []string{"-Ddatabase.password=imported-password"},
		}},
		Actions: []Action{{
			Name: "Run", Type: "java_vuser_run", Target: "Actions",
			Params: map[string]any{"method": "action"},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) == 0 ||
		!strings.Contains(strings.Join(issues, "\n"), "sensitive_property") {
		t.Fatalf("ordinary validation issues = %v", issues)
	}
	if issues := ValidateScriptIssuesWithPreservedImportCredentials(script); len(issues) != 0 {
		t.Fatalf("trusted import validation issues = %v", issues)
	}

	script.Config.JavaVuser.AdditionalVMParameters =
		append(script.Config.JavaVuser.AdditionalVMParameters, "-javaagent:/tmp/agent.jar")
	if issues := ValidateScriptIssuesWithPreservedImportCredentials(script); len(issues) == 0 ||
		!strings.Contains(strings.Join(issues, "\n"), "agent_hook") {
		t.Fatalf("trusted structural validation issues = %v", issues)
	}
}
