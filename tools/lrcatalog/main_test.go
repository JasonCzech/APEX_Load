package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoCatalogDir = "../../pkg/convert/loadrunner/testdata/loadrunner_functions"

// TestCatalogUpToDateWithFixtures fails when a fixture lists a function that is not yet in
// its catalog CSV. Refreshing a fixture from a newer VuGen version therefore surfaces the
// new functions here instead of letting them silently fall through to custom_call at import.
func TestCatalogUpToDateWithFixtures(t *testing.T) {
	changed, err := Generate("fixtures", repoCatalogDir, true)
	if err != nil {
		t.Fatalf("generate (check): %v", err)
	}
	if len(changed) > 0 {
		t.Fatalf("catalog out of date with fixtures; run `go run ./tools/lrcatalog`:\n  %s", strings.Join(changed, "\n  "))
	}
}

// TestGenerateIsIdempotentAndPreservesCuration verifies that running the generator in write
// mode against a temporary copy of the catalog adds missing functions, preserves curated
// rows verbatim, and is a no-op on a second run.
func TestGenerateIsIdempotentAndPreservesCuration(t *testing.T) {
	tmp := t.TempDir()

	// Seed a catalog with one curated row whose reason must survive regeneration.
	curated := "# header\n# Format: name,family,support_mode[,reason]\n" +
		"web_set_user,web,review_only,CURATED REASON KEEP ME\n"
	if err := os.WriteFile(filepath.Join(tmp, "web.csv"), []byte(curated), 0o644); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	// First run should append the fixture functions that are missing.
	changed, err := Generate("fixtures", tmp, false)
	if err != nil {
		t.Fatalf("generate (write): %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("expected the generator to append missing web functions")
	}

	got, err := os.ReadFile(filepath.Join(tmp, "web.csv"))
	if err != nil {
		t.Fatalf("read regenerated catalog: %v", err)
	}
	content := string(got)
	if !strings.Contains(content, "web_set_user,web,review_only,CURATED REASON KEEP ME") {
		t.Fatalf("curated row was not preserved verbatim:\n%s", content)
	}
	if !strings.Contains(content, "web_add_cookie,web,review_only,") {
		t.Fatalf("expected a newly appended web_add_cookie row:\n%s", content)
	}

	// Second run must be a no-op now that every fixture function is present.
	changed, err = Generate("fixtures", tmp, false)
	if err != nil {
		t.Fatalf("generate (second write): %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("generator is not idempotent; second run changed: %v", changed)
	}
}
