package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAnalysisScratchReclaimsOnlyExpiredJobDirectories(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	for _, name := range []string{"job-old", "job-current", "other-old"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if name != "job-current" {
			if err := os.Chtimes(path, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := reclaimAnalysisScratch(root, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"job-current", "other-old"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal("removed unrelated or live directory", err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "job-old")); !os.IsNotExist(err) {
		t.Fatal("expired staging retained")
	}
}
