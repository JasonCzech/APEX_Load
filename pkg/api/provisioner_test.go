package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeInventory(entries ...GeneratorInventoryEntry) func(context.Context) SystemInventoryResponse {
	return func(context.Context) SystemInventoryResponse {
		return SystemInventoryResponse{Generators: entries}
	}
}

func TestStaticProvisionerDescribe(t *testing.T) {
	inv := fakeInventory(
		GeneratorInventoryEntry{Target: "gen-1:50051", Region: "us-east", Status: "READY"},
		GeneratorInventoryEntry{Target: "gen-2:50051", Region: "eu-west", Status: "READY"},
		GeneratorInventoryEntry{Target: "gen-3:50051", Region: "us-east", Status: "READY"},
	)
	info := (&staticGeneratorProvisioner{inventory: inv}).Describe()
	if info.Name != staticProvisionerName || info.CanProvision {
		t.Fatalf("static provisioner should not claim on-demand provisioning: %#v", info)
	}
	if len(info.SupportedRegions) != 2 || info.SupportedRegions[0] != "eu-west" || info.SupportedRegions[1] != "us-east" {
		t.Fatalf("regions = %#v, want sorted [eu-west us-east]", info.SupportedRegions)
	}
}

func TestStaticProvisionerProvisionFiltersRegionAndCount(t *testing.T) {
	inv := fakeInventory(
		GeneratorInventoryEntry{Target: "gen-1:50051", Region: "us-east"},
		GeneratorInventoryEntry{Target: "gen-2:50051", Region: "eu-west"},
		GeneratorInventoryEntry{Target: "gen-3:50051", Region: "us-east"},
		GeneratorInventoryEntry{Target: "gen-4:50051", Region: "us-east", DrainState: "draining"},
	)
	p := &staticGeneratorProvisioner{inventory: inv}

	got, err := p.Provision(context.Background(), ProvisionRequest{Region: "us-east", Count: 2})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 generators (count cap), got %d: %#v", len(got), got)
	}
	for _, g := range got {
		if g.Region != "us-east" || g.Ephemeral {
			t.Fatalf("unexpected generator %#v", g)
		}
	}

	// Draining generators are excluded.
	all, _ := p.Provision(context.Background(), ProvisionRequest{Region: "us-east"})
	if len(all) != 2 {
		t.Fatalf("draining generator should be excluded, got %d", len(all))
	}

	// Deprovision is a no-op for the static fleet.
	if err := p.Deprovision(context.Background(), []string{"gen-1:50051"}); err != nil {
		t.Fatalf("static deprovision should be a no-op, got %v", err)
	}
}

func TestResolveGeneratorProvisionerDefaultsAndRegistry(t *testing.T) {
	inv := fakeInventory()
	// Default (no env) -> static.
	if got := resolveGeneratorProvisioner(inv).Describe().Name; got != staticProvisionerName {
		t.Fatalf("default provisioner = %q, want %q", got, staticProvisionerName)
	}
	// Unknown name -> falls back to static.
	t.Setenv(provisionerEnvVar, "does-not-exist")
	if got := resolveGeneratorProvisioner(inv).Describe().Name; got != staticProvisionerName {
		t.Fatalf("unknown provisioner should fall back to static, got %q", got)
	}
	// A registered provisioner is selectable.
	RegisterGeneratorProvisioner("test-cloud", func(func(context.Context) SystemInventoryResponse) GeneratorProvisioner {
		return stubProvisioner{}
	})
	t.Setenv(provisionerEnvVar, "test-cloud")
	if got := resolveGeneratorProvisioner(inv).Describe().Name; got != "test-cloud" {
		t.Fatalf("registered provisioner not selected, got %q", got)
	}
}

type stubProvisioner struct{}

func (stubProvisioner) Describe() ProvisionerInfo {
	return ProvisionerInfo{Name: "test-cloud", CanProvision: true}
}
func (stubProvisioner) Provision(context.Context, ProvisionRequest) ([]ProvisionedGenerator, error) {
	return nil, nil
}
func (stubProvisioner) Deprovision(context.Context, []string) error { return nil }

func TestFleetProvisionerEndpoint(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/provisioner", nil)
	rr := httptest.NewRecorder()
	server.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var info ProvisionerInfo
	if err := json.NewDecoder(rr.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Name == "" {
		t.Fatalf("expected a provisioner name, got %#v", info)
	}
}

func TestKubernetesFleetScalerDisabledWhenKEDAOwnsReplicas(t *testing.T) {
	t.Setenv(provisionerEnvVar, kubernetesProvisionerName)
	t.Setenv(generatorAutoscalingOwnerEnvVar, "keda")

	scaler := newFleetPoolScaler(noopKubeRestarter{}, K8sRestartSettings{
		Enabled:              true,
		Namespace:            "apexload",
		GeneratorStatefulSet: "apexload-generator",
	})
	if scaler.CanProvision() {
		t.Fatalf("KEDA-owned generator replicas must disable controller-side provisioning")
	}
	info := scaler.Describe()
	if info.Name != "keda" || !strings.Contains(info.Description, generatorAutoscalingOwnerEnvVar+"=keda") {
		t.Fatalf("conflict description = %#v, want KEDA ownership explanation", info)
	}
	if _, _, err := scaler.ScalePool(context.Background(), nil, 3); err == nil || !strings.Contains(err.Error(), "owns the generator StatefulSet replicas") {
		t.Fatalf("ScalePool error = %v, want autoscaling owner conflict", err)
	}
}

type noopKubeRestarter struct{}

func (noopKubeRestarter) DeletePod(context.Context, string, string) error { return nil }
func (noopKubeRestarter) RolloutRestart(context.Context, string, string, string) error {
	return nil
}
func (noopKubeRestarter) PodByIP(context.Context, string, string, string) (KubePodRef, error) {
	return KubePodRef{}, nil
}
func (noopKubeRestarter) ListPods(context.Context, string, string) ([]KubePodRef, error) {
	return nil, nil
}
func (noopKubeRestarter) ScaleStatefulSet(context.Context, string, string, int) error { return nil }
