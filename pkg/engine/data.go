package engine

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
)

// DataProvider loads and serves rows from CSV/DAT parameter files.
type DataProvider struct {
	sources map[string]*dataSource
	files   map[string]*fileDataSource
	shard   DataShardConfig
	// rng, when non-nil, makes the "random" strategy deterministic (replay). It is
	// set only via SeedRandom during replay setup (single goroutine); load runs
	// leave it nil and use the concurrency-safe global rand. rngMu guards rng.
	rngMu sync.Mutex
	rng   *rand.Rand
}

// DataShardConfig partitions one logical data source across distributed
// generator shards. Ordinal is zero-based; Count==0 is accepted only with
// Ordinal==0 and means the local/non-sharded default (Count=1).
type DataShardConfig struct {
	Ordinal uint32
	Count   uint32
}

type dataSource struct {
	name         string
	columns      []string
	rows         [][]string
	columnValues map[string][]string
	strategy     string
	inputBytes   int64
	cellCount    int64
	seqIdx       atomic.Int64
	mu           sync.Mutex
}

type fileDataSource struct {
	name        string
	fileName    string
	contentType string
	storageRef  string
	body        []byte
}

const (
	maxDataProviderSources    = 128
	maxDataProviderInputBytes = 128 << 20
	maxDataProviderCells      = 2_000_000
	maxDataSourceRecords      = 1_000_000
	maxDataSourceColumns      = 4_096
	maxDataSourceCells        = 2_000_000
)

// NewDataProvider creates a provider from DSL DataSource definitions.
func NewDataProvider(sources []dsl.DataSource) (*DataProvider, error) {
	return NewShardedDataProviderWithAssets(sources, DataShardConfig{}, nil)
}

// NewShardedDataProvider creates a provider whose sequential, random, unique,
// pop, foreach, and column views are deterministically partitioned by shard.
func NewShardedDataProvider(sources []dsl.DataSource, shard DataShardConfig) (*DataProvider, error) {
	return NewShardedDataProviderWithAssets(sources, shard, nil)
}

// NewShardedDataProviderWithAssets resolves staged_asset references only from
// the immutable, integrity-checked assignment asset map supplied by the
// generator boundary. It never reopens controller-local paths.
func NewShardedDataProviderWithAssets(sources []dsl.DataSource, shard DataShardConfig, assets map[string][]byte) (*DataProvider, error) {
	if len(sources) > maxDataProviderSources {
		return nil, fmt.Errorf("data provider exceeds %d source limit", maxDataProviderSources)
	}
	if shard.Count == 0 {
		if shard.Ordinal != 0 {
			return nil, fmt.Errorf("data shard ordinal %d requires a positive shard count", shard.Ordinal)
		}
		shard.Count = 1
	}
	if shard.Ordinal >= shard.Count {
		return nil, fmt.Errorf("data shard ordinal %d must be smaller than shard count %d", shard.Ordinal, shard.Count)
	}
	dp := &DataProvider{
		sources: make(map[string]*dataSource),
		files:   make(map[string]*fileDataSource),
		shard:   shard,
	}

	var totalInputBytes int64
	var totalCells int64
	for _, src := range sources {
		name := strings.TrimSpace(src.Name)
		if name == "" {
			return nil, fmt.Errorf("data source name is required")
		}
		if _, exists := dp.sources[name]; exists {
			return nil, fmt.Errorf("duplicate data source name %q", name)
		}
		if _, exists := dp.files[name]; exists {
			return nil, fmt.Errorf("duplicate data source name %q", name)
		}
		src.Name = name
		if strings.EqualFold(strings.TrimSpace(src.Type), "file") {
			fileSource, err := loadFileDataSource(src, assets)
			if err != nil {
				return nil, fmt.Errorf("file data source %q: %w", src.Name, err)
			}
			inputBytes := int64(len(fileSource.body))
			if inputBytes > maxDataProviderInputBytes-totalInputBytes {
				return nil, fmt.Errorf("data provider exceeds %d byte input limit", maxDataProviderInputBytes)
			}
			totalInputBytes += inputBytes
			dp.files[src.Name] = fileSource
			continue
		}
		ds, err := loadDataSource(src, assets)
		if err != nil {
			return nil, fmt.Errorf("data source %q: %w", src.Name, err)
		}
		if ds.inputBytes > maxDataProviderInputBytes-totalInputBytes {
			return nil, fmt.Errorf("data provider exceeds %d byte input limit", maxDataProviderInputBytes)
		}
		if ds.cellCount > maxDataProviderCells-totalCells {
			return nil, fmt.Errorf("data provider exceeds %d parsed-cell limit", maxDataProviderCells)
		}
		totalInputBytes += ds.inputBytes
		totalCells += ds.cellCount
		dp.sources[src.Name] = ds
	}

	return dp, nil
}

