package api

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// clientDownloadsDisabledMessage is returned by every client-software download
// endpoint when an administrator has turned downloads off (the fail-closed half
// of the client_downloads setting; the dashboard hides the button as the other
// half).
const clientDownloadsDisabledMessage = "client software downloads are disabled by an administrator"

// downloadChecksumFile is the sha256sum-format manifest staged alongside the
// release packages (lines are "<hex>  <name>"). Shared by the recording-agent
// and desktop download handlers.
const downloadChecksumFile = "SHA256SUMS.txt"

// clientDownloadsAvailable reports whether client-software downloads are enabled
// by the current admin settings. Used to gate the recording-agent and desktop
// download/checksum endpoints.
func (s *Server) clientDownloadsAvailable() bool {
	return clientDownloadsEnabled(s.runner.GetSystemSettings().ClientDownloads)
}

// downloadPlatform resolves the target platform for a download from an explicit
// ?platform= query (allow-listed) or, failing that, the request User-Agent.
func downloadPlatform(r *http.Request) string {
	platform := sanitizeDownloadPlatform(r.URL.Query().Get("platform"))
	if platform != "" {
		return platform
	}
	ua := strings.ToLower(r.UserAgent())
	switch {
	case strings.Contains(ua, "windows"):
		return "windows-amd64"
	case strings.Contains(ua, "macintosh"), strings.Contains(ua, "mac os x"):
		return "darwin-arm64"
	case strings.Contains(ua, "linux"):
		return "linux-amd64"
	default:
		return ""
	}
}

func sanitizeDownloadPlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	platform = strings.ReplaceAll(platform, "/", "-")
	allowed := map[string]struct{}{
		"darwin-arm64":  {},
		"darwin-amd64":  {},
		"windows-amd64": {},
		"linux-amd64":   {},
		"linux-arm64":   {},
	}
	if _, ok := allowed[platform]; ok {
		return platform
	}
	return ""
}

// resolveDownloadDistDir resolves a configured (possibly relative) dist directory
// to a concrete path, falling back to defaultDir. Relative paths are searched for
// upward from the working directory and the executable directory so a package
// staged at repo root is found regardless of the controller's CWD.
func resolveDownloadDistDir(dir, defaultDir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = defaultDir
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	if resolved, ok := findRelativeDistDir(dir, downloadDistSearchBases()...); ok {
		return resolved
	}
	return dir
}

func downloadDistSearchBases() []string {
	var bases []string
	if wd, err := os.Getwd(); err == nil {
		bases = append(bases, wd)
	}
	if exe, err := os.Executable(); err == nil {
		bases = append(bases, filepath.Dir(exe))
	}
	return bases
}

func findRelativeDistDir(rel string, bases ...string) (string, bool) {
	rel = strings.TrimSpace(rel)
	if rel == "" || filepath.IsAbs(rel) {
		return rel, false
	}
	for _, base := range bases {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		for {
			candidate := filepath.Join(base, rel)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate, true
			}
			parent := filepath.Dir(base)
			if parent == base {
				break
			}
			base = parent
		}
	}
	return rel, false
}

// findDownloadPackage returns the newest staged "<prefix>*-<platform>.tar.gz" in
// distDir. humanName/stageCmd/distDirEnv are woven into the not-found errors so
// each artifact surfaces an actionable staging hint.
func findDownloadPackage(distDir, prefix, platform, humanName, stageCmd, distDirEnv string) (string, error) {
	distDir = strings.TrimSpace(distDir)
	entries, err := os.ReadDir(distDir)
	if err != nil {
		return "", fmt.Errorf("%s package directory is not available. Run %s or set %s to the release package directory", humanName, stageCmd, distDirEnv)
	}

	var matches []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		if platform != "" && !strings.Contains(name, "-"+platform+".tar.gz") {
			continue
		}
		matches = append(matches, filepath.Join(distDir, name))
	}
	sort.Strings(matches)
	if len(matches) == 0 && platform != "" {
		return "", fmt.Errorf("%s package for %s is not staged. Run %s or set %s to a directory containing %s*-%s.tar.gz", humanName, platform, stageCmd, distDirEnv, prefix, platform)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%s package is not staged. Run %s or set %s to the release package directory", humanName, stageCmd, distDirEnv)
	}
	return matches[len(matches)-1], nil
}

// packageChecksumSHA256 returns the hex SHA-256 for filename from the
// downloadChecksumFile staged alongside the packages, if present.
func packageChecksumSHA256(distDir, filename string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(distDir, downloadChecksumFile))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// sha256sum binary-mode prefixes the name with '*'.
		if strings.TrimPrefix(fields[1], "*") == filename {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// serveDownloadPackage streams a staged package with integrity headers
// (X-Content-SHA256 + RFC 3230 Digest) so clients can verify the binary before
// running it.
func serveDownloadPackage(w http.ResponseWriter, r *http.Request, packagePath string) {
	filename := filepath.Base(packagePath)
	if sum, ok := packageChecksumSHA256(filepath.Dir(packagePath), filename); ok {
		w.Header().Set("X-Content-SHA256", sum)
		if raw, decErr := hex.DecodeString(sum); decErr == nil {
			w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(raw))
		}
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(filename)))
	http.ServeFile(w, r, packagePath)
}

// writeDownloadChecksumJSON returns the package's SHA-256 as JSON, or 404 when no
// checksum is staged.
func writeDownloadChecksumJSON(w http.ResponseWriter, packagePath string) {
	filename := filepath.Base(packagePath)
	sum, ok := packageChecksumSHA256(filepath.Dir(packagePath), filename)
	if !ok {
		writeError(w, http.StatusNotFound, "checksum for the requested package is not staged")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"algorithm": "sha-256",
		"filename":  filename,
		"sha256":    sum,
	})
}
