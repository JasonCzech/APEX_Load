package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestDiagnosticWarningsRemainPartialWithoutDiscardingSampledEvidence(t *testing.T) {
	for _, kind := range []string{"targets", "alerts"} {
		for _, populated := range []bool{false, true} {
			for _, warning := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/populated=%t/warning=%t", kind, populated, warning), func(t *testing.T) {
					requests := 0
					client := fixture(t, Config{}, func(request *http.Request) (*http.Response, error) {
						requests++
						if request.URL.Path != "/api/v1/"+kind {
							t.Fatal("wrong sampled-state API", request.URL.Path)
						}
						records := []any{}
						field := "activeTargets"
						if kind == "alerts" {
							field = "alerts"
						}
						if populated {
							records = append(records, map[string]any{"labels": map[string]string{"job": "sut", "alertname": "TargetError"}, "health": "up", "state": "firing"})
						}
						response := map[string]any{"status": "success", "data": map[string]any{field: records}, "infos": []string{"upstream informational annotation"}}
						if warning && requests == 1 {
							response["warnings"] = []string{"remote read returned partial data"}
						}
						body, err := json.Marshal(response)
						if err != nil {
							t.Fatal(err)
						}
						return rawResponse(string(body)), nil
					})
					selection := Selection{CollectionID: t.Name(), EvidenceSelector: `{job="sut"}`}
					to := time.Now().UTC()
					rows, coverage, err := client.collectDiagnostics(context.Background(), selection, to.Add(-time.Minute), to, t.Name(), kind, "/api/v1/"+kind)
					wantStatus, wantRecords := "no_data", 0
					if populated {
						wantStatus, wantRecords = "complete", 1
					}
					if warning {
						wantStatus = "partial"
					}
					if err != nil || len(rows) != wantRecords || coverage.Status != wantStatus || coverage.ObservedAt == nil || len(coverage.Infos) != 1 || warning != (len(coverage.Warnings) == 1) {
						t.Fatalf("sampled warning normalization: rows=%+v coverage=%+v err=%v", rows, coverage, err)
					}
					cachedRows, cached, err := client.collectDiagnostics(context.Background(), selection, to.Add(-time.Minute), to, t.Name(), kind, "/api/v1/"+kind)
					if warning {
						healthyStatus := "no_data"
						if populated {
							healthyStatus = "complete"
						}
						if err != nil || len(cachedRows) != wantRecords || requests != 2 || cached.Cached || cached.Status != healthyStatus || len(cached.Warnings) != 0 {
							t.Fatalf("partial snapshot suppressed recovery: rows=%+v coverage=%+v err=%v", cachedRows, cached, err)
						}
						replayed, afterRecovery, err := client.collectDiagnostics(context.Background(), selection, to.Add(-time.Minute), to, t.Name(), kind, "/api/v1/"+kind)
						if err != nil || len(replayed) != 0 || requests != 2 || !afterRecovery.Cached || afterRecovery.Status != healthyStatus || !afterRecovery.ObservedAt.Equal(*cached.ObservedAt) {
							t.Fatalf("successful recovery was not cached: %+v, err=%v", afterRecovery, err)
						}
					} else if err != nil || len(cachedRows) != 0 || requests != 1 || !cached.Cached || cached.Status != wantStatus || !cached.ObservedAt.Equal(*coverage.ObservedAt) {
						t.Fatalf("cache manufactured complete evidence: rows=%+v coverage=%+v err=%v", cachedRows, cached, err)
					}
				})
			}
		}
	}
}
