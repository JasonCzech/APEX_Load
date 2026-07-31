package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func writeStagedDownloadChecksums(t *testing.T, distDir string, filenames ...string) map[string]string {
	t.Helper()
	sums := make(map[string]string, len(filenames))
	var manifest strings.Builder
	for _, filename := range filenames {
		contents, err := os.ReadFile(filepath.Join(distDir, filename))
		if err != nil {
			t.Fatal(err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(contents))
		sums[filename] = sum
		fmt.Fprintf(&manifest, "%s  %s\n", sum, filename)
	}
	if err := os.WriteFile(filepath.Join(distDir, downloadChecksumFile), []byte(manifest.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return sums
}

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
	raName := "apexload-recording-agent-0.1.0-linux-amd64.tar.gz"
	if err := os.WriteFile(filepath.Join(raDir, raName), []byte("agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStagedDownloadChecksums(t, raDir, raName)
	dtDir := t.TempDir()
	dtName := "apexload-desktop-0.1.0-linux-amd64.tar.gz"
	if err := os.WriteFile(filepath.Join(dtDir, dtName), []byte("desktop"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStagedDownloadChecksums(t, dtDir, dtName)
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
	withCurrentSystemSettingsIfMatch(req, server)
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

func TestDownloadPlatformFromUserAgent(t *testing.T) {
	cases := []struct {
		name string
		ua   string
		want string
	}{
		{
			name: "mac compatibility UA is ambiguous",
			ua:   "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
			want: "",
		},
		{
			name: "intel mac explicit",
			ua:   "APEXLoad/1.0 (Macintosh; Mac OS X 14.0; x86_64)",
			want: "darwin-amd64",
		},
		{
			name: "apple silicon explicit",
			ua:   "APEXLoad/1.0 (Macintosh; Mac OS X 14.0; arm64)",
			want: "darwin-arm64",
		},
		{
			name: "ambiguous mac",
			ua:   "Mozilla/5.0 (Macintosh; Mac OS X 14_5) AppleWebKit/605.1.15",
			want: "",
		},
		{
			name: "linux arm",
			ua:   "curl/8.0 (Linux aarch64)",
			want: "linux-arm64",
		},
		{
			name: "android is not desktop linux",
			ua:   "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36",
			want: "",
		},
		{
			name: "unknown",
			ua:   "UnknownClient/1.0",
			want: "",
		},
		{
			name: "ambiguous linux",
			ua:   "Mozilla/5.0 (X11; Linux) AppleWebKit/537.36",
			want: "",
		},
		{
			name: "unsupported linux",
			ua:   "Mozilla/5.0 (X11; Linux riscv64) AppleWebKit/537.36",
			want: "",
		},
		{
			name: "linux x64",
			ua:   "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			want: "linux-amd64",
		},
		{
			name: "windows x64",
			ua:   "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
			want: "windows-amd64",
		},
		{
			name: "windows 32 bit",
			ua:   "Mozilla/5.0 (Windows NT 10.0; WOW32; x86)",
			want: "",
		},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/download", nil)
		req.Header.Set("User-Agent", tc.ua)
		if got := downloadPlatform(req); got != tc.want {
			t.Fatalf("%s: downloadPlatform() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClientDownloadsRejectInvalidExplicitPlatformWithoutUserAgentFallback(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 10})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/desktop/latest?platform=linux-arm46", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "platform query parameter is invalid") {
		t.Fatalf("body = %q, want invalid-platform guidance", resp.Body.String())
	}
}

func TestClientDownloadsRequireKnownPlatform(t *testing.T) {
	raDir := t.TempDir()
	for _, name := range []string{
		"apexload-recording-agent-0.1.0-linux-amd64.tar.gz",
		"apexload-recording-agent-0.1.0-darwin-arm64.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(raDir, name), []byte("agent"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dtDir := t.TempDir()
	for _, name := range []string{
		"apexload-desktop-0.1.0-linux-amd64.tar.gz",
		"apexload-desktop-0.1.0-darwin-arm64.tar.gz",
	} {
		if err := os.WriteFile(filepath.Join(dtDir, name), []byte("desktop"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(recordingAgentDistDirEnv, raDir)
	t.Setenv(desktopDistDirEnv, dtDir)

	server := NewServer(ServerConfig{MaxConcurrent: 10})
	endpoints := []string{
		"/api/v1/downloads/recording-agent/latest",
		"/api/v1/downloads/recording-agent/latest/checksum",
		"/api/v1/downloads/desktop/latest",
		"/api/v1/downloads/desktop/latest/checksum",
	}
	for _, endpoint := range endpoints {
		req := httptest.NewRequest(http.MethodGet, endpoint, nil)
		req.Header.Set("User-Agent", "UnknownClient/1.0")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 body=%s", endpoint, resp.Code, resp.Body.String())
		}
		if !strings.Contains(resp.Body.String(), "platform query parameter is required") {
			t.Fatalf("%s body = %q, want platform guidance", endpoint, resp.Body.String())
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

func TestFindDownloadPackageUsesSemanticVersionOrdering(t *testing.T) {
	distDir := t.TempDir()
	for _, version := range []string{"1.9.0", "1.10.0", "invalid"} {
		name := recordingAgentPackagePrefix + version + "-linux-amd64.tar.gz"
		if err := os.WriteFile(filepath.Join(distDir, name), []byte(version), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := findDownloadPackage(distDir, recordingAgentPackagePrefix, "linux-amd64", "Agent", "stage", recordingAgentDistDirEnv)
	if err != nil {
		t.Fatal(err)
	}
	if want := recordingAgentPackagePrefix + "1.10.0-linux-amd64.tar.gz"; filepath.Base(got) != want {
		t.Fatalf("selected %q, want %q", filepath.Base(got), want)
	}
}

func TestFindDownloadPackageRejectsEqualPrecedenceBuildVariants(t *testing.T) {
	distDir := t.TempDir()
	for _, version := range []string{"1.10.0+build.1", "1.10.0+build.2"} {
		name := recordingAgentPackagePrefix + version + "-linux-amd64.tar.gz"
		if err := os.WriteFile(filepath.Join(distDir, name), []byte(version), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := findDownloadPackage(distDir, recordingAgentPackagePrefix, "linux-amd64", "Agent", "stage", recordingAgentDistDirEnv); err == nil || !strings.Contains(err.Error(), "ambiguous build variants") {
		t.Fatalf("equal-precedence variants were not rejected: %v", err)
	}
}

func TestPackageChecksumRequiresExactlyOneCanonicalBasenameRow(t *testing.T) {
	distDir := t.TempDir()
	name := recordingAgentPackagePrefix + "2.0.0-linux-amd64.tar.gz"
	if err := os.WriteFile(filepath.Join(distDir, name), []byte("package"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := writeStagedDownloadChecksums(t, distDir, name)[name]
	manifestPath := filepath.Join(distDir, downloadChecksumFile)
	tests := []struct {
		name     string
		manifest string
		wantOK   bool
	}{
		{name: "canonical", manifest: sum + "  " + name + "\n", wantOK: true},
		{name: "dot slash filename", manifest: sum + "  ./" + name + "\n"},
		{name: "uppercase digest", manifest: strings.ToUpper(sum) + "  " + name + "\n"},
		{name: "duplicate row", manifest: sum + "  " + name + "\n" + sum + "  " + name + "\n"},
		{name: "extra field", manifest: sum + "  unexpected  " + name + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(manifestPath, []byte(test.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			got, ok := packageChecksumSHA256(distDir, name)
			if ok != test.wantOK {
				t.Fatalf("packageChecksumSHA256() = %q, %v, want ok=%v", got, ok, test.wantOK)
			}
		})
	}
}

func TestDownloadPackageHandoffFailsClosedUntilChecksumMatches(t *testing.T) {
	distDir := t.TempDir()
	name := recordingAgentPackagePrefix + "2.0.0-linux-amd64.tar.gz"
	packagePath := filepath.Join(distDir, name)
	if err := os.WriteFile(packagePath, []byte("old bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStagedDownloadChecksums(t, distDir, name)
	if err := os.WriteFile(packagePath, []byte("new bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	checksumResp := httptest.NewRecorder()
	writeDownloadChecksumJSON(checksumResp, packagePath)
	if checksumResp.Code != http.StatusOK {
		t.Fatalf("staged checksum metadata status = %d, want 200 body=%s", checksumResp.Code, checksumResp.Body.String())
	}
	// The metadata route returns the manifest's expected digest without hashing
	// a potentially large archive. The actual download below remains fail-closed
	// until those expected bytes are published.

	resp := httptest.NewRecorder()
	serveDownloadPackage(resp, req, packagePath)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("mismatched handoff status = %d, want 503 body=%s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "new bytes") || resp.Header().Get("Digest") != "" {
		t.Fatal("mismatched package bytes or trusted digest escaped the fail-closed handoff")
	}

	sums := writeStagedDownloadChecksums(t, distDir, name)
	resp = httptest.NewRecorder()
	serveDownloadPackage(resp, req, packagePath)
	if resp.Code != http.StatusOK || resp.Body.String() != "new bytes" {
		t.Fatalf("completed handoff status/body = %d/%q", resp.Code, resp.Body.String())
	}
	if resp.Header().Get("X-Content-SHA256") != sums[name] {
		t.Fatal("completed handoff did not surface the checksum for the served snapshot")
	}
}

func TestDownloadPackageExtendsArtifactWriteDeadline(t *testing.T) {
	distDir := t.TempDir()
	name := recordingAgentPackagePrefix + "2.0.0-linux-amd64.tar.gz"
	packagePath := filepath.Join(distDir, name)
	if err := os.WriteFile(packagePath, []byte("package bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStagedDownloadChecksums(t, distDir, name)
	recorder := &archiveDeadlineRecorder{}
	started := time.Now()

	serveDownloadPackage(recorder, httptest.NewRequest(http.MethodGet, "/download", nil), packagePath)

	minimum := started.Add(configuredArchiveMaterializationTimeout() + 4*time.Second)
	if recorder.deadline.Before(minimum) {
		t.Fatalf("package write deadline = %v, want at least %v", recorder.deadline, minimum)
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
