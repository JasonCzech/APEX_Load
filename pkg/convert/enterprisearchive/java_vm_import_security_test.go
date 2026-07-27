package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestValidateImportedJavaVMParametersAllowsSafeOpaqueValuesWithoutNormalization(t *testing.T) {
	parameters := []string{
		"-Xmx512m",
		`-Dconfig={"label": "hello"}`,
		"-Dlabel=hello world",
		"-Dendpoint=https://example.test/safe/path?view=full",
	}
	if issues := validateImportedJavaVMParameters(parameters); len(issues) != 0 {
		t.Fatalf("safe Java VM parameters rejected: %#v", issues)
	}
	if got := executableJavaVMParameters(parameters); len(got) != len(parameters) {
		t.Fatalf("safe Java VM parameters = %#v, want exact list", got)
	} else {
		for index := range parameters {
			if got[index] != parameters[index] {
				t.Fatalf("safe parameter %d = %q, want exact %q", index, got[index], parameters[index])
			}
		}
	}
}

func TestParseRuntimeCfgRejectsUnsafeJavaVMArgumentsWithoutPartialSiblings(t *testing.T) {
	const (
		passwordCanary = "short-secret"
		apiKeyCanary   = "api-key-canary"
		privateCanary  = "private-key-canary"
		entropyCanary  = "q7W9pL2xN8vC4mR6tY1kH5sF3dJ0aB9uE2zG"
		urlCanary      = "userinfo-password-canary"
	)
	tests := []struct {
		name    string
		content string
		canary  string
	}{
		{
			name: "password property",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Dservice.password=` + passwordCanary,
			canary: passwordCanary,
		},
		{
			name: "api key in numbered alias row",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m
AdditionalVmParams_extra_arg_1=-Dapi.key=` + apiKeyCanary,
			canary: apiKeyCanary,
		},
		{
			name: "private key property",
			content: `[Java:Options]
AdditionalVmParams=-Dprivate_key=` + privateCanary,
			canary: privateCanary,
		},
		{
			name: "neutral high entropy property",
			content: `[JavaVM]
VmParams=-Dlabel=` + entropyCanary,
			canary: entropyCanary,
		},
		{
			name: "neutral credential URL property",
			content: `[Java Environment Settings]
ExtraVmParams="-Dendpoint=https://runner:` + urlCanary + `@example.test/service"`,
			canary: urlCanary,
		},
		{
			name: "neutral credential URL fragment",
			content: `[JavaVM:Options]
Java_VM_Params=-Dendpoint=https://example.test/service#token=` + urlCanary,
			canary: urlCanary,
		},
		{
			name: "unsafe process option",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -javaagent:/tmp/untrusted-agent.jar`,
			canary: "untrusted-agent.jar",
		},
		{
			name: "legacy profiler",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -prof:/tmp/untrusted-profile`,
			canary: "untrusted-profile",
		},
		{
			name: "legacy alternate JRE selection",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -VeRsIoN:1.8+`,
		},
		{
			name: "full version terminal option",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -FuLlVeRsIoN`,
		},
		{
			name: "OpenJ9 options file",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Xoptionsfile=/tmp/untrusted.options`,
			canary: "untrusted.options",
		},
		{
			name: "OpenJ9 attach override",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Dcom.ibm.tools.attach.enable=yes`,
		},
		{
			name: "OpenJ9 unknown option policy override",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:-IgnoreUnrecognizedXXColonOptions`,
		},
		{
			name: "OpenJ9 dump hook",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Xdump:tool:events=vmstart,exec=/tmp/untrusted-command`,
			canary: "untrusted-command",
		},
		{
			name: "OpenJ9 trace properties",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Xtrace:properties=/tmp/untrusted-trace-options`,
			canary: "untrusted-trace-options",
		},
		{
			name: "OpenJ9 verbose GC log",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Xverbosegclog:/tmp/untrusted-gc-log`,
			canary: "untrusted-gc-log",
		},
		{
			name: "OpenJ9 JIT log",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Xjit:vlog=/tmp/untrusted-jit-log`,
			canary: "untrusted-jit-log",
		},
		{
			name: "heap dump on OOM",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+HeapDumpOnOutOfMemoryError`,
		},
		{
			name: "heap dump before full GC",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+HeapDumpBeforeFullGC`,
		},
		{
			name: "heap dump after full GC",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+HeapDumpAfterFullGC`,
		},
		{
			name: "crash on OOM",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+CrashOnOutOfMemoryError`,
		},
		{
			name: "management server",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+ManagementServer`,
		},
		{
			name: "exact management property",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -DcOm.SuN.MaNaGeMeNt`,
		},
		{
			name: "legacy AOT native library",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:AOTLibrary=/tmp/native.so`,
		},
		{
			name: "alternate JVM library",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XXaltjvm=/tmp/alternate`,
		},
		{
			name: "disabled bytecode verification",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XvErIfY:NoNe`,
		},
		{
			name: "modern AOT cache",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:AOTCache=/tmp/cache.aot`,
		},
		{
			name: "OpenJ9 JIT server",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:JITServerAddress=compiler.example.test`,
		},
		{
			name: "OpenJ9 shared classes",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Xshareclasses:cacheDir=/tmp/cache,restoreFromSnapshot`,
		},
		{
			name: "HotSpot shared archive dump",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Xshare:dump`,
		},
		{
			name: "unified logging help",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Xlog:help`,
		},
		{
			name: "initial VM flags",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+PrInTfLaGsInItIaL`,
		},
		{
			name: "VM flags with comments",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+PrInTfLaGsWiThCoMmEnTs`,
		},
		{
			name: "legacy VM flags",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XpRiNtFlAgS`,
		},
		{
			name: "dump shared spaces",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+DumpSharedSpaces`,
		},
		{
			name: "print shared archive and exit",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+PrintSharedArchiveAndExit`,
		},
		{
			name: "perf data save file",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:PerfDataSaveFile=/tmp/perf.data -XX:+PerfDataSaveToFile`,
		},
		{
			name: "pause at startup file",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:PauseAtStartupFile=/tmp/pause -XX:+PauseAtStartup`,
		},
		{
			name: "arbitrary option carries high entropy",
			content: `[JavaVM:Options]
Java_VM_Params=-Xopaque:` + entropyCanary,
			canary: entropyCanary,
		},
		{
			name: "literal redaction sentinel is ambiguous",
			content: `[JavaVM:Options]
Java_VM_Params=-Dlabel=` + credential.Redacted,
			// The disclosure sentinel is the intended preserved value.
			canary: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := ParseRuntimeCfg(test.content)
			if len(rc.JavaVMAdditionalParameters) != 0 {
				t.Fatalf("partially retained executable arguments: %#v", rc.JavaVMAdditionalParameters)
			}
			if len(rc.javaVMParameterRejections) == 0 ||
				rc.JavaVMParametersParseError == "" {
				t.Fatalf("rejections/error = %#v / %q, want value-free rejection", rc.javaVMParameterRejections, rc.JavaVMParametersParseError)
			}
			serialized, err := json.Marshal(rc.RuntimeIssues)
			if err != nil {
				t.Fatalf("marshal runtime issues: %v", err)
			}
			if test.canary != "" &&
				(strings.Contains(string(serialized), test.canary) ||
					strings.Contains(rc.JavaVMParametersRaw, test.canary)) {
				t.Fatalf("runtime diagnostics/raw retained canary %q: %s / %q", test.canary, serialized, rc.JavaVMParametersRaw)
			}
			for _, row := range rc.JavaVMParameterRows {
				if test.canary != "" && strings.Contains(row, test.canary) {
					t.Fatalf("preserved row retained canary %q: %q", test.canary, row)
				}
			}
		})
	}
}

