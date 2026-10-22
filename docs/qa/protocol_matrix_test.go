package qa_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The non-Messaging protocol matrix is PROTO-B's acceptance inventory. Every
// relative link in it resolves to an existing file. Every test it cites in a
// code span is declared: in a test file its table row links, or, outside the
// tables, in a test file of the repository. A renamed or deleted test or file
// cannot leave the matrix claiming evidence it no longer has.
func TestProtocolTelemetryMatrixCitesRealTests(t *testing.T) {
	raw, err := os.ReadFile("PROTOCOL_TELEMETRY_MATRIX.md")
	if err != nil {
		t.Fatal(err)
	}
	// Inline links (with or without a title) and reference definitions.
	link := regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)|^\[[^\]]+\]:\s*(\S+)`)
	span := regexp.MustCompile("`([^`]+)`")
	citation := regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9_]*`)
	declared := func(name string, files []string) bool {
		declaration := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(t \*testing\.T\) \{`)
		for _, file := range files {
			source, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if declaration.Match(source) {
				return true
			}
		}
		return false
	}
	var repositoryTests []string
	cited := 0
	for number, line := range strings.Split(string(raw), "\n") {
		var testFiles []string
		for _, match := range link.FindAllStringSubmatch(line, -1) {
			target, _, _ := strings.Cut(match[1]+match[2], "#")
			if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			path := filepath.FromSlash(target)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("line %d links %s, which does not exist", number+1, target)
				continue
			}
			if strings.HasSuffix(path, "_test.go") {
				testFiles = append(testFiles, path)
			}
		}
		row := strings.HasPrefix(line, "|")
		for _, code := range span.FindAllStringSubmatch(line, -1) {
			for _, name := range citation.FindAllString(code[1], -1) {
				cited++
				files := testFiles
				if !row {
					if repositoryTests == nil {
						repositoryTests = goTestFiles(t, filepath.Join("..", ".."))
					}
					files = repositoryTests
				}
				if !declared(name, files) {
					if row {
						t.Errorf("line %d cites %s, which none of its linked test files declares", number+1, name)
					} else {
						t.Errorf("line %d cites %s, which no test file declares", number+1, name)
					}
				}
			}
		}
	}
	if cited == 0 {
		t.Fatal("the matrix cites no tests")
	}
}

// goTestFiles lists the repository's Go test files the way `go test ./...`
// finds packages: directories named testdata or starting with "." or "_"
// (retired snapshots, other checkouts under .claude) are skipped, as are
// dependencies and the dashboard.
func goTestFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() && path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "node_modules" || name == "dashboard" || name == "vendor") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
