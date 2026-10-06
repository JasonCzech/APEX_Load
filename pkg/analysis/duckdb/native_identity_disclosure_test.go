//go:build analytics_duckdb && cgo

package duckdb

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNativeIdentityCredentialPathsCannotEscapeProtectedPreviewIntoSeries(t *testing.T) {
	const secret = "protected-column-secret-identity-oracle"
	e, path := nativeFixture(t, "SELECT 1700000000 AS ts, 7::DOUBLE AS value, "+literal(secret)+" AS password, "+literal(secret)+" AS api_token, 'tenant-one' AS tenant, 'token-is-an-ordinary-operation-value' AS transaction")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := e.Preview(t.Context(), path)
	if err != nil || len(preview.Samples) != 1 || preview.Samples[0]["password"] != "[redacted]" || preview.Samples[0]["api_token"] != "[redacted]" {
		t.Fatal("protected preview disclosed identity candidate", preview, err)
	}
	body, _ := json.Marshal(preview)
	if bytes.Contains(body, []byte(secret)) {
		t.Fatal("preview disclosed protected source value")
	}
	mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"value"}, Name: "v", Kind: analysis.KindGauge, Unit: "count"}}}
	hashed := false
	e.workerBeforeHash = func() { hashed = true }
	for _, role := range []string{"source", "host", "service", "dimension"} {
		identity := &analysis.ParquetIdentityMapping{}
		switch role {
		case "source":
			identity.Source = Column{"password"}
		case "host":
			identity.Host = Column{"api_token"}
		case "service":
			identity.Service = Column{"password"}
		case "dimension":
			identity.Dimensions = []analysis.ParquetDimensionMapping{{Name: "transaction", Value: Column{"api_token"}}}
		}
		mapping.Identity = identity
		result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping, AllowRejected: true})
		if !errors.Is(err, errWorkerProtocol) || len(result.Files) != 0 || len(result.RejectionFiles) != 0 || result.Accepted != 0 || hashed {
			t.Fatal("protected path reached input hashing/normalization or published evidence", role, result, err, hashed)
		}
	}
	e.workerBeforeHash = nil
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatal("refusal altered the protected original", err)
	}
	mapping.Identity = &analysis.ParquetIdentityMapping{Source: Column{"tenant"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "transaction", Value: Column{"transaction"}}}}
	result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil || result.Accepted != 1 {
		t.Fatal("ordinary analytical labels were refused", result, err)
	}
	result = retainNativeFixtureOutputs(t, e, result)
	points, err := e.Series(t.Context(), result.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "v", IdentitySchemaVersion: 2, From: time.Unix(1700000000, 0).UTC(), To: time.Unix(1700000001, 0).UTC(), BucketSeconds: 1, Aggregation: "avg"})
	exported, _ := json.Marshal(points)
	if err != nil || len(points) != 1 || points[0].Source != "tenant-one" || points[0].Dimensions["transaction"] != "token-is-an-ordinary-operation-value" || strings.Contains(string(exported), secret) {
		t.Fatal("series/export lost ordinary identity or exposed protected value", points, err)
	}
}
