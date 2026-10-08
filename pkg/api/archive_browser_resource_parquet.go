package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/golang/snappy"
	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/xitongsys/parquet-go/parquet"
)

const resourceParquetFooterLimit = 2 << 20
const resourceParquetHeaderLimit = 16 << 10
const resourceParquetCompressedPageLimit = 8 << 20
const resourceParquetDecodedPageLimit = 16 << 20
const resourceParquetPageValuesLimit = 65536
const resourceParquetResidentLimit = 128 << 20

type resourceParquetMemoryBudget struct{ used int64 }

func (b *resourceParquetMemoryBudget) reserve(n int64) error {
	if n < 0 || n > resourceParquetResidentLimit-b.used {
		return errBrowserResourceReadLimit
	}
	b.used += n
	return nil
}
func (b *resourceParquetMemoryBudget) release(n int64) { b.used -= n }

// Only the existing archive writer's flat primitive columns and page encodings
// are admitted. General SDK row readers are deliberately absent: they can turn
// corrupt/truncated pages into nulls. This reader preserves errors and exact row
// counts before a page becomes visible and never opens external chunk paths.
type resourceParquetMetadataProtocol struct {
	thrift.TProtocol
	remaining, depth int
}

func (p *resourceParquetMetadataProtocol) reserve(n int) error {
	if n < 0 || n > 1024 || n > p.remaining {
		return errBrowserResourceReadLimit
	}
	p.remaining -= n
	return nil
}
func (p *resourceParquetMetadataProtocol) ReadListBegin(ctx context.Context) (thrift.TType, int, error) {
	t, n, e := p.TProtocol.ReadListBegin(ctx)
	if e == nil {
		e = p.reserve(n)
	}
	return t, n, e
}
func (p *resourceParquetMetadataProtocol) ReadSetBegin(ctx context.Context) (thrift.TType, int, error) {
	t, n, e := p.TProtocol.ReadSetBegin(ctx)
	if e == nil {
		e = p.reserve(n)
	}
	return t, n, e
}
func (p *resourceParquetMetadataProtocol) ReadMapBegin(ctx context.Context) (thrift.TType, thrift.TType, int, error) {
	k, v, n, e := p.TProtocol.ReadMapBegin(ctx)
	if e == nil {
		e = p.reserve(n)
	}
	if e == nil {
		e = p.reserve(n)
	}
	return k, v, n, e
}
func (p *resourceParquetMetadataProtocol) ReadStructBegin(ctx context.Context) (string, error) {
	if p.depth >= 48 {
		return "", errBrowserResourceReadLimit
	}
	if e := p.reserve(1); e != nil {
		return "", e
	}
	p.depth++
	return p.TProtocol.ReadStructBegin(ctx)
}
func (p *resourceParquetMetadataProtocol) ReadStructEnd(ctx context.Context) error {
	p.depth--
	return p.TProtocol.ReadStructEnd(ctx)
}
func (p *resourceParquetMetadataProtocol) Skip(ctx context.Context, t thrift.TType) error {
	return thrift.Skip(ctx, p, t, 48)
}
func resourceParquetProtocol(raw []byte, limit int) (*resourceParquetMetadataProtocol, *thrift.TMemoryBuffer) {
	transport := &thrift.TMemoryBuffer{Buffer: bytes.NewBuffer(raw)}
	return &resourceParquetMetadataProtocol{TProtocol: thrift.NewTCompactProtocolConf(transport, &thrift.TConfiguration{MaxMessageSize: int32(limit)}), remaining: 65536}, transport
}

