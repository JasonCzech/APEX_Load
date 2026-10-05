//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type nativeExplainNode struct {
	Name         string                     `json:"name"`
	OperatorName string                     `json:"operator_name"`
	OperatorType string                     `json:"operator_type"`
	Extra        map[string]json.RawMessage `json:"extra_info"`
	Children     []nativeExplainNode        `json:"children"`
	Cardinality  int64                      `json:"operator_cardinality"`
	RowsScanned  int64                      `json:"operator_rows_scanned"`
}

func nativeExplainReadNodes(nodes []nativeExplainNode) []nativeExplainNode {
	var reads []nativeExplainNode
	for _, node := range nodes {
		if strings.Contains(strings.ToUpper(node.Name+node.OperatorName), "READ_PARQUET") {
			reads = append(reads, node)
		}
		reads = append(reads, nativeExplainReadNodes(node.Children)...)
	}
	return reads
}
func nativeExplainInfoStrings(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		t.Fatal("pinned native scan explanation has unrecognized scalar shape")
	}
	return many
}
func TestNativeSeriesExactProductionSQLProjectionAndPushdownExplain(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000000+i AS ts, CASE i WHEN 10 THEN NULL ELSE i END::DOUBLE AS value, 'agent' AS source, CASE WHEN i%2=0 THEN 'host-a' ELSE 'host-b' END AS host, 'svc' AS service, 'east' AS region, CASE WHEN i%3=0 THEN 'cpu' ELSE 'other' END AS metric FROM range(64) t(i)")
	mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Identity: &analysis.ParquetIdentityMapping{Source: Column{"source"}, Host: Column{"host"}, Service: Column{"service"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "region", Value: Column{"region"}}}}, Measures: []Measure{{NameColumn: Column{"metric"}, Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}, {Name: "counter", Value: Column{"value"}, Kind: analysis.KindCounter, Unit: "count"}}}
	result, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "cpu", IdentitySchemaVersion: 2, SourceID: "agent", Host: "host-a", Service: "svc", Dimensions: map[string]string{"region": "east"}, From: time.Unix(1700000003, 0).UTC(), To: time.Unix(1700000057, 0).UTC(), BucketSeconds: 6, Aggregation: "avg"}
	for i, kind := range []string{analysis.KindGauge, analysis.KindCounter} {
		query := q
		if kind == analysis.KindCounter {
			query.Metric = "counter"
			query.Aggregation = "rate"
		}
		t.Run(kind, func(t *testing.T) {
			table, err := nativeIdentityTable([]string{result.Files[i]})
			if err != nil {
				t.Fatal(err)
			}
			aggregation, err := nativeSeriesAggregationSQL(table, query, kind)
			if err != nil {
				t.Fatal(err)
			}
			statements := []struct{ name, sql string }{{"quality", nativeSeriesQualitySQL(table, query)}, {"metadata", nativeSeriesMetadataSQL(table, query)}, {"aggregation", aggregation}}
			if err = e.withDB(t.Context(), func(ctx context.Context, db *sql.DB) error {
				for _, statement := range statements {
					var key, raw string
					if err := db.QueryRowContext(ctx, "EXPLAIN (FORMAT JSON) "+statement.sql).Scan(&key, &raw); err != nil {
						return err
					}
					if key != "physical_plan" || len(raw) > 512<<10 {
						t.Fatal("unbounded/unrecognized pinned physical explanation")
					}
					var nodes []nativeExplainNode
					if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
						t.Fatal(err)
					}
					reads := nativeExplainReadNodes(nodes)
					if len(reads) != 1 {
						t.Fatal("exact production query did not expose one genuine Parquet scan", raw)
					}
					projections := nativeExplainInfoStrings(t, reads[0].Extra["Projections"])
					filters := nativeExplainInfoStrings(t, reads[0].Extra["Filters"])
					filterText := strings.Join(filters, " ")
					for _, column := range []string{"timestamp_utc", "host"} {
						if !strings.Contains(filterText, column) {
							t.Fatal("simple predicate did not reach genuine Parquet scan", statement.name, column, raw)
						}
					}
					if kind == analysis.KindGauge && !strings.Contains(filterText, "metric_name") {
						t.Fatal("long-table metric predicate did not reach scan", raw)
					}
					if statement.name == "aggregation" {
						joined := "," + strings.Join(projections, ",") + ","
						for _, needed := range []string{"value", "timestamp_utc"} {
							if !strings.Contains(joined, ","+needed+",") {
								t.Fatal("production aggregate scan lost required projection", needed, raw)
							}
						}
						for _, unused := range []string{"schema_version", "kind", "unit", "identity_version"} {
							if strings.Contains(joined, ","+unused+",") {
								t.Fatal("unused canonical metadata was physically projected by aggregate scan", unused, raw)
							}
						}
					}
					t.Logf("production_%s_%s_physical_plan=%s", kind, statement.name, raw)
				}
				var key, raw string
				if err := db.QueryRowContext(ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+aggregation).Scan(&key, &raw); err != nil {
					return err
				}
				if len(raw) > 512<<10 {
					t.Fatal("excessive actual scan profile")
				}
				var profile nativeExplainNode
				if err := json.Unmarshal([]byte(raw), &profile); err != nil {
					t.Fatal(err)
				}
				reads := nativeExplainReadNodes([]nativeExplainNode{profile})
				if len(reads) != 1 || reads[0].RowsScanned <= 0 || reads[0].Cardinality <= 0 {
					t.Fatal("actual production query scan counters unavailable", raw)
				}
				t.Logf("production_%s_actual_scan_rows=%d post_filter_rows=%d", kind, reads[0].RowsScanned, reads[0].Cardinality)
				t.Logf("production_%s_actual_profile=%s", kind, raw)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
