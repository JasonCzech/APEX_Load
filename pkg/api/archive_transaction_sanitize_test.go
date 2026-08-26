package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRunArchiveWriterScrubsNamedSecretsAndTypedEvidence(t *testing.T) {
	var output bytes.Buffer
	w := &runArchiveWriter{
		manifest:  RunArchiveManifest{TestID: "run-current", TestName: "Current"},
		txEncoder: json.NewEncoder(&output),
	}
	if err := w.RecordResult(&protocol.Result{
		Success:             true,
		BusinessTransaction: true,
		ActionName:          "Checkout",
		Extractions: map[string]string{
			"jms_settlement_token": "x",
			"soap_operation":       "CreateOrder",
		},
		ProtocolEvidence: map[string]any{
			"event": struct {
				PrivateKey string `json:"private_key"`
				Status     string `json:"status"`
			}{PrivateKey: "y", Status: "ok"},
		},
	}); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	var event TransactionEvent
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode transaction: %v", err)
	}
	if event.Extractions["jms_settlement_token"] != scrub.Redacted || event.Extractions["soap_operation"] != "CreateOrder" {
		t.Fatalf("archived extractions = %#v", event.Extractions)
	}
	if !event.BusinessTransaction || event.ActionName != "Checkout" {
		t.Fatalf("archived transaction provenance = %#v", event)
	}
	encoded, _ := json.Marshal(event.ProtocolEvidence)
	if strings.Contains(string(encoded), `"private_key":"y"`) || !strings.Contains(string(encoded), `"status":"ok"`) {
		t.Fatalf("archived evidence = %s", encoded)
	}
}