func snapshotBrowserResourceParquet(ctx context.Context, source *os.File) (*os.File, string, int64, func(), error) {
	if ctx == nil || source == nil {
		return nil, "", 0, nil, errBrowserResourceReadMalformed
	}
	if err := ctx.Err(); err != nil {
		return nil, "", 0, nil, err
	}
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", 0, nil, errBrowserResourceReadMalformed
	}
	if info.Size() > maxArchiveQueryBytes {
		return nil, "", 0, nil, errBrowserResourceReadLimit
	}
	available, err := diskspace.AvailablePath(os.TempDir())
	if err != nil || available < info.Size()+(128<<20) {
		return nil, "", 0, nil, errBrowserResourceUnavailable
	}
	// One admitted heavy scan owns at most512MiB of private scratch; file permissions
	// remain0600 and this rebuildable snapshot never becomes durable metadata.
	copy, err := os.CreateTemp("", "apexload-browser-resource-*.parquet")
	if err != nil {
		return nil, "", 0, nil, errBrowserResourceUnavailable
	}
	stop := browserResourceCloseOnCancel(ctx, copy)
	cleanup := func() { _ = copy.Close(); stop(); _ = os.Remove(copy.Name()) }
	success := false
	defer func() {
		if !success {
			cleanup()
		}
	}()
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return nil, "", 0, nil, errBrowserResourceReadMalformed
	}
	hash := sha256.New()
	counter := &browserResourceCountingReader{r: source, ctx: ctx}
	count, err := io.Copy(io.MultiWriter(copy, hash), counter)
	if err != nil {
		return nil, "", 0, nil, browserResourceContextOr(ctx, err)
	}
	if count != info.Size() {
		return nil, "", 0, nil, errBrowserResourceReadChanged
	}
	if _, err := copy.Seek(0, io.SeekStart); err != nil {
		return nil, "", 0, nil, errBrowserResourceReadMalformed
	}
	if err := ctx.Err(); err != nil {
		return nil, "", 0, nil, err
	}
	success = true
	return copy, hex.EncodeToString(hash.Sum(nil)), count, cleanup, nil
}

type resourceParquetFile struct {
	file     *os.File
	footer   *parquet.FileMetaData
	footerAt int64
	schema   map[string]*parquet.SchemaElement
}

