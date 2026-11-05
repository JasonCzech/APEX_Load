package importreview

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

// noProjectSecrets stands in for a controller's project-secret resolver: a
// replay or debug start passes one, an import or authoring preview does not.
var noProjectSecrets = engine.SecretResolverFunc(func(string) (string, bool) { return "", false })

func environmentPreflightScript() *dsl.Script {
	return &dsl.Script{
		Name: "Env preflight", Version: "1", Protocol: "browser",
		Config: dsl.ScriptConfig{BaseURL: "https://{{env.APEXLOAD_PREFLIGHT_HOST}}"},
		Actions: []dsl.Action{
			{Name: "Open", Type: "browser_navigate", Target: "/"},
			{Name: "Fill password", Type: "browser_fill", Selector: "#pw", Target: "#pw", Value: "{{env.APEXLOAD_PREFLIGHT_PW}}",
				Metadata: map[string]any{"note": "recorded as {{env.APEXLOAD_PREFLIGHT_NOTE_ONLY}}"}},
			{Name: "Fill legacy", Type: "browser_fill", Selector: "#pw2", Target: "#pw2", Value: "{{env.PASSWORD}}"},
			{Name: "Fill other", Type: "browser_fill", Selector: "#otp", Target: "#otp", Value: "{{env.APEXLOAD_PREFLIGHT_OTP}}"},
		},
	}
}

func environmentWarning(preflight *ReplayPreflight) string {
	for _, line := range preflight.Warnings {
		if strings.HasPrefix(line, ReplayUnreadableEnvironmentWarning) {
			return line
		}
	}
	return ""
}

// On the controller that runs a replay or debug start, the preflight names
// each variable a script cannot read and why, and warns rather than refusing.
func TestBuildReplayPreflightReportsUnreadableEnvironment(t *testing.T) {
	t.Setenv(engine.EnvScriptEnvAllowlist, "APEXLOAD_PREFLIGHT_PW,APEXLOAD_PREFLIGHT_HOST")
	t.Setenv("APEXLOAD_PREFLIGHT_HOST", "app.example.test")
	preflight := BuildReplayPreflight(environmentPreflightScript(), nil, nil, nil, noProjectSecrets)
	if !preflight.Valid {
		t.Fatalf("preflight refused replay: %+v", preflight)
	}
	// A reference in step metadata records provenance; nothing reads it.
	wantRequired := []string{"APEXLOAD_PREFLIGHT_HOST", "APEXLOAD_PREFLIGHT_OTP", "APEXLOAD_PREFLIGHT_PW", "PASSWORD"}
	if strings.Join(preflight.RequiredEnvironment, ",") != strings.Join(wantRequired, ",") {
		t.Fatalf("required = %v, want %v", preflight.RequiredEnvironment, wantRequired)
	}
	wantUnreadable := []string{"APEXLOAD_PREFLIGHT_OTP", "APEXLOAD_PREFLIGHT_PW", "PASSWORD"}
	if strings.Join(preflight.UnreadableEnvironment, ",") != strings.Join(wantUnreadable, ",") {
		t.Fatalf("unreadable = %v, want %v", preflight.UnreadableEnvironment, wantUnreadable)
	}
	warning := environmentWarning(preflight)
	for _, want := range []string{
		"APEXLOAD_PREFLIGHT_PW (not set)",
		"APEXLOAD_PREFLIGHT_OTP (not listed in APEXLOAD_SCRIPT_ENV_ALLOWLIST)",
		"PASSWORD (the name is always blocked",
	} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning = %q, want it to name %q", warning, want)
		}
	}
	var check *ReplayPreflightCheck
	for index := range preflight.Checks {
		if preflight.Checks[index].ID == "environment" {
			check = &preflight.Checks[index]
		}
	}
	if check == nil || check.Status != "warn" {
		t.Fatalf("environment check = %+v, want a warning", check)
	}
}

// An import or authoring preview stores its preflight with the script, so it
// reports only what holds on every controller: an always-blocked name.
func TestBuildReplayPreflightPreviewReportsOnlyBlockedNames(t *testing.T) {
	t.Setenv(engine.EnvScriptEnvAllowlist, "")
	preflight := BuildReplayPreflight(environmentPreflightScript(), nil, nil, nil)
	if got := strings.Join(preflight.UnreadableEnvironment, ","); got != "PASSWORD" {
		t.Fatalf("unreadable = %q, want only the always-blocked PASSWORD", got)
	}
	if warning := environmentWarning(preflight); strings.Contains(warning, "not set") || strings.Contains(warning, "not listed") {
		t.Fatalf("warning = %q, want no controller-specific reason in a stored preview", warning)
	}
}

