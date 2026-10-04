package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"strings"
	"testing"
	"time"
)

func dynatraceMonitorFixture(t *testing.T) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	s, _, _ := monitorFixture(t)
	return seedDynatraceMonitorFixture(t, s)
}
func seedDynatraceMonitorFixture(t *testing.T, s *SQLiteStore) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	_, _, current := seedMonitorFixture(t, s)
	now := current.Add(-10 * time.Minute).Truncate(time.Minute)
	c := &DynatraceConnection{ID: "dt-native", ProjectID: "monitor-p", Name: "DT", Enabled: true, EnvironmentURL: "https://fixture.live.dynatrace.com", MetricsEnabled: true, EnvironmentTokenSecret: "dt-private-reference", MetricQueries: []DynatraceMetricQuery{{Name: "cpu", MetricSelector: "builtin:host.cpu.usage", Aggregation: "avg"}}}
	if err := s.CreateDynatraceConnection(c); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateDynatraceConnectionHealthIfRevision(c.ID, c.ProjectID, c.Revision, "connected", "", dynatrace.Capabilities{Metrics: true}, now); err != nil || !ok {
		t.Fatalf("verify connection %v %v", ok, err)
	}
	sel := dynatrace.Selection{ProjectID: c.ProjectID, Metrics: []dynatrace.MetricSelection{{Name: "cpu", MetricSelector: "builtin:host.cpu.usage", Aggregation: "avg", Unit: "percent", Semantics: "gauge"}}}
	source := MonitorSource{ID: "dt-source", ProjectID: c.ProjectID, Name: "DT", Enabled: true, Pack: "dynatrace", ExecutorMode: "direct", ConnectionID: c.ID, Dynatrace: &sel, IntervalSeconds: 60}
	saved, err := s.SaveMonitorSource(source, 0)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.GetTest("monitor-run")
	if err != nil {
		t.Fatal(err)
	}
	run.MonitorBindings = &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}
	run.StartTime, run.StartedAt = now, &now
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	return s, *saved, now
}
func dynatracePoint(s MonitorSource, start time.Time, value float64) analysis.MetricPoint {
	return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: s.ProjectID, RunID: "monitor-run", Source: s.ID, MetricName: "cpu", Kind: analysis.KindGauge, Unit: "percent", TimestampUTC: start, Quality: analysis.QualityValid, Value: &value, Provider: &analysis.ProviderMeasurement{Name: "dynatrace", Backend: "classic", MetricID: "builtin:host.cpu.usage", EntityIDs: map[string]string{"host": "HOST-0123456789ABCDEF"}, BucketStart: start, BucketEnd: start.Add(time.Minute), ResolutionSeconds: 60, Statistic: "time_average", Settled: true, AggregationEligible: true}}
}
func acceptDynatraceFixture(t *testing.T, s *SQLiteStore, source MonitorSource, sequence int64, payload ProviderMonitorBatch, at time.Time) MonitorBatchReceipt {
	t.Helper()
	body, _ := json.Marshal(payload)
	sum := sha256.Sum256(body)
	r := MonitorBatchReceipt{JobID: "monitor-run:" + source.ID, Sequence: sequence, Digest: hex.EncodeToString(sum[:]), ArtifactKey: "fixture", SampleCount: len(payload.Points), ReceivedAt: at}
	if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", r, payload, "", "", 0); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDynatraceDurableCanonicalCursorReconciliation(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	checkDynatraceDurableCanonicalCursorReconciliation(t, s, source, start)
}
func checkDynatraceDurableCanonicalCursorReconciliation(t *testing.T, s *SQLiteStore, source MonitorSource, start time.Time) {
	t.Helper()
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil || len(jobs) != 1 || jobs[0].DynatraceSnapshot == nil {
		t.Fatalf("start %+v %v", jobs, err)
	}
	until := start.Add(2 * time.Minute)
	first := ProviderMonitorBatch{Points: []analysis.MetricPoint{dynatracePoint(source, start, 1)}, QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderQueryProgress{NextQuery: 0, TotalQueries: 1, Cursor: "opaque-private-request-token"}}
	receipt := acceptDynatraceFixture(t, s, source, 0, first, until)
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", receipt, first, "", "", 0); err != nil {
		t.Fatal(err)
	}
	receipt.Digest = strings.Repeat("0", 64)
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", receipt, first, "", "", 0); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	job, _ := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if job.ProviderCursor != "opaque-private-request-token" || job.QueryWatermark.IsZero() == false {
		t.Fatalf("cursor or watermark %+v", job)
	}
	second := ProviderMonitorBatch{Points: []analysis.MetricPoint{dynatracePoint(source, start, 4)}, QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderQueryProgress{NextQuery: 1, TotalQueries: 1, Complete: true}, QueryWindowEnd: &until, Coverage: map[string]analysis.ProviderCoverage{"metrics:cpu": {Status: "complete", From: start, To: until}}}
	acceptDynatraceFixture(t, s, source, 1, second, until)
	heads, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, "point", "", 100)
	if err != nil || len(heads) != 1 || heads[0].Sequence != 1 {
		t.Fatalf("canonical revision %+v %v", heads, err)
	}
	job, _ = s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if job.ProviderCursor != "" || !job.QueryWatermark.Equal(until) {
		t.Fatalf("committed progress %+v", job)
	}
	run, _ := s.GetTest("monitor-run")
	run.Status = "COMPLETE"
	run.EndTime = &until
	if saved, err := s.SaveTestWithRunMonitors(run, "", "", true); err != nil || !saved {
		t.Fatalf("terminal load %v %v", saved, err)
	}
	job, _ = s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if job.State != "reconciling" || job.DrainDeadline == nil || !job.DrainDeadline.Equal(until.Add(5*time.Minute)) {
		t.Fatalf("persisted drain %+v", job)
	}
	final := second
	final.Final = true
	acceptDynatraceFixture(t, s, source, 2, final, until.Add(3*time.Minute+time.Second))
	acceptDynatraceFixture(t, s, source, 3, final, until.Add(4*time.Minute+time.Second))
	job, _ = s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if job.State != "stopped" {
		t.Fatalf("stable passes %+v", job)
	}
}
func TestDynatraceScanReservationsAreDurableAndReserveDrain(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	jobID := jobs[0].ID
	for i := 0; i < 8; i++ {
		if ok, err := s.ReserveDynatraceScan(source.ProjectID, jobID, fmt.Sprintf("execute-%d", i), 1_000_000_000, start); err != nil || !ok {
			t.Fatalf("reserve %d %v %v", i, ok, err)
		}
	}
	if ok, err := s.ReserveDynatraceScan(source.ProjectID, jobID, "ninth-live", 1_000_000_000, start); err != nil || ok {
		t.Fatalf("drain allowance spent live %v %v", ok, err)
	}
	if ok, err := s.ReserveDynatraceScan(source.ProjectID, jobID, "execute-0", 1_000_000_000, start); err != nil || !ok {
		t.Fatalf("lost acknowledgement not idempotent %v %v", ok, err)
	}
	if _, err := s.ReserveDynatraceScan(source.ProjectID, jobID, "execute-0", 1, start); err == nil {
		t.Fatal("conflicting reservation accepted")
	}
	until := start.Add(time.Minute)
	p := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderQueryProgress{NextQuery: 1, TotalQueries: 1, Complete: true, ScannedBytes: 10, QueryCostKnown: true, ScanReservationIDs: []string{"execute-0"}, ScanSettlements: []analysis.ProviderScanSettlement{{AttemptID: "execute-0", ScannedBytes: 10}}}}
	acceptDynatraceFixture(t, s, source, 0, p, until)
	job, _ := s.GetRunMonitorJob(source.ProjectID, jobID)
	if job.ScanConsumedBytes != 10 || job.ScanReservedBytes != 7_000_000_000 {
		t.Fatalf("receipt scan accounting %+v", job)
	}
	if ok, err := s.ReserveDynatraceScan(source.ProjectID, jobID, "unknown-cost", 999_999_990, until); err != nil || !ok {
		t.Fatalf("released unused reservation %v %v", ok, err)
	}
	p.Progress.ScannedBytes = 0
	p.Progress.QueryCostKnown = false
	p.Progress.ScanReservationIDs = []string{"unknown-cost"}
	p.Progress.ScanSettlements = nil
	acceptDynatraceFixture(t, s, source, 1, p, until)
	job, _ = s.GetRunMonitorJob(source.ProjectID, jobID)
	if job.ScanConsumedBytes != 10 || job.ScanReservedBytes != 7_999_999_990 {
		t.Fatal("unknown cost released conservative reservation", job)
	}
	run, _ := s.GetTest("monitor-run")
	run.Status = "COMPLETE"
	run.EndTime = &until
	if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ReserveDynatraceScan(source.ProjectID, jobID, "drain-execute", 1_000_000_000, until); err != nil || !ok {
		t.Fatalf("reserved drain unavailable %v %v", ok, err)
	}
	p.Progress.ScanReservationIDs = []string{"forged"}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", MonitorBatchReceipt{JobID: jobID, Sequence: 2, Digest: strings.Repeat("1", 64), ReceivedAt: until}, p, "", "", 0); err == nil {
		t.Fatal("unknown execution charged")
	}
}

