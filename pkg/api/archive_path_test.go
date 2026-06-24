package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveArtifactPathRejectsUnsafeNames(t *testing.T) {
	root := t.TempDir()
	okPath, err := archiveArtifactPath(root, "nested/transactions.jsonl")
	if err != nil {
		t.Fatalf("archiveArtifactPath safe path error: %v", err)
	}
	if rel, err := filepath.Rel(root, okPath); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("safe path %q escaped root %q rel=%q err=%v", okPath, root, rel, err)
	}

	tests := []string{
		"",
		"/tmp/transactions.jsonl",
		"../transactions.jsonl",
		"nested/../transactions.jsonl",
		`nested\transactions.jsonl`,
		"C:/archives/transactions.jsonl",
		"transactions\x00.jsonl",
	}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			if got, err := archiveArtifactPath(root, name); err == nil {
				t.Fatalf("archiveArtifactPath(%q) = %q, want error", name, got)
			}
		})
	}
}

func TestLoadRunArchiveManifestRejectsUnsafeArtifactNames(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RunArchiveManifest)
	}{
		{
			name: "required traversal",
			mutate: func(manifest *RunArchiveManifest) {
				manifest.TransactionsFile = "../escape.jsonl"
			},
		},
		{
			name: "required absolute path",
			mutate: func(manifest *RunArchiveManifest) {
				manifest.ResourceSamplesFile = "/tmp/resources.jsonl"
			},
		},
		{
			name: "required windows volume",
			mutate: func(manifest *RunArchiveManifest) {
				manifest.IntervalSummariesFile = "C:/archives/intervals.jsonl"
			},
		},
		{
			name: "optional backslash",
			mutate: func(manifest *RunArchiveManifest) {
				manifest.TransactionsParquetFile = `parquet\transactions.parquet`
			},
		},
		{
			name: "optional nul byte",
			mutate: func(manifest *RunArchiveManifest) {
				manifest.TestManifestFile = "test\x00manifest.json"
			},
		},
		{
			name: "notes traversal",
			mutate: func(manifest *RunArchiveManifest) {
				manifest.TestNotesFile = "../test_notes.json"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archiveDir := t.TempDir()
			manifest := RunArchiveManifest{
				SchemaVersion:         archiveSchemaVersion,
				TestID:                "run-path-safety",
				TransactionsFile:      "transactions.jsonl",
				ResourceSamplesFile:   "resources.jsonl",
				IntervalSummariesFile: "interval_summaries.jsonl",
			}
			tt.mutate(&manifest)
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			manifestPath := filepath.Join(archiveDir, "manifest.json")
			if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
			if _, err := loadRunArchiveManifest(manifestPath); err == nil {
				t.Fatal("loadRunArchiveManifest returned nil error for unsafe artifact path")
			}
		})
	}
}
