package javarunner

import (
	"fmt"
	"os"
	"path/filepath"
	"unicode"
)

const (
	MaxClasspathEntries    = 256
	MaxClasspathEntryBytes = 4096
)

const (
	ClasspathTooMany          = "too_many_entries"
	ClasspathEmpty            = "empty"
	ClasspathEntryTooLong     = "too_long"
	ClasspathControlCharacter = "control_character"
	ClasspathWildcard         = "wildcard_not_supported"
	ClasspathListSeparator    = "list_separator"
	ClasspathUnavailable      = "unavailable"
)

// ClasspathValidationIssue identifies an invalid entry without carrying its
// value, which may have been produced by secret/template resolution.
type ClasspathValidationIssue struct {
	Index    int
	Category string
}

// ResolveClasspath validates and canonicalizes the exact classpath entries an
// executor will pass to Java. Relative paths are resolved against workDir (or
// the executor's current directory when workDir is empty), symlinks are
// evaluated. Wildcards are deliberately rejected: script classpaths identify
// exact precompiled paths and must never trigger an unbounded filesystem glob.
//
// The returned issue deliberately contains only an index and stable category;
// classpath values may have been produced by secret/template resolution and
// must not be reflected into logs or API errors.
func ResolveClasspath(entries []string, workDir string) ([]string, *ClasspathValidationIssue) {
	if issues := ValidateClasspath(entries); len(issues) > 0 {
		issue := issues[0]
		return nil, &issue
	}
	resolved := make([]string, 0, len(entries))
	for index, entry := range entries {
		path := entry
		if !filepath.IsAbs(path) && workDir != "" {
			path = filepath.Join(workDir, path)
		}
		canonical, err := canonicalClasspathPath(path)
		if err != nil {
			return nil, &ClasspathValidationIssue{Index: index, Category: ClasspathUnavailable}
		}
		resolved = append(resolved, canonical)
	}
	return resolved, nil
}

func containsClasspathWildcard(path string) bool {
	return filepath.Base(path) != "" && (containsRune(path, '*') || containsRune(path, '?') || containsRune(path, '['))
}

// containsClasspathListSeparator reports whether an entry smuggles more than
// one path via an OS path-list separator. It is cross-OS aware: this validator
// runs on the controller (whose OS may differ from the executing generator), so
// keying off the controller-host filepath.ListSeparator would reject every
// Windows drive-letter entry (C:\lib\a.jar) on a Linux controller even though
// it resolves fine on a Windows generator. Instead reject both ':' and ';'
// regardless of host, ignoring a single leading Windows drive-letter colon so
// genuine single-path Windows entries survive import.
func containsClasspathListSeparator(entry string) bool {
	if containsRune(entry, ';') {
		return true
	}
	rest := entry
	if len(entry) >= 2 && isASCIILetter(entry[0]) && entry[1] == ':' {
		rest = entry[2:]
	}
	return containsRune(rest, ':')
}

func isASCIILetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func containsRune(value string, expected rune) bool {
	for _, current := range value {
		if current == expected {
			return true
		}
	}
	return false
}

func canonicalClasspathPath(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", fmt.Errorf("classpath entry is not a regular file or directory")
	}
	return resolved, nil
}

func (issue ClasspathValidationIssue) Error() string {
	if issue.Index < 0 {
		return fmt.Sprintf("Java classpath is invalid (%s)", issue.Category)
	}
	return fmt.Sprintf("Java classpath entry %d is invalid (%s)", issue.Index, issue.Category)
}

func ValidateClasspath(entries []string) []ClasspathValidationIssue {
	issues := make([]ClasspathValidationIssue, 0)
	if len(entries) > MaxClasspathEntries {
		issues = append(issues, ClasspathValidationIssue{Index: -1, Category: ClasspathTooMany})
	}
	for index, entry := range entries {
		category := ""
		switch {
		case entry == "":
			category = ClasspathEmpty
		case len(entry) > MaxClasspathEntryBytes:
			category = ClasspathEntryTooLong
		case containsControlCharacter(entry):
			category = ClasspathControlCharacter
		case containsClasspathWildcard(entry):
			category = ClasspathWildcard
		case containsClasspathListSeparator(entry):
			category = ClasspathListSeparator
		}
		if category != "" {
			issues = append(issues, ClasspathValidationIssue{Index: index, Category: category})
		}
	}
	return issues
}

func containsControlCharacter(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}
