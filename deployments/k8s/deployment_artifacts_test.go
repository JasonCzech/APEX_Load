package k8s_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var placeholderPattern = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)

// TestRenderScriptHandlesEveryOverlayPlaceholder guards B10: every ${PLACEHOLDER}
// used in an AKS overlay must have a matching substitution rule in
// scripts/render-aks-manifests.sh. Without this, a placeholder like
// ${KEDA_WORKLOAD_IDENTITY_CLIENT_ID} (aks-azure-monitor TriggerAuthentication)
// silently survives rendering and the KEDA scaler fails workload-identity auth,
// stalling generator autoscaling.
func TestRenderScriptHandlesEveryOverlayPlaceholder(t *testing.T) {
	renderScript := readTextFile(t, "../../scripts/render-aks-manifests.sh")

	overlayDirs := []string{"aks", "aks-azure-monitor", "aks-prometheus", "onprem-autoscaling"}
	seen := map[string]bool{}
	for _, dir := range overlayDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // overlay is optional; skip if absent
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			content := readTextFile(t, filepath.Join(dir, entry.Name()))
			for _, m := range placeholderPattern.FindAllStringSubmatch(content, -1) {
				name := m[1]
				if seen[name] {
					continue
				}
				seen[name] = true
				if !strings.Contains(renderScript, name) {
					t.Errorf("overlay placeholder ${%s} (in %s/%s) has no substitution rule in render-aks-manifests.sh", name, dir, entry.Name())
				}
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no ${...} placeholders found across AKS overlays; test wiring is likely broken")
	}
}

func TestFreshClusterBasesDoNotIncludeOptionalCRDResources(t *testing.T) {
	tests := []struct {
		path      string
		forbidden []string
	}{
		{
			path:      "local/kustomization.yaml",
			forbidden: []string{"generator-keda-scaledobject.yaml", "servicemonitor.yaml"},
		},
		{
			path:      "onprem/kustomization.yaml",
			forbidden: []string{"generator-keda-scaledobject.yaml", "servicemonitor.yaml"},
		},
		{
			path:      "aks/kustomization.yaml",
			forbidden: []string{"servicemonitor.yaml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			content := readTextFile(t, tt.path)
			for _, forbidden := range tt.forbidden {
				if strings.Contains(content, forbidden) {
					t.Fatalf("%s includes optional CRD-backed resource %q", tt.path, forbidden)
				}
			}
		})
	}
}

func TestKEDAScaledObjectsProtectActiveDemand(t *testing.T) {
	for _, path := range []string{
		"aks/generator-keda-scaledobject.yaml",
		"onprem/generator-keda-scaledobject.yaml",
		"local/generator-keda-scaledobject.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			content := readTextFile(t, path)
			checks := []string{
				"metricName: apexload_active_vusers",
				`query: sum(apexload_scaling_vusers{state="active"})`,
				"metricName: apexload_active_browser_vusers",
				`query: sum(apexload_scaling_protocol_vusers{state="active",protocol="browser"})`,
			}
			for _, check := range checks {
				if !strings.Contains(content, check) {
					t.Fatalf("%s missing %q", path, check)
				}
			}
		})
	}
}

func TestKEDAScaledObjectsKeepScaleDownStabilization(t *testing.T) {
	for _, path := range []string{
		"aks/generator-keda-scaledobject.yaml",
		"onprem/generator-keda-scaledobject.yaml",
		"local/generator-keda-scaledobject.yaml",
		"onprem-autoscaling/generator-keda-scaledobject.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			content := readTextFile(t, path)
			if !strings.Contains(content, "scaleDown:") || !strings.Contains(content, "stabilizationWindowSeconds: 600") {
				t.Fatalf("%s must keep a 600s scaleDown stabilization window", path)
			}
		})
	}
}

