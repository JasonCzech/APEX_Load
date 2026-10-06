package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestElasticQuotaCountsOnlyElasticBytesAndEvidenceWithSharedSeries(t *testing.T) {
	s, source, start := elasticMonitorFixture(t)
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job := jobs[0]
	for _, fixture := range []struct {
		id, pack  string
		bytes     int64
		artifacts int64
	}{{"other-elastic", "elastic", 2 << 20, 123}, {"other-appd", "appdynamics", 32 << 20, 64 << 20}} {
		other := job
		other.ID, other.Source.ID, other.Source.Pack = fixture.id, fixture.id, fixture.pack
		other.AcceptedArtifactBytes = fixture.artifacts
		body, err := json.Marshal(other)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`INSERT INTO run_monitor_jobs(id,project_id,test_id,collector_id,state,expires_at,payload) VALUES(?,?,?,?,?,?,?)`, other.ID, other.ProjectID, other.RunID, "", "collecting", other.ExpiresAt, string(body)); err != nil {
			t.Fatal(err)
		}
		// These persisted metadata totals represent existing retained artifacts;
		// the quota scan does not need to reopen their immutable content.
		if _, err = s.db.Exec(`INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,content_digest,record_bytes,series_key) VALUES(?,?,0,0,'diagnostic',?,?, '')`, other.ID, fixture.id+"-evidence", strings.Repeat("a", 64), fixture.bytes); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`INSERT INTO run_monitor_observation_heads(job_id,record_key,sequence,record_index,record_kind,content_digest,record_bytes,series_key) VALUES(?,?,0,0,'point',?,0,?)`, other.ID, fixture.id+"-point", strings.Repeat("b", 64), fixture.id+"-series"); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	quota, err := newElasticQuotaTracker(tx, job)
	if err != nil {
		t.Fatal(err)
	}
	if quota.runBytes != 2<<20 || quota.runRecords != 1 || quota.runSeries != 2 {
		t.Fatalf("provider quotas mixed unrelated data or lost shared series: %+v", quota)
	}
	artifacts, err := elasticAcceptedArtifactBytesTx(tx, job)
	if err != nil || artifacts != 123 {
		t.Fatalf("Elastic immutable artifact total=%d, err=%v", artifacts, err)
	}
	evidence := analysis.ProviderEvidence{ID: "new", Provider: "elasticsearch", Kind: "log", QueryID: "logs", TimestampUTC: start, Log: &analysis.ProviderLog{Message: "retained"}}
	if allowed, err := quota.allow(analysis.MetricPoint{}, &evidence); err != nil || !allowed {
		t.Fatalf("unrelated provider bytes truncated Elastic evidence: allowed=%v err=%v", allowed, err)
	}
}

