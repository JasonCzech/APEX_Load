package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestConvertZipRejectsExcessiveEntryCount(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for i := 0; i <= maxEnterpriseArchiveEntries; i++ {
		entry, err := writer.Create(fmt.Sprintf("file-%d.txt", i))
		if err != nil {
			t.Fatalf("create entry: %v", err)
		}
		_, _ = entry.Write(nil)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if _, err := ConvertZip(archive.Bytes(), ConvertOptions{}); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("ConvertZip error = %v, want entry-count rejection", err)
	}
}

func TestConvertZipSurfacesCorruptEntry(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: "Action.c", Method: zip.Store}
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	_, _ = entry.Write([]byte("hello-corruption-marker"))
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	data := archive.Bytes()
	index := bytes.Index(data, []byte("hello-corruption-marker"))
	if index < 0 {
		t.Fatal("stored payload not found")
	}
	data[index] ^= 0xff
	if _, err := ConvertZip(data, ConvertOptions{}); err == nil || !strings.Contains(err.Error(), "read zip entry") {
		t.Fatalf("ConvertZip error = %v, want corrupt-entry read error", err)
	}
}

func TestConvertZipRejectsDuplicateNormalizedNames(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range []string{"root/Action.c", "other/Action.c"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create entry: %v", err)
		}
		_, _ = entry.Write([]byte("Action(){}"))
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if _, err := ConvertZip(archive.Bytes(), ConvertOptions{}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("ConvertZip error = %v, want duplicate-name rejection", err)
	}
}

func TestConvertZipRejectsCaseInsensitiveDuplicateNormalizedNames(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range []string{"root/default.cfg", "root/DEFAULT.cfg"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create entry: %v", err)
		}
		_, _ = entry.Write([]byte("[JavaVM:Options]\nuse_jdk=search_jdk\n"))
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if _, err := ConvertZip(archive.Bytes(), ConvertOptions{}); err == nil || !strings.Contains(err.Error(), "case-insensitive duplicate") {
		t.Fatalf("ConvertZip error = %v, want case-insensitive duplicate-name rejection", err)
	}
}

func TestConvertZipRejectsMultipleProjectFilesDeterministically(t *testing.T) {
	build := func(names []string) []byte {
		t.Helper()
		var archive bytes.Buffer
		writer := zip.NewWriter(&archive)
		for _, name := range names {
			entry, err := writer.Create(name)
			if err != nil {
				t.Fatalf("create entry %q: %v", name, err)
			}
			_, _ = entry.Write([]byte("[General]\nType=Java Vuser\n"))
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close zip: %v", err)
		}
		return archive.Bytes()
	}

	first := []string{"root/project-z/Z.USR", "root/project-a/A.usr"}
	second := []string{"root/project-a/A.usr", "root/project-z/Z.USR"}
	var want string
	for index, names := range [][]string{first, second} {
		_, err := ConvertZip(build(names), ConvertOptions{})
		if err == nil {
			t.Fatalf("archive %d was accepted, want multiple-project rejection", index)
		}
		got := err.Error()
		if !strings.Contains(got, "multiple LoadRunner project (.usr) files") ||
			!strings.Contains(got, "project-a/A.usr, project-z/Z.USR") {
			t.Fatalf("ConvertZip error = %q, want sorted cross-directory project names", got)
		}
		if index == 0 {
			want = got
		} else if got != want {
			t.Fatalf("map/archive-order dependent errors: first=%q second=%q", want, got)
		}
	}
}

func TestConvertZipRejectsTruncatedProjectConfigInsteadOfHeuristicRouting(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	project, err := writer.Create("root/Project.usr")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	_, _ = project.Write([]byte(
		"[General]\n" +
			strings.Repeat("X", maxUSRConfigLineBytes+1) +
			"\nType=DevWeb\n",
	))
	main, err := writer.Create("root/main.js")
	if err != nil {
		t.Fatalf("create main.js: %v", err)
	}
	_, _ = main.Write([]byte(`load.action("Browse", async function() {});`))
	java, err := writer.Create("root/Actions.java")
	if err != nil {
		t.Fatalf("create Actions.java: %v", err)
	}
	_, _ = java.Write([]byte(`public class Actions { public int action() { return 0; } }`))
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	_, err = ConvertZip(archive.Bytes(), ConvertOptions{})
	if err == nil ||
		!strings.Contains(err.Error(), `parse LoadRunner project file "Project.usr"`) ||
		!strings.Contains(err.Error(), "token too long") {
		t.Fatalf("ConvertZip error = %v, want bounded project-config parse rejection", err)
	}
}
