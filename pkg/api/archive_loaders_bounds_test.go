package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTransactionJSONL(t *testing.T, transactions []TransactionEvent) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transactions.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, transaction := range transactions {
		if err := encoder.Encode(transaction); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadJSONLSortedTransactionPageKeepsOnlyRequestedWindow(t *testing.T) {
	transactions := make([]TransactionEvent, 20)
	for index := range transactions {
		transactions[index] = TransactionEvent{
			Timestamp: time.Unix(int64(index), 0), ActionName: "action", DurationMs: float64(index), Success: true,
		}
	}
	path := writeTransactionJSONL(t, transactions)
	items, total, err := readJSONLSortedTransactionPage(path, nil, archivePage{Limit: 3, Offset: 2}, "duration", "desc")
	if err != nil {
		t.Fatal(err)
	}
	if total != 20 || len(items) != 3 {
		t.Fatalf("total/items = %d/%d, want 20/3", total, len(items))
	}
	for index, want := range []float64{17, 16, 15} {
		if items[index].DurationMs != want {
			t.Fatalf("item[%d] duration = %v, want %v", index, items[index].DurationMs, want)
		}
	}
}

func TestArchiveLoaderRejectsUnboundedPageWindows(t *testing.T) {
	path := writeTransactionJSONL(t, []TransactionEvent{{ActionName: "action", Success: true}})
	if _, _, err := readJSONLSortedTransactionPage(path, nil, archivePage{Limit: 1, Offset: maxArchiveSortWindow}, "duration", "asc"); err == nil {
		t.Fatal("expected oversized sorted window error")
	}
	if _, _, err := readJSONLPage[TransactionEvent](path, archivePage{Limit: maxArchivePageSize + 1}, nil); err == nil {
		t.Fatal("expected oversized page limit error")
	}
}

func TestArchiveLoaderAcceptsSnapshotLargerThanLegacyScannerLimit(t *testing.T) {
	want := strings.Repeat("x", 3<<20)
	path := writeTransactionJSONL(t, []TransactionEvent{{ActionName: "large", ErrorMessage: want}})
	items, total, err := readJSONLPage[TransactionEvent](path, archivePage{Limit: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ErrorMessage != want {
		t.Fatalf("large record was not preserved: total=%d items=%d bytes=%d", total, len(items), len(items[0].ErrorMessage))
	}
}
