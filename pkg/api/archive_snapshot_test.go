package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestResolveArchivedSnapshotRequestURLFromDependencyTarget(t *testing.T) {
	tests := []struct {
		name             string
		requestURL       string
		dependencyTarget string
		want             string
	}{
		{
			name:             "rebuilds https URL for legacy var base",
			requestURL:       "{{var.url}}/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws",
			dependencyTarget: "csi-gw-ext.att.com:443",
			want:             "https://csi-gw-ext.att.com:443/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws",
		},
		{
			name:             "rebuilds http URL for port 80 dependency",
			requestURL:       "{{param.base_url}}/orders",
			dependencyTarget: "orders.example.test:80",
			want:             "http://orders.example.test:80/orders",
		},
		{
			name:             "preserves already resolved URL",
			requestURL:       "https://api.example.test/orders",
			dependencyTarget: "api.example.test",
			want:             "https://api.example.test/orders",
		},
		{
			name:             "leaves non-leading template values unchanged",
			requestURL:       "/orders/{{param.order_id}}",
			dependencyTarget: "api.example.test",
			want:             "/orders/{{param.order_id}}",
		},
		{
			name:             "leaves unresolved base without dependency unchanged",
			requestURL:       "{{var.url}}/orders",
			dependencyTarget: "",
			want:             "{{var.url}}/orders",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveArchivedSnapshotRequestURL(tt.requestURL, tt.dependencyTarget); got != tt.want {
				t.Fatalf("resolveArchivedSnapshotRequestURL(%q, %q) = %q, want %q", tt.requestURL, tt.dependencyTarget, got, tt.want)
			}
		})
	}
}

func TestLoadArchiveTransactionsNormalizesLegacyTemplatedSnapshotURL(t *testing.T) {
	archiveDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-acme",
		TestName:              "ACME",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}

	txFile, err := os.Create(filepath.Join(archiveDir, manifest.TransactionsFile))
	if err != nil {
		t.Fatalf("Create transactions file returned error: %v", err)
	}
	if err := json.NewEncoder(txFile).Encode(TransactionEvent{
		Timestamp:        time.Date(2026, time.May, 20, 20, 55, 22, 0, time.UTC),
		TestID:           manifest.TestID,
		TestName:         manifest.TestName,
		ActionName:       "ACME_ISBA_web_custom_request",
		DependencyType:   "http",
		DependencyTarget: "csi-gw-ext.att.com:443",
		Success:          false,
		Snapshot: &protocol.ErrorSnapshot{
			RequestMethod: "POST",
			RequestURL:    "{{var.url}}/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws",
		},
	}); err != nil {
		t.Fatalf("Encode transaction returned error: %v", err)
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("Close transactions file returned error: %v", err)
	}

	transactions, total, err := loadArchiveTransactions(
		&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath},
		transactionEventFilter{Action: "ACME_ISBA_web_custom_request"},
		archivePage{Limit: 1},
	)
	if err != nil {
		t.Fatalf("loadArchiveTransactions returned error: %v", err)
	}
	if total != 1 || len(transactions) != 1 {
		t.Fatalf("loadArchiveTransactions returned total=%d len=%d, want one transaction", total, len(transactions))
	}
	const want = "https://csi-gw-ext.att.com:443/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreement.jws"
	if got := transactions[0].Snapshot.RequestURL; got != want {
		t.Fatalf("Snapshot.RequestURL = %q, want %q", got, want)
	}
}

