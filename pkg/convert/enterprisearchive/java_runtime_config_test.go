package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestParseRuntimeCfgMapsCanonicalJavaVMOptionsCaseInsensitively(t *testing.T) {
	runtimeCfg := ParseRuntimeCfg(`[jAvAvM:oPtIoNs]
UsE_JdK=specified_jdk
JDK_HOME=C:\Program Files\Java\jdk-21
JaVa_Vm_PaRaMs=-Xmx512m -Dlabel="hello world" "-Dpath=C:\Program Files\App"
uSe_XbOoT=0
EnAbLe_cL_PeR_VuSeR=1
JaVa_EnV_ClAsSpAtH=C:\fixtures\actions.jar;D:\shared\helpers.jar
`)

	if runtimeCfg.JavaVMJDKSelection != "specified" || runtimeCfg.JavaVMJDKHome != `C:\Program Files\Java\jdk-21` {
		t.Fatalf("JDK selection/home = %q %q", runtimeCfg.JavaVMJDKSelection, runtimeCfg.JavaVMJDKHome)
	}
	wantParameters := []string{"-Xmx512m", "-Dlabel=hello world", `-Dpath=C:\Program Files\App`}
	if !reflect.DeepEqual(runtimeCfg.JavaVMAdditionalParameters, wantParameters) || runtimeCfg.JavaVMParametersParseError != "" {
		t.Fatalf("VM parameters = %#v error=%q, want %#v", runtimeCfg.JavaVMAdditionalParameters, runtimeCfg.JavaVMParametersParseError, wantParameters)
	}
	if runtimeCfg.JavaVMUseXBootClasspath == nil || *runtimeCfg.JavaVMUseXBootClasspath {
		t.Fatalf("UseXBootClasspath = %#v, want explicit false", runtimeCfg.JavaVMUseXBootClasspath)
	}
	if runtimeCfg.JavaVMDedicatedClassLoader == nil || !*runtimeCfg.JavaVMDedicatedClassLoader {
		t.Fatalf("DedicatedClassLoader = %#v, want true", runtimeCfg.JavaVMDedicatedClassLoader)
	}
	wantClasspath := []string{`C:\fixtures\actions.jar`, `D:\shared\helpers.jar`}
	if !reflect.DeepEqual(runtimeCfg.JavaVMClasspath, wantClasspath) {
		t.Fatalf("classpath = %#v, want %#v", runtimeCfg.JavaVMClasspath, wantClasspath)
	}

	scriptConfig := runtimeCfg.ToScriptConfig()
	if scriptConfig.JavaVuser.JDKSelection != "specified" || scriptConfig.JavaVuser.JDKHome != runtimeCfg.JavaVMJDKHome ||
		!reflect.DeepEqual(scriptConfig.JavaVuser.AdditionalVMParameters, wantParameters) ||
		!reflect.DeepEqual(scriptConfig.JavaVuser.Classpath, wantClasspath) {
		t.Fatalf("script Java Vuser config = %#v", scriptConfig.JavaVuser)
	}
	if scriptConfig.JavaVuser.UseXBootClasspath == nil || *scriptConfig.JavaVuser.UseXBootClasspath {
		t.Fatalf("script UseXBootClasspath = %#v, want explicit false", scriptConfig.JavaVuser.UseXBootClasspath)
	}
}

func TestParseRuntimeCfgJavaVMJDKSelectionPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		selection  string
		home       string
		parseError bool
	}{
		{
			name:      "home infers specified without selector",
			content:   "[JavaVM:Options]\njdk_home=/opt/jdk-21\n",
			selection: "specified",
			home:      "/opt/jdk-21",
		},
		{
			name:      "explicit internal retains stale home",
			content:   "[JavaVM:Options]\nuse_jdk=search_jdk\njdk_home=/old/jdk\n",
			selection: "internal",
			home:      "/old/jdk",
		},
		{
			name:       "unknown explicit selector does not infer",
			content:    "[JavaVM:Options]\nuse_jdk=future_mode\njdk_home=/old/jdk\n",
			selection:  "",
			home:       "/old/jdk",
			parseError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtimeCfg := ParseRuntimeCfg(test.content)
			if runtimeCfg.JavaVMJDKSelection != test.selection || runtimeCfg.JavaVMJDKHome != test.home {
				t.Fatalf("selection/home = %q %q, want %q %q", runtimeCfg.JavaVMJDKSelection, runtimeCfg.JavaVMJDKHome, test.selection, test.home)
			}
			if got := runtimeCfg.JavaVMJDKSelectionParseError != ""; got != test.parseError {
				t.Fatalf("JDK selector parse error = %q, want present=%v", runtimeCfg.JavaVMJDKSelectionParseError, test.parseError)
			}
		})
	}
}

