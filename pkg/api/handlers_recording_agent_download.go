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

const (
	recordingAgentDistDirEnv     = "APEXLOAD_RECORDING_AGENT_DIST_DIR"
	defaultRecordingAgentDistDir = "dist/recording-agent"
	recordingAgentPackagePrefix  = "apexload-recording-agent-"
	recordingAgentChecksumFile   = "SHA256SUMS.txt"
)

func (s *Server) handleDownloadRecordingAgent(w http.ResponseWriter, r *http.Request) {
	packagePath, err := findRecordingAgentPackage(recordingAgentDistDir(), recordingAgentPlatform(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	filename := filepath.Base(packagePath)
	if sum, ok := recordingAgentPackageSHA256(filepath.Dir(packagePath), filename); ok {
		w.Header().Set("X-Content-SHA256", sum)
		if raw, decErr := hex.DecodeString(sum); decErr == nil {
			// RFC 3230 Digest header so clients can verify the binary before running it.
			w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(raw))
		}
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(filename)))
	http.ServeFile(w, r, packagePath)
}

// handleRecordingAgentChecksum returns the SHA-256 of the recording-agent package the
// download endpoint would serve, so the dashboard can show the expected hash next to the
// download and users can verify the artifact out of band.
func (s *Server) handleRecordingAgentChecksum(w http.ResponseWriter, r *http.Request) {
	packagePath, err := findRecordingAgentPackage(recordingAgentDistDir(), recordingAgentPlatform(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	filename := filepath.Base(packagePath)
	sum, ok := recordingAgentPackageSHA256(filepath.Dir(packagePath), filename)
	if !ok {
		writeError(w, http.StatusNotFound, "checksum for the APEX Load Recording Agent package is not staged")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"algorithm": "sha-256",
		"filename":  filename,
		"sha256":    sum,
	})
}

// recordingAgentPackageSHA256 returns the hex SHA-256 for filename from the SHA256SUMS.txt
// staged alongside the packages (lines are "<hex>  <name>"), if present.
func recordingAgentPackageSHA256(distDir, filename string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(distDir, recordingAgentChecksumFile))
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

func recordingAgentDistDir() string {
	if dir := strings.TrimSpace(os.Getenv(recordingAgentDistDirEnv)); dir != "" {
		return resolveRecordingAgentDistDir(dir)
	}
	return resolveRecordingAgentDistDir(defaultRecordingAgentDistDir)
}

func resolveRecordingAgentDistDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = defaultRecordingAgentDistDir
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	if resolved, ok := findRelativeRecordingAgentDistDir(dir, recordingAgentSearchBases()...); ok {
		return resolved
	}
	return dir
}

func recordingAgentSearchBases() []string {
	var bases []string
	if wd, err := os.Getwd(); err == nil {
		bases = append(bases, wd)
	}
	if exe, err := os.Executable(); err == nil {
		bases = append(bases, filepath.Dir(exe))
	}
	return bases
}

func findRelativeRecordingAgentDistDir(rel string, bases ...string) (string, bool) {
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

func recordingAgentPlatform(r *http.Request) string {
	platform := sanitizeRecordingAgentPlatform(r.URL.Query().Get("platform"))
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

func sanitizeRecordingAgentPlatform(platform string) string {
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

func findRecordingAgentPackage(distDir, platform string) (string, error) {
	distDir = strings.TrimSpace(distDir)
	if distDir == "" {
		distDir = defaultRecordingAgentDistDir
	}
	entries, err := os.ReadDir(distDir)
	if err != nil {
		return "", fmt.Errorf("APEX Load Recording Agent package directory is not available. Run make package-recording-agent or set %s to the release package directory", recordingAgentDistDirEnv)
	}

	var matches []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, recordingAgentPackagePrefix) || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		if platform != "" && !strings.Contains(name, "-"+platform+".tar.gz") {
			continue
		}
		matches = append(matches, filepath.Join(distDir, name))
	}
	sort.Strings(matches)
	if len(matches) == 0 && platform != "" {
		return "", fmt.Errorf("APEX Load Recording Agent package for %s is not staged. Run make package-recording-agent or set %s to a directory containing %s*-%s.tar.gz", platform, recordingAgentDistDirEnv, recordingAgentPackagePrefix, platform)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("APEX Load Recording Agent package is not staged. Run make package-recording-agent or set %s to the release package directory", recordingAgentDistDirEnv)
	}
	return matches[len(matches)-1], nil
}
