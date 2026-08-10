package api

import (
	"archive/zip"
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func buildJavaVMParameterRowArchive(t *testing.T, runtimeCfg string) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	addFile := func(path string, content string) {
		entry, err := writer.Create(path)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", path, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", path, err)
		}
	}
	addFile("JavaVM/JavaVM.usr", "[General]\nType=Java Vuser\nScriptLanguage=JAVA\nRuntimeSettings=default.cfg\n\n[Actions]\nAction=Actions.java\n")
	addFile("JavaVM/default.cfg", runtimeCfg)
	addFile("JavaVM/Actions.java", "public class Actions {\n  public int action() throws Throwable { return 0; }\n}\n")
	if err := writer.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}
	return archive.Bytes()
}

func preservedJavaVMRows(t *testing.T, value any) []string {
	t.Helper()
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		rows := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				t.Fatalf("preserved JVM row %#v is not a string", item)
			}
			rows = append(rows, text)
		}
		return rows
	default:
		t.Fatalf("preserved JVM parameter member = %#v, want an ordered list of rows", value)
		return nil
	}
}

// The durable write is the last hop before storage, export, download, GitHub
// push, and library bundling, all of which now ship the stored script. The
// enterprise-archive converter keeps a credential-looking -D assignment exact
// because masking it hands the operator a command line they cannot run; that
// guarantee is worthless if the persistence boundary rewrites the same rows.
// Assert the durable row, not just the converter output.
func TestDurableImportedJavaVMParameterRowsSurviveThePersistenceBoundary(t *testing.T) {
	const canary = "durable-jvm-row-password-canary"
	archive := buildJavaVMParameterRowArchive(t, "[JavaVM:Options]\n"+
		"Java_VM_Params=-Xmx512m -Dservice.password="+canary+"\n"+
		"Java_VM_Params_extra_arg_1=-Dservice.token="+canary+"-two\n")

	result, err := enterprisearchive.ConvertZip(archive, enterprisearchive.ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	durable, err := preparePersistentImportedLibraryScript(result.Script, nil, "enterprise_archive")
	if err != nil {
		t.Fatalf("preparePersistentImportedLibraryScript returned error: %v", err)
	}

	wantArgv := []string{"-Xmx512m", "-Dservice.password=" + canary, "-Dservice.token=" + canary + "-two"}
	if got := durable.Config.JavaVuser.AdditionalVMParameters; !reflect.DeepEqual(got, wantArgv) {
		t.Fatalf("durable executable VM parameters = %#v, want the complete authored argv %#v", got, wantArgv)
	}
	settings, ok := durable.Metadata.Import.Preserved["java_vm_settings"].(map[string]any)
	if !ok {
		t.Fatalf("durable preserved java_vm_settings = %#v, want the normalized picker object", durable.Metadata.Import.Preserved["java_vm_settings"])
	}
	if got := preservedJavaVMRows(t, settings["additional_vm_parameters"]); !reflect.DeepEqual(got, wantArgv) {
		t.Fatalf("durable preserved additional_vm_parameters = %#v, want %#v", got, wantArgv)
	}
	wantRows := []string{"-Xmx512m -Dservice.password=" + canary, "-Dservice.token=" + canary + "-two"}
	if got := preservedJavaVMRows(t, settings["additional_vm_parameter_rows"]); !reflect.DeepEqual(got, wantRows) {
		t.Fatalf("durable preserved additional_vm_parameter_rows = %#v, want the exact source rows %#v", got, wantRows)
	}
	raw, _ := settings["additional_vm_parameters_raw"].(string)
	if raw != wantRows[0] {
		t.Fatalf("durable preserved additional_vm_parameters_raw = %q, want the exact source row %q", raw, wantRows[0])
	}
	if strings.Contains(raw, maskedScriptSecret) {
		t.Fatalf("durable preserved additional_vm_parameters_raw was redacted: %q", raw)
	}
}

// Retaining the JVM command line must not reopen the durable raw-source side
// channel the surrounding default branch closes: only the three bounded,
// well-shaped command-line members of this one object are exempt.
func TestDurableImportedJavaVMSettingsStillProjectUnexemptMembers(t *testing.T) {
	const canary = "jvm-settings-side-channel-canary"
	oversized := strings.Repeat("x", maxImportedJavaVMParameterRowBytes+1) + " -Dservice.password=" + canary
	script := &dsl.Script{
		Name: "java-vm-settings-projection",
		Metadata: &dsl.ScriptMetadata{
			Import: &dsl.ImportMetadata{
				Preserved: map[string]any{
					"java_vm_settings": map[string]any{
						// Not a command-line member: still projected.
						"operator_note": "password=" + canary,
						// Command-line member, but not the shape the converter
						// writes: falls back to the ordinary projection.
						"additional_vm_parameters": []any{
							"-Dservice.password=" + canary,
							map[string]any{"password": canary},
						},
						// Command-line member beyond the row bound: same
						// fallback.
						"additional_vm_parameters_raw": oversized,
					},
				},
			},
		},
	}
	if err := sanitizePersistentImportReviewSurfaces(script); err != nil {
		t.Fatalf("sanitizePersistentImportReviewSurfaces returned error: %v", err)
	}
	settings, ok := script.Metadata.Import.Preserved["java_vm_settings"].(map[string]any)
	if !ok {
		t.Fatalf("preserved java_vm_settings = %#v, want an object", script.Metadata.Import.Preserved["java_vm_settings"])
	}
	note, _ := settings["operator_note"].(string)
	if strings.Contains(note, canary) {
		t.Fatalf("unexempt java_vm_settings member kept raw source text: %q", note)
	}
	parameters, ok := settings["additional_vm_parameters"].([]any)
	if !ok || len(parameters) != 2 {
		t.Fatalf("wrong-shaped additional_vm_parameters = %#v, want the projected list", settings["additional_vm_parameters"])
	}
	if text, _ := parameters[0].(string); strings.Contains(text, canary) {
		t.Fatalf("wrong-shaped additional_vm_parameters kept raw source text: %q", text)
	}
	raw, _ := settings["additional_vm_parameters_raw"].(string)
	if strings.Contains(raw, canary) {
		t.Fatalf("oversized additional_vm_parameters_raw kept raw source text: %q", raw)
	}
}
