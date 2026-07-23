package k8s_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"testing"

	"gopkg.in/yaml.v3"
)

type ingressManifest struct {
	Kind string `yaml:"kind"`
	Spec struct {
		Rules []struct {
			HTTP struct {
				Paths []struct {
					Path    string `yaml:"path"`
					Backend struct {
						Service struct {
							Name string `yaml:"name"`
						} `yaml:"service"`
					} `yaml:"backend"`
				} `yaml:"paths"`
			} `yaml:"http"`
		} `yaml:"rules"`
	} `yaml:"spec"`
}

func assertIngressActiveControllerRoutes(t *testing.T, input io.Reader) {
	t.Helper()
	decoder := yaml.NewDecoder(input)
	want := map[string]bool{
		"/api/v1/ci":                         false,
		"/api/v1/tests":                      false,
		"/api/v1/scenarios":                  false,
		"/api/v1/schedules/health":           false,
		"/api/v1/execution-plans":            false,
		"/api/v1/capacity/timeslots":         false,
		"/api/v1/system/execution-readiness": false,
		"/api/v1/scripts":                    false,
		"/api/v1/replay":                     false,
		"/api/v1/script-debug":               false,
		"/api/v1/browser-replay":             false,
		"/api/v1/recorder":                   false,
		"/api/v1/browser-recorder":           false,
		"/api/v1/ws":                         false,
	}
	for {
		var manifest ingressManifest
		err := decoder.Decode(&manifest)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode rendered manifest: %v", err)
		}
		if manifest.Kind != "Ingress" {
			continue
		}
		for _, rule := range manifest.Spec.Rules {
			for _, path := range rule.HTTP.Paths {
				if _, required := want[path.Path]; required && path.Backend.Service.Name == "apexload-controller-active" {
					want[path.Path] = true
				}
			}
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("rendered ingress does not route %s to apexload-controller-active", path)
		}
	}
}

func TestProductionIngressesRouteExecutionReplayAndRealtimeLifecyclesToActiveController(t *testing.T) {
	t.Run("AKS manifest", func(t *testing.T) {
		file, err := os.Open("aks/controller-ingress.yaml")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		assertIngressActiveControllerRoutes(t, file)
	})

	t.Run("onprem manifest", func(t *testing.T) {
		file, err := os.Open("onprem/controller-ingress.yaml")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		assertIngressActiveControllerRoutes(t, file)
	})

	t.Run("helm rendered manifest", func(t *testing.T) {
		helm, err := exec.LookPath("helm")
		if err != nil {
			t.Skip("helm is required to render the chart")
		}
		command := exec.Command(helm, "template", "apexload", "../helm/apexload",
			"--set", "image.registry=registry.example.net",
			"--set", "ingress.host=apexload.example.net",
			"--set", "dataPlatform.azureStorageAccount=apexloadstorage",
			"--set", "dataPlatform.keyVaultUrl=https://apexload.vault.azure.net",
			"--set", "workloadIdentity.controllerClientId=00000000-0000-0000-0000-000000000001",
			"--set", "workloadIdentity.generatorClientId=00000000-0000-0000-0000-000000000002",
		)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("helm template: %v\n%s", err, output)
		}
		assertIngressActiveControllerRoutes(t, bytes.NewReader(output))
	})
}
