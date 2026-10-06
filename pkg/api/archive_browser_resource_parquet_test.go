package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

type resourcePhysicalProjectionFixture struct {
	Projection string `parquet:"name=resource_projection_json, type=BYTE_ARRAY, convertedtype=UTF8"`
}
type resourcePhysicalLegacyFixture struct {
	Timestamp int64  `parquet:"name=timestamp_unix_ms, type=INT64"`
	Action    string `parquet:"name=action_name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Entries   string `parquet:"name=network_entries_json, type=BYTE_ARRAY, convertedtype=UTF8"`
	Snapshot  string `parquet:"name=snapshot_json, type=BYTE_ARRAY, convertedtype=UTF8"`
}

func resourcePhysicalWrite(t *testing.T, obj any, rows []any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transactions.parquet")
	file, err := local.NewLocalFileWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	output, err := writer.NewParquetWriter(file, obj, 1)
	if err != nil {
		t.Fatal(err)
	}
	output.CompressionType = parquet.CompressionCodec_SNAPPY
	output.PageSize = 256
	output.RowGroupSize = 4096
	for _, row := range rows {
		if err := output.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := output.WriteStop(); err != nil {
		t.Fatal(err)
	}
	return path
}
func resourcePhysicalScan(t *testing.T, path string, q BrowserResourcePageQuery, after *browserResourceSortKey) (browserResourceScanResult, error) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	return scanBrowserResourceParquet(context.Background(), file, "run-1", q, after)
}
func TestBrowserResourcePhysicalParquetMatchesRawWireAcrossPagesAndContinuation(t *testing.T) {
	rows := []any{}
	var jsonl bytes.Buffer
	for i := 0; i < 80; i++ {
		native, _ := json.Marshal(resourcePageTestEvidence())
		at := time.Unix(1700000000, int64(i)+123456789).UTC().Format(time.RFC3339Nano)
		source := []byte(fmt.Sprintf(`{"test_id":"run-1","timestamp":%q,"action_name":"load","group":"workers","region":"local","page_url":"https://example.test/page","client_metrics":{"device_profile":"Desktop","device_platform":"darwin","browser_engine":"chromium"},"network_entries":[{"url":"https://example.test/data","duration_ms":%d,"status_code":0,"bytes_received":0,"browser_evidence":%s}],"request_body":"PRIVATE-NOT-SELECTED"}`, at, i%3, native))
		jsonl.Write(source)
		jsonl.WriteByte('\n')
		rows = append(rows, resourcePhysicalProjectionFixture{Projection: string(protocol.BrowserResourceArchiveProjection(source))})
	}
	path := resourcePhysicalWrite(t, new(resourcePhysicalProjectionFixture), rows)
	for _, direction := range []string{"asc", "desc"} {
		q := resourcePageTestQuery(t, url.Values{"sort_by": {"duration_ms"}, "sort_dir": {direction}})
		physical, err := resourcePhysicalScan(t, path, q, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(jsonl.Bytes()), "run-1", q, nil)
		if err != nil {
			t.Fatal(err)
		}
		if physical.Transactions != 80 || physical.Retained != 80 || len(physical.Items) != 50 || !physical.HasNext || !reflect.DeepEqual(physical.Items, raw.Items) || physical.TimestampPrecision != "rfc3339_nanosecond" {
			t.Fatal("physical pages changed retained wire/selection population")
		}
		next, err := resourcePhysicalScan(t, path, q, &physical.Last)
		if err != nil {
			t.Fatal(err)
		}
		nextRaw, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(jsonl.Bytes()), "run-1", q, &raw.Last)
		if err != nil {
			t.Fatal(err)
		}
		if len(next.Items) != 30 || next.HasNext || !reflect.DeepEqual(next.Items, nextRaw.Items) || next.SHA256 != physical.SHA256 {
			t.Fatal("physical continuation lost observations or source identity")
		}
	}
}
func TestBrowserResourcePhysicalProjectionKeepsAbsentNullEmptyAndZeroDistinct(t *testing.T) {
	cases := []struct {
		name, source string
		present      bool
		retained     int64
	}{
		{"absent", `{}`, false, 0},
		{"null", `{"network_entries":null}`, false, 0},
		{"both_null", `{"network_entries":null,"snapshot":{"network_entries":null}}`, false, 0},
		{"empty", `{"network_entries":[]}`, true, 0},
		{"empty_null_fallback", `{"network_entries":[],"snapshot":{"network_entries":null}}`, true, 0},
		{"null_empty_fallback", `{"network_entries":null,"snapshot":{"network_entries":[]}}`, true, 0},
		{"zero", `{"network_entries":[{"duration_ms":0,"status_code":0,"bytes_received":0}]}`, true, 1},
		{"snapshot", `{"network_entries":[],"snapshot":{"network_entries":[{"duration_ms":0}]}}`, true, 1},
		{"null_snapshot", `{"network_entries":null,"snapshot":{"network_entries":[{"duration_ms":0}]}}`, true, 1},
		{"ordinary_priority", `{"network_entries":[{"duration_ms":0}],"snapshot":{"network_entries":null}}`, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := resourcePhysicalWrite(t, new(resourcePhysicalProjectionFixture), []any{resourcePhysicalProjectionFixture{Projection: string(protocol.BrowserResourceArchiveProjection([]byte(tc.source)))}})
			q := resourcePageTestQuery(t, nil)
			physical, err := resourcePhysicalScan(t, path, q, nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := scanBrowserResourceJSONL(context.Background(), strings.NewReader(tc.source+"\n"), "run-1", q, nil)
			if err != nil {
				t.Fatal(err)
			}
			if physical.SourcePresent != tc.present || raw.SourcePresent != tc.present || physical.Retained != tc.retained || raw.Retained != tc.retained || physical.Matched != raw.Matched || !reflect.DeepEqual(physical.Items, raw.Items) {
				t.Fatalf("physical projection invented missing capture or lost observed zero: physical=%+v raw=%+v", physical, raw)
			}
		})
	}
}
func TestBrowserResourceLegacyPhysicalDictionaryAndMillisecondLimit(t *testing.T) {
	rows := []any{}
	for i := 0; i < 80; i++ {
		rows = append(rows, resourcePhysicalLegacyFixture{Timestamp: 1700000000123, Action: fmt.Sprintf("action%d", i%3), Entries: `[{"duration_ms":0,"bytes_received":0}]`, Snapshot: "null"})
	}
	path := resourcePhysicalWrite(t, new(resourcePhysicalLegacyFixture), rows)
	result, err := resourcePhysicalScan(t, path, resourcePageTestQuery(t, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Retained != 80 || len(result.Items) != 50 || result.Items[0].Timestamp == nil || result.Items[0].Timestamp.Nanosecond() != 123000000 || result.TimestampPrecision != "unix_millisecond" {
		t.Fatal("legacy dictionary/millisecond projection changed")
	}
	for _, value := range []string{"null", "[]", ""} {
		path := resourcePhysicalWrite(t, new(resourcePhysicalLegacyFixture), []any{resourcePhysicalLegacyFixture{Timestamp: 1700000000000, Action: "old", Entries: value, Snapshot: "null"}})
		result, err := resourcePhysicalScan(t, path, resourcePageTestQuery(t, nil), nil)
		if err != nil || result.SourcePresent || result.Retained != 0 {
			t.Fatalf("legacy serialization default became observed empty: %q %v", value, err)
		}
	}
}
func resourcePhysicalRewriteFooter(t *testing.T, path string, mutate func(*parquet.FileMetaData)) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	physical, err := openBrowserResourcePhysicalParquet(context.Background(), file, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mutate(physical.footer)
	transport := thrift.NewTMemoryBuffer()
	compact := thrift.NewTCompactProtocolConf(transport, &thrift.TConfiguration{MaxMessageSize: resourceParquetFooterLimit})
	if err := physical.footer.Write(context.Background(), compact); err != nil {
		t.Fatal(err)
	}
	footer := transport.Bytes()
	output := append(append([]byte{}, raw[:physical.footerAt]...), footer...)
	var tail [8]byte
	binary.LittleEndian.PutUint32(tail[:4], uint32(len(footer)))
	copy(tail[4:], "PAR1")
	output = append(output, tail[:]...)
	if err := os.WriteFile(path, output, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestBrowserResourcePhysicalMalformedSourcesReturnNoPartialEvidence(t *testing.T) {
	for _, kind := range []string{"external_chunk", "wrong_row_count", "overlap", "trailer_truncated", "page_corrupt", "unsupported_semantics", "future_projection", "unavailable_limit"} {
		t.Run(kind, func(t *testing.T) {
			source := []byte(`{"network_entries":[{"duration_ms":0}]}`)
			projection := string(protocol.BrowserResourceArchiveProjection(source))
			if kind == "future_projection" {
				projection = `{"version":2,"availability":"retained_projection","transaction":{}}`
			}
			if kind == "unavailable_limit" {
				projection = `{"version":1,"availability":"unavailable","reason":"resource_projection_limit"}`
			}
			path := resourcePhysicalWrite(t, new(resourcePhysicalProjectionFixture), []any{resourcePhysicalProjectionFixture{Projection: projection}})
			switch kind {
			case "external_chunk":
				resourcePhysicalRewriteFooter(t, path, func(meta *parquet.FileMetaData) {
					external := "/private/not-an-archive-object"
					meta.RowGroups[0].Columns[0].FilePath = &external
				})
			case "wrong_row_count":
				resourcePhysicalRewriteFooter(t, path, func(meta *parquet.FileMetaData) { meta.NumRows++ })
			case "overlap":
				resourcePhysicalRewriteFooter(t, path, func(meta *parquet.FileMetaData) {
					meta.RowGroups[0].Columns[0].MetaData.TotalCompressedSize = math.MaxInt64
				})
			case "unsupported_semantics":
				resourcePhysicalRewriteFooter(t, path, func(meta *parquet.FileMetaData) {
					kind := parquet.ConvertedType_DECIMAL
					meta.Schema[1].ConvertedType = &kind
				})
			case "trailer_truncated":
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw[:len(raw)-3], 0600); err != nil {
					t.Fatal(err)
				}
			case "page_corrupt":
				file, err := os.OpenFile(path, os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteAt([]byte{0xff, 0xff, 0xff, 0xff}, 4); err != nil {
					t.Fatal(err)
				}
				_ = file.Close()
			}
			result, err := resourcePhysicalScan(t, path, resourcePageTestQuery(t, nil), nil)
			if err == nil || !reflect.DeepEqual(result, browserResourceScanResult{}) {
				t.Fatalf("malformed physical source returned partial evidence: %v", err)
			}
			if kind == "unavailable_limit" && !errors.Is(err, errBrowserResourceReadLimit) {
				t.Fatalf("explicit limit category lost: %v", err)
			}
		})
	}
}
func TestBrowserResourcePhysicalDecodersRefuseForgedAllocationAndDictionaryIndices(t *testing.T) {
	if _, err := resourceParquetPlain([]byte{255, 255, 255, 255}, parquet.Type_BYTE_ARRAY, 1); err == nil {
		t.Fatal("forged string allocation admitted")
	}
	if _, err := resourceParquetPlain(nil, parquet.Type_INT64, resourceParquetPageValuesLimit+1); !errors.Is(err, errBrowserResourceReadLimit) {
		t.Fatal("value budget bypassed")
	}
	for _, raw := range [][]byte{{1, 2, 1}, {1, 0}, {32, 2, 0}, {1, 3}, {0, 2, 0}} {
		if _, err := resourceParquetDictionary(raw, 1, []any{"only"}); err == nil {
			t.Fatalf("forged dictionary stream admitted:%v", raw)
		}
	}
	valid, err := resourceParquetDictionary([]byte{1, 6, 0}, 3, []any{"only"})
	if err != nil || !reflect.DeepEqual(valid, []any{"only", "only", "only"}) {
		t.Fatal("valid repeated dictionary values changed")
	}
}

func TestBrowserResourcePhysicalDictionaryZeroWidthAndBitPackedBoundaries(t *testing.T) {
	for name, fixture := range map[string]struct {
		raw        []byte
		count      int
		dictionary []any
		expected   []any
	}{
		"zero_width_rle":     {[]byte{0, 6}, 3, []any{"only"}, []any{"only", "only", "only"}},
		"bit_packed_padding": {[]byte{1, 3, 0b00000101}, 3, []any{"zero", "one"}, []any{"one", "zero", "one"}},
	} {
		t.Run(name, func(t *testing.T) {
			values, err := resourceParquetDictionary(fixture.raw, fixture.count, fixture.dictionary)
			if err != nil || !reflect.DeepEqual(values, fixture.expected) {
				t.Fatalf("valid dictionary boundary refused: %v", err)
			}
		})
	}
}

func TestBrowserResourcePhysicalAggregateMemoryBudgetRefusesRatherThanDroppingEvidence(t *testing.T) {
	budget := &resourceParquetMemoryBudget{}
	if err := budget.reserve(resourceParquetResidentLimit); err != nil {
		t.Fatal(err)
	}
	if err := budget.reserve(1); !errors.Is(err, errBrowserResourceReadLimit) {
		t.Fatal("aggregate page/dictionary limit bypassed")
	}
	budget.release(resourceParquetResidentLimit)
	if budget.used != 0 {
		t.Fatal("owned decoder state remained charged")
	}
	if err := budget.reserve(-1); err == nil {
		t.Fatal("negative charge manufactured headroom")
	}
}

func TestBrowserResourcePhysicalSnapshotOwnsCancellationAndRemovesScratch(t *testing.T) {
	path := resourcePhysicalWrite(t, new(resourcePhysicalProjectionFixture), []any{resourcePhysicalProjectionFixture{Projection: string(protocol.BrowserResourceArchiveProjection([]byte(`{}`)))}})
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	ctx, cancel := context.WithCancel(context.Background())
	snapshot, digest, size, cleanup, err := snapshotBrowserResourceParquet(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if size < 12 || len(digest) != 64 {
		cleanup()
		t.Fatal("incomplete snapshot was admitted")
	}
	name := snapshot.Name()
	cancel()
	cleanup() // joins the actual owned close callback
	if _, err := snapshot.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("cancelled private descriptor remains open: %v", err)
	}
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned scratch remains visible: %v", err)
	}
	if _, _, _, _, err := snapshotBrowserResourceParquet(ctx, source); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled copy was admitted: %v", err)
	}
}

func TestBrowserResourcePhysicalPlainStringBoxingCannotExceedRetainedBudget(t *testing.T) {
	const count int32 = 65536
	// One-byte scalar strings require five decoded bytes each, but every
	// retained value also owns a boxed string header and an interface slot.
	decoded := int(count) * 5
	oldUndercharge := int64(decoded) + int64(count)*16
	stringCharge := resourceParquetPlainRetainedCharge(decoded, count, parquet.Type_BYTE_ARRAY)
	if stringCharge != int64(decoded)+int64(count)*32 || stringCharge <= oldUndercharge {
		t.Fatal("boxed string ownership was not charged")
	}
	budget := &resourceParquetMemoryBudget{}
	if err := budget.reserve(resourceParquetResidentLimit - oldUndercharge); err != nil {
		t.Fatal(err)
	}
	if err := budget.reserve(stringCharge); !errors.Is(err, errBrowserResourceReadLimit) {
		t.Fatal("high-cardinality strings exceeded the retained decoder budget")
	}
	if budget.used != resourceParquetResidentLimit-oldUndercharge {
		t.Fatal("refused charge consumed or created headroom")
	}
	if numeric := resourceParquetPlainRetainedCharge(int(count)*8, count, parquet.Type_INT64); numeric != int64(count)*24 {
		t.Fatal("numeric boxing ownership changed")
	}
	if _, err := resourceParquetDictionary([]byte{1, 2, 2}, 1, []any{"zero", "one", "two"}); err == nil {
		t.Fatal("RLE unused width bits redirected a dictionary index")
	}
}

func TestBrowserResourcePhysicalStringPageBranchesEnforceBoxedRetentionBeforeMaterialization(t *testing.T) {
	const count int32 = 1024
	var decoded bytes.Buffer
	for index := int32(0); index < count; index++ {
		text := fmt.Sprintf("distinct-string-%04d", index)
		if err := binary.Write(&decoded, binary.LittleEndian, uint32(len(text))); err != nil {
			t.Fatal(err)
		}
		decoded.WriteString(text)
	}
	for _, dictionary := range []bool{false, true} {
		name := "plain"
		if dictionary {
			name = "dictionary"
		}
		t.Run(name, func(t *testing.T) {
			header := parquet.NewPageHeader()
			header.Type = parquet.PageType_DATA_PAGE
			header.CompressedPageSize = int32(decoded.Len())
			header.UncompressedPageSize = int32(decoded.Len())
			if dictionary {
				header.Type = parquet.PageType_DICTIONARY_PAGE
				header.DictionaryPageHeader = &parquet.DictionaryPageHeader{NumValues: count, Encoding: parquet.Encoding_PLAIN}
			} else {
				header.DataPageHeader = &parquet.DataPageHeader{NumValues: count, Encoding: parquet.Encoding_PLAIN, DefinitionLevelEncoding: parquet.Encoding_RLE, RepetitionLevelEncoding: parquet.Encoding_RLE}
			}
			transport := &thrift.TMemoryBuffer{Buffer: &bytes.Buffer{}}
			if err := header.Write(context.Background(), thrift.NewTCompactProtocolConf(transport, &thrift.TConfiguration{})); err != nil {
				t.Fatal(err)
			}
			page := append(append([]byte("PAR1"), transport.Bytes()...), decoded.Bytes()...)
			path := filepath.Join(t.TempDir(), "owned-string-page.parquet")
			if err := os.WriteFile(path, page, 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			oldUndercharge := int64(decoded.Len()) + int64(count)*16
			budget := &resourceParquetMemoryBudget{used: resourceParquetResidentLimit - oldUndercharge}
			before := budget.used
			metadata := &parquet.ColumnMetaData{Type: parquet.Type_BYTE_ARRAY, Codec: parquet.CompressionCodec_UNCOMPRESSED, NumValues: int64(count), DataPageOffset: 4, TotalCompressedSize: int64(len(page) - 4)}
			if dictionary {
				at := int64(4)
				metadata.DictionaryPageOffset = &at
				metadata.DataPageOffset = int64(len(page))
			}
			cursor := newResourceParquetColumn(context.Background(), file, metadata, budget)
			defer cursor.release()
			if _, err := cursor.next(); !errors.Is(err, errBrowserResourceReadLimit) {
				t.Fatalf("physical%s page branch did not refuse boxed string budget:%v", name, err)
			}
			if budget.used != before || cursor.valuesCharge != 0 || cursor.dictionaryCharge != 0 || cursor.values != nil || cursor.dictionary != nil {
				t.Fatal("refused page retained values or consumed headroom")
			}
		})
	}
}

func TestBrowserResourcePhysicalMalformedPageHeaderCannotMaterializeValues(t *testing.T) {
	for name, mutate := range map[string]func(*parquet.PageHeader){
		"mixed_dictionary": func(h *parquet.PageHeader) {
			h.DictionaryPageHeader = &parquet.DictionaryPageHeader{NumValues: 1, Encoding: parquet.Encoding_PLAIN}
		},
		"mixed_index":                 func(h *parquet.PageHeader) { h.IndexPageHeader = parquet.NewIndexPageHeader() },
		"mixed_v2":                    func(h *parquet.PageHeader) { h.DataPageHeaderV2 = parquet.NewDataPageHeaderV2() },
		"unknown_definition_encoding": func(h *parquet.PageHeader) { h.DataPageHeader.DefinitionLevelEncoding = parquet.Encoding(999) },
		"unknown_repetition_encoding": func(h *parquet.PageHeader) { h.DataPageHeader.RepetitionLevelEncoding = parquet.Encoding(999) },
	} {
		t.Run(name, func(t *testing.T) {
			header := parquet.NewPageHeader()
			header.Type = parquet.PageType_DATA_PAGE
			header.CompressedPageSize = 5
			header.UncompressedPageSize = 5
			header.DataPageHeader = &parquet.DataPageHeader{NumValues: 1, Encoding: parquet.Encoding_PLAIN, DefinitionLevelEncoding: parquet.Encoding_RLE, RepetitionLevelEncoding: parquet.Encoding_RLE}
			mutate(header)
			transport := &thrift.TMemoryBuffer{Buffer: &bytes.Buffer{}}
			if err := header.Write(context.Background(), thrift.NewTCompactProtocolConf(transport, &thrift.TConfiguration{})); err != nil {
				t.Fatal(err)
			}
			page := append(append([]byte("PAR1"), transport.Bytes()...), []byte{1, 0, 0, 0, 'a'}...)
			path := filepath.Join(t.TempDir(), "malformed-owned-page.parquet")
			if err := os.WriteFile(path, page, 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			budget := &resourceParquetMemoryBudget{}
			cursor := newResourceParquetColumn(context.Background(), file, &parquet.ColumnMetaData{Type: parquet.Type_BYTE_ARRAY, Codec: parquet.CompressionCodec_UNCOMPRESSED, NumValues: 1, DataPageOffset: 4, TotalCompressedSize: int64(len(page) - 4)}, budget)
			defer cursor.release()
			if _, err := cursor.next(); err == nil || !errors.Is(err, errBrowserResourceReadMalformed) && !errors.Is(err, errBrowserResourceUnavailable) {
				t.Fatalf("malformed physical header admitted:%v", err)
			}
			if cursor.values != nil || cursor.dictionary != nil || budget.used != 0 {
				t.Fatal("malformed header retained a partial value population")
			}
		})
	}
}

func TestBrowserResourceDictionaryEncodedWidthIsIndependentFromBoundedCardinality(t *testing.T) {
	dictionary := []any{"zero", "one", "two"}
	// Genuine pinned writer uses32-bit little-endian RLE indexes, including for
	// a three-entry dictionary. Both RLE and packed paths retain exact indexes.
	rle := []byte{32, 2, 0, 0, 0, 0, 2, 1, 0, 0, 0, 2, 2, 0, 0, 0}
	packed := make([]byte, 34)
	packed[0], packed[1] = 32, 3
	binary.LittleEndian.PutUint32(packed[2:6], 0)
	binary.LittleEndian.PutUint32(packed[6:10], 1)
	binary.LittleEndian.PutUint32(packed[10:14], 2)
	for _, raw := range [][]byte{rle, packed} {
		values, err := resourceParquetDictionary(raw, 3, dictionary)
		if err != nil || !reflect.DeepEqual(values, dictionary) {
			t.Fatalf("legal32-bit dictionary index failed: %v", err)
		}
	}
	for _, raw := range [][]byte{{33, 2, 0, 0, 0, 0, 0}, {32, 2, 0, 0, 0, 128}} {
		if values, err := resourceParquetDictionary(raw, 1, dictionary); !errors.Is(err, errBrowserResourceReadMalformed) || values != nil {
			t.Fatal("invalid width or high-bit index became retained evidence")
		}
	}
	binary.LittleEndian.PutUint32(packed[2:6], 1<<31)
	if values, err := resourceParquetDictionary(packed, 3, dictionary); !errors.Is(err, errBrowserResourceReadMalformed) || values != nil {
		t.Fatal("out-of-range packed32-bit index became retained evidence")
	}
}

func TestBrowserResourcePhysicalDictionaryWidthRefusesCorruptIndexes(t *testing.T) {
	var dictionary bytes.Buffer
	for _, text := range []string{"zero", "one", "two"} {
		if err := binary.Write(&dictionary, binary.LittleEndian, uint32(len(text))); err != nil {
			t.Fatal(err)
		}
		dictionary.WriteString(text)
	}
	for name, payload := range map[string][]byte{
		"legal32":          {32, 2, 0, 0, 0, 0},
		"width33":          {33, 2, 0, 0, 0, 0, 0},
		"high_bit_index32": {32, 2, 0, 0, 0, 128},
	} {
		t.Run(name, func(t *testing.T) {
			serialize := func(header *parquet.PageHeader, payload []byte) []byte {
				header.CompressedPageSize, header.UncompressedPageSize = int32(len(payload)), int32(len(payload))
				transport := &thrift.TMemoryBuffer{Buffer: &bytes.Buffer{}}
				if err := header.Write(context.Background(), thrift.NewTCompactProtocolConf(transport, &thrift.TConfiguration{})); err != nil {
					t.Fatal(err)
				}
				return append(append([]byte(nil), transport.Bytes()...), payload...)
			}
			dictHeader := &parquet.PageHeader{Type: parquet.PageType_DICTIONARY_PAGE, DictionaryPageHeader: &parquet.DictionaryPageHeader{NumValues: 3, Encoding: parquet.Encoding_PLAIN}}
			dictPage := serialize(dictHeader, dictionary.Bytes())
			dataHeader := &parquet.PageHeader{Type: parquet.PageType_DATA_PAGE, DataPageHeader: &parquet.DataPageHeader{NumValues: 1, Encoding: parquet.Encoding_RLE_DICTIONARY, DefinitionLevelEncoding: parquet.Encoding_RLE, RepetitionLevelEncoding: parquet.Encoding_RLE}}
			dataPage := serialize(dataHeader, payload)
			raw := append(append(append([]byte("PAR1"), dictPage...), dataPage...), []byte{}...)
			path := filepath.Join(t.TempDir(), "owned-width-dictionary.parquet")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			budget := &resourceParquetMemoryBudget{}
			start := int64(4)
			cursor := newResourceParquetColumn(context.Background(), file, &parquet.ColumnMetaData{Type: parquet.Type_BYTE_ARRAY, Codec: parquet.CompressionCodec_UNCOMPRESSED, NumValues: 1, DictionaryPageOffset: &start, DataPageOffset: 4 + int64(len(dictPage)), TotalCompressedSize: int64(len(raw) - 4)}, budget)
			defer cursor.release()
			value, err := cursor.next()
			if name == "legal32" {
				if err != nil || value != "zero" {
					t.Fatalf("legal physical32-bit page refused:%v", err)
				}
				if err := cursor.finish(); err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, errBrowserResourceReadMalformed) || value != nil || cursor.values != nil || cursor.valuesCharge != 0 {
				t.Fatal("corrupt physical dictionary index materialized a row")
			}
			cursor.release()
			if budget.used != 0 {
				t.Fatal("refused physical dictionary retained owned values")
			}
		})
	}
}

// This deliberately retains the historical unbounded-statistics producer shape.
// Unsupported physical headers must fail explicitly, never return partial counts.
func TestBrowserResourcePhysicalLegacyOpaqueStatisticsRefusesWithoutPartialEvidence(t *testing.T) {
	entries := make([]map[string]any, 80)
	for i := range entries {
		evidence := resourcePageTestEvidence()
		evidence.RequestID = fmt.Sprintf("%s-request-%d", evidence.SourceGeneration, i)
		entries[i] = map[string]any{"url": "https://example.test/resource", "duration_ms": 0, "browser_evidence": evidence}
	}
	raw, err := json.Marshal(map[string]any{"test_id": "run-1", "network_entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	projection := protocol.BrowserResourceArchiveProjection(raw)
	if len(projection) <= resourceParquetHeaderLimit {
		t.Fatal("legacy-statistics fixture does not cross the header bound")
	}
	path := resourcePhysicalWrite(t, new(resourcePhysicalProjectionFixture), []any{resourcePhysicalProjectionFixture{Projection: string(projection)}})
	result, err := resourcePhysicalScan(t, path, resourcePageTestQuery(t, nil), nil)
	if !errors.Is(err, errBrowserResourceReadLimit) || !reflect.DeepEqual(result, browserResourceScanResult{}) {
		t.Fatalf("oversized historical lexical-statistics header lost explicit limit refusal or produced partial evidence: %v", err)
	}
}
