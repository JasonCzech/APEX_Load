package enterprisearchive

// java_source_names.go recognizes Java sources that reached the import with a
// .txt extension.
//
// Corporate mail gateways and proxies routinely strip .java attachments, so
// operators rename sources to .txt before sending them. The import UI already
// offers .txt in its supplemental-file picker and the API applies no extension
// filter, but every Java pass keys off a .java suffix — so such a file used to
// be accepted, stored, and then silently ignored, surfacing as a confusing
// "unresolved superclass" promotion blocker.
//
// The rename happens once, on the archive key itself, before anything else
// reads the file map. Renaming later (inside parseJavaProgram) would leave
// javaClass.File pointing at a path that is not an archive entry, which drops
// the source from BuildJavaVuserSourceManifest and therefore from the approval
// evidence bound to that manifest, while also bypassing the duplicate-entry
// guards in ConvertZip.

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// maxJavaSourceSniffBytes bounds the lexer pass used to decide whether a .txt
// entry is Java. A package, import, or type declaration always appears well
// within this prefix; scanning more than this to answer a naming question is
// not worth the cost on a 64 MiB archive entry.
const maxJavaSourceSniffBytes = 256 << 10

var (
	javaSourcePackageRe = regexp.MustCompile(`(?m)^[ \t]*package[ \t]+[A-Za-z_$][A-Za-z0-9_$]*(?:[ \t]*\.[ \t]*[A-Za-z_$][A-Za-z0-9_$]*)*[ \t]*;`)
	javaSourceImportRe  = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+(?:static[ \t]+)?[A-Za-z_$][A-Za-z0-9_$]*(?:[ \t]*\.[ \t]*(?:[A-Za-z_$][A-Za-z0-9_$]*|\*))*[ \t]*;`)
	// A type declaration only counts when a Java ACCESS/CLASS MODIFIER precedes
	// it. "class <Ident> {" on its own is not evidence of Java: it matches
	// ordinary English prose ("the class Actions {see listing}"), a C enum
	// ("enum Status { OK, FAIL };"), a JavaScript class, and even CSS. Renaming
	// on that basis let a plain handover note in a Web archive divert the whole
	// import into the Java Vuser converter and discard the recorded HTTP
	// workload, so the bar here is deliberately high.
	javaSourceModifiers = `(?:public|protected|private|abstract|final|static|sealed|strictfp)`
	javaSourceTypeRe    = regexp.MustCompile(
		`\b` + javaSourceModifiers + `(?:[ \t]+` + javaSourceModifiers + `)*[ \t]+(?:(?:class|interface|enum|record)|@[ \t]*interface)[ \t]+[A-Za-z_$][A-Za-z0-9_$]*`,
	)
	// The shapes a compilable Java file can legally open with.
	javaSourceFileStartRe = regexp.MustCompile(
		`^(?:package[ \t]|import[ \t]|@[A-Za-z_$]|` + javaSourceModifiers + `[ \t]|class[ \t]|interface[ \t]|enum[ \t]|record[ \t])`,
	)
)

// javaSourceFilename returns the archive path a source entry should be stored
// under. Entries that already end in .java keep their path unchanged. A .txt
// entry whose content is Java is renamed in place with its directory preserved,
// because javaSourcePathMatchRank ranks exact, suffix, and basename matches
// differently when selecting the runner class from a .usr declaration. An empty
// result means the entry is not Java source.
func javaSourceFilename(name, content string) string {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".java") {
		return name
	}
	if !strings.HasSuffix(lower, ".txt") {
		return ""
	}
	target := name[:len(name)-len(".txt")]
	if !strings.HasSuffix(strings.ToLower(target), ".java") {
		target += ".java"
	}
	// Reject entries whose whole base name is the extension (".txt",
	// ".java.txt"); renaming those would produce a dotfile, not a class.
	if len(path.Base(target)) <= len(".java") {
		return ""
	}
	if !looksLikeJavaSourceText(content) {
		return ""
	}
	return target
}

// looksLikeJavaSourceText reports whether content is plausibly Java source.
// Comments, string literals, and text blocks are masked first so a log or a
// document that merely quotes Java is not mistaken for source.
func looksLikeJavaSourceText(content string) bool {
	// Strip a UTF-8 BOM first: a mail-gateway .txt has typically passed through
	// a Windows editor that writes one, and an un-stripped BOM defeats the
	// anchored first-line check below. The browser side needs no equivalent —
	// Blob.text() strips the BOM as part of UTF-8 decoding.
	content = strings.TrimPrefix(content, "\ufeff")
	if len(content) > maxJavaSourceSniffBytes {
		content = content[:maxJavaSourceSniffBytes]
	}
	code := javaCodeOnly(content)
	// The file must BEGIN like Java. A handover note that quotes a full class
	// listing further down still contains a modifier-qualified declaration, and
	// renaming it folds documentation into the immutable source manifest that
	// approval evidence is hash-bound to — so editing a sentence in a README
	// would change an otherwise identical script's digest. Real source is
	// compilable, meaning its first meaningful token is a package/import, an
	// annotation, or a (possibly modified) type declaration.
	if !javaSourceFileStartRe.MatchString(firstMeaningfulJavaLine(code)) {
		return false
	}
	// A modifier-qualified type declaration is strong enough on its own. Failing
	// that, a package/import declaration plus a body is required — one weak
	// signal is never sufficient.
	//
	// Accepted false negative: a package-private `class X { }` carrying no
	// package and no import is not recognized. Dropping the modifier requirement
	// would re-admit a C file whose first line is `enum Status { OK, FAIL };`,
	// and such a file is not a realistic LoadRunner Vuser source (those import
	// lrapi). Rename it by hand.
	if javaSourceTypeRe.MatchString(code) {
		return true
	}
	if !javaSourcePackageRe.MatchString(code) && !javaSourceImportRe.MatchString(code) {
		return false
	}
	return strings.Contains(code, "{")
}

// firstMeaningfulJavaLine returns the first non-blank line of already
// comment-masked code, which for real source is the file's opening declaration.
func firstMeaningfulJavaLine(code string) string {
	for _, line := range strings.Split(code, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// archiveHasJavaSourceSignal reports whether the archive independently declares
// itself Java, so the .txt rename can be scoped to archives where it is wanted.
//
// This is the structural half of the protection: protocol detection runs AFTER
// the rename and partly keys off .java filenames, so an unscoped rename can
// flip a non-Java archive into the Java Vuser converter. Requiring evidence that
// does not come from the renamed file itself makes that impossible, while still
// covering both real cases — a supplemental source beside real .java files, and
// a whole script whose .usr declares Java action files.
func archiveHasJavaSourceSignal(files map[string]string) bool {
	for name, content := range files {
		lowerName := strings.ToLower(name)
		if strings.HasSuffix(lowerName, ".java") {
			return true
		}
		if !strings.HasSuffix(lowerName, ".usr") {
			continue
		}
		// Deliberately a line scan rather than ParseUSRFile: the project file has
		// not been located or validated yet at this point in ConvertZip.
		//
		// Values are matched with word precision on purpose. A substring test for
		// "=java" also matches ScriptLanguage=JavaScript, and a test for ".java"
		// matches any passing mention of a Java file in a comment — both would
		// re-open the rename on exactly the TruClient/DevWeb/C archives this gate
		// exists to protect.
		for _, line := range strings.Split(content, "\n") {
			// Skip comment lines the way ParseConfigSections/ParseUSRFile do. A
			// commented-out migration leftover like ";Action=Legacy.java" is
			// invisible to the parser, so it must not open this gate either.
			trimmedLine := strings.TrimSpace(line)
			if strings.HasPrefix(trimmedLine, ";") || strings.HasPrefix(trimmedLine, "#") {
				continue
			}
			separator := strings.Index(trimmedLine, "=")
			if separator < 0 {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(trimmedLine[:separator]))
			value := strings.ToLower(strings.TrimSpace(trimmedLine[separator+1:]))
			switch key {
			case "type", "scriptlanguage", "runtype":
				if javaWordRe.MatchString(value) {
					return true
				}
			}
			if strings.HasSuffix(value, ".java") {
				return true
			}
		}
	}
	return false
}

// javaWordRe matches "java" as a whole word, so "Java Vuser", "JAVA", and
// "Java-Interpreter" qualify while "JavaScript" does not.
var javaWordRe = regexp.MustCompile(`\bjava\b`)

// normalizeJavaSourceArchiveNames renames Java sources that arrived as .txt to
// their .java path, in place, on the archive file map. Collisions are reported
// rather than resolved: two entries that normalize to the same path would
// otherwise parse into two classes sharing a File and Name, which makes
// qualified-type resolution ambiguous and produces a false "unresolved
// superclass" blocker on an import that would otherwise succeed.
func normalizeJavaSourceArchiveNames(files map[string]string, normalizedFileNames map[string]string) error {
	// Scoped to archives that declare themselves Java by evidence other than the
	// renamed file. Without this, a plain .txt in a Web/DevWeb archive could be
	// renamed and then satisfy the .java-filename fallback in isJavaVuserArchive,
	// routing the archive to the Java converter and dropping its real workload.
	if !archiveHasJavaSourceSignal(files) {
		return nil
	}

	renames := make(map[string]string)
	for _, name := range sortedFileNames(files) {
		if !strings.HasSuffix(strings.ToLower(name), ".txt") {
			continue
		}
		target := javaSourceFilename(name, files[name])
		if target == "" || target == name {
			continue
		}
		renames[name] = target
	}
	if len(renames) == 0 {
		return nil
	}

	sources := make([]string, 0, len(renames))
	for source := range renames {
		sources = append(sources, source)
	}
	sort.Strings(sources)

	claimed := make(map[string]string, len(renames))
	for _, source := range sources {
		target := renames[source]
		caseFoldedTarget := strings.ToLower(target)
		if existing, taken := normalizedFileNames[caseFoldedTarget]; taken {
			return fmt.Errorf(
				"zip archive contains Java source %q, which imports as %q and collides with existing entry %q; remove one of them and re-import",
				source, target, existing,
			)
		}
		if existing, taken := claimed[caseFoldedTarget]; taken {
			return fmt.Errorf(
				"zip archive contains Java sources %q and %q, which both import as %q; remove one of them and re-import",
				existing, source, target,
			)
		}
		claimed[caseFoldedTarget] = source
	}

	for _, source := range sources {
		target := renames[source]
		files[target] = files[source]
		delete(files, source)
		delete(normalizedFileNames, strings.ToLower(source))
		normalizedFileNames[strings.ToLower(target)] = target
	}
	return nil
}
