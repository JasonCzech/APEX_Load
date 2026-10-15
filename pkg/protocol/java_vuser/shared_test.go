package java_vuser

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
)

func TestPrepareSharedJVMRuntimeDropsUnsafeLabels(t *testing.T) {
	t.Setenv(config.EnvJavaVuserRunnerEnabled, "true")
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeSidecar)
	t.Setenv(config.EnvJavaVuserRunnerURL, "http://127.0.0.1:18098")
	t.Setenv(config.EnvJavaVuserRunnerAuthToken, "token-0123456789abcdef")
	cfg := map[string]any{"group_name": strings.Repeat("g", 257), "scenario_id": "scenario\nid", "java_vuser_additional_vm_parameters": []string{"-Xmx1g"}}
	rt, reason, err := PrepareSharedJVMRuntime(context.Background(), cfg, " Actions ", strings.Repeat("a", 64), 0)
	if err != nil || reason != "" {
		t.Fatalf("prepare %q %v", reason, err)
	}
	if rt.Claim.GroupName != "" || rt.Claim.ScenarioID != "" || rt.Claim.TargetClass != "Actions" || rt.Client == nil {
		t.Fatalf("claim %+v", rt.Claim)
	}
	if strings.Join(rt.Claim.Runtime.AdditionalVMParameters, " ") != "-Xmx1g" {
		t.Fatalf("runtime %+v", rt.Claim.Runtime)
	}
	cfg["group_name"], cfg["scenario_id"] = "Checkout", "scenario-1"
	if rt, _, _ = PrepareSharedJVMRuntime(context.Background(), cfg, "Actions", strings.Repeat("a", 64), 0); rt.Claim.GroupName != "Checkout" || rt.Claim.ScenarioID != "scenario-1" {
		t.Fatalf("labels %+v", rt.Claim)
	}
	t.Setenv(config.EnvJavaVuserRunnerMode, config.JavaVuserRunnerModeLocal)
	if _, reason, _ := PrepareSharedJVMRuntime(context.Background(), cfg, "Actions", strings.Repeat("a", 64), 0); reason != SharedJVMReasonNeedsSidecar {
		t.Fatalf("local runner reason %q", reason)
	}
}
