package sourceview

import (
	"fmt"
	"strings"
	"testing"
)

func TestBuildRedactsTruncatesAndNormalizesMappings(t *testing.T) {
	longContent := strings.Repeat("a", MaxFileChars+10)
	review := Build(map[string]string{
		"sample.json": `{"Authorization":"Bearer raw-secret-token","password":"raw-secret-password"}`,
		"huge.txt":    longContent,
	}, []Mapping{
		{SourceFile: "sample.json", StartLine: 2, Function: "request", TargetPath: "actions[0]", Status: "converted"},
		{SourceFile: "sample.json", StartLine: 2, Function: "request", TargetPath: "actions[0]", Status: "converted"},
		{SourceFile: "huge.txt", StartLine: 1, Function: "blob", Status: "review_only"},
	})
	if review == nil {
		t.Fatal("Build returned nil")
	}
	if len(review.Mappings) != 3 {
		t.Fatalf("mappings = %d, want 3", len(review.Mappings))
	}
	if review.Mappings[0].ID == "" || review.Mappings[0].ID == review.Mappings[1].ID {
		t.Fatalf("mapping IDs were not stable and unique: %#v", review.Mappings)
	}
	files := map[string]SourceFile{}
	for _, file := range review.SourceFiles {
		files[file.Path] = file
	}
	sample := files["sample.json"]
	if !sample.Redacted {
		t.Fatalf("sample redacted = false, want true")
	}
	if strings.Contains(sample.Content, "raw-secret") {
		t.Fatalf("redacted content leaked secret: %s", sample.Content)
	}
	huge := files["huge.txt"]
	if !huge.Truncated {
		t.Fatalf("huge truncated = false, want true")
	}
	if huge.OriginalBytes != len([]byte(longContent)) {
		t.Fatalf("original bytes = %d, want %d", huge.OriginalBytes, len([]byte(longContent)))
	}
}

func TestLanguageAndLineForOffset(t *testing.T) {
	cases := map[string]string{
		"plan.jmx":        "xml",
		"collection.json": "json",
		"openapi.yaml":    "yaml",
		"Action.c":        "c",
		"script.js":       "javascript",
		"sample.http":     "http",
	}
	for name, want := range cases {
		if got := Language(name); got != want {
			t.Fatalf("Language(%q) = %q, want %q", name, got, want)
		}
	}
	content := "line1\nline2\nline3"
	if got := LineForOffset(content, strings.Index(content, "line3")); got != 3 {
		t.Fatalf("LineForOffset(line3) = %d, want 3", got)
	}
	if got := LineForOffset(content, len(content)+100); got != 3 {
		t.Fatalf("LineForOffset(past end) = %d, want 3", got)
	}
}

func TestBuildLimitsSourceFiles(t *testing.T) {
	files := map[string]string{}
	for index := 0; index < MaxFiles+3; index++ {
		files[fmt.Sprintf("file-%02d.json", index)] = "{}"
	}
	review := Build(files, nil)
	if review == nil {
		t.Fatal("Build returned nil")
	}
	if len(review.SourceFiles) != MaxFiles {
		t.Fatalf("source files = %d, want %d", len(review.SourceFiles), MaxFiles)
	}
}
