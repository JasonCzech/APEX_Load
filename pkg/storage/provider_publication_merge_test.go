package storage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
)

func TestPublicationProviderPrivateStateRoundTrips(t *testing.T) {
	at := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	job := RunMonitorJob{
		PrometheusConnectionSnapshot: &PrometheusConnectionSnapshot{ConnectionID: "prom"},
		DynatraceSnapshot:            &DynatraceConnectionSnapshot{ConnectionID: "dyn"},
		QueryTime:                    &at, ProviderCursor: "private-cursor", RunTraceIDs: []string{"trace"},
		ScanConsumedBytes: 13, ScanReservedBytes: 29,
		FrozenMonitorRules: []analysis.MonitorRule{{QueryID: "cpu", Provider: "prometheus"}}, RulesFrozen: true,
		QueryCoverage: map[string]appdynamics.Coverage{"cpu": {Status: "complete", ExpectedPoints: 4, Warnings: []string{"source-warning"}, ObservedAt: &at}},
	}
	body, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var restored RunMonitorJob
	if err = json.Unmarshal(body, &restored); err != nil || !reflect.DeepEqual(job, restored) {
		t.Fatalf("provider state lost on restart: %+v, %v", restored, err)
	}
	preparation := PreparationMonitorSnapshot{MonitorRules: job.FrozenMonitorRules, RulesFrozen: true,
		PrometheusConnections: map[string]*PrometheusConnectionSnapshot{"prom": job.PrometheusConnectionSnapshot},
		DynatraceConnections:  map[string]*DynatraceConnectionSnapshot{"dyn": job.DynatraceSnapshot},
	}
	body, err = json.Marshal(preparation)
	if err != nil {
		t.Fatal(err)
	}
	var restoredPreparation PreparationMonitorSnapshot
	if err = json.Unmarshal(body, &restoredPreparation); err != nil || !reflect.DeepEqual(preparation, restoredPreparation) {
		t.Fatalf("prepared provider snapshots lost: %+v, %v", restoredPreparation, err)
	}
}

func TestPublicationPrometheusCanonicalHeadsRemainReadable(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	point := prometheusTestPoint(source, start, strings.Repeat("a", 64))
	batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{point}}
	receipt := providerReceipt(t, jobs[0].ID, 0, batch, start.Add(time.Minute))
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", receipt, batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	heads, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "point", "", 10)
	if err != nil || len(heads) != 1 {
		t.Fatal(heads, err)
	}
	if err = VerifyProviderHeadContent(heads[0], point); err != nil {
		t.Fatal("published Prometheus evidence rejected", err)
	}
	point.Provider = &analysis.ProviderMeasurement{Name: "dynatrace"}
	if err = VerifyProviderHeadContent(heads[0], point); err == nil {
		t.Fatal("different provider substituted for published evidence")
	}
}
