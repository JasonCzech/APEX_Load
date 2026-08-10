package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

// One credential-looking -D assignment must not delete the operator's entire
// JVM command line. The imported argv is executable configuration the operator
// already owns; sensitivity is an exposure review, not a structural rejection.
func TestExecutableJavaVMParametersKeepsSensitiveAssignments(t *testing.T) {
	const canary = "jvm-argv-password-canary"
	parameters := []string{
		"-Xmx512m",
		"-Dservice.password=" + canary,
		"-Dlabel=plain",
	}
	if got := executableJavaVMParameters(parameters); !reflect.DeepEqual(got, parameters) {
		t.Fatalf("executable JVM parameters = %#v, want the complete authored argv %#v", got, parameters)
	}
}

// Structurally unsafe process-control options are still omitted, and only
// those: their safe siblings, including credential-bearing ones, survive.
func TestExecutableJavaVMParametersDropsOnlyStructurallyUnsafeTokens(t *testing.T) {
	const canary = "jvm-argv-sibling-password-canary"
	got := executableJavaVMParameters([]string{
		"-Xmx512m",
		"-javaagent:/tmp/untrusted-agent.jar",
		"-Dservice.password=" + canary,
	})
	want := []string{"-Xmx512m", "-Dservice.password=" + canary}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("executable JVM parameters = %#v, want %#v", got, want)
	}
}

// End-to-end: a Java Vuser archive whose JVM parameters include a sensitive -D
// assignment must still produce an executable argv and a preserved
// java_vm_settings block that a dashboard picker can round trip.
func TestConvertZipRetainsSensitiveJavaVMParameterArgv(t *testing.T) {
	const canary = "archive-jvm-password-canary"
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
Java_VM_Params=-Xmx512m -Dservice.password=`+canary+` -Dlabel=plain
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
	want := []string{"-Xmx512m", "-Dservice.password=" + canary, "-Dlabel=plain"}
	if got := result.Script.Config.JavaVuser.AdditionalVMParameters; !reflect.DeepEqual(got, want) {
		t.Fatalf("executable VM parameters = %#v, want the complete authored argv %#v", got, want)
	}
	preserved := result.Script.Metadata.Import.Preserved
	javaVM, ok := preserved["java_vm_settings"].(map[string]any)
	if !ok {
		t.Fatalf("preserved java_vm_settings = %#v, want normalized object", preserved["java_vm_settings"])
	}
	if got, _ := javaVM["additional_vm_parameters"].([]string); !reflect.DeepEqual(got, want) {
		t.Fatalf("preserved additional_vm_parameters = %#v, want %#v", javaVM["additional_vm_parameters"], want)
	}
	raw, _ := javaVM["additional_vm_parameters_raw"].(string)
	if !strings.Contains(raw, canary) || strings.Contains(raw, credential.Redacted) {
		t.Fatalf("preserved additional_vm_parameters_raw = %q, want the exact source row", raw)
	}
}
