package buildinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnvironment(t *testing.T) {
	// A docker marker fixture that actually exists, so the stat probe succeeds.
	existingMarker := filepath.Join(t.TempDir(), ".dockerenv")
	if err := os.WriteFile(existingMarker, []byte{}, 0o600); err != nil {
		t.Fatalf("seed docker marker: %v", err)
	}

	tests := []struct {
		name       string
		appEnv     string
		k8sHost    string
		dockerPath string
		want       string
	}{
		{name: "explicit override wins over everything", appEnv: "AKS ", k8sHost: "10.0.0.1", dockerPath: existingMarker, want: "aks"},
		{name: "override lowercased and trimmed", appEnv: "  Local-Dev  ", want: "local-dev"},
		{name: "kubernetes via service host", k8sHost: "10.0.0.1", dockerPath: existingMarker, want: "kubernetes"},
		{name: "docker via marker file", dockerPath: existingMarker, want: "docker"},
		{name: "local default", dockerPath: filepath.Join(t.TempDir(), "missing"), want: "local"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ENVIRONMENT", tc.appEnv)
			t.Setenv("KUBERNETES_SERVICE_HOST", tc.k8sHost)
			if tc.dockerPath != "" {
				orig := dockerEnvPath
				dockerEnvPath = tc.dockerPath
				t.Cleanup(func() { dockerEnvPath = orig })
			}
			if got := Environment(); got != tc.want {
				t.Errorf("Environment() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetPopulatesRuntimeFields(t *testing.T) {
	t.Setenv("APEXLOAD_ENVIRONMENT", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	orig := dockerEnvPath
	dockerEnvPath = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { dockerEnvPath = orig })

	info := Get()
	if info.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
	if info.Environment != "local" {
		t.Errorf("Environment = %q, want %q", info.Environment, "local")
	}
	// Version is never blank: either ldflag/VCS-derived or the "dev" fallback.
	if info.Version == "" {
		t.Error("Version is empty; expected a value or the 'dev' fallback")
	}
}

func TestGetUsesLdflagValues(t *testing.T) {
	origV, origC, origB := Version, Commit, BuildTime
	t.Cleanup(func() { Version, Commit, BuildTime = origV, origC, origB })

	Version = "9.9.9"
	Commit = "abcdef0"
	BuildTime = "2026-06-03T00:00:00Z"

	info := Get()
	if info.Version != "9.9.9" {
		t.Errorf("Version = %q, want %q", info.Version, "9.9.9")
	}
	if info.Commit != "abcdef0" {
		t.Errorf("Commit = %q, want %q", info.Commit, "abcdef0")
	}
	if info.BuildTime != "2026-06-03T00:00:00Z" {
		t.Errorf("BuildTime = %q, want %q", info.BuildTime, "2026-06-03T00:00:00Z")
	}
}
