package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
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

func TestParseRuntimeCfgPreservesCredentialsButRejectsUnsafeJavaVMOptions(t *testing.T) {
	const (
		passwordCanary = "short-secret"
		apiKeyCanary   = "api-key-canary"
		privateCanary  = "private-key-canary"
		entropyCanary  = "q7W9pL2xN8vC4mR6tY1kH5sF3dJ0aB9uE2zG"
		urlCanary      = "userinfo-password-canary"
	)
	tests := []struct {
		name      string
		content   string
		canary    string
		sensitive bool
	}{
		{
			name: "password property",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -Dservice.password=` + passwordCanary,
			canary:    passwordCanary,
			sensitive: true,
		},
		{
			name: "api key in numbered alias row",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m
AdditionalVmParams_extra_arg_1=-Dapi.key=` + apiKeyCanary,
			canary:    apiKeyCanary,
			sensitive: true,
		},
		{
			name: "private key property",
			content: `[Java:Options]
AdditionalVmParams=-Dprivate_key=` + privateCanary,
			canary:    privateCanary,
			sensitive: true,
		},
		{
			name: "neutral high entropy property",
			content: `[JavaVM]
VmParams=-Dlabel=` + entropyCanary,
			canary:    entropyCanary,
			sensitive: true,
		},
		{
			name: "neutral credential URL property",
			content: `[Java Environment Settings]
ExtraVmParams="-Dendpoint=https://runner:` + urlCanary + `@example.test/service"`,
			canary:    urlCanary,
			sensitive: true,
		},
		{
			name: "neutral credential URL fragment",
			content: `[JavaVM:Options]
Java_VM_Params=-Dendpoint=https://example.test/service#token=` + urlCanary,
			canary:    urlCanary,
			sensitive: true,
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
			name: "crash core dump",
			content: `[JavaVM:Options]
Java_VM_Params=-Xmx512m -XX:+CreateCoredumpOnCrash`,
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
			canary:    entropyCanary,
			sensitive: true,
		},
		{
			name: "literal redaction sentinel is ambiguous",
			content: `[JavaVM:Options]
Java_VM_Params=-Dlabel=` + credential.Redacted,
			// The disclosure sentinel is the intended preserved value.
			canary:    "",
			sensitive: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rc := ParseRuntimeCfg(test.content)
			if test.sensitive {
				if len(rc.JavaVMAdditionalParameters) == 0 {
					t.Fatalf("credential-bearing executable arguments were removed: %#v", rc)
				}
				if len(rc.javaVMParameterExposureReviews) == 0 ||
					len(rc.javaVMParameterRejections) != 0 ||
					rc.JavaVMParametersParseError != "" {
					t.Fatalf(
						"exposure/rejections/error = %#v / %#v / %q, want non-blocking exposure review",
						rc.javaVMParameterExposureReviews,
						rc.javaVMParameterRejections,
						rc.JavaVMParametersParseError,
					)
				}
				if test.canary != "" &&
					!strings.Contains(strings.Join(rc.JavaVMAdditionalParameters, "\n"), test.canary) {
					t.Fatalf("executable arguments lost canary: %#v", rc.JavaVMAdditionalParameters)
				}
			} else {
				if issues := validateImportedJavaVMParameterStructure(
					rc.JavaVMAdditionalParameters,
				); len(issues) != 0 {
					t.Fatalf("structurally unsafe arguments survived: %#v", issues)
				}
				if len(rc.javaVMParameterRejections) == 0 ||
					rc.JavaVMParametersParseError != "" {
					t.Fatalf("rejections/error = %#v / %q, want token-level value-free rejection", rc.javaVMParameterRejections, rc.JavaVMParametersParseError)
				}
			}
			serialized, err := json.Marshal(rc.RuntimeIssues)
			if err != nil {
				t.Fatalf("marshal runtime issues: %v", err)
			}
			// Public diagnostics stay value-free: they name the source key and
			// token index only.
			if test.canary != "" && strings.Contains(string(serialized), test.canary) {
				t.Fatalf("runtime diagnostics retained canary %q: %s", test.canary, serialized)
			}
			// The raw row and the ordered rows are the round-trip record of the
			// operator's own command line and the source of the dashboard VM
			// parameter picker. Masking them handed the operator back a command
			// line they could not run, so they must retain the source bytes; the
			// executable argv is what drops structurally unsafe tokens.
			if test.canary != "" && !strings.Contains(rc.JavaVMParametersRaw, test.canary) &&
				!strings.Contains(strings.Join(rc.JavaVMParameterRows, "\n"), test.canary) {
				t.Fatalf(
					"preserved JVM rows lost the imported value %q: %q / %#v",
					test.canary,
					rc.JavaVMParametersRaw,
					rc.JavaVMParameterRows,
				)
			}
			for _, row := range rc.JavaVMParameterRows {
				if strings.Contains(row, credential.Redacted) && test.canary != "" {
					t.Fatalf("preserved row replaced the imported value with a sentinel: %q", row)
				}
			}
		})
	}
}

