package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/xitongsys/parquet-go/parquet"
)

func TestBrowserResourceControllerWriterPhysicalParityUsesExactJSONLPresence(t *testing.T) {
	root := t.TempDir()
	manifest := &RunArchiveManifest{TransactionsFile: "transactions.jsonl", TransactionsParquetFile: "transactions.parquet", ResourceSamplesFile: "resources.jsonl", ResourceSamplesParquetFile: "resources.parquet", IntervalSummariesFile: "summaries.jsonl", IntervalSummariesParquetFile: "summaries.parquet"}
	var source bytes.Buffer
	for _, row := range []string{`{}`, `{"network_entries":null}`, `{"network_entries":[]}`, `{"network_entries":[],"snapshot":{"network_entries":[{"duration_ms":0}]}}`} {
		source.WriteString(row + "\n")
		manifest.TransactionCount++
	}
	native, err := json.Marshal(resourcePageTestEvidence())
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 80; index++ {
		source.WriteString(fmt.Sprintf(`{"test_id":"run-1","timestamp":"2023-11-14T22:13:20.123456789Z","action_name":"load","page_url":"https://example.test/page","client_metrics":{"device_profile":"Desktop","device_platform":"darwin","browser_engine":"chromium"},"network_entries":[{"url":"https://example.test/data","status_code":0,"duration_ms":0,"bytes_received":0,"browser_evidence":%s}],"request_body":"PRIVATE-NOT-PROJECTED"}`+"\n", native))
		manifest.TransactionCount++
	}
	for name, raw := range map[string][]byte{manifest.TransactionsFile: source.Bytes(), manifest.ResourceSamplesFile: nil, manifest.IntervalSummariesFile: nil} {
		if err := os.WriteFile(filepath.Join(root, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeArchiveParquetFiles(root, manifest); err != nil {
		t.Fatal(err)
	}
	input, err := os.ReadFile(filepath.Join(root, manifest.TransactionsFile))
	if err != nil || !bytes.Equal(input, source.Bytes()) {
		t.Fatal("physical conversion changed original JSONL")
	}
	q := resourcePageTestQuery(t, nil)
	jsonl, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(input), "run-1", q, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, manifest.TransactionsParquetFile)
	physical, err := resourcePhysicalScan(t, path, q, nil)
	if err != nil {
		t.Fatal(err)
	}
	if physical.Transactions != 84 || physical.Retained != 81 || physical.Matched != 81 || !physical.SourcePresent || !physical.HasNext || !reflect.DeepEqual(physical.Items, jsonl.Items) {
		t.Fatal("actual controller writer changed retained presence, zero/false, source precision or selection")
	}
	next, err := resourcePhysicalScan(t, path, q, &physical.Last)
	if err != nil {
		t.Fatal(err)
	}
	nextJSONL, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(input), "run-1", q, &jsonl.Last)
	if err != nil {
		t.Fatal(err)
	}
	if next.HasNext || len(next.Items) != 31 || !reflect.DeepEqual(next.Items, nextJSONL.Items) {
		t.Fatal("actual controller writer physical continuation changed observations")
	}
}

func TestBrowserResourceControllerWriterSingleTransactionProjectionHeaderIsBounded(t *testing.T) {
	root := t.TempDir()
	manifest := &RunArchiveManifest{TransactionsFile: "transactions.jsonl", TransactionsParquetFile: "transactions.parquet", ResourceSamplesFile: "resources.jsonl", ResourceSamplesParquetFile: "resources.parquet", IntervalSummariesFile: "summaries.jsonl", IntervalSummariesParquetFile: "summaries.parquet", TransactionCount: 1}
	entries := make([]map[string]any, 80)
	for i := range entries {
		evidence := resourcePageTestEvidence()
		evidence.RequestID = fmt.Sprintf("%s-request-%d", evidence.SourceGeneration, i)
		entries[i] = map[string]any{"url": "https://example.test/resource", "duration_ms": 0, "browser_evidence": evidence}
	}
	raw, err := json.Marshal(map[string]any{"test_id": "run-1", "timestamp": "2026-10-05T13:02:50.297486-04:00", "network_entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	source := append(append([]byte(nil), raw...), '\n')
	for name, content := range map[string][]byte{manifest.TransactionsFile: source, manifest.ResourceSamplesFile: nil, manifest.IntervalSummariesFile: nil} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeArchiveParquetFiles(root, manifest); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, manifest.TransactionsParquetFile)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	physical, err := openBrowserResourcePhysicalParquet(context.Background(), file, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, column := range physical.footer.RowGroups[0].Columns {
		metadata := column.MetaData
		if len(metadata.PathInSchema) != 1 || metadata.PathInSchema[0] != "resource_projection_json" {
			continue
		}
		found = true
		if stats := metadata.Statistics; stats != nil && (len(stats.Min)+len(stats.Max)+len(stats.MinValue)+len(stats.MaxValue) != 0) {
			t.Fatal("opaque resource JSON retained lexical footer statistics")
		}
		// Decode this controlled producer header with a larger test-only bound to
		// report the actual header size. Untrusted production admission stays 16 KiB.
		if metadata.TotalCompressedSize < 1 || metadata.TotalCompressedSize > 8<<20 {
			t.Fatal("controlled controller column exceeds test bounds")
		}
		encoded := make([]byte, int(metadata.TotalCompressedSize))
		if _, err := file.ReadAt(encoded, metadata.DataPageOffset); err != nil {
			t.Fatal(err)
		}
		decoder, transport := resourceParquetProtocol(encoded, resourceParquetDecodedPageLimit)
		header := parquet.NewPageHeader()
		if err := header.Read(context.Background(), decoder); err != nil || header.DataPageHeader == nil {
			t.Fatal("controlled actual projection page header was unavailable")
		}
		headerBytes := len(encoded) - transport.Len()
		stats := header.DataPageHeader.Statistics
		statisticsBytes := 0
		if stats != nil {
			statisticsBytes = len(stats.Min) + len(stats.Max) + len(stats.MinValue) + len(stats.MaxValue)
		}
		t.Logf("actual single-transaction projection entries=80 projection_bytes=%d page_header_bytes=%d string_statistics_bytes=%d admitted_header_limit=%d", len(protocol.BrowserResourceArchiveProjection(raw)), headerBytes, statisticsBytes, resourceParquetHeaderLimit)
		if headerBytes > resourceParquetHeaderLimit || statisticsBytes != 0 {
			t.Fatal("opaque resource JSON created unbounded lexical page statistics")
		}
	}
	if !found {
		t.Fatal("actual controller projection column was not published")
	}
	q := resourcePageTestQuery(t, nil)
	jsonl, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(source), "run-1", q, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := resourcePhysicalScan(t, path, q, nil)
	if err != nil || !reflect.DeepEqual(first.Items, jsonl.Items) || len(first.Items) != 50 || !first.HasNext || first.Retained != 80 {
		t.Fatalf("single-transaction physical projection changed retained observations: %v", err)
	}
	second, err := resourcePhysicalScan(t, path, q, &first.Last)
	if err != nil || len(second.Items) != 30 || second.HasNext {
		t.Fatalf("single-transaction physical continuation failed: %v", err)
	}
	original, err := os.ReadFile(filepath.Join(root, manifest.TransactionsFile))
	if err != nil || !bytes.Equal(original, source) {
		t.Fatal("bounded-statistics conversion changed original source bytes")
	}
}