func TestCombinedResultSinkArchivesSyntheticTransactionWithoutDuplicatingActionResult(t *testing.T) {
	var output bytes.Buffer
	writer := &runArchiveWriter{
		manifest:  RunArchiveManifest{TestID: "run-synthetic", TestName: "Synthetic"},
		txEncoder: json.NewEncoder(&output),
	}
	sink := combinedResultSink{writer}
	ordinary := &protocol.Result{ActionName: "HTTP request", Success: true, StartTime: time.Now()}
	synthetic := &protocol.Result{ActionName: "Checkout flow", BusinessTransaction: true, Success: true, StartTime: time.Now()}
	if err := sink.RecordResult(ordinary); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	if err := sink.RecordTransaction(synthetic); err != nil {
		t.Fatalf("RecordTransaction: %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	var events []TransactionEvent
	for decoder.More() {
		var event TransactionEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode transaction: %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 2 || writer.manifest.TransactionCount != 2 {
		t.Fatalf("archive events/count = %#v/%d, want one action plus one logical transaction", events, writer.manifest.TransactionCount)
	}
	if events[0].ActionName != "HTTP request" || events[0].BusinessTransaction {
		t.Fatalf("ordinary action event = %#v", events[0])
	}
	if events[1].ActionName != "Checkout flow" || !events[1].BusinessTransaction {
		t.Fatalf("synthetic transaction event = %#v", events[1])
	}
}

func TestLegacyArchiveTransactionsAreSanitizedForLoadAndDirectStream(t *testing.T) {
	dir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:           archiveSchemaVersion,
		TestID:                  "legacy-run",
		TransactionsFile:        "transactions.jsonl",
		TransactionsParquetFile: "transactions.parquet",
	}
	transaction := TransactionEvent{
		TestID:      manifest.TestID,
		Success:     true,
		PageURL:     "https://user:short@example.test/?csrf_token=c",
		Extractions: map[string]string{"client%5Fsecret": "x", "soap_operation": "Create"},
		ProtocolEvidence: map[string]any{
			"events": []any{map[string]any{"settlement_token": "y", "status": "ok"}},
		},
		Snapshot: &protocol.ErrorSnapshot{
			RequestHeaders: map[string]string{"Authorization": "Bearer z"},
			RequestBody:    "private_key=q",
		},
	}
	writeJSONL(t, filepath.Join(dir, manifest.TransactionsFile), transaction)
	writeJSONL(t, filepath.Join(dir, runArchiveManifestFileName), manifest)
	if err := os.WriteFile(filepath.Join(dir, manifest.TransactionsParquetFile), []byte("raw parquet x y z q"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: dir, ManifestPath: filepath.Join(dir, runArchiveManifestFileName)}

	items, total, err := loadArchiveTransactions(record, transactionEventFilter{}, archivePage{Limit: 10})
	if err != nil {
		t.Fatalf("loadArchiveTransactions: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("transactions total=%d len=%d", total, len(items))
	}
	assertSanitizedLegacyTransaction(t, items[0])

	var direct bytes.Buffer
	stats, err := writeSanitizedArchiveTransactions(context.Background(), record, &manifest, &direct)
	if err != nil {
		t.Fatalf("writeSanitizedArchiveTransactions: %v", err)
	}
	if stats.Count != 1 || stats.SizeBytes != int64(direct.Len()) {
		t.Fatalf("stats = %#v, direct bytes=%d", stats, direct.Len())
	}
	hash := sha256.Sum256(direct.Bytes())
	if stats.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("stats hash = %q, want %x", stats.SHA256, hash)
	}
	if strings.Contains(direct.String(), `"client%5Fsecret":"x"`) || strings.Contains(direct.String(), `"settlement_token":"y"`) || strings.Contains(direct.String(), "user:short") {
		t.Fatalf("direct sanitized JSONL leaked: %s", direct.String())
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := writeSanitizedArchiveTransactions(canceled, record, &manifest, io.Discard); err == nil {
		t.Fatal("canceled legacy rewrite unexpectedly succeeded")
	}
}

func TestLegacyArchiveZIPRewritesTransactionsAndOmitsUnsafeParquet(t *testing.T) {
	dir := t.TempDir()
	rawTransaction := TransactionEvent{
		TestID:      "legacy-zip",
		Success:     true,
		Extractions: map[string]string{"session_id": "short", "soap_operation": "Submit"},
	}
	var rawJSONL bytes.Buffer
	if err := json.NewEncoder(&rawJSONL).Encode(rawTransaction); err != nil {
		t.Fatal(err)
	}
	manifest := RunArchiveManifest{
		SchemaVersion:           archiveSchemaVersion,
		TestID:                  "legacy-zip",
		TransactionsFile:        "transactions.jsonl",
		TransactionsParquetFile: "transactions.parquet",
		TransactionCount:        99,
		Artifacts: []RunArchiveArtifact{
			{Name: "transactions.jsonl", SizeBytes: int64(rawJSONL.Len()), SHA256: sha256BytesHex(rawJSONL.Bytes())},
			{Name: "transactions.parquet", SizeBytes: 12, SHA256: "unsafe"},
		},
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.TransactionsFile), rawJSONL.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.TransactionsParquetFile), []byte("short secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, runArchiveManifestFileName), manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: dir, ManifestPath: filepath.Join(dir, runArchiveManifestFileName)}
	var archive bytes.Buffer
	if err := zipSanitizedRunArchiveContext(context.Background(), record, &manifest, &archive); err != nil {
		t.Fatalf("zipSanitizedRunArchiveContext: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("open ZIP: %v", err)
	}
	files := make(map[string][]byte)
	for _, entry := range reader.File {
		opened, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(io.LimitReader(opened, maxArchiveJSONLRecordBytes+1))
		closeErr := opened.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("read %s: %v / %v", entry.Name, err, closeErr)
		}
		files[entry.Name] = data
	}
	if _, exists := files[manifest.TransactionsParquetFile]; exists {
		t.Fatal("safe ZIP retained unsafe legacy transaction parquet")
	}
	txData := files[manifest.TransactionsFile]
	if strings.Contains(string(txData), `"session_id":"short"`) || !strings.Contains(string(txData), scrub.Redacted) {
		t.Fatalf("safe ZIP transactions = %s", txData)
	}
	var safeManifest RunArchiveManifest
	if err := json.Unmarshal(files[runArchiveManifestFileName], &safeManifest); err != nil {
		t.Fatalf("decode safe manifest: %v", err)
	}
	if safeManifest.TransactionsParquetFile != "" || safeManifest.TransactionCount != 1 {
		t.Fatalf("safe manifest parquet/count = %q/%d", safeManifest.TransactionsParquetFile, safeManifest.TransactionCount)
	}
	if len(safeManifest.Artifacts) != 1 || safeManifest.Artifacts[0].Name != manifest.TransactionsFile ||
		safeManifest.Artifacts[0].SizeBytes != int64(len(txData)) ||
		safeManifest.Artifacts[0].SHA256 != sha256BytesHex(txData) {
		t.Fatalf("safe manifest transaction descriptor = %#v", safeManifest.Artifacts)
	}
}

func TestLegacyDirectArtifactEndpointCannotBypassTransactionSanitization(t *testing.T) {
	dir := t.TempDir()
	manifest := RunArchiveManifest{
		SchemaVersion:           archiveSchemaVersion,
		TestID:                  "legacy-direct",
		TestName:                "Legacy Direct",
		TransactionsFile:        "transactions.jsonl",
		TransactionsParquetFile: "transactions.parquet",
	}
	writeJSONL(t, filepath.Join(dir, manifest.TransactionsFile), TransactionEvent{
		TestID:      manifest.TestID,
		Success:     true,
		Extractions: map[string]string{"private_key": "tiny"},
	})
	if err := os.WriteFile(filepath.Join(dir, manifest.TransactionsParquetFile), []byte("tiny"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestData, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(dir, runArchiveManifestFileName)
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{
		TestID: manifest.TestID, ArchivePath: dir, ManifestPath: manifestPath, CompletedAt: time.Now(),
	}
	server := NewServer(ServerConfig{})
	server.runner.mu.Lock()
	server.runner.tests[manifest.TestID] = &ManagedTest{ID: manifest.TestID, Name: manifest.TestName, Status: "COMPLETE", Archive: record}
	server.runner.archives[manifest.TestID] = record
	server.runner.mu.Unlock()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/legacy-direct/archive/artifacts/transactions.jsonl", nil)
	request.SetPathValue("id", manifest.TestID)
	request.SetPathValue("name", manifest.TransactionsFile)
	response := httptest.NewRecorder()
	server.handleDownloadRunArchiveArtifact(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("direct transaction status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"private_key":"tiny"`) || !strings.Contains(response.Body.String(), scrub.Redacted) {
		t.Fatalf("direct transaction download leaked: %s", response.Body.String())
	}

	parquetRequest := httptest.NewRequest(http.MethodGet, "/api/v1/tests/legacy-direct/archive/artifacts/transactions.parquet", nil)
	parquetRequest.SetPathValue("id", manifest.TestID)
	parquetRequest.SetPathValue("name", manifest.TransactionsParquetFile)
	parquetResponse := httptest.NewRecorder()
	server.handleDownloadRunArchiveArtifact(parquetResponse, parquetRequest)
	if parquetResponse.Code != http.StatusNotFound {
		t.Fatalf("legacy parquet status=%d body=%s", parquetResponse.Code, parquetResponse.Body.String())
	}
}

func assertSanitizedLegacyTransaction(t *testing.T, item TransactionEvent) {
	t.Helper()
	if item.Extractions["client%5Fsecret"] != scrub.Redacted || item.Extractions["soap_operation"] != "Create" {
		t.Fatalf("loaded extractions = %#v", item.Extractions)
	}
	encoded, _ := json.Marshal(item)
	text := string(encoded)
	for _, leaked := range []string{`"settlement_token":"y"`, "user:short", "Bearer z", "private_key=q"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("loaded transaction leaked %q: %s", leaked, text)
		}
	}
}

func TestSanitizeArchivedTransactionPreservesReportIdentityNames(t *testing.T) {
	const tokenLikeName = "AbCdEfGh1234567890ijklMN"
	item := &TransactionEvent{
		TestName:            "test-" + tokenLikeName,
		Group:               "group-" + tokenLikeName,
		ActionName:          "transaction-" + tokenLikeName,
		BusinessTransaction: true,
		SourceActionName:    "action-" + tokenLikeName,
		SourceTransaction:   "source-transaction-" + tokenLikeName,
		ErrorMessage:        "request failed with token " + tokenLikeName,
	}

	sanitizeArchivedTransaction(item)

	for name, value := range map[string]string{
		"group":              item.Group,
		"transaction":        item.ActionName,
		"source transaction": item.SourceTransaction,
	} {
		if !strings.Contains(value, tokenLikeName) || strings.Contains(value, "[REDACTED]") {
			t.Fatalf("%s identity = %q, want authored name preserved", name, value)
		}
	}
	if item.ErrorMessage != "request failed with token "+tokenLikeName {
		t.Fatalf("ordinary error evidence was rewritten: %q", item.ErrorMessage)
	}
	if item.TestName != "test-"+tokenLikeName || item.SourceActionName != "action-"+tokenLikeName {
		t.Fatalf("ordinary metadata was rewritten: test=%q source_action=%q", item.TestName, item.SourceActionName)
	}

	ordinary := &TransactionEvent{ActionName: "action-" + tokenLikeName}
	sanitizeArchivedTransaction(ordinary)
	if ordinary.ActionName != "action-"+tokenLikeName {
		t.Fatalf("ordinary archived action was rewritten: %q", ordinary.ActionName)
	}

	structured := &TransactionEvent{
		Group:               "group token=abc123",
		ActionName:          "transaction token=abc123",
		BusinessTransaction: true,
		SourceTransaction:   "source token=abc123",
	}
	sanitizeArchivedTransaction(structured)
	if structured.Group != "group token=abc123" || structured.ActionName != "transaction token=abc123" || structured.SourceTransaction != "source token=abc123" {
		t.Fatalf("structured-looking archive identities were rewritten: %#v", structured)
	}
}

func TestArchiveTransactionCSVIncludesBusinessTransactionProvenance(t *testing.T) {
	var output bytes.Buffer
	if err := writeArchiveTransactionsCSV([]TransactionEvent{{
		ActionName:          "Checkout",
		BusinessTransaction: true,
	}}, &output); err != nil {
		t.Fatalf("writeArchiveTransactionsCSV: %v", err)
	}
	rows, err := csv.NewReader(&output).ReadAll()
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("CSV rows = %#v", rows)
	}
	column := -1
	for index, header := range rows[0] {
		if header == "business_transaction" {
			column = index
			break
		}
	}
	if column < 0 || rows[1][column] != "true" {
		t.Fatalf("CSV transaction provenance = %#v", rows)
	}
}

func TestCompactArchiveAnalysisTransactionCarriesBusinessTransactionProvenance(t *testing.T) {
	transaction := compactArchiveAnalysisTransaction(TransactionEvent{
		ActionName:          "Checkout",
		BusinessTransaction: true,
	})
	if !transaction.BusinessTransaction {
		t.Fatalf("compacted transaction provenance = %#v", transaction)
	}
}

func TestIntervalSummaryTransactionNamePreservesAuthoredIdentity(t *testing.T) {
	const transactionName = "checkout-AbCdEfGh1234567890ijklMN"
	if got := intervalTransactionName(transactionName); got != transactionName {
		t.Fatalf("interval transaction identity = %q, want %q", got, transactionName)
	}
}
