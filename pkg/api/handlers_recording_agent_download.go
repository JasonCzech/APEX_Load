package api

import (
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
)

func (s *Server) handleDownloadRecordingAgent(w http.ResponseWriter, r *http.Request) {
	packagePath, err := findRecordingAgentPackage(recordingAgentDistDir(), recordingAgentPlatform(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	filename := filepath.Base(packagePath)
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(filename)))
	http.ServeFile(w, r, packagePath)
}

func recordingAgentDistDir() string {
	if dir := strings.TrimSpace(os.Getenv(recordingAgentDistDirEnv)); dir != "" {
		return dir
	}
	return defaultRecordingAgentDistDir
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
