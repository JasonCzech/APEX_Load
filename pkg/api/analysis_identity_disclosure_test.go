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

func TestAnalysisIdentityProtectedPathsRefusedBySavedMappingAndHistoricalBindings(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	mapping := analysis.ParquetMapping{Version: 2, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}}
	for _, identity := range []*analysis.ParquetIdentityMapping{
		{Source: []string{"password"}}, {Host: []string{"api", "key"}}, {Service: []string{"credentials", "client_secret"}}, {Dimensions: []analysis.ParquetDimensionMapping{{Name: "transaction", Value: []string{"storage_state"}}}},
	} {
		mapping.Identity = identity
		r := timeslotRequest(t, s, http.MethodPost, "/api/v1/analysis/mappings?project_id=proj-a", map[string]any{"name": "protected mapping", "definition": mapping})
		if r.Code != http.StatusBadRequest {
			t.Fatal("protected identity saved as a new definition", r.Code, r.Body.String())
		}
		// These are historical metadata bindings, not new-write admission. No
		// normalized artifact exists: disclosure refusal must precede any artifact
		// size/read or shared native reservation.
		in := &storage.AnalysisImport{ID: "dataset", ProjectID: "proj-a", State: "ready", Revision: 1, Mapping: &mapping}
		q := analytics.SeriesQuery{ProjectID: in.ProjectID, DatasetID: in.ID, Metric: "v", IdentitySchemaVersion: 2, From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
		if _, err := bindImportedNativeSeriesQuery(in, q); err == nil {
			t.Fatal("historical protected mapping admitted at series bind")
		}
		if points, err := queryAnalysisDataset(context.Background(), in, q); err == nil || len(points) != 0 {
			t.Fatal("historical protected mapping produced outward series", points, err)
		}
		if _, err := snapshotAnalysisQueryDataset(in); err == nil {
			t.Fatal("comparison snapshot discarded protected path before refusing it")
		}
		selection := AnalysisSeriesSelection{DatasetID: in.ID, Metric: "v", Aggregation: "avg"}
		if _, err := importedNativeSeriesQuery(in.ProjectID, in, selection, AnalysisComparisonRequest{From: q.From, To: q.To, BucketSeconds: 1}); err == nil {
			t.Fatal("historical protected mapping admitted at comparison bind")
		}
	}
	definitions, err := s.store.(storage.AnalysisDefinitionStore).ListAnalysisDefinitions("proj-a", "mapping", 100, 0)
	if err != nil || len(definitions) != 0 {
		t.Fatal("refused mappings published definition metadata", definitions, err)
	}
	mapping.Version = 1
	mapping.Identity = nil
	in := &storage.AnalysisImport{ID: "dataset", ProjectID: "proj-a", State: "ready", Revision: 1, Mapping: &mapping}
	if _, err := bindImportedNativeSeriesQuery(in, analytics.SeriesQuery{ProjectID: in.ProjectID, DatasetID: in.ID, Metric: "v", From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}); err != nil {
		t.Fatal("valid legacy mapping unavailable", err)
	}
	raw, _ := json.Marshal(mapping)
	if _, err := analysis.DecodeParquetMappingDefinition(raw); err != nil {
		t.Fatal("valid legacy mapping definition unavailable", err)
	}
}
