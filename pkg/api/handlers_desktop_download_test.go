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
	writeStagedDownloadChecksums(t, distDir, packageName)
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
	packageName := "apexload-desktop-0.1.0-linux-amd64.deb"
	tarName := "apexload-desktop-0.1.0-linux-amd64.tar.gz"
	if err := os.WriteFile(filepath.Join(distDir, packageName), []byte("installable desktop package"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, tarName), []byte("raw desktop archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	sums := writeStagedDownloadChecksums(t, distDir, packageName, tarName)
	sum := sums[packageName]
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
	if got := resp.Header().Get("Content-Disposition"); !strings.Contains(got, packageName) {
		t.Fatalf("Content-Disposition = %q, want preferred Debian package %q", got, packageName)
	}
	if got := resp.Header().Get("Content-Type"); got != "application/vnd.debian.binary-package" {
		t.Fatalf("Content-Type = %q, want Debian package type", got)
	}
	if got := resp.Body.String(); got != "installable desktop package" {
		t.Fatalf("body = %q, want Debian package bytes", got)
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

func TestFindDesktopPackageChoosesNewestVersionAcrossLinuxFormats(t *testing.T) {
	distDir := t.TempDir()
	oldDeb := "apexload-desktop-1.9.0-linux-amd64.deb"
	newTar := "apexload-desktop-1.10.0-linux-amd64.tar.gz"
	for _, name := range []string{oldDeb, newTar} {
		if err := os.WriteFile(filepath.Join(distDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := findDesktopPackage(distDir, "linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != newTar {
		t.Fatalf("selected %q, want newer tarball %q", filepath.Base(got), newTar)
	}

	newDeb := "apexload-desktop-1.10.0-linux-amd64.deb"
	if err := os.WriteFile(filepath.Join(distDir, newDeb), []byte(newDeb), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = findDesktopPackage(distDir, "linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != newDeb {
		t.Fatalf("selected %q, want Debian package for same latest version %q", filepath.Base(got), newDeb)
	}
}
