package java_vuser

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
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

func TestRuntimeCapabilitiesReportsSharedJVMCapacity(t *testing.T) {
	resetSidecarRuntimeCaches()
	t.Cleanup(resetSidecarRuntimeCaches)
	base := []string{protocol.FeatureJavaVuserPersistentLifecycleV1, javasidecar.FeatureSessionFrameTokenV1, javasidecar.FeatureOwnerFencingV1, javasidecar.FeatureExternalRuntimeV1, javasidecar.FeatureRunnerOwnerLeaseV1, javasidecar.FeatureRuntimeLeaseV1}
	for name, tc := range map[string]struct {
		features []string
		shared   *javasidecar.SharedJVMCapabilities
		want     *protocol.SharedJVMCapacity
	}{
		"advertised":       {append(append([]string(nil), base...), javasidecar.FeatureSharedJVMV1), &javasidecar.SharedJVMCapabilities{VUsersPerJVM: 250, MaxJVMs: 25, MaxVUsers: 6250, AvailableVUsers: 6000}, &protocol.SharedJVMCapacity{VUsersPerJVM: 250, MaxVUsers: 6250, AvailableVUsers: 6000}},
		"feature missing":  {base, &javasidecar.SharedJVMCapabilities{VUsersPerJVM: 250, MaxVUsers: 6250}, nil},
		"envelope missing": {append(append([]string(nil), base...), javasidecar.FeatureSharedJVMV1), nil, nil},
		"available capped": {append(append([]string(nil), base...), javasidecar.FeatureSharedJVMV1), &javasidecar.SharedJVMCapabilities{VUsersPerJVM: 2, MaxVUsers: 10, AvailableVUsers: 99}, &protocol.SharedJVMCapacity{VUsersPerJVM: 2, MaxVUsers: 10, AvailableVUsers: 10}},
	} {
		resetSidecarRuntimeCaches()
		runner := JavaVuser{
			cfg:     configuration{Enabled: true, RunnerMode: config.JavaVuserRunnerModeSidecar, RunnerURL: "http://127.0.0.1:18099/" + strings.ReplaceAll(name, " ", "-"), RunnerCommand: defaultRunnerCommand, AuthToken: "token-0123456789abcdef"},
			sidecar: &fakeJavaVuserSidecarClient{capabilitiesResponse: javasidecar.CapabilitiesResponse{Healthy: true, JavaMajor: 21, MaxProcesses: 25, Features: tc.features, SharedJVM: tc.shared}},
		}
		capabilities := runner.RuntimeCapabilities()
		if !capabilities.Available || capabilities.MaxVUsers != 25 {
			t.Fatalf("%s: dedicated capacity changed %+v", name, capabilities)
		}
		if (capabilities.SharedJVM == nil) != (tc.want == nil) || tc.want != nil && *capabilities.SharedJVM != *tc.want {
			t.Fatalf("%s: shared %+v, want %+v", name, capabilities.SharedJVM, tc.want)
		}
	}
}
