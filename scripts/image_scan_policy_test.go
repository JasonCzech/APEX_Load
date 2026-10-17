package scripts_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every image vulnerability scan applies one policy: each scanned image gets a
// step listing every HIGH/CRITICAL OS finding without blocking, a step
// blocking the OS findings its distribution has shipped a fix for, and a step
// blocking every application-library finding, fixed upstream or not. The
// steps are inlined rather than shared through a local action because release
// and on-prem publishing check out the tag's tree, which for an older tag would
// not hold a newer action.
func TestImageScansBlockFixableOSAndEveryLibraryFinding(t *testing.T) {
	type step struct {
		Name string         `yaml:"name"`
		Uses string         `yaml:"uses"`
		With map[string]any `yaml:"with"`
	}
	type workflow struct {
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	// The scanned images per workflow: CI's per-PR image, and each release
	// and on-prem candidate's two platform digests.
	want := map[string]int{"ci.yml": 1, "release.yml": 2, "publish-onprem.yml": 2}
	modes := map[string]string{
		"os/false/0":      "lists OS findings",
		"os/true/1":       "blocks fixable OS findings",
		"library/false/1": "blocks every library finding",
	}
	paths, err := filepath.Glob("../.github/workflows/*.y*ml")
	if err != nil || len(paths) == 0 {
		t.Fatal("no workflows", err)
	}
	scanned := map[string]int{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var parsed workflow
		if err := yaml.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		file := filepath.Base(path)
		for id, job := range parsed.Jobs {
			images := map[string]map[string]bool{}
			for _, s := range job.Steps {
				if !strings.HasPrefix(s.Uses, "aquasecurity/trivy-action@") {
					continue
				}
				value := func(key string) string { return fmt.Sprint(s.With[key]) }
				mode := value("vuln-type") + "/" + value("ignore-unfixed") + "/" + value("exit-code")
				if _, ok := modes[mode]; !ok || value("severity") != "HIGH,CRITICAL" {
					t.Errorf("%s job %s step %q scans %s at %s, outside the policy", file, id, s.Name, mode, value("severity"))
					continue
				}
				ref := value("image-ref")
				if images[ref] == nil {
					images[ref] = map[string]bool{}
				}
				if images[ref][mode] {
					t.Errorf("%s job %s %s %s twice", file, id, modes[mode], ref)
				}
				images[ref][mode] = true
			}
			for ref, seen := range images {
				for mode, what := range modes {
					if !seen[mode] {
						t.Errorf("%s job %s scans %s but no step %s", file, id, ref, what)
					}
				}
				scanned[file]++
			}
		}
	}
	for file, n := range want {
		if scanned[file] != n {
			t.Errorf("%s scans %d images, want %d", file, scanned[file], n)
		}
	}
	for file := range scanned {
		if _, ok := want[file]; !ok {
			t.Errorf("%s scans images; add it to this test's inventory", file)
		}
	}
}
