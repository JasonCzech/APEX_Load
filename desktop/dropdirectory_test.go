package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeDropFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeDroppedDirectory(t *testing.T, raw string) map[string]string {
	t.Helper()
	var parsed droppedDirectory
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range parsed.Files {
		data, err := base64.StdEncoding.DecodeString(file.Base64)
		if err != nil {
			t.Fatal(err)
		}
		files[file.Path] = string(data)
	}
	return files
}

// A dropped collection folder reads like a browser folder pick: every file,
// named from the folder down, without bookkeeping folders or links out.
func TestReadDroppedDirectoryReturnsTheFolderLikeAFolderPick(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "CTA Collection")
	writeDropFile(t, filepath.Join(root, "opencollection.yml"), "opencollection: 1.0.0\n")
	writeDropFile(t, filepath.Join(root, "CTA", "Inquire.yml"), "info: {name: Inquire}\n")
	writeDropFile(t, filepath.Join(root, ".gitignore"), "*.log\n")
	writeDropFile(t, filepath.Join(root, ".git", "config"), "[core]\n")
	writeDropFile(t, filepath.Join(parent, "outside.txt"), "not dropped\n")
	if err := os.Symlink(filepath.Join(parent, "outside.txt"), filepath.Join(root, "linked.txt")); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.rememberDroppedDirectory(root)
	raw, err := app.ReadDroppedDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	files := decodeDroppedDirectory(t, raw)
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	want := []string{"CTA Collection/.gitignore", "CTA Collection/CTA/Inquire.yml", "CTA Collection/opencollection.yml"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if files["CTA Collection/CTA/Inquire.yml"] != "info: {name: Inquire}\n" {
		t.Fatalf("content = %q", files["CTA Collection/CTA/Inquire.yml"])
	}
}

// Only a folder the OS reported in a drop is readable; a dropped file of an
// unsupported type grants nothing, and a folder swapped after the drop is
// refused.
func TestReadDroppedDirectoryReadsOnlyGrantedFolders(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "collection")
	writeDropFile(t, filepath.Join(root, "request.bru"), "meta {}\n")
	file := filepath.Join(parent, "notes.docx")
	writeDropFile(t, file, "x")

	app := NewApp()
	app.dropGrantWait = 30 * time.Millisecond
	if _, err := app.ReadDroppedDirectory(root); err == nil {
		t.Fatal("an ungranted folder was read")
	}
	app.rememberDroppedDirectory(file)
	if _, err := app.ReadDroppedDirectory(file); err == nil {
		t.Fatal("a dropped file was read as a folder")
	}

	app.rememberDroppedDirectory(root)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	writeDropFile(t, filepath.Join(root, "request.bru"), "meta {}\n")
	if _, err := app.ReadDroppedDirectory(root); err == nil {
		t.Fatal("a folder replaced after the drop was read")
	}
}

// A mistaken drop of a huge tree stops instead of walking it all.
func TestReadDroppedDirectoryStopsAtTheEntryLimit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	for i := 0; i <= maxDroppedDirectoryEntries; i++ {
		writeDropFile(t, filepath.Join(root, "d"+strings.Repeat("x", i%7), "f"+strconv.Itoa(i)), "")
	}
	app := NewApp()
	app.rememberDroppedDirectory(root)
	if _, err := app.ReadDroppedDirectory(root); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("err = %v, want the entry limit", err)
	}
}

// A folder named like a file type is still a folder.
func TestReadDroppedDirectoryReadsAFolderNamedLikeAFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "payments.v2.yml")
	writeDropFile(t, filepath.Join(root, "opencollection.yml"), "opencollection: 1.0.0\n")
	app := NewApp()
	if isImportableFile(root) {
		app.rememberReadPath(root) // grants nothing: not a regular file
	}
	app.rememberDroppedDirectory(root)
	raw, err := app.ReadDroppedDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if files := decodeDroppedDirectory(t, raw); files["payments.v2.yml/opencollection.yml"] == "" {
		t.Fatalf("files = %v", files)
	}
}

// A dropped item the bridge did not grant is refused as soon as the drop is
// recorded, not after the grant wait: a document read as a folder, a folder
// read as a file.
func TestUngrantedDroppedPathsAreRefusedAtOnce(t *testing.T) {
	parent := t.TempDir()
	document := filepath.Join(parent, "notes.docx")
	writeDropFile(t, document, "x")
	folder := filepath.Join(parent, "orders.yml")
	writeDropFile(t, filepath.Join(folder, "opencollection.yml"), "opencollection: 1.0.0\n")

	app := NewApp()
	app.dropGrantWait = time.Hour
	for _, path := range []string{document, folder} {
		if isImportableFile(path) {
			app.rememberReadPath(path)
		}
		app.rememberDroppedDirectory(path)
	}
	app.rememberDropEvent([]string{document, folder})

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := app.ReadDroppedDirectory(document); err == nil || !strings.Contains(err.Error(), "not granted") {
			t.Errorf("document read as a folder: err = %v", err)
		}
		if _, err := app.ReadDroppedFileBase64(folder); err == nil || !strings.Contains(err.Error(), "not granted") {
			t.Errorf("folder read as a file: err = %v", err)
		}
		if _, err := app.ReadDroppedDirectory(folder); err != nil {
			t.Errorf("granted folder: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an ungranted dropped path waited for a grant")
	}
}