func loadFileDataSource(src dsl.DataSource, assets map[string][]byte) (*fileDataSource, error) {
	readerSource, err := openFileDataSourceReader(src, assets)
	if err != nil {
		return nil, err
	}
	defer readerSource.Close()

	content, err := fileasset.ReadAllLimited(readerSource)
	if err != nil {
		return nil, fmt.Errorf("read file asset: %w", err)
	}
	return &fileDataSource{
		name:        src.Name,
		fileName:    src.FileName,
		contentType: src.ContentType,
		storageRef:  src.StorageRef,
		body:        content,
	}, nil
}

func loadDataSource(src dsl.DataSource, assets map[string][]byte) (*dataSource, error) {
	readerSource, err := openDataSourceReader(src, assets)
	if err != nil {
		return nil, err
	}
	defer readerSource.Close()

	content, err := fileasset.ReadAllLimited(readerSource)
	if err != nil {
		return nil, fmt.Errorf("read data source: %w", err)
	}
	delimiter := resolveDataSourceDelimiter(src, string(content))

	reader := csv.NewReader(bytes.NewReader(content))
	reader.Comma = delimiter
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records := make([][]string, 0, min(1024, maxDataSourceRecords))
	cellCount := 0
	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read CSV: %w", readErr)
		}
		if len(records) >= maxDataSourceRecords {
			return nil, fmt.Errorf("CSV exceeds %d record limit", maxDataSourceRecords)
		}
		if len(record) > maxDataSourceColumns {
			return nil, fmt.Errorf("CSV record exceeds %d column limit", maxDataSourceColumns)
		}
		if len(record) > maxDataSourceCells-cellCount {
			return nil, fmt.Errorf("CSV exceeds %d parsed-cell limit", maxDataSourceCells)
		}
		cellCount += len(record)
		records = append(records, record)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("file is empty")
	}

	ds := &dataSource{
		name:       src.Name,
		strategy:   src.Strategy,
		inputBytes: int64(len(content)),
		cellCount:  int64(cellCount),
	}

	if src.FirstRowHeader {
		ds.columns = records[0]
		ds.rows = records[1:]
	} else if len(src.Columns) > 0 {
		ds.columns = src.Columns
		ds.rows = records
	} else {
		// Auto-generate column names: col_1, col_2, ...
		if len(records) > 0 {
			cols := make([]string, len(records[0]))
			for i := range cols {
				cols[i] = fmt.Sprintf("col_%d", i+1)
			}
			ds.columns = cols
		}
		ds.rows = records
	}

	// Clean column names
	for i, c := range ds.columns {
		ds.columns[i] = strings.TrimSpace(c)
	}

	if len(ds.rows) == 0 {
		return nil, fmt.Errorf("no data rows found")
	}
	ds.columnValues = make(map[string][]string, len(ds.columns))
	for columnIndex, column := range ds.columns {
		column = strings.TrimSpace(column)
		if _, duplicate := ds.columnValues[column]; duplicate {
			continue
		}
		values := make([]string, len(ds.rows))
		for rowIndex, row := range ds.rows {
			if columnIndex < len(row) {
				values[rowIndex] = strings.TrimSpace(row[columnIndex])
			}
		}
		ds.columnValues[column] = values
	}

	if ds.strategy == "" {
		ds.strategy = "sequential"
	}
	switch ds.strategy {
	case "sequential", "random", "unique", "pop":
	default:
		return nil, fmt.Errorf("unsupported data source strategy %q", ds.strategy)
	}

	return ds, nil
}

func resolveDataSourceDelimiter(src dsl.DataSource, content string) rune {
	// Delegates to pkg/dsl so script validation and the runtime loader can never
	// disagree about which delimiter a data source uses.
	return dsl.ResolveDataSourceDelimiter(src, content)
}

func detectDataSourceDelimiter(content string) rune {
	return dsl.DetectDataSourceDelimiter(content)
}

func scoreDataSourceDelimiter(content string, delimiter rune) int {
	return dsl.ScoreDataSourceDelimiter(content, delimiter)
}

func openDataSourceReader(src dsl.DataSource, assets map[string][]byte) (io.ReadCloser, error) {
	if staged := strings.TrimSpace(src.StagedAsset); staged != "" {
		content, ok := assets[staged]
		if !ok {
			return nil, fmt.Errorf("staged asset %q is missing", staged)
		}
		return io.NopCloser(bytes.NewReader(content)), nil
	}
	if strings.TrimSpace(src.InlineData) != "" {
		return io.NopCloser(strings.NewReader(src.InlineData)), nil
	}
	if strings.TrimSpace(src.Path) == "" {
		return nil, fmt.Errorf("either path or inline_data is required")
	}
	return fileasset.OpenPath(src.Path)
}

