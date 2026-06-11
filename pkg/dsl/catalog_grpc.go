package dsl

import (
	"fmt"
	"strings"
)

// grpcActionSpecs declares the grpc protocol action types. All four call
// shapes share the same field surface and residual rules.
func grpcActionSpecs() []ActionSpec {
	fields := []FieldSpec{
		{Name: "target", Required: true, Kind: FieldKindString, Doc: "Fully-qualified service/method, e.g. pkg.Service/Method."},
		{Name: "body", Kind: FieldKindString, Doc: "Request message as JSON; template variables are expanded."},
		{Name: "headers", Kind: FieldKindObject, Doc: "Per-call gRPC metadata merged over config.grpc.metadata."},
		{Name: "params.timeout_ms", Kind: FieldKindInt, Doc: "Per-call deadline in milliseconds; zero or greater."},
		{Name: "params.max_messages", Kind: FieldKindInt, Doc: "Cap on streamed messages; zero or greater."},
	}
	summaries := []struct {
		actionType string
		summary    string
	}{
		{"grpc_unary", "Unary gRPC call."},
		{"grpc_server_stream", "Server-streaming gRPC call; reads until the stream ends or max_messages."},
		{"grpc_client_stream", "Client-streaming gRPC call."},
		{"grpc_bidi_stream", "Bidirectional-streaming gRPC call."},
	}
	specs := make([]ActionSpec, 0, len(summaries))
	for _, s := range summaries {
		specs = append(specs, ActionSpec{
			Type:      s.actionType,
			Protocols: []string{"grpc"},
			Summary:   s.summary,
			Fields:    fields,
			Validate:  validateGRPCMethodAction,
		})
	}
	return specs
}

// validateGRPCMethodAction holds the shared residual rules for all gRPC call
// shapes; messages embed action.Type, so one function serves all four specs.
func validateGRPCMethodAction(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	if strings.TrimSpace(action.Target) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing target", label))
	} else if !strings.Contains(strings.Trim(action.Target, "/"), "/") {
		issues = append(issues, fmt.Sprintf("%s: %s target must be service/method", label, action.Type))
	}
	if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok && timeoutMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: %s timeout_ms must be zero or greater", label, action.Type))
	}
	if maxMessages, ok := actionParamIntValue(action.Params, "max_messages"); ok && maxMessages < 0 {
		issues = append(issues, fmt.Sprintf("%s: %s max_messages must be zero or greater", label, action.Type))
	}
	return issues
}