func TestParseRuntimeCfgPreservesQuotedWindowsJDKHomeLiterally(t *testing.T) {
	runtimeCfg := ParseRuntimeCfg(`[JavaVM:Options]
use_jdk=specified_jdk
jdk_home="D:\tools\temurin-21"
`)
	if runtimeCfg.JavaVMJDKHome != `D:\tools\temurin-21` {
		t.Fatalf("JDK home = %q, want literal Windows path", runtimeCfg.JavaVMJDKHome)
	}
	if strings.ContainsAny(runtimeCfg.JavaVMJDKHome, "\t\r\n") {
		t.Fatalf("JDK home contains a decoded control character: %q", runtimeCfg.JavaVMJDKHome)
	}
}

func TestParseRuntimeCfgRejectsAmbiguousJavaVMBooleansWithoutDefaulting(t *testing.T) {
	runtimeCfg := ParseRuntimeCfg(`[JavaVM:Options]
Use_Xboot=inherit
enable_CL_per_vuser=maybe
`)
	if runtimeCfg.JavaVMUseXBootClasspath != nil || runtimeCfg.JavaVMDedicatedClassLoader != nil {
		t.Fatalf("invalid booleans became executable values: xboot=%#v dedicated=%#v", runtimeCfg.JavaVMUseXBootClasspath, runtimeCfg.JavaVMDedicatedClassLoader)
	}
	if runtimeCfg.JavaVMUseXBootParseError == "" || runtimeCfg.JavaVMDedicatedParseError == "" {
		t.Fatalf("parse errors = xboot %q dedicated %q, want both", runtimeCfg.JavaVMUseXBootParseError, runtimeCfg.JavaVMDedicatedParseError)
	}
}

func TestFindSectionPrefersExactCanonicalAliasDeterministically(t *testing.T) {
	sections := map[string]map[string]string{
		"jAvAvM:oPtIoNs": {"use_jdk": "search_jdk"},
		"JavaVM:Options": {"use_jdk": "specified_jdk"},
	}
	for index := 0; index < 100; index++ {
		section := findSection(sections, "JavaVM:Options", "JavaVM")
		if section["use_jdk"] != "specified_jdk" {
			t.Fatalf("iteration %d selected %#v, want exact canonical section", index, section)
		}
	}
}

func TestSplitQuotedRuntimeArgumentsDoesNotExpandShellSyntax(t *testing.T) {
	arguments, err := splitQuotedRuntimeArguments(`-Dlabel="hello world" '-Dliteral=$(touch /tmp/nope)' -Dwindows=C:\tools\app -Dunc=\\server\share -Dregex=\\d+ -Dquoted=\"value\"`)
	if err != nil {
		t.Fatalf("splitQuotedRuntimeArguments returned error: %v", err)
	}
	want := []string{"-Dlabel=hello world", "-Dliteral=$(touch /tmp/nope)", `-Dwindows=C:\tools\app`, `-Dunc=\\server\share`, `-Dregex=\\d+`, `-Dquoted="value"`}
	if !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}

	if arguments, err = splitQuotedRuntimeArguments(`-Xmx512m "-Dlabel=unterminated`); err == nil || arguments != nil {
		t.Fatalf("malformed arguments = %#v error=%v, want nil/error", arguments, err)
	}
}

