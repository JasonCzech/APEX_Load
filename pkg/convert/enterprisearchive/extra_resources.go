package enterprisearchive

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"strings"
)

func applyExtraResources(fn ParsedFunction, action *dsl.Action) {
	active := false
	resources := []any{}
	current := map[string]any{}
	invalid := false
	finish := func() {
		if _, ok := current["url"]; !ok {
			invalid = true
		} else {
			resources = append(resources, current)
		}
		current = map[string]any{}
	}
	for _, arg := range fn.CallArgs {
		if arg.Kind == "macro" && strings.EqualFold(arg.Value, "EXTRARES") {
			active = true
			continue
		}
		if !active {
			continue
		}
		if arg.Kind == "macro" {
			switch strings.ToUpper(arg.Value) {
			case "ENDITEM":
				finish()
			case "LAST":
				if len(current) > 0 {
					invalid = true
				}
				active = false
			default:
				invalid = true
			}
			continue
		}
		if arg.Kind != "key_value" {
			invalid = true
			continue
		}
		key := strings.ToLower(arg.Name)
		if key != "url" && key != "referer" {
			invalid = true
			continue
		}
		if _, exists := current[key]; exists {
			invalid = true
		}
		current[key] = convertParamReferences(arg.Value)
	}
	if len(resources) > httpprotocol.MaxPageResources || active {
		invalid = true
	}
	if len(resources) > 0 {
		if action.Params == nil {
			action.Params = map[string]any{}
		}
		action.Params["resources"] = resources
	}
	if invalid {
		importreview.ApplyToAction(action, []importreview.Finding{{Code: "import_review_required", ReasonCode: "extra_resources_unsupported", Severity: diag.Blocker, Source: "enterprise_script_archive", Location: fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber), Detail: "Explicit resource attributes could not be represented completely.", Gate: importreview.GateExecution, Resolution: "replace_behavior_and_remove_gate"}})
	}
}
