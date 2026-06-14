package k8s_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRestartControlsExamplesGrantOnlyScopedRBAC(t *testing.T) {
	for _, path := range []string{
		"aks/restart-controls.example.yaml",
		"local/restart-controls.example.yaml",
		"onprem/restart-controls.example.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			docs := readRestartControlDocuments(t, path)
			if len(docs) != 2 {
				t.Fatalf("document count = %d, want 2", len(docs))
			}
			role := findManifestKind(t, docs, "Role")
			if role.Metadata.Name != "apexload-restart-controls" {
				t.Fatalf("Role name = %q", role.Metadata.Name)
			}
			got := map[string][]string{}
			for _, rule := range role.Rules {
				key := strings.Join(rule.APIGroups, ",") + "/" + strings.Join(rule.Resources, ",")
				verbs := append([]string(nil), rule.Verbs...)
				sort.Strings(verbs)
				got[key] = verbs
			}
			want := map[string][]string{
				"/pods":             {"delete", "get", "list"},
				"apps/deployments":  {"get", "patch"},
				"apps/statefulsets": {"get", "patch"},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Role rules = %#v, want %#v", got, want)
			}

			binding := findManifestKind(t, docs, "RoleBinding")
			if binding.RoleRef.Kind != "Role" || binding.RoleRef.Name != "apexload-restart-controls" {
				t.Fatalf("RoleBinding roleRef = %#v", binding.RoleRef)
			}
			if len(binding.Subjects) != 1 || binding.Subjects[0].Kind != "ServiceAccount" || binding.Subjects[0].Name != "apexload-controller" {
				t.Fatalf("RoleBinding subjects = %#v", binding.Subjects)
			}
		})
	}
}

func TestRestartControlsExamplesStayOptIn(t *testing.T) {
	for _, path := range []string{
		"aks/kustomization.yaml",
		"local/kustomization.yaml",
		"onprem/kustomization.yaml",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(raw), "restart-controls.example.yaml") {
			t.Fatalf("%s includes restart-controls.example.yaml; it must remain opt-in", path)
		}
	}
}

func readRestartControlDocuments(t *testing.T, path string) []restartManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var docs []restartManifest
	for {
		var doc restartManifest
		err := decoder.Decode(&doc)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode %s: %v", path, err)
		}
		if doc.Kind != "" {
			docs = append(docs, doc)
		}
	}
	return docs
}

func findManifestKind(t *testing.T, docs []restartManifest, kind string) restartManifest {
	t.Helper()
	for _, doc := range docs {
		if doc.Kind == kind {
			return doc
		}
	}
	t.Fatalf("missing %s manifest", kind)
	return restartManifest{}
}

type restartManifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Rules []struct {
		APIGroups []string `yaml:"apiGroups"`
		Resources []string `yaml:"resources"`
		Verbs     []string `yaml:"verbs"`
	} `yaml:"rules"`
	Subjects []struct {
		Kind      string `yaml:"kind"`
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"subjects"`
	RoleRef struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"roleRef"`
}
