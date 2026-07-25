package java_vuser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

func writeLocalSourceJar(t *testing.T, dir, name string, content []byte) (digest, storageRef string) {
	t.Helper()
	if err := fileasset.RegisterRoot(dir); err != nil {
		t.Fatalf("register root: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write jar: %v", err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), (&url.URL{Scheme: "file", Path: path}).String()
}

// M1 regression: the local runner must materialize ONLY the script-owned
// classpath slice, never operator-provisioned shared entries. An asset whose
// Entry matches an operator entry must be ignored (no materialized file), so a
// user upload can never shadow an operator-trusted JAR. The call errors later at
// ResolveClasspath (the entries do not exist on disk); we assert the
// materialization side effects, which happen first.
func TestPrepareLocalConfigurationDoesNotShadowOperatorClasspath(t *testing.T) {
	resetProcessSlots()
	defer resetProcessSlots()

	sourceDir := t.TempDir()
	scriptDigest, scriptRef := writeLocalSourceJar(t, sourceDir, "app.jar", []byte("PK\x03\x04 script jar"))
	operatorDigest, operatorRef := writeLocalSourceJar(t, sourceDir, "shared.jar", []byte("PK\x03\x04 operator jar"))

	workDir := t.TempDir()
	cfg := configuration{
		Enabled:                true,
		RunnerCommand:          "java",
		RunnerCommandExplicit:  true,
		MaxProcesses:           1,
		WorkDir:                workDir,
		ResolvedJDK:            &javarunner.ResolvedJDK{Home: "/x", JavaBin: "/x/bin/java", Major: 17},
		ScriptClasspath:        []string{"app.jar"},
		Classpath:              []string{"app.jar", "shared.jar"}, // script ++ operator
		ClasspathUploadEnabled: true,
		ClasspathAssets: []dsl.JavaClasspathAsset{
			{Entry: "app.jar", SHA256: scriptDigest, StorageRef: scriptRef},        // script entry: should materialize
			{Entry: "shared.jar", SHA256: operatorDigest, StorageRef: operatorRef}, // operator entry: must be ignored
		},
	}

	// The overall call errors downstream (harness/resolve); ignore it — we assert
	// the materialization side effects which run before those stages.
	_, _ = prepareLocalConfigurationWithResolver(context.Background(), cfg,
		func(context.Context, javarunner.ResolveOptions) (javarunner.ResolvedJDK, error) {
			return javarunner.ResolvedJDK{}, nil
		})

	assetDir := filepath.Join(workDir, javarunner.ClasspathAssetDirName)
	if _, err := os.Stat(filepath.Join(assetDir, scriptDigest+".jar")); err != nil {
		t.Fatalf("script-owned classpath entry was not materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(assetDir, operatorDigest+".jar")); !os.IsNotExist(err) {
		t.Fatalf("operator classpath entry was materialized (shadowing not prevented): stat err=%v", err)
	}
}

// M5: the local flag-on materialization branch reaches its error formatter, and
// that error is value-free (index + category only, never the entry/storage_ref).
func TestPrepareLocalConfigurationMaterializationErrorIsValueFree(t *testing.T) {
	resetProcessSlots()
	defer resetProcessSlots()

	secret := "/secret/companyX/app.jar"
	workDir := t.TempDir()
	cfg := configuration{
		Enabled:                true,
		RunnerCommand:          "java",
		RunnerCommandExplicit:  true,
		MaxProcesses:           1,
		WorkDir:                workDir,
		ResolvedJDK:            &javarunner.ResolvedJDK{Home: "/x", JavaBin: "/x/bin/java", Major: 17},
		ScriptClasspath:        []string{secret},
		Classpath:              []string{secret},
		ClasspathUploadEnabled: true,
		ClasspathAssets: []dsl.JavaClasspathAsset{{
			Entry:      secret,
			SHA256:     hex.EncodeToString(make([]byte, sha256.Size)),
			StorageRef: "file:///nonexistent/secret.jar", // outside any registered root -> unavailable
		}},
	}

	_, err := prepareLocalConfigurationWithResolver(context.Background(), cfg,
		func(context.Context, javarunner.ResolveOptions) (javarunner.ResolvedJDK, error) {
			return javarunner.ResolvedJDK{}, nil
		})
	if err == nil {
		t.Fatal("expected a materialization error")
	}
	// Assert the machine-readable category constant, not the English prose: the
	// prose is a fixed literal and would still match if the category regressed.
	if !strings.Contains(err.Error(), javarunner.ClasspathAssetUnavailable) || !strings.Contains(err.Error(), "entry 0") {
		t.Fatalf("error not the expected value-free asset error: %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "secret.jar") {
		t.Fatalf("error leaked the classpath value / storage_ref: %v", err)
	}
}
