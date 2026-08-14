package fileasset

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetStorageRefValidationDoesNotDependOnRuntimeOpener(t *testing.T) {
	namespace, err := ProjectAssetNamespace("project-a")
	if err != nil {
		t.Fatal(err)
	}
	ref := "asset://" + namespace + "/asset-1"
	if got, err := ValidateStorageRefForProject(ref, "project-a"); err != nil || got != ref {
		t.Fatalf("owner validation = %q, %v", got, err)
	}
	if _, err := ValidateStorageRefForProject(ref, "project-b"); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("foreign project error = %v, want ownership rejection", err)
	}
	if _, err := OpenStorageRef(ref); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("open without runtime resolver error = %v", err)
	}
	if err := RegisterStorageRefOpener("asset", func(string, int64) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("payload")), nil
	}); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenStorageRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "payload" {
		t.Fatalf("resolved data = %q, %v", data, err)
	}
}

func TestOpenPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.pem")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "escape.pem")); err != nil {
		t.Fatal(err)
	}
	if err := RegisterRoot(root); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPath(filepath.Join(root, "escape.pem")); err == nil {
		t.Fatal("OpenPath followed a symlink outside its allowed root")
	}
}

func TestReadPathLimitedEnforcesCallerLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "credential.pem")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 17)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RegisterRoot(root); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPathLimited(path, 16); err == nil || !strings.Contains(err.Error(), "exceeds 16 byte limit") {
		t.Fatalf("ReadPathLimited error = %v, want size rejection", err)
	}
}

func TestValidatePathForProjectFencesProjectRoots(t *testing.T) {
	root := t.TempDir()
	if err := RegisterProjectRoot(root); err != nil {
		t.Fatalf("RegisterProjectRoot: %v", err)
	}
	projectDir, err := ProjectAssetDir(root, "project-a")
	if err != nil {
		t.Fatalf("ProjectAssetDir: %v", err)
	}
	path := filepath.Join(projectDir, "import-1", "capability", "asset.bin")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("asset"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := ValidatePathForProject(path, "project-a"); err != nil {
		t.Fatalf("owner validation failed: %v", err)
	}
	if _, err := ValidatePathForProject(path, "project-b"); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("foreign project error = %v, want ownership rejection", err)
	}
	if _, err := ValidatePathForProject(path, ""); err == nil || !strings.Contains(err.Error(), "project_id is required") {
		t.Fatalf("empty project error = %v, want required rejection", err)
	}
}

func TestProjectAssetDirAvoidsSanitizationCollisions(t *testing.T) {
	root := t.TempDir()
	first, err := ProjectAssetDir(root, "project/a")
	if err != nil {
		t.Fatalf("first ProjectAssetDir: %v", err)
	}
	second, err := ProjectAssetDir(root, "project_a")
	if err != nil {
		t.Fatalf("second ProjectAssetDir: %v", err)
	}
	if first == second {
		t.Fatalf("distinct project IDs collided at %q", first)
	}
}