func TestGeneratorManifestsUseGRPCHealthProbes(t *testing.T) {
	for _, path := range []string{
		"local/generator-statefulset.yaml",
		"onprem/generator-statefulset.yaml",
		"aks/generator-statefulset.yaml",
		"../helm/apexload/templates/generator.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			content := readTextFile(t, path)
			if !strings.Contains(content, "readinessProbe:") || !strings.Contains(content, "livenessProbe:") || !strings.Contains(content, "grpc:") {
				t.Fatalf("%s must use gRPC readiness/liveness probes", path)
			}
			if strings.Contains(content, "sleep 15") {
				t.Fatalf("%s must not use blind preStop sleep for generator drain", path)
			}
		})
	}
}

func TestHAOverlaysSetReplicasAndFlagTogether(t *testing.T) {
	for _, path := range []string{"onprem-ha/kustomization.yaml", "aks-ha/kustomization.yaml"} {
		t.Run(path, func(t *testing.T) {
			content := readTextFile(t, path)
			for _, check := range []string{"path: /spec/replicas", "value: 2", "APEXLOAD_CONTROLLER_HA", `value: "true"`} {
				if !strings.Contains(content, check) {
					t.Fatalf("%s missing HA invariant %q", path, check)
				}
			}
		})
	}
}

func TestAKSRawApplyUsesRenderer(t *testing.T) {
	content := readTextFile(t, "../../Makefile")
	if !strings.Contains(content, "k8s-aks-apply: k8s-aks-render") {
		t.Fatalf("Makefile must make k8s-aks-apply depend on k8s-aks-render")
	}
	if strings.Contains(content, "k8s-aks-apply:\n\tkubectl apply -k deployments/k8s/aks") {
		t.Fatalf("Makefile regressed to raw AKS kustomize apply")
	}
}

func TestDockerLocalRunsAsNonRoot(t *testing.T) {
	dockerfile := readTextFile(t, "../../Dockerfile")
	for _, check := range []string{"chown -R 65532:65532", "USER 65532:65532"} {
		if !strings.Contains(dockerfile, check) {
			t.Fatalf("Dockerfile missing %q", check)
		}
	}
	compose := readTextFile(t, "../../compose.yaml")
	for _, check := range []string{`user: "65532:65532"`, "no-new-privileges:true", "read_only: true"} {
		if !strings.Contains(compose, check) {
			t.Fatalf("compose.yaml missing %q", check)
		}
	}
}

func TestDockerComposeIncludesGeneratorFleet(t *testing.T) {
	compose := readTextFile(t, "../../compose.yaml")
	for _, check := range []string{"generator:", "BINARY: generator", "APEXLOAD_GENERATORS: dns://generator:9090", "APEXLOAD_GENERATOR_MAX_VUSERS"} {
		if !strings.Contains(compose, check) {
			t.Fatalf("compose.yaml missing generator fleet check %q", check)
		}
	}
}

func TestLocalKubernetesUsesDNSFleetDiscovery(t *testing.T) {
	controller := readTextFile(t, "local/controller-deployment.yaml")
	for _, check := range []string{
		"APEXLOAD_GENERATORS",
		"dns://apexload-generator.apexload-local.svc.cluster.local:9090",
	} {
		if !strings.Contains(controller, check) {
			t.Fatalf("local controller manifest missing DNS discovery check %q", check)
		}
	}
	if strings.Contains(controller, "apexload-generator-0.apexload-generator") || strings.Contains(controller, "apexload-generator-1.apexload-generator") {
		t.Fatalf("local controller manifest must not hard-code generator StatefulSet ordinals")
	}

	service := readTextFile(t, "local/generator-service.yaml")
	for _, check := range []string{"clusterIP: None", "publishNotReadyAddresses: false"} {
		if !strings.Contains(service, check) {
			t.Fatalf("local generator Service missing %q", check)
		}
	}

	kind := readTextFile(t, "local-kind/kustomization.yaml")
	if !strings.Contains(kind, "../local") {
		t.Fatalf("local-kind overlay must inherit the DNS-discovery local base")
	}
}

