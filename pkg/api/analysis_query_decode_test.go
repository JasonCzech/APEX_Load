package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
)

const strictSeriesQueryFixture = `{"project_id":"proj-a","dataset_id":"dataset","metric":"value","identity_schema_version":2,"source_id":"source","host":"host","service":"service","dimensions":{"__proto__":"","a/b~c":"one"},"from":"2026-01-01T00:00:00Z","to":"2026-01-01T00:01:00Z","bucket_seconds":1,"aggregation":"avg"}`
const strictComparisonQueryFixture = `{"left":{"dataset_id":"left","metric":"value","aggregation":"avg","dimensions":{"__proto__":"","a/b~c":"one"}},"right":{"dataset_id":"right","metric":"value","aggregation":"avg"},"from":"2026-01-01T00:00:00Z","to":"2026-01-01T00:01:00Z","bucket_seconds":1,"max_lag_seconds":0,"mode":"overlay"}`

func TestAnalysisQueryClosedAdmissionOwnsResultAndPreservesRejectedDestination(t *testing.T) {
	var valid analytics.SeriesQuery
	if json.Unmarshal([]byte(strictSeriesQueryFixture), &valid) != nil || valid.Validate() != nil {
		t.Fatal("invalid admitted query fixture")
	}
	for name, body := range map[string]string{
		"sql":                 strings.Replace(strictSeriesQueryFixture, `"metric":"value"`, `"metric":"value","sql":"SELECT * FROM external_file"`, 1),
		"statistic":           strings.Replace(strictSeriesQueryFixture, `"metric":"value"`, `"metric":"value","statistic":"p95"`, 1),
		"alias":               strings.Replace(strictSeriesQueryFixture, `"metric":`, `"Metric":`, 1),
		"escaped_duplicate":   strings.Replace(strictSeriesQueryFixture, `"metric":"value"`, `"metric":"old","\u006detric":"value"`, 1),
		"dimension_duplicate": strings.Replace(strictSeriesQueryFixture, `"a/b~c":"one"`, `"a/b~c":"old","a/b~c":"one"`, 1),
		"null":                strings.Replace(strictSeriesQueryFixture, `"source_id":"source"`, `"source_id":null`, 1),
		"surrogate":           strings.Replace(strictSeriesQueryFixture, `"source"`, `"\ud800"`, 1),
		"late_time_error":     strings.Replace(strictSeriesQueryFixture, `"to":"2026-01-01T00:01:00Z"`, `"to":"invalid"`, 1),
		"trailing":            strictSeriesQueryFixture + `{}`,
		"invalid_utf8":        strictSeriesQueryFixture + string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			actual := valid
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body))
			if decodeAnalysisQueryRequest(w, r, &actual) || w.Code != http.StatusBadRequest || !reflect.DeepEqual(actual, valid) {
				t.Fatal("rejected query changed typed operation", actual, w.Code)
			}
		})
	}
	var actual analytics.SeriesQuery
	if !decodeAnalysisQueryRequest(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(strictSeriesQueryFixture)), &actual) || actual.Validate() != nil || actual.Dimensions["__proto__"] != "" {
		t.Fatal("valid exact query refused", actual)
	}
	actual.Dimensions["a/b~c"] = "edited"
	if valid.Dimensions["a/b~c"] != "one" {
		t.Fatal("accepted destination retained prior mutable dimension map")
	}
}

func TestAnalysisQueryByteAndDimensionPopulationBounds(t *testing.T) {
	for _, over := range []bool{false, true} {
		body := strictSeriesQueryFixture + strings.Repeat(" ", (16<<10)-len(strictSeriesQueryFixture))
		if over {
			body += " "
		}
		w := httptest.NewRecorder()
		var q analytics.SeriesQuery
		ok := decodeAnalysisQueryRequest(w, httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body)), &q)
		if over && (ok || w.Code != http.StatusRequestEntityTooLarge) || !over && !ok {
			t.Fatal("request byte bound differs", len(body), ok, w.Code)
		}
	}
	for _, population := range []int{16, 17} {
		entries := make([]string, population)
		for i := range entries {
			entries[i] = `"` + strings.Repeat("x", i+1) + `":""`
		}
		body := strings.Replace(strictSeriesQueryFixture, `{"__proto__":"","a/b~c":"one"}`, "{"+strings.Join(entries, ",")+"}", 1)
		var q analytics.SeriesQuery
		w := httptest.NewRecorder()
		ok := decodeAnalysisQueryRequest(w, httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(body)), &q)
		if population == 16 && (!ok || q.Validate() != nil) || population == 17 && (ok || w.Code != http.StatusBadRequest || q.Dimensions != nil) {
			t.Fatal("query dimensions exceed typed population admission", population, q, ok, w.Code)
		}
	}
}

func TestAnalysisComparisonHandlerRejectsIgnoredRequestedOptionsBeforeHeavyAdmission(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	for name, body := range map[string]string{
		"sql":                 strings.Replace(strictComparisonQueryFixture, `"mode":"overlay"`, `"mode":"overlay","sql":"SELECT 1"`, 1),
		"selector_option":     strings.Replace(strictComparisonQueryFixture, `"metric":"value"`, `"metric":"value","case_insensitive":true`, 1),
		"duplicate_mode":      strings.Replace(strictComparisonQueryFixture, `"mode":"overlay"`, `"mode":"overlay","mode":"correlation"`, 1),
		"duplicate_dimension": strings.Replace(strictComparisonQueryFixture, `"a/b~c":"one"`, `"a/b~c":"one","a/b~c":"two"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/analysis/compare?project_id=proj-a", strings.NewReader(body))
			s.mux.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest || len(analysisPipelineJobs) != 0 {
				t.Fatal("unsupported comparison entered analytical work", w.Code, w.Body.String(), len(analysisPipelineJobs))
			}
		})
	}
}