func openBrowserResourcePhysicalParquet(ctx context.Context, file *os.File, size int64) (*resourceParquetFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if size < 12 || size > maxArchiveQueryBytes {
		return nil, errBrowserResourceReadMalformed
	}
	var head [4]byte
	var tail [8]byte
	if _, err := file.ReadAt(head[:], 0); err != nil || string(head[:]) != "PAR1" {
		return nil, errBrowserResourceReadMalformed
	}
	if _, err := file.ReadAt(tail[:], size-8); err != nil || string(tail[4:]) != "PAR1" {
		return nil, errBrowserResourceReadMalformed
	}
	footerBytes := int64(binary.LittleEndian.Uint32(tail[:4]))
	if footerBytes <= 0 || footerBytes > resourceParquetFooterLimit || footerBytes > size-12 {
		return nil, errBrowserResourceReadLimit
	}
	at := size - 8 - footerBytes
	raw := make([]byte, int(footerBytes))
	if _, err := file.ReadAt(raw, at); err != nil {
		return nil, errBrowserResourceReadMalformed
	}
	decoder, transport := resourceParquetProtocol(raw, resourceParquetFooterLimit)
	metadata := parquet.NewFileMetaData()
	if err := metadata.Read(ctx, decoder); err != nil {
		return nil, browserResourceContextOr(ctx, err)
	}
	if transport.Len() != 0 || metadata.Version != 1 && metadata.Version != 2 || metadata.NumRows < 0 || metadata.NumRows > maxArchiveQueryRecords || len(metadata.Schema) < 2 || len(metadata.Schema) > 129 || metadata.Schema[0] == nil || metadata.Schema[0].Type != nil || metadata.Schema[0].GetNumChildren() != int32(len(metadata.Schema)-1) {
		return nil, errBrowserResourceReadMalformed
	}
	result := &resourceParquetFile{file: file, footer: metadata, footerAt: at, schema: map[string]*parquet.SchemaElement{}}
	folded := map[string]bool{}
	for _, field := range metadata.Schema[1:] {
		if field == nil || field.Type == nil || field.GetNumChildren() != 0 || field.RepetitionType == nil || field.GetRepetitionType() != parquet.FieldRepetitionType_REQUIRED || field.Name == "" || len(field.Name) > 128 || !utf8.ValidString(field.Name) || strings.ContainsAny(field.Name, "\x00\r\n") || folded[strings.ToLower(field.Name)] {
			return nil, errBrowserResourceUnavailable
		}
		folded[strings.ToLower(field.Name)] = true
		result.schema[field.Name] = field
	}
	type span struct{ begin, end int64 }
	spans := []span{}
	rows := int64(0)
	for _, group := range metadata.RowGroups {
		if group == nil || group.NumRows < 0 || group.NumRows > maxArchiveQueryRecords-rows || len(group.Columns) != len(result.schema) {
			return nil, errBrowserResourceReadMalformed
		}
		rows += group.NumRows
		seen := map[string]bool{}
		for _, chunk := range group.Columns {
			if chunk == nil || chunk.MetaData == nil || chunk.FilePath != nil && *chunk.FilePath != "" || len(chunk.MetaData.PathInSchema) != 1 {
				return nil, errBrowserResourceReadMalformed
			}
			col := chunk.MetaData
			name := col.PathInSchema[0]
			field := result.schema[name]
			if field == nil || seen[name] || col.Type != *field.Type || col.NumValues != group.NumRows || col.TotalCompressedSize < 0 {
				return nil, errBrowserResourceReadMalformed
			}
			seen[name] = true
			start := col.DataPageOffset
			if col.DictionaryPageOffset != nil {
				if *col.DictionaryPageOffset >= col.DataPageOffset {
					return nil, errBrowserResourceReadMalformed
				}
				start = *col.DictionaryPageOffset
			}
			if start < 4 || start > at || col.TotalCompressedSize > at-start {
				return nil, errBrowserResourceReadMalformed
			}
			if col.TotalCompressedSize > 0 {
				spans = append(spans, span{start, start + col.TotalCompressedSize})
			} else if col.NumValues != 0 {
				return nil, errBrowserResourceReadMalformed
			}
		}
	}
	if rows != metadata.NumRows {
		return nil, errBrowserResourceReadMalformed
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].begin < spans[j].begin })
	for i := 1; i < len(spans); i++ {
		if spans[i].begin < spans[i-1].end {
			return nil, errBrowserResourceReadMalformed
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

type resourceParquetColumn struct {
	budget                         *resourceParquetMemoryBudget
	dictionaryCharge, valuesCharge int64
	ctx                            context.Context
	file                           *os.File
	metadata                       *parquet.ColumnMetaData
	position, end, expected, read  int64
	dictionary, values             []any
	index                          int
	dictionaryRead                 bool
	dataRead                       bool
}

func newResourceParquetColumn(ctx context.Context, file *os.File, metadata *parquet.ColumnMetaData, budget *resourceParquetMemoryBudget) *resourceParquetColumn {
	start := metadata.DataPageOffset
	if metadata.DictionaryPageOffset != nil {
		start = *metadata.DictionaryPageOffset
	}
	return &resourceParquetColumn{ctx: ctx, file: file, metadata: metadata, budget: budget, position: start, end: start + metadata.TotalCompressedSize, expected: metadata.NumValues}
}
func (c *resourceParquetColumn) next() (any, error) {
	for c.index >= len(c.values) {
		if c.read >= c.expected {
			return nil, io.EOF
		}
		if err := c.page(); err != nil {
			return nil, err
		}
	}
	value := c.values[c.index]
	c.index++
	c.read++
	return value, nil
}
func (c *resourceParquetColumn) release() {
	c.budget.release(c.valuesCharge + c.dictionaryCharge)
	c.valuesCharge = 0
	c.dictionaryCharge = 0
	c.values = nil
	c.dictionary = nil
}
func (c *resourceParquetColumn) finish() error {
	defer c.release()
	if c.read != c.expected || c.index != len(c.values) || c.position != c.end {
		return errBrowserResourceReadMalformed
	}
	return c.ctx.Err()
}
func (c *resourceParquetColumn) page() error {
	// Exhausted pages no longer count as retained decoder state. The transient
	// compressed/decompressed buffers are separately bounded per page.
	c.budget.release(c.valuesCharge)
	c.valuesCharge = 0
	c.values = nil
	c.index = 0
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if c.position >= c.end {
		return errBrowserResourceReadMalformed
	}
	length := c.end - c.position
	if length > resourceParquetHeaderLimit {
		length = resourceParquetHeaderLimit
	}
	raw := make([]byte, int(length))
	if _, err := c.file.ReadAt(raw, c.position); err != nil {
		return errBrowserResourceReadMalformed
	}
	decoder, transport := resourceParquetProtocol(raw, resourceParquetHeaderLimit)
	header := parquet.NewPageHeader()
	if err := header.Read(c.ctx, decoder); err != nil {
		var protocolError thrift.TProtocolException
		if errors.As(err, &protocolError) && protocolError.TypeId() == thrift.SIZE_LIMIT {
			return browserResourceContextOr(c.ctx, errBrowserResourceReadLimit)
		}
		return browserResourceContextOr(c.ctx, errBrowserResourceReadMalformed)
	}
	used := len(raw) - transport.Len()
	if used <= 0 || header.CompressedPageSize < 0 || header.UncompressedPageSize < 0 || header.CompressedPageSize > resourceParquetCompressedPageLimit || header.UncompressedPageSize > resourceParquetDecodedPageLimit {
		return errBrowserResourceReadLimit
	}
	payloadAt := c.position + int64(used)
	end := payloadAt + int64(header.CompressedPageSize)
	if end > c.end {
		return errBrowserResourceReadMalformed
	}
	encoded := make([]byte, int(header.CompressedPageSize))
	if _, err := c.file.ReadAt(encoded, payloadAt); err != nil {
		return errBrowserResourceReadMalformed
	}
	if header.Crc != nil && crc32.ChecksumIEEE(encoded) != uint32(*header.Crc) {
		return errBrowserResourceReadMalformed
	}
	var decoded []byte
	switch c.metadata.Codec {
	case parquet.CompressionCodec_UNCOMPRESSED:
		decoded = encoded
	case parquet.CompressionCodec_SNAPPY:
		decodedLength, err := snappy.DecodedLen(encoded)
		if err != nil || decodedLength != int(header.UncompressedPageSize) || decodedLength > resourceParquetDecodedPageLimit {
			return errBrowserResourceReadMalformed
		}
		decoded, err = snappy.Decode(nil, encoded)
		if err != nil {
			return errBrowserResourceReadMalformed
		}
	default:
		return errBrowserResourceUnavailable
	}
	if len(decoded) != int(header.UncompressedPageSize) {
		return errBrowserResourceReadMalformed
	}
	c.position = end
	switch header.Type {
	case parquet.PageType_DICTIONARY_PAGE:
		h := header.DictionaryPageHeader
		if h == nil || header.DataPageHeader != nil || header.DataPageHeaderV2 != nil || header.IndexPageHeader != nil || c.dictionaryRead || c.dataRead || h.NumValues < 1 || h.NumValues > resourceParquetPageValuesLimit || h.Encoding != parquet.Encoding_PLAIN {
			return errBrowserResourceReadMalformed
		}
		charge := resourceParquetPlainRetainedCharge(len(decoded), h.NumValues, c.metadata.Type)
		if err := c.budget.reserve(charge); err != nil {
			return err
		}
		values, err := resourceParquetPlain(decoded, c.metadata.Type, int(h.NumValues))
		if err != nil {
			c.budget.release(charge)
			return err
		}
		c.dictionary = values
		c.dictionaryCharge = charge
		c.dictionaryRead = true
		c.values = nil
		c.index = 0
		return nil
	case parquet.PageType_DATA_PAGE:
		h := header.DataPageHeader
		if h == nil || header.DictionaryPageHeader != nil || header.DataPageHeaderV2 != nil || header.IndexPageHeader != nil || h.NumValues < 1 || h.NumValues > resourceParquetPageValuesLimit || int64(h.NumValues) > c.expected-c.read {
			return errBrowserResourceReadMalformed
		}
		if h.DefinitionLevelEncoding != parquet.Encoding_RLE && h.DefinitionLevelEncoding != parquet.Encoding_BIT_PACKED || h.RepetitionLevelEncoding != parquet.Encoding_RLE && h.RepetitionLevelEncoding != parquet.Encoding_BIT_PACKED {
			return errBrowserResourceUnavailable
		}
		if !c.dataRead && c.position-int64(header.CompressedPageSize)-int64(used) != c.metadata.DataPageOffset {
			return errBrowserResourceReadMalformed
		}
		c.dataRead = true
		charge := int64(h.NumValues) * 16
		if h.Encoding == parquet.Encoding_PLAIN {
			charge = resourceParquetPlainRetainedCharge(len(decoded), h.NumValues, c.metadata.Type)
		}
		if err := c.budget.reserve(charge); err != nil {
			return err
		}
		reserved := true
		defer func() {
			if reserved {
				c.budget.release(charge)
			}
		}()
		var values []any
		var err error
		switch h.Encoding {
		case parquet.Encoding_PLAIN:
			values, err = resourceParquetPlain(decoded, c.metadata.Type, int(h.NumValues))
		case parquet.Encoding_PLAIN_DICTIONARY, parquet.Encoding_RLE_DICTIONARY:
			if !c.dictionaryRead {
				return errBrowserResourceReadMalformed
			}
			values, err = resourceParquetDictionary(decoded, int(h.NumValues), c.dictionary)
		default:
			return errBrowserResourceUnavailable
		}
		if err != nil {
			return err
		}
		c.values = values
		c.valuesCharge = charge
		reserved = false
		c.index = 0
		return nil
	default:
		return errBrowserResourceUnavailable
	}
}

// BYTE_ARRAY values retain both the []any interface slot and a boxed string
// header in addition to owned bytes. The decoded length already includes the
// length prefixes, making this charge conservative. Dictionary-index pages
// alias the owned dictionary values and require only their interface slots.
func resourceParquetPlainRetainedCharge(decodedBytes int, count int32, kind parquet.Type) int64 {
	slot := int64(16)
	if kind == parquet.Type_BYTE_ARRAY {
		slot = 32
	}
	return int64(decodedBytes) + int64(count)*slot
}

func resourceParquetPlain(raw []byte, kind parquet.Type, count int) ([]any, error) {
	if count < 0 || count > resourceParquetPageValuesLimit {
		return nil, errBrowserResourceReadLimit
	}
	values := make([]any, 0, count)
	at := 0
	for i := 0; i < count; i++ {
		switch kind {
		case parquet.Type_INT64:
			if len(raw)-at < 8 {
				return nil, errBrowserResourceReadMalformed
			}
			values = append(values, int64(binary.LittleEndian.Uint64(raw[at:])))
			at += 8
		case parquet.Type_DOUBLE:
			if len(raw)-at < 8 {
				return nil, errBrowserResourceReadMalformed
			}
			values = append(values, math.Float64frombits(binary.LittleEndian.Uint64(raw[at:])))
			at += 8
		case parquet.Type_BYTE_ARRAY:
			if len(raw)-at < 4 {
				return nil, errBrowserResourceReadMalformed
			}
			n := int64(binary.LittleEndian.Uint32(raw[at:]))
			at += 4
			if n > maxBrowserResourceReadRecordBytes || n > int64(len(raw)-at) {
				return nil, errBrowserResourceReadLimit
			}
			value := raw[at : at+int(n)]
			if !utf8.Valid(value) {
				return nil, errBrowserResourceReadMalformed
			}
			values = append(values, string(value))
			at += int(n)
		default:
			return nil, errBrowserResourceUnavailable
		}
	}
	if at != len(raw) {
		return nil, errBrowserResourceReadMalformed
	}
	return values, nil
}
func resourceParquetDictionary(raw []byte, count int, dictionary []any) ([]any, error) {
	if len(raw) < 1 || count < 1 || count > resourceParquetPageValuesLimit || len(dictionary) < 1 || len(dictionary) > resourceParquetPageValuesLimit {
		return nil, errBrowserResourceReadMalformed
	}
	width := uint(raw[0])
	// Encoded width may be wider than the bounded dictionary requires. The
	// pinned writer emits32, and Parquet permits0..32; actual indexes still
	// must resolve within the admitted dictionary.
	if width > 32 {
		return nil, errBrowserResourceReadMalformed
	}
	raw = raw[1:]
	values := make([]any, 0, count)
	for len(values) < count {
		header, n := binary.Uvarint(raw)
		if n <= 0 || header == 0 {
			return nil, errBrowserResourceReadMalformed
		}
		raw = raw[n:]
		if header&1 == 0 {
			run := header >> 1
			bytesPer := int((width + 7) / 8)
			if run > uint64(count-len(values)) || len(raw) < bytesPer {
				return nil, errBrowserResourceReadMalformed
			}
			index := uint32(0)
			for i := 0; i < bytesPer; i++ {
				index |= uint32(raw[i]) << uint(8*i)
			}
			raw = raw[bytesPer:]
			if uint64(index) >= uint64(len(dictionary)) || uint64(index) >= uint64(1)<<width {
				return nil, errBrowserResourceReadMalformed
			}
			for i := uint64(0); i < run; i++ {
				values = append(values, dictionary[index])
			}
		} else {
			groups := header >> 1
			remain := count - len(values)
			if groups > uint64((remain+7)/8) {
				return nil, errBrowserResourceReadMalformed
			}
			packedBytes := int(groups) * int(width)
			if len(raw) < packedBytes {
				return nil, errBrowserResourceReadMalformed
			}
			packed := raw[:packedBytes]
			raw = raw[packedBytes:]
			for i := 0; i < int(groups)*8 && len(values) < count; i++ {
				index := uint32(0)
				for bit := uint(0); bit < width; bit++ {
					position := uint(i)*width + bit
					if packed[position/8]&(1<<(position%8)) != 0 {
						index |= 1 << bit
					}
				}
				if uint64(index) >= uint64(len(dictionary)) {
					return nil, errBrowserResourceReadMalformed
				}
				values = append(values, dictionary[index])
			}
		}
	}
	if len(raw) != 0 {
		return nil, errBrowserResourceReadMalformed
	}
	return values, nil
}

func scanBrowserResourceParquet(ctx context.Context, source *os.File, testID string, q BrowserResourcePageQuery, after *browserResourceSortKey) (browserResourceScanResult, error) {
	if ctx == nil || source == nil || q.Limit < 1 || q.Limit > MaxBrowserResourcePageItems {
		return browserResourceScanResult{}, errBrowserResourceReadMalformed
	}
	var result browserResourceScanResult
	digest, size, err := visitBrowserResourceParquet(ctx, source, func(raw []byte, legacyMillisecond bool) error {
		result.Transactions++
		items, present, err := browserResourceTransactionRows(raw, result.Transactions, testID)
		if err != nil {
			return err
		}
		result.SourcePresent = result.SourcePresent || present
		// A legacy millisecond column remains explicitly millisecond precision.
		if legacyMillisecond {
			for i := range items {
				if items[i].Timestamp != nil {
					items[i].TimestampPrecision = "unix_millisecond"
				}
			}
		}
		for _, item := range items {
			if err := result.observe(item, q, after); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return browserResourceScanResult{}, err
	}
	result.SHA256, result.SizeBytes = digest, size
	result.finish(q)
	return result, nil
}

// visitBrowserResourceParquetRows calls visit with each transaction row of a
// Parquet transaction artifact, as JSON.
func visitBrowserResourceParquetRows(ctx context.Context, source *os.File, visit func([]byte) error) error {
	if ctx == nil || source == nil {
		return errBrowserResourceReadMalformed
	}
	_, _, err := visitBrowserResourceParquet(ctx, source, func(raw []byte, _ bool) error { return visit(raw) })
	return err
}

// visitBrowserResourceParquet reads a private snapshot of a Parquet
// transaction artifact row by row: the canonical resource projection when it
// has one, otherwise the legacy columns rebuilt as a row; legacyMillisecond
// says a row's timestamp came from the legacy millisecond column. It returns
// the snapshot's SHA-256 and size.
func visitBrowserResourceParquet(ctx context.Context, source *os.File, visit func(raw []byte, legacyMillisecond bool) error) (string, int64, error) {
	snapshot, digest, size, cleanup, err := snapshotBrowserResourceParquet(ctx, source)
	if err != nil {
		return "", 0, err
	}
	defer cleanup()
	physical, err := openBrowserResourcePhysicalParquet(ctx, snapshot, size)
	if err != nil {
		return "", 0, browserResourceContextOr(ctx, err)
	}
	selected := []string{"resource_projection_json"}
	_, canonical := physical.schema[selected[0]]
	if !canonical {
		selected = []string{"test_id", "timestamp_rfc3339", "timestamp_unix_ms", "action_name", "group_name", "region", "page_url", "client_metrics_json", "network_entries_json", "snapshot_json"}
	}
	legacyMillisecond := !canonical && physical.schema["timestamp_rfc3339"] == nil && physical.schema["timestamp_unix_ms"] != nil
	var rows int64
	budget := &resourceParquetMemoryBudget{}
	for _, group := range physical.footer.RowGroups {
		cursors := map[string]*resourceParquetColumn{}
		for _, chunk := range group.Columns {
			name := chunk.MetaData.PathInSchema[0]
			for _, candidate := range selected {
				if name == candidate {
					if !resourceParquetSelectedSemantic(physical.schema[name], name) {
						return "", 0, errBrowserResourceUnavailable
					}
					kind := chunk.MetaData.Type
					if name == "timestamp_unix_ms" {
						if kind != parquet.Type_INT64 {
							return "", 0, errBrowserResourceUnavailable
						}
					} else if kind != parquet.Type_BYTE_ARRAY {
						return "", 0, errBrowserResourceUnavailable
					}
					cursors[name] = newResourceParquetColumn(ctx, snapshot, chunk.MetaData, budget)
					defer cursors[name].release()
				}
			}
		}
		for row := int64(0); row < group.NumRows; row++ {
			if err := ctx.Err(); err != nil {
				return "", 0, err
			}
			values := map[string]any{}
			for name, cursor := range cursors {
				value, err := cursor.next()
				if err != nil {
					return "", 0, browserResourceContextOr(ctx, err)
				}
				values[name] = value
			}
			var raw []byte
			if canonical {
				projection, ok := values["resource_projection_json"].(string)
				if !ok {
					return "", 0, errBrowserResourceReadMalformed
				}
				var reason string
				raw, reason, err = protocol.DecodeBrowserResourceArchiveProjection([]byte(projection))
				if err != nil {
					return "", 0, errBrowserResourceReadMalformed
				}
				if reason == "resource_projection_limit" {
					return "", 0, errBrowserResourceReadLimit
				}
				if reason != "" {
					return "", 0, errBrowserResourceReadMalformed
				}
			} else {
				raw, err = resourceParquetLegacyRow(values)
				if err != nil {
					return "", 0, err
				}
			}
			rows++
			if err := visit(raw, legacyMillisecond); err != nil {
				return "", 0, err
			}
		}
		for _, cursor := range cursors {
			if err := cursor.finish(); err != nil {
				return "", 0, err
			}
		}
	}
	if rows != physical.footer.NumRows {
		return "", 0, errBrowserResourceReadMalformed
	}
	return digest, size, nil
}
func resourceParquetLegacyRow(values map[string]any) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	total := 0
	for _, value := range values {
		if text, ok := value.(string); ok {
			if len(text) > maxBrowserResourceReadRecordBytes-total {
				return nil, errBrowserResourceReadLimit
			}
			total += len(text)
		}
	}
	for column, name := range map[string]string{"test_id": "test_id", "action_name": "action_name", "group_name": "group", "region": "region", "page_url": "page_url"} {
		if value, present := values[column]; present {
			text, ok := value.(string)
			if !ok {
				return nil, errBrowserResourceReadMalformed
			}
			encoded, _ := json.Marshal(text)
			fields[name] = encoded
		}
	}
	if value, present := values["timestamp_rfc3339"]; present {
		text, ok := value.(string)
		if !ok {
			return nil, errBrowserResourceReadMalformed
		}
		if text != "" {
			fields["timestamp"], _ = json.Marshal(text)
		}
	} else if value, present := values["timestamp_unix_ms"]; present {
		ms, ok := value.(int64)
		if !ok {
			return nil, errBrowserResourceReadMalformed
		}
		fields["timestamp"], _ = json.Marshal(time.UnixMilli(ms).UTC().Format(time.RFC3339Nano))
	}
	for column, name := range map[string]string{"client_metrics_json": "client_metrics", "network_entries_json": "network_entries", "snapshot_json": "snapshot"} {
		if value, present := values[column]; present {
			text, ok := value.(string)
			if !ok || len(text) > maxBrowserResourceReadRecordBytes {
				return nil, errBrowserResourceReadLimit
			}
			trimmed := bytes.TrimSpace([]byte(text))
			if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || name == "network_entries" && bytes.Equal(trimmed, []byte("[]")) {
				continue
			}
			if !json.Valid(trimmed) {
				return nil, errBrowserResourceReadMalformed
			}
			fields[name] = append([]byte(nil), trimmed...)
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil || len(raw) > maxBrowserResourceReadRecordBytes {
		return nil, errBrowserResourceReadLimit
	}
	return raw, nil
}

// A selected canonical column must retain its declared semantic type. In
// particular a microsecond/unadjusted logical timestamp cannot be rebound to
// the legacy timestamp_unix_ms epoch column merely because its bits are INT64.
func resourceParquetSelectedSemantic(field *parquet.SchemaElement, name string) bool {
	if field == nil || field.Type == nil {
		return false
	}
	if name == "timestamp_unix_ms" {
		if *field.Type != parquet.Type_INT64 || field.ConvertedType != nil && *field.ConvertedType != parquet.ConvertedType_TIMESTAMP_MILLIS {
			return false
		}
		if field.LogicalType != nil {
			kind := field.LogicalType.TIMESTAMP
			return field.LogicalType.CountSetFieldsLogicalType() == 1 && kind != nil && kind.IsAdjustedToUTC && kind.Unit != nil && kind.Unit.CountSetFieldsTimeUnit() == 1 && kind.Unit.MILLIS != nil
		}
		return true
	}
	return *field.Type == parquet.Type_BYTE_ARRAY && (field.ConvertedType == nil || *field.ConvertedType == parquet.ConvertedType_UTF8) && (field.LogicalType == nil || field.LogicalType.CountSetFieldsLogicalType() == 1 && field.LogicalType.STRING != nil)
}
