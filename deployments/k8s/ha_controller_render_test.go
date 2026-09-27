package k8s_test

import (
	"bytes"
	"io"
	"os/exec"
	"testing"

	"gopkg.in/yaml.v3"
)

// haControllerDocument captures the fields of the HA controller manifests that
// carry the API port. Decoding through YAML is the point of the test: an
// indentation slip in the chart makes the whole document unparseable, which
// `helm template` rejects before anything reaches the cluster.
type haControllerDocument struct {
	Kind     string            `yaml:"kind"`
	Data     map[string]string `yaml:"data"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	AddressType string `yaml:"addressType"`
	Ports       []struct {
		Name     string `yaml:"name"`
		Protocol string `yaml:"protocol"`
		Port     int    `yaml:"port"`
	} `yaml:"ports"`
	Spec struct {
		Triggers []struct {
			Metadata map[string]string `yaml:"metadata"`
		} `yaml:"triggers"`
		Ports []struct {
			Name string `yaml:"name"`
			Port int    `yaml:"port"`
		} `yaml:"ports"`
	} `yaml:"spec"`
}

func renderHAChart(t *testing.T, extraArgs ...string) []haControllerDocument {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required to render the chart")
	}
	args := []string{
		"template", "apexload", "../helm/apexload",
		"--set", "image.registry=registry.example.net",
		"--set", "ingress.host=apexload.example.net",
		"--set", "dataPlatform.azureStorageAccount=apexloadstorage",
		"--set", "dataPlatform.keyVaultUrl=https://apexload.vault.azure.net",
		"--set", "workloadIdentity.controllerClientId=00000000-0000-0000-0000-000000000001",
		"--set", "workloadIdentity.generatorClientId=00000000-0000-0000-0000-000000000002",
		"--set", "controller.ha.enabled=true",
		"--set", "dataPlatform.store=postgres",
		"--set", "importAssets.persistence.existingClaim=apexload-import-assets",
	}
	command := exec.Command(helm, append(args, extraArgs...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, output)
	}

	var documents []haControllerDocument
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	for {
		var document haControllerDocument
		if err := decoder.Decode(&document); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode rendered manifest: %v\n%s", err, output)
		}
		documents = append(documents, document)
	}
	return documents
}

func TestHAControllerRendersParseableEndpointSlice(t *testing.T) {
	documents := renderHAChart(t)

	found := false
	for _, document := range documents {
		if document.Kind != "EndpointSlice" {
			continue
		}
		found = true
		if document.AddressType == "" {
			t.Errorf("EndpointSlice %q has no addressType", document.Metadata.Name)
		}
		// ports is a top-level EndpointSlice field, not nested under spec.
		if len(document.Ports) != 1 {
			t.Fatalf("EndpointSlice %q ports = %#v, want exactly one top-level http port",
				document.Metadata.Name, document.Ports)
		}
		if document.Ports[0].Name != "http" || document.Ports[0].Protocol != "TCP" || document.Ports[0].Port != 8080 {
			t.Errorf("EndpointSlice port = %#v, want http/TCP/8080", document.Ports[0])
		}
	}
	if !found {
		t.Fatal("no EndpointSlice rendered with controller.ha.enabled=true")
	}
}

// controller.addr drives the container port, both Service ports, the
// EndpointSlice port, and the NetworkPolicy rule, so a custom value must reach
// every one of them.
func TestHAControllerPropagatesCustomAddrPort(t *testing.T) {
	documents := renderHAChart(t, "--set", "controller.addr=:9443")

	sawEndpointSlice := false
	sawService := false
	for _, document := range documents {
		switch document.Kind {
		case "EndpointSlice":
			sawEndpointSlice = true
			if len(document.Ports) != 1 || document.Ports[0].Port != 9443 {
				t.Errorf("EndpointSlice ports = %#v, want port 9443", document.Ports)
			}
		case "Service":
			for _, port := range document.Spec.Ports {
				if port.Name == "http" {
					sawService = true
					if port.Port != 9443 {
						t.Errorf("Service %q http port = %d, want 9443", document.Metadata.Name, port.Port)
					}
				}
			}
		}
	}
	if !sawEndpointSlice || !sawService {
		t.Fatalf("expected both an EndpointSlice and an http Service (endpointSlice=%v service=%v)",
			sawEndpointSlice, sawService)
	}
}

// A bind address with no port would silently render port 0 into every manifest,
// so the chart must fail the render instead.
func TestControllerAddrWithoutPortIsRejected(t *testing.T) {
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
		"--set", "controller.addr=0.0.0.0",
	)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("expected a portless controller.addr to fail the render, got:\n%s", output)
	}
}
