//go:build analytics_duckdb && cgo

package duckdb

import (
	"errors"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Every expression is generated from a validated scalar column path or a
// quoted constant. Identity values never become SQL identifiers or expressions.
func nativeIdentityProjection(mapping Mapping, dataset string) (projection, canonical, valid string) {
	identity := mapping.Identity
	source, host, service := literal(dataset), literal(""), literal("")
	if identity != nil {
		if len(identity.Source) > 0 {
			source = "try_cast(" + column(identity.Source) + " AS VARCHAR)"
		}
		if len(identity.Host) > 0 {
			host = "try_cast(" + column(identity.Host) + " AS VARCHAR)"
		}
		if len(identity.Service) > 0 {
			service = "try_cast(" + column(identity.Service) + " AS VARCHAR)"
		}
	}
	projection = source + " AS source, " + host + " AS host, " + service + " AS service"
	valid = nativeRequiredIdentityText("source", 256) + " AND host IS NOT NULL AND octet_length(encode(host))<=256 AND NOT contains(host,chr(0)) AND service IS NOT NULL AND octet_length(encode(service))<=256 AND NOT contains(service,chr(0))"
	dimensions := literal("{}")
	if identity != nil && len(identity.Dimensions) > 0 {
		names, values := []string{}, []string{}
		for i, d := range identity.Dimensions {
			alias := "identity_dimension_" + strconv.Itoa(i)
			projection += ", try_cast(" + column(d.Value) + " AS VARCHAR) AS " + alias
			names = append(names, literal(d.Name))
			values = append(values, alias)
			valid += " AND " + alias + " IS NOT NULL AND octet_length(encode(" + alias + "))<=128 AND NOT contains(" + alias + ",chr(0))"
		}
		dimensions = "to_json(map([" + strings.Join(names, ",") + "],[" + strings.Join(values, ",") + "]))::VARCHAR"
	}
	canonical = strconv.Itoa(mapping.Version) + " AS identity_version, source, host, service, " + dimensions + " AS dimensions_json"
	return projection, canonical, valid
}
func nativeIdentityTable(paths []string) (string, error) {
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		schema, err := analysis.InspectParquet(path)
		if err != nil {
			return "", err
		}
		if err := analysis.ValidateParquetSQLSchema(schema); err != nil {
			return "", err
		}
		columns := map[string]bool{}
		for _, c := range schema.Columns {
			if len(c.PathComponents) == 1 {
				columns[c.PathComponents[0]] = true
			}
		}
		for _, name := range []string{"schema_version", "project_id", "dataset_id", "source", "timestamp_utc", "metric_name", "kind", "unit", "value", "quality"} {
			if !columns[name] {
				return "", errors.New("normalized artifact lacks canonical measurement column")
			}
		}
		identity := "1 AS identity_version, '' AS host, '' AS service, '{}' AS dimensions_json"
		if columns["identity_version"] {
			for _, name := range []string{"host", "service", "dimensions_json"} {
				if !columns[name] {
					return "", errors.New("normalized artifact lacks canonical identity column")
				}
			}
			identity = "identity_version, host, service, dimensions_json"
		} else if columns["host"] || columns["service"] || columns["dimensions_json"] {
			return "", errors.New("normalized artifact has unversioned identity columns")
		}
		// Only declared summaries write summary columns; every other artifact,
		// including all earlier ones, reads as having no declaration.
		summary := "'' AS summary_statistic, CAST(0 AS INTEGER) AS summary_window_seconds, CAST(NULL AS DOUBLE) AS summary_sample_count, CAST(NULL AS DOUBLE) AS summary_sample_sum"
		present := 0
		for _, name := range nativeSummaryColumns {
			if columns[name] {
				present++
			}
		}
		if present == len(nativeSummaryColumns) {
			summary = strings.Join(nativeSummaryColumns, ", ")
		} else if present != 0 {
			return "", errors.New("normalized artifact has incomplete summary columns")
		}
		parts = append(parts, "SELECT schema_version, project_id, dataset_id, source, timestamp_utc, metric_name, kind, unit, value, quality, "+identity+", "+summary+" FROM read_parquet("+literal(path)+", hive_partitioning=false)")
	}
	return "(" + strings.Join(parts, " UNION ALL ") + ")", nil
}
func nativeIdentityFilter(q SeriesQuery) string {
	filter := ""
	for _, pair := range [][2]string{{"source", q.SourceID}, {"host", q.Host}, {"service", q.Service}} {
		if pair[1] != "" {
			filter += " AND " + pair[0] + "=" + literal(pair[1])
		}
	}
	// RFC 6901 pointers select an exact key, including slashes/quotes/tildes;
	// neither JSONPath syntax nor SQL can be supplied by a dimension name.
	for key, value := range q.Dimensions {
		pointer := "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		filter += " AND json_extract_string(dimensions_json," + literal(pointer) + ")=" + literal(value)
	}
	return filter
}

