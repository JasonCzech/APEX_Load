package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/scrub"
)

func writeAgedFile(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"log":{"entries":[]}}`), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
	return path
}

func TestPlanHARArtifactRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	expiredScrubbed := writeAgedFile(t, dir, "apexload_browser_111_scrubbed.har", 10*24*time.Hour)
	expiredOrphan := writeAgedFile(t, dir, "apexload_browser_222.har.tmp", 9*24*time.Hour)
	writeAgedFile(t, dir, "apexload_browser_333_scrubbed.har", 2*24*time.Hour) // fresh, kept
	writeAgedFile(t, dir, "unrelated.har", 30*24*time.Hour)                    // not ours, never touched
	writeAgedFile(t, dir, "apexload_browser_notes.txt", 30*24*time.Hour)       // wrong suffix

	settings := ArchiveRetentionSettings{Enabled: true, HARArtifactDir: dir}
	candidates := planHARArtifactRetention(now, settings)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want the two expired browser artifacts", candidates)
	}
	byPath := map[string]HARArtifactPruneCandidate{}
	for _, candidate := range candidates {
		byPath[candidate.Path] = candidate
	}
	scrubbed, ok := byPath[expiredScrubbed]
	if !ok || scrubbed.Sensitivity != scrub.SensitivityScrubbed {
		t.Errorf("scrubbed artifact candidate = %+v", byPath)
	}
	if scrubbed.RetentionDays != defaultHARRetentionDays {
		t.Errorf("retention days = %d, want default %d", scrubbed.RetentionDays, defaultHARRetentionDays)
	}
	orphan, ok := byPath[expiredOrphan]
	if !ok || orphan.Sensitivity != scrub.SensitivityRaw {
		t.Errorf("orphaned working file must classify as raw: %+v", byPath)
	}
}

func TestPlanHARArtifactRetentionRespectsSettings(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	writeAgedFile(t, dir, "apexload_browser_444_scrubbed.har", 10*24*time.Hour)

	if got := planHARArtifactRetention(now, ArchiveRetentionSettings{Enabled: false, HARArtifactDir: dir}); len(got) != 0 {
		t.Errorf("disabled retention must plan nothing, got %+v", got)
	}
	// A longer custom window keeps the 10-day-old artifact.
	if got := planHARArtifactRetention(now, ArchiveRetentionSettings{Enabled: true, HARArtifactDir: dir, HARRetentionDays: 30}); len(got) != 0 {
		t.Errorf("30-day window should keep the artifact, got %+v", got)
	}
	// A shorter window prunes it.
	if got := planHARArtifactRetention(now, ArchiveRetentionSettings{Enabled: true, HARArtifactDir: dir, HARRetentionDays: 3}); len(got) != 1 {
		t.Errorf("3-day window should prune the artifact, got %+v", got)
	}
}

func TestPlanHARArtifactRetentionImmediatelyPurgesRawWorkingFilesEvenWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	raw := writeAgedFile(t, dir, "apexload_browser_fresh.har.tmp", time.Second)
	writeAgedFile(t, dir, "apexload_browser_fresh_scrubbed.har", time.Second)

	candidates := planHARArtifactRetention(now, ArchiveRetentionSettings{Enabled: false, HARArtifactDir: dir})
	if len(candidates) != 1 || candidates[0].Path != raw || candidates[0].Sensitivity != scrub.SensitivityRaw || candidates[0].RetentionDays != 0 {
		t.Fatalf("disabled retention candidates = %+v, want only immediate raw purge", candidates)
	}
	_, pruned, failed := applyHARArtifactRetention(candidates)
	if pruned != 1 || failed != 0 {
		t.Fatalf("raw purge = pruned %d failed %d", pruned, failed)
	}
	if _, err := os.Stat(raw); !os.IsNotExist(err) {
		t.Fatalf("raw working file survived purge: %v", err)
	}
}

func TestApplyHARArtifactRetention(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	expired := writeAgedFile(t, dir, "apexload_browser_555_scrubbed.har", 10*24*time.Hour)
	kept := writeAgedFile(t, dir, "apexload_browser_666_scrubbed.har", 1*24*time.Hour)

	candidates := planHARArtifactRetention(now, ArchiveRetentionSettings{Enabled: true, HARArtifactDir: dir})
	results, pruned, failed := applyHARArtifactRetention(candidates)
	if pruned != 1 || failed != 0 || len(results) != 1 {
		t.Fatalf("apply = pruned %d failed %d results %+v", pruned, failed, results)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Error("expired artifact still on disk")
	}
	if _, err := os.Stat(kept); err != nil {
		t.Error("fresh artifact must survive the sweep")
	}
}

func TestApplyHARArtifactRetentionRejectsReplacedCandidate(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	path := writeAgedFile(t, dir, "apexload_browser_replaced_scrubbed.har", 10*24*time.Hour)
	candidates := planHARArtifactRetention(now, ArchiveRetentionSettings{Enabled: true, HARArtifactDir: dir})
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want one", candidates)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	results, pruned, failed := applyHARArtifactRetention(candidates)
	if pruned != 0 || failed != 1 || len(results) != 1 || results[0].Status != "failed" {
		t.Fatalf("apply replaced candidate = pruned %d failed %d results %+v", pruned, failed, results)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("replacement was removed: %v", err)
	}
	if string(data) != "replacement" {
		t.Fatalf("replacement content = %q", data)
	}
}

func TestPlanHARArtifactRetentionBoundsCandidateMemory(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	for index := 0; index < 5; index++ {
		writeAgedFile(t, dir, fmt.Sprintf("apexload_browser_%d_scrubbed.har", index), 10*24*time.Hour)
	}
	candidates := planHARArtifactRetentionLimit(now, ArchiveRetentionSettings{Enabled: true, HARArtifactDir: dir}, 3)
	if len(candidates) != 3 {
		t.Fatalf("candidate count = %d, want hard cap 3", len(candidates))
	}
}

func TestPlanHARArtifactRetentionIgnoresSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := writeAgedFile(t, dir, "unrelated-target", 10*24*time.Hour)
	link := filepath.Join(dir, "apexload_browser_link_scrubbed.har")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if candidates := planHARArtifactRetention(time.Now().UTC(), ArchiveRetentionSettings{Enabled: true, HARArtifactDir: dir}); len(candidates) != 0 {
		t.Fatalf("symlink was admitted as HAR retention candidate: %+v", candidates)
	}
}

func TestIsHARArtifactName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "apexload_browser_123_scrubbed.har", want: true},
		{name: "apexload_browser_123.har.tmp", want: true},
		{name: "recording.har", want: false},
		{name: "apexload_browser_notes.txt", want: false},
		{name: "browser_123.har", want: false},
	}
	for _, tt := range tests {
		if got := isHARArtifactName(tt.name); got != tt.want {
			t.Errorf("isHARArtifactName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