func TestDynatraceNativeConnectionRevisionAndSecretInvalidation(t *testing.T) {
	s, source, now := dynatraceMonitorFixture(t)
	c, err := s.GetDynatraceConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Readiness() != "ready" {
		t.Fatal(c.Readiness())
	}
	prior := c.Revision
	c.Name = "Renamed"
	if ok, err := s.UpdateDynatraceConnectionIfRevision(c, prior); err != nil || !ok || c.VerifiedRevision != c.Revision {
		t.Fatalf("display update %v %v %+v", ok, err, c)
	}
	if ok, err := s.UpdateDynatraceConnectionHealthIfRevision(c.ID, c.ProjectID, prior, "connected", "", dynatrace.Capabilities{}, now); err != nil || ok {
		t.Fatalf("stale health attached %v %v", ok, err)
	}
	consumers, err := s.ListDynatraceSecretConsumers(c.ProjectID, "DT_PRIVATE_REFERENCE")
	if err != nil || len(consumers) != 1 {
		t.Fatalf("logical secret consumers %+v %v", consumers, err)
	}
	if _, err = s.InvalidateDynatraceSecretPreparations(c.ProjectID, "dt-private-reference", now); err != nil {
		t.Fatal(err)
	}
	c, _ = s.GetDynatraceConnection(c.ID)
	if c.Readiness() != "unverified" || c.VerifiedRevision != 0 {
		t.Fatalf("rotated credential verification %+v", c)
	}
	body, _ := json.Marshal(c)
	if strings.Contains(string(body), "dt-private-reference") {
		t.Fatalf("secret locator disclosed %s", body)
	}
}

