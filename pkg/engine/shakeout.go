package engine

import (
	"context"
	"fmt"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// RunScriptShakeout executes one init-and-action pass for a single script instance.
// Any protocol initialization failure, init failure, or unsuccessful action fails the shakeout.
func RunScriptShakeout(ctx context.Context, groupName string, script *dsl.Script) error {
	return RunScriptShakeoutWithSecretResolver(ctx, groupName, script, nil)
}

// RunScriptShakeoutWithSecretResolver executes a shakeout with an explicitly
// governed secret source. A nil resolver preserves the standalone CLI/runtime
// behavior and resolves from the script's default project-scoped providers.
func RunScriptShakeoutWithSecretResolver(ctx context.Context, groupName string, script *dsl.Script, resolver SecretResolver) error {
	if script == nil {
		return fmt.Errorf("script is required")
	}
	if err := importExecutionGateError(script, "shakeout"); err != nil {
		return err
	}
	secretResolver := resolver
	if secretResolver == nil {
		secretResolver = defaultSecretResolverForScript(script)
	}
	resolutionCtx, resolutionCancel := WithSecretResolutionBudget(ctx)
	secretResolver, secretValues, err := snapshotReferencedSecretsContext(resolutionCtx, script, secretResolver)
	resolutionCancel()
	if err != nil {
		return fmt.Errorf("inspect script secret references: %s", scrubSensitiveText(err.Error()))
	}
	secretValues = append(secretValues, runtimeProtocolAuthCompositeValues(script, secretResolver)...)
	redactionConfig, err := WithSecretValueRedactionChecked(nil, secretValues)
	if err != nil {
		return fmt.Errorf("prepare runtime secret redaction: %s", scrubSensitiveText(err.Error()))
	}

	proto, err := protocol.Get(script.Protocol)
	if err != nil {
		return fmt.Errorf("protocol init: %s", scrubSensitiveTextWithConfig(err.Error(), redactionConfig))
	}

	bindProtocolRuntimeRedaction(proto, func() *RedactionConfig { return redactionConfig })
	if err := callProtocolInit(ctx, proto, buildProtoConfigForRunModeWithSecretResolver(script, "replay", secretResolver), redactionConfig); err != nil {
		runProtocolTeardown(proto, func() *RedactionConfig { return redactionConfig })
		return fmt.Errorf("protocol init: %s", err)
	}
	defer runProtocolTeardown(proto, func() *RedactionConfig { return redactionConfig })

	vu := &VUser{
		ID:              0,
		Group:           groupName,
		Proto:           proto,
		Script:          script,
		Metrics:         NewMetricsCollector(16),
		Variables:       make(map[string]string),
		SecretResolver:  secretResolver,
		RedactionConfig: redactionConfig,
	}
	vu.bindRuntimeTextRedactor()
	// Register DSL cleanup as soon as the protocol-backed VUser exists. It runs
	// before the already-deferred protocol teardown and uses an independent,
	// bounded context even when data-source startup, init, or a main action
	// failed/cancelled.
	defer func() {
		teardownCtx, teardownCancel := teardownContext()
		defer teardownCancel()
		for _, action := range script.Teardown {
			_ = runShakeoutAction(teardownCtx, vu, action, "teardown")
		}
	}()

	dataProvider, err := NewDataProvider(script.DataSources)
	if err != nil {
		return fmt.Errorf("data source init: %s", scrubSensitiveTextWithConfig(err.Error(), redactionConfig))
	}
	vu.DataProvider = dataProvider
	vu.loadDataRow()

	for _, action := range script.Init {
		if err := runShakeoutAction(ctx, vu, action, "init"); err != nil {
			return err
		}
	}

	for _, action := range script.Actions {
		if err := runShakeoutAction(ctx, vu, action, "action"); err != nil {
			return err
		}
	}

	return nil
}

func runShakeoutAction(ctx context.Context, vu *VUser, action dsl.Action, phase string) error {
	if handled, err := vu.handleEngineAction(ctx, action); handled {
		if err == nil {
			return nil
		}
		return fmt.Errorf("%s", scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig))
	}
	resolved := vu.resolveAction(action)
	result, err := vu.executeWithRetry(ctx, resolved, vu.Script.Config.ErrorHandling)
	if err != nil {
		return fmt.Errorf("%s", scrubSensitiveTextWithConfig(fmt.Sprintf("%s %q error: %s", phase, action.Name, err), vu.RedactionConfig))
	}
	if result == nil {
		return fmt.Errorf("%s %q returned no result", phase, action.Name)
	}
	if err := vu.mergeExtractions(result); err != nil {
		return fmt.Errorf("%s", scrubSensitiveTextWithConfig(fmt.Sprintf("%s %q extraction retention failed: %s", phase, action.Name, err), vu.RedactionConfig))
	}
	if !result.Success {
		reason := result.ErrorMessage
		if reason == "" {
			reason = fmt.Sprintf("status %d", result.StatusCode)
		}
		return fmt.Errorf("%s", scrubSensitiveTextWithConfig(fmt.Sprintf("%s %q failed: %s", phase, action.Name, reason), vu.RedactionConfig))
	}
	return nil
}
