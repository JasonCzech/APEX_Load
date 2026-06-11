package api

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/scrub"
)

// defaultHARRetentionDays is intentionally short: HAR-with-bodies is the most
// sensitive artifact class even after scrub-on-write.
const defaultHARRetentionDays = 7

// harArtifactPattern matches the runtime browser HAR artifacts written by the
// browser protocol's har_capture policies, including orphaned working files
// from crashed sessions.
const (
	harArtifactPrefix     = "apexload_browser_"
	harArtifactSuffix     = ".har"
	harArtifactTempSuffix = ".har.tmp"
)

type HARArtifactPruneCandidate struct {
	Path          string    `json:"path"`
	Sensitivity   string    `json:"sensitivity"`
	SizeBytes     int64     `json:"size_bytes"`
	RetentionDays int       `json:"retention_days"`
	ModifiedAt    time.Time `json:"modified_at"`
	ExpiredAt     time.Time `json:"expired_at"`
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
	if !settings.Enabled {
		return nil
	}
	retentionDays := settings.HARRetentionDays
	if retentionDays <= 0 {
		retentionDays = defaultHARRetentionDays
	}
	dir := strings.TrimSpace(settings.HARArtifactDir)
	if dir == "" {
		dir = os.TempDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	candidates := make([]HARArtifactPruneCandidate, 0)
	for _, entry := range entries {
		if entry.IsDir() || !isHARArtifactName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		expiredAt := info.ModTime().Add(time.Duration(retentionDays) * 24 * time.Hour)
		if now.Before(expiredAt) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		candidates = append(candidates, HARArtifactPruneCandidate{
			Path:          path,
			Sensitivity:   harArtifactSensitivity(entry.Name()),
			SizeBytes:     info.Size(),
			RetentionDays: retentionDays,
			ModifiedAt:    info.ModTime().UTC(),
			ExpiredAt:     expiredAt.UTC(),
		})
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
		if err := os.Remove(candidate.Path); err != nil {
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
