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
	if script == nil {
		return fmt.Errorf("script is required")
	}

	proto, err := protocol.Get(script.Protocol)
	if err != nil {
		return fmt.Errorf("protocol init: %w", err)
	}

	secretResolver := newDefaultSecretResolver()
	if err := proto.Init(ctx, buildProtoConfigForRunModeWithSecretResolver(script, "replay", secretResolver)); err != nil {
		return fmt.Errorf("protocol init: %w", err)
	}
	defer proto.Teardown(context.Background())

	dataProvider, err := NewDataProvider(script.DataSources)
	if err != nil {
		return fmt.Errorf("data source init: %w", err)
	}

	vu := &VUser{
		ID:             0,
		Group:          groupName,
		Proto:          proto,
		Script:         script,
		Metrics:        NewMetricsCollector(16),
		Variables:      make(map[string]string),
		DataProvider:   dataProvider,
		SecretResolver: secretResolver,
	}
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

	for _, action := range script.Teardown {
		_ = runShakeoutAction(context.Background(), vu, action, "teardown")
	}

	return nil
}

func runShakeoutAction(ctx context.Context, vu *VUser, action dsl.Action, phase string) error {
	if handled, err := vu.handleEngineAction(ctx, action); handled {
		return err
	}
	resolved := vu.resolveAction(action)
	result, err := vu.executeWithRetry(ctx, resolved, vu.Script.Config.ErrorHandling)
	if err != nil {
		return fmt.Errorf("%s %q error: %w", phase, action.Name, err)
	}
	if result == nil {
		return fmt.Errorf("%s %q returned no result", phase, action.Name)
	}
	vu.mergeExtractions(result)
	if !result.Success {
		reason := result.ErrorMessage
		if reason == "" {
			reason = fmt.Sprintf("status %d", result.StatusCode)
		}
		return fmt.Errorf("%s %q failed: %s", phase, action.Name, reason)
	}
	return nil
}
