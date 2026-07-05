package api

import (
	"net/http"
	"os"
)

const (
	recordingAgentDistDirEnv     = "APEXLOAD_RECORDING_AGENT_DIST_DIR"
	defaultRecordingAgentDistDir = "dist/recording-agent"
	recordingAgentPackagePrefix  = "apexload-recording-agent-"
	recordingAgentStageCommand   = "make package-recording-agent"
	// recordingAgentChecksumFile is retained for tests; the manifest name is
	// shared across client-software downloads (see downloadChecksumFile).
	recordingAgentChecksumFile = downloadChecksumFile
)

func (s *Server) handleDownloadRecordingAgent(w http.ResponseWriter, r *http.Request) {
	if !s.clientDownloadsAvailable() {
		writeError(w, http.StatusForbidden, clientDownloadsDisabledMessage)
		return
	}
	platform, ok := requireDownloadPlatform(w, r, "APEX Load Recording Agent")
	if !ok {
		return
	}
	packagePath, err := findRecordingAgentPackage(recordingAgentDistDir(), platform)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	serveDownloadPackage(w, r, packagePath)
}

// handleRecordingAgentChecksum returns the SHA-256 of the recording-agent package the
// download endpoint would serve, so the dashboard can show the expected hash next to the
// download and users can verify the artifact out of band.
func (s *Server) handleRecordingAgentChecksum(w http.ResponseWriter, r *http.Request) {
	if !s.clientDownloadsAvailable() {
		writeError(w, http.StatusForbidden, clientDownloadsDisabledMessage)
		return
	}
	platform, ok := requireDownloadPlatform(w, r, "APEX Load Recording Agent")
	if !ok {
		return
	}
	packagePath, err := findRecordingAgentPackage(recordingAgentDistDir(), platform)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeDownloadChecksumJSON(w, packagePath)
}

func recordingAgentDistDir() string {
	return resolveDownloadDistDir(os.Getenv(recordingAgentDistDirEnv), defaultRecordingAgentDistDir)
}

func findRecordingAgentPackage(distDir, platform string) (string, error) {
	return findDownloadPackage(distDir, recordingAgentPackagePrefix, platform, "APEX Load Recording Agent", recordingAgentStageCommand, recordingAgentDistDirEnv)
}