func TestConvertZipDirectRuntimeOverridePreservesCredentialWithNonBlockingReview(t *testing.T) {
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
	if got := result.Script.Config.JavaVuser.AdditionalVMParameters; len(got) != 2 ||
		got[0] != "-Xmx512m" || got[1] != "-Dservice.password="+canary {
		t.Fatalf("direct imported VM override = %#v, want exact source argv", got)
	}
	preserved := result.Script.Metadata.Import.Preserved
	if preserved[dsl.ImportPreservedCredentialPolicyKey] !=
		dsl.ImportPreservedCredentialPolicyPreserve {
		t.Fatalf("JVM credential preservation policy = %#v", preserved)
	}
	targetsPayload, err := json.Marshal(
		preserved[dsl.ImportPreservedCredentialTargetsMetadataKey],
	)
	if err != nil {
		t.Fatalf("marshal JVM credential locators: %v", err)
	}
	if !strings.Contains(
		string(targetsPayload),
		"config.java_vuser.additional_vm_parameters.opaque",
	) {
		t.Fatalf("JVM credential locator was discarded: %s", targetsPayload)
	}
	if len(result.Script.Actions) == 0 {
		t.Fatal("direct imported VM override has no Java action")
	}
	if blocker := result.Script.Actions[0].Metadata["execution_blocker"]; blocker == javaVMParameterExecutionBlocker {
		t.Fatalf("credential-only VM override received execution blocker: %#v", result.Script.Actions[0].Metadata)
	}
	review, _ := result.Script.Actions[0].Metadata["java_vm_parameter_review"].(map[string]any)
	if review["reason_code"] != javaVMParameterExposureReviewCode ||
		review["value_preserved"] != true ||
		review["execution_blocker"] != false {
		t.Fatalf("direct VM exposure review = %#v", review)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(payload), canary) || strings.Contains(string(payload), "{{secret.") {
		t.Fatalf("direct runtime override did not retain exact destination credential: %s", payload)
	}
	reviewPayload, err := json.Marshal([]any{result.ReviewArtifact, result.SourceReview, result.Warnings})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reviewPayload), canary) {
		t.Fatalf("review evidence leaked direct runtime credential: %s", reviewPayload)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("direct VM override created a provisionable requirement: %#v", requirements)
	}
}