func TestParseRuntimeCfgPreservesSeparatelyQuotedJavaVMArguments(t *testing.T) {
	runtimeCfg := ParseRuntimeCfg(`[JavaVM:Options]
Java_VM_Params="-Done=first value" "-Dtwo=second value"
Java_Env_ClassPath="C:\fixtures\folder;with-semicolon\actions.jar";D:\shared\helpers.jar;C:\O'Brien\fixture.jar
`)
	wantArguments := []string{"-Done=first value", "-Dtwo=second value"}
	if !reflect.DeepEqual(runtimeCfg.JavaVMAdditionalParameters, wantArguments) {
		t.Fatalf("VM arguments = %#v, want %#v", runtimeCfg.JavaVMAdditionalParameters, wantArguments)
	}
	wantClasspath := []string{`C:\fixtures\folder;with-semicolon\actions.jar`, `D:\shared\helpers.jar`, `C:\O'Brien\fixture.jar`}
	if !reflect.DeepEqual(runtimeCfg.JavaVMClasspath, wantClasspath) {
		t.Fatalf("classpath = %#v, want %#v", runtimeCfg.JavaVMClasspath, wantClasspath)
	}
}

func TestMergeJavaVuserRuntimeSettingsTargetsEveryJavaLaneAndPreservesDefaults(t *testing.T) {
	useXBoot := false
	dedicated := true
	httpScript := &dsl.Script{Protocol: "http"}
	javaScript := &dsl.Script{
		Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			HarnessJar:      "runner.jar",
			MaxProcesses:    25,
			ActionTimeoutMs: 30000,
		}},
	}
	result := &ConvertResult{Script: httpScript, Scripts: []*dsl.Script{httpScript, javaScript}}
	source := &dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
		JDKSelection:           "specified",
		JDKHome:                "/opt/jdk-21",
		AdditionalVMParameters: []string{"-Xmx512m", "-Dlabel=hello world"},
		UseXBootClasspath:      &useXBoot,
		DedicatedClassLoader:   &dedicated,
		Classpath:              []string{"/assets/actions.jar"},
	}}

	mergeJavaVuserRuntimeSettingsIntoResult(result, source)

	if httpScript.Config.JavaVuser.JDKSelection != "" {
		t.Fatalf("HTTP script received Java VM settings: %#v", httpScript.Config.JavaVuser)
	}
	got := javaScript.Config.JavaVuser
	if got.HarnessJar != "runner.jar" || got.MaxProcesses != 25 || got.ActionTimeoutMs != 30000 {
		t.Fatalf("runner defaults were clobbered: %#v", got)
	}
	if got.JDKSelection != "specified" || got.JDKHome != "/opt/jdk-21" || !reflect.DeepEqual(got.Classpath, []string{"/assets/actions.jar"}) {
		t.Fatalf("Java VM settings were not merged: %#v", got)
	}
	if got.UseXBootClasspath == nil || *got.UseXBootClasspath || got.DedicatedClassLoader == nil || !*got.DedicatedClassLoader {
		t.Fatalf("presence-aware booleans = xboot %#v dedicated %#v", got.UseXBootClasspath, got.DedicatedClassLoader)
	}
}

