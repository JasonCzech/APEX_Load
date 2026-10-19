package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
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
	if _, present := r.URL.Query()["platform"]; present {
		return sanitizeDownloadPlatform(r.URL.Query().Get("platform"))
	}
	ua := strings.ToLower(r.UserAgent())
	switch {
	case strings.Contains(ua, "android"):
		// Android reports "Linux" for compatibility but cannot run a Linux
		// desktop package. Require an explicit supported platform instead.
		return ""
	case strings.Contains(ua, "windows"):
		if strings.Contains(ua, "win64") || strings.Contains(ua, "x64") || strings.Contains(ua, "x86_64") || strings.Contains(ua, "amd64") {
			return "windows-amd64"
		}
		return ""
	case strings.Contains(ua, "macintosh"), strings.Contains(ua, "mac os x"):
		// Apple Silicon browsers intentionally retain "Intel Mac OS X" in their
		// compatibility UA, and Intel Macs have no package. Only an explicit
		// Apple Silicon token is trustworthy.
		if strings.Contains(ua, "arm64") || strings.Contains(ua, "aarch64") || strings.Contains(ua, "apple silicon") {
			return "darwin-arm64"
		}
		return ""
	case strings.Contains(ua, "linux"):
		if strings.Contains(ua, "aarch64") || strings.Contains(ua, "arm64") {
			return "linux-arm64"
		}
		if strings.Contains(ua, "x86_64") || strings.Contains(ua, "amd64") {
			return "linux-amd64"
		}
		return ""
	default:
		return ""
	}
}

func requireDownloadPlatform(w http.ResponseWriter, r *http.Request, humanName string) (string, bool) {
	if values, present := r.URL.Query()["platform"]; present && (len(values) != 1 || sanitizeDownloadPlatform(values[0]) == "") {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("platform query parameter is invalid for %s downloads", humanName))
		return "", false
	}
	platform := downloadPlatform(r)
	if platform == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("platform query parameter is required for %s downloads", humanName))
		return "", false
	}
	return platform, true
}

func sanitizeDownloadPlatform(platform string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	platform = strings.ReplaceAll(platform, "/", "-")
	allowed := map[string]struct{}{
		"darwin-arm64":  {},
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

// pathWithinDir reports whether file, after resolving symlinks in both file and
// dir, stays inside dir. It is used to keep download handlers from serving a
// symlink staged inside the dist directory that points outside it.
func pathWithinDir(file, dir string) bool {
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolvedDir = dir
	}
	resolvedDir, err = filepath.Abs(resolvedDir)
	if err != nil {
		return false
	}
	resolvedFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		// File missing or unreadable: fall back to the lexical path so a
		// non-existent target is simply not matched downstream.
		resolvedFile = file
	}
	resolvedFile, err = filepath.Abs(resolvedFile)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedDir, resolvedFile)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// findDownloadPackage returns the newest staged "<prefix>*-<platform>.tar.gz" in
// distDir. humanName/stageCmd/distDirEnv are woven into the not-found errors so
// each artifact surfaces an actionable staging hint.
func findDownloadPackage(distDir, prefix, platform, humanName, stageCmd, distDirEnv string) (string, error) {
	return findDownloadPackageWithSuffix(distDir, prefix, platform, ".tar.gz", humanName, stageCmd, distDirEnv)
}

func findDownloadPackageWithSuffix(distDir, prefix, platform, suffix, humanName, stageCmd, distDirEnv string) (string, error) {
	distDir = strings.TrimSpace(distDir)
	entries, err := os.ReadDir(distDir)
	if err != nil {
		return "", fmt.Errorf("%s package directory is not available. Run %s or set %s to the release package directory", humanName, stageCmd, distDirEnv)
	}

	type versionedPackage struct {
		path    string
		version string
	}
	var matches []versionedPackage
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		platformSuffix := suffix
		if platform != "" {
			platformSuffix = "-" + platform + suffix
		}
		if !strings.HasSuffix(name, platformSuffix) {
			continue
		}
		version := strings.TrimSuffix(strings.TrimPrefix(name, prefix), platformSuffix)
		if version == "" || !semver.IsValid("v"+version) {
			continue
		}
		candidate := filepath.Join(distDir, name)
		// Defense in depth: never serve a staged entry whose real path (after
		// resolving symlinks) escapes the dist directory. This blocks a symlinked
		// "<prefix>-<platform>.tar.gz" from exfiltrating arbitrary host files.
		if !pathWithinDir(candidate, distDir) {
			continue
		}
		matches = append(matches, versionedPackage{path: candidate, version: version})
	}
	sort.Slice(matches, func(i, j int) bool {
		comparison := semver.Compare("v"+matches[i].version, "v"+matches[j].version)
		if comparison == 0 {
			return matches[i].path < matches[j].path
		}
		return comparison < 0
	})
	if len(matches) == 0 && platform != "" {
		return "", fmt.Errorf("%s package for %s is not staged. Run %s or set %s to a directory containing %s*-%s%s", humanName, platform, stageCmd, distDirEnv, prefix, platform, suffix)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%s package is not staged. Run %s or set %s to the release package directory", humanName, stageCmd, distDirEnv)
	}
	if len(matches) > 1 {
		latest := matches[len(matches)-1]
		previous := matches[len(matches)-2]
		if latest.version != previous.version && semver.Compare("v"+latest.version, "v"+previous.version) == 0 {
			return "", fmt.Errorf("%s package inventory contains ambiguous build variants for the latest version", humanName)
		}
	}
	return matches[len(matches)-1].path, nil
}

