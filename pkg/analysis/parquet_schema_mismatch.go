package analysis

import (
	"fmt"
	"strconv"
	"strings"
)

// MappedColumns lists every column the mapping reads, each once, in a fixed
// order: the timestamp, the identity columns, then each measure's value,
// name, sample count, sample sum and histogram columns.
func (m ParquetMapping) MappedColumns() []ParquetColumnPath {
	var columns []ParquetColumnPath
	add := func(path ParquetColumnPath) {
		if len(path) == 0 {
			return
		}
		for _, existing := range columns {
			if slicesEqual(existing, path) {
				return
			}
		}
		columns = append(columns, path)
	}
	add(m.Timestamp)
	if m.Identity != nil {
		for _, path := range []ParquetColumnPath{m.Identity.Source, m.Identity.Host, m.Identity.Service} {
			add(path)
		}
		for _, dimension := range m.Identity.Dimensions {
			add(dimension.Value)
		}
	}
	for _, measure := range m.Measures {
		for _, path := range append([]ParquetColumnPath{measure.Value, measure.NameColumn, measure.SampleCountColumn, measure.SampleSumColumn}, measure.Histogram.Paths()...) {
			add(path)
		}
	}
	return columns
}

// Schema mismatch reasons: the file has no such column, holds it as a list or
// repeated field, or holds a group of fields there.
const (
	SchemaMismatchAbsent   = "absent"
	SchemaMismatchRepeated = "repeated"
	SchemaMismatchGroup    = "group"
)

// SchemaMismatchError says that one source file of an import does not hold a
// column the mapping reads as a scalar column. File indexes the import's files
// and Column the mapping's MappedColumns, so the error carries no source text.
type SchemaMismatchError struct {
	File   int
	Column int
	Reason string
}

func (e *SchemaMismatchError) Error() string {
	return fmt.Sprintf("source file %d does not hold mapped column %d as a scalar column (%s)", e.File, e.Column, e.Reason)
}

// Describe names the file and the column for a person: the file's import
// path and the mapped column path as the schema preview shows it.
func (e *SchemaMismatchError) Describe(file string, mapping ParquetMapping) string {
	column := fmt.Sprintf("#%d", e.Column+1)
	if columns := mapping.MappedColumns(); e.Column >= 0 && e.Column < len(columns) {
		column = EscapedColumnPath(columns[e.Column])
	}
	switch e.Reason {
	case SchemaMismatchRepeated:
		return fmt.Sprintf("File %q does not match the mapping: column %s is a list or repeated field, which cannot be mapped to a scalar measurement.", file, quotedName(column))
	case SchemaMismatchGroup:
		return fmt.Sprintf("File %q does not match the mapping: column %s is a group of fields; map one of its fields instead.", file, quotedName(column))
	}
	return fmt.Sprintf("File %q does not match the mapping: it has no column %s.", file, quotedName(column))
}

// ValidSchemaMismatchReason reports whether reason is one of the reasons above.
func ValidSchemaMismatchReason(reason string) bool {
	return reason == SchemaMismatchAbsent || reason == SchemaMismatchRepeated || reason == SchemaMismatchGroup
}

// SchemaMismatch is the first mapped column one file's schema does not hold
// as a scalar column, or nil when it holds every mapped column. A mapped path
// with fields below it is a group when any of them is a scalar field (map one
// of those), and otherwise a list or repeated field.
func (m ParquetMapping) SchemaMismatch(file int, schema ParquetSchemaPreview) *SchemaMismatchError {
	for index, path := range m.MappedColumns() {
		reason := SchemaMismatchAbsent
		below, scalarBelow := false, false
		for _, column := range schema.Columns {
			if slicesEqual(column.PathComponents, path) {
				reason = ""
				if column.Repeated {
					reason = SchemaMismatchRepeated
				}
				break
			}
			if len(column.PathComponents) > len(path) && slicesEqual(column.PathComponents[:len(path)], path) {
				below = true
				scalarBelow = scalarBelow || !column.Repeated
			}
		}
		if reason == SchemaMismatchAbsent && below {
			reason = SchemaMismatchRepeated
			if scalarBelow {
				reason = SchemaMismatchGroup
			}
		}
		if reason != "" {
			return &SchemaMismatchError{File: file, Column: index, Reason: reason}
		}
	}
	return nil
}

// MaxRejectedRowsNamed bounds the source row ordinals a RejectedRowsError
// carries.
const MaxRejectedRowsNamed = 5

// RejectedRowsError says that rows of one file had a measurement rejected
// while the import did not accept rejected measurements. File indexes the
// import's files and Measure the mapping's measures; Rows are the first
// zero-based source row ordinals (at most MaxRejectedRowsNamed), so the error
// carries no source values.
type RejectedRowsError struct {
	File    int
	Measure int
	Count   int64
	Rows    []int64
}

func (e *RejectedRowsError) Error() string {
	return fmt.Sprintf("file %d measure %d contains %d rejected rows; explicit acceptance required", e.File, e.Measure, e.Count)
}

// Describe names the file, the measure (its name, or its value column as the
// schema preview shows it) and the first rejected rows for a person.
func (e *RejectedRowsError) Describe(file string, mapping ParquetMapping) string {
	measure := fmt.Sprintf("#%d", e.Measure+1)
	if e.Measure >= 0 && e.Measure < len(mapping.Measures) {
		if declared := mapping.Measures[e.Measure]; declared.Name != "" {
			measure = declared.Name
		} else {
			measure = EscapedColumnPath(declared.Value)
		}
	}
	rows := make([]string, len(e.Rows))
	for index, row := range e.Rows {
		rows[index] = fmt.Sprint(row)
	}
	noun := "rows"
	if e.Count == 1 {
		noun = "row"
	}
	return fmt.Sprintf("File %q has %d %s whose measure %s was rejected (first source rows, counting from 0: %s). Accept rejected measurements to import the rest, or correct the file.", file, e.Count, noun, quotedName(measure), strings.Join(rows, ", "))
}

// quotedName quotes a name shown as the schema preview shows it, escaping only
// a double quote and what is not printable (control characters, line and
// paragraph separators, bidi controls), so its own escaping stays as it is and
// the message stays one unambiguous line.
func quotedName(name string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range name {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case !strconv.IsPrint(r) && r > 0xFFFF:
			fmt.Fprintf(&out, `\U%08x`, r)
		case !strconv.IsPrint(r):
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}
