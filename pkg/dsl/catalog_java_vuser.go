package dsl

import (
	"fmt"
	"strings"
)

func javaVuserActionSpecs() []ActionSpec {
	return []ActionSpec{
		{
			Type:      "java_vuser_run",
			Protocols: []string{"java_vuser"},
			Summary:   "Runs a LoadRunner Java Vuser lifecycle through the governed JVM sidecar runner.",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "Java class name to load, usually Actions."},
				{Name: "params.method", Kind: FieldKindEnum, Enum: []string{"init", "action", "end", "full_lifecycle"}, Doc: "Lifecycle method or full lifecycle sequence to invoke."},
				{Name: "params.source_archive_ref", Kind: FieldKindString, Doc: "Preserved source archive or authoring artifact reference."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Per-run timeout in milliseconds; falls back to config.java_vuser.action_timeout_ms."},
			},
			Validate: func(index int, action Action) []string {
				if strings.TrimSpace(action.Target) == "" {
					return []string{fmt.Sprintf("actions[%d] %q: java_vuser_run requires target class name", index, action.Name)}
				}
				if method, ok := actionParamStringValue(action.Params, "method"); ok && method != "" {
					switch strings.ToLower(strings.TrimSpace(method)) {
					case "init", "action", "end", "full_lifecycle":
					default:
						return []string{fmt.Sprintf("actions[%d] %q: java_vuser_run params.method must be init, action, end, or full_lifecycle", index, action.Name)}
					}
				}
				return nil
			},
		},
	}
}
