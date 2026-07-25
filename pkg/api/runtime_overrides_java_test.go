package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Scenario-level Java Env overrides: field-merge semantics, group-over-root
// precedence, deep cloning, the operator-field governance boundary, and the
// denylist re-validation that runs on the post-override script.

func TestApplyRuntimeOverridesJavaVuserFieldMergePreservesImportedValues(t *testing.T) {
	skip := true
	script := &dsl.Script{Protocol: "java_vuser"}
	script.Config.JavaVuser = dsl.JavaVuserConfig{
		HarnessJar:             "runner.jar",
		MaxProcesses:           25,
		JDKSelection:           "specified",
		JDKHome:                "/opt/jdk-21",
		AdditionalVMParameters: []string{"-Xmx512m"},
		Classpath:              []string{"/assets/imported.jar"},
	}
	o := &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{
		SkipParameterEvaluation: &skip,
	}}

	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := script.Config.JavaVuser
	if got.SkipParameterEvaluation == nil || !*got.SkipParameterEvaluation {
		t.Fatalf("skip flag not applied: %#v", got)
	}
	// A single-field override must not wipe imported script-owned values.
	if got.JDKSelection != "specified" || got.JDKHome != "/opt/jdk-21" ||
		!reflect.DeepEqual(got.AdditionalVMParameters, []string{"-Xmx512m"}) ||
		!reflect.DeepEqual(got.Classpath, []string{"/assets/imported.jar"}) {
		t.Fatalf("field-merge clobbered imported values: %#v", got)
	}
	// Operator-owned fields have no override counterpart and stay untouched.
	if got.HarnessJar != "runner.jar" || got.MaxProcesses != 25 {
		t.Fatalf("operator fields changed: %#v", got)
	}
}

func TestApplyRuntimeOverridesJavaVuserReplacesListsAsUnits(t *testing.T) {
	script := &dsl.Script{Protocol: "java_vuser"}
	script.Config.JavaVuser = dsl.JavaVuserConfig{
		AdditionalVMParameters: []string{"-Xmx512m", "-Denv=old"},
		Classpath:              []string{"/old/app.jar"},
	}
	o := &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{
		AdditionalVMParameters: []string{"-Xmx1g"},
		Classpath:              []string{"/new/app.jar", "/new/lib.jar"},
	}}

	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := script.Config.JavaVuser
	if !reflect.DeepEqual(got.AdditionalVMParameters, []string{"-Xmx1g"}) ||
		!reflect.DeepEqual(got.Classpath, []string{"/new/app.jar", "/new/lib.jar"}) {
		t.Fatalf("lists not replaced as units: %#v", got)
	}
}

func TestMergeRuntimeSettingsJavaVuserGroupWinsPerField(t *testing.T) {
	rootXBoot := true
	groupSkip := true
	base := &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{
		JDKSelection:           "specified",
		JDKHome:                "/root/jdk",
		AdditionalVMParameters: []string{"-Xmx256m"},
		UseXBootClasspath:      &rootXBoot,
	}}
	group := &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{
		JDKHome:                 "/group/jdk",
		SkipParameterEvaluation: &groupSkip,
	}}

	merged := mergeRuntimeSettings(base, group)
	got := merged.JavaVuser
	if got == nil {
		t.Fatal("merged java_vuser section is nil")
	}
	if got.JDKHome != "/group/jdk" || got.SkipParameterEvaluation == nil || !*got.SkipParameterEvaluation {
		t.Fatalf("group fields did not win: %#v", got)
	}
	if got.JDKSelection != "specified" || !reflect.DeepEqual(got.AdditionalVMParameters, []string{"-Xmx256m"}) ||
		got.UseXBootClasspath == nil || !*got.UseXBootClasspath {
		t.Fatalf("root fields not retained: %#v", got)
	}
	if base.JavaVuser.JDKHome != "/root/jdk" || base.JavaVuser.SkipParameterEvaluation != nil {
		t.Fatalf("merge mutated base: %#v", base.JavaVuser)
	}
}

func TestMergeRuntimeSettingsJavaVuserGroupOnly(t *testing.T) {
	group := &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{JDKSelection: "internal"}}
	merged := mergeRuntimeSettings(&RuntimeSettingsOverride{}, group)
	if merged.JavaVuser == nil || merged.JavaVuser.JDKSelection != "internal" {
		t.Fatalf("group-only java_vuser section lost: %#v", merged.JavaVuser)
	}
}

func TestCloneRuntimeSettingsDeepClonesJavaVuser(t *testing.T) {
	dedicated := true
	o := &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{
		AdditionalVMParameters: []string{"-Xmx512m"},
		Classpath:              []string{"/a.jar"},
		DedicatedClassLoader:   &dedicated,
	}}
	clone := cloneRuntimeSettings(o)
	clone.JavaVuser.AdditionalVMParameters[0] = "MUT"
	clone.JavaVuser.Classpath[0] = "MUT"
	*clone.JavaVuser.DedicatedClassLoader = false
	if o.JavaVuser.AdditionalVMParameters[0] != "-Xmx512m" || o.JavaVuser.Classpath[0] != "/a.jar" || !*o.JavaVuser.DedicatedClassLoader {
		t.Fatalf("clone aliases the original java_vuser section: %#v", o.JavaVuser)
	}
}

