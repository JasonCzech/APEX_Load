package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
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

func TestRuntimeSettingsFromScriptConfigIncludesClosedJavaVuserLayer(t *testing.T) {
	useXBoot := true
	config := dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
		Enabled:                true,
		RunnerCommand:          "/operator/runner",
		HarnessJar:             "/operator/harness.jar",
		MaxProcesses:           20,
		JDKSelection:           "specified",
		JDKHome:                "/opt/jdk-21",
		AdditionalVMParameters: []string{"-Xmx512m"},
		Classpath:              []string{"/opt/app.jar"},
		UseXBootClasspath:      &useXBoot,
	}}

	got := runtimeSettingsFromScriptConfig(config)
	if got == nil || got.JavaVuser == nil {
		t.Fatal("script Java Vuser layer was omitted from runtime projection")
	}
	if got.JavaVuser.JDKSelection != "specified" || got.JavaVuser.JDKHome != "/opt/jdk-21" ||
		!reflect.DeepEqual(got.JavaVuser.AdditionalVMParameters, []string{"-Xmx512m"}) ||
		!reflect.DeepEqual(got.JavaVuser.Classpath, []string{"/opt/app.jar"}) ||
		got.JavaVuser.UseXBootClasspath == nil || !*got.JavaVuser.UseXBootClasspath {
		t.Fatalf("Java Vuser runtime projection = %#v", got.JavaVuser)
	}
	data, err := json.Marshal(got.JavaVuser)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"runner_command", "harness_jar", "max_processes", "enabled"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("runtime projection exposed operator field %q: %s", forbidden, data)
		}
	}
}

func TestMaskRuntimeSettingsRedactsAndReconcilesJavaVMArguments(t *testing.T) {
	const credential = "java-runtime-secret-value-3819"
	prior := &RuntimeSettingsOverride{JavaVuser: &RuntimeJavaVuserOverride{
		JDKSelection:           "internal",
		AdditionalVMParameters: []string{"-Xmx512m", "-Dservice.password=" + credential},
	}}

	masked := maskRuntimeSettings(prior)
	if got := masked.JavaVuser.AdditionalVMParameters; len(got) != 2 ||
		got[0] != "-Xmx512m" || got[1] != scrub.Redacted {
		t.Fatalf("masked Java VM parameters = %#v", got)
	}
	if strings.Contains(mustMarshalJSON(t, masked), credential) {
		t.Fatal("masked runtime settings leaked a Java VM credential")
	}
	masked.JavaVuser.JDKSelection = "specified"
	reconciled := reconcileGroupRuntimeSecrets(masked, prior)
	if got := reconciled.JavaVuser.AdditionalVMParameters[1]; got != "-Dservice.password="+credential {
		t.Fatalf("masked Java VM argument was not restored: %q", got)
	}
	if reconciled.JavaVuser.JDKSelection != "specified" {
		t.Fatalf("sibling Java setting edit was lost: %#v", reconciled.JavaVuser)
	}
}

func TestReconcileJavaVuserRuntimeSecretsRequiresSensitiveSameIndex(t *testing.T) {
	const (
		databaseSecret = "-Ddatabase.password=java-runtime-database-secret-3819"
		serviceSecret  = "-Dservice.token=java-runtime-service-secret-7426"
	)
	tests := []struct {
		name     string
		prior    []string
		incoming []string
		want     []string
	}{
		{
			name:     "reordered sentinel stays unresolved",
			prior:    []string{databaseSecret, "-Xmx512m"},
			incoming: []string{"-Xmx512m", scrub.Redacted},
			want:     []string{"-Xmx512m", scrub.Redacted},
		},
		{
			name:     "insertion before sentinel stays unresolved",
			prior:    []string{databaseSecret, "-Xmx512m"},
			incoming: []string{"-Xms128m", scrub.Redacted, "-Xmx512m"},
			want:     []string{"-Xms128m", scrub.Redacted, "-Xmx512m"},
		},
		{
			name:     "deletion before sentinel stays unresolved",
			prior:    []string{"-Xms128m", databaseSecret, "-Xmx512m"},
			incoming: []string{scrub.Redacted, "-Xmx512m"},
			want:     []string{scrub.Redacted, "-Xmx512m"},
		},
		{
			name:     "multiple aligned sensitive arguments restore independently",
			prior:    []string{databaseSecret, serviceSecret, "-Xmx512m"},
			incoming: []string{scrub.Redacted, scrub.Redacted, "-Xmx512m"},
			want:     []string{databaseSecret, serviceSecret, "-Xmx512m"},
		},
		{
			name:     "multiple sensitive arguments fail closed after middle deletion",
			prior:    []string{databaseSecret, "-Xms128m", serviceSecret},
			incoming: []string{scrub.Redacted, scrub.Redacted},
			want:     []string{scrub.Redacted, scrub.Redacted},
		},
		{
			name:     "adjacent sensitive arguments fail closed after deletion",
			prior:    []string{databaseSecret, serviceSecret},
			incoming: []string{scrub.Redacted},
			want:     []string{scrub.Redacted},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			incoming := &RuntimeJavaVuserOverride{
				AdditionalVMParameters: append([]string(nil), tt.incoming...),
			}
			prior := &RuntimeJavaVuserOverride{
				AdditionalVMParameters: append([]string(nil), tt.prior...),
			}

			reconcileJavaVuserRuntimeSecrets(incoming, prior)

			if !reflect.DeepEqual(incoming.AdditionalVMParameters, tt.want) {
				t.Fatalf("reconciled Java VM parameters = %#v, want %#v", incoming.AdditionalVMParameters, tt.want)
			}
		})
	}
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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

	// Shared JVMs stay re-blessable; moving into LoadRunner thread mode, where
	// libraries and native code share a JVM, needs the script approved again.
	shared := cloneScriptForStoredGroup(base)
	shared.Config.JavaVuser.JVMSharing = dsl.JavaVuserJVMSharingShared
	if javaVuserOverrideChangedExecutableSurface(base, shared) {
		t.Fatal("a shared-JVM override must stay re-blessable")
	}
	threads := cloneScriptForStoredGroup(base)
	threads.Config.JavaVuser.JVMSharing = dsl.JavaVuserJVMSharingThreads
	if !javaVuserOverrideChangedExecutableSurface(base, threads) {
		t.Fatal("an override into thread mode must be flagged")
	}
	if javaVuserOverrideChangedExecutableSurface(threads, shared) || javaVuserOverrideChangedExecutableSurface(threads, threads) {
		t.Fatal("leaving or keeping an approved thread mode must not be flagged")
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
	err := applyRuntimeOverrides(script, o)
	if err == nil || !strings.Contains(err.Error(), "additional_vm_parameters") {
		t.Fatalf("denylisted VM parameter apply error = %v", err)
	}
}
