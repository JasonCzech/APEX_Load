package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleDownloadDesktopAppServesStagedPackage(t *testing.T) {
	distDir := t.TempDir()
	packageName := "apexload-desktop-0.1.0-darwin-arm64.tar.gz"
	packagePath := filepath.Join(distDir, packageName)
	if err := os.WriteFile(packagePath, []byte("desktop package"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(desktopDistDirEnv, distDir)
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/desktop/latest?platform=darwin-arm64", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if got := resp.Body.String(); got != "desktop package" {
		t.Fatalf("body = %q, want package content", got)
	}
	if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, packageName) {
		t.Fatalf("content-disposition = %q, want package filename", got)
	}
	if got := resp.Header().Get("Content-Type"); !strings.Contains(got, "application/gzip") {
		t.Fatalf("content-type = %q, want application/gzip", got)
	}
}

func TestHandleDownloadDesktopAppMissingPackageReturnsActionableError(t *testing.T) {
	t.Setenv(desktopDistDirEnv, t.TempDir())
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/desktop/latest?platform=windows-amd64", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusNotFound, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "<div id=\"root\"></div>") {
		t.Fatalf("body served dashboard shell instead of API error: %q", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "APEX Load Desktop package for windows-amd64 is not staged") {
		t.Fatalf("body = %q, want actionable package staging error", resp.Body.String())
	}
}

func TestDesktopDownloadSurfacesChecksum(t *testing.T) {
	distDir := t.TempDir()
	packageName := "apexload-desktop-0.1.0-linux-amd64.tar.gz"
	if err := os.WriteFile(filepath.Join(distDir, packageName), []byte("desktop package"), 0o600); err != nil {
		t.Fatal(err)
	}
	const sum = "d8df2adeb0f3718d0beeebb225572273e1677b6208cef613c2378f3b3f41f3b2"
	if err := os.WriteFile(filepath.Join(distDir, downloadChecksumFile), []byte(sum+"  "+packageName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(desktopDistDirEnv, distDir)
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/desktop/latest?platform=linux-amd64", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("download status = %d body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Header().Get("X-Content-SHA256"); got != sum {
		t.Fatalf("X-Content-SHA256 = %q, want %q", got, sum)
	}
	if got := resp.Header().Get("Digest"); !strings.HasPrefix(got, "sha-256=") {
		t.Fatalf("Digest = %q, want sha-256= prefix", got)
	}

	creq := httptest.NewRequest(http.MethodGet, "/api/v1/downloads/desktop/latest/checksum?platform=linux-amd64", nil)
	cresp := httptest.NewRecorder()
	server.mux.ServeHTTP(cresp, creq)
	if cresp.Code != http.StatusOK {
		t.Fatalf("checksum status = %d body=%s", cresp.Code, cresp.Body.String())
	}
	if !strings.Contains(cresp.Body.String(), sum) {
		t.Fatalf("checksum body = %q, want sha256 %s", cresp.Body.String(), sum)
	}
}
