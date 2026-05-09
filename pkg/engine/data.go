package engine

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jasonczech/stormrunner/pkg/dsl"
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
	mu       sync.Mutex // for unique strategy
	used     map[int]bool
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
	f, err := os.Open(src.Path)
	if err != nil {
		return nil, fmt.Errorf("open file %s: %w", src.Path, err)
	}
	defer f.Close()

	delimiter := ','
	if src.Delimiter != "" {
		delimiter = rune(src.Delimiter[0])
	}
	if src.Type == "dat" && src.Delimiter == "" {
		delimiter = '\t'
	}

	reader := csv.NewReader(f)
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
		used:     make(map[int]bool),
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

	return ds, nil
}

// GetRow returns a set of variable name → value pairs for the given VUser.
// The row selection depends on the strategy:
//   - sequential: round-robin through rows
//   - random: pick a random row each time
//   - unique: each VUser gets a distinct row (wraps if more VUsers than rows)
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