func TestTerraformValidatesAKSGeneratorCapacity(t *testing.T) {
	variables := readTextFile(t, "../azure/terraform/variables.tf")
	main := readTextFile(t, "../azure/terraform/main.tf")
	aks := readTextFile(t, "../azure/terraform/aks.tf")

	for _, check := range []string{
		`variable "generator_keda_max_replicas"`,
		`variable "generator_pod_cpu_millicores"`,
		`variable "generator_pod_memory_mib"`,
		`variable "generator_node_reserved_fraction"`,
		"generator_schedulable_pods",
		"local.generator_schedulable_pods >= var.generator_keda_max_replicas",
	} {
		if !strings.Contains(variables+main+aks, check) {
			t.Fatalf("terraform generator capacity validation missing %q", check)
		}
	}

	keda := readTextFile(t, "aks/generator-keda-scaledobject.yaml")
	kedaMax, err := findYAMLInt(keda, "maxReplicaCount")
	if err != nil {
		t.Fatal(err)
	}
	tfKedaMax, err := findTerraformDefaultNumber(variables, "generator_keda_max_replicas")
	if err != nil {
		t.Fatal(err)
	}
	if tfKedaMax != kedaMax {
		t.Fatalf("terraform generator_keda_max_replicas = %d, want AKS KEDA maxReplicaCount %d", tfKedaMax, kedaMax)
	}

	statefulSet := readTextFile(t, "aks/generator-statefulset.yaml")
	cpu, err := findYAMLResourceRequest(statefulSet, "cpu")
	if err != nil {
		t.Fatal(err)
	}
	if tfCPU, err := findTerraformDefaultNumber(variables, "generator_pod_cpu_millicores"); err != nil {
		t.Fatal(err)
	} else if tfCPU != cpu {
		t.Fatalf("terraform generator_pod_cpu_millicores = %d, want AKS request %d", tfCPU, cpu)
	}

	memory, err := findYAMLResourceRequest(statefulSet, "memory")
	if err != nil {
		t.Fatal(err)
	}
	if tfMemory, err := findTerraformDefaultNumber(variables, "generator_pod_memory_mib"); err != nil {
		t.Fatal(err)
	} else if tfMemory != memory {
		t.Fatalf("terraform generator_pod_memory_mib = %d, want AKS request %d", tfMemory, memory)
	}
}

func findYAMLInt(content string, key string) (int, error) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*([0-9]+)\b`)
	match := re.FindStringSubmatch(content)
	if match == nil {
		return 0, fmt.Errorf("missing YAML integer %s", key)
	}
	return strconv.Atoi(match[1])
}

func findTerraformDefaultNumber(content string, variable string) (int, error) {
	re := regexp.MustCompile(`(?s)variable\s+"` + regexp.QuoteMeta(variable) + `"\s+\{.*?default\s+=\s+([0-9]+)`)
	match := re.FindStringSubmatch(content)
	if match == nil {
		return 0, fmt.Errorf("missing Terraform numeric default for %s", variable)
	}
	return strconv.Atoi(match[1])
}

func findYAMLResourceRequest(content string, name string) (int, error) {
	re := regexp.MustCompile(`(?s)resources:\s*\n\s*requests:\s*\n(?P<body>.*?)(?:\n\s*limits:)`)
	match := re.FindStringSubmatch(content)
	if match == nil {
		return 0, fmt.Errorf("missing resources.requests block")
	}
	body := match[1]
	valueRe := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*"?([^"\n]+)"?\s*$`)
	valueMatch := valueRe.FindStringSubmatch(body)
	if valueMatch == nil {
		return 0, fmt.Errorf("missing resource request %s", name)
	}
	return parseKubernetesResourceQuantity(valueMatch[1])
}

func parseKubernetesResourceQuantity(value string) (int, error) {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasSuffix(value, "m"):
		return strconv.Atoi(strings.TrimSuffix(value, "m"))
	case strings.HasSuffix(value, "Gi"):
		n, err := strconv.Atoi(strings.TrimSuffix(value, "Gi"))
		return n * 1024, err
	case strings.HasSuffix(value, "Mi"):
		return strconv.Atoi(strings.TrimSuffix(value, "Mi"))
	default:
		n, err := strconv.Atoi(value)
		if err != nil {
			return 0, err
		}
		return n * 1000, nil
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
