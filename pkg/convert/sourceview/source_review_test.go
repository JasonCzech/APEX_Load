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
	index := NewLineIndex(content)
	if got := index.LineForOffset(strings.Index(content, "line2")); got != 2 {
		t.Fatalf("LineIndex.LineForOffset(line2) = %d, want 2", got)
	}
	if got := index.MaxLine(1); got != 3 {
		t.Fatalf("LineIndex.MaxLine = %d, want 3", got)
	}
	if got := NewLineIndex("").MaxLine(7); got != 7 {
		t.Fatalf("empty LineIndex.MaxLine = %d, want fallback 7", got)
	}
}

func TestBuildRedactsBeforeTruncatingReviewContent(t *testing.T) {
	key := `{"password":"`
	value := strings.Repeat("BOUNDARYLEAK", 8)
	prefix := strings.Repeat("a", MaxFileChars-len(key)-5)
	content := prefix + key + value + `"}` + strings.Repeat("z", 200)
	review := Build(map[string]string{"huge.json": content}, nil)
	if review == nil || len(review.SourceFiles) != 1 {
		t.Fatalf("review source files = %#v, want one file", review)
	}
	file := review.SourceFiles[0]
	if !file.Truncated {
		t.Fatalf("file truncated = false, want true")
	}
	if !file.Redacted {
		t.Fatalf("file redacted = false, want true for displayed secret")
	}
	if file.OriginalBytes != len([]byte(content)) {
		t.Fatalf("original bytes = %d, want %d", file.OriginalBytes, len([]byte(content)))
	}
	if strings.Contains(file.Content, value) || strings.Contains(file.Content, "BOUND") {
		t.Fatalf("review content leaked secret: %s", file.Content)
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