func TestBuildReplayPreflightReadsEnvironmentInBase64Bodies(t *testing.T) {
	t.Setenv(engine.EnvScriptEnvAllowlist, "")
	script := &dsl.Script{Name: "Encoded", Version: "1", Protocol: "http", Actions: []dsl.Action{{
		Name: "post", Type: "request", Method: "POST", Target: "https://api.example.test/login",
		Body: base64.StdEncoding.EncodeToString([]byte(`{"key":"{{env.APEXLOAD_PREFLIGHT_ENCODED}}"}`)), BodyEncoding: "base64",
	}}}
	preflight := BuildReplayPreflight(script, nil, nil, nil, noProjectSecrets)
	if got := strings.Join(preflight.RequiredEnvironment, ","); got != "APEXLOAD_PREFLIGHT_ENCODED" {
		t.Fatalf("required = %q, want the name inside the base64 body", got)
	}
}

func TestBuildReplayPreflightOmitsEnvironmentCheckWithoutReferences(t *testing.T) {
	script := &dsl.Script{Name: "No env", Version: "1", Protocol: "browser",
		Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "https://app.example.test/"}}}
	preflight := BuildReplayPreflight(script, nil, nil, nil, noProjectSecrets)
	for _, check := range preflight.Checks {
		if check.ID == "environment" {
			t.Fatalf("checks = %+v, want no environment check", preflight.Checks)
		}
	}
}

func TestBuildReplayPreflightReadsEnvironmentInControlConditions(t *testing.T) {
	t.Setenv(engine.EnvScriptEnvAllowlist, "")
	script := &dsl.Script{Name: "Branching", Version: "1", Protocol: "browser", Actions: []dsl.Action{{
		Name: "If", Type: "control_if",
		Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "{{env.APEXLOAD_PREFLIGHT_MODE}}", Op: "eq", Right: "fast"},
			Steps: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "https://app.example.test/"}}},
	}}}
	preflight := BuildReplayPreflight(script, nil, nil, nil, noProjectSecrets)
	if got := strings.Join(preflight.RequiredEnvironment, ","); got != "APEXLOAD_PREFLIGHT_MODE" {
		t.Fatalf("required = %q, want the name in the condition", got)
	}
}

func TestBuildReplayPreflightPreviewDoesNotCallNamesReadable(t *testing.T) {
	t.Setenv(engine.EnvScriptEnvAllowlist, "")
	script := &dsl.Script{Name: "Preview", Version: "1", Protocol: "browser", Actions: []dsl.Action{
		{Name: "Fill", Type: "browser_fill", Selector: "#pw", Target: "#pw", Value: "{{env.APEXLOAD_PREFLIGHT_PW}}"}}}
	preflight := BuildReplayPreflight(script, nil, nil, nil)
	for _, check := range preflight.Checks {
		if check.ID == "environment" {
			if !strings.HasPrefix(check.Detail, "Referenced: APEXLOAD_PREFLIGHT_PW") {
				t.Fatalf("preview check = %+v, want the name reported as referenced, not readable", check)
			}
			return
		}
	}
	t.Fatal("no environment check")
}

// A parallel block's branch steps are scanned like any step: their metadata
// and steps left for review are not.
func TestBuildReplayPreflightScansParallelBranchesLikeSteps(t *testing.T) {
	t.Setenv(engine.EnvScriptEnvAllowlist, "")
	script := &dsl.Script{Name: "Parallel", Version: "1", Protocol: "http", Actions: []dsl.Action{{
		Name: "Both", Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: "a", Steps: []dsl.Action{
			{Name: "get", Type: "request", Method: "GET", Target: "https://api.example.test/{{env.APEXLOAD_PREFLIGHT_BRANCH}}",
				Metadata: map[string]any{"note": "{{env.APEXLOAD_PREFLIGHT_NOTE}}"}},
		}}}},
	}}}
	preflight := BuildReplayPreflight(script, nil, nil, nil, noProjectSecrets)
	if got := strings.Join(preflight.RequiredEnvironment, ","); got != "APEXLOAD_PREFLIGHT_BRANCH" {
		t.Fatalf("required = %q, want the branch step's name and not its metadata's", got)
	}
}