func openFileDataSourceReader(src dsl.DataSource, assets map[string][]byte) (io.ReadCloser, error) {
	if staged := strings.TrimSpace(src.StagedAsset); staged != "" {
		content, ok := assets[staged]
		if !ok {
			return nil, fmt.Errorf("staged asset %q is missing", staged)
		}
		return io.NopCloser(bytes.NewReader(content)), nil
	}
	if strings.TrimSpace(src.InlineData) != "" {
		switch strings.ToLower(strings.TrimSpace(src.Encoding)) {
		case "", "text":
			return io.NopCloser(strings.NewReader(src.InlineData)), nil
		case "base64":
			decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(src.InlineData), ""))
			if err != nil {
				return nil, fmt.Errorf("decode inline_data base64: %w", err)
			}
			return io.NopCloser(strings.NewReader(string(decoded))), nil
		default:
			return nil, fmt.Errorf("unsupported file encoding %q", src.Encoding)
		}
	}
	if strings.TrimSpace(src.Path) != "" {
		return fileasset.OpenPath(src.Path)
	}
	if strings.TrimSpace(src.StorageRef) != "" {
		return fileasset.OpenStorageRef(src.StorageRef)
	}
	return nil, fmt.Errorf("inline_data, path, or storage_ref is required")
}

// GetRow returns a set of variable name → value pairs for the given VUser.
// The row selection depends on the strategy:
//   - sequential: round-robin through rows
//   - random: pick a random row each time
//   - unique: each VUser gets a distinct row (wraps if more VUsers than rows)
//   - pop: globally consume rows in order and stop returning rows after exhaustion
func (dp *DataProvider) GetRow(sourceName string, vuserID int) map[string]string {
	ds, ok := dp.sources[sourceName]
	if !ok || len(ds.rows) == 0 {
		return nil
	}

	var rowIdx int

	switch ds.strategy {
	case "sequential":
		idx := ds.seqIdx.Add(1) - 1
		rowIdx = dp.shardedRowIndex(len(ds.rows), idx, true)

	case "random":
		eligible := dp.shardedRowCount(len(ds.rows))
		if eligible == 0 {
			return nil
		}
		rowIdx = dp.shardedRowIndex(len(ds.rows), int64(dp.randIntn(eligible)), false)

	case "unique":
		// Select only from this shard's partition. Distributed VUser IDs live in
		// large, disjoint namespaces and therefore cannot safely be reduced modulo
		// the full source: doing so can make two shards select the same row.
		eligible := dp.shardedRowCount(len(ds.rows))
		if eligible == 0 {
			return nil
		}
		if vuserID < 0 {
			vuserID = 0
		}
		rowIdx = dp.shardedRowIndex(len(ds.rows), int64(vuserID%eligible), false)

	case "pop":
		ds.mu.Lock()
		defer ds.mu.Unlock()
		idx := ds.seqIdx.Load()
		if idx >= int64(dp.shardedRowCount(len(ds.rows))) {
			return nil
		}
		rowIdx = dp.shardedRowIndex(len(ds.rows), idx, false)
		ds.seqIdx.Add(1)

	default:
		rowIdx = 0
	}
	if rowIdx < 0 || rowIdx >= len(ds.rows) {
		return nil
	}

	row := ds.rows[rowIdx]
	result := make(map[string]string, len(ds.columns))
	for i, col := range ds.columns {
		if i < len(row) {
			result[ds.name+"."+col] = strings.TrimSpace(row[i])
		}
	}

	return result
}

func (dp *DataProvider) shardedRowCount(total int) int {
	if dp == nil || total <= 0 {
		return 0
	}
	ordinal := uint64(dp.shard.Ordinal)
	if ordinal >= uint64(total) {
		return 0
	}
	count := uint64(dp.shard.Count)
	return int((uint64(total)-1-ordinal)/count + 1)
}

func (dp *DataProvider) shardedRowIndex(total int, localIndex int64, wrap bool) int {
	eligible := dp.shardedRowCount(total)
	if eligible == 0 || localIndex < 0 {
		return -1
	}
	if wrap {
		localIndex %= int64(eligible)
	} else if localIndex >= int64(eligible) {
		return -1
	}
	return int(uint64(dp.shard.Ordinal) + uint64(localIndex)*uint64(dp.shard.Count))
}

// SeedRandom makes the "random" data-selection strategy reproducible by backing
// it with a deterministically seeded generator, so replaying with the same seed
// reselects the same rows. Call before execution; load runs never call it.
func (dp *DataProvider) SeedRandom(seed int64) {
	if dp == nil {
		return
	}
	dp.rngMu.Lock()
	dp.rng = rand.New(rand.NewSource(seed))
	dp.rngMu.Unlock()
}

