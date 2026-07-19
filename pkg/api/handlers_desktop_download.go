package api

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"golang.org/x/mod/semver"
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
	if strings.HasPrefix(platform, "linux-") {
		deb, debErr := findDownloadPackageWithSuffix(distDir, desktopPackagePrefix, platform, ".deb", "APEX Load Desktop", desktopStageCommand, desktopDistDirEnv)
		tarball, tarErr := findDownloadPackage(distDir, desktopPackagePrefix, platform, "APEX Load Desktop", desktopStageCommand, desktopDistDirEnv)
		switch {
		case debErr != nil && tarErr != nil:
			return "", tarErr
		case debErr != nil:
			return tarball, nil
		case tarErr != nil:
			return deb, nil
		}
		debVersion, debOK := downloadPackageVersion(deb, desktopPackagePrefix, platform, ".deb")
		tarVersion, tarOK := downloadPackageVersion(tarball, desktopPackagePrefix, platform, ".tar.gz")
		if !debOK || !tarOK {
			return "", fmt.Errorf("APEX Load Desktop package inventory contains an invalid versioned filename")
		}
		comparison := semver.Compare("v"+debVersion, "v"+tarVersion)
		if comparison > 0 {
			return deb, nil
		}
		if comparison < 0 {
			return tarball, nil
		}
		if debVersion != tarVersion {
			return "", fmt.Errorf("APEX Load Desktop package inventory contains inconsistent build variants for the latest Linux version")
		}
		return deb, nil
	}
	return findDownloadPackage(distDir, desktopPackagePrefix, platform, "APEX Load Desktop", desktopStageCommand, desktopDistDirEnv)
}
