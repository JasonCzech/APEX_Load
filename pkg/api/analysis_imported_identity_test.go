package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestImportedNativeQueryPreservesBoundedNamedAndExactDimensionFilters(t *testing.T) {
	in := &storage.AnalysisImport{ID: "dataset", ProjectID: "project", Mapping: &analysis.ParquetMapping{Version: 2}}
	selection := AnalysisSeriesSelection{DatasetID: "dataset", SourceID: "agent", Host: "host", Service: "service", Transaction: "rpc", Protocol: "grpc", Group: "load", Metric: "latency", Aggregation: "p95", Dimensions: map[string]string{"label": "value", "group": "load"}}
	window := AnalysisComparisonRequest{From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1}
	query, err := importedNativeSeriesQuery("project", in, selection, window)
	if err != nil || query.IdentitySchemaVersion != 2 || query.SourceID != "agent" || query.Host != "host" || query.Service != "service" || len(query.Dimensions) != 4 || query.Dimensions["transaction"] != "rpc" || query.Dimensions["protocol"] != "grpc" {
		t.Fatal("requested identity filters lost", query, err)
	}
	selection.Dimensions["label"] = "mutated"
	if query.Dimensions["label"] != "value" {
		t.Fatal("query borrowed authored dimension map")
	}
	selection.Dimensions["group"] = "other"
	if _, err = importedNativeSeriesQuery("project", in, selection, window); err == nil {
		t.Fatal("conflicting aliases silently overwrote a filter")
	}
	selection.Dimensions = map[string]string{}
	in.Mapping.Version = 1
	if _, err = importedNativeSeriesQuery("project", in, selection, window); err == nil {
		t.Fatal("legacy artifact silently ignored requested filters")
	}
	in.Mapping.Version = 2
	if _, err = importedNativeSeriesQuery("other", in, selection, window); err == nil {
		t.Fatal("cross-project native selection allowed")
	}
}
func TestImportedCombinedDimensionAdmissionBeforeClone(t *testing.T) {
	selection := AnalysisSeriesSelection{Dimensions: map[string]string{}}
	for i := 0; i < 16; i++ {
		selection.Dimensions[string(rune('a'+i))] = "x"
	}
	if validateImportedIdentitySelection(selection) != nil {
		t.Fatal("exact dimension population rejected")
	}
	selection.Group = "group"
	if validateImportedIdentitySelection(selection) == nil {
		t.Fatal("named dimensions escaped combined count")
	}
	delete(selection.Dimensions, "a")
	if validateImportedIdentitySelection(selection) != nil {
		t.Fatal("exact combined population rejected")
	}
	selection.Transaction = strings.Repeat("t", 129)
	if validateImportedIdentitySelection(selection) == nil {
		t.Fatal("oversized named identity accepted")
	}
	selection.Transaction = ""
	selection.Dimensions["a"] = "\x00"
	if validateImportedIdentitySelection(selection) == nil {
		t.Fatal("NUL filter entered SQL conversion")
	}
}

func TestDatasetSeriesIdentityVersionComesFromAuthoritativeMapping(t *testing.T) {
	in := &storage.AnalysisImport{ID: "dataset", ProjectID: "project", State: "ready", Mapping: &analysis.ParquetMapping{Version: 2}}
	q := analytics.SeriesQuery{ProjectID: in.ProjectID, DatasetID: in.ID, Metric: "v", Host: "host", Dimensions: map[string]string{"group": "one"}, From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"}
	bound, err := bindImportedNativeSeriesQuery(in, q)
	if err != nil || bound.IdentitySchemaVersion != 2 || bound.Host != "host" || bound.Dimensions["group"] != "one" {
		t.Fatal("ordinary dataset query lost mapping capability", bound, err)
	}
	q.Dimensions["group"] = "changed"
	if bound.Dimensions["group"] != "one" {
		t.Fatal("bound query borrowed caller filters")
	}
	for _, version := range []int{1, 3} {
		q.IdentitySchemaVersion = version
		if _, err := bindImportedNativeSeriesQuery(in, q); err == nil {
			t.Fatal("explicit mismatched identity version accepted", version)
		}
	}
	q.IdentitySchemaVersion = 0
	in.Mapping.Version = 1
	if _, err := bindImportedNativeSeriesQuery(in, q); err == nil {
		t.Fatal("legacy labels silently ignored")
	}
	q.Host, q.Dimensions = "", nil
	bound, err = bindImportedNativeSeriesQuery(in, q)
	if err != nil || bound.IdentitySchemaVersion != 1 {
		t.Fatal("legacy query rejected", err)
	}
	in.Mapping = nil
	if _, err := bindImportedNativeSeriesQuery(in, q); err != nil {
		t.Fatal("historical unmapped identity rejected", err)
	}
	q.ProjectID = "foreign"
	if _, err := bindImportedNativeSeriesQuery(in, q); err == nil {
		t.Fatal("foreign dataset query admitted")
	}
}

func TestSavedV2MappingRetainsSourceAndDimensionsAcrossRevisions(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	mapping := analysis.ParquetMapping{Version: 2, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}, Identity: &analysis.ParquetIdentityMapping{Source: []string{"nested", "source"}, Host: []string{"host"}, Service: []string{"service"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "a/b~c", Value: []string{"label"}}}}}
	path := "/api/v1/analysis/mappings?project_id=proj-a"
	r := timeslotRequest(t, s, "POST", path, map[string]any{"name": "identity mapping", "definition": mapping})
	if r.Code != 200 {
		t.Fatal("saved identity definition rejected", r.Code, r.Body.String())
	}
	var saved storage.AnalysisDefinition
	if err := json.Unmarshal(r.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	var actual analysis.ParquetMapping
	if err := json.Unmarshal(saved.Definition, &actual); err != nil || actual.Identity == nil || actual.Identity.Source[0] != "nested" || actual.Identity.Dimensions[0].Name != "a/b~c" {
		t.Fatal("saved mapping lost exact identity", actual, err)
	}
	mapping.Identity.Host = []string{"host2"}
	r = timeslotRequest(t, s, "PUT", "/api/v1/analysis/mappings/"+saved.ID+"?project_id=proj-a", map[string]any{"name": "identity mapping", "revision": saved.Revision, "definition": mapping})
	if r.Code != 200 {
		t.Fatal("identity mapping revision rejected", r.Code, r.Body.String())
	}
	r = timeslotRequest(t, s, "GET", "/api/v1/analysis/mappings/"+saved.ID+"?project_id=proj-a&revision=1", nil)
	if r.Code != 200 {
		t.Fatal("historical identity mapping unavailable", r.Code)
	}
	if err := json.Unmarshal(r.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(saved.Definition, &actual); err != nil || actual.Identity.Host[0] != "host" {
		t.Fatal("historical identity overwritten", actual, err)
	}
	mapping.Version = 1
	r = timeslotRequest(t, s, "POST", path, map[string]any{"name": "invalid legacy identity", "definition": mapping})
	if r.Code != 400 {
		t.Fatal("legacy identity definition accepted", r.Code, r.Body.String())
	}
}
