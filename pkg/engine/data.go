package engine

import (
	"encoding/csv"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// DataProvider loads and serves rows from CSV/DAT parameter files.
type DataProvider struct {
	sources map[string]*dataSource
}

type dataSource struct {
	name     string
	columns  []string
	rows     [][]string
	strategy string
	seqIdx   atomic.Int64
	mu       sync.Mutex
}

// NewDataProvider creates a provider from DSL DataSource definitions.
func NewDataProvider(sources []dsl.DataSource) (*DataProvider, error) {
	dp := &DataProvider{
		sources: make(map[string]*dataSource),
	}

	for _, src := range sources {
		ds, err := loadDataSource(src)
		if err != nil {
			return nil, fmt.Errorf("data source %q: %w", src.Name, err)
		}
		dp.sources[src.Name] = ds
	}

	return dp, nil
}

func loadDataSource(src dsl.DataSource) (*dataSource, error) {
	readerSource, err := openDataSourceReader(src)
	if err != nil {
		return nil, err
	}
	defer readerSource.Close()

	content, err := io.ReadAll(readerSource)
	if err != nil {
		return nil, fmt.Errorf("read data source: %w", err)
	}
	delimiter := resolveDataSourceDelimiter(src, string(content))

	reader := csv.NewReader(strings.NewReader(string(content)))
	reader.Comma = delimiter
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read CSV: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("file is empty")
	}

	ds := &dataSource{
		name:     src.Name,
		strategy: src.Strategy,
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
	if src.Delimiter == "\t" {
		return '\t'
	}
	delimiter := strings.TrimSpace(src.Delimiter)
	switch strings.ToLower(delimiter) {
	case "":
		if src.Type == "dat" {
			return '\t'
		}
		if detected := detectDataSourceDelimiter(content); detected != 0 {
			return detected
		}
		return ','
	case "auto":
		if detected := detectDataSourceDelimiter(content); detected != 0 {
			return detected
		}
		if src.Type == "dat" {
			return '\t'
		}
		return ','
	case `\t`, "tab":
		return '\t'
	case "comma":
		return ','
	default:
		return []rune(delimiter)[0]
	}
}

func detectDataSourceDelimiter(content string) rune {
	commaScore := scoreDataSourceDelimiter(content, ',')
	tabScore := scoreDataSourceDelimiter(content, '\t')
	if tabScore == 0 && commaScore == 0 {
		return 0
	}
	if tabScore > commaScore {
		return '\t'
	}
	return ','
}

func scoreDataSourceDelimiter(content string, delimiter rune) int {
	score := 0
	for index, line := range strings.Split(content, "\n") {
		if index >= 20 {
			break
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		count := strings.Count(line, string(delimiter))
		if count > 0 {
			score += count + 1
		}
	}
	return score
}

func openDataSourceReader(src dsl.DataSource) (io.ReadCloser, error) {
	if strings.TrimSpace(src.InlineData) != "" {
		return io.NopCloser(strings.NewReader(src.InlineData)), nil
	}
	if strings.TrimSpace(src.Path) == "" {
		return nil, fmt.Errorf("either path or inline_data is required")
	}
	f, err := os.Open(src.Path)
	if err != nil {
		return nil, fmt.Errorf("open file %s: %w", src.Path, err)
	}
	return f, nil
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
		rowIdx = int(idx) % len(ds.rows)

	case "random":
		rowIdx = rand.Intn(len(ds.rows))

	case "unique":
		// Each VUser gets its own row; wraps around if needed
		rowIdx = vuserID % len(ds.rows)

	case "pop":
		ds.mu.Lock()
		defer ds.mu.Unlock()
		idx := int(ds.seqIdx.Load())
		if idx >= len(ds.rows) {
			return nil
		}
		rowIdx = idx
		ds.seqIdx.Add(1)

	default:
		rowIdx = 0
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
	column = strings.TrimSpace(column)
	columnIndex := -1
	for i, candidate := range ds.columns {
		if strings.TrimSpace(candidate) == column {
			columnIndex = i
			break
		}
	}
	if columnIndex < 0 {
		return nil, false
	}

	values := make([]string, 0, len(ds.rows))
	for _, row := range ds.rows {
		if columnIndex >= len(row) {
			values = append(values, "")
			continue
		}
		values = append(values, strings.TrimSpace(row[columnIndex]))
	}
	return values, true
}
