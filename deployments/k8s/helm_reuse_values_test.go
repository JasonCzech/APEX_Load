package k8s_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestHelmUpgradeDefaultsMatchChartDefaults(t *testing.T) {
	read := func(path string) map[string]any {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if err := yaml.Unmarshal(raw, &values); err != nil {
			t.Fatal(err)
		}
		return values
	}
	defaults := read("../helm/apexload/values.yaml")
	for section, fallback := range read("../helm/apexload/defaults/upgrade.yaml") {
		if !reflect.DeepEqual(defaults[section], fallback) {
			t.Errorf("upgrade fallback for %s differs from values.yaml", section)
		}
	}
}

func TestHelmHistoricalReuseValuesUpgrade(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required")
	}
	// Exact chart values from f757e964ba455ffb603028b6310bd09af418853d,
	// September 15, 2026. Replacing chart defaults models --reuse-values,
	// which does not merge newly introduced defaults into a stored release.
	legacy, err := os.ReadFile("testdata/helm-values-2026-09-15.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		args      []string
		http3     string
		preflight string
		archive   bool
		egress    bool
		ingress   bool
	}{
		{name: "missing sections", http3: "false", preflight: "report", ingress: true},
		{name: "partial sections", args: []string{"http3.enabled=true", "archiveStaging.persistence.enabled=true", "networkPolicy.egress.enabled=true"}, http3: "true", preflight: "report", archive: true, egress: true, ingress: true},
		{name: "explicit false", args: []string{"http3.enabled=false", "http3.quic.disablePathMTUDiscovery=false", "archiveStaging.persistence.enabled=false", "networkPolicy.enabled=false", "networkPolicy.egress.enabled=false"}, http3: "false", preflight: "report"},
		{name: "custom nested values", args: []string{"http3.preflight=off", "http3.quic.initialPacketSize=1300", "archiveStaging.persistence.enabled=true", "archiveStaging.persistence.size=48Gi", "networkPolicy.egress.enabled=true"}, http3: "false", preflight: "off", archive: true, egress: true, ingress: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chart := filepath.Join(t.TempDir(), "apexload")
			if err := os.CopyFS(chart, os.DirFS("../helm/apexload")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(chart, "values.yaml"), legacy, 0600); err != nil {
				t.Fatal(err)
			}
			args := archiveStagingHelmArgs()
			args[2] = chart
			for _, value := range tc.args {
				args = append(args, "--set", value)
			}
			// Empty lists are deliberate operator choices, even with defaults.
			args = append(args, "--set-json", "networkPolicy.egress.targets.cidrs=[]")
			output, err := exec.Command(helm, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("render reused release: %v\n%s", err, output)
			}
			docs := decodeWorkloadDocuments(t, output)
			controller := findWorkload(t, docs, "Deployment", "apexload-controller")
			generator := findWorkload(t, docs, "StatefulSet", "apexload-generator")
			for role, env := range map[string]map[string]workloadEnv{
				"controller": containerEnv(controller, "controller"),
				"generator":  containerEnv(generator, "generator"),
			} {
				if env["APEXLOAD_HTTP3"].Value != tc.http3 {
					t.Errorf("%s HTTP/3 = %q, want %q", role, env["APEXLOAD_HTTP3"].Value, tc.http3)
				}
			}
			if got := containerEnv(controller, "controller")["APEXLOAD_HTTP3_PREFLIGHT"].Value; got != tc.preflight {
				t.Errorf("preflight = %q, want %q", got, tc.preflight)
			}
			if hasWorkload(docs, "NetworkPolicy", "apexload-controller-egress") != tc.egress || hasWorkload(docs, "NetworkPolicy", "apexload-controller-ingress") != tc.ingress {
				t.Fatal("network policy flags did not retain the release settings")
			}
			if bytes.Contains(output, []byte("cidr: 0.0.0.0/0")) || bytes.Contains(output, []byte("cidr: ::/0")) {
				t.Fatal("empty target list was replaced by defaults")
			}
			claim := ""
			if tc.archive {
				claim = "apexload-controller-archive-staging"
			}
			assertArchiveStagingMount(t, decodeArchiveStagingDocuments(t, output), tc.archive, claim)
		})
	}
}
