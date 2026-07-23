package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// PreflightScriptRuntime performs optional, script-aware runtime validation
// with secret-resolved configuration. It is safe to call during distributed
// two-phase staging: no VUser or protocol execution lifecycle is created.
func PreflightScriptRuntime(ctx context.Context, script *dsl.Script, resolver SecretResolver) (protocol.RuntimePreflightResult, error) {
	if script == nil {
		return protocol.RuntimePreflightResult{}, fmt.Errorf("script is required")
	}
	instance, err := protocol.Get(strings.ToLower(strings.TrimSpace(script.Protocol)))
	if err != nil {
		return protocol.RuntimePreflightResult{}, err
	}
	_, ok := instance.(protocol.RuntimePreflightProvider)
	if !ok {
		return protocol.RuntimePreflightResult{}, nil
	}
	if resolver == nil {
		resolver = defaultSecretResolverForScript(script)
	}
	targets, err := runtimePreflightTargets(ctx, script, resolver)
	if err != nil {
		return protocol.RuntimePreflightResult{}, err
	}
	request := protocol.RuntimePreflightRequest{
		Config:  buildProtoConfigForRunModeWithSecretResolver(script, "preflight", resolver),
		Targets: targets,
	}
	result, _, err := protocol.RunRuntimePreflight(ctx, instance, request)
	return result, err
}

func safeRuntimeWarningCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return "runtime_preflight_warning"
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			continue
		}
		return "runtime_preflight_warning"
	}
	return value
}

func runtimePreflightTargets(ctx context.Context, script *dsl.Script, resolver SecretResolver) ([]protocol.RuntimePreflightTarget, error) {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	seen := make(map[string]struct{})
	targets := make([]protocol.RuntimePreflightTarget, 0)
	var visit func([]dsl.Action) error
	targetIndex := 0
	visit = func(actions []dsl.Action) error {
		for _, action := range actions {
			if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
				index := targetIndex
				targetIndex++
				className, err := resolveRuntimePreflightTargetTemplate(ctx, strings.TrimSpace(action.Target), resolver)
				if err != nil {
					return fmt.Errorf("java_vuser target %d class %w", index, err)
				}
				if strings.TrimSpace(className) == "" {
					return fmt.Errorf("java_vuser target %d class resolves to an empty value", index)
				}
				methodValue := ""
				if value, ok := action.Params["method"]; ok && value != nil {
					methodValue = strings.TrimSpace(fmt.Sprint(value))
				}
				method, err := resolveRuntimePreflightTargetTemplate(ctx, methodValue, resolver)
				if err != nil {
					return fmt.Errorf("java_vuser target %d method %w", index, err)
				}
				if method == "" || method == "<nil>" {
					method = "full_lifecycle"
				}
				key := className + "\x00" + method
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					targets = append(targets, protocol.RuntimePreflightTarget{ClassName: className, Method: method})
				}
			}
			if action.Control != nil {
				if err := visit(action.Control.Steps); err != nil {
					return err
				}
				if err := visit(action.Control.Else); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(script.Init); err != nil {
		return nil, err
	}
	if err := visit(script.Actions); err != nil {
		return nil, err
	}
	if err := visit(script.Teardown); err != nil {
		return nil, err
	}
	return targets, nil
}

// resolveRuntimePreflightTargetTemplate resolves only values that are stable
// before VUser allocation. Errors identify a category, never the authored or
// resolved value, because either may contain secret material.
func resolveRuntimePreflightTargetTemplate(ctx context.Context, input string, resolver SecretResolver) (string, error) {
	matches := templatePattern.FindAllStringIndex(input, -1)
	if len(matches) == 0 {
		if strings.Contains(input, "{{") || strings.Contains(input, "}}") {
			return "", fmt.Errorf("contains an invalid template")
		}
		return input, nil
	}
	var resolved strings.Builder
	resolved.Grow(len(input))
	last := 0
	for _, match := range matches {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("resolution was canceled")
		}
		token := input[match[0]:match[1]]
		expr := strings.TrimSpace(token[2 : len(token)-2])
		literal := input[last:match[0]]
		if strings.Contains(literal, "{{") || strings.Contains(literal, "}}") {
			return "", fmt.Errorf("contains an invalid template")
		}
		resolved.WriteString(literal)
		last = match[1]
		switch {
		case strings.HasPrefix(expr, "secret."):
			value, ok := ResolveSecret(ctx, resolver, strings.TrimSpace(strings.TrimPrefix(expr, "secret.")))
			if !ok {
				return "", fmt.Errorf("contains an unresolved secret template")
			}
			resolved.WriteString(value)
		case strings.HasPrefix(expr, "env."):
			value, ok := lookupScriptEnv(strings.TrimSpace(strings.TrimPrefix(expr, "env.")))
			if !ok {
				return "", fmt.Errorf("contains an unresolved environment template")
			}
			resolved.WriteString(value)
		default:
			return "", fmt.Errorf("contains a dynamic template that is unavailable before VUser allocation")
		}
	}
	tail := input[last:]
	if strings.Contains(tail, "{{") || strings.Contains(tail, "}}") {
		return "", fmt.Errorf("contains an invalid template")
	}
	resolved.WriteString(tail)
	return resolved.String(), nil
}