func TestConvertZipDirectRuntimeOverrideStillBlocksUnsafeJavaVMOption(t *testing.T) {
	const (
		canary         = "mixed-direct-runtime-password"
		rejectedCanary = "rejected-javaagent-password"
	)
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
				AdditionalVMParameters: []string{
					"-Dservice.password=" + canary,
					"-Xmx512m",
					"-javaagent:https://agent-user:" + rejectedCanary +
						"@agent.example.test/untrusted.jar",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	want := []string{"-Dservice.password=" + canary, "-Xmx512m"}
	if got := result.Script.Config.JavaVuser.AdditionalVMParameters; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered VM override = %#v, want safe siblings %#v", got, want)
	}
	if len(result.Script.Actions) == 0 ||
		result.Script.Actions[0].Metadata["execution_blocker"] != javaVMParameterExecutionBlocker {
		t.Fatalf("structurally unsafe VM override did not gate Java action: %#v", result.Script.Actions)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "structurally unsafe") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
	envelope, ok := result.Script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaVMSourceKey].(map[string]any)
	rows, rowsOK := envelope["rows_base64"].([]string)
	if !ok || !rowsOK || len(rows) != 1 {
		t.Fatalf("rejected direct JVM source envelope = %#v", envelope)
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(rows[0])
	if decodeErr != nil || !strings.Contains(string(decoded), rejectedCanary) {
		t.Fatalf("rejected direct JVM row = %q / %v", decoded, decodeErr)
	}
	projected, err := scriptdisclosure.Project(result.Script)
	if err != nil {
		t.Fatalf("project disclosure-safe script: %v", err)
	}
	projectedJSON, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal disclosure-safe script: %v", err)
	}
	if strings.Contains(string(projectedJSON), rejectedCanary) {
		t.Fatalf("disclosure projection leaked rejected JVM credential: %s", projectedJSON)
	}
}

func TestConvertZipRetainsMalformedCredentialVMRowOnlyInDurableDraft(t *testing.T) {
	const canary = "malformed-vm-row-password"
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	addJavaZipFile(t, writer, "JavaMalformed/JavaMalformed.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, writer, "JavaMalformed/Actions.java", `public class Actions {
  public int action() throws Throwable { return 0; }
}`)
	addJavaZipFile(t, writer, "JavaMalformed/default.cfg", `[JavaVM:Options]
Java_VM_Params="-Dservice.password=`+canary)
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(archive.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script.Metadata == nil || result.Script.Metadata.Import == nil {
		t.Fatalf("import metadata = %#v", result.Script.Metadata)
	}
	envelope, ok := result.Script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaVMSourceKey].(map[string]any)
	rows, rowsOK := envelope["rows_base64"].([]string)
	if !ok || !rowsOK || len(rows) != 1 {
		t.Fatalf("protected malformed JVM source envelope = %#v", envelope)
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(rows[0])
	if decodeErr != nil || !strings.Contains(string(decoded), canary) {
		t.Fatalf("protected malformed JVM source rows = %#v / %v", rows, decodeErr)
	}
	if len(result.Script.Actions) == 0 ||
		result.Script.Actions[0].Metadata["execution_blocker"] !=
			javaVMParameterExecutionBlocker {
		t.Fatalf("malformed JVM source was not gated: %#v", result.Script.Actions)
	}

	projected, err := scriptdisclosure.Project(result.Script)
	if err != nil {
		t.Fatalf("project disclosure-safe script: %v", err)
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal disclosure-safe script: %v", err)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("disclosure projection leaked protected JVM source row: %s", payload)
	}
}

func TestPreserveRejectedJavaVMSourceRowsEnforcesStorageBounds(t *testing.T) {
	rows := func(count int, bytesPerRow int) []string {
		values := make([]string, count)
		for index := range values {
			values[index] = strings.Repeat("x", bytesPerRow)
		}
		return values
	}
	tests := []struct {
		name    string
		rows    []string
		wantErr bool
	}{
		{
			name: "maximum item count",
			rows: rows(maxProtectedJavaVMSourceRows, 1),
		},
		{
			name: "maximum value size",
			rows: rows(1, maxProtectedJavaVMSourceValueBytes),
		},
		{
			name: "maximum total size",
			rows: rows(
				maxProtectedJavaVMSourceTotalBytes/
					maxProtectedJavaVMSourceValueBytes,
				maxProtectedJavaVMSourceValueBytes,
			),
		},
		{
			name:    "item count exceeded",
			rows:    rows(maxProtectedJavaVMSourceRows+1, 1),
			wantErr: true,
		},
		{
			name:    "value size exceeded",
			rows:    rows(1, maxProtectedJavaVMSourceValueBytes+1),
			wantErr: true,
		},
		{
			name: "total size exceeded",
			rows: rows(
				(maxProtectedJavaVMSourceTotalBytes/
					maxProtectedJavaVMSourceValueBytes)+1,
				maxProtectedJavaVMSourceValueBytes,
			),
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{}
			err := preserveRejectedJavaVMSourceRows(script, test.rows)
			if test.wantErr {
				if err == nil {
					t.Fatal("protected Java VM source bounds accepted oversized input")
				}
				if script.Metadata != nil && script.Metadata.Import != nil &&
					script.Metadata.Import.Preserved != nil {
					if _, exists := script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaVMSourceKey]; exists {
						t.Fatal("oversized protected Java VM source was partially persisted")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("preserveRejectedJavaVMSourceRows: %v", err)
			}
			if script.Metadata == nil || script.Metadata.Import == nil {
				t.Fatalf("protected source metadata = %#v", script.Metadata)
			}
			if _, exists := script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaVMSourceKey]; !exists {
				t.Fatal("boundary-sized protected Java VM source was not retained")
			}
		})
	}
}

