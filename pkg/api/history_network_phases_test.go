package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A completed run's history is served from storage: every interval that
// carried network phases must still carry them, and an interval stored
// without phases must stay unknown rather than turn into zeros.
func TestGetMetricsServesStoredNetworkPhasesForEveryInterval(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "phases.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	start := time.Date(2026, time.November, 9, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	if err := store.SaveTest(&storage.TestRecord{ID: "phase-run", ProjectID: storage.DefaultProjectID, Name: "phases", Status: "COMPLETE", StartTime: start, EndTime: &end}); err != nil {
		t.Fatal(err)
	}
	server := func(index int) float64 { return 60 + float64(index) }
	phased := func(index int, at time.Time) *LiveMetrics {
		return &LiveMetrics{
			Timestamp:       at,
			AvgResponseMs:   120,
			TotalTx:         int64(10 * (index + 1)),
			AvgDNSMs:        float64(index), // index 0 has DNS 0, which JSON omits
			AvgConnectMs:    4.5,
			AvgTLSMs:        9.25,
			AvgTTFBMs:       80,
			AvgWaitMs:       30,
			PhaseObserved:   &engine.PhaseObservations{DNS: int64(index), Connect: 4, TLS: 4, NoTTFB: 1, NoTTFBSetupMs: 2.5, NoTTFBRemainderMs: 60},
			AvgServerMs:     float64Ptr(server(index)),
			AvgNoResponseMs: float64Ptr(6),
		}
	}
	// The first interval predates phase persistence: it carries none.
	if err := store.SaveHistory("phase-run", start, &LiveMetrics{Timestamp: start, AvgResponseMs: 100, TotalTx: 5}); err != nil {
		t.Fatal(err)
	}
	const phasedPoints = 3
	for index := 0; index < phasedPoints; index++ {
		at := start.Add(time.Duration(index+1) * 10 * time.Second)
		if err := store.SaveHistory("phase-run", at, phased(index, at)); err != nil {
			t.Fatal(err)
		}
	}

	api := NewServer(ServerConfig{Store: store})
	for _, query := range []string{"", "?limit=3"} {
		recorder := httptest.NewRecorder()
		api.mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/tests/phase-run/metrics"+query, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET metrics%s = %d %s", query, recorder.Code, recorder.Body.String())
		}
		var body struct {
			Metrics map[string]any   `json:"metrics"`
			History []map[string]any `json:"history"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		history := body.History
		if query == "" {
			if len(history) != phasedPoints+1 {
				t.Fatalf("history has %d points, want %d: %s", len(history), phasedPoints+1, recorder.Body.String())
			}
			for _, key := range []string{"avg_dns_ms", "avg_connect_ms", "avg_tls_ms", "avg_ttfb_ms", "avg_wait_ms", "avg_server_ms", "avg_no_response_ms", "phase_observed"} {
				if _, ok := history[0][key]; ok {
					t.Fatalf("legacy interval invented %s: %v", key, history[0])
				}
			}
			history = history[1:]
		}
		if len(history) != phasedPoints {
			t.Fatalf("GET metrics%s history has %d phased points, want %d", query, len(history), phasedPoints)
		}
		for index, point := range history {
			if point["avg_connect_ms"] != 4.5 || point["avg_tls_ms"] != 9.25 || point["avg_ttfb_ms"] != 80.0 || point["avg_wait_ms"] != 30.0 {
				t.Fatalf("GET metrics%s point %d averages = %v", query, index, point)
			}
			if index > 0 && point["avg_dns_ms"] != float64(index) {
				t.Fatalf("GET metrics%s point %d avg_dns_ms = %v", query, index, point["avg_dns_ms"])
			}
			if point["avg_server_ms"] != server(index) || point["avg_no_response_ms"] != 6.0 {
				t.Fatalf("GET metrics%s point %d split = %v / %v", query, index, point["avg_server_ms"], point["avg_no_response_ms"])
			}
			observed, _ := point["phase_observed"].(map[string]any)
			if observed == nil || observed["dns"] != float64(index) || observed["connect"] != 4.0 || observed["tls"] != 4.0 || observed["no_ttfb"] != 1.0 || observed["no_ttfb_setup_ms"] != 2.5 || observed["no_ttfb_remainder_ms"] != 60.0 {
				t.Fatalf("GET metrics%s point %d phase_observed = %v", query, index, point["phase_observed"])
			}
		}
		if body.Metrics["avg_server_ms"] != server(phasedPoints-1) || body.Metrics["phase_observed"] == nil {
			t.Fatalf("GET metrics%s current point lost its phases: %v", query, body.Metrics)
		}
	}
}

// Without phase observations the stored server/no-response split is not
// restored: it is only meaningful with the counts it was derived from.
func TestApplyIntervalNetworkPhasesKeepsSplitUnknownWithoutObservations(t *testing.T) {
	live := &LiveMetrics{}
	applyIntervalNetworkPhases(live, &storage.IntervalNetworkPhasesRecord{AvgTTFBMs: 80, AvgServerMs: float64Ptr(60), AvgNoResponseMs: float64Ptr(1), PhaseObserved: json.RawMessage(`"bad"`)})
	if live.AvgTTFBMs != 80 || live.PhaseObserved != nil || live.AvgServerMs != nil || live.AvgNoResponseMs != nil {
		t.Fatalf("unreadable observations = %+v", live)
	}
	applyIntervalNetworkPhases(live, nil)
	if live.AvgTTFBMs != 80 {
		t.Fatal("a nil record changed the point")
	}
}
