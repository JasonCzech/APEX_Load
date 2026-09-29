package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKEDAFleetIdentityRendersThroughCloudHAOverlays(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl is required")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "terraform"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	for key, value := range map[string]string{
		"APEXLOAD_ACR_LOGIN_SERVER": "registry.example.net", "APEXLOAD_STORAGE_ACCOUNT_NAME": "teststorage",
		"APEXLOAD_KEY_VAULT_URL": "https://test.vault.azure.net", "APEXLOAD_CONTROLLER_WORKLOAD_IDENTITY_CLIENT_ID": "11111111-1111-4111-8111-111111111111",
		"APEXLOAD_GENERATOR_WORKLOAD_IDENTITY_CLIENT_ID": "22222222-2222-4222-8222-222222222222",
		"APEXLOAD_AWS_REGION":                            "us-east-1", "APEXLOAD_ECR_CONTROLLER_REPOSITORY_URL": "registry.example.net/apexload-controller",
		"APEXLOAD_ECR_GENERATOR_REPOSITORY_URL": "registry.example.net/apexload-generator", "APEXLOAD_ARCHIVE_BUCKET_NAME": "test-archives",
		"APEXLOAD_WRAPPING_KMS_KEY_ARN":     "arn:aws:kms:us-east-1:123456789012:key/test-key",
		"APEXLOAD_CONTROLLER_IRSA_ROLE_ARN": "arn:aws:iam::123456789012:role/test-controller", "APEXLOAD_GENERATOR_IRSA_ROLE_ARN": "arn:aws:iam::123456789012:role/test-generator",
	} {
		t.Setenv(key, value)
	}
	for _, overlay := range []string{"aks", "aks-ha", "eks", "eks-ha"} {
		t.Run(overlay, func(t *testing.T) {
			t.Setenv("APEXLOAD_FLEET_ID", "test-cluster/"+overlay)
			output := filepath.Join(t.TempDir(), "rendered.yaml")
			command := exec.Command("bash", "render-"+overlay[:3]+"-manifests.sh", "--overlay-dir", "../deployments/k8s/"+overlay, "--output", output, "--image-tag", "fleet-test")
			if data, err := command.CombinedOutput(); err != nil {
				t.Fatalf("render: %v\n%s", err, data)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, "APEXLOAD_FLEET_ID: test-cluster/"+overlay) || !strings.Contains(text, `fleet="test-cluster/`+overlay+`"`) {
				t.Fatalf("controller and KEDA identities do not match:\n%s", text)
			}
			if !strings.Contains(text, "apexload-controller:fleet-test") {
				t.Fatal("image tag substitution was lost")
			}
		})
	}
	t.Setenv("APEXLOAD_FLEET_ID", `bad"selector`)
	command := exec.Command("bash", "render-aks-manifests.sh", "--output", filepath.Join(root, "invalid.yaml"))
	if data, err := command.CombinedOutput(); err == nil || !strings.Contains(string(data), "set APEXLOAD_FLEET_ID") {
		t.Fatalf("invalid identity not rejected: %v %s", err, data)
	}
}