func TestDynatraceOperationOwnerFenceAndKinds(t *testing.T) {
	s, _, now := dynatraceMonitorFixture(t)
	for _, kind := range []string{"verify", "discover", "preview"} {
		op := &DynatraceOperation{ID: kind, ProjectID: "monitor-p", ConnectionID: "dt-native", ConnectionRevision: 1, Kind: kind, PayloadJSON: `{}`, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
		if err := s.CreateDynatraceOperation(op); err != nil {
			t.Fatal(err)
		}
		if ok, err := s.ClaimDynatraceOperation(op.ID, "owner", now, now.Add(30*time.Second)); err != nil || !ok {
			t.Fatalf("claim %v %v", ok, err)
		}
		if ok, err := s.CompleteDynatraceOperation(op.ID, "stale", `{}`, "", now); err != nil || ok {
			t.Fatalf("stale owner completed %v %v", ok, err)
		}
		if ok, err := s.CompleteDynatraceOperation(op.ID, "owner", `{}`, "", now); err != nil || !ok {
			t.Fatalf("complete %v %v", ok, err)
		}
	}
	bad := &DynatraceOperation{ID: "bad", ProjectID: "monitor-p", ConnectionID: "dt-native", ConnectionRevision: 1, Kind: "delete", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := s.CreateDynatraceOperation(bad); err == nil {
		t.Fatal("write operation accepted")
	}
}

func TestDynatracePartialChunksFreezeWindowWithoutEarlyWatermark(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	until := start.Add(2 * time.Minute)
	partial := ProviderMonitorBatch{Points: []analysis.MetricPoint{dynatracePoint(source, start, 1)}, QueryFromUTC: start, QueryUntilUTC: until}
	acceptDynatraceFixture(t, s, source, 0, partial, until)
	job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || !job.QueryFromUTC.Equal(start) || !job.QueryUntilUTC.Equal(until) || !job.QueryWatermark.IsZero() || job.QueryOffset != 0 {
		t.Fatal(job, err)
	}
	drifted := partial
	drifted.QueryUntilUTC = until.Add(time.Minute)
	if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, jobs[0].ID, 1, drifted, until.Add(time.Minute)), drifted, "", "", 0); err == nil {
		t.Fatal("partial query window drift accepted")
	}
	final := partial
	final.Progress = &analysis.ProviderQueryProgress{NextQuery: 1, TotalQueries: 1, Complete: true}
	final.QueryWindowEnd = &until
	final.Coverage = map[string]analysis.ProviderCoverage{"metrics:cpu": {Status: "complete", From: start, To: until}}
	acceptDynatraceFixture(t, s, source, 1, final, until)
	job, err = s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || !job.QueryWatermark.Equal(until) || !job.QueryUntilUTC.IsZero() {
		t.Fatal(job, err)
	}
}

