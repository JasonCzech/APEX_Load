package generatorrpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/jasonczech/apexload/pkg/protocol"
	parquetlocal "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/reader"
)

func TestBrowserResourceGeneratorWriterPhysicalParityUsesExactJSONLPresence(t *testing.T) {
	root := t.TempDir()
	manifest := &archiveManifest{TransactionsFile: "transactions.jsonl", TransactionsParquetFile: "transactions.parquet", ResourceSamplesFile: "resources.jsonl", ResourceSamplesParquetFile: "resources.parquet", IntervalSummariesFile: "summaries.jsonl", IntervalSummariesParquetFile: "summaries.parquet"}
	generation := "0123456789abcdef0123456789abcdef"
	zero := 0.0
	body := int64(0)
	no := false
	evidence := &protocol.BrowserResourceEvidence{Version: 2, SourceGeneration: generation, RequestID: generation + "-request-1", ObservedAt: time.Unix(1700000000, 123456789).UTC(), TimingSource: "playwright", State: "finished", Phases: []protocol.BrowserResourcePhase{{Name: "response", StartMS: &zero, EndMS: &zero}}, Bytes: protocol.BrowserResourceBytes{ResponseEncodedBody: &body}, ServiceWorker: &no}
	native, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	rawRows := [][]byte{[]byte(`{}`), []byte(`{"network_entries":null}`), []byte(`{"network_entries":[]}`), []byte(`{"network_entries":[],"snapshot":{"network_entries":[{"duration_ms":0}]}}`)}
	for index := 0; index < 80; index++ {
		rawRows = append(rawRows, []byte(fmt.Sprintf(`{"test_id":"run-1","timestamp":"2023-11-14T22:13:20.123456789Z","action_name":"load","page_url":"https://example.test/page","client_metrics":{"device_profile":"Desktop","device_platform":"darwin","browser_engine":"chromium"},"network_entries":[{"url":"https://example.test/data","status_code":0,"duration_ms":0,"bytes_received":0,"browser_evidence":%s}],"request_body":"PRIVATE-NOT-PROJECTED"}`, native)))
	}
	var source bytes.Buffer
	for _, raw := range rawRows {
		source.Write(raw)
		source.WriteByte('\n')
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
	file, err := parquetlocal.NewLocalFileReader(filepath.Join(root, manifest.TransactionsParquetFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	physical, err := reader.NewParquetReader(file, new(parquetTransactionEvent), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer physical.ReadStop()
	if physical.GetNumRows() != int64(len(rawRows)) {
		t.Fatal("actual generator writer changed row count")
	}
	rows := make([]parquetTransactionEvent, len(rawRows))
	if err := physical.Read(&rows); err != nil {
		t.Fatal(err)
	}
	for index, row := range rows {
		expected := protocol.BrowserResourceArchiveProjection(rawRows[index])
		if !bytes.Equal([]byte(row.ResourceProjectionJSON), expected) || bytes.Contains([]byte(row.ResourceProjectionJSON), []byte("PRIVATE")) {
			t.Fatalf("actual generator writer changed exact source presence or private projection at row%d", index)
		}
		minimal, reason, err := protocol.DecodeBrowserResourceArchiveProjection([]byte(row.ResourceProjectionJSON))
		if err != nil || reason != "" || len(minimal) == 0 {
			t.Fatal("physical generator projection was not admitted")
		}
		if index >= 4 && (row.PageURL != "https://example.test/page" || row.ClientMetricsJSON == "null" || row.NetworkEntriesJSON == "null" || row.TimestampRFC3339 != "2023-11-14T22:13:20.123456789Z") {
			t.Fatal("actual generator physical columns lost resource/client/page/time context")
		}
	}
}

func TestBrowserResourceGeneratorWriterSingleTransactionProjectionHeaderIsBounded(t *testing.T) {
	root := t.TempDir()
	manifest := &archiveManifest{TransactionsFile: "transactions.jsonl", TransactionsParquetFile: "transactions.parquet", ResourceSamplesFile: "resources.jsonl", ResourceSamplesParquetFile: "resources.parquet", IntervalSummariesFile: "summaries.jsonl", IntervalSummariesParquetFile: "summaries.parquet"}
	entries := make([]map[string]any, 80)
	generation := "0123456789abcdef0123456789abcdef"
	zero, body, no := 0.0, int64(0), false
	for index := range entries {
		evidence := &protocol.BrowserResourceEvidence{Version: 2, SourceGeneration: generation, RequestID: fmt.Sprintf("%s-request-%d", generation, index), ObservedAt: time.Unix(1700000000, 123456789).UTC(), TimingSource: "playwright", State: "finished", Phases: []protocol.BrowserResourcePhase{{Name: "response", StartMS: &zero, EndMS: &zero}}, Bytes: protocol.BrowserResourceBytes{ResponseEncodedBody: &body}, ServiceWorker: &no}
		entries[index] = map[string]any{"url": fmt.Sprintf("https://example.test/resource/%d", index), "duration_ms": 0, "browser_evidence": evidence}
	}
	raw, err := json.Marshal(map[string]any{"test_id": "run-1", "timestamp": "2026-10-05T13:02:50.297486-04:00", "network_entries": entries, "request_body": "PRIVATE-NOT-PROJECTED"})
	if err != nil {
		t.Fatal(err)
	}
	if len(protocol.BrowserResourceArchiveProjection(raw)) <= 16<<10 {
		t.Fatal("single-transaction fixture does not exercise the header statistics boundary")
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
	input, err := parquetlocal.NewLocalFileReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	physical, err := reader.NewParquetReader(input, new(parquetTransactionEvent), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer physical.ReadStop()
	if physical.GetNumRows() != 1 {
		t.Fatal("actual generator writer changed single-transaction row count")
	}
	rows := make([]parquetTransactionEvent, 1)
	if err := physical.Read(&rows); err != nil || !bytes.Equal([]byte(rows[0].ResourceProjectionJSON), protocol.BrowserResourceArchiveProjection(raw)) || bytes.Contains([]byte(rows[0].ResourceProjectionJSON), []byte("PRIVATE")) {
		t.Fatal("actual generator single-transaction physical projection changed source presence or disclosure")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < 12 {
		t.Fatal("controlled generator file was unavailable")
	}
	trailer := make([]byte, 8)
	if _, err := file.ReadAt(trailer, info.Size()-8); err != nil || string(trailer[4:]) != "PAR1" {
		t.Fatal("controlled generator physical trailer was unavailable")
	}
	footerSize := int64(binary.LittleEndian.Uint32(trailer))
	if footerSize < 1 || footerSize > 8<<20 || footerSize > info.Size()-12 {
		t.Fatal("controlled generator footer exceeds test bounds")
	}
	footerBytes := make([]byte, int(footerSize))
	if _, err := file.ReadAt(footerBytes, info.Size()-8-footerSize); err != nil {
		t.Fatal(err)
	}
	footer := parquet.NewFileMetaData()
	footerTransport := &thrift.TMemoryBuffer{Buffer: bytes.NewBuffer(footerBytes)}
	if err := footer.Read(context.Background(), thrift.NewTCompactProtocolConf(footerTransport, &thrift.TConfiguration{MaxMessageSize: 8 << 20})); err != nil || len(footer.RowGroups) != 1 {
		t.Fatal("controlled generator wire footer was unavailable")
	}
	// The SDK reader renames its own Footer paths to Go field names. Inspect
	// an independent wire decode so this proof uses actual physical identities.
	found := false
	for _, column := range footer.RowGroups[0].Columns {
		metadata := column.MetaData
		if len(metadata.PathInSchema) != 1 || metadata.PathInSchema[0] != "resource_projection_json" {
			continue
		}
		found = true
		if stats := metadata.Statistics; stats != nil && (len(stats.Min)+len(stats.Max)+len(stats.MinValue)+len(stats.MaxValue) != 0) {
			t.Fatal("opaque resource JSON retained lexical footer statistics")
		}
		if metadata.TotalCompressedSize < 1 || metadata.TotalCompressedSize > 8<<20 {
			t.Fatal("controlled generator column exceeds test bounds")
		}
		encoded := make([]byte, int(metadata.TotalCompressedSize))
		if _, err := file.ReadAt(encoded, metadata.DataPageOffset); err != nil {
			t.Fatal(err)
		}
		transport := &thrift.TMemoryBuffer{Buffer: bytes.NewBuffer(encoded)}
		decoder := thrift.NewTCompactProtocolConf(transport, &thrift.TConfiguration{MaxMessageSize: 8 << 20})
		header := parquet.NewPageHeader()
		if err := header.Read(context.Background(), decoder); err != nil || header.DataPageHeader == nil {
			t.Fatal("controlled generator projection page header was unavailable")
		}
		headerBytes := len(encoded) - transport.Len()
		statsBytes := 0
		if stats := header.DataPageHeader.Statistics; stats != nil {
			statsBytes = len(stats.Min) + len(stats.Max) + len(stats.MinValue) + len(stats.MaxValue)
		}
		t.Logf("actual generator single-transaction projection entries=80 projection_bytes=%d page_header_bytes=%d string_statistics_bytes=%d", len(rows[0].ResourceProjectionJSON), headerBytes, statsBytes)
		if headerBytes > 16<<10 || statsBytes != 0 {
			t.Fatal("opaque generator resource JSON created unbounded lexical page statistics")
		}
	}
	if !found {
		t.Fatal("actual generator projection column was not published")
	}
	original, err := os.ReadFile(filepath.Join(root, manifest.TransactionsFile))
	if err != nil || !bytes.Equal(original, source) {
		t.Fatal("bounded-statistics generator conversion changed original source bytes")
	}
}