func TestKEDAFleetIdentityRendersThroughOnPremOverlays(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl is required")
	}
	for _, overlay := range []string{"onprem", "onprem-ha", "onprem-autoscaling", "onprem-ha-autoscaling"} {
		t.Run(overlay, func(t *testing.T) {
			t.Setenv("APEXLOAD_FLEET_ID", "test-cluster/"+overlay)
			output := filepath.Join(t.TempDir(), "rendered.yaml")
			command := exec.Command("bash", "render-onprem-manifests.sh", "--overlay-dir", "../deployments/k8s/"+overlay, "--output", output)
			if data, err := command.CombinedOutput(); err != nil {
				t.Fatalf("render: %v\n%s", err, data)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, `APEXLOAD_FLEET_ID: "test-cluster/`+overlay+`"`) || strings.Contains(text, "${APEXLOAD_FLEET_ID}") {
				t.Fatalf("controller fleet identity not rendered for %s", overlay)
			}
			if strings.Contains(overlay, "autoscaling") {
				if !strings.Contains(text, `fleet="test-cluster/`+overlay+`"`) || !strings.Contains(text, "APEXLOAD_GENERATOR_AUTOSCALING_OWNER: keda") {
					t.Fatalf("KEDA identity or owner does not match controller for %s", overlay)
				}
			}
			if overlay == "onprem-ha-autoscaling" && (!strings.Contains(text, "replicas: 2") || !strings.Contains(text, "APEXLOAD_CONTROLLER_HA")) {
				t.Fatal("combined overlay lost controller HA")
			}
			repeat := exec.Command("bash", "render-onprem-manifests.sh", "--overlay-dir", "../deployments/k8s/"+overlay, "--output", output)
			if data, err := repeat.CombinedOutput(); err == nil || !strings.Contains(string(data), "output already exists") {
				t.Fatalf("overwrite was not refused: %v %s", err, data)
			}
		})
	}
	for _, fleet := range []string{"", `bad"selector`, " leading", strings.Repeat("x", 129), "change-me", "site-CHANGE-ME-1"} {
		t.Run("invalid-"+fleet, func(t *testing.T) {
			t.Setenv("APEXLOAD_FLEET_ID", fleet)
			output := filepath.Join(t.TempDir(), "invalid.yaml")
			command := exec.Command("bash", "render-onprem-manifests.sh", "--output", output)
			if data, err := command.CombinedOutput(); err == nil || !strings.Contains(string(data), "set APEXLOAD_FLEET_ID") {
				t.Fatalf("invalid fleet ID was accepted: %v %s", err, data)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("invalid render wrote output")
			}
		})
	}
	for _, fleet := range []string{"true", "FALSE", "null", "123", "0x10", "1e3"} {
		t.Run("yaml-scalar-"+fleet, func(t *testing.T) {
			t.Setenv("APEXLOAD_FLEET_ID", fleet)
			output := filepath.Join(t.TempDir(), "rendered.yaml")
			command := exec.Command("bash", "render-onprem-manifests.sh", "--overlay-dir", "../deployments/k8s/onprem-autoscaling", "--output", output)
			if data, err := command.CombinedOutput(); err != nil {
				t.Fatalf("render: %v\n%s", err, data)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, `APEXLOAD_FLEET_ID: "`+fleet+`"`) || !strings.Contains(text, `fleet="`+fleet+`"`) {
				t.Fatalf("fleet %q was not rendered as a string in both ConfigMap and KEDA queries", fleet)
			}
		})
	}
	t.Setenv("APEXLOAD_FLEET_ID", "valid-fleet")
	for _, output := range []string{"relative.yaml", filepath.Join(t.TempDir(), "missing-parent", "rendered.yaml")} {
		if output != "relative.yaml" {
			// A dangling symlink must never be followed or overwritten.
			if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(filepath.Dir(output), "absent"), output); err != nil {
				t.Fatal(err)
			}
		}
		command := exec.Command("bash", "render-onprem-manifests.sh", "--output", output)
		if _, err := command.CombinedOutput(); err == nil {
			t.Fatalf("unsafe output path %q accepted", output)
		}
	}
}

