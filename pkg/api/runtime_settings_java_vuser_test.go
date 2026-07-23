package api

import "testing"

func TestRuntimeSettingsSupportMatrixScopesJavaVMSettingsToJavaVuser(t *testing.T) {
	matrix := runtimeSettingsSupportMatrix()
	wantStatuses := map[string]string{
		"java_vm_jdk":                    "executed",
		"java_vm_parameters":             "executed",
		"java_vm_classpath":              "executed",
		"java_vm_dedicated_class_loader": "executed",
		"java_vm_xbootclasspath":         "compatibility_only",
	}

	for id, status := range wantStatuses {
		found := false
		for _, entry := range matrix {
			if entry.ID != id {
				continue
			}
			if entry.Status != status {
				t.Fatalf("entry %s status = %q, want %q", id, entry.Status, status)
			}
			if len(entry.Protocols) != 1 || entry.Protocols[0] != "java_vuser" || entry.Source != "source_java_vuser" {
				t.Fatalf("entry %s scope = protocols %#v source %q", id, entry.Protocols, entry.Source)
			}
			found = true
			break
		}
		if !found {
			t.Fatalf("support matrix missing %s", id)
		}
	}

	if !containsString(runtimeSettingsSupportedProtocols(matrix), "java_vuser") {
		t.Fatal("runtime settings protocol catalog is missing java_vuser")
	}
}
