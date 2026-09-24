package generatorrpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestCSVExportIdentityGeneratorArchiveRoundTrip(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	writer, err := newArchiveRecorder("export-identity-generator", "export-identity-run", "CSV identity")
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{"trace"}, {}} {
		if err := writer.RecordResult(&protocol.Result{ActionName: "redacted", CSVExportIDs: ids}); err != nil {
			t.Fatal(err)
		}
	}
	record, err := writer.Finalize("COMPLETE", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("records=%d", len(lines))
	}
	for i, line := range lines {
		var event archiveTransactionEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		want := []string{}
		if i == 0 {
			want = []string{"trace"}
		}
		if event.CSVExportIDs == nil || !slices.Equal(event.CSVExportIDs, want) {
			t.Fatalf("record %d binding=%v", i, event.CSVExportIDs)
		}
	}
}
