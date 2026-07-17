package api

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// defaultHARRetentionDays is intentionally short: HAR-with-bodies is the most
// sensitive artifact class even after scrub-on-write.
const defaultHARRetentionDays = 7

// harArtifactPattern matches the runtime browser HAR artifacts written by the
// browser protocol's har_capture policies, including orphaned working files
// from crashed sessions.
const (
	harArtifactPrefix                 = "apexload_browser_"
	harArtifactSuffix                 = ".har"
	harArtifactTempSuffix             = ".har.tmp"
	maxHARArtifactRetentionCandidates = 10_000
	harArtifactRetentionReadBatch     = 256
)

type HARArtifactPruneCandidate struct {
	Path          string    `json:"path"`
	Sensitivity   string    `json:"sensitivity"`
	SizeBytes     int64     `json:"size_bytes"`
	RetentionDays int       `json:"retention_days"`
	ModifiedAt    time.Time `json:"modified_at"`
	ExpiredAt     time.Time `json:"expired_at"`
	identity      os.FileInfo
}

type HARArtifactRetentionResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// planHARArtifactRetention finds runtime browser HAR artifacts (and orphaned
// working files) older than the HAR retention window. Only files carrying the
// browser-protocol artifact naming are considered — the sweep never touches
// other files in the directory.
func planHARArtifactRetention(now time.Time, settings ArchiveRetentionSettings) []HARArtifactPruneCandidate {
	return planHARArtifactRetentionLimit(now, settings, maxHARArtifactRetentionCandidates)
}

func planHARArtifactRetentionLimit(now time.Time, settings ArchiveRetentionSettings, limit int) []HARArtifactPruneCandidate {
	if limit <= 0 {
		return nil
	}
	retentionDays := settings.HARRetentionDays
	if retentionDays <= 0 {
		retentionDays = defaultHARRetentionDays
	}
	retention := time.Duration(0)
	if settings.Enabled {
		var ok bool
		retention, ok = checkedDuration(retentionDays, 24*time.Hour)
		if !ok || retention <= 0 {
			return nil
		}
	}
	dir := strings.TrimSpace(settings.HARArtifactDir)
	if dir == "" {
		dir = os.TempDir()
	}
	root, err := openArchiveDirectoryNoSymlink(dir)
	if err != nil {
		return nil
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil
	}
	defer directory.Close()

	candidates := make([]HARArtifactPruneCandidate, 0)
	for len(candidates) < limit {
		entries, readErr := directory.ReadDir(harArtifactRetentionReadBatch)
		for _, entry := range entries {
			if len(candidates) >= limit {
				break
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isHARArtifactName(entry.Name()) {
				continue
			}
			info, err := root.Lstat(entry.Name())
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			rawWorkingFile := strings.HasSuffix(entry.Name(), harArtifactTempSuffix)
			if rawWorkingFile && browserprotocol.RuntimeHARWorkingPathActive(path) {
				continue
			}
			if !rawWorkingFile && !settings.Enabled {
				continue
			}
			expiredAt := info.ModTime()
			candidateRetentionDays := 0
			if !rawWorkingFile {
				expiredAt = info.ModTime().Add(retention)
				candidateRetentionDays = retentionDays
				if now.Before(expiredAt) {
					continue
				}
			}
			candidates = append(candidates, HARArtifactPruneCandidate{
				Path:          path,
				Sensitivity:   harArtifactSensitivity(entry.Name()),
				SizeBytes:     info.Size(),
				RetentionDays: candidateRetentionDays,
				ModifiedAt:    info.ModTime().UTC(),
				ExpiredAt:     expiredAt.UTC(),
				identity:      info,
			})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			break
		}
	}
	return candidates
}

func isHARArtifactName(name string) bool {
	return strings.HasPrefix(name, harArtifactPrefix) &&
		(strings.HasSuffix(name, harArtifactSuffix) || strings.HasSuffix(name, harArtifactTempSuffix))
}

// harArtifactSensitivity classifies by the artifact naming contract: persisted
// artifacts carry the scrubbed label in their filename; working files never
// passed scrub-on-write.
func harArtifactSensitivity(name string) string {
	if strings.Contains(name, "_"+scrub.SensitivityScrubbed+harArtifactSuffix) {
		return scrub.SensitivityScrubbed
	}
	return scrub.SensitivityRaw
}

func applyHARArtifactRetention(candidates []HARArtifactPruneCandidate) ([]HARArtifactRetentionResult, int, int) {
	results := make([]HARArtifactRetentionResult, 0, len(candidates))
	pruned, failed := 0, 0
	for _, candidate := range candidates {
		result := HARArtifactRetentionResult{Path: candidate.Path, Status: "pruned"}
		if err := removeHARArtifactCandidate(candidate); err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			failed++
		} else {
			pruned++
		}
		results = append(results, result)
	}
	return results, pruned, failed
}

func removeHARArtifactCandidate(candidate HARArtifactPruneCandidate) error {
	path := filepath.Clean(strings.TrimSpace(candidate.Path))
	name := filepath.Base(path)
	if path == "." || !isHARArtifactName(name) || candidate.identity == nil {
		return fmt.Errorf("HAR retention candidate is incomplete")
	}
	if strings.HasSuffix(name, harArtifactTempSuffix) && browserprotocol.RuntimeHARWorkingPathActive(path) {
		return fmt.Errorf("HAR working file %q is active", name)
	}
	root, err := openArchiveDirectoryNoSymlink(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	current, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() ||
		!os.SameFile(candidate.identity, current) || current.Size() != candidate.SizeBytes ||
		!current.ModTime().UTC().Equal(candidate.ModifiedAt) {
		return fmt.Errorf("HAR artifact %q changed after retention planning", name)
	}
	tombstone, err := archiveTemporaryName(".", name+".retention")
	if err != nil {
		return err
	}
	if err := root.Rename(name, tombstone); err != nil {
		return err
	}
	tombstoneInfo, err := root.Lstat(tombstone)
	if err != nil || tombstoneInfo.Mode()&os.ModeSymlink != 0 || !tombstoneInfo.Mode().IsRegular() || !os.SameFile(candidate.identity, tombstoneInfo) {
		_ = root.Rename(tombstone, name)
		if err != nil {
			return err
		}
		return fmt.Errorf("HAR artifact %q changed during retention deletion", name)
	}
	if err := root.Remove(tombstone); err != nil {
		_ = root.Rename(tombstone, name)
		return err
	}
	return syncOpenedArchiveDirectory(root)
}