func TestConvertZipDirectRuntimeOverrideClearsOnlyJavaUnsafeVMArguments(t *testing.T) {
	const canary = "direct-runtime-override-password"
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	addJavaZipFile(t, writer, "JavaOverride/JavaOverride.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, writer, "JavaOverride/Actions.java", `public class Actions {
  public int action() throws Throwable { return 0; }
}`)
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(archive.Bytes(), ConvertOptions{
		RuntimeSettings: &dsl.ScriptConfig{
			JavaVuser: dsl.JavaVuserConfig{
				AdditionalVMParameters: []string{"-Xmx512m", "-Dservice.password=" + canary},
			},
		},
	})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if got := result.Script.Config.JavaVuser.AdditionalVMParameters; len(got) != 0 {
		t.Fatalf("direct unsafe VM override survived: %#v", got)
	}
	if len(result.Script.Actions) == 0 ||
		result.Script.Actions[0].Metadata["execution_blocker"] != javaVMParameterExecutionBlocker {
		t.Fatalf("direct unsafe VM override did not gate Java action: %#v", result.Script.Actions)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(payload), canary) || strings.Contains(string(payload), "{{secret.") {
		t.Fatalf("direct runtime override leaked/projected secret in result: %s", payload)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("direct VM override created a provisionable requirement: %#v", requirements)
	}
}

func TestJavaVMParameterGateDoesNotAffectNonJavaScriptInMixedResult(t *testing.T) {
	javaScript := &dsl.Script{
		Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			AdditionalVMParameters: []string{"-Xmx512m"},
		}},
		Actions: []dsl.Action{{Name: "Java action", Type: "java_vuser_run"}},
	}
	httpScript := &dsl.Script{
		Protocol: "http",
		Actions:  []dsl.Action{{Name: "HTTP action", Type: "request", Method: "GET", Target: "/"}},
	}
	result := &ConvertResult{
		Script:  javaScript,
		Scripts: []*dsl.Script{javaScript, httpScript},
	}
	runtimeCfg := &RuntimeConfig{javaVMParameterRejections: []javaVMParameterRejection{{
		sourceKey:  "Java_VM_Params",
		sourceLine: 4,
		tokenIndex: 1,
		category:   "sensitive_property",
	}}}

	applyJavaVMParameterRejectionReview(result, result.Scripts, runtimeCfg, "default.cfg")

	if javaScript.Actions[0].Metadata["execution_blocker"] != javaVMParameterExecutionBlocker {
		t.Fatalf("Java action gate = %#v", javaScript.Actions[0].Metadata)
	}
	if httpScript.Actions[0].Metadata != nil {
		t.Fatalf("non-Java script received Java VM gate: %#v", httpScript.Actions[0].Metadata)
	}
}

func TestConvertZipUnsafeJavaVMArgumentIsOmittedRedactedAndDurablyBlocked(t *testing.T) {
	const (
		passwordCanary = "java-vm-password-canary"
		apiKeyCanary   = "java-vm-api-key-canary"
	)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	addJavaZipFile(t, writer, "JavaSecure/JavaSecure.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RuntimeSettings=default.cfg
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, writer, "JavaSecure/default.cfg", `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Dservice.password=`+passwordCanary+`
AdditionalVmParams_extra_arg_1=-Dapi.key=`+apiKeyCanary+`
`)
	addJavaZipFile(t, writer, "JavaSecure/Actions.java", `public class Actions {
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
	if got := result.Script.Config.JavaVuser.AdditionalVMParameters; len(got) != 0 {
		t.Fatalf("executable Java VM argv = %#v, want fail-closed empty list", got)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("Java VM argument created provisionable secret requirements: %#v", requirements)
	}
	if len(result.Script.Actions) == 0 {
		t.Fatal("Java Vuser draft has no action to carry the durable gate")
	}
	for index := range result.Script.Actions {
		if result.Script.Actions[index].Metadata["execution_blocker"] != javaVMParameterExecutionBlocker {
			t.Fatalf("actions[%d] gate = %#v, want %q", index, result.Script.Actions[index].Metadata, javaVMParameterExecutionBlocker)
		}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, canary := range []string{passwordCanary, apiKeyCanary} {
		if strings.Contains(string(payload), canary) {
			t.Fatalf("converter JSON retained Java VM canary %q", canary)
		}
	}
	if strings.Contains(string(payload), "{{secret.") {
		t.Fatalf("Java VM rejection produced a provisionable secret reference: %s", payload)
	}
	if !strings.Contains(string(payload), credential.Redacted) ||
		!strings.Contains(string(payload), javaVMParameterExecutionBlocker) {
		t.Fatalf("converter JSON omitted redaction/blocker evidence: %s", payload)
	}
}