func TestOnPremHAKEDAResourcesStayInSync(t *testing.T) {
	for _, name := range []string{"generator-keda-scaledobject.yaml", "servicemonitor.yaml"} {
		withoutHA, err := os.ReadFile(filepath.Join("..", "deployments", "k8s", "onprem-autoscaling", name))
		if err != nil {
			t.Fatal(err)
		}
		withHA, err := os.ReadFile(filepath.Join("..", "deployments", "k8s", "onprem-ha-autoscaling", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(withoutHA) != string(withHA) {
			t.Fatalf("%s differs between on-prem autoscaling overlays", name)
		}
	}
}

func TestOnPremRendererRejectsEditedFleetSelectors(t *testing.T) {
	placeholder := "$" + "{APEXLOAD_FLEET_ID}"
	for _, tc := range []struct {
		name, configID, queryID string
	}{
		{name: "query differs", configID: placeholder, queryID: "other"},
		{name: "config differs", configID: "other", queryID: placeholder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			rendered := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: apexload-data-platform\ndata:\n  APEXLOAD_FLEET_ID: " + tc.configID +
				"\n---\napiVersion: keda.sh/v1alpha1\nkind: ScaledObject\nmetadata:\n  name: apexload-generator\nspec:\n  triggers:\n    - type: prometheus\n      metadata:\n        query: max(apexload_fleet_autoscaling{fleet=\"" + tc.queryID + "\",signal=\"total_vusers\"})\n"
			shim := "#!/bin/sh\ncat <<'YAML'\n" + rendered + "YAML\n"
			if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(shim), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("APEXLOAD_FLEET_ID", "selected-fleet")
			output := filepath.Join(t.TempDir(), "rendered.yaml")
			command := exec.Command("bash", "render-onprem-manifests.sh", "--output", output)
			if data, err := command.CombinedOutput(); err == nil || !strings.Contains(string(data), "fleet ID does not match the selected ID") &&
				!strings.Contains(string(data), "fleet selector does not match the selected ID") {
				t.Fatalf("mismatched fleets were accepted: %v\n%s", err, data)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("rejected render wrote output")
			}
		})
	}
	t.Run("unrelated config map masks controller mismatch", func(t *testing.T) {
		bin := t.TempDir()
		rendered := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: apexload-data-platform\ndata:\n  APEXLOAD_FLEET_ID: other\n" +
			"---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: unrelated\ndata:\n  APEXLOAD_FLEET_ID: ${APEXLOAD_FLEET_ID}\n" +
			"---\napiVersion: keda.sh/v1alpha1\nkind: ScaledObject\nmetadata:\n  name: apexload-generator\nspec:\n  triggers:\n    - type: prometheus\n      metadata:\n        query: max(apexload_fleet_autoscaling{fleet=\"${APEXLOAD_FLEET_ID}\",signal=\"total_vusers\"})\n"
		shim := "#!/bin/sh\ncat <<'YAML'\n" + rendered + "YAML\n"
		if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(shim), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("APEXLOAD_FLEET_ID", "selected-fleet")
		output := filepath.Join(t.TempDir(), "rendered.yaml")
		command := exec.Command("bash", "render-onprem-manifests.sh", "--output", output)
		if data, err := command.CombinedOutput(); err == nil || !strings.Contains(string(data), "controller ConfigMap fleet ID does not match the selected ID") {
			t.Fatalf("unrelated fleet ID hid controller mismatch: %v\n%s", err, data)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("rejected render wrote output")
		}
	})
	for _, tc := range []struct{ name, suffix string }{
		{name: "unscoped term in one query", suffix: " + max(apexload_fleet_autoscaling)"},
		{name: "unscoped second trigger", suffix: "\n    - type: prometheus\n      metadata:\n        query: max(apexload_fleet_autoscaling)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			rendered := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: apexload-data-platform\ndata:\n  APEXLOAD_FLEET_ID: ${APEXLOAD_FLEET_ID}\n" +
				"---\napiVersion: keda.sh/v1alpha1\nkind: ScaledObject\nmetadata:\n  name: apexload-generator\nspec:\n  triggers:\n    - type: prometheus\n      metadata:\n        query: max(apexload_fleet_autoscaling{fleet=\"${APEXLOAD_FLEET_ID}\",signal=\"total_vusers\"})" + tc.suffix + "\n"
			shim := "#!/bin/sh\ncat <<'YAML'\n" + rendered + "YAML\n"
			if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(shim), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("APEXLOAD_FLEET_ID", "selected-fleet")
			output := filepath.Join(t.TempDir(), "rendered.yaml")
			command := exec.Command("bash", "render-onprem-manifests.sh", "--output", output)
			if data, err := command.CombinedOutput(); err == nil || !strings.Contains(string(data), "fleet selector does not match the selected ID") {
				t.Fatalf("unscoped fleet metric was accepted: %v\n%s", err, data)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("rejected render wrote output")
			}
		})
	}
}
