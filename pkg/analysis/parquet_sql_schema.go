package analysis

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// ValidateParquetSQLSchema refuses physical names that DuckDB cannot bind
// exactly. Quoting does not make DuckDB identifiers or struct keys case
// sensitive: ASCII sibling collisions can rename fields or bind another value.
// Refuse the schema rather than guessing deduplicated names, including names
// whose generated suffix could collide with an otherwise distinct field.
func ValidateParquetSQLSchema(schema ParquetSchemaPreview) error {
	if len(schema.Columns) < 1 || len(schema.Columns) > maxParquetColumns {
		return errors.New("analytical schema column population exceeds bounds")
	}
	// A bounded trie retains each physical component once. Do not repeatedly
	// serialize full parent paths: deeply nested schemas would otherwise turn
	// a small admitted footer into a much larger temporary allocation.
	type field struct {
		name     string
		children map[string]*field
	}
	root := make(map[string]*field)
	for _, column := range schema.Columns {
		path := column.PathComponents
		if len(path) < 1 || len(path) > maxParquetNesting {
			return errors.New("analytical schema path exceeds bounds")
		}
		fields := root
		for index, part := range path {
			if part == "" || len(part) > 256 || !utf8.ValidString(part) || strings.ContainsRune(part, 0) {
				return errors.New("invalid analytical schema component")
			}
			folded := parquetSQLIdentifier(part)
			child, present := fields[folded]
			if present && child.name != part {
				return errors.New("case-ambiguous Parquet sibling fields cannot preserve exact analytical identity")
			}
			if !present {
				child = &field{name: part}
				fields[folded] = child
			}
			if index+1 < len(path) {
				if child.children == nil {
					child.children = make(map[string]*field)
				}
				fields = child.children
			}
		}
	}
	return nil
}

// DuckDB compares identifiers with ASCII folding, preserving non-ASCII bytes.
func parquetSQLIdentifier(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, name)
}