// Only internally approved column aliases enter this helper. Its required text
// semantics match Go strings.TrimSpace, including Unicode White Space.
func nativeRequiredIdentityText(alias string, maxBytes int) string {
	space := literal("\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000")
	return alias + " IS NOT NULL AND length(trim(" + alias + "," + space + "))>=1 AND octet_length(encode(" + alias + "))<=" + strconv.Itoa(maxBytes) + " AND NOT contains(" + alias + ",chr(0))"
}

var nativeSummaryColumns = []string{"summary_statistic", "summary_window_seconds", "summary_sample_count", "summary_sample_sum"}

// nativeSummaryProjection maps a declared summary's sample columns and writes
// its declaration into each canonical row. Sample sums share the value unit
// and scale; sample counts are unitless. Other measures add nothing.
func nativeSummaryProjection(measure Measure, scale string) (projection, canonical, valid string) {
	if measure.Kind != analysis.KindSummary || measure.Statistic == "" {
		return "", "", ""
	}
	sample := func(alias string, path Column, scaled bool) string {
		if len(path) == 0 {
			return ", CAST(NULL AS DOUBLE) AS " + alias + ", TRUE AS " + alias + "_was_null"
		}
		expr := "try_cast(" + column(path) + " AS DOUBLE)"
		if scaled {
			expr += "*" + scale
		}
		return ", " + expr + " AS " + alias + ", (" + column(path) + " IS NULL) AS " + alias + "_was_null"
	}
	projection = sample("sample_count", measure.SampleCountColumn, false) + sample("sample_sum", measure.SampleSumColumn, true)
	canonical = ", " + literal(measure.Statistic) + " AS summary_statistic, CAST(" + strconv.Itoa(measure.WindowSeconds) + " AS INTEGER) AS summary_window_seconds, CASE WHEN was_null THEN NULL ELSE sample_count END AS summary_sample_count, CASE WHEN was_null THEN NULL ELSE sample_sum END AS summary_sample_sum"
	valid = " AND (sample_count_was_null OR (sample_count IS NOT NULL AND isfinite(sample_count) AND sample_count>=0)) AND (sample_sum_was_null OR (sample_sum IS NOT NULL AND isfinite(sample_sum)))"
	return projection, canonical, valid
}

// nativeSummaryValueValid is a summary's rule on its value itself: a count
// is never negative. It belongs to the row's value, not to its samples.
func nativeSummaryValueValid(measure Measure) string {
	if measure.Kind == analysis.KindSummary && measure.Statistic == "count" {
		return " AND (was_null OR value>=0)"
	}
	return ""
}

// nativeSummarySamplesValid admits canonical summary sample columns.
const nativeSummarySamplesValid = "(summary_sample_count IS NULL OR (isfinite(summary_sample_count) AND summary_sample_count>=0)) AND (summary_sample_sum IS NULL OR isfinite(summary_sample_sum))"