func TestDynatraceTerminalProgressRequiresAllSelectedCoverage(t *testing.T) {
	for _, chunks := range []bool{false, true} {
		t.Run(fmt.Sprintf("coverage_committed_in_earlier_chunk=%t", chunks), func(t *testing.T) {
			s, source, start := dynatraceMonitorFixture(t)
			source.Dynatrace.Metrics = append(source.Dynatrace.Metrics, dynatrace.MetricSelection{Name: "memory", MetricSelector: "builtin:host.mem.usage", Aggregation: "avg", Unit: "percent", Semantics: "gauge"})
			saved, err := s.SaveMonitorSource(source, source.Revision)
			if err != nil {
				t.Fatal(err)
			}
			source = *saved
			jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			until := start.Add(2 * time.Minute)
			run, err := s.GetTest("monitor-run")
			if err != nil {
				t.Fatal(err)
			}
			run.Status, run.EndTime = "COMPLETE", &until
			if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
				t.Fatal(err)
			}
			cpu := analysis.ProviderCoverage{Status: "complete", From: start, To: until}
			batch := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderQueryProgress{NextQuery: 2, TotalQueries: 2, Complete: true}, QueryWindowEnd: &until, Final: true, Coverage: map[string]analysis.ProviderCoverage{"metrics:cpu": cpu}}
			sequence := int64(0)
			if chunks {
				first := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderQueryProgress{NextQuery: 1, TotalQueries: 2}, Coverage: map[string]analysis.ProviderCoverage{"metrics:cpu": cpu}}
				acceptDynatraceFixture(t, s, source, sequence, first, until.Add(3*time.Minute))
				sequence++
				batch.Coverage = map[string]analysis.ProviderCoverage{"metrics:memory": cpu}
			}
			acceptDynatraceFixture(t, s, source, sequence, batch, until.Add(3*time.Minute+time.Second))
			job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if chunks {
				if !job.QueryWatermark.Equal(until) || job.StablePasses != 1 {
					t.Fatalf("earlier committed coverage was lost: %+v", job)
				}
				return
			}
			if !job.QueryWatermark.IsZero() || job.StablePasses != 0 || job.LastSuccessAt != nil {
				t.Fatalf("missing selected coverage advanced monitoring: %+v", job)
			}
			acceptDynatraceFixture(t, s, source, sequence+1, batch, until.Add(4*time.Minute+time.Second))
			job, err = s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
			if err != nil || job.State != "reconciling" || !job.QueryWatermark.IsZero() {
				t.Fatalf("repeated incomplete evidence closed required monitoring: %+v %v", job, err)
			}
		})
	}
}