func TestConvertZipJavaVuserCarriesJavaVMRuntimeSettings(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	addJavaZipFile(t, writer, "JavaVM/JavaVM.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RuntimeSettings=default.cfg
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, writer, "JavaVM/default.cfg", `[JavaVM:Options]
use_jdk=specified_jdk
jdk_home=/opt/java/jdk-21
Java_VM_Params=-Xmx512m -Dlabel="hello world"
Use_Xboot=0
enable_CL_per_vuser=1
Java_Env_ClassPath=C:\fixtures\actions.jar;D:\shared\helpers.jar
`)
	addJavaZipFile(t, writer, "JavaVM/Actions.java", `public class Actions {
  public int action() throws Throwable { return 0; }
}`)
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(archive.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "java_vuser" {
		t.Fatalf("script = %#v, want java_vuser", result.Script)
	}
	config := result.Script.Config.JavaVuser
	if config.JDKSelection != "specified" || config.JDKHome != "/opt/java/jdk-21" ||
		!reflect.DeepEqual(config.AdditionalVMParameters, []string{"-Xmx512m", "-Dlabel=hello world"}) ||
		!reflect.DeepEqual(config.Classpath, []string{`C:\fixtures\actions.jar`, `D:\shared\helpers.jar`}) {
		t.Fatalf("Java VM config = %#v", config)
	}
	if config.HarnessJar == "" || config.MaxProcesses != 25 || config.ActionTimeoutMs != 30000 {
		t.Fatalf("runner draft defaults were not preserved: %#v", config)
	}
	if config.UseXBootClasspath == nil || *config.UseXBootClasspath || config.DedicatedClassLoader == nil || !*config.DedicatedClassLoader {
		t.Fatalf("presence-aware booleans = xboot %#v dedicated %#v", config.UseXBootClasspath, config.DedicatedClassLoader)
	}
}

func TestConvertZipMalformedJavaVMParametersBlocksReviewWithoutPartialArguments(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	addJavaZipFile(t, writer, "Malformed/JavaVM.usr", `[General]
Type=Java Vuser
RuntimeSettings=default.cfg
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, writer, "Malformed/default.cfg", `[JavaVM:Options]
Java_VM_Params=-Xmx512m "-Dlabel=unterminated
`)
	addJavaZipFile(t, writer, "Malformed/Actions.java", `public class Actions {
  public int action() throws Throwable { return 0; }
}`)
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(archive.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Config.JavaVuser.AdditionalVMParameters) != 0 {
		t.Fatalf("partially imported VM parameters = %#v", result.Script.Config.JavaVuser.AdditionalVMParameters)
	}
	if result.ReviewArtifact == nil || result.ReviewArtifact.Metadata["java_vm_parameters_raw"] != `-Xmx512m "-Dlabel=unterminated` {
		t.Fatalf("review artifact = %#v, want raw malformed value", result.ReviewArtifact)
	}
	blockers := javaReviewArtifactBlockers(result, javaSourceIntegrityKey)
	if !stringSliceContains(blockers, javaVMParametersReviewBlocker) {
		t.Fatalf("source integrity blockers = %#v", blockers)
	}
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, want blocked", result.FidelityReport)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "malformed quoting") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestConvertZipMalformedJavaVMClasspathBlocksReviewWithoutPartialEntries(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	addJavaZipFile(t, writer, "MalformedClasspath/JavaVM.usr", `[General]
Type=Java Vuser
RuntimeSettings=default.cfg
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, writer, "MalformedClasspath/default.cfg", `[JavaVM:Options]
Java_Env_ClassPath="C:\fixtures\unterminated;D:\shared\helpers.jar
`)
	addJavaZipFile(t, writer, "MalformedClasspath/Actions.java", `public class Actions {
  public int action() throws Throwable { return 0; }
}`)
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(archive.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if len(result.Script.Config.JavaVuser.Classpath) != 0 {
		t.Fatalf("partially imported classpath = %#v", result.Script.Config.JavaVuser.Classpath)
	}
	if result.ReviewArtifact == nil || result.ReviewArtifact.Metadata["java_vm_classpath_raw"] != `"C:\fixtures\unterminated;D:\shared\helpers.jar` {
		t.Fatalf("review artifact = %#v, want raw malformed classpath", result.ReviewArtifact)
	}
	if blockers := javaReviewArtifactBlockers(result, javaSourceIntegrityKey); !stringSliceContains(blockers, javaVMClasspathReviewBlocker) {
		t.Fatalf("source integrity blockers = %#v", blockers)
	}
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, want blocked", result.FidelityReport)
	}
}

