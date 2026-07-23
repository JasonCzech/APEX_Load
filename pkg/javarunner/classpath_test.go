package javarunner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateClasspathReturnsValueFreeIssues(t *testing.T) {
	const secret = "sensitive-classpath"
	entries := []string{"/classes", "", "/" + secret + "\nother"}
	issues := ValidateClasspath(entries)
	if len(issues) != 2 || issues[0].Category != ClasspathEmpty || issues[1].Category != ClasspathControlCharacter {
		t.Fatalf("issues = %#v", issues)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Error(), secret) {
			t.Fatalf("issue exposed classpath value: %v", issue)
		}
	}
}

func TestValidateClasspathEnforcesBounds(t *testing.T) {
	entries := make([]string, MaxClasspathEntries+1)
	for index := range entries {
		entries[index] = "/classes"
	}
	entries[0] = strings.Repeat("x", MaxClasspathEntryBytes+1)
	issues := ValidateClasspath(entries)
	if len(issues) != 2 || issues[0].Category != ClasspathTooMany || issues[1].Category != ClasspathEntryTooLong {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestResolveClasspathCanonicalizesExactEntries(t *testing.T) {
	dir := t.TempDir()
	libs := filepath.Join(dir, "libs")
	if err := os.MkdirAll(libs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.jar", "a.jar"} {
		if err := os.WriteFile(filepath.Join(libs, name), []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resolved, issue := ResolveClasspath([]string{"libs/a.jar", "libs/b.jar", "libs"}, dir)
	if issue != nil {
		t.Fatalf("ResolveClasspath returned issue: %v", issue)
	}
	want := []string{
		filepath.Join(libs, "a.jar"),
		filepath.Join(libs, "b.jar"),
		libs,
	}
	for index := range want {
		want[index], _ = filepath.EvalSymlinks(want[index])
	}
	if !reflect.DeepEqual(resolved, want) {
		t.Fatalf("resolved = %#v, want %#v", resolved, want)
	}
}

func TestResolveClasspathRejectsUnavailableValuesWithoutEchoingThem(t *testing.T) {
	dir := t.TempDir()
	const secret = "secret-library-name"
	if err := os.WriteFile(filepath.Join(dir, "actions.jar"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{" actions.jar ", secret + ".jar"} {
		_, issue := ResolveClasspath([]string{entry}, dir)
		if issue == nil || issue.Index != 0 || issue.Category != ClasspathUnavailable {
			t.Fatalf("ResolveClasspath(%q) issue = %#v", entry, issue)
		}
		if strings.Contains(issue.Error(), entry) || strings.Contains(issue.Error(), secret) {
			t.Fatalf("issue exposed classpath value: %v", issue)
		}
	}
}

func TestValidateClasspathRejectsWildcardsAndNativeListSeparators(t *testing.T) {
	entries := []string{"libs/*.jar", "one" + string(filepath.ListSeparator) + "two"}
	issues := ValidateClasspath(entries)
	if len(issues) != 2 || issues[0].Category != ClasspathWildcard || issues[1].Category != ClasspathListSeparator {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestValidateClasspathListSeparatorIsCrossOSAware(t *testing.T) {
	// A single Windows drive-letter path must survive validation on any
	// controller OS; only genuine multi-path smuggling is a list-separator.
	accepted := []string{
		`C:\lib\a.jar`,
		`D:/lib/b.jar`,
		"/opt/lib/a.jar",
	}
	if issues := ValidateClasspath(accepted); len(issues) != 0 {
		t.Fatalf("ValidateClasspath(%v) = %#v, want no issues", accepted, issues)
	}
	rejected := map[string]string{
		"windows separator":       `C:\a.jar;C:\b.jar`,
		"unix separator":          "/opt/a.jar:/opt/b.jar",
		"drive plus second colon": `C:\a.jar:D:\b.jar`,
	}
	for name, entry := range rejected {
		t.Run(name, func(t *testing.T) {
			issues := ValidateClasspath([]string{entry})
			if len(issues) != 1 || issues[0].Category != ClasspathListSeparator {
				t.Fatalf("ValidateClasspath(%q) = %#v, want one ClasspathListSeparator issue", entry, issues)
			}
		})
	}
}
