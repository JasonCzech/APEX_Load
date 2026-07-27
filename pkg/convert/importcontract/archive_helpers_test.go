package importcontract

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/wsdl"
)

// zipFixtureEntry is a reusable hostile-archive input for importer contract
// tests. Tests intentionally generate archives instead of checking binary ZIPs
// into testdata, keeping entry count, declared mode, and expanded size obvious
// in review.
type zipFixtureEntry struct {
	Name   string
	Body   []byte
	Method uint16
	Mode   os.FileMode
}

func buildZIPFixture(t *testing.T, entries []zipFixtureEntry) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.Name, Method: entry.Method}
		if header.Method == 0 {
			header.Method = zip.Deflate
		}
		if entry.Mode != 0 {
			header.SetMode(entry.Mode)
		}
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("create ZIP entry %q: %v", entry.Name, err)
		}
		if _, err := part.Write(entry.Body); err != nil {
			t.Fatalf("write ZIP entry %q: %v", entry.Name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP fixture: %v", err)
	}
	return archive.Bytes()
}

func hostilePathEntries() []zipFixtureEntry {
	return []zipFixtureEntry{
		{Name: "../outside.wsdl", Body: []byte("<definitions/>")},
		{Name: "/absolute.wsdl", Body: []byte("<definitions/>")},
		{Name: `nested\..\..\escape.xsd`, Body: []byte("<schema/>")},
		{Name: "schema/../service.wsdl", Body: []byte("<definitions/>")},
		{Name: "link-to-service", Body: []byte("service.wsdl"), Mode: os.ModeSymlink | 0o777},
	}
}

func TestHostileZIPFixtureBuilderPreservesNamesModesAndPayloads(t *testing.T) {
	entries := hostilePathEntries()
	archive := buildZIPFixture(t, entries)
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open generated ZIP: %v", err)
	}
	if len(reader.File) != len(entries) {
		t.Fatalf("entries = %d, want %d", len(reader.File), len(entries))
	}
	for index, file := range reader.File {
		want := entries[index]
		if file.Name != want.Name {
			t.Fatalf("entry %d name = %q, want exact hostile name %q", index, file.Name, want.Name)
		}
		if file.FileInfo().Mode()&os.ModeSymlink != want.Mode&os.ModeSymlink {
			t.Fatalf("entry %q symlink mode = %v, want %v", file.Name, file.FileInfo().Mode(), want.Mode)
		}
		part, err := file.Open()
		if err != nil {
			t.Fatalf("open entry %q: %v", file.Name, err)
		}
		body, err := io.ReadAll(part)
		part.Close()
		if err != nil {
			t.Fatalf("read entry %q: %v", file.Name, err)
		}
		if !bytes.Equal(body, want.Body) {
			t.Fatalf("entry %q body = %q, want %q", file.Name, body, want.Body)
		}
	}
}

func TestWSDLArchiveResourceLimitsCharacterizeCurrentContract(t *testing.T) {
	t.Run("entry_count", func(t *testing.T) {
		entries := make([]zipFixtureEntry, 2001)
		for index := range entries {
			entries[index] = zipFixtureEntry{
				Name: fmt.Sprintf("schemas/%04d.xsd", index),
				Body: []byte("<schema/>"),
			}
		}
		archive := buildZIPFixture(t, entries)
		if _, _, err := wsdl.FilesFromZip(archive); err == nil || !strings.Contains(err.Error(), "too many entries") {
			t.Fatalf("FilesFromZip error = %v, want entry-count rejection", err)
		}
	})

	t.Run("expanded_size", func(t *testing.T) {
		archive := buildZIPFixture(t, []zipFixtureEntry{
			{Name: "service.wsdl", Body: bytes.Repeat([]byte("x"), (20<<20)+1)},
		})
		if _, _, err := wsdl.FilesFromZip(archive); err == nil ||
			(!strings.Contains(err.Error(), "decompressed size") && !strings.Contains(err.Error(), "uncompressed size")) {
			t.Fatalf("FilesFromZip error = %v, want expanded-size rejection", err)
		}
	})
}
