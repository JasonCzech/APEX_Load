package storage

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

func TestPrometheusDiagnosticChronologySurvivesLateArrivalAndEqualTimeRevision(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	end := start.Add(time.Minute)
	early, late := start.Add(time.Second), start.Add(30*time.Second)
	diagnostic := monitorprovider.Diagnostic{ID: "target-one", Kind: "target", TimestampUTC: late, Status: "up", EntityIDs: map[string]string{"job": "sut"}}
	key := DiagnosticKeyForJob(jobs[0].ID, diagnostic)
	for sequence, observed := range []time.Time{late, early, late} {
		diagnostic.TimestampUTC = observed
		diagnostic.Status = []string{"up", "down", "unknown"}[sequence]
		batch := ProviderMonitorBatch{Evidence: []monitorprovider.Diagnostic{diagnostic}, QueryFromUTC: start, QueryUntilUTC: end}
		if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, int64(sequence), batch, end), batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
		latest, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "diagnostic", "", 10)
		wantLatest := int64(0)
		if sequence == 2 {
			wantLatest = 2
		}
		if err != nil || len(latest) != 1 || latest[0].Sequence != wantLatest {
			t.Fatal("latest state regressed to an older source observation", latest, err)
		}
	}
	first, err := s.ListProviderFirstDiagnosticHeads(source.ProjectID, "monitor-run", []string{key})
	if err != nil || len(first) != 1 || first[0].Sequence != 1 {
		t.Fatal("late earliest source observation was not retained", first, err)
	}
	loaded, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	observations := loaded.PrometheusDiagnosticTimes[key]
	if !observations.First.Equal(early) || !observations.Last.Equal(late) {
		t.Fatal("durable observation chronology changed", observations)
	}
	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	var restored RunMonitorJob
	if err = json.Unmarshal(encoded, &restored); err != nil || !restored.PrometheusDiagnosticTimes[key].First.Equal(early) || !restored.PrometheusDiagnosticTimes[key].Last.Equal(late) {
		t.Fatal("private observation chronology did not survive restart JSON", err)
	}
	var receipts int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM run_monitor_batches WHERE job_id=?`, jobs[0].ID).Scan(&receipts); err != nil || receipts != 3 {
		t.Fatal("older observation receipt was discarded", receipts, err)
	}
}

func TestPrometheusDiagnosticChronologyBoundAllowsKnownIdentityRevisions(t *testing.T) {
	at := time.Now().UTC()
	job := RunMonitorJob{}
	for i := range 1000 {
		if _, _, err := retainPrometheusDiagnosticTimes(&job, fmt.Sprintf("identity-%d", i), monitorprovider.Diagnostic{TimestampUTC: at}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := retainPrometheusDiagnosticTimes(&job, "unknown", monitorprovider.Diagnostic{TimestampUTC: at}); err == nil {
		t.Fatal("unbounded diagnostic identities retained")
	}
	if first, latest, err := retainPrometheusDiagnosticTimes(&job, "identity-0", monitorprovider.Diagnostic{TimestampUTC: at.Add(time.Second)}); err != nil || first || !latest || len(job.PrometheusDiagnosticTimes) != 1000 {
		t.Fatal("bounded known diagnostic revision rejected", first, latest, err)
	}
}

func TestPrometheusDiagnosticLastSuccessfulSnapshotSurvivesFailuresAndLateCaptures(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	end := start.Add(3 * time.Minute)
	early, late := start.Add(time.Second), start.Add(121*time.Second)
	for sequence, update := range []monitorprovider.Coverage{
		{Status: "complete", ObservedAt: &late},
		{Status: "unavailable"},
		{Status: "partial", ObservedAt: &late},
		{Status: "complete", ObservedAt: &late, Truncated: true},
		{Status: "no_data", ObservedAt: &early, Cached: true, CacheAgeSeconds: 30},
		{Status: "complete", ObservedAt: &end, Warnings: []string{"partial target state"}},
		{Status: "no_data", ObservedAt: &end, Warnings: []string{"partial alert state"}},
	} {
		batch := ProviderMonitorBatch{Coverage: map[string]monitorprovider.Coverage{"targets": update, "alerts": update}}
		if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, int64(sequence), batch, end), batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
		job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"targets", "alerts"} {
			kept := job.Coverage[kind+"_last_success"]
			if kept.ObservedAt == nil || !kept.ObservedAt.Equal(late) || kept.Status != "complete" || kept.Truncated {
				t.Fatal("later failure or earlier cache erased successful snapshot", kind, kept)
			}
			if _, leaked := job.QueryCoverage[kind+"_last_success"]; leaked {
				t.Fatal("internal successful-snapshot reference entered query progress")
			}
			wantSlots := 1
			if sequence >= 4 {
				wantSlots = 2
			}
			if captured := job.Coverage[kind+"_polling"].Points; captured != wantSlots {
				t.Fatal("failed/truncated snapshots manufactured successful poll slots", kind, captured)
			}
		}
		if len(batch.Coverage) != 2 {
			t.Fatal("internal successful snapshot mutated immutable batch")
		}
	}
}

func TestPrometheusDiagnosticCoverageTracksSuccessfulSourcePollSlots(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	source.Prometheus.Targets = true
	source.Prometheus.Alerts = true
	source.Prometheus.EvidenceSelector = `{instance="same"}`
	connection, _ := s.GetPrometheusConnection(source.ConnectionID)
	if _, err := s.UpdatePrometheusConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", prometheus.Capabilities{Metrics: true, Targets: true, Alerts: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveMonitorSource(source, source.Revision)
	if err != nil {
		t.Fatal(err)
	}
	source = *saved
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	end := start.Add(3 * time.Minute)
	for seq, slot := range []int{0, 0, 2} {
		observed := start.Add(time.Duration(slot*60+1) * time.Second)
		batch := ProviderMonitorBatch{Coverage: map[string]monitorprovider.Coverage{"targets": {Status: "complete", ObservedAt: &observed}, "alerts": {Status: "no_data", ObservedAt: &observed}}}
		if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, int64(seq), batch, end), batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	record, _ := s.GetTest("monitor-run")
	record.Status = "COMPLETE"
	record.EndTime = &end
	if _, err = s.SaveTestWithRunMonitors(record, "", "", true); err != nil {
		t.Fatal(err)
	}
	job, _ := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	for _, key := range []string{"targets_polling", "alerts_polling"} {
		cov := job.Coverage[key]
		if cov.Points != 2 || cov.ExpectedPoints != 3 || cov.MissingPoints != 1 || cov.Status != "partial" || cov.Freshness != "sampled_current_state" {
			t.Fatalf("missing sampled coverage %s %+v", key, cov)
		}
	}
	// Reconciliation must preserve the load-window sampling gaps, even if a
	// collector mistakenly includes a current diagnostic observation afterward.
	observed := end.Add(time.Second)
	batch := ProviderMonitorBatch{Coverage: map[string]monitorprovider.Coverage{"targets": {Status: "complete", ObservedAt: &observed}}}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, 3, batch, end.Add(time.Second)), batch, "", "", 0); err == nil {
		t.Fatal("post-load diagnostic query entered sampled load evidence")
	}
	job, _ = s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if job.Coverage["targets_polling"].Points != 2 || job.Coverage["targets_polling"].MissingPoints != 1 {
		t.Fatal("drain manufactured historical diagnostic polls")
	}
	observed = start.Add(61 * time.Second)
	batch.Coverage["targets"] = monitorprovider.Coverage{Status: "complete", ObservedAt: &observed}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, 3, batch, end.Add(2*time.Second)), batch, "", "", 0); err != nil {
		t.Fatal("captured pre-end diagnostic was lost during drain", err)
	}
	job, _ = s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if cov := job.Coverage["targets_polling"]; cov.Points != 3 || cov.MissingPoints != 0 || !cov.To.Equal(end) {
		t.Fatal("late captured poll changed the load window", cov)
	}
}

func TestPrometheusCoverageRejectsForgedObservationOrCacheMetadata(t *testing.T) {
	start := time.Now().UTC().Add(-time.Minute)
	observed := start.Add(time.Second)
	end := start.Add(45 * time.Second)
	job := RunMonitorJob{StartedAt: start, LoadEndedAt: &end}
	valid := monitorprovider.Coverage{Status: "complete", ObservedAt: &observed, Cached: true, CacheAgeSeconds: 30}
	if err := validatePrometheusCoverage(job, map[string]monitorprovider.Coverage{"targets": valid}, end); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"numeric_timestamp", "cached_without_timestamp", "negative_cache_age", "uncached_age", "future", "after_load", "non_utc", "negative_count", "oversized_count"} {
		t.Run(scenario, func(t *testing.T) {
			cov := valid
			key := "targets"
			switch scenario {
			case "numeric_timestamp":
				key = "cpu"
			case "cached_without_timestamp":
				cov.ObservedAt = nil
			case "negative_cache_age":
				cov.CacheAgeSeconds = -1
			case "uncached_age":
				cov.Cached = false
			case "future":
				v := end.Add(time.Minute)
				cov.ObservedAt = &v
			case "after_load":
				v := end.Add(time.Second)
				cov.ObservedAt = &v
			case "non_utc":
				v := observed.In(time.FixedZone("offset", 3600))
				cov.ObservedAt = &v
			case "negative_count":
				cov.Points = -1
			case "oversized_count":
				cov.ExpectedPoints = maxPrometheusCoveragePoints + 1
			}
			if err := validatePrometheusCoverage(job, map[string]monitorprovider.Coverage{key: cov}, end); err == nil {
				t.Fatal("forged coverage accepted")
			}
		})
	}
}

func TestPrometheusSuccessfulEmptyAlertsDoNotBlockNumericStableClosure(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	source.Prometheus.Alerts = true
	source.Prometheus.EvidenceSelector = `{job="sut"}`
	if _, err := s.SaveMonitorSource(source, source.Revision); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	end := start.Add(time.Minute)
	record, err := s.GetTest("monitor-run")
	if err != nil {
		t.Fatal(err)
	}
	record.Status, record.EndTime = "COMPLETE", &end
	if _, err = s.SaveTestWithRunMonitors(record, "", "", true); err != nil {
		t.Fatal(err)
	}
	observed := end.Add(-time.Second)
	batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{prometheusTestPoint(source, start, "one")}, QueryFromUTC: start, QueryUntilUTC: end, QueryWindowEnd: &end, Progress: &monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, Final: true, Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: start, To: end}, "alerts": {Status: "no_data", From: start, To: end, ObservedAt: &observed}}}
	for sequence, received := range []time.Time{end.Add(180 * time.Second), end.Add(200 * time.Second)} {
		if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, int64(sequence), batch, received), batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || job.State != "stopped" || job.Coverage["alerts_polling"].Status != "complete" || job.StablePasses != 2 {
		t.Fatal("successfully sampled empty alerts prevented numeric closure", job, err)
	}
}

func TestPrometheusWarningBearingDiagnosticSnapshotCannotEstablishStableClosure(t *testing.T) {
	for _, status := range []string{"complete", "no_data"} {
		t.Run(status, func(t *testing.T) {
			s, source, start := prometheusMonitorFixture(t)
			source.Prometheus.Alerts = true
			source.Prometheus.EvidenceSelector = `{job="sut"}`
			if _, err := s.SaveMonitorSource(source, source.Revision); err != nil {
				t.Fatal(err)
			}
			jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
			if err != nil || len(jobs) != 1 {
				t.Fatal(jobs, err)
			}
			end := start.Add(time.Minute)
			record, err := s.GetTest("monitor-run")
			if err != nil {
				t.Fatal(err)
			}
			record.Status, record.EndTime = "COMPLETE", &end
			if _, err = s.SaveTestWithRunMonitors(record, "", "", true); err != nil {
				t.Fatal(err)
			}
			observed := end.Add(-time.Second)
			batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{prometheusTestPoint(source, start, "one")}, QueryFromUTC: start, QueryUntilUTC: end, QueryWindowEnd: &end, Progress: &monitorprovider.QueryProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, Final: true, Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: start, To: end}, "alerts": {Status: status, From: start, To: end, ObservedAt: &observed, Warnings: []string{"partial active alert response"}}}}
			for sequence, received := range []time.Time{end.Add(180 * time.Second), end.Add(200 * time.Second)} {
				if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, int64(sequence), batch, received), batch, "", "", 0); err != nil {
					t.Fatal(err)
				}
			}
			job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
			if err != nil || job.State != "reconciling" || job.StablePasses != 0 || job.Coverage["alerts_polling"].Points != 0 || job.Coverage["alerts_last_success"].ObservedAt != nil {
				t.Fatal("warned sampled state established success or early finalization", job, err)
			}
		})
	}
}

func TestPrometheusDiagnosticFirstHeadPreservesInitialReceiptWithoutUsingIdentityQuota(t *testing.T) {
	s, source, start := prometheusMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, time.Now().Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	end := start.Add(time.Minute)
	diagnostic := monitorprovider.Diagnostic{ID: "target-one", Kind: "target", TimestampUTC: start.Add(time.Second), Status: "up", EntityIDs: map[string]string{"job": "sut"}}
	batch := ProviderMonitorBatch{Evidence: []monitorprovider.Diagnostic{diagnostic}, QueryFromUTC: start, QueryUntilUTC: end}
	for sequence := int64(0); sequence < 2; sequence++ {
		if sequence == 1 {
			batch.Evidence[0].TimestampUTC = start.Add(30 * time.Second)
			batch.Evidence[0].Status = "down"
		}
		if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, sequence, batch, end), batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "diagnostic_first", "", 10)
	if err != nil || len(first) != 1 || first[0].Sequence != 0 || first[0].Index != 0 || first[0].Key != "first:"+DiagnosticKeyForJob(jobs[0].ID, diagnostic) {
		t.Fatal("first observed receipt changed with latest state", first, err)
	}
	latest, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "diagnostic", "", 10)
	if err != nil || len(latest) != 1 || latest[0].Sequence != 1 {
		t.Fatal("latest observed state stopped advancing", latest, err)
	}
	var counted int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM run_monitor_observation_heads WHERE job_id=? AND diagnostic_kind='target'`, jobs[0].ID).Scan(&counted); err != nil || counted != 1 {
		t.Fatal("first receipt consumed the bounded target identity quota", counted, err)
	}
	key := DiagnosticKeyForJob(jobs[0].ID, diagnostic)
	if heads, err := s.ListProviderFirstDiagnosticHeads(source.ProjectID, "monitor-run", []string{key}); err != nil || len(heads) != 1 || heads[0].Sequence != 0 {
		t.Fatal("bounded first-reference lookup lost initial evidence", heads, err)
	}
	if heads, err := s.ListProviderFirstDiagnosticHeads("other-project", "monitor-run", []string{key}); err != nil || len(heads) != 0 {
		t.Fatal("first-reference lookup crossed project scope", heads, err)
	}
	for _, keys := range [][]string{{"invalid"}, make([]string, 1000)} {
		if _, err := s.ListProviderFirstDiagnosticHeads(source.ProjectID, "monitor-run", keys); err == nil {
			t.Fatal("unbounded or invalid first-reference page accepted")
		}
	}
}
