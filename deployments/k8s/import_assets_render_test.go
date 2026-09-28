package k8s_test

import (
	"bytes"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type importAssetsManifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name        string            `yaml:"name"`
		Namespace   string            `yaml:"namespace"`
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
	Data map[string]string `yaml:"data"`
	Spec struct {
		Resources struct {
			Requests map[string]string `yaml:"requests"`
		} `yaml:"resources"`
		AccessModes      []string `yaml:"accessModes"`
		StorageClassName string   `yaml:"storageClassName"`
		Strategy         struct {
			Type          string `yaml:"type"`
			RollingUpdate struct {
				MaxSurge       int `yaml:"maxSurge"`
				MaxUnavailable int `yaml:"maxUnavailable"`
			} `yaml:"rollingUpdate"`
		} `yaml:"strategy"`
		Template struct {
			Spec struct {
				Containers []struct {
					Name string `yaml:"name"`
					Env  []struct {
						Name  string `yaml:"name"`
						Value string `yaml:"value"`
					} `yaml:"env"`
					VolumeMounts []struct {
						Name      string `yaml:"name"`
						MountPath string `yaml:"mountPath"`
					} `yaml:"volumeMounts"`
				} `yaml:"containers"`
				Volumes []struct {
					Name     string `yaml:"name"`
					EmptyDir *struct {
						SizeLimit string `yaml:"sizeLimit"`
					} `yaml:"emptyDir"`
					PersistentVolumeClaim *struct {
						ClaimName string `yaml:"claimName"`
					} `yaml:"persistentVolumeClaim"`
				} `yaml:"volumes"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func TestHelmCustomNamespaceAndEphemeralImportAssetsRenderConsistently(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required to render the chart")
	}
	output, err := exec.Command(helm,
		"template", "custom", "../helm/apexload",
		"--namespace", "release-ns",
		"--set", "namespace.name=resource-ns",
		"--set", "image.registry=registry.example.net",
		"--set", "ingress.host=apexload.example.net",
		"--set", "dataPlatform.azureStorageAccount=apexloadstorage",
		"--set", "dataPlatform.keyVaultUrl=https://apexload.vault.azure.net",
		"--set", "workloadIdentity.controllerClientId=00000000-0000-0000-0000-000000000001",
		"--set", "workloadIdentity.generatorClientId=00000000-0000-0000-0000-000000000002",
		"--set", "importAssets.persistence.enabled=false",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, output)
	}

	const (
		wantNamespace = "resource-ns"
		wantRoot      = "/var/lib/apexload-import-assets"
		wantDNS       = "dns://apexload-generator.resource-ns.svc.cluster.local:9090"
		wantTLSName   = "apexload-generator.resource-ns.svc.cluster.local"
	)
	foundConfig := false
	foundController := false
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	for {
		var manifest importAssetsManifest
		if err := decoder.Decode(&manifest); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode rendered manifest: %v", err)
		}
		switch {
		case manifest.Kind == "ConfigMap" && manifest.Metadata.Name == "apexload-data-platform":
			foundConfig = true
			if manifest.Metadata.Namespace != wantNamespace || manifest.Data["APEXLOAD_GENERATORS"] != wantDNS || manifest.Data["APEXLOAD_GRPC_TLS_SERVER_NAME"] != wantTLSName {
				t.Fatalf("namespace-aware controller config = namespace %q generators %q TLS name %q", manifest.Metadata.Namespace, manifest.Data["APEXLOAD_GENERATORS"], manifest.Data["APEXLOAD_GRPC_TLS_SERVER_NAME"])
			}
		case manifest.Kind == "Deployment" && manifest.Metadata.Name == "apexload-controller":
			foundController = true
			if manifest.Metadata.Namespace != wantNamespace {
				t.Fatalf("controller namespace = %q, want %q", manifest.Metadata.Namespace, wantNamespace)
			}
			if manifest.Spec.Strategy.Type != "Recreate" {
				t.Fatalf("non-HA controller strategy = %q, want Recreate", manifest.Spec.Strategy.Type)
			}
			if len(manifest.Spec.Template.Spec.Containers) == 0 {
				t.Fatal("rendered controller has no containers")
			}
			controller := manifest.Spec.Template.Spec.Containers[0]
			if !hasImportAssetsEnv(controller.Env, wantRoot) || !hasImportAssetsMount(controller.VolumeMounts, wantRoot) {
				t.Fatalf("controller import-asset env/mount = env %#v mounts %#v", controller.Env, controller.VolumeMounts)
			}
			foundEmptyDir := false
			for _, volume := range manifest.Spec.Template.Spec.Volumes {
				if volume.Name == "import-assets" {
					foundEmptyDir = volume.EmptyDir != nil && volume.EmptyDir.SizeLimit == "10Gi" && volume.PersistentVolumeClaim == nil
				}
			}
			if !foundEmptyDir {
				t.Fatalf("persistence-disabled import-assets volume is not an emptyDir: %#v", manifest.Spec.Template.Spec.Volumes)
			}
		case manifest.Kind == "PersistentVolumeClaim" && manifest.Metadata.Name == "apexload-controller-import-assets":
			t.Fatal("persistence-disabled Helm render unexpectedly created an import-assets PVC")
		}
	}
	if !foundConfig || !foundController {
		t.Fatalf("rendered chart missing config/controller: config=%t controller=%t", foundConfig, foundController)
	}
}

func TestAKSHAImportAssetsRendersAzureFilesRWXClaim(t *testing.T) {
	kustomize, err := exec.LookPath("kustomize")
	if err != nil {
		t.Skip("kustomize is required to render the AKS HA overlay")
	}
	output, err := exec.Command(kustomize, "build", "aks-ha").CombinedOutput()
	if err != nil {
		t.Fatalf("kustomize build aks-ha: %v\n%s", err, output)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	for {
		var manifest importAssetsManifest
		if err := decoder.Decode(&manifest); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode AKS HA manifest: %v", err)
		}
		if manifest.Kind != "PersistentVolumeClaim" || manifest.Metadata.Name != "apexload-controller-import-assets" {
			continue
		}
		if !slices.Equal(manifest.Spec.AccessModes, []string{"ReadWriteMany"}) || manifest.Spec.StorageClassName != "azurefile-csi" {
			t.Fatalf("AKS HA import-assets claim = accessModes %#v storageClass %q", manifest.Spec.AccessModes, manifest.Spec.StorageClassName)
		}
		return
	}
	t.Fatal("AKS HA render did not contain the controller import-assets claim")
}

func TestKustomizeControllersRollWithoutOverlappingRWOClaim(t *testing.T) {
	kustomize, err := exec.LookPath("kustomize")
	if err != nil {
		t.Skip("kustomize is required to render deployment overlays")
	}
	for _, overlay := range []string{"aks", "aks-ha", "onprem", "onprem-ha"} {
		t.Run(overlay, func(t *testing.T) {
			output, err := exec.Command(kustomize, "build", overlay).CombinedOutput()
			if err != nil {
				t.Fatalf("kustomize build %s: %v\n%s", overlay, err, output)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(output))
			for {
				var manifest importAssetsManifest
				if err := decoder.Decode(&manifest); err == io.EOF {
					break
				} else if err != nil {
					t.Fatalf("decode %s manifest: %v", overlay, err)
				}
				if manifest.Kind != "Deployment" || manifest.Metadata.Name != "apexload-controller" {
					continue
				}
				strategy := manifest.Spec.Strategy
				if strategy.Type != "RollingUpdate" || strategy.RollingUpdate.MaxSurge != 0 || strategy.RollingUpdate.MaxUnavailable != 1 {
					t.Fatalf("%s controller rollout = %#v, want no-surge one-at-a-time update", overlay, strategy)
				}
				return
			}
			t.Fatalf("%s render did not contain the controller deployment", overlay)
		})
	}
}

func TestHelmHARejectsEphemeralImportAssets(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required to render the chart")
	}
	output, err := exec.Command(helm,
		"template", "custom", "../helm/apexload",
		"--namespace", "release-ns",
		"--set", "image.registry=registry.example.net",
		"--set", "ingress.host=apexload.example.net",
		"--set", "dataPlatform.azureStorageAccount=apexloadstorage",
		"--set", "dataPlatform.keyVaultUrl=https://apexload.vault.azure.net",
		"--set", "workloadIdentity.controllerClientId=00000000-0000-0000-0000-000000000001",
		"--set", "workloadIdentity.generatorClientId=00000000-0000-0000-0000-000000000002",
		"--set", "controller.ha.enabled=true",
		"--set", "importAssets.persistence.enabled=false",
	).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "importAssets.persistence.enabled must be true when controller HA is enabled") {
		t.Fatalf("HA ephemeral import-assets render error = %v\n%s", err, output)
	}
}

func TestHelmHARequiresAndMountsOperatorRWXImportClaim(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required to render the chart")
	}
	baseArgs := []string{
		"template", "custom", "../helm/apexload",
		"--namespace", "release-ns",
		"--set", "image.registry=registry.example.net",
		"--set", "ingress.host=apexload.example.net",
		"--set", "dataPlatform.azureStorageAccount=apexloadstorage",
		"--set", "dataPlatform.keyVaultUrl=https://apexload.vault.azure.net",
		"--set", "workloadIdentity.controllerClientId=00000000-0000-0000-0000-000000000001",
		"--set", "workloadIdentity.generatorClientId=00000000-0000-0000-0000-000000000002",
		"--set", "controller.ha.enabled=true",
	}
	output, err := exec.Command(helm, baseArgs...).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "importAssets.persistence.existingClaim is required when controller HA is enabled") {
		t.Fatalf("HA missing-claim render error = %v\n%s", err, output)
	}

	output, err = exec.Command(helm, append(baseArgs, "--set", "importAssets.persistence.existingClaim=shared-import-assets")...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template with HA import claim: %v\n%s", err, output)
	}
	foundController := false
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	for {
		var manifest importAssetsManifest
		if err := decoder.Decode(&manifest); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode HA Helm manifest: %v", err)
		}
		if manifest.Kind == "PersistentVolumeClaim" && manifest.Metadata.Name == "shared-import-assets" {
			t.Fatal("chart unexpectedly tried to create the operator-supplied import-assets claim")
		}
		if manifest.Kind != "Deployment" || manifest.Metadata.Name != "apexload-controller" {
			continue
		}
		foundController = true
		if manifest.Spec.Strategy.Type == "Recreate" {
			t.Fatal("HA controller unexpectedly uses the non-HA Recreate strategy")
		}
		foundClaim := false
		for _, volume := range manifest.Spec.Template.Spec.Volumes {
			if volume.Name == "import-assets" && volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == "shared-import-assets" {
				foundClaim = true
			}
		}
		if !foundClaim {
			t.Fatalf("HA controller did not mount operator import claim: %#v", manifest.Spec.Template.Spec.Volumes)
		}
	}
	if !foundController {
		t.Fatal("HA Helm render did not contain the controller deployment")
	}
}

func hasImportAssetsEnv(env []struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}, wantRoot string) bool {
	for _, item := range env {
		if item.Name == "APEXLOAD_IMPORT_ASSET_DIR" && item.Value == wantRoot {
			return true
		}
	}
	return false
}

func hasImportAssetsMount(mounts []struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
}, wantRoot string) bool {
	for _, mount := range mounts {
		if mount.Name == "import-assets" && mount.MountPath == wantRoot {
			return true
		}
	}
	return false
}