// randIntn returns a random index in [0,n). With a deterministic seed installed
// (replay) it draws from the seeded generator under rngMu; otherwise it uses the
// global, mutex-safe math/rand source (the load hot path).
func (dp *DataProvider) randIntn(n int) int {
	if dp.rng != nil {
		dp.rngMu.Lock()
		defer dp.rngMu.Unlock()
		return dp.rng.Intn(n)
	}
	return rand.Intn(n)
}

// GetAllRows returns every row of a data source in order as raw column→value
// maps (column names only, with no "source." prefix). Unlike GetRow it never
// advances the per-VUser/per-source cursor, so foreach control_loop blocks can
// iterate a source's full contents within a single VUser iteration without
// disturbing the per-iteration {{data.src.col}} binding. Returns nil when the
// provider or source is missing.
func (dp *DataProvider) GetAllRows(sourceName string) []map[string]string {
	view := dp.rowView(sourceName)
	rows := make([]map[string]string, 0, view.Len())
	for i := 0; i < view.Len(); i++ {
		rows = append(rows, view.Row(i))
	}
	return rows
}

// dataRowView is an immutable indexed view over provider-owned rows. Foreach
// loops use it to apply their pass cap before allocating any row maps.
type dataRowView struct {
	source *dataSource
	shard  DataShardConfig
}

func (dp *DataProvider) rowView(sourceName string) dataRowView {
	if dp == nil {
		return dataRowView{}
	}
	return dataRowView{source: dp.sources[sourceName], shard: dp.shard}
}

func (view dataRowView) Len() int {
	if view.source == nil {
		return 0
	}
	return shardedViewRowCount(len(view.source.rows), view.shard)
}

func (view dataRowView) Columns() []string {
	if view.source == nil {
		return nil
	}
	return view.source.columns
}

func (view dataRowView) Row(index int) map[string]string {
	if view.source == nil || index < 0 || index >= view.Len() {
		return nil
	}
	rowIndex := int(uint64(view.shard.Ordinal) + uint64(index)*uint64(view.shard.Count))
	row := view.source.rows[rowIndex]
	out := make(map[string]string, min(len(view.source.columns), len(row)))
	for i, column := range view.source.columns {
		if i >= len(row) {
			break
		}
		out[column] = strings.TrimSpace(row[i])
	}
	return out
}

func shardedViewRowCount(total int, shard DataShardConfig) int {
	if total <= 0 || uint64(shard.Ordinal) >= uint64(total) {
		return 0
	}
	count := shard.Count
	if count == 0 {
		count = 1
	}
	return int((uint64(total)-1-uint64(shard.Ordinal))/uint64(count) + 1)
}

// SourceNames returns the names of all loaded data sources.
func (dp *DataProvider) SourceNames() []string {
	names := make([]string, 0, len(dp.sources))
	for name := range dp.sources {
		names = append(names, name)
	}
	return names
}

// HasSources returns true if any data sources are configured.
func (dp *DataProvider) HasSources() bool {
	return len(dp.sources) > 0
}

// GetColumnValues returns all values for a given source column in row order.
func (dp *DataProvider) GetColumnValues(sourceName, column string) ([]string, bool) {
	if dp == nil {
		return nil, false
	}
	ds, ok := dp.sources[sourceName]
	if !ok {
		return nil, false
	}
	values, ok := ds.columnValues[strings.TrimSpace(column)]
	if !ok {
		return nil, false
	}
	count := dp.shardedRowCount(len(values))
	if count == len(values) {
		// Immutable provider-owned storage: callers may retain subslices but must
		// not mutate the returned values.
		return values, true
	}
	partition := make([]string, 0, count)
	for index := 0; index < count; index++ {
		rowIndex := dp.shardedRowIndex(len(values), int64(index), false)
		partition = append(partition, values[rowIndex])
	}
	return partition, true
}

// GetFileContent returns the bytes for a file data source as a Go string. Go
// strings can carry arbitrary bytes, which lets template expansion splice the
// original asset into HTTP bodies without UTF-8 coercion.
func (dp *DataProvider) GetFileContent(name string) (string, bool) {
	if dp == nil {
		return "", false
	}
	fileSource, ok := dp.files[strings.TrimSpace(name)]
	if !ok || fileSource == nil {
		return "", false
	}
	// Convert on demand rather than keeping a permanent second copy of the file
	// content: a large data file (up to 512MiB) would otherwise hold ~2x its size
	// in generator memory for the whole run even when this accessor is never used.
	return string(fileSource.body), true
}
