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
		{
			name:   "Salesforce HAR postData params pw",
			input:  "{\n  \"name\": \"pw\",\n  \"value\": \"SandboxPass!99\"\n}",
			secret: "SandboxPass!99",
		},
		{
			name:   "Salesforce HAR form text pw and frontdoor sid",
			input:  `"text": "un=perf%40example.test&pw=SandboxPass%2199", "url": "https://org.my.salesforce.com/secur/frontdoor.jsp?sid=00Daa0000012345!AQEAQFakeSessionTokenValue1234567890&cshc=x"`,
			secret: "SandboxPass%2199",
		},
		{
			name:   "Salesforce JSON-escaped hidden sid input",
			input:  `"text": "<form><input type=\"hidden\" name=\"sid\" id=\"sid\" value=\"00Daa0000012345!AQEAQHiddenFormSession1234567890\" /></form>"`,
			secret: "00Daa0000012345!AQEAQHiddenFormSession1234567890",
		},
		{
			name:   "Salesforce percent-encoded frontdoor sid in a retURL",
			input:  `"value": "https://org.my.salesforce.com/?retURL=%2Fsecur%2Ffrontdoor.jsp%3Fsid%3D00Daa0000012345%21AQEAQEncodedSessionToken1234567890%26cshc%3Dx"`,
			secret: "AQEAQEncodedSessionToken1234567890",
		},
		{
			name:   "Salesforce double-encoded session id",
			input:  `next=%252Fsecur%252Ffrontdoor.jsp%253Fx%253D00Daa0000012345%2521AQEAQDoubleEncodedToken1234567890`,
			secret: "AQEAQDoubleEncodedToken1234567890",
		},
		{
			name:   "Salesforce Aura token cookie in HAR cookies[]",
			input:  "{\n  \"name\": \"__Host-ERIC_PROD-2876452450001777317\",\n  \"value\": \"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig\"\n}",
			secret: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig",
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
	// Minified JS identifiers such as "const pw=a" must not be false-positived.
	jsBundle := `function f(a){const pw=a+1;var Pw=2;return pw+Pw;}`
	if got, changed := Redact(jsBundle); changed || got != jsBundle {
		t.Fatalf("minified JS unexpectedly redacted: %q", got)
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

// Inside a HAR every JSON body is itself a JSON string, so a token response's
// keys are escaped. The key-based redactor still finds them.
func TestRedactFindsTokensInAJSONBodyHeldInAJSONString(t *testing.T) {
	content := `{"response":{"content":{"text":"{\"access_token\":\"opaqueAccess123\",\"refresh_token\":\"5Aep861SyntheticRefresh\",\"id_token\":\"eyJhbGciOi.synthetic.sig\",\"instance_url\":\"https:\/\/acme.my.salesforce.com\"}"}}}`
	redacted, changed := Redact(content)
	if !changed {
		t.Fatal("Redact reported no change")
	}
	for _, secret := range []string{"opaqueAccess123", "5Aep861SyntheticRefresh", "eyJhbGciOi.synthetic.sig"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted text still holds %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, `\"refresh_token\":\"[REDACTED]\"`) || !strings.Contains(redacted, `\"instance_url\":\"https:\/\/acme.my.salesforce.com\"`) {
		t.Fatalf("redaction did not keep the escaped JSON shape: %s", redacted)
	}
	// An escaped value that itself holds an escape is redacted whole.
	slashed, _ := Redact(`"{\"token\":\"abc\\\/def\"}"`)
	if strings.Contains(slashed, "abc") || strings.Contains(slashed, "def") {
		t.Fatalf("escaped value with an inner escape was not redacted whole: %s", slashed)
	}
	// So is one holding an escaped quote (\\\") or ending in an escaped
	// backslash (\\\\), and the value after it keeps its shape.
	quoted, _ := Redact(`"{\"token\":\"abc\\\"SECRETREST\",\"pw\":\"x\\\\\",\"user\":\"kept\"}"`)
	if strings.Contains(quoted, "abc") || strings.Contains(quoted, "SECRETREST") || !strings.Contains(quoted, `\"pw\":\"[REDACTED]\"`) || !strings.Contains(quoted, `\"user\":\"kept\"`) {
		t.Fatalf("escaped value with an inner quote or backslash was not redacted whole: %s", quoted)
	}
}

func TestBuildExcludesUnreferencedVuGenReplayAndIDEArtifacts(t *testing.T) {
	realFiles := []string{
		"default.cfg",
		"default.usp",
		"globals.h",
		"vuser_init.c",
		"Action.c",
		"vuser_end.c",
		"replay.har",
	}
	noiseFiles := []string{
		"result1/Iteration1/t1.json",
		"result1/Iteration1/t1_RequestHeader.txt",
		`result1\Iteration1\t2_ResponseHeader.txt`,
		"result2/Results.xml",
		"nested/result10/Results.xml",
		"Bookmarks.xml",
		"CompilerLogMetadata.xml",
		"ReplaySummaryReport.xml",
		"ScriptUploadMetadata.xml",
		"UserTasks.xml",
		"ThumbnailsCache.tmp",
		"mdrv.log",
		"mdrv_cmd.txt",
		"options.txt",
		"output.bak",
		"output.txt",
		"logfile.log",
		"debug.inf",
		"combined_script.c",
		"Action_old.c",
	}

	files := make(map[string]string, len(realFiles)+len(noiseFiles)+MaxFiles)
	for _, name := range realFiles {
		files[name] = "// real script content\n"
	}
	for _, name := range noiseFiles {
		files[name] = "// unreferenced IDE or replay noise\n"
	}
	for index := 1; index <= MaxFiles; index++ {
		files[fmt.Sprintf("result1/Iteration1/step_%02d.json", index)] = "{}"
	}

	mappings := []Mapping{
		{SourceFile: "Action.c", StartLine: 1, Function: "web_url", TargetPath: "actions[0]", Status: "converted"},
	}
	for _, mappingsCase := range [][]Mapping{mappings, nil} {
		review := Build(files, mappingsCase)
		if review == nil {
			t.Fatal("Build returned nil")
		}
		if review.TotalSourceFiles != len(realFiles) {
			t.Fatalf("TotalSourceFiles = %d, want %d (mappings=%v)", review.TotalSourceFiles, len(realFiles), len(mappingsCase))
		}
		if len(review.OmittedSourceFiles) != 0 {
			t.Fatalf("OmittedSourceFiles = %#v, want empty (mappings=%v)", review.OmittedSourceFiles, len(mappingsCase))
		}
		if len(review.SourceFiles) != len(realFiles) {
			t.Fatalf("len(SourceFiles) = %d, want %d (mappings=%v)", len(review.SourceFiles), len(realFiles), len(mappingsCase))
		}
		got := make(map[string]bool, len(review.SourceFiles))
		for _, sf := range review.SourceFiles {
			got[sf.Path] = true
		}
		for _, want := range realFiles {
			if !got[want] {
				t.Fatalf("expected real script file %q in SourceFiles, got %#v", want, review.SourceFiles)
			}
		}
		for _, excluded := range noiseFiles {
			if got[excluded] {
				t.Fatalf("unexpected noise file %q in SourceFiles", excluded)
			}
		}
	}
}

func TestShouldIncludeFileAndBuildTableDrivenArtifacts(t *testing.T) {
	tests := []struct {
		name           string
		file           string
		mappedFile     string
		wantUnmapped   bool
		wantWhenMapped bool
	}{
		{
			name:           "replay output JSON in result1",
			file:           "result1/Iteration1/t1.json",
			mappedFile:     "result1/Iteration1/t1.json",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "replay summary XML in result2",
			file:           "result2/Results.xml",
			mappedFile:     "result2/Results.xml",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "nested replay directory with backslashes",
			file:           `nested\result1\Iteration1\t1_RequestHeader.txt`,
			mappedFile:     `nested\result1\Iteration1\t1_RequestHeader.txt`,
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "Bookmarks.xml metadata",
			file:           "Bookmarks.xml",
			mappedFile:     "Bookmarks.xml",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "CompilerLogMetadata.xml metadata",
			file:           "CompilerLogMetadata.xml",
			mappedFile:     "CompilerLogMetadata.xml",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "ReplaySummaryReport.xml metadata",
			file:           "ReplaySummaryReport.xml",
			mappedFile:     "ReplaySummaryReport.xml",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "ScriptUploadMetadata.xml metadata",
			file:           "ScriptUploadMetadata.xml",
			mappedFile:     "ScriptUploadMetadata.xml",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "UserTasks.xml metadata",
			file:           "UserTasks.xml",
			mappedFile:     "UserTasks.xml",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "ThumbnailsCache.tmp metadata",
			file:           "ThumbnailsCache.tmp",
			mappedFile:     "ThumbnailsCache.tmp",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "combined wrapper C file",
			file:           "combined_script.c",
			mappedFile:     "combined_script.c",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "backup _old.c file",
			file:           "Action_old.c",
			mappedFile:     "Action_old.c",
			wantUnmapped:   false,
			wantWhenMapped: true,
		},
		{
			name:           "unreferenced pre_cci.c remains included",
			file:           "pre_cci.c",
			mappedFile:     "pre_cci.c",
			wantUnmapped:   true,
			wantWhenMapped: true,
		},
		{
			name:           "non-numeric result directory remains included",
			file:           "result/Results.xml",
			mappedFile:     "result/Results.xml",
			wantUnmapped:   true,
			wantWhenMapped: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			unmappedRef := map[string]struct{}{"action.c": {}}
			if got := shouldIncludeFile(tc.file, unmappedRef); got != tc.wantUnmapped {
				t.Fatalf("shouldIncludeFile(%q, unmapped) = %v, want %v", tc.file, got, tc.wantUnmapped)
			}

			mappedRef := map[string]struct{}{strings.ToLower(tc.mappedFile): {}}
			if got := shouldIncludeFile(tc.file, mappedRef); got != tc.wantWhenMapped {
				t.Fatalf("shouldIncludeFile(%q, mapped) = %v, want %v", tc.file, got, tc.wantWhenMapped)
			}

			review := Build(map[string]string{
				tc.file:    "// content\n",
				"Action.c": "Action() { return 0; }\n",
			}, []Mapping{
				{SourceFile: tc.mappedFile, StartLine: 1, Function: "fn", TargetPath: "actions[0]", Status: "converted"},
			})
			if review == nil {
				t.Fatal("Build returned nil")
			}
			found := false
			for _, sf := range review.SourceFiles {
				if sf.Path == tc.file {
					found = true
					break
				}
			}
			if found != tc.wantWhenMapped {
				t.Fatalf("Build mapped %q: found=%v, want %v (SourceFiles=%#v)", tc.file, found, tc.wantWhenMapped, review.SourceFiles)
			}
		})
	}
}
