package k8s_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func archiveStagingHelmArgs() []string {
	return []string{"template", "staging", "../helm/apexload", "--namespace", "release-ns",
		"--set", "namespace.name=staging-ns", "--set", "image.registry=registry.example.net",
		"--set", "ingress.host=apexload.example.net", "--set", "dataPlatform.azureStorageAccount=apexloadstorage",
		"--set", "dataPlatform.keyVaultUrl=https://apexload.vault.azure.net",
		"--set", "workloadIdentity.controllerClientId=00000000-0000-0000-0000-000000000001",
		"--set", "workloadIdentity.generatorClientId=00000000-0000-0000-0000-000000000002",
		"--set", "importAssets.persistence.existingClaim=separate-import-assets"}
}

func TestHelmArchiveStagingPersistence(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required")
	}
	for _, tc := range []struct {
		name                              string
		values                            []string
		claim, mode, class, size, failure string
		enabled, create                   bool
	}{
		{name: "default ephemeral"},
		{name: "reject shared reader cache", values: []string{"archiveStaging.persistence.enabled=true", "importAssets.root=/var/lib/apexload/archive-read-cache"}, failure: "must not overlap importAssets.root"},
		{name: "reject root mount", values: []string{"archiveStaging.persistence.enabled=true", "importAssets.root=/"}, failure: "must not overlap importAssets.root"},
		{name: "custom size", values: []string{"archiveStaging.persistence.enabled=true", "archiveStaging.persistence.size=48Gi"}, claim: "apexload-controller-archive-staging", mode: "ReadWriteMany", size: "48Gi", enabled: true, create: true},
		{name: "DNS subdomain claim", values: []string{"archiveStaging.persistence.enabled=true", "archiveStaging.persistence.existingClaim=shared.archives"}, claim: "shared.archives", enabled: true},
		{name: "reject empty DNS label", values: []string{"archiveStaging.persistence.existingClaim=a..b"}, failure: "archiveStaging.persistence.existingClaim"},
		{name: "reject import claim reuse", values: []string{"archiveStaging.persistence.enabled=true", "archiveStaging.persistence.existingClaim=separate-import-assets"}, failure: "separate claim from importAssets"},
		{name: "reject import mount parent", values: []string{"archiveStaging.persistence.enabled=true", "importAssets.root=/var/lib/apexload"}, failure: "must not overlap importAssets.root"},
		{name: "reject import mount child", values: []string{"archiveStaging.persistence.enabled=true", "importAssets.root=/var/lib/apexload/archives/imports"}, failure: "must not overlap importAssets.root"},
		{name: "reject Bruno claim reuse", values: []string{"archiveStaging.persistence.enabled=true", "bruno.importEnabled=true", "bruno.artifactPersistence.existingClaim=apexload-controller-archive-staging"}, failure: "separate path and claim from Bruno"},
		{name: "created shared claim", values: []string{"archiveStaging.persistence.enabled=true", "archiveStaging.persistence.storageClassName=qualified-posix-rwx"}, claim: "apexload-controller-archive-staging", mode: "ReadWriteMany", class: "qualified-posix-rwx", enabled: true, create: true},
		{name: "existing shared claim", values: []string{"archiveStaging.persistence.enabled=true", "archiveStaging.persistence.existingClaim=shared-archives", "controller.ha.enabled=true"}, claim: "shared-archives", enabled: true},
		{name: "HA created claim", values: []string{"archiveStaging.persistence.enabled=true", "controller.ha.enabled=true"}, claim: "apexload-controller-archive-staging", mode: "ReadWriteMany", enabled: true, create: true},
		{name: "single writer", values: []string{"archiveStaging.persistence.enabled=true", "archiveStaging.persistence.accessModes[0]=ReadWriteOncePod"}, claim: "apexload-controller-archive-staging", mode: "ReadWriteOncePod", enabled: true, create: true},
		{name: "reject HA RWO", values: []string{"archiveStaging.persistence.enabled=true", "controller.ha.enabled=true", "archiveStaging.persistence.accessModes[0]=ReadWriteOnce"}, failure: "must declare ReadWriteMany"},
		{name: "reject HA existing RWO", values: []string{"archiveStaging.persistence.enabled=true", "controller.ha.enabled=true", "archiveStaging.persistence.existingClaim=shared-archives", "archiveStaging.persistence.accessModes[0]=ReadWriteOnce"}, failure: "must declare ReadWriteMany"},
		{name: "reject read only", values: []string{"archiveStaging.persistence.accessModes[0]=ReadOnlyMany"}, failure: "archiveStaging.persistence.accessModes.0"},
		{name: "reject zero size", values: []string{"archiveStaging.persistence.size=0Gi"}, failure: "archiveStaging.persistence.size"},
		{name: "reject invalid claim", values: []string{"archiveStaging.persistence.existingClaim=Invalid_Claim"}, failure: "archiveStaging.persistence.existingClaim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := archiveStagingHelmArgs()
			for _, v := range tc.values {
				args = append(args, "--set", v)
			}
			out, err := exec.Command(helm, args...).CombinedOutput()
			if tc.failure != "" {
				if err == nil || !strings.Contains(strings.ReplaceAll(string(out), "/", "."), tc.failure) {
					t.Fatalf("error=%v output=%s", err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("render: %v\n%s", err, out)
			}
			docs := decodeArchiveStagingDocuments(t, out)
			assertArchiveStagingMount(t, docs, tc.enabled, tc.claim)
			claims := 0
			for _, doc := range docs {
				if doc.Kind != "PersistentVolumeClaim" || doc.Metadata.Name == "apexload-controller-import-assets" {
					continue
				}
				claims++
				size := tc.size
				if size == "" {
					size = "256Gi"
				}
				if doc.Spec.Resources.Requests["storage"] != size || doc.Metadata.Annotations["helm.sh/resource-policy"] != "keep" {
					t.Fatal("staging capacity or retention policy missing")
				}
				if !tc.create || doc.Metadata.Name != tc.claim || doc.Metadata.Namespace != "staging-ns" || !slices.Equal(doc.Spec.AccessModes, []string{tc.mode}) || doc.Spec.StorageClassName != tc.class {
					t.Fatalf("unexpected claim: %+v", doc)
				}
			}
			want := 0
			if tc.create {
				want = 1
			}
			if claims != want {
				t.Fatalf("claims=%d want=%d", claims, want)
			}
		})
	}
}

