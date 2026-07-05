package api

import (
	"net/http"
	"os"
)

const (
	desktopDistDirEnv     = "APEXLOAD_DESKTOP_DIST_DIR"
	defaultDesktopDistDir = "dist/desktop"
	desktopPackagePrefix  = "apexload-desktop-"
	desktopStageCommand   = "make desktop-release"
)

func (s *Server) handleDownloadDesktopApp(w http.ResponseWriter, r *http.Request) {
	if !s.clientDownloadsAvailable() {
		writeError(w, http.StatusForbidden, clientDownloadsDisabledMessage)
		return
	}
	platform, ok := requireDownloadPlatform(w, r, "APEX Load Desktop")
	if !ok {
		return
	}
	packagePath, err := findDesktopPackage(desktopDistDir(), platform)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	serveDownloadPackage(w, r, packagePath)
}

// handleDesktopAppChecksum returns the SHA-256 of the desktop package the download
// endpoint would serve, so the dashboard can show the expected hash and users can
// verify the artifact out of band.
func (s *Server) handleDesktopAppChecksum(w http.ResponseWriter, r *http.Request) {
	if !s.clientDownloadsAvailable() {
		writeError(w, http.StatusForbidden, clientDownloadsDisabledMessage)
		return
	}
	platform, ok := requireDownloadPlatform(w, r, "APEX Load Desktop")
	if !ok {
		return
	}
	packagePath, err := findDesktopPackage(desktopDistDir(), platform)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeDownloadChecksumJSON(w, packagePath)
}

func desktopDistDir() string {
	return resolveDownloadDistDir(os.Getenv(desktopDistDirEnv), defaultDesktopDistDir)
}

func findDesktopPackage(distDir, platform string) (string, error) {
	return findDownloadPackage(distDir, desktopPackagePrefix, platform, "APEX Load Desktop", desktopStageCommand, desktopDistDirEnv)
}
