package generatorrpc

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserAuthoredPageGeneratorArchiveRetention(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(map[int]string{1: "cdp", 2: "native_commit"}[version], func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
			recorder, err := newArchiveRecorder("authored-generator", "pages", "pages")
			if err != nil {
				t.Fatal(err)
			}
			defer recorder.Discard()
			step := protocol.NewBrowserOriginalAuthoredStep(protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}, "actions[0]")
			generation := strings.Repeat("c", 32)
			receipt := &protocol.BrowserAuthoredPageBinding{Version: version, State: "bound", ObservedAt: time.Now().UTC(), AuthoredStep: step, PageID: protocol.BrowserAuthoredPageID(step), SourceGeneration: generation, RequestID: generation + "-r1", DocumentID: generation + "-d1", LifecycleID: generation + "-l1", Association: "cdp_loader_join"}
			if version == 2 {
				receipt.Association = "native_navigation_commit"
				receipt.NavigationSequence = 9007199254740991
			}
			if !receipt.Valid() {
				t.Fatal("invalid fixture")
			}
			if err := recorder.RecordResult(&protocol.Result{ActionName: "navigate", Success: true, ProtocolEvidence: map[string]any{protocol.BrowserAuthoredPageEvidenceKey: receipt}}); err != nil {
				t.Fatal(err)
			}
			record, err := recorder.Finalize("COMPLETE", time.Now().UTC(), "")
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(filepath.Join(record.ArchivePath, "transactions.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 64<<10), 4<<20)
			if !scanner.Scan() {
				t.Fatalf("missing archive row: %v", scanner.Err())
			}
			var row archiveTransactionEvent
			if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			check := func(evidence map[string]any) {
				t.Helper()
				raw, err := json.Marshal(evidence[protocol.BrowserAuthoredPageEvidenceKey])
				if err != nil {
					t.Fatal(err)
				}
				got, err := protocol.DecodeBrowserAuthoredPageBinding(raw)
				if err != nil || !reflect.DeepEqual(got, receipt) {
					t.Fatalf("receipt changed: %+v, %v", got, err)
				}
			}
			check(row.ProtocolEvidence)
			parquetRows := toParquetTransactions([]archiveTransactionEvent{row})
			var parquetEvidence map[string]any
			if err := json.Unmarshal([]byte(parquetRows[0].ProtocolEvidenceJSON), &parquetEvidence); err != nil {
				t.Fatal(err)
			}
			check(parquetEvidence)
		})
	}
}
