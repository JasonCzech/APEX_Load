package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestDefaultSystemSettingsEnablesClientDownloads(t *testing.T) {
	if !defaultSystemSettings(10).ClientDownloads.Enabled {
		t.Fatal("client software downloads should default to enabled")
	}
}

// TestClientDownloadsDisabledFailsClosed is the security core: when an admin
// disables downloads, every download/checksum endpoint must refuse (403) even
// when packages are staged — the gate, not a missing file, is what blocks it.
func TestClientDownloadsDisabledFailsClosed(t *testing.T) {
	raDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(raDir, "apexload-recording-agent-0.1.0-linux-amd64.tar.gz"), []byte("agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	dtDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dtDir, "apexload-desktop-0.1.0-linux-amd64.tar.gz"), []byte("desktop"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(recordingAgentDistDirEnv, raDir)
	t.Setenv(desktopDistDirEnv, dtDir)

	server := NewServer(ServerConfig{MaxConcurrent: 10})

	downloads := []string{
		"/api/v1/downloads/recording-agent/latest?platform=linux-amd64",
		"/api/v1/downloads/desktop/latest?platform=linux-amd64",
	}
	allEndpoints := append([]string{
		"/api/v1/downloads/recording-agent/latest/checksum?platform=linux-amd64",
		"/api/v1/downloads/desktop/latest/checksum?platform=linux-amd64",
	}, downloads...)

	// Enabled by default: staged downloads succeed.
	for _, ep := range downloads {
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, ep, nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("enabled %s status = %d, want 200 body=%s", ep, resp.Code, resp.Body.String())
		}
	}

	// Disable downloads.
	settings := server.runner.GetSystemSettings()
	settings.ClientDownloads.Enabled = false
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	// Every endpoint now fails closed with 403.
	for _, ep := range allEndpoints {
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, ep, nil))
		if resp.Code != http.StatusForbidden {
			t.Fatalf("disabled %s status = %d, want 403 body=%s", ep, resp.Code, resp.Body.String())
		}
		if !strings.Contains(resp.Body.String(), "disabled by an administrator") {
			t.Fatalf("disabled %s body = %q, want disabled message", ep, resp.Body.String())
		}
	}
}

func TestClientDownloadsExplicitDisablePersists(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 10})
	if !server.runner.GetSystemSettings().ClientDownloads.Enabled {
		t.Fatal("expected client downloads to start enabled")
	}

	settings := server.runner.GetSystemSettings()
	settings.ClientDownloads.Enabled = false
	updated, err := server.runner.UpdateSystemSettings(settings)
	if err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	if updated.ClientDownloads.Enabled {
		t.Fatalf("explicit disable was overridden on write: %+v", updated.ClientDownloads)
	}
	if server.runner.GetSystemSettings().ClientDownloads.Enabled {
		t.Fatal("explicit disable did not persist across GetSystemSettings")
	}
}

// TestClientDownloadsMigrationDefaultsEnabled covers the upgrade path: a settings
// blob persisted before client_downloads existed has no key, and must load as
// enabled rather than the zero value {Enabled:false}.
func TestClientDownloadsMigrationDefaultsEnabled(t *testing.T) {
	legacy := marshalSettingsWithoutKey(t, defaultSystemSettings(10), "client_downloads")

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	if err := store.SaveSetting(systemSettingsStoreKey, legacy); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10})
	if !server.runner.GetSystemSettings().ClientDownloads.Enabled {
		t.Fatal("legacy settings blob without client_downloads should load as enabled")
	}
}

// TestClientDownloadsPUTOmitPreservesValue verifies the handler inherits the
// stored client_downloads when a PUT omits the section (so saving an unrelated
// setting can't silently re-enable downloads an admin turned off).
func TestClientDownloadsPUTOmitPreservesValue(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10})

	// Disable downloads first.
	settings := server.runner.GetSystemSettings()
	settings.ClientDownloads.Enabled = false
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	// PUT a body that omits client_downloads entirely.
	body := marshalSettingsWithoutKey(t, server.runner.GetSystemSettings(), "client_downloads")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader([]byte(body)))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	if server.runner.GetSystemSettings().ClientDownloads.Enabled {
		t.Fatal("PUT omitting client_downloads re-enabled it instead of preserving the disable")
	}
}

// marshalSettingsWithoutKey serializes settings and removes a top-level key so
// the result mimics an older / partial settings blob.
func marshalSettingsWithoutKey(t *testing.T, settings SystemSettings, key string) string {
	t.Helper()
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal settings to map: %v", err)
	}
	delete(fields, key)
	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("re-marshal settings: %v", err)
	}
	return string(out)
}

func TestSanitizeDownloadPlatform(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"darwin-arm64", "darwin-arm64"},
		{"linux-amd64", "linux-amd64"},
		{"LINUX-AMD64", "linux-amd64"},
		{"  windows-amd64  ", "windows-amd64"},
		{"linux/amd64", "linux-amd64"},
		{"", ""},
		{"   ", ""},
		{"../../etc/passwd", ""},
		{"linux-amd64\x00", ""},
		{"%2e%2e", ""},
		{"darwin-arm64; rm -rf /", ""},
		{strings.Repeat("a", 4096), ""},
		{"freebsd-amd64", ""},
	}
	for _, tc := range cases {
		if got := sanitizeDownloadPlatform(tc.in); got != tc.want {
			t.Errorf("sanitizeDownloadPlatform(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFindDownloadPackageRejectsSymlinkEscape verifies a symlink staged inside
// the dist directory that points outside it is never selected for serving —
// the path-confinement guard added to findDownloadPackage.
func TestFindDownloadPackageRejectsSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.tar.gz")
	if err := os.WriteFile(secret, []byte("top secret host file"), 0o600); err != nil {
		t.Fatal(err)
	}

	distDir := t.TempDir()
	// A malicious symlink whose name matches the served-package pattern.
	link := filepath.Join(distDir, recordingAgentPackagePrefix+"0.1.0-linux-amd64.tar.gz")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := findDownloadPackage(distDir, recordingAgentPackagePrefix, "linux-amd64", "Agent", "stage", recordingAgentDistDirEnv)
	if err == nil {
		t.Fatal("symlink escaping the dist dir must not be served, but a package was returned")
	}

	// A legitimate regular package in the same dir is still found, and is chosen
	// over the escaping symlink.
	real := filepath.Join(distDir, recordingAgentPackagePrefix+"0.2.0-linux-amd64.tar.gz")
	if err := os.WriteFile(real, []byte("agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := findDownloadPackage(distDir, recordingAgentPackagePrefix, "linux-amd64", "Agent", "stage", recordingAgentDistDirEnv)
	if err != nil {
		t.Fatalf("legit package should be found: %v", err)
	}
	if filepath.Base(got) != filepath.Base(real) {
		t.Fatalf("selected %q, want the real package %q (symlink must be excluded)", got, real)
	}
}

func TestPathWithinDir(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "pkg.tar.gz")
	if err := os.WriteFile(inside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !pathWithinDir(inside, dir) {
		t.Fatal("a regular file inside the dir should be within it")
	}
	if pathWithinDir(filepath.Join(dir, "..", "escape.tar.gz"), dir) {
		t.Fatal("a parent-relative path must not be reported as within the dir")
	}
}
