package dsl

import (
	"reflect"
	"strings"
	"testing"
)

func TestNativeServicesDeclarationsJoinAliasesAndKeepGuestPermissions(t *testing.T) {
	s := &Script{Config: ScriptConfig{NativeServices: &NativeServicesConfig{LiveTables: []NativeLiveTableRequirement{{Alias: "queue", Operations: []string{"push", "lease"}}}}}}
	aliases, err := ScriptLiveTableAliases(s)
	if err != nil || !reflect.DeepEqual(aliases, []string{"queue"}) {
		t.Fatalf("%v %v", aliases, err)
	}
	r, err := ScriptNativeServiceRequirements(s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(NativeServicePermissionClosure(r[0].Operations), []string{"lease", "push", "release", "renew"}) {
		t.Fatal("missing trusted lease controls")
	}
	if !reflect.DeepEqual(r[0].Operations, []string{"lease", "push"}) {
		t.Fatal("guest permissions expanded")
	}
	for _, req := range []NativeLiveTableRequirement{{Alias: "{{var.alias}}", Operations: []string{"push"}}, {Alias: "queue", Operations: []string{"push", "push"}}, {Alias: "queue", Operations: []string{"connect"}}} {
		s.Config.NativeServices.LiveTables = []NativeLiveTableRequirement{req}
		if _, err := ScriptNativeServiceRequirements(s); err == nil {
			t.Fatal("invalid declaration accepted")
		}
	}
}
func TestNativeLiteralValidationDoesNotInterpretTemplates(t *testing.T) {
	params := map[string]any{"alias": "queue", "operation": "push", "values": map[string]string{"id": "{{secret.literal}}"}}
	if _, err := ParseLiteralLiveTableAction(params); err != nil {
		t.Fatal(err)
	}
	params = map[string]any{"alias": "queue", "operation": "update", "record_id": "row", "expected_version": "{{var.version}}", "values": map[string]string{"id": "x"}}
	if _, err := ParseLiteralLiveTableAction(params); err == nil {
		t.Fatal("native version template accepted")
	}
}

func TestNativeRuntimeFeatureSelectionSeparatesJavaFromJavaScript(t *testing.T) {
	for _, kind := range []string{"http", "java_vuser"} {
		s := &Script{Protocol: kind, Config: ScriptConfig{NativeServices: &NativeServicesConfig{LiveTables: []NativeLiveTableRequirement{{Alias: "work", Operations: []string{"push"}}}}}}
		want := []string{"native_data_services_v1"}
		if kind == "java_vuser" {
			want = append(want, "native_data_java_v1")
		}
		if got := RequiredNativeRuntimeFeatures(s); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got %v, want %v", kind, got, want)
		}
		s.Actions = []Action{{Type: "native_javascript"}}
		want = append(want, "native_javascript_v1")
		if got := RequiredNativeRuntimeFeatures(s); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s got %v, want %v", kind, got, want)
		}
	}
}

func TestNativeDeclarationAliasMatchesBindingLimit(t *testing.T) {
	for _, n := range []int{64, 65} {
		script := &Script{Config: ScriptConfig{NativeServices: &NativeServicesConfig{LiveTables: []NativeLiveTableRequirement{{Alias: strings.Repeat("a", n), Operations: []string{"push"}}}}}}
		_, err := ScriptNativeServiceRequirements(script)
		if (err == nil) != (n == 64) {
			t.Fatalf("alias length %d: %v", n, err)
		}
	}
}