func TestLoadArchiveTransactionsSortsAndFiltersColumns(t *testing.T) {
	record := writeArchiveTransactionsForTest(t, []TransactionEvent{
		{
			Timestamp:     time.Date(2026, time.May, 20, 20, 0, 0, 0, time.UTC),
			TestID:        "test-query",
			TestName:      "Query Test",
			ActionName:    "Checkout",
			Group:         "Web",
			DurationMs:    140,
			BytesReceived: 900,
			StatusCode:    500,
			Success:       false,
			ErrorMessage:  "upstream timeout",
		},
		{
			Timestamp:     time.Date(2026, time.May, 20, 20, 0, 2, 0, time.UTC),
			TestID:        "test-query",
			TestName:      "Query Test",
			ActionName:    "Checkout",
			Group:         "Web",
			DurationMs:    40,
			BytesReceived: 150,
			StatusCode:    200,
			Success:       true,
		},
		{
			Timestamp:     time.Date(2026, time.May, 20, 20, 0, 1, 0, time.UTC),
			TestID:        "test-query",
			TestName:      "Query Test",
			ActionName:    "Checkout",
			Group:         "Web",
			DurationMs:    95,
			BytesReceived: 450,
			StatusCode:    404,
			Success:       false,
			ErrorMessage:  "not found in catalog",
		},
	})

	minDuration := 90.0
	minBytes := int64(400)
	transactions, total, err := loadArchiveTransactions(
		record,
		transactionEventFilter{
			Action:        "Checkout",
			Status:        "4",
			MinDurationMs: &minDuration,
			MinBytes:      &minBytes,
			ErrorContains: "catalog",
			SortBy:        "duration",
			SortDir:       "desc",
		},
		archivePage{Limit: 10},
	)
	if err != nil {
		t.Fatalf("loadArchiveTransactions returned error: %v", err)
	}
	if total != 1 || len(transactions) != 1 {
		t.Fatalf("loadArchiveTransactions returned total=%d len=%d, want one transaction", total, len(transactions))
	}
	if got := transactions[0].StatusCode; got != 404 {
		t.Fatalf("StatusCode = %d, want 404", got)
	}

	transactions, total, err = loadArchiveTransactions(
		record,
		transactionEventFilter{
			Action:  "Checkout",
			SortBy:  "duration",
			SortDir: "desc",
		},
		archivePage{Limit: 2},
	)
	if err != nil {
		t.Fatalf("loadArchiveTransactions returned error: %v", err)
	}
	if total != 3 || len(transactions) != 2 {
		t.Fatalf("sorted page returned total=%d len=%d, want total 3 len 2", total, len(transactions))
	}
	if transactions[0].DurationMs != 140 || transactions[1].DurationMs != 95 {
		t.Fatalf("duration sort returned durations %.1f, %.1f; want 140.0, 95.0", transactions[0].DurationMs, transactions[1].DurationMs)
	}
}

func TestParseTransactionEventFilterColumnOptions(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/archive/transactions?time_from=2026-05-20T20:00:00Z&time_to=2026-05-20T20:05:00Z&status=403&min_duration_ms=50&max_duration_ms=200&min_bytes=100&max_bytes=900&error_contains=timeout&sort_by=duration&sort_dir=desc", nil)
	recorder := httptest.NewRecorder()

	filter, ok := parseTransactionEventFilter(recorder, req)
	if !ok {
		t.Fatalf("parseTransactionEventFilter returned false: %s", recorder.Body.String())
	}
	if filter.TimeFrom == nil || filter.TimeTo == nil {
		t.Fatal("expected time range filters to be parsed")
	}
	if filter.Status != "403" {
		t.Fatalf("Status = %q, want 403", filter.Status)
	}
	if filter.MinDurationMs == nil || *filter.MinDurationMs != 50 {
		t.Fatalf("MinDurationMs = %v, want 50", filter.MinDurationMs)
	}
	if filter.MaxDurationMs == nil || *filter.MaxDurationMs != 200 {
		t.Fatalf("MaxDurationMs = %v, want 200", filter.MaxDurationMs)
	}
	if filter.MinBytes == nil || *filter.MinBytes != 100 {
		t.Fatalf("MinBytes = %v, want 100", filter.MinBytes)
	}
	if filter.MaxBytes == nil || *filter.MaxBytes != 900 {
		t.Fatalf("MaxBytes = %v, want 900", filter.MaxBytes)
	}
	if filter.ErrorContains != "timeout" {
		t.Fatalf("ErrorContains = %q, want timeout", filter.ErrorContains)
	}
	if filter.SortBy != "duration" || filter.SortDir != "desc" {
		t.Fatalf("sort = %s/%s, want duration/desc", filter.SortBy, filter.SortDir)
	}
}

func writeArchiveTransactionsForTest(t *testing.T, transactions []TransactionEvent) *storage.RunArchiveRecord {
	t.Helper()
	archiveDir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-query",
		TestName:              "Query Test",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount:      int64(len(transactions)),
	}
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}

	txFile, err := os.Create(filepath.Join(archiveDir, manifest.TransactionsFile))
	if err != nil {
		t.Fatalf("Create transactions file returned error: %v", err)
	}
	encoder := json.NewEncoder(txFile)
	for _, transaction := range transactions {
		if err := encoder.Encode(transaction); err != nil {
			t.Fatalf("Encode transaction returned error: %v", err)
		}
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("Close transactions file returned error: %v", err)
	}
	return &storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath}
}
