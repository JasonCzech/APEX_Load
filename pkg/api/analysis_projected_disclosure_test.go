package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func projectedDisclosureMapping(version int, role string) analysis.ParquetMapping {
	m := analysis.ParquetMapping{Version: version, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}}
	switch role {
	case "timestamp":
		m.Timestamp = []string{"password"}
	case "value":
		m.Measures[0].Value = []string{"password"}
	case "name_column":
		m.Measures[0].Name = ""
		m.Measures[0].NameColumn = []string{"api", "key"}
	}
	return m
}

func TestAnalysisProjectedProtectedPathsRefusedBySavedAndHistoricalMappings(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	for _, version := range []int{1, 2} {
		for _, role := range []string{"timestamp", "value", "name_column"} {
			m := projectedDisclosureMapping(version, role)
			w := timeslotRequest(t, s, http.MethodPost, "/api/v1/analysis/mappings?project_id=proj-a", map[string]any{"name": "protected projection", "definition": m})
			if w.Code != http.StatusBadRequest {
				t.Fatal("new protected mapping saved", version, role, w.Code, w.Body.String())
			}
			in := &storage.AnalysisImport{ID: "dataset", ProjectID: "proj-a", State: "ready", Revision: 1, Mapping: &m}
			q := analytics.SeriesQuery{ProjectID: in.ProjectID, DatasetID: in.ID, Metric: "v", From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
			if _, err := bindImportedNativeSeriesQuery(in, q); err == nil {
				t.Fatal("persisted protected projection admitted", version, role)
			}
			if points, err := queryAnalysisDataset(context.Background(), in, q); err == nil || len(points) != 0 {
				t.Fatal("persisted projection produced series", points, err)
			}
			if _, err := snapshotAnalysisQueryDataset(in); err == nil {
				t.Fatal("protected origin dropped before comparison admission")
			}
			if _, err := importedNativeSeriesQuery(in.ProjectID, in, AnalysisSeriesSelection{DatasetID: in.ID, Metric: "v", Aggregation: "avg"}, AnalysisComparisonRequest{From: q.From, To: q.To, BucketSeconds: 1}); err == nil {
				t.Fatal("protected comparison admitted")
			}
		}
	}
	definitions, err := s.store.(storage.AnalysisDefinitionStore).ListAnalysisDefinitions("proj-a", "mapping", 100, 0)
	if err != nil || len(definitions) != 0 {
		t.Fatal("refused definitions became durable", definitions, err)
	}
	valid := projectedDisclosureMapping(1, "")
	valid.Measures[0].Name = "password"
	raw, _ := json.Marshal(valid)
	if _, err := analysis.DecodeParquetMappingDefinition(raw); err != nil {
		t.Fatal("ordinary output metric label refused", err)
	}
	if _, err := bindImportedNativeSeriesQuery(&storage.AnalysisImport{ID: "dataset", ProjectID: "proj-a", Mapping: &valid}, analytics.SeriesQuery{ProjectID: "proj-a", DatasetID: "dataset", Metric: "password", From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}); err != nil {
		t.Fatal("safe legacy mapping unavailable", err)
	}
}
