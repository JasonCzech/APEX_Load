package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAppDynamicsSeriesRequestedPageLimit(t *testing.T) {
	server, _, job, first := apiAppDynamicsMonitorFixture(t)
	second := first
	metadata := *first.Provider
	second.Provider = &metadata
	second.TimestampUTC = first.TimestampUTC.Add(time.Minute)
	second.Provider.BucketStart = second.TimestampUTC
	second.Provider.BucketEnd = second.TimestampUTC.Add(time.Minute)
	payload := storage.ProviderMonitorBatch{QueryFromUTC: job.StartedAt, QueryUntilUTC: job.StartedAt.Add(5 * time.Minute), Points: []analysis.MetricPoint{first, second}}
	if err := server.ingestAppDynamicsBatch(context.Background(), job, 0, payload, executionLeaseTerm{}); err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/v1/tests/" + job.RunID + "/monitor-series?source_id=" + job.Source.ID + "&metric=calls"
	var page struct {
		Points   []analysis.MetricPoint `json:"points"`
		HasMore  bool                   `json:"has_more"`
		AfterKey string                 `json:"after_key"`
	}
	response := timeslotRequest(t, server, http.MethodGet, endpoint+"&limit=1", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Points) != 1 || !page.HasMore || page.AfterKey == "" {
		t.Fatalf("first bounded page: %d %s", response.Code, response.Body.String())
	}
	firstTimestamp := page.Points[0].TimestampUTC
	response = timeslotRequest(t, server, http.MethodGet, endpoint+"&limit=1&after_key="+url.QueryEscape(page.AfterKey), nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Points) != 1 || page.HasMore || page.Points[0].TimestampUTC.Equal(firstTimestamp) {
		t.Fatalf("second bounded page: %d %s", response.Code, response.Body.String())
	}
	for _, query := range []string{"", "&limit=999"} {
		response = timeslotRequest(t, server, http.MethodGet, endpoint+query, nil)
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Points) != 2 || page.HasMore {
			t.Fatalf("default/maximum page: %d %s", response.Code, response.Body.String())
		}
	}
	for _, limit := range []string{"0", "-1", "1000", "invalid"} {
		response = timeslotRequest(t, server, http.MethodGet, endpoint+"&limit="+limit, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid limit %q: %d %s", limit, response.Code, response.Body.String())
		}
	}
}
