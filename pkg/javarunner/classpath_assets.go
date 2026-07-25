package javarunner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/fileasset"
)

// ClasspathAssetDirName is the per-workDir subdirectory materialized JARs are
// written into. Files are content-addressed (<sha256>.jar) so repeated calls
// across Vuser workers reuse the same file rather than re-copying it.
const ClasspathAssetDirName = ".apexload-classpath"

// classpathAssetPartialPrefix names in-progress temp copies. It is shared by the
// os.CreateTemp pattern and the reclaim sweep so the two can never disagree.
const classpathAssetPartialPrefix = ".partial-"

// classpathAssetPartialGrace is how long a temp file is assumed to belong to a
// live, concurrently-copying worker. The asset directory is shared by every
// VUser on a generator, and materialization is not serialized, so a sweep that
// deleted any .partial-* would unlink a sibling's in-flight copy and break its
// rename/publish. Only temps untouched for longer than this — which a live copy
// never is, since it is bounded by the runner's startup/action timeouts — are
// treated as orphaned by a dead process and reclaimed.
const classpathAssetPartialGrace = time.Hour

const (
	ClasspathAssetIntegrity       = "classpath_asset_integrity"
	ClasspathAssetUnavailable     = "classpath_asset_unavailable"
	ClasspathAssetWorkDirRequired = "classpath_asset_workdir_required"
)

// ClasspathAsset binds one exact classpath entry to an uploaded JAR by content
// hash. StorageRef is a same-host file:// source read by a local runner; empty
// means no local source is available (a distributed runner would supply staged
// bytes instead, which is not yet wired).
type ClasspathAsset struct {
	Entry      string
	SHA256     string
	StorageRef string
}

// ClasspathAssetIssue identifies a materialization failure without echoing the
// classpath value, which may have been produced by secret/template resolution.
type ClasspathAssetIssue struct {
	Index    int
	Category string
}

func (issue ClasspathAssetIssue) Error() string {
	if issue.Index < 0 {
		return fmt.Sprintf("Java classpath asset is invalid (%s)", issue.Category)
	}
	return fmt.Sprintf("Java classpath asset for entry %d is invalid (%s)", issue.Index, issue.Category)
}

// MaterializeClasspathAssets copies each uploaded JAR bound to a classpath entry
// into <workDir>/.apexload-classpath/<sha256>.jar and returns a copy of the
// classpath with those entries rewritten to their materialized absolute paths.
// Entries without a bound asset pass through unchanged (and continue to fail
// closed in ResolveClasspath if absent). It is idempotent and content-addressed,
// and verifies the SHA-256 of the bytes it writes before publishing them, so a
// corrupted or mismatched source never becomes a runnable classpath entry.
//
// Callers are responsible for the operator opt-in gate: this function performs
// no policy checks and materializes whatever assets it is given.
func MaterializeClasspathAssets(classpath []string, assets []ClasspathAsset, workDir string) ([]string, *ClasspathAssetIssue) {
	if len(assets) == 0 {
		return classpath, nil
	}
	// Index assets by the exact entry they satisfy; first binding wins.
	byEntry := make(map[string]ClasspathAsset, len(assets))
	for _, asset := range assets {
		entry := strings.TrimSpace(asset.Entry)
		if entry == "" {
			continue
		}
		if _, exists := byEntry[entry]; !exists {
			byEntry[entry] = asset
		}
	}
	if len(byEntry) == 0 {
		return classpath, nil
	}
	if strings.TrimSpace(workDir) == "" {
		return nil, &ClasspathAssetIssue{Index: -1, Category: ClasspathAssetWorkDirRequired}
	}
	assetDir := filepath.Join(workDir, ClasspathAssetDirName)
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		return nil, &ClasspathAssetIssue{Index: -1, Category: ClasspathAssetWorkDirRequired}
	}
	// Reclaim any .partial-* temp files left by a previous run that was killed
	// mid-copy (the deferred cleanup only fires in-process). Best-effort.
	sweepClasspathAssetPartials(assetDir)
	rewritten := make([]string, len(classpath))
	copy(rewritten, classpath)
	for index, entry := range classpath {
		asset, ok := byEntry[strings.TrimSpace(entry)]
		if !ok {
			continue
		}
		path, category := materializeOneClasspathAsset(assetDir, asset)
		if category != "" {
			return nil, &ClasspathAssetIssue{Index: index, Category: category}
		}
		rewritten[index] = path
	}
	return rewritten, nil
}

// materializeOneClasspathAsset ensures one uploaded JAR exists at a
// content-addressed path under assetDir and returns that path. On failure it
// returns a value-free category code (never the classpath value or source path).
func materializeOneClasspathAsset(assetDir string, asset ClasspathAsset) (string, string) {
	digest := strings.ToLower(strings.TrimSpace(asset.SHA256))
	if !isHexSHA256(digest) {
		return "", ClasspathAssetIntegrity
	}
	finalPath := filepath.Join(assetDir, digest+".jar")

	// Fast path: an earlier worker already published this content-addressed
	// file. The name is only assigned after a hash-verified write, so trust it.
	if info, err := os.Stat(finalPath); err == nil && info.Mode().IsRegular() {
		return finalPath, ""
	}

	ref := strings.TrimSpace(asset.StorageRef)
	if ref == "" {
		return "", ClasspathAssetUnavailable
	}
	source, err := fileasset.OpenStorageRefLimited(ref, fileasset.MaxJarBytes)
	if err != nil {
		return "", ClasspathAssetUnavailable
	}
	defer source.Close()

	tmp, err := os.CreateTemp(assetDir, classpathAssetPartialPrefix+"*")
	if err != nil {
		return "", ClasspathAssetWorkDirRequired
	}
	tmpPath := tmp.Name()
	published := false
	defer func() {
		_ = tmp.Close()
		if !published {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(source, fileasset.MaxJarBytes+1)); err != nil {
		return "", ClasspathAssetUnavailable
	}
	if hex.EncodeToString(hasher.Sum(nil)) != digest {
		return "", ClasspathAssetIntegrity
	}
	if err := tmp.Close(); err != nil {
		return "", ClasspathAssetWorkDirRequired
	}

	// Publish atomically. If another worker won the race, the content-addressed
	// target already exists with identical bytes; reuse it and let the deferred
	// cleanup remove our now-redundant temp file.
	if err := os.Rename(tmpPath, finalPath); err != nil {
		if info, statErr := os.Stat(finalPath); statErr == nil && info.Mode().IsRegular() {
			return finalPath, ""
		}
		return "", ClasspathAssetWorkDirRequired
	}
	published = true
	return finalPath, ""
}

// sweepClasspathAssetPartials reclaims temp files orphaned by a process that
// died mid-materialization. It deliberately skips any temp modified within
// classpathAssetPartialGrace: the asset directory is shared across concurrent
// VUsers, and removing a sibling's in-flight copy would make its os.Rename fail
// with ENOENT and surface a spurious classpath_asset_workdir_required.
// Best-effort: errors are ignored so a sweep failure never blocks a run.
func sweepClasspathAssetPartials(assetDir string) {
	entries, err := os.ReadDir(assetDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-classpathAssetPartialGrace)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), classpathAssetPartialPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			// Unstattable or recently written: assume a live worker owns it.
			continue
		}
		_ = os.Remove(filepath.Join(assetDir, entry.Name()))
	}
}

func isHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