func TestEvidenceMetadataFilteringPrecedesPagination(t *testing.T) {
	s, m, start := elasticMonitorFixture(t)
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	end := start.Add(time.Minute)
	evidence := []analysis.ProviderEvidence{
		{ID: "a", Kind: "log", Provider: "elasticsearch", QueryID: "logs", Service: "checkout", Severity: "error", TimestampUTC: start, Log: &analysis.ProviderLog{Message: "a"}},
		{ID: "b", Kind: "span", Provider: "elasticsearch", QueryID: "traces", Service: "checkout", Severity: "error", TimestampUTC: start.Add(time.Nanosecond), Span: &analysis.ProviderSpan{TraceID: "trace", SpanID: "one"}},
		{ID: "c", Kind: "span", Provider: "elasticsearch", QueryID: "traces", Service: "checkout", Severity: "error", TimestampUTC: start.Add(2 * time.Nanosecond), Span: &analysis.ProviderSpan{TraceID: "trace", SpanID: "two"}},
		{ID: "d", Kind: "span", Provider: "elasticsearch", QueryID: "traces", Service: "other", TimestampUTC: start.Add(3 * time.Nanosecond), Span: &analysis.ProviderSpan{TraceID: "other", SpanID: "three"}},
	}
	b := ProviderMonitorBatch{Evidence: evidence, QueryFromUTC: start, QueryUntilUTC: end}
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, jobs[0].ID, 0, b, end), b, "", "", 0); err != nil {
		t.Fatal(err)
	}
	from, until := start.Add(time.Nanosecond), start.Add(3*time.Nanosecond)
	f := EvidenceHeadFilter{Kind: "span", Service: "checkout", Severity: "error", TraceID: "trace", From: &from, Until: &until}
	first, err := s.ListProviderEvidenceHeads(m.ProjectID, "monitor-run", "", "", f, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first %v %v", first, err)
	}
	if len(first[0].ContentDigest) != 64 || VerifyProviderHeadContent(first[0], evidence[first[0].Index]) != nil {
		t.Fatal("filtered evidence head lost immutable content integrity")
	}
	tampered := evidence[first[0].Index]
	tampered.ID = "altered"
	if VerifyProviderHeadContent(first[0], tampered) == nil {
		t.Fatal("altered filtered evidence passed content integrity")
	}
	second, err := s.ListProviderEvidenceHeads(m.ProjectID, "monitor-run", m.ID, first[0].Key, f, 2)
	if err != nil || len(second) != 1 || second[0].Key == first[0].Key {
		t.Fatalf("second %v %v", second, err)
	}
	for _, test := range []struct{ project, run, source string }{{"monitor-other", "monitor-run", ""}, {m.ProjectID, "other", ""}, {m.ProjectID, "monitor-run", "other"}} {
		heads, err := s.ListProviderEvidenceHeads(test.project, test.run, test.source, "", f, 100)
		if err != nil || len(heads) != 0 {
			t.Fatalf("scope leak %v %v", heads, err)
		}
	}
	f.Until = &from
	if _, err = s.ListProviderEvidenceHeads(m.ProjectID, "monitor-run", "", "", f, 100); err == nil {
		t.Fatal("empty time interval accepted")
	}
}

func TestElasticRetentionRetryAndIndependentMetricFreeze(t *testing.T) {
	s, m, start := elasticMonitorFixture(t)
	m.Elastic.MaxRetainedRecords = 1
	updated, err := s.SaveMonitorSource(m, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m = *updated
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job := jobs[0]
	end := start.Add(time.Minute)
	d := func(id string) analysis.ProviderEvidence {
		return analysis.ProviderEvidence{ID: id, Kind: "log", Provider: "elasticsearch", QueryID: "logs", TimestampUTC: start, Log: &analysis.ProviderLog{Message: id}}
	}
	b := ProviderMonitorBatch{Evidence: []analysis.ProviderEvidence{d("a"), d("b")}, QueryFromUTC: start, QueryUntilUTC: end, QueryWindowEnd: &end, Progress: &analysis.ProviderProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, Coverage: map[string]analysis.ProviderCoverage{"logs": {QueryID: "logs", Status: "complete", From: start, To: end}}}
	receipt := providerReceipt(t, job.ID, 0, b, end)
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", receipt, b, "", "", 0); err != nil {
		t.Fatal(err)
	}
	first, _ := s.GetRunMonitorJob(m.ProjectID, job.ID)
	if !first.TruncatedQueries["logs"] || !first.Coverage["logs"].Truncated {
		t.Fatalf("truncation absent %+v", first)
	}
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", receipt, b, "", "", 0); err != nil {
		t.Fatal(err)
	}
	second, _ := s.GetRunMonitorJob(m.ProjectID, job.ID)
	if second.AcceptedArtifactBytes != first.AcceptedArtifactBytes || second.RetainedEvidenceBytes != first.RetainedEvidenceBytes {
		t.Fatal("retry consumed quota")
	}
	changed := b
	changed.Evidence = []analysis.ProviderEvidence{d("changed")}
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 0, changed, end), changed, "", "", 0); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	heads, err := s.ListProviderObservationHeads(m.ProjectID, "monitor-run", m.ID, "diagnostic", "", 10)
	if err != nil || len(heads) != 1 {
		t.Fatalf("count ceiling %+v %v", heads, err)
	}
	run, _ := s.GetTest("monitor-run")
	run.Status = "COMPLETE"
	run.EndTime = &end
	if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	b.Final = true
	b.Evidence = nil
	for i := 0; i < 2; i++ {
		at := end.Add(time.Duration(65+i*15) * time.Second)
		if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, int64(1+i), b, at), b, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	final, _ := s.GetRunMonitorJob(m.ProjectID, job.ID)
	if final.State != "incomplete" || !final.Coverage["logs"].Truncated {
		t.Fatalf("false completion %+v", final)
	}
}
