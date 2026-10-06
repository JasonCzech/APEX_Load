//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNativePartitionProducerPhysicalCountsIdentityAndDigest(t *testing.T) {
	e, path := nativeFixture(t, "SELECT '2026-01-01T00:00:00.123456Z' AS ts, CASE i WHEN 0 THEN NULL ELSE 7::DOUBLE END AS value, 'agent' AS source, 'host-a' AS host, 'api' AS service, CASE i WHEN 0 THEN '' ELSE 'east' END AS region, CASE i WHEN 0 THEN 'latency' ELSE 'throughput' END AS metric FROM range(2) t(i)")
	mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp_offset", Timezone: "UTC", Identity: &analysis.ParquetIdentityMapping{Source: Column{"source"}, Host: Column{"host"}, Service: Column{"service"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "region", Value: Column{"region"}}}}, Measures: []Measure{{Name: "value", Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}, {NameColumn: Column{"metric"}, Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}}}
	result, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted != 4 || len(result.Files) != 2 || len(result.Partitions) != 2 {
		t.Fatal("accepted measures did not reconcile with physical partitions", result)
	}
	var rows int64
	for i, part := range result.Partitions {
		schema, err := analysis.InspectParquet(part.File)
		if err != nil || part.File != result.Files[i] || schema.Rows != 2 || part.Stats.Rows != schema.Rows || part.Stats.Valid != 1 || part.Stats.Missing != 1 || part.Stats.Validate() != nil {
			t.Fatal("physical evidence did not match producer summary", part, schema, err)
		}
		f, err := e.OpenOutput(part.File)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.New()
		n, copyErr := io.Copy(hash, f)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil || n != part.Bytes || hex.EncodeToString(hash.Sum(nil)) != part.SHA256 {
			t.Fatal("summary was not bound to exact parent-owned bytes", copyErr, closeErr)
		}
		rows += part.Stats.Rows
		if part.Stats.Identity != nil {
			t.Fatal("mixed full identity became a constant proof")
		}
		if i == 0 && (part.Stats.Metric == nil || *part.Stats.Metric != "value") || i == 1 && part.Stats.Metric != nil {
			t.Fatal("mixed metric name became a constant proof")
		}
	}
	if rows != result.Accepted {
		t.Fatal("accepted original measures differ from physical row population")
	}
}

func TestNativePartitionProducerNanosecondSelectionKeepsPhysicalBoundary(t *testing.T) {
	for _, text := range []string{"2026-01-01T00:00:00.123456Z", "1969-12-31T23:59:59.999999Z"} {
		t.Run(text, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339Nano, text)
			if err != nil {
				t.Fatal(err)
			}
			e, path := nativeFixture(t, "SELECT "+literal(text)+" AS ts, NULL::DOUBLE AS value")
			mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp_offset", Timezone: "UTC", Measures: []Measure{{Name: "value", Value: Column{"value"}, Kind: analysis.KindCounter, Unit: "count"}}}
			result, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
			if err != nil {
				t.Fatal(err)
			}
			part := result.Partitions[0]
			if part.Stats.Missing != 1 || part.Stats.Valid != 0 || !part.Stats.MinTime.Equal(at) || !part.Stats.MaxTime.Equal(at) {
				t.Fatal("canonical physical timestamp/quality changed", part.Stats)
			}
			manifest, err := analysis.NewNormalizedPartitionManifest("p", "d", mapping, []analysis.NormalizedPartition{{ObjectKey: "physical-part.parquet", SHA256: part.SHA256, Bytes: part.Bytes, Stats: part.Stats}})
			if err != nil {
				t.Fatal(err)
			}
			for _, delta := range []time.Duration{-time.Nanosecond, time.Nanosecond} {
				for _, window := range [][2]time.Time{{at.Add(delta), at.Add(time.Second)}, {at.Add(-time.Second), at.Add(delta)}} {
					selected, err := manifest.Select(analysis.PartitionSelection{Metric: "value", IdentityVersion: 1, From: window[0], To: window[1]})
					if err != nil || len(selected) != 1 {
						t.Fatal("submicrosecond endpoint used an unsafe exclusion proof", window, selected, err)
					}
				}
			}
			selected, err := manifest.Select(analysis.PartitionSelection{Metric: "value", IdentityVersion: 1, From: at.Add(-time.Second), To: at})
			if err != nil || len(selected) != 0 {
				t.Fatal("canonical half-open physical boundary not pruned", selected, err)
			}
		})
	}
}
