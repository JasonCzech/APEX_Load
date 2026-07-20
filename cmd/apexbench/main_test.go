package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteOutputRequiresExplicitNonEmptyPathAndDoesNotOverwriteByDefault(t *testing.T) {
	if err := writeOutput("", []byte("data"), false); err == nil {
		t.Fatal("empty output path succeeded")
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := writeOutput(path, []byte("first"), false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeOutput(path, []byte("second"), false); err == nil {
		t.Fatal("unexpected overwrite without explicit overwrite flag")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "first" {
		t.Fatalf("content = %q, want first", content)
	}
	if err := writeOutput(path, []byte("second"), true); err != nil {
		t.Fatalf("explicit overwrite: %v", err)
	}
}

func TestApplyOverridesIgnoreNonPositiveValues(t *testing.T) {
	integer := 10
	applyPositiveIntOverride(&integer, 0)
	if integer != 10 {
		t.Fatalf("integer = %d, want 10", integer)
	}
}