// A finished attempt must release its reservation even when another attempt in
// the same pass is still running; otherwise every pass leaks a full reservation
// until the run's scan budget is exhausted.
func TestDynatraceScanSettlementIsPerAttempt(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	jobID := jobs[0].ID
	until := start.Add(time.Minute)
	reserve := func(id string) {
		t.Helper()
		if ok, err := s.ReserveDynatraceScan(source.ProjectID, jobID, id, 1_000_000_000, start); err != nil || !ok {
			t.Fatalf("reserve %s %v %v", id, ok, err)
		}
	}
	batch := func(progress analysis.ProviderQueryProgress) ProviderMonitorBatch {
		return ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, Progress: &progress}
	}
	// Pass 1: attempt A finished (5 KB), attempt B is still running.
	reserve("attempt-a")
	reserve("attempt-b")
	acceptDynatraceFixture(t, s, source, 0, batch(analysis.ProviderQueryProgress{TotalQueries: 2, NextQuery: 1, ScannedBytes: 5000, ScanReservationIDs: []string{"attempt-a", "attempt-b"}, ScanSettlements: []analysis.ProviderScanSettlement{{AttemptID: "attempt-a", ScannedBytes: 5000}}}), until)
	job, _ := s.GetRunMonitorJob(source.ProjectID, jobID)
	if job.ScanReservedBytes != 1_000_000_000 || job.ScanConsumedBytes != 5000 {
		t.Fatalf("pass 1 accounting reserved=%d consumed=%d", job.ScanReservedBytes, job.ScanConsumedBytes)
	}
	// Pass 2: B completes via its continuation.
	acceptDynatraceFixture(t, s, source, 1, batch(analysis.ProviderQueryProgress{TotalQueries: 2, NextQuery: 2, Complete: true, ScannedBytes: 7000, QueryCostKnown: true, ScanSettlements: []analysis.ProviderScanSettlement{{AttemptID: "attempt-b", ScannedBytes: 7000}}}), until)
	job, _ = s.GetRunMonitorJob(source.ProjectID, jobID)
	if job.ScanReservedBytes != 0 || job.ScanConsumedBytes != 12_000 {
		t.Fatalf("pass 2 accounting reserved=%d consumed=%d", job.ScanReservedBytes, job.ScanConsumedBytes)
	}
	// Many passes of cheap queries never exhaust the 8 GB collecting allowance.
	for pass := 0; pass < 20; pass++ {
		id := fmt.Sprintf("cheap-%02d", pass)
		reserve(id)
		acceptDynatraceFixture(t, s, source, int64(2+pass), batch(analysis.ProviderQueryProgress{TotalQueries: 2, NextQuery: 2, Complete: true, ScannedBytes: 1000, QueryCostKnown: true, ScanReservationIDs: []string{id}, ScanSettlements: []analysis.ProviderScanSettlement{{AttemptID: id, ScannedBytes: 1000}}}), until)
	}
	job, _ = s.GetRunMonitorJob(source.ProjectID, jobID)
	if job.ScanReservedBytes != 0 || job.ScanConsumedBytes != 32_000 {
		t.Fatalf("repeated passes reserved=%d consumed=%d", job.ScanReservedBytes, job.ScanConsumedBytes)
	}
	reject := func(sequence int64, progress analysis.ProviderQueryProgress, why string) {
		t.Helper()
		if err := s.AcceptProviderMonitorBatch(source.ProjectID, "", MonitorBatchReceipt{JobID: jobID, Sequence: sequence, Digest: strings.Repeat(fmt.Sprint(sequence%10), 64), ReceivedAt: until}, batch(progress), "", "", 0); err == nil {
			t.Fatal(why)
		}
	}
	reject(22, analysis.ProviderQueryProgress{TotalQueries: 2, NextQuery: 2, Complete: true, ScannedBytes: 1, ScanSettlements: []analysis.ProviderScanSettlement{{AttemptID: "attempt-a", ScannedBytes: 1}}}, "settled attempt charged twice")
	reject(22, analysis.ProviderQueryProgress{TotalQueries: 2, NextQuery: 2, Complete: true, ScannedBytes: 1, ScanSettlements: []analysis.ProviderScanSettlement{{AttemptID: "never-admitted", ScannedBytes: 1}}}, "unadmitted attempt settled")
	reserve("mismatch")
	reject(22, analysis.ProviderQueryProgress{TotalQueries: 2, NextQuery: 2, Complete: true, ScannedBytes: 99, ScanSettlements: []analysis.ProviderScanSettlement{{AttemptID: "mismatch", ScannedBytes: 1}}}, "pass total differs from settled attempts")
}
