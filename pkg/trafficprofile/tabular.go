package trafficprofile

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func ParseCSV(data []byte) (*Sheet, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(data) {
		return nil, errors.New("CSV must be UTF-8")
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = dsl.DetectDataSourceDelimiter(string(data))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	if len(records) < 2 {
		return nil, errors.New("CSV requires a header and at least one data row")
	}
	columns := normalizedColumns(records[0])
	if err := uniqueColumns(columns); err != nil {
		return nil, err
	}
	rows := make([][]string, 0, len(records)-1)
	for _, record := range records[1:] {
		row := make([]string, len(columns))
		copy(row, record)
		rows = append(rows, row)
	}
	return newSheet("CSV", columns, rows), nil
}

func newSheet(name string, columns []string, rows [][]string) *Sheet {
	samples := rows
	if len(samples) > 20 {
		samples = samples[:20]
	}
	return &Sheet{Name: name, Columns: columns, Rows: rows, Samples: samples}
}

func normalizedColumns(row []string) []string {
	columns := make([]string, len(row))
	for i, value := range row {
		value = strings.TrimSpace(value)
		if value == "" {
			value = fmt.Sprintf("column_%d", i+1)
		}
		columns[i] = value
	}
	return columns
}

func uniqueColumns(columns []string) error {
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		key := strings.ToLower(strings.TrimSpace(column))
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate column %q", column)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func SeriesFromSheet(sheet *Sheet, opts TableOptions) ([]Series, error) {
	if sheet == nil {
		return nil, errors.New("sheet is required")
	}
	timestampIndex := columnIndex(sheet.Columns, opts.TimestampColumn)
	if timestampIndex < 0 {
		return nil, fmt.Errorf("timestamp column %q was not found", opts.TimestampColumn)
	}
	if len(opts.ValueColumns) == 0 {
		return nil, errors.New("at least one value column is required")
	}
	location := opts.Timezone
	seriesColumnIndex := -1
	if opts.SeriesColumn != "" {
		seriesColumnIndex = columnIndex(sheet.Columns, opts.SeriesColumn)
		if seriesColumnIndex < 0 {
			return nil, fmt.Errorf("series column %q was not found", opts.SeriesColumn)
		}
	}
	result := make(map[string]*Series)
	order := make([]string, 0)
	for rowIndex, row := range sheet.Rows {
		if timestampIndex >= len(row) || strings.TrimSpace(row[timestampIndex]) == "" {
			continue
		}
		timestamp, err := ParseTimestamp(row[timestampIndex], location)
		if err != nil {
			return nil, fmt.Errorf("row %d timestamp: %w", rowIndex+2, err)
		}
		pivot := ""
		if seriesColumnIndex >= 0 && seriesColumnIndex < len(row) {
			pivot = strings.TrimSpace(row[seriesColumnIndex])
		}
		for _, column := range opts.ValueColumns {
			valueIndex := columnIndex(sheet.Columns, column)
			if valueIndex < 0 {
				return nil, fmt.Errorf("value column %q was not found", column)
			}
			if valueIndex >= len(row) || strings.TrimSpace(row[valueIndex]) == "" {
				continue
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(row[valueIndex]), 64)
			if err != nil {
				return nil, fmt.Errorf("row %d column %q must be numeric", rowIndex+2, column)
			}
			name := column
			if pivot != "" {
				name = pivot
				if len(opts.ValueColumns) > 1 {
					name += " / " + column
				}
			}
			key := strings.ToLower(strings.Join(strings.Fields(name), "_"))
			current := result[key]
			if current == nil {
				current = &Series{Key: key, Name: name, Points: make([]Point, 0)}
				result[key] = current
				order = append(order, key)
			} else if current.Name != name {
				return nil, fmt.Errorf("series names %q and %q normalize to the same key %q", current.Name, name, key)
			}
			current.Points = append(current.Points, Point{Timestamp: timestamp.UTC(), Value: value})
		}
	}
	out := make([]Series, 0, len(order))
	for _, key := range order {
		out = append(out, *result[key])
	}
	return out, nil
}

func columnIndex(columns []string, target string) int {
	for i, column := range columns {
		if strings.EqualFold(strings.TrimSpace(column), strings.TrimSpace(target)) {
			return i
		}
	}
	return -1
}

var timestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"1/2/2006 15:04:05",
	"1/2/2006 15:04",
	"1/2/06 15:04",
	"1/2/2006",
}

func ParseTimestamp(raw string, location *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("timestamp is empty")
	}
	if numeric, err := strconv.ParseFloat(raw, 64); err == nil {
		if math.IsNaN(numeric) || math.IsInf(numeric, 0) {
			return time.Time{}, errors.New("timestamp must be finite")
		}
		if numeric < math.MinInt64 || numeric > math.MaxInt64 {
			return time.Time{}, errors.New("numeric timestamp is outside the supported range")
		}
		var parsed time.Time
		if numeric >= 1e11 || numeric <= -1e11 {
			parsed = time.UnixMilli(int64(numeric)).UTC()
		} else {
			seconds := int64(numeric)
			nanos := int64((numeric - float64(seconds)) * float64(time.Second))
			parsed = time.Unix(seconds, nanos).UTC()
		}
		if parsed.Year() < 1 || parsed.Year() > 9999 {
			return time.Time{}, errors.New("numeric timestamp is outside the supported calendar range")
		}
		return parsed, nil
	}
	for _, layout := range timestampLayouts {
		var parsed time.Time
		var err error
		if strings.Contains(layout, "Z07") {
			parsed, err = time.Parse(layout, raw)
		} else {
			if location == nil {
				continue
			}
			parsed, err = time.ParseInLocation(layout, raw, location)
		}
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not RFC3339, ISO local time, Unix seconds, or Unix milliseconds", raw)
}