func TestRuntimeJavaVuserOverrideCannotSmuggleOperatorFields(t *testing.T) {
	// The override struct is closed: operator-owned runner fields present in a
	// request payload are dropped by decoding and cannot reach the script.
	payload := `{"jdk_home":"/opt/jdk-21","runner_command":"/evil/runner","harness_jar":"/evil.jar","work_dir":"/evil","max_processes":999,"enabled":true}`
	var o RuntimeJavaVuserOverride
	if err := json.Unmarshal([]byte(payload), &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	script := &dsl.Script{Protocol: "java_vuser"}
	script.Config.JavaVuser = dsl.JavaVuserConfig{RunnerCommand: "/operator/runner", HarnessJar: "runner.jar", WorkDir: "/work", MaxProcesses: 25}
	if err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{JavaVuser: &o}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := script.Config.JavaVuser
	if got.RunnerCommand != "/operator/runner" || got.HarnessJar != "runner.jar" || got.WorkDir != "/work" || got.MaxProcesses != 25 || got.Enabled {
		t.Fatalf("operator fields were overridden: %#v", got)
	}
	if got.JDKHome != "/opt/jdk-21" {
		t.Fatalf("legitimate field not applied: %#v", got)
	}
	// And the marshaled contract itself exposes no operator keys.
	data, err := json.Marshal(&RuntimeJavaVuserOverride{JDKSelection: "internal"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"runner_command", "harness_jar", "work_dir", "max_processes", "enabled"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("override contract leaks operator key %q: %s", key, data)
		}
	}
}

func TestJavaVuserOverrideChangedExecutableSurface(t *testing.T) {
	skip := true
	base := &dsl.Script{
		Protocol: "java_vuser",
		Actions:  []dsl.Action{{Name: "Actions.action", Type: "java_vuser_run", Params: map[string]any{"method": "action"}}},
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			JDKSelection:           "specified",
			JDKHome:                "/opt/jdk-21",
			Classpath:              []string{"/opt/app.jar"},
			AdditionalVMParameters: []string{"-Xmx512m"},
		}},
	}

	// Runtime-safe divergence: skip flag + pacing changes, code surface intact.
	safe := cloneScriptForStoredGroup(base)
	safe.Config.JavaVuser.SkipParameterEvaluation = &skip
	safe.Config.Pacing = dsl.PacingConfig{Type: "fixed_delay", DelayMs: 250}
	if javaVuserOverrideChangedExecutableSurface(base, safe) {
		t.Fatal("runtime-safe divergence must not be flagged as an executable-surface change")
	}

	// Classpath change → flagged.
	cp := cloneScriptForStoredGroup(base)
	cp.Config.JavaVuser.Classpath = []string{"/tmp/attacker.jar", "/opt/app.jar"}
	if !javaVuserOverrideChangedExecutableSurface(base, cp) {
		t.Fatal("classpath change must be flagged")
	}

	// JDK home change → flagged.
	jdk := cloneScriptForStoredGroup(base)
	jdk.Config.JavaVuser.JDKHome = "/opt/other-jdk"
	if !javaVuserOverrideChangedExecutableSurface(base, jdk) {
		t.Fatal("JDK home change must be flagged")
	}

	// VM parameters change → flagged.
	vm := cloneScriptForStoredGroup(base)
	vm.Config.JavaVuser.AdditionalVMParameters = []string{"-Xmx2g"}
	if !javaVuserOverrideChangedExecutableSurface(base, vm) {
		t.Fatal("VM parameter change must be flagged")
	}

	// Action rewrite (the {{env}}-template vector: class/method/source ref) → flagged.
	act := cloneScriptForStoredGroup(base)
	act.Actions[0].Params["method"] = "attacker_entrypoint"
	if !javaVuserOverrideChangedExecutableSurface(base, act) {
		t.Fatal("action rewrite must be flagged")
	}

	// Attaching a classpath JAR changes the bytes loaded into the JVM → flagged.
	withAsset := cloneScriptForStoredGroup(base)
	withAsset.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{{
		Entry:      "/opt/app.jar",
		SHA256:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		StorageRef: "file:///assets/app.jar",
	}}
	if !javaVuserOverrideChangedExecutableSurface(base, withAsset) {
		t.Fatal("attaching a classpath JAR must be flagged")
	}

	// Swapping the attached JAR (different sha256) → flagged.
	swapped := cloneScriptForStoredGroup(withAsset)
	swapped.Config.JavaVuser.ClasspathAssets = []dsl.JavaClasspathAsset{{
		Entry:      "/opt/app.jar",
		SHA256:     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		StorageRef: "file:///assets/app.jar",
	}}
	if !javaVuserOverrideChangedExecutableSurface(withAsset, swapped) {
		t.Fatal("swapping the attached JAR must be flagged")
	}
}

func TestApplyRuntimeOverridesJavaVuserDenylistedVMParameterFailsValidation(t *testing.T) {
	// Mirrors the create-test sequence: applyRuntimeOverrides then
	// dsl.ValidateScriptIssues — a denylisted VM parameter supplied via a
	// scenario override must fail validation, not reach execution.
	script := &dsl.Script{Name: "j", Protocol: "java_vuser", Actions: []dsl.Action{{
		Name: "Actions.action", Type: "java_vuser_run", Params: map[string]any{"method": "action"},
	}}}
	o := &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{
		AdditionalVMParameters: []string{"-javaagent:/tmp/evil.jar"},
	}}
	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	issues := dsl.ValidateScriptIssues(script)
	found := false
	for _, issue := range issues {
		if strings.Contains(issue, "additional_vm_parameters") {
			found = true
		}
	}
	if !found {
		t.Fatalf("denylisted VM parameter passed validation: %#v", issues)
	}
}
