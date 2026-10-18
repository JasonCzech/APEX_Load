package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"errors"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"strings"
	"testing"
)

func TestArchiveLimitRefusalDoesNotClaimCorruption(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	// Declared size is inspected before decompression; no oversized allocation is
	// needed to reproduce the limit refusal for a large HAR in an archive.
	_, err := writer.CreateRaw(&zip.FileHeader{Name: "capture.har", Method: zip.Store, UncompressedSize64: maxEnterpriseArchiveEntryBytes + 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = readEnterpriseArchive(buf.Bytes())
	if !errors.Is(err, archiveingest.ErrLimitExceeded) || !strings.Contains(err.Error(), "67108864") {
		t.Fatalf("wrong limit refusal: %v", err)
	}
	for _, misleading := range []string{"corrupted", "incomplete", "unsupported ZIP compression"} {
		if strings.Contains(err.Error(), misleading) {
			t.Fatalf("limit refusal claims %s: %v", misleading, err)
		}
	}
	_, err = readEnterpriseArchive([]byte("PK\x03\x04broken archive"))
	if err == nil || errors.Is(err, archiveingest.ErrLimitExceeded) || !strings.Contains(err.Error(), "corrupted") {
		t.Fatalf("malformed archive lost diagnostic: %v", err)
	}
}
