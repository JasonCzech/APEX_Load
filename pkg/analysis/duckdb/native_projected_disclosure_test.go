//go:build analytics_duckdb && cgo

package duckdb

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNativeProjectedCredentialSourceRolesCannotEscapeProtectedPreview(t *testing.T) {
	const secret = "protected-metric-name-source-oracle"
	e, path := nativeFixture(t, "SELECT 1700000000 AS ts, 7::DOUBLE AS value, 1234567::BIGINT AS password, "+literal(secret)+" AS api_token")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := e.Preview(t.Context(), path)
	if err != nil || len(preview.Samples) != 1 || preview.Samples[0]["password"] != "[redacted]" || preview.Samples[0]["api_token"] != "[redacted]" {
		t.Fatal("protected numeric/name preview exposed", preview, err)
	}
	for _, version := range []int{1, 2} {
		for _, role := range []string{"timestamp", "value", "name_column"} {
			m := Mapping{Version: version, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}}
			switch role {
			case "timestamp":
				m.Timestamp = Column{"password"}
			case "value":
				m.Measures[0].Value = Column{"password"}
			case "name_column":
				m.Measures[0].Name = ""
				m.Measures[0].NameColumn = Column{"api_token"}
			}
			hashed := false
			e.workerBeforeHash = func() { hashed = true }
			result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: m, AllowRejected: true})
			if !errors.Is(err, errWorkerProtocol) || hashed || result.Accepted != 0 || len(result.Files) != 0 || len(result.RejectionFiles) != 0 {
				t.Fatal("protected role entered source work or published evidence", role, version, result, err)
			}
			e.workerBeforeHash = nil
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatal("refusal changed original", err)
			}
		}
	}
	// A constant metric label is authored data, not a selected source column.
	m := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "password", Kind: analysis.KindGauge, Unit: "count"}}}
	result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: m})
	if err != nil || result.Accepted != 1 {
		t.Fatal("valid legacy scalar mapping refused", result, err)
	}
	result = retainNativeFixtureOutputs(t, e, result)
	points, err := e.Series(t.Context(), result.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "password", From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"})
	raw, _ := json.Marshal(points)
	if err != nil || len(points) != 1 || points[0].Value == nil || *points[0].Value != 7 || bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte("1234567")) {
		t.Fatal("ordinary output label changed or protected source disclosed", points, err)
	}
}
