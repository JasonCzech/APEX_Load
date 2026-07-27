package enterprisearchive

import (
	"strings"
	"testing"
)

func TestJavaSourceFilenameRenamesJavaTextEntries(t *testing.T) {
	const source = `package com.acme;

public class BasicClient extends Object {
  public BasicClient() {}
}`

	tests := []struct {
		name    string
		file    string
		content string
		want    string
	}{
		{name: "java is unchanged", file: "src/BasicClient.java", content: source, want: "src/BasicClient.java"},
		{name: "txt keeps its directory", file: "src/com/acme/BasicClient.txt", content: source, want: "src/com/acme/BasicClient.java"},
		{name: "double extension collapses", file: "BasicClient.java.txt", content: source, want: "BasicClient.java"},
		{name: "uppercase extension", file: "BasicClient.TXT", content: source, want: "BasicClient.java"},
		{name: "bare extension is rejected", file: "src/.txt", content: source, want: ""},
		{name: "non-java txt is left alone", file: "params.txt", content: "user,password\nalice,secret\n", want: ""},
		{name: "other extensions are ignored", file: "notes.md", content: source, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := javaSourceFilename(tc.file, tc.content); got != tc.want {
				t.Fatalf("javaSourceFilename(%q) = %q, want %q", tc.file, got, tc.want)
			}
		})
	}
}

