package javarunner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errOperatorCDS = errors.New("operator_cds_manifest_invalid")

// OperatorCDSManifest binds an offline-built archive to exact runtime inputs.
// All artifacts must be regular files on operator-controlled immutable mounts.
// Directories and mutable archive files are deliberately not supported.
type OperatorCDSManifest struct {
	Schema          string   `json:"schema"`
	JavaVersion     string   `json:"java_version"`
	JavaSHA256      string   `json:"java_sha256"`
	ReleaseSHA256   string   `json:"release_sha256"`
	HarnessSHA256   string   `json:"harness_sha256"`
	ClasspathSHA256 []string `json:"classpath_sha256"`
	ArchivePath     string   `json:"archive_path"`
	ArchiveSHA256   string   `json:"archive_sha256"`
}

func validateOperatorCDS(spec CommandSpec, classpath []string) (string, error) {
	if spec.ResolvedJDK == nil || spec.ResolvedJDK.Major < 17 || spec.ResolvedJDK.VMFamily != JavaVMHotSpot ||
		spec.JavaMajor != spec.ResolvedJDK.Major || spec.VMFamily != spec.ResolvedJDK.VMFamily {
		return "", errOperatorCDS
	}
	for _, arg := range spec.VMParameters {
		if strings.HasPrefix(strings.ToLower(arg), "-xshare:") {
			return "", errOperatorCDS
		}
	}
	// Check type before opening: a FIFO can block at Open, before f.Stat runs.
	initial, err := os.Stat(spec.OperatorCDSManifest)
	if err != nil || !initial.Mode().IsRegular() {
		return "", errOperatorCDS
	}
	f, err := os.Open(spec.OperatorCDSManifest)
	if err != nil {
		return "", errOperatorCDS
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return "", errOperatorCDS
	}
	var m OperatorCDSManifest
	dec := json.NewDecoder(io.LimitReader(f, (64<<10)+1))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil {
		return "", errOperatorCDS
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return "", errOperatorCDS
	}
	if m.Schema != "apexload.java-vuser-cds/v1" || m.JavaVersion != spec.ResolvedJDK.Version ||
		len(m.ClasspathSHA256) != len(classpath) || !filepath.IsAbs(m.ArchivePath) || strings.ContainsAny(m.ArchivePath, "\r\n\x00"+string(os.PathListSeparator)) {
		return "", errOperatorCDS
	}
	archive, err := filepath.EvalSymlinks(m.ArchivePath)
	if err != nil {
		return "", errOperatorCDS
	}
	archiveInfo, err := os.Stat(archive)
	if err != nil || !archiveInfo.Mode().IsRegular() || archiveInfo.Mode().Perm()&0222 != 0 {
		return "", errOperatorCDS
	}
	paths := []string{spec.ResolvedJDK.JavaBin, filepath.Join(spec.ResolvedJDK.Home, "release"), spec.HarnessJar, archive}
	digests := []string{m.JavaSHA256, m.ReleaseSHA256, m.HarnessSHA256, m.ArchiveSHA256}
	paths = append(paths, classpath...)
	digests = append(digests, m.ClasspathSHA256...)
	for i, path := range paths {
		if !matchesCDSFile(path, digests[i]) {
			return "", errOperatorCDS
		}
	}
	return archive, nil
}

func matchesCDSFile(path, digest string) bool {
	want, err := hex.DecodeString(digest)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	initial, err := os.Stat(path)
	if err != nil || !initial.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<30 {
		return false
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, (2<<30)+1))
	if err != nil || n != info.Size() {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), digest)
}
