package scripts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedHARSDKForkIntegrityAndPackaging(t *testing.T) {
	root := ".."
	directory := filepath.Join(root, "third_party/playwright-go")
	data, err := os.ReadFile(filepath.Join(directory, "UPSTREAM_MANIFEST.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		UpstreamModule  string   `json:"upstream_module"`
		UpstreamVersion string   `json:"upstream_version"`
		ModuleZipSum    string   `json:"module_zip_sum"`
		Altered         []string `json:"altered_upstream_files"`
		Sources         []struct {
			Path         string `json:"path"`
			UpstreamPath string `json:"upstream_path"`
			SHA          string `json:"sha256"`
		} `json:"sources"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.UpstreamModule != "github.com/mxschmitt/playwright-go" || manifest.UpstreamVersion != "v0.6100.0" || manifest.ModuleZipSum != "h1:HYNnbGZsTHz8veJyDGe4fU1iPxfvXqzmwKchzuvGCsY=" || len(manifest.Sources) != 80 || len(manifest.Altered) != 2 || manifest.Altered[0] != "browser_context.go" || manifest.Altered[1] != "frame.go" {
		t.Fatal("pinned SDK provenance/source boundary changed")
	}
	renamedREADME := 0
	for _, item := range manifest.Sources {
		if item.Path == "README.upstream.txt" {
			if item.UpstreamPath != "README.md" {
				t.Fatal("renamed upstream README lost its original path")
			}
			renamedREADME++
		} else if item.UpstreamPath != "" {
			t.Fatal("undeclared upstream source path mapping")
		}
		if !filepath.IsLocal(item.Path) {
			t.Fatal("invalid provenance source path")
		}
		bytes, err := os.ReadFile(filepath.Join(directory, item.Path))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(bytes)
		if item.Path != "browser_context.go" && item.Path != "frame.go" && hex.EncodeToString(sum[:]) != item.SHA {
			t.Fatalf("undeclared SDK source delta: %s", item.Path)
		}
	}
	if renamedREADME != 1 {
		t.Fatal("pristine renamed upstream README must appear exactly once")
	}
	wrapper, err := os.ReadFile(filepath.Join(directory, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"(APEX_PATCHES.md)", "(LICENSE)", "(UPSTREAM_MANIFEST.json)", "(README.upstream.txt)", "https://github.com/mxschmitt/playwright-go/blob/v0.6100.0/README.md"} {
		if !strings.Contains(string(wrapper), link) {
			t.Fatalf("local SDK README missing reviewed documentation link %s", link)
		}
	}
	for _, rel := range []string{"Dockerfile", "Dockerfile.controller", "Dockerfile.browser-generator", "runners/java_vuser/Dockerfile", "runners/java_vuser/Dockerfile.supervisor", "runners/java_vuser/Dockerfile.broker", "runners/java_vuser/Dockerfile.facade"} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "COPY go.mod go.sum ./\nCOPY third_party/playwright-go/ ./third_party/playwright-go/\nRUN go mod download") {
			t.Fatalf("local SDK missing before dependency download in %s", rel)
		}
	}
	for _, rel := range []string{"go.mod", "Makefile", ".github/workflows/ci.yml"} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if rel == "go.mod" {
			if !strings.Contains(string(data), "replace github.com/mxschmitt/playwright-go => ./third_party/playwright-go") {
				t.Fatal("SDK local replacement missing")
			}
		} else if !strings.Contains(string(data), "github.com/mxschmitt/playwright-go -run '^TestAPEX(OwnedHAR|LoadState)'") {
			t.Fatalf("dependency-module contract tests missing from %s", rel)
		}
	}
}
