package dsl

import (
	"fmt"
	"strings"
	"testing"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

func TestBrowserDialogCatalogDocumentsRuntimeDefault(t *testing.T) {
	spec, ok := SpecForActionType("browser_handle_dialog")
	if !ok {
		t.Fatal("browser_handle_dialog catalog spec not found")
	}
	field, ok := catalogFieldByName(spec, "params.wait_ms")
	if !ok {
		t.Fatal("browser_handle_dialog catalog is missing params.wait_ms")
	}
	want := fmt.Sprintf("default %d", browserprotocol.DefaultDialogWaitMS)
	if !strings.Contains(field.Doc, want) {
		t.Fatalf("params.wait_ms documentation = %q, want runtime %s", field.Doc, want)
	}
}

func TestBrowserMouseButtonCatalogMatchesRuntimeAliasesAndRejectsTypos(t *testing.T) {
	for _, actionType := range []string{"browser_click", "browser_dblclick"} {
		t.Run(actionType, func(t *testing.T) {
			spec, ok := SpecForActionType(actionType)
			if !ok {
				t.Fatalf("%s catalog spec not found", actionType)
			}
			field, ok := catalogFieldByName(spec, "params.button")
			if !ok || field.Kind != FieldKindEnum || strings.Join(field.Enum, ",") != "left,right,middle" {
				t.Fatalf("%s button field = %#v, want runtime-compatible suggested choices", actionType, field)
			}
			script := &Script{
				Name: "button contract", Protocol: "browser",
				Actions: []Action{{Name: "click", Type: actionType, Selector: "#target", Params: map[string]any{"button": "typo"}}},
			}
			issues := ValidateScriptIssues(script)
			if len(issues) == 0 || !strings.Contains(strings.Join(issues, "\n"), "params.button must be one of left, right, middle") {
				t.Fatalf("invalid button issues = %v", issues)
			}
			for _, accepted := range []string{"right", "RIGHT", "primary", "Secondary", "auxiliary", "aux"} {
				script.Actions[0].Params["button"] = accepted
				if issues := ValidateScriptIssues(script); len(issues) != 0 {
					t.Fatalf("runtime-supported button %q was rejected: %v", accepted, issues)
				}
			}
			script.Actions[0].Params["button"] = 1
			issues = ValidateScriptIssues(script)
			if len(issues) == 0 || !strings.Contains(strings.Join(issues, "\n"), "params.button must be a string") {
				t.Fatalf("non-string button issues = %v", issues)
			}
		})
	}
}

func TestKafkaCatalogDocumentsRuntimeActionParameters(t *testing.T) {
	wantFields := map[string][]string{
		"kafka_produce":       {"params.correlation_id"},
		"kafka_consume":       {"params.consumer_group"},
		"kafka_request_reply": {"params.correlation_id"},
		"kafka_commit":        {"params.consumer_group"},
		"kafka_seek":          {"params.consumer_group"},
	}
	for actionType, fields := range wantFields {
		t.Run(actionType, func(t *testing.T) {
			spec, ok := SpecForActionType(actionType)
			if !ok {
				t.Fatalf("%s catalog spec not found", actionType)
			}
			for _, fieldName := range fields {
				if _, ok := catalogFieldByName(spec, fieldName); !ok {
					t.Errorf("%s catalog is missing runtime parameter %s", actionType, fieldName)
				}
			}
		})
	}
}

func catalogFieldByName(spec ActionSpec, name string) (FieldSpec, bool) {
	for _, field := range spec.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return FieldSpec{}, false
}