func downloadPackageVersion(packagePath, prefix, platform, suffix string) (string, bool) {
	name := filepath.Base(packagePath)
	platformSuffix := "-" + platform + suffix
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, platformSuffix) {
		return "", false
	}
	version := strings.TrimSuffix(strings.TrimPrefix(name, prefix), platformSuffix)
	return version, version != "" && semver.IsValid("v"+version)
}

// packageChecksumSHA256 returns the hex SHA-256 for filename from the
// downloadChecksumFile staged alongside the packages, if present.
func packageChecksumSHA256(distDir, filename string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(distDir, downloadChecksumFile))
	if err != nil {
		return "", false
	}
	matchedRows := 0
	matchedSum := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.TrimPrefix(fields[len(fields)-1], "*") != filename {
			continue
		}
		matchedRows++
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 || fields[0] != strings.ToLower(fields[0]) {
			return "", false
		}
		raw, decodeErr := hex.DecodeString(fields[0])
		if decodeErr != nil || len(raw) != sha256.Size {
			return "", false
		}
		matchedSum = fields[0]
	}
	return matchedSum, matchedRows == 1
}

var errDownloadChecksumNotStaged = errors.New("download checksum is not staged")

func openVerifiedDownloadPackage(packagePath string) (*os.File, os.FileInfo, string, error) {
	file, info, expected, err := openStagedDownloadPackage(packagePath)
	if err != nil {
		return nil, nil, "", err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, nil, "", fmt.Errorf("hash package: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return nil, nil, "", errors.New("download package does not match its staged checksum")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, nil, "", fmt.Errorf("rewind package: %w", err)
	}
	closeOnError = false
	return file, info, expected, nil
}

func openStagedDownloadPackage(packagePath string) (*os.File, os.FileInfo, string, error) {
	distDir := filepath.Dir(packagePath)
	filename := filepath.Base(packagePath)
	expected, ok := packageChecksumSHA256(distDir, filename)
	if !ok {
		return nil, nil, "", errDownloadChecksumNotStaged
	}

	root, err := os.OpenRoot(distDir)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open package directory: %w", err)
	}
	defer root.Close()
	file, err := root.Open(filename)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open package: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, "", fmt.Errorf("stat package: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, "", errors.New("download package is not a regular file")
	}
	return file, info, expected, nil
}

// serveDownloadPackage streams a staged package with integrity headers
// (X-Content-SHA256 + RFC 3230 Digest) so clients can verify the binary before
// running it.
func serveDownloadPackage(w http.ResponseWriter, r *http.Request, packagePath string) {
	extendArtifactWriteDeadline(w)
	filename := filepath.Base(packagePath)
	file, info, sum, err := openVerifiedDownloadPackage(packagePath)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "download package integrity verification failed")
		return
	}
	defer file.Close()
	w.Header().Set("X-Content-SHA256", sum)
	raw, _ := hex.DecodeString(sum)
	w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(raw))
	contentType := "application/gzip"
	if strings.HasSuffix(strings.ToLower(filename), ".deb") {
		contentType = "application/vnd.debian.binary-package"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(filename)))
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

// writeDownloadChecksumJSON returns the unique canonical digest from the staged
// release manifest after confirming the package exists as a confined regular
// file. The download path hashes the exact open file it serves; avoiding that
// full archive read here keeps this metadata endpoint inexpensive.
func writeDownloadChecksumJSON(w http.ResponseWriter, packagePath string) {
	filename := filepath.Base(packagePath)
	file, _, sum, err := openStagedDownloadPackage(packagePath)
	if errors.Is(err, errDownloadChecksumNotStaged) {
		writeError(w, http.StatusNotFound, "checksum for the requested package is not staged")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "download package integrity verification failed")
		return
	}
	_ = file.Close()
	writeJSON(w, http.StatusOK, map[string]string{
		"algorithm": "sha-256",
		"filename":  filename,
		"sha256":    sum,
	})
}
