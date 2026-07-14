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
