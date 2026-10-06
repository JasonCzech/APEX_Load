//go:build analytics_duckdb && cgo

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestNativeSeriesHandlerRejectsIgnoredOptionsAndForeignRequestedScope(t *testing.T) {
	s, raw := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	store := raw.(storage.AnalysisImportStore)
	in := newStrictIdentitySeriesDatasetFixture(t, store)
	path := "/api/v1/analysis/datasets/" + in.ID + "/series"
	base := strings.Replace(strictSeriesQueryFixture, `"dataset_id":"dataset"`, `"dataset_id":"`+in.ID+`"`, 1)
	for name, body := range map[string]string{
		"sql":                     strings.Replace(base, `"metric":"value"`, `"metric":"value","sql":"SELECT 1"`, 1),
		"unknown_identity_option": strings.Replace(base, `"metric":"value"`, `"metric":"value","case_insensitive":true`, 1),
		"duplicate_metric":        strings.Replace(base, `"metric":"value"`, `"metric":"value","metric":"other"`, 1),
		"foreign_project":         strings.Replace(base, `"project_id":"proj-a"`, `"project_id":"proj-b"`, 1),
		"foreign_dataset":         strings.Replace(base, in.ID, "foreign-dataset", 1),
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			s.mux.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest || len(analysisPipelineJobs) != 0 {
				t.Fatal("unsupported series option reached native work", w.Code, w.Body.String())
			}
			current, err := store.GetAnalysisImport(in.ID)
			if err != nil || current == nil || current.Revision != in.Revision || current.State != "ready" {
				t.Fatal("query admission modified authoritative dataset", current, err)
			}
		})
	}
}

// Keep an independent fixture symbol for exact task-owned compiled-artifact
// inspection. This is complete legacy metadata only: no test source read,
// artifact write or native child is admitted by the refused request cases.
//
//go:noinline
func newStrictIdentitySeriesDatasetFixture(t *testing.T, store storage.AnalysisImportStore) *storage.AnalysisImport {
	t.Helper()
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: "00000000-0000-0000-0000-000000000011", ProjectID: "proj-a", Name: "strict query fixture", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "00000000-0000-0000-0000-000000000012", RelativePath: "strict.parquet", Size: 12, Uploaded: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return publishAnalysisAPIFixture(t, store, in, nil)
}
