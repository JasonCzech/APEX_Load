package zipbounds

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFileRejectsEOCDEntryFloodBeforeArchiveZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flood.zip")
	eocd := make([]byte, endOfCentralDirectoryBytes)
	binary.LittleEndian.PutUint32(eocd[0:4], endOfCentralDirectorySignature)
	binary.LittleEndian.PutUint16(eocd[8:10], 5000)
	binary.LittleEndian.PutUint16(eocd[10:12], 5000)
	if err := os.WriteFile(path, eocd, 0o600); err != nil {
		t.Fatal(err)
	}
	err := ValidateFile(path, Limits{MaxEntries: 4096, MaxCentralDirectoryBytes: 8 << 20})
	if err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("entry flood error = %v", err)
	}
}

func TestValidateFileRejectsLongCentralDirectoryName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long-name.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create(strings.Repeat("n", 1025))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("payload"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	err = ValidateFile(path, Limits{MaxEntries: 4096, MaxCentralDirectoryBytes: 8 << 20, MaxNameBytes: 1024})
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("long-name error = %v", err)
	}
}

func TestValidateFileAcceptsBoundedClassicZip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "valid.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte(`{"ok":true}`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFile(path, Limits{
		MaxArchiveBytes: 1 << 20, MaxEntries: 8, MaxCentralDirectoryBytes: 1 << 16,
		MaxNameBytes: 128, MaxExtraBytes: 128, MaxCommentBytes: 128,
	}); err != nil {
		t.Fatalf("valid zip rejected: %v", err)
	}
}

func TestValidateReaderRejectsMissingInput(t *testing.T) {
	if err := ValidateReader(nil, 30, Limits{}); err == nil {
		t.Fatal("nil reader accepted")
	}
	if err := ValidateReader(bytes.NewReader(nil), -1, Limits{}); err == nil {
		t.Fatal("negative size accepted")
	}
}