func TestConvertZipAmbiguousJavaVMOptionsRemainReviewBlocked(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	addJavaZipFile(t, writer, "Ambiguous/JavaVM.usr", `[General]
Type=Java Vuser
RuntimeSettings=default.cfg
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, writer, "Ambiguous/default.cfg", `[JavaVM:Options]
use_jdk=future_mode
jdk_home=/opt/old-jdk
Use_Xboot=inherit
enable_CL_per_vuser=maybe
`)
	addJavaZipFile(t, writer, "Ambiguous/Actions.java", `public class Actions {
  public int action() throws Throwable { return 0; }
}`)
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(archive.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	config := result.Script.Config.JavaVuser
	if config.JDKSelection != "" || config.UseXBootClasspath != nil || config.DedicatedClassLoader != nil {
		t.Fatalf("ambiguous source values became executable settings: %#v", config)
	}
	if result.ReviewArtifact == nil {
		t.Fatal("review artifact is nil")
	}
	for key, want := range map[string]string{
		"java_vm_jdk_selection_raw":          "future_mode",
		"java_vm_use_xbootclasspath_raw":     "inherit",
		"java_vm_dedicated_class_loader_raw": "maybe",
	} {
		if got := result.ReviewArtifact.Metadata[key]; got != want {
			t.Fatalf("review metadata %s = %#v, want %q", key, got, want)
		}
	}
	blockers := javaReviewArtifactBlockers(result, javaSourceIntegrityKey)
	if !stringSliceContains(blockers, javaVMOptionsReviewBlocker) {
		t.Fatalf("source integrity blockers = %#v", blockers)
	}
	if result.FidelityReport == nil || result.FidelityReport.PromotionStatus != ImportPromotionBlocked {
		t.Fatalf("fidelity report = %#v, want blocked", result.FidelityReport)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "unsupported selector or invalid boolean") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestJavaVMRuntimeSupportDefinitionsRemainProtocolScoped(t *testing.T) {
	runtimeSections := map[string]map[string]map[string]string{
		"default.cfg": ParseConfigSections(`[JavaVM:Options]
use_jdk=specified_jdk
jdk_home=/opt/jdk-21
Java_VM_Params=-Xmx512m
Use_Xboot=1
enable_CL_per_vuser=0
Java_Env_ClassPath=C:\fixtures\actions.jar
`),
	}
	parity := buildRTSParityReportForProtocol(runtimeSections, "java_vuser")
	wantStatuses := map[string]RuntimeSettingStatus{
		"java_vm_jdk":                    RuntimeSettingStatusExecuted,
		"java_vm_parameters":             RuntimeSettingStatusExecuted,
		"java_vm_classpath":              RuntimeSettingStatusExecuted,
		"java_vm_dedicated_class_loader": RuntimeSettingStatusExecuted,
		"java_vm_xbootclasspath":         RuntimeSettingStatusCompatibilityOnly,
	}
	for id, wantStatus := range wantStatuses {
		found := false
		for _, entry := range parity {
			if entry.ID == id && entry.Status == wantStatus {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("parity = %#v, missing %s/%s", parity, id, wantStatus)
		}
	}

	for _, entry := range GetRTSSupportMatrix() {
		if strings.HasPrefix(entry.ID, "java_vm_") {
			t.Fatalf("Web RTS matrix contains Java-only entry: %#v", entry)
		}
	}
	for _, entry := range BuildRTSParityReport(runtimeSections) {
		if strings.HasPrefix(entry.ID, "java_vm_") {
			t.Fatalf("Web source parity report contains Java-only entry: %#v", entry)
		}
	}
	if got := len(GetJavaVuserRTSSupportMatrix()); got != len(wantStatuses) {
		t.Fatalf("Java Vuser RTS matrix count = %d, want %d", got, len(wantStatuses))
	}

	useXBoot := false
	javaScript := &dsl.Script{Protocol: "java_vuser", Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{UseXBootClasspath: &useXBoot}}}
	if parity := BuildRTSParityReportFromScript(javaScript); len(parity) != 1 || parity[0].ID != "java_vm_xbootclasspath" {
		t.Fatalf("Java script parity = %#v", parity)
	}
	httpScript := &dsl.Script{Protocol: "http", Config: javaScript.Config}
	if parity := BuildRTSParityReportFromScript(httpScript); len(parity) != 0 {
		t.Fatalf("HTTP script received Java-only parity = %#v", parity)
	}

	if _, ok := javaSurfaceSupportByID(JavaImportSurfaceCoverage(), "java-vm-runtime-settings"); !ok {
		t.Fatal("Java import surface coverage missing Java VM runtime settings")
	}
}

func TestAttachSourceMetadataScopesJavaVMParityToJavaVuserScripts(t *testing.T) {
	files := map[string]string{
		"default.cfg": `[JavaVM:Options]
Use_Xboot=1
`,
	}
	httpScript := &dsl.Script{Protocol: "http"}
	javaScript := &dsl.Script{Protocol: "java_vuser"}
	for _, script := range []*dsl.Script{httpScript, javaScript} {
		attachSourceMetadata(script, nil, "", nil, nil, files, "default.cfg", nil, nil, nil)
	}

	httpPreserved := httpScript.Metadata.Import.Preserved
	if parity, exists := httpPreserved["runtime_settings_parity"]; exists {
		t.Fatalf("HTTP script received Java-only source parity: %#v", parity)
	}
	javaPreserved := javaScript.Metadata.Import.Preserved
	parity, ok := javaPreserved["runtime_settings_parity"].([]RuntimeSettingParityEntry)
	if !ok || len(parity) != 1 || parity[0].ID != "java_vm_xbootclasspath" {
		t.Fatalf("Java script parity = %#v, want scoped Xboot entry", javaPreserved["runtime_settings_parity"])
	}
}

func TestParseRuntimeCfgJavaVMKeyAliases(t *testing.T) {
	tests := []struct {
		name    string
		content string
		assert  func(t *testing.T, rc *RuntimeConfig)
	}{
		{
			name:    "jdk_selection selector alias",
			content: "[JavaVM:Options]\njdk_selection=specified_jdk\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMJDKSelection != "specified" {
					t.Fatalf("selection = %q", rc.JavaVMJDKSelection)
				}
			},
		},
		{
			name:    "UseSpecifiedJdk selector alias",
			content: "[JavaVM:Options]\nUseSpecifiedJdk=1\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMJDKSelection != "specified" {
					t.Fatalf("selection = %q", rc.JavaVMJDKSelection)
				}
			},
		},
		{
			name:    "JdkPath home alias",
			content: "[JavaVM:Options]\nJdkPath=/opt/jdk-17\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMJDKHome != "/opt/jdk-17" {
					t.Fatalf("home = %q", rc.JavaVMJDKHome)
				}
			},
		},
		{
			name:    "JavaVmJdkPath home alias",
			content: "[JavaVM:Options]\nJavaVmJdkPath=/opt/jdk-18\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMJDKHome != "/opt/jdk-18" {
					t.Fatalf("home = %q", rc.JavaVMJDKHome)
				}
			},
		},
		{
			name:    "UserJdkPath home alias",
			content: "[JavaVM:Options]\nUserJdkPath=/opt/jdk-19\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMJDKHome != "/opt/jdk-19" {
					t.Fatalf("home = %q", rc.JavaVMJDKHome)
				}
			},
		},
		{
			name:    "JdkHome home alias",
			content: "[JavaVM:Options]\nJdkHome=/opt/jdk-20\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMJDKHome != "/opt/jdk-20" {
					t.Fatalf("home = %q", rc.JavaVMJDKHome)
				}
			},
		},
		{
			name:    "java_vm_parameters params alias",
			content: "[JavaVM:Options]\njava_vm_parameters=-Xmx256m\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if !reflect.DeepEqual(rc.JavaVMAdditionalParameters, []string{"-Xmx256m"}) {
					t.Fatalf("parameters = %#v", rc.JavaVMAdditionalParameters)
				}
			},
		},
		{
			name:    "AdditionalVmParams params alias",
			content: "[JavaVM:Options]\nAdditionalVmParams=-Xms64m\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if !reflect.DeepEqual(rc.JavaVMAdditionalParameters, []string{"-Xms64m"}) {
					t.Fatalf("parameters = %#v", rc.JavaVMAdditionalParameters)
				}
			},
		},
		{
			name:    "ExtraVmParams params alias",
			content: "[JavaVM:Options]\nExtraVmParams=-Denv=perf\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if !reflect.DeepEqual(rc.JavaVMAdditionalParameters, []string{"-Denv=perf"}) {
					t.Fatalf("parameters = %#v", rc.JavaVMAdditionalParameters)
				}
			},
		},
		{
			name:    "VmParams params alias",
			content: "[JavaVM:Options]\nVmParams=-verbose:gc\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if !reflect.DeepEqual(rc.JavaVMAdditionalParameters, []string{"-verbose:gc"}) {
					t.Fatalf("parameters = %#v", rc.JavaVMAdditionalParameters)
				}
			},
		},
		{
			name:    "UseXbootClasspath xboot alias",
			content: "[JavaVM:Options]\nUseXbootClasspath=1\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMUseXBootClasspath == nil || !*rc.JavaVMUseXBootClasspath {
					t.Fatalf("xboot = %#v", rc.JavaVMUseXBootClasspath)
				}
			},
		},
		{
			name:    "enable_classloader_per_vuser loader alias",
			content: "[JavaVM:Options]\nenable_classloader_per_vuser=1\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMDedicatedClassLoader == nil || !*rc.JavaVMDedicatedClassLoader {
					t.Fatalf("dedicated = %#v", rc.JavaVMDedicatedClassLoader)
				}
			},
		},
		{
			name:    "DedicatedClassLoader loader alias",
			content: "[JavaVM:Options]\nDedicatedClassLoader=true\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMDedicatedClassLoader == nil || !*rc.JavaVMDedicatedClassLoader {
					t.Fatalf("dedicated = %#v", rc.JavaVMDedicatedClassLoader)
				}
			},
		},
		{
			name:    "ClassLoaderPerVuser loader alias",
			content: "[JavaVM:Options]\nClassLoaderPerVuser=0\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				if rc.JavaVMDedicatedClassLoader == nil || *rc.JavaVMDedicatedClassLoader {
					t.Fatalf("dedicated = %#v, want explicit false", rc.JavaVMDedicatedClassLoader)
				}
			},
		},
		{
			name:    "java_env_class_paths classpath alias",
			content: "[JavaVM:Options]\njava_env_class_paths=/fixtures/actions.jar;/shared/helpers.jar\n",
			assert: func(t *testing.T, rc *RuntimeConfig) {
				want := []string{"/fixtures/actions.jar", "/shared/helpers.jar"}
				if !reflect.DeepEqual(rc.JavaVMClasspath, want) {
					t.Fatalf("classpath = %#v", rc.JavaVMClasspath)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.assert(t, ParseRuntimeCfg(test.content))
		})
	}
}

func TestParseRuntimeCfgJavaVMSectionAliases(t *testing.T) {
	body := "use_jdk=specified_jdk\njdk_home=/opt/jdk-21\nJava_VM_Params=-Xmx512m\n"
	for _, sectionName := range []string{"JavaVM", "Java VM", "Java Environment Settings"} {
		t.Run(sectionName, func(t *testing.T) {
			runtimeCfg := ParseRuntimeCfg("[" + sectionName + "]\n" + body)
			if runtimeCfg.JavaVMJDKSelection != "specified" || runtimeCfg.JavaVMJDKHome != "/opt/jdk-21" {
				t.Fatalf("selection/home = %q %q", runtimeCfg.JavaVMJDKSelection, runtimeCfg.JavaVMJDKHome)
			}
			if !reflect.DeepEqual(runtimeCfg.JavaVMAdditionalParameters, []string{"-Xmx512m"}) {
				t.Fatalf("parameters = %#v", runtimeCfg.JavaVMAdditionalParameters)
			}
		})
	}
}

func TestParseRuntimeCfgWithoutJavaVMSectionLeavesJavaVuserConfigZero(t *testing.T) {
	runtimeCfg := ParseRuntimeCfg(`[HTTP]
KeepAlive=1

[Think Time]
Options=NOTHINK
`)
	if runtimeCfg.JavaVMJDKSelection != "" || runtimeCfg.JavaVMJDKHome != "" ||
		runtimeCfg.JavaVMAdditionalParameters != nil || runtimeCfg.JavaVMUseXBootClasspath != nil ||
		runtimeCfg.JavaVMDedicatedClassLoader != nil || runtimeCfg.JavaVMClasspath != nil {
		t.Fatalf("Java VM fields parsed from a cfg without a Java VM section: %+v", runtimeCfg)
	}

	scriptConfig := runtimeCfg.ToScriptConfig()
	if !reflect.DeepEqual(scriptConfig.JavaVuser, dsl.JavaVuserConfig{}) {
		t.Fatalf("JavaVuser config = %#v, want zero value", scriptConfig.JavaVuser)
	}
}

func TestParseRuntimeCfgJavaVMFirstAliasWinsWithinOneSetting(t *testing.T) {
	// use_jdk is the first selector alias, so it must win even when a later
	// alias (UseSpecifiedJdk) or a case variant is also present.
	runtimeCfg := ParseRuntimeCfg(`[JavaVM:Options]
UseSpecifiedJdk=specified_jdk
use_jdk=search_jdk
`)
	if runtimeCfg.JavaVMJDKSelection != "internal" {
		t.Fatalf("selection = %q, want first alias (use_jdk=search_jdk) to win", runtimeCfg.JavaVMJDKSelection)
	}

	// A case variant of the first alias still beats an exact later alias.
	runtimeCfg = ParseRuntimeCfg(`[JavaVM:Options]
jdk_selection=specified_jdk
USE_JDK=search_jdk
`)
	if runtimeCfg.JavaVMJDKSelection != "internal" {
		t.Fatalf("selection = %q, want case-variant first alias to win", runtimeCfg.JavaVMJDKSelection)
	}
}
