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
