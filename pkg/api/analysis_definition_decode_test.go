package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

const strictSavedMappingFixture = `{"version":2,"timestamp":["ts"],"timestamp_encoding":"unix_s","timezone":"UTC","measures":[{"value":["v"],"name":"v","kind":"gauge","unit":"count"}],"identity":{"source":["source"],"dimensions":[{"name":"a/b~c","value":["label"]}]}}`

func TestSavedDefinitionRequestRefusesUnsupportedAmbiguousAndMalformedMapping(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	path := "/api/v1/analysis/mappings?project_id=proj-a"
	body := `{"name":"mapping","definition":` + strictSavedMappingFixture + `}`
	for name, invalid := range map[string]string{
		"root_mapping_option":    strings.Replace(body, `"version":2`, `"version":2,"summary_only":true`, 1),
		"identity_option":        strings.Replace(body, `"source":[`, `"case_insensitive":true,"source":[`, 1),
		"measure_option":         strings.Replace(body, `"kind":"gauge"`, `"kind":"gauge","statistic":"p95"`, 1),
		"dimension_option":       strings.Replace(body, `"name":"a/b~c"`, `"name":"a/b~c","default":"unknown"`, 1),
		"escaped_duplicate":      strings.Replace(body, `"version":2`, `"version":1,"\u0076ersion":2`, 1),
		"duplicate_identity":     strings.Replace(body, `"source":["source"]`, `"source":["old"],"source":["source"]`, 1),
		"case_alias":             strings.Replace(body, `"version":2`, `"VERSION":2`, 1),
		"request_alias":          strings.Replace(body, `"name":`, `"Name":`, 1),
		"unknown_request_option": strings.Replace(body, `"name":`, `"unsupported":true,"name":`, 1),
		"duplicate_request":      `{"name":"mapping","definition":{},"definition":` + strictSavedMappingFixture + `}`,
		"trailing_request":       body + ` {}`,
		"invalid_unicode":        strings.Replace(body, `a/b~c`, `\ud800`, 1),
		"collection_over":        strings.Replace(body, `["source"]`, `[`+strings.TrimSuffix(strings.Repeat(`"part",`, 17), ",")+`]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(invalid))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatal("unsupported/ambiguous mapping was saved", w.Code, w.Body.String())
			}
		})
	}
	list := timeslotRequest(t, s, http.MethodGet, path, nil)
	var items []storage.AnalysisDefinition
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &items) != nil || len(items) != 0 {
		t.Fatal("rejected requests acquired durable revisions", list.Code, list.Body.String())
	}
	r := timeslotRequest(t, s, http.MethodPost, path, map[string]any{"name": "valid", "definition": json.RawMessage(strictSavedMappingFixture)})
	if r.Code != http.StatusOK {
		t.Fatal("valid strict v2 write refused", r.Code, r.Body.String())
	}
	var created storage.AnalysisDefinition
	if json.Unmarshal(r.Body.Bytes(), &created) != nil || created.Revision != 1 {
		t.Fatal("valid revision receipt", r.Body.String())
	}
	badUpdate := strings.Replace(strictSavedMappingFixture, `"kind":"gauge"`, `"kind":"gauge","statistic":"p95"`, 1)
	r = timeslotRequest(t, s, http.MethodPut, "/api/v1/analysis/mappings/"+created.ID+"?project_id=proj-a", map[string]any{"name": "invalid update", "revision": created.Revision, "definition": json.RawMessage(badUpdate)})
	if r.Code != http.StatusBadRequest {
		t.Fatal("unsupported update saved", r.Code, r.Body.String())
	}
	current, err := s.store.(storage.AnalysisDefinitionStore).GetAnalysisDefinition("proj-a", created.ID, 0)
	if err != nil || current.Revision != 1 || current.Name != "valid" {
		t.Fatal("failed strict update changed durable definition", current, err)
	}
}

func TestHistoricalSavedMappingReadsRemainCompatibleButCannotWriteUnsupportedOptions(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	legacy := strings.Replace(strictSavedMappingFixture, `"version":2`, `"version":2,"legacy_option":"retained"`, 1)
	store := s.store.(storage.AnalysisDefinitionStore)
	old, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "legacy-mapping", ProjectID: "proj-a", Kind: "mapping", Name: "old definition", Definition: json.RawMessage(legacy)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/analysis/mappings/" + old.ID + "?project_id=proj-a"
	r := timeslotRequest(t, s, http.MethodGet, path, nil)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"legacy_option":"retained"`) {
		t.Fatal("strict writes changed historical read behavior", r.Code, r.Body.String())
	}
	r = timeslotRequest(t, s, http.MethodPut, path, map[string]any{"name": old.Name, "revision": old.Revision, "definition": old.Definition})
	if r.Code != http.StatusBadRequest {
		t.Fatal("historical unsupported option silently rewritten", r.Code, r.Body.String())
	}
}

func TestAnalyticalDefinitionEnvelopeAndComparisonAdmissionIsAtomicAndExact(t *testing.T) {
	good := `{"left":{"dataset_id":"a","metric":"v","aggregation":"avg","dimensions":{"__proto__":"","a/b~c":"one"}},"right":{"dataset_id":"b","metric":"v","aggregation":"avg"},"from":"2026-01-01T00:00:00Z","to":"2026-01-01T00:01:00Z","bucket_seconds":1,"max_lag_seconds":0,"mode":"overlay"}`
	var valid AnalysisComparisonRequest
	if err := decodeAnalysisDefinitionJSON([]byte(good), &valid); err != nil || valid.Left.Dimensions["__proto__"] != "" || valid.validate() != nil {
		t.Fatal("valid exact comparison refused", valid, err)
	}
	for _, invalid := range []string{
		strings.Replace(good, `"metric":"v"`, `"metric":"v","statistic":"p95"`, 1),
		strings.Replace(good, `"metric":"v"`, `"Metric":"v"`, 1),
		strings.Replace(good, `"mode":"overlay"`, `"mode":"overlay","mode":"correlation"`, 1),
		strings.Replace(good, `"a/b~c":"one"`, `"a/b~c":"one","a/b~c":"two"`, 1),
		strings.Replace(good, `"bucket_seconds":1`, `"bucket_seconds":1.5`, 1),
		good + ` {}`,
	} {
		actual := valid
		if err := decodeAnalysisDefinitionJSON([]byte(invalid), &actual); err == nil || !reflect.DeepEqual(actual, valid) {
			t.Fatal("invalid comparison changed admitted destination", actual, err)
		}
	}
	for _, target := range []any{nil, storage.AnalysisDefinition{}, (*storage.AnalysisDefinition)(nil)} {
		if decodeAnalysisDefinitionJSON([]byte(`{}`), target) == nil {
			t.Fatal("non-owned destination accepted")
		}
	}
}