func TestLooksLikeJavaSourceTextIgnoresProseAndQuotedJava(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "type declaration",
			content: "public final class Foo implements Bar {\n}\n",
			want:    true,
		},
		{
			name:    "package plus body",
			content: "package com.acme;\n\nclass Foo {\n}\n",
			want:    true,
		},
		{
			name:    "annotation type",
			content: "public @interface Marker {\n}\n",
			want:    true,
		},
		{
			// "extends" and "implements" appear constantly in English prose;
			// matching them alone would rewrite recording logs and READMEs.
			name:    "prose that mentions java keywords",
			content: "The replay class extends the recorded session and implements retries.\n",
			want:    false,
		},
		{
			// A bare "class <Ident> {" is NOT evidence of Java. Accepting it let a
			// handover note in a Web archive divert the entire import into the
			// Java Vuser converter, discarding the recorded HTTP workload.
			name:    "prose naming a class with a brace",
			content: "Handover notes\nThe class Actions {see listing} uses lrapi helpers from the old harness.\n",
			want:    false,
		},
		{
			name:    "C enum saved as text",
			content: "enum Status { OK, FAIL };\n",
			want:    false,
		},
		{
			name:    "javascript class",
			content: "class Foo extends Bar {\n  run() {}\n}\n",
			want:    false,
		},
		{
			name:    "css rule",
			content: ".class Foo { color: red; }\n",
			want:    false,
		},
		{
			name:    "csv whose header looks like a declaration",
			content: "class Name {id}\nA,1\nB,2\n",
			want:    false,
		},
		{
			// A note that QUOTES a full class listing still contains a
			// modifier-qualified declaration. Renaming it folds documentation into
			// the immutable source manifest that approval evidence is hash-bound
			// to, so editing a sentence would change an identical script's digest.
			// Real source is compilable: its first meaningful token is a
			// package/import, an annotation, or a type declaration.
			name: "handover note quoting a class listing",
			content: `Handover notes for the payments harness.

The runner class is shown below for reference:

public class Actions extends BasicClient {
  public int action() { return 0; }
}

Contact the perf team before changing it.`,
			want: false,
		},
		{
			name:    "license header before the declaration",
			content: "/* Copyright 2026 Acme */\npublic class Actions { }\n",
			want:    true,
		},
		{
			name:    "annotation before the declaration",
			content: "@Deprecated\npublic class Actions { }\n",
			want:    true,
		},
		{
			name:    "leading blank lines",
			content: "\n\n\npublic class Actions { }\n",
			want:    true,
		},
		{
			// A mail-gateway .txt has typically passed through a Windows editor
			// that writes a UTF-8 BOM; un-stripped it defeats the anchored
			// first-line check and rejects real source.
			name:    "utf-8 bom before the declaration",
			content: "\ufeffpackage com.acme;\npublic class Actions { }\n",
			want:    true,
		},
		{
			name:    "utf-8 bom with crlf line endings",
			content: "\ufeffpackage com.acme;\r\npublic class Actions { }\r\n",
			want:    true,
		},
		{
			// Pins the ^ anchor of javaSourceFileStartRe: "package " appears
			// mid-sentence on the first line, and the file also quotes a
			// modifier-qualified declaration below. An unanchored first-line
			// check would accept this.
			name:    "first line mentioning package mid-sentence",
			content: "See package com.acme; for details\npublic class Actions { }\n",
			want:    false,
		},
		{
			name:    "java quoted inside a string literal",
			content: "package com.acme;\nString sample = \"public class Foo { }\";\n",
			want:    false,
		},
		{
			name:    "java inside a block comment",
			content: "/* public class Foo extends Bar { } */\n",
			want:    false,
		},
		{
			name:    "loadrunner parameter file",
			content: "account,region\n1001,east\n1002,west\n",
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeJavaSourceText(tc.content); got != tc.want {
				t.Fatalf("looksLikeJavaSourceText() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConvertZipResolvesSuperclassSuppliedAsTextFile(t *testing.T) {
	// Corporate mail gateways strip .java attachments, so supplemental sources
	// routinely arrive renamed to .txt. The archive must treat them as Java.
	files := map[string]string{
		"Actions.java": `public class Actions extends BasicClient {
  public int action() { return 0; }
}`,
		"BasicClient.java.txt": `public class BasicClient {
  public BasicClient() {}
}`,
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaTextSuperclass", files), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)

	if result.FidelityReport == nil {
		t.Fatal("fidelity report is nil")
	}
	blockers := strings.ToLower(strings.Join(result.FidelityReport.Blockers, "\n"))
	if strings.Contains(blockers, "unresolved superclass") {
		t.Fatalf("fidelity blockers = %#v, want the .txt superclass to resolve", result.FidelityReport.Blockers)
	}

	// The rename has to reach the immutable source manifest, because the
	// import-promotion approval evidence is bound to that hash. A parser-local
	// rename would leave the file out of it entirely.
	manifestValue, ok := result.ReviewArtifact.Metadata[JavaVuserSourceManifestMetadataKey]
	if !ok {
		t.Fatal("review artifact is missing the Java source manifest")
	}
	manifest, err := DecodeJavaVuserSourceManifest(manifestValue)
	if err != nil {
		t.Fatalf("DecodeJavaVuserSourceManifest: %v", err)
	}
	var manifestPaths []string
	for _, entry := range manifest.Files {
		manifestPaths = append(manifestPaths, entry.Path)
	}
	if !containsFold(manifestPaths, "BasicClient.java") {
		t.Fatalf("source manifest paths = %#v, want BasicClient.java", manifestPaths)
	}
	if containsFold(manifestPaths, "BasicClient.java.txt") {
		t.Fatalf("source manifest paths = %#v, want the .txt path renamed", manifestPaths)
	}
}

// The harder shape: not just a supplemental superclass, but the .usr's declared
// action file itself arriving as .txt — a whole script that came through a mail
// gateway. This is the case that exercises .usr action-file matching against the
// renamed path (javaSourcePathMatchRank), which the supplemental test does not.
func TestConvertZipResolvesActionFileSuppliedAsTextFile(t *testing.T) {
	files := map[string]string{
		"Actions.java.txt": `import lrapi.lr;
public class Actions {
  public int init() { return 0; }
  public int action() { lr.start_transaction("t"); lr.end_transaction("t", lr.AUTO); return 0; }
  public int end() { return 0; }
}`,
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaTextActionFile", files), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)

	if result.Script.Protocol != "java_vuser" {
		t.Fatalf("protocol = %q, want java_vuser", result.Script.Protocol)
	}
	if result.FidelityReport == nil {
		t.Fatal("fidelity report is nil")
	}
	if len(result.FidelityReport.Blockers) != 0 {
		t.Fatalf("blockers = %#v, want none for a fully resolved .txt script", result.FidelityReport.Blockers)
	}

	manifestValue, ok := result.ReviewArtifact.Metadata[JavaVuserSourceManifestMetadataKey]
	if !ok {
		t.Fatal("review artifact is missing the Java source manifest")
	}
	manifest, err := DecodeJavaVuserSourceManifest(manifestValue)
	if err != nil {
		t.Fatalf("DecodeJavaVuserSourceManifest: %v", err)
	}
	var manifestPaths []string
	for _, entry := range manifest.Files {
		manifestPaths = append(manifestPaths, entry.Path)
	}
	if !containsFold(manifestPaths, "Actions.java") {
		t.Fatalf("source manifest paths = %#v, want the renamed Actions.java", manifestPaths)
	}
}

// The gate that scopes the rename to Java archives must not be satisfied by a
// passing mention of Java. A substring test for "=java" also matches
// ScriptLanguage=JavaScript, which would re-open the rename on exactly the
// TruClient/DevWeb archives the gate exists to protect.
func TestArchiveHasJavaSourceSignalMatchesJavaWithWordPrecision(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{
			name:  "javascript script language is not java",
			files: map[string]string{"S.usr": "[General]\nType=TruClient\nScriptLanguage=JavaScript\n[Actions]\nAction=Action.js\n"},
			want:  false,
		},
		{
			name:  "a comment mentioning a java file is not a declaration",
			files: map[string]string{"S.usr": "[General]\nType=QuickTest_Web\nScriptLanguage=C\n; ported from Legacy.java\n[Actions]\nAction=Action.c\n"},
			want:  false,
		},
		{
			// The parser skips ;-prefixed lines, so a commented-out migration
			// leftover WITH a key=value shape must not open the gate either —
			// this line reaches the value parser, unlike the comment above.
			name:  "a commented-out java action line is not a declaration",
			files: map[string]string{"S.usr": "[General]\nType=QuickTest_Web\nScriptLanguage=C\n;Action=Legacy.java\n[Actions]\nAction=Action.c\n"},
			want:  false,
		},
		{
			name:  "a commented-out java type line is not a declaration",
			files: map[string]string{"S.usr": "[General]\n;Type=Java Vuser\nType=QuickTest_Web\nScriptLanguage=C\n"},
			want:  false,
		},
		{name: "no usr and no java entries", files: map[string]string{"Action.c": "Action() { return 0; }"}, want: false},
		{
			name:  "java vuser type",
			files: map[string]string{"S.usr": "[General]\nType=Java Vuser\nScriptLanguage=JAVA\n"},
			want:  true,
		},
		{
			name:  "java-interpreter run type",
			files: map[string]string{"S.usr": "[General]\nType=Java\nRunType=Java-Interpreter\n"},
			want:  true,
		},
		{
			name:  "declared java action file",
			files: map[string]string{"S.usr": "[General]\nType=Unknown\n[Actions]\nAction=Actions.java\n"},
			want:  true,
		},
		{
			name:  "a real java entry needs no usr",
			files: map[string]string{"Actions.java": "public class Actions {}"},
			want:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := archiveHasJavaSourceSignal(tc.files); got != tc.want {
				t.Fatalf("archiveHasJavaSourceSignal() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The rename must never change which converter an archive is routed to. Protocol
// detection runs after it and partly keys off .java filenames, so an unscoped
// rename let a plain text file in a Web archive satisfy isJavaVuserArchive's
// fallback and divert the import — discarding the recorded HTTP workload.
func TestConvertZipDoesNotLetTextFilesDivertANonJavaArchive(t *testing.T) {
	cAction := `Action() {
  lr_start_transaction("Home");
  web_url("home", "URL=https://shop.example.com/home", LAST);
  lr_end_transaction("Home", LR_AUTO);
  return 0;
}`

	tests := []struct {
		name    string
		extra   string
		content string
	}{
		{name: "no extra file at all"},
		// This case is the only one that actually exercises
		// archiveHasJavaSourceSignal. It has to clear BOTH hurdles that stand
		// between a .txt and a diverted import: the content must satisfy
		// looksLikeJavaSourceText, AND the renamed name must trip an
		// isJavaVuserArchive fallback (here the actions.java basename). Content
		// that only clears the first is stopped by the sniff, so the scope gate
		// stays unexercised and the case proves nothing.
		{name: "real java source named to trip the divert fallback", extra: "Actions.txt", content: "package com.acme;\n\npublic class Actions {\n  public int action() { return 0; }\n}\n"},
		{name: "prose note mentioning lrapi", extra: "README.txt", content: "Handover notes\nThe class Actions {see listing} uses lrapi helpers from the old Java harness."},
		{name: "C enum named like a lifecycle file", extra: "Actions.txt", content: "enum Status { OK, FAIL };"},
		{name: "javascript class", extra: "notes.txt", content: "class Foo extends Bar {\n}\n"},
		{name: "css rule", extra: "style.txt", content: ".class Foo { color: red; }"},
		{name: "vuser_init named data file", extra: "vuser_init.txt", content: "class Name {id}\nA,1\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"Action.c": cAction}
			if tc.extra != "" {
				files[tc.extra] = tc.content
			}
			result, err := ConvertZip(zipFilesForTest(t, files), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			if result.Script.Protocol != "http" {
				t.Fatalf("protocol = %q, want http (the archive must not be diverted to the Java converter)", result.Script.Protocol)
			}
			if len(result.Script.Actions) == 0 {
				t.Fatal("script has no actions; the recorded HTTP workload was discarded")
			}
			for _, action := range result.Script.Actions {
				if action.Type == "java_vuser_run" {
					t.Fatalf("actions contain java_vuser_run: %#v", result.Script.Actions)
				}
			}
		})
	}
}

// Even inside a genuinely Java archive, prose and data files must stay out of the
// immutable source manifest — the import-promotion approval evidence is bound to
// that hash, so polluting it makes an operator approve files that are not source
// and makes the digest depend on unrelated text.
func TestConvertZipKeepsNonSourceTextOutOfTheSourceManifest(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int action() { return 0; }
}`,
		"accounts.txt": "class Name {id}\nA,1\n",
		"notes.txt":    "The class Actions {see the attached listing} was recorded on 2026-07-01.",
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaManifestPurity", files), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	manifest, err := DecodeJavaVuserSourceManifest(result.ReviewArtifact.Metadata[JavaVuserSourceManifestMetadataKey])
	if err != nil {
		t.Fatalf("DecodeJavaVuserSourceManifest: %v", err)
	}
	for _, entry := range manifest.Files {
		if strings.EqualFold(entry.Path, "accounts.java") || strings.EqualFold(entry.Path, "notes.java") {
			t.Fatalf("source manifest = %#v, want non-source text excluded", manifest.Files)
		}
	}
	// && here made the check unreachable: a wrong path already fails the loop
	// above, so the size assertion could never fire on its own.
	if len(manifest.Files) != 1 || !strings.EqualFold(manifest.Files[0].Path, "Actions.java") {
		t.Fatalf("source manifest = %#v, want exactly the real Java source", manifest.Files)
	}
}

func TestConvertZipRejectsJavaTextEntryThatCollidesWithJavaSource(t *testing.T) {
	// Two entries normalizing to one path would parse into two classes sharing
	// a File and Name, which makes qualified-type resolution ambiguous and
	// emits a false "unresolved superclass" blocker. Fail loudly instead.
	files := map[string]string{
		"Actions.java": `public class Actions extends BasicClient {
  public int action() { return 0; }
}`,
		"BasicClient.java": `public class BasicClient {
  public BasicClient() {}
}`,
		"BasicClient.txt": `public class BasicClient {
  public BasicClient() {}
}`,
	}
	_, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaTextCollision", files), ConvertOptions{})
	if err == nil {
		t.Fatal("ConvertZip succeeded, want a collision error")
	}
	message := err.Error()
	if !strings.Contains(message, "BasicClient.txt") || !strings.Contains(message, "BasicClient.java") {
		t.Fatalf("error = %q, want both colliding entries named", message)
	}
}

func TestConvertZipLeavesNonJavaTextEntriesAlone(t *testing.T) {
	files := map[string]string{
		"Actions.java": `public class Actions {
  public int action() { return 0; }
}`,
		"accounts.txt": "account,region\n1001,east\n",
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaTextParameters", files), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	assertCombinedJavaImport(t, result)

	manifestValue, ok := result.ReviewArtifact.Metadata[JavaVuserSourceManifestMetadataKey]
	if !ok {
		t.Fatal("review artifact is missing the Java source manifest")
	}
	manifest, err := DecodeJavaVuserSourceManifest(manifestValue)
	if err != nil {
		t.Fatalf("DecodeJavaVuserSourceManifest: %v", err)
	}
	for _, entry := range manifest.Files {
		if strings.Contains(strings.ToLower(entry.Path), "accounts") {
			t.Fatalf("source manifest = %#v, want the parameter file left as .txt", manifest.Files)
		}
	}
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}
