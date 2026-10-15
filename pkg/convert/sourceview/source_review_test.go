package sourceview

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBuildPreservesTruncatesAndNormalizesMappings(t *testing.T) {
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
	if sample.Redacted {
		t.Fatalf("sample redacted = true, want exact imported source")
	}
	if sample.Content != `{"Authorization":"Bearer raw-secret-token","password":"raw-secret-password"}` {
		t.Fatalf("source content changed: %s", sample.Content)
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

func TestBuildPreservesBeforeTruncatingReviewContent(t *testing.T) {
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
	if file.Redacted {
		t.Fatalf("file redacted = true, want exact imported source")
	}
	if file.OriginalBytes != len([]byte(content)) {
		t.Fatalf("original bytes = %d, want %d", file.OriginalBytes, len([]byte(content)))
	}
	if !strings.Contains(file.Content, "BOUND") {
		t.Fatalf("review content lost imported source before truncation: %s", file.Content)
	}
}

func TestRedactAssociatesStructuredSensitiveNamesWithShortValues(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		secret string
	}{
		{
			name:   "Postman custom header",
			input:  `{"key":"X-Auth-Token","value":"tiny"}`,
			secret: "tiny",
		},
		{
			name:   "reverse JSON pair",
			input:  `{"value":"short","name":"client_secret"}`,
			secret: "short",
		},
		{
			name:   "TruClient XML argument",
			input:  `<Argument name="Password" value="mini" selector="[data-password='mini']"/>`,
			secret: "mini",
		},
		{
			name:   "sensitive selector attribute",
			input:  `input[data-token="brief"]`,
			secret: "brief",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := Redact(test.input)
			if !changed || strings.Contains(got, test.secret) || !strings.Contains(got, "[REDACTED]") {
				t.Fatalf("Redact(%q) = %q, changed=%t", test.input, got, changed)
			}
		})
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
	if review.TotalSourceFiles != MaxFiles+3 {
		t.Fatalf("total source files = %d, want %d", review.TotalSourceFiles, MaxFiles+3)
	}
	if len(review.OmittedSourceFiles) != 3 {
		t.Fatalf("omitted source files = %#v, want three explicit paths", review.OmittedSourceFiles)
	}
	if review.OmittedSourceFiles[0] != fmt.Sprintf("file-%02d.json", MaxFiles) {
		t.Fatalf("first omitted source file = %q, want deterministic sorted boundary", review.OmittedSourceFiles[0])
	}
}

func TestBuildPrioritizesMappedSourceWithinUnchangedDisplayBudget(t *testing.T) {
	files := map[string]string{"z/user_modified_perfs.json": `{"security.csp.enable":{"value":true}}`}
	for index := 0; index < MaxFiles+3; index++ {
		files[fmt.Sprintf("aux-%02d.json", index)] = "{}"
	}
	for repeat := 0; repeat < 20; repeat++ {
		review := Build(files, []Mapping{{SourceFile: "z/user_modified_perfs.json", StartLine: 1, TargetPath: "metadata.import.preserved.browser_preferences", Status: "review_only"}})
		if len(review.SourceFiles) != MaxFiles || review.TotalSourceFiles != MaxFiles+4 || len(review.OmittedSourceFiles) != 4 {
			t.Fatal("prioritization changed the bounded display accounting")
		}
		if review.SourceFiles[0].Path != "z/user_modified_perfs.json" {
			t.Fatal("mapped source was omitted behind auxiliary files")
		}
		if review.OmittedSourceFiles[0] != "aux-19.json" {
			t.Fatal("bounded source selection is not deterministic")
		}
	}
}

func TestBuildExactMappingPathsTakePriorityOverBasenameAliases(t *testing.T) {
	files := map[string]string{"F01.c": "unused alias"}
	mappings := make([]Mapping, 0, MaxFiles)
	for index := 1; index <= MaxFiles; index++ {
		name := fmt.Sprintf("nested/F%02d.c", index)
		files[name] = "mapped source"
		mappings = append(mappings, Mapping{SourceFile: name, TargetPath: fmt.Sprintf("actions[%d]", index-1)})
	}
	review := Build(files, mappings)
	if len(review.SourceFiles) != MaxFiles || len(review.OmittedSourceFiles) != 1 || review.OmittedSourceFiles[0] != "F01.c" {
		t.Fatal("basename alias displaced an exactly mapped source file")
	}
	for _, file := range review.SourceFiles {
		if !strings.HasPrefix(file.Path, "nested/") {
			t.Fatal("unreferenced basename alias received exact mapping priority")
		}
	}
}

func TestBuildKeepsFullTextOnlyForTruncatedMappedFiles(t *testing.T) {
	long := strings.Repeat("web_url(\"step\", LAST);\n", MaxFileChars/5)
	review := Build(map[string]string{
		"Action.c":     long,
		"pre_cci.c":    long,
		"vuser_init.c": "lr_start_transaction(\"t\");\n",
	}, []Mapping{
		{SourceFile: "Action.c", StartLine: 5000, Function: "web_url", Status: "converted"},
		{SourceFile: "vuser_init.c", StartLine: 1, Function: "lr_start_transaction", Status: "converted"},
	})
	files := map[string]SourceFile{}
	for _, file := range review.SourceFiles {
		files[file.Path] = file
	}
	if action := files["Action.c"]; !action.Truncated || action.FullText != long {
		t.Fatalf("Action.c truncated=%v full text kept=%v, want the whole mapped file kept", action.Truncated, action.FullText == long)
	}
	if extra := files["pre_cci.c"]; !extra.Truncated || extra.FullText != "" {
		t.Fatalf("pre_cci.c full text kept=%v, want only mapped files kept", extra.FullText != "")
	}
	if small := files["vuser_init.c"]; small.Truncated || small.FullText != "" {
		t.Fatalf("vuser_init.c truncated=%v full text kept=%v, want a whole preview and no copy", small.Truncated, small.FullText != "")
	}
	encoded, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	// Two previews serialize; the full text, twice their size, must not.
	if len(encoded) >= len(long) {
		t.Fatalf("serialized review is %d bytes, at least the %d-byte full text", len(encoded), len(long))
	}
	if strings.Contains(string(encoded), "FullText") || strings.Contains(string(encoded), "full_text") {
		t.Fatal("serialized review names the in-memory full text")
	}
}
