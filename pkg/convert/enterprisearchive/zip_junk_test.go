package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A script zipped on a Mac carries .DS_Store and a __MACOSX folder of ._
// resource forks, and a working copy carries .git. None is part of the
// script: ._Action.c once read as a C action source.
func TestConvertZipLeavesOutOperatingSystemAndVersionControlFiles(t *testing.T) {
	root := filepath.Join("testdata", "golden_web_http_html")
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	add := func(name string, data []byte) {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relative, _ := filepath.Rel(root, path)
		add("Script/"+filepath.ToSlash(relative), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	add("Script/.DS_Store", []byte("\x00\x00\x00\x01Bud1"))
	add("__MACOSX/Script/._Action.c", []byte("\x00\x05\x16\x07appledouble"))
	add("Script/.git/HEAD", []byte("ref: refs/heads/main\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := ConvertZip(archive.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FidelityReport == nil || len(result.FidelityReport.SourceFiles) == 0 {
		t.Fatal("no fidelity report")
	}
	for _, file := range result.FidelityReport.SourceFiles {
		if strings.Contains(file.Path, ".DS_Store") || strings.Contains(file.Path, "__MACOSX") || strings.Contains(file.Path, ".git/") {
			t.Errorf("source files list %s (%s)", file.Path, file.Role)
		}
	}
}
