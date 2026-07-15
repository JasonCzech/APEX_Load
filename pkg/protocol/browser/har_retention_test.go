package browser

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestRuntimeHARRetentionBoundsCompletedArtifacts(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	paths := make([]string, 4)
	for index := range paths {
		paths[index] = filepath.Join(dir, "apexload_browser_"+string(rune('a'+index))+"_"+scrub.SensitivityScrubbed+".har")
		if err := os.WriteFile(paths[index], []byte("12345678"), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := base.Add(time.Duration(index) * time.Minute)
		if err := os.Chtimes(paths[index], stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	// Two eight-byte artifacts fit the count limit, but only one fits the byte
	// limit. The newest artifact is retained.
	if err := enforceRuntimeHARRetentionLimit(dir, 2, 15); err != nil {
		t.Fatal(err)
	}
	for index, path := range paths {
		_, err := os.Stat(path)
		if index == len(paths)-1 {
			if err != nil {
				t.Fatalf("newest artifact was removed: %v", err)
			}
		} else if !os.IsNotExist(err) {
			t.Fatalf("older artifact %q was not pruned: %v", path, err)
		}
	}
}

func TestRuntimeHARRetentionIgnoresUnownedFilesAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	unowned := filepath.Join(dir, "report.har")
	if err := os.WriteFile(unowned, []byte("unowned"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "outside")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "apexload_browser_link_"+scrub.SensitivityScrubbed+".har")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(dir, "apexload_browser_owned_"+scrub.SensitivityScrubbed+".har")
	if err := os.WriteFile(owned, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := enforceRuntimeHARRetentionLimit(dir, 1, 1); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{unowned, target, symlink} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("unowned path %q was touched: %v", path, err)
		}
	}
	if _, err := os.Stat(owned); !os.IsNotExist(err) {
		t.Fatalf("owned artifact above byte budget was not removed: %v", err)
	}
}
