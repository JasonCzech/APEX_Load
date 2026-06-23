package k8s_test

import (
	"os"
	"path/filepath"
	"regexp"
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

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
