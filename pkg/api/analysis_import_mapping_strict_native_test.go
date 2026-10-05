//go:build analytics_duckdb && cgo

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestImportMappingWriteCannotBypassStrictSavedDefinitionAdmission(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	s, rawStore := newTimeslotServer(t)
	store := rawStore.(storage.AnalysisImportStore)
	// Existing v0 upload metadata needs no new cursor/writer assertion. The test
	// exercises mapping admission only; it performs no native query or artifact IO.
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: "strict-map-import", ProjectID: "proj-a", Name: "legacy complete upload", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "file", RelativePath: "input.parquet", Size: 1, Uploaded: 1}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/analysis/imports/" + in.ID + "/mapping"
	body := fmt.Sprintf(`{"revision":%d,"mapping":%s}`, in.Revision, strictSavedMappingFixture)
	for name, invalid := range map[string]string{
		"unknown_root":       strings.Replace(body, `"version":2`, `"version":2,"summary_only":true`, 1),
		"unknown_identity":   strings.Replace(body, `"source":[`, `"unsupported":true,"source":[`, 1),
		"unknown_measure":    strings.Replace(body, `"kind":"gauge"`, `"kind":"gauge","statistic":"p95"`, 1),
		"duplicate":          strings.Replace(body, `"version":2`, `"version":1,"version":2`, 1),
		"duplicate_envelope": strings.Replace(body, `"revision":`, `"revision":0,"revision":`, 1),
		"trailing":           body + ` {}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, path, strings.NewReader(invalid))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatal("import mapping bypassed strict definition decoding", w.Code, w.Body.String())
			}
			current, err := store.GetAnalysisImport(in.ID)
			if err != nil || current.Revision != in.Revision || current.State != "uploading" || current.Mapping != nil {
				t.Fatal("failed mapping write changed authoritative import", current, err)
			}
		})
	}
	r := timeslotRequest(t, s, http.MethodPut, path, map[string]any{"revision": in.Revision, "mapping": json.RawMessage(strictSavedMappingFixture)})
	if r.Code != http.StatusOK {
		t.Fatal("valid import mapping refused", r.Code, r.Body.String())
	}
	current, err := store.GetAnalysisImport(in.ID)
	if err != nil || current == nil || current.Revision != in.Revision+1 || current.State != "mapped" || current.Mapping == nil || current.Mapping.Identity == nil || len(current.Mapping.Identity.Source) != 1 || current.Mapping.Identity.Source[0] != "source" {
		t.Fatal("approved v2 mapping not retained", current, err)
	}
}
