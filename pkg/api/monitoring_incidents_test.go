package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestThresholdIncidentAPIJournalAndAcknowledgement(t *testing.T) {
	server, store := newTimeslotServer(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.SaveProject(&storage.Project{ID: "incidents", Name: "Incidents", CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: "incident-run", ProjectID: "incidents", Name: "Checkout", Status: "RUNNING", StartTime: at}); err != nil {
		t.Fatal(err)
	}
	server.runner.journalThresholdEvaluation(&ManagedTest{ID: "incident-run", ProjectID: "incidents", Name: "Checkout"}, &LiveMetrics{Timestamp: at}, []map[string]any{{"metric": "error_pct", "value": 7.0, "threshold": 5.0, "action": "stop_requested", "secret": "must not persist"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.runner.thresholdJournal.shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	rec := timeslotRequest(t, server, http.MethodGet, "/api/v1/monitoring/threshold-incidents?project_id=incidents", nil)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Incidents []storage.ThresholdIncident `json:"incidents"`
		Active    int                         `json:"active_count"`
		Unacked   int                         `json:"unacknowledged_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Incidents) != 1 || result.Active != 1 || result.Unacked != 1 {
		t.Fatalf("list: %+v", result)
	}
	incident := result.Incidents[0]
	if _, ok := incident.Payload["secret"]; ok {
		t.Fatal("non-incident payload persisted")
	}
	path := "/api/v1/monitoring/threshold-incidents/" + incident.ID + "/ack"
	for _, ack := range []bool{true, false} {
		rec = timeslotRequest(t, server, http.MethodPatch, path, map[string]any{"acknowledged": ack})
		if rec.Code != 200 {
			t.Fatalf("ack: %d %s", rec.Code, rec.Body.String())
		}
		saved, err := store.(storage.ThresholdIncidentStore).GetThresholdIncident(incident.ID)
		if err != nil || (saved.AcknowledgedAt != nil) != ack || !saved.Active {
			t.Fatalf("ack state: %+v %v", saved, err)
		}
	}
	rec = timeslotRequest(t, server, http.MethodPatch, path, map[string]any{})
	if rec.Code != 400 {
		t.Fatalf("missing acknowledgement accepted: %d", rec.Code)
	}
	for _, query := range []string{"", "?project_id=incidents&status=bogus", "?project_id=incidents&severity=bad", "?project_id=incidents&since=bad"} {
		rec = timeslotRequest(t, server, http.MethodGet, "/api/v1/monitoring/threshold-incidents"+query, nil)
		if rec.Code != 400 {
			t.Fatalf("invalid query %q returned %d", query, rec.Code)
		}
	}
}

func TestThresholdSampleRequiresObservationsForConfiguredSignals(t *testing.T) {
	limit := 100.0
	for _, tc := range []struct {
		name     string
		live     *LiveMetrics
		policies ThresholdSettings
		want     bool
	}{
		{name: "empty tick", live: &LiveMetrics{}, want: false},
		{name: "transaction sample", live: &LiveMetrics{TotalTx: 1}, want: true},
		{name: "server timing missing", live: &LiveMetrics{TotalTx: 1}, policies: ThresholdSettings{MaxAvgServerProcessingMs: &limit}, want: false},
		{name: "server timing observed", live: &LiveMetrics{TotalTx: 1, Transactions: map[string]*TxMetrics{"orders": {ServerTelemetry: &ServerTelemetrySummary{Samples: 1}}}}, policies: ThresholdSettings{MaxAvgServerProcessingMs: &limit}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := thresholdSampleCovered(tc.live, tc.policies); got != tc.want {
				t.Fatalf("coverage %v want %v", got, tc.want)
			}
		})
	}
}
