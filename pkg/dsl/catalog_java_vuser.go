package dsl

import (
	"fmt"
	"strings"
)

func validateJavaVuserActionIssues(index int, action Action) []string {
	spec, ok := protocolActionSpec("java_vuser", action.Type)
	if !ok {
		return []string{fmt.Sprintf("actions[%d] %q: unsupported Java Vuser action type %q", index, action.Name, action.Type)}
	}
	if spec.Validate != nil {
		return spec.Validate(index, action)
	}
	return nil
}

func javaVuserActionSpecs() []ActionSpec {
	return []ActionSpec{
		{
			Type:      "java_vuser_run",
			Protocols: []string{"java_vuser"},
			Summary:   "Runs a LoadRunner Java Vuser lifecycle through the governed JVM sidecar runner.",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "Java class name to load, usually Actions."},
				{Name: "params.method", Kind: FieldKindEnum, Enum: []string{"init", "vuser_init", "initialize", "action", "Action", "run", "end", "vuser_end", "teardown", "full_lifecycle"}, Doc: "Source lifecycle method name or legacy full lifecycle sequence to invoke."},
				{Name: "params.source_archive_ref", Kind: FieldKindString, Doc: "Preserved source archive or authoring artifact reference."},
				{Name: "params.timeout_ms", Kind: FieldKindInt, Maximum: MaxConfiguredProtocolTimeoutMS, Doc: "Per-run timeout in milliseconds (maximum 3600000); falls back to config.java_vuser.action_timeout_ms."},
				{Name: "params.runner_requirement", Kind: FieldKindString, Doc: "Governance gate required before execution, usually java_vuser_jvm_runner."},
				{Name: "params.event_contract", Kind: FieldKindString, Doc: "Runner event stream contract expected from the sidecar shim."},
				{Name: "params.shim_event_types", Kind: FieldKindArray, Doc: "Event type names emitted as JSON lines by the lrapi.lr sidecar shim under the declared event contract."},
				{Name: "params.lr_parameters", Kind: FieldKindObject, Doc: "Initial LoadRunner parameter values exposed to lrapi.lr.eval_string as {Name}."},
			},
			Validate: func(index int, action Action) []string {
				if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok && (timeoutMs < 0 || timeoutMs > MaxConfiguredProtocolTimeoutMS) {
					return []string{fmt.Sprintf("actions[%d] %q: java_vuser_run params.timeout_ms must be between 0 and %d", index, action.Name, MaxConfiguredProtocolTimeoutMS)}
				}
				if strings.TrimSpace(action.Target) == "" {
					return []string{fmt.Sprintf("actions[%d] %q: java_vuser_run requires target class name", index, action.Name)}
				}
				if method, ok := actionParamStringValue(action.Params, "method"); ok && method != "" {
					switch strings.ToLower(strings.TrimSpace(method)) {
					case "init", "vuser_init", "initialize", "action", "run", "end", "vuser_end", "teardown", "full_lifecycle":
					default:
						return []string{fmt.Sprintf("actions[%d] %q: java_vuser_run params.method must be a supported Java lifecycle method or full_lifecycle", index, action.Name)}
					}
				}
				return nil
			},
		},
	}
}