func TestJavaVMCredentialReviewDoesNotGateOrAffectNonJavaScriptInMixedResult(t *testing.T) {
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

	if javaScript.Actions[0].Metadata["execution_blocker"] == javaVMParameterExecutionBlocker {
		t.Fatalf("Java credential received execution gate = %#v", javaScript.Actions[0].Metadata)
	}
	review, _ := javaScript.Actions[0].Metadata["java_vm_parameter_review"].(map[string]any)
	if review["reason_code"] != javaVMParameterExposureReviewCode ||
		review["value_preserved"] != true {
		t.Fatalf("Java action review = %#v", javaScript.Actions[0].Metadata)
	}
	if httpScript.Actions[0].Metadata != nil {
		t.Fatalf("non-Java script received Java VM gate: %#v", httpScript.Actions[0].Metadata)
	}
}

func TestConvertZipCredentialJavaVMArgumentsRemainExactWithDisclosureSafeReview(t *testing.T) {
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
	if got := result.Script.Config.JavaVuser.AdditionalVMParameters; len(got) != 3 ||
		got[0] != "-Xmx512m" ||
		got[1] != "-Dservice.password="+passwordCanary ||
		got[2] != "-Dapi.key="+apiKeyCanary {
		t.Fatalf("executable Java VM argv = %#v, want exact imported list", got)
	}
	if requirements := result.ProtectedRequirements.Take(); len(requirements) != 0 {
		t.Fatalf("Java VM argument created provisionable secret requirements: %#v", requirements)
	}
	if len(result.Script.Actions) == 0 {
		t.Fatal("Java Vuser draft has no action to carry durable review evidence")
	}
	for index := range result.Script.Actions {
		metadata := result.Script.Actions[index].Metadata
		if metadata["execution_blocker"] == javaVMParameterExecutionBlocker {
			t.Fatalf("actions[%d] credential-only gate = %#v", index, metadata)
		}
		review, _ := metadata["java_vm_parameter_review"].(map[string]any)
		if review["reason_code"] != javaVMParameterExposureReviewCode ||
			review["value_preserved"] != true ||
			review["execution_blocker"] != false {
			t.Fatalf("actions[%d] review = %#v", index, metadata)
		}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, canary := range []string{passwordCanary, apiKeyCanary} {
		if !strings.Contains(string(payload), canary) {
			t.Fatalf("converter destination script lost Java VM canary %q", canary)
		}
	}
	if strings.Contains(string(payload), "{{secret.") {
		t.Fatalf("Java VM rejection produced a provisionable secret reference: %s", payload)
	}
	reviewPayload, err := json.Marshal([]any{
		result.ReviewArtifact,
		result.SourceReview,
		result.Warnings,
		result.Script.Actions[0].Metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{passwordCanary, apiKeyCanary} {
		if strings.Contains(string(reviewPayload), canary) {
			t.Fatalf("review evidence retained Java VM canary %q: %s", canary, reviewPayload)
		}
	}
	if !strings.Contains(string(reviewPayload), credential.Redacted) ||
		!strings.Contains(string(reviewPayload), javaVMParameterExposureReviewCode) {
		t.Fatalf("converter review omitted redaction/exposure evidence: %s", reviewPayload)
	}
}