func decodeArchiveStagingDocuments(t *testing.T, data []byte) []importAssetsManifest {
	t.Helper()
	var docs []importAssetsManifest
	d := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc importAssetsManifest
		if err := d.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, doc)
	}
	return docs
}

func assertArchiveStagingMount(t *testing.T, docs []importAssetsManifest, enabled bool, claim string) {
	t.Helper()
	found := false
	for _, doc := range docs {
		if doc.Kind != "Deployment" || doc.Metadata.Name != "apexload-controller" {
			continue
		}
		found = true
		mounted, volume, archiveEnv, stagingEnv, readerEnv, scratch, imports := false, false, false, false, false, false, false
		for _, c := range doc.Spec.Template.Spec.Containers {
			if c.Name != "controller" {
				continue
			}
			for _, m := range c.VolumeMounts {
				if m.Name == "archive-staging" {
					mounted = m.MountPath == "/var/lib/apexload/archives"
				}
				if m.Name == "data" {
					scratch = m.MountPath == "/var/lib/apexload"
				}
				if m.Name == "import-assets" {
					imports = m.MountPath != "/var/lib/apexload/archives"
				}
			}
			for _, e := range c.Env {
				if e.Name == "APEXLOAD_ARCHIVE_READER_CACHE_DIR" {
					readerEnv = e.Value == "/var/lib/apexload/archive-read-cache"
				}
				if e.Name == "APEXLOAD_ARCHIVE_DIR" {
					archiveEnv = e.Value == "/var/lib/apexload/archives"
				}
				if e.Name == "APEXLOAD_ARCHIVE_STAGING_DIR" {
					stagingEnv = e.Value == "/var/lib/apexload/archives/_object_staging"
				}
			}
		}
		for _, v := range doc.Spec.Template.Spec.Volumes {
			if v.Name == "archive-staging" {
				volume = v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == claim && v.EmptyDir == nil
			}
		}
		if mounted != enabled || volume != enabled || stagingEnv != enabled || readerEnv != enabled || !archiveEnv || !scratch || !imports {
			t.Fatalf("mount=%t volume=%t archiveEnv=%t stagingEnv=%t scratch=%t imports=%t enabled=%t", mounted, volume, archiveEnv, stagingEnv, scratch, imports, enabled)
		}
	}
	if !found {
		t.Fatal("missing controller")
	}
}

func TestKustomizeArchiveStagingComponent(t *testing.T) {
	kustomize, err := exec.LookPath("kustomize")
	if err != nil {
		t.Skip("kustomize is required")
	}
	for _, overlay := range []string{"aks", "aks-ha", "eks", "eks-ha", "onprem", "onprem-ha"} {
		t.Run(overlay, func(t *testing.T) {
			baseline, err := exec.Command(kustomize, "build", overlay).CombinedOutput()
			if err != nil {
				t.Fatalf("baseline render: %v\n%s", err, baseline)
			}
			assertArchiveStagingMount(t, decodeArchiveStagingDocuments(t, baseline), false, "")
			// A sibling overlay keeps normal Kustomize root restrictions in effect.
			dir, err := os.MkdirTemp(".", ".archive-staging-render-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			contents := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - ../" + overlay + "\ncomponents:\n  - ../components/archive-staging\n"
			if err := os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(kustomize, "build", dir).CombinedOutput()
			if err != nil {
				t.Fatalf("render: %v\n%s", err, out)
			}
			docs := decodeArchiveStagingDocuments(t, out)
			assertArchiveStagingMount(t, docs, true, "apexload-controller-archive-staging")
			for _, doc := range docs {
				if doc.Kind == "PersistentVolumeClaim" && doc.Metadata.Name == "apexload-controller-archive-staging" {
					t.Fatal("component must not invent a storage class or create the existing claim")
				}
			}
		})
	}
}
