package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

type javaVuserCapacityGateProtocol struct {
	maxVUsers int64
	ceiling   *int64
	features  []string
}

func (*javaVuserCapacityGateProtocol) Name() string { return "java_vuser" }
func (*javaVuserCapacityGateProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*javaVuserCapacityGateProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}
func (*javaVuserCapacityGateProtocol) Teardown(context.Context) error    { return nil }
func (*javaVuserCapacityGateProtocol) HealthCheck(context.Context) error { return nil }
func (*javaVuserCapacityGateProtocol) RuntimeHealthy(context.Context) bool {
	return true
}
func (p *javaVuserCapacityGateProtocol) RuntimeCapabilities() protocol.RuntimeCapabilities {
	features := p.features
	if features == nil {
		features = []string{
			protocol.FeatureJavaVuserPersistentLifecycleV1,
			protocol.FeatureJavaVuserSessionFrameTokenV1,
		}
	}
	return protocol.RuntimeCapabilities{
		Available:        true,
		Features:         features,
		MaxVUsers:        p.maxVUsers,
		DedicatedCeiling: p.ceiling,
	}
}
func (*javaVuserCapacityGateProtocol) LegacyRuntimeAvailable() bool { return true }
func (*javaVuserCapacityGateProtocol) LegacyRuntimeHealthy() bool   { return true }

func TestEngineJavaVuserRuntimeGateRejectsDisabledRunner(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	instance, err := protocol.Get("java_vuser")
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{script: combinedJavaRuntimeGateScript(), vusers: 1}
	if err := engine.validateJavaVuserRuntime(context.Background(), instance); err == nil || !strings.Contains(err.Error(), "runtime is unavailable") {
		t.Fatalf("validateJavaVuserRuntime = %v, want disabled-runner rejection", err)
	}
}

func TestEngineJavaVuserRuntimeGateEnforcesPersistentVUserCapacity(t *testing.T) {
	instance := &javaVuserCapacityGateProtocol{maxVUsers: 1}
	engine := &Engine{script: combinedJavaRuntimeGateScript(), vusers: 2}
	if err := engine.validateJavaVuserRuntime(context.Background(), instance); err == nil || !strings.Contains(err.Error(), "capacity exceeded") {
		t.Fatalf("validateJavaVuserRuntime = %v, want persistent capacity rejection", err)
	}
}

// Shared JVMs and the runner's memory budget can leave fewer dedicated JVMs
// than process slots.
func TestEngineJavaVuserRuntimeGateUsesTheDedicatedCeiling(t *testing.T) {
	ceiling := int64(1)
	instance := &javaVuserCapacityGateProtocol{maxVUsers: 4, ceiling: &ceiling}
	engine := &Engine{script: combinedJavaRuntimeGateScript(), vusers: 2}
	if err := engine.validateJavaVuserRuntime(context.Background(), instance); err == nil || !strings.Contains(err.Error(), "requires 2 VUsers, maximum 1") {
		t.Fatalf("validateJavaVuserRuntime = %v, want the runner's dedicated ceiling", err)
	}
	engine.vusers = 1
	if err := engine.validateJavaVuserRuntime(context.Background(), instance); err != nil {
		t.Fatalf("demand within the ceiling: %v", err)
	}
}

func TestEngineJavaVuserRuntimeGateRejectsTokenlessPersistentRunner(t *testing.T) {
	tokenless := &javaVuserCapacityGateProtocol{
		maxVUsers: 2,
		features:  []string{protocol.FeatureJavaVuserPersistentLifecycleV1},
	}
	engine := &Engine{script: combinedJavaRuntimeGateScript(), vusers: 1}
	if err := engine.validateJavaVuserRuntime(context.Background(), tokenless); err == nil ||
		!strings.Contains(err.Error(), protocol.FeatureJavaVuserSessionFrameTokenV1) {
		t.Fatalf("validateJavaVuserRuntime(tokenless) = %v, want governed persistent capability rejection", err)
	}
}

func TestEngineJavaVuserLegacyOneShotBypassesPersistentVUserCapacity(t *testing.T) {
	instance := &javaVuserCapacityGateProtocol{maxVUsers: 1}
	script := combinedJavaRuntimeGateScript()
	script.Actions[0].Params["method"] = "full_lifecycle"
	engine := &Engine{script: script, vusers: 100}
	if err := engine.validateJavaVuserRuntime(context.Background(), instance); err != nil {
		t.Fatalf("validateJavaVuserRuntime(one-shot) = %v, want pinned-capacity bypass", err)
	}
}

func TestMultiGroupRunRevalidatesRuntimeAtActualStart(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "false")
	mge, err := NewMultiGroup(MultiGroupConfig{
		// Model a previously accepted staging/preflight observation. Run must
		// not trust it across a coordinated future-start delay.
		RuntimePreflightValidated: true,
		Duration:                  time.Second,
		Groups: []GroupConfig{{
			Name:     "java",
			Script:   combinedJavaRuntimeGateScript(),
			VUsers:   1,
			RampDown: time.Millisecond,
		}},
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	if _, err := mge.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "runtime is unavailable") {
		t.Fatalf("Run = %v, want fresh actual-start runtime rejection", err)
	}
}

func combinedJavaRuntimeGateScript() *dsl.Script {
	return &dsl.Script{
		Protocol: "java_vuser",
		Actions: []dsl.Action{{
			Type:   "java_vuser_run",
			Target: "Actions",
			Params: map[string]any{"method": "action"},
		}},
	}
}
