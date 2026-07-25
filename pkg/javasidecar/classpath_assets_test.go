package javasidecar

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

func writeSidecarSourceJar(t *testing.T, dir string, content []byte) (digest, storageRef string) {
	t.Helper()
	if err := fileasset.RegisterRoot(dir); err != nil {
		t.Fatalf("register root: %v", err)
	}
	path := filepath.Join(dir, "dep.jar")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write jar: %v", err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), (&url.URL{Scheme: "file", Path: path}).String()
}

// The gate must be inert when the operator has not opted in: the classpath is
// returned verbatim and nothing is written to disk (fail-closed parity).
func TestServerMaterializeClasspathAssetsFlagOffIsNoop(t *testing.T) {
	s := &Server{operator: config.JavaVuserOperatorConfig{ClasspathUploadEnabled: false}}
	workDir := t.TempDir()
	classpath := []string{"dep.jar"}
	assets := []ClasspathAssetRef{{Entry: "dep.jar", SHA256: "deadbeef", StorageRef: "file:///whatever"}}

	out, failure := s.materializeClasspathAssets(classpath, assets, workDir)
	if failure != nil {
		t.Fatalf("unexpected failure: %+v", failure)
	}
	if len(out) != 1 || out[0] != "dep.jar" {
		t.Fatalf("classpath mutated with flag off: %#v", out)
	}
	if _, err := os.Stat(filepath.Join(workDir, javarunner.ClasspathAssetDirName)); !os.IsNotExist(err) {
		t.Fatalf("flag-off gate created the asset dir (stat err=%v)", err)
	}
}

func TestServerMaterializeClasspathAssetsFlagOnRewrites(t *testing.T) {
	s := &Server{operator: config.JavaVuserOperatorConfig{ClasspathUploadEnabled: true}}
	content := []byte("PK\x03\x04 sidecar jar")
	digest, storageRef := writeSidecarSourceJar(t, t.TempDir(), content)
	workDir := t.TempDir()

	out, failure := s.materializeClasspathAssets(
		[]string{"dep.jar"},
		[]ClasspathAssetRef{{Entry: "dep.jar", SHA256: digest, StorageRef: storageRef}},
		workDir,
	)
	if failure != nil {
		t.Fatalf("unexpected failure: %+v", failure)
	}
	want := filepath.Join(workDir, javarunner.ClasspathAssetDirName, digest+".jar")
	if out[0] != want {
		t.Fatalf("entry not rewritten: got %q want %q", out[0], want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("materialized jar missing: %v", err)
	}
}

func TestServerMaterializeClasspathAssetsFlagOnUnavailable(t *testing.T) {
	s := &Server{operator: config.JavaVuserOperatorConfig{ClasspathUploadEnabled: true}}
	workDir := t.TempDir()

	_, failure := s.materializeClasspathAssets(
		[]string{"dep.jar"},
		// StorageRef points outside any registered root.
		[]ClasspathAssetRef{{Entry: "dep.jar", SHA256: hex.EncodeToString(make([]byte, sha256.Size)), StorageRef: "file:///nope/dep.jar"}},
		workDir,
	)
	if failure == nil || failure.code != javarunner.ClasspathAssetUnavailable {
		t.Fatalf("expected classpath_asset_unavailable, got %+v", failure)
	}
	if failure.index == nil || *failure.index != 0 {
		t.Fatalf("expected index 0, got %+v", failure.index)
	}
}
