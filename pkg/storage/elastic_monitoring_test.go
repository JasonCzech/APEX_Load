package storage

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
	"strconv"
	"strings"
	"testing"
	"time"
)

func elasticMonitorFixture(t *testing.T) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	s, _, now := monitorFixture(t)
	return seedElasticMonitorConnection(t, s, now)
}

func seedElasticMonitorConnection(t *testing.T, s *SQLiteStore, now time.Time) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	start := now.Add(-10 * time.Minute).Truncate(time.Minute)
	c := &ELKConnection{ID: "elastic", ProjectID: "monitor-p", Name: "Elastic", Enabled: true, Config: elastic.Config{Endpoint: "https://elastic.example", Product: "elasticsearch", AuthType: "api_key"}, APIKeySecretName: "API_KEY"}
	if err := s.CreateELKConnection(c); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateElasticConnectionHealth(c.ID, c.ProjectID, c.Revision, "connected", "", elastic.Capabilities{DSL: true, Logs: true}, now); err != nil || !ok {
		t.Fatalf("verification %t %v", ok, err)
	}
	m := MonitorSource{ID: "elastic-source", ProjectID: c.ProjectID, Name: "Elastic logs", Pack: "elastic", ExecutorMode: "direct", ConnectionID: c.ID, IntervalSeconds: 10, Enabled: true, Elastic: &elastic.Selection{ProjectID: c.ProjectID, Indices: []string{"logs-*"}, Logs: true}}
	saved, err := s.SaveMonitorSource(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	m = *saved
	if err = s.SaveTest(&TestRecord{ID: "monitor-run", Name: "run", ProjectID: c.ProjectID, ScenarioID: "monitor-scenario", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: m.ID, Required: true}}}}); err != nil {
		t.Fatal(err)
	}
	return s, m, start
}
func TestElasticConnectionRevisionVerificationAndPrivateSnapshot(t *testing.T) {
	s, m, start := elasticMonitorFixture(t)
	c, err := s.GetELKConnection(m.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if c.ConfigurationStatus() != "ready" {
		t.Fatal(c.ConfigurationStatus())
	}
	expected := c.Revision
	c.Name = "renamed"
	ok, err := s.UpdateElasticConnectionIfRevision(c, expected)
	if err != nil || !ok || c.VerifiedRevision != expected+1 {
		t.Fatalf("display revision: %+v %t %v", c, ok, err)
	}
	if ok, err = s.UpdateElasticConnectionHealth(c.ID, c.ProjectID, expected, "connected", "", elastic.Capabilities{}, time.Now().UTC()); err != nil || ok {
		t.Fatalf("stale verification %t %v", ok, err)
	}
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	var recovered RunMonitorJob
	if json.Unmarshal(b, &recovered) != nil || recovered.ElasticConnectionSnapshot == nil || recovered.ElasticConnectionSnapshot.APIKeySecretName != "API_KEY" {
		t.Fatal("private snapshot lost")
	}
	c.Config.Endpoint = "https://new.example"
	if ok, err = s.UpdateElasticConnectionIfRevision(c, c.Revision); err != nil || !ok {
		t.Fatal(err)
	}
	if c.VerifiedRevision != 0 || c.ConfigurationStatus() != "unverified" {
		t.Fatal("endpoint edit retained verification")
	}
	frozen, _ := s.GetRunMonitorJob(m.ProjectID, jobs[0].ID)
	if frozen.ElasticConnectionSnapshot.Config.Endpoint != "https://elastic.example" {
		t.Fatal("active snapshot redirected")
	}
}

func TestElasticSourceRequiresVerifiedSavedConnectionRevision(t *testing.T) {
	s, source, now := elasticMonitorFixture(t)
	assertElasticSourceRequiresVerifiedSavedConnectionRevision(t, s, source, now)
}

func assertElasticSourceRequiresVerifiedSavedConnectionRevision(t *testing.T, s *SQLiteStore, source MonitorSource, now time.Time) {
	t.Helper()
	connection := &ELKConnection{ID: "unverified-elastic", ProjectID: source.ProjectID, Name: "Unverified", Enabled: true, Config: elastic.Config{Endpoint: "https://unverified.example", Product: "elasticsearch", AuthType: "api_key"}, APIKeySecretName: "API_KEY"}
	if err := s.CreateELKConnection(connection); err != nil {
		t.Fatal(err)
	}
	source.ID, source.Revision, source.ConnectionID = "unverified-source", 0, connection.ID
	if _, err := s.SaveMonitorSource(source, 0); err == nil {
		t.Fatal("reusable source accepted an unverified connection revision")
	}
	if ok, err := s.UpdateElasticConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", elastic.Capabilities{DSL: true, Logs: true}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := s.SaveMonitorSource(source, 0); err != nil {
		t.Fatal("verified saved revision was rejected", err)
	}
}

func TestElasticCollectorSourceDefaultsExecutionFromConnection(t *testing.T) {
	s, source, now := elasticMonitorFixture(t)
	collector, err := s.GetMonitoringCollector("monitor-collector")
	if err != nil {
		t.Fatal(err)
	}
	collector.AllowedMetricPacks = append(collector.AllowedMetricPacks, "elastic")
	if err = s.SaveMonitoringCollector(collector); err != nil {
		t.Fatal(err)
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public := base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	connection := &ELKConnection{ID: "collector-elastic", ProjectID: source.ProjectID, Name: "Collector Elastic", Enabled: true, CollectionMode: "collector", CollectorAgentID: collector.ID, Config: elastic.Config{Endpoint: "https://collector-elastic.example", Product: "elasticsearch", AuthType: "api_key"}, APIKeySecretName: "API_KEY"}
	if err = s.CreateELKConnection(connection); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveCollectorRunReadiness(source.ProjectID, collector.ID, []string{"elastic", "elastic.v1"}, []string{connection.Config.Endpoint + "#" + public}, now); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateElasticConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", elastic.Capabilities{DSL: true, Logs: true}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	source.ID, source.Revision, source.ConnectionID, source.ExecutorMode, source.CollectorID, source.IntervalSeconds = "collector-source", 0, connection.ID, "", "", 0
	saved, err := s.SaveMonitorSource(source, 0)
	if err != nil || saved.ExecutorMode != "collector" || saved.CollectorID != collector.ID || saved.CollectorPublicKey != public || saved.IntervalSeconds != 10 {
		t.Fatalf("connection execution defaults lost: %+v %v", saved, err)
	}
}

func TestElasticPreparedMissingVerificationCannotUseLaterConnectionState(t *testing.T) {
	s, source, _ := elasticMonitorFixture(t)
	assertElasticPreparedMissingVerificationCannotUseLaterConnectionState(t, s, source)
}

func assertElasticPreparedMissingVerificationCannotUseLaterConnectionState(t *testing.T, s *SQLiteStore, source MonitorSource) {
	t.Helper()
	now := time.Now().UTC()
	connection, err := s.GetELKConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateElasticConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "unavailable", "", elastic.Capabilities{}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	run, err := s.GetTest("monitor-run")
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "QUEUED"
	run.MonitorBindings.Sources[0].Required = false
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.CreatePreparation(Preparation{ID: "unverified-preparation", ProjectID: source.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("e", 64), IdempotencyKey: "unverified", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil || prepared.Monitors.ExpandedElasticConnections[source.ID] != nil || len(prepared.Monitors.Warnings) != 1 || !strings.HasSuffix(prepared.Monitors.Warnings[0], "connection_not_ready") {
		t.Fatalf("unverified connection entered immutable snapshot: %+v %v", prepared, err)
	}
	ready, err := s.TransitionPreparation(prepared.ID, "preparing", "ready", prepared.Revision, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "INITIALIZING"
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumePreparation(ready.ID, ready.Revision, "", "starter", "Starter"); err != nil {
		t.Fatal(err)
	}
	// Verification after the immutable preparation was consumed applies only
	// to future runs. It cannot fill a missing execution authorization.
	if ok, err := s.UpdateElasticConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", elastic.Capabilities{DSL: true, Logs: true}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	jobs, err := s.StartRunMonitors(source.ProjectID, run.ID, run.ScenarioID, now, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 || jobs[0].State != "incomplete" || jobs[0].CoverageReason != "connection_not_ready" || jobs[0].ElasticConnectionSnapshot != nil {
		t.Fatalf("prepared source polled a later verification: %+v %v", jobs, err)
	}
}

func TestElasticPreparationReadinessChangeRequiresNewSnapshot(t *testing.T) {
	for _, beforeReady := range []bool{true, false} {
		t.Run(map[bool]string{true: "before_ready", false: "before_consumption"}[beforeReady], func(t *testing.T) {
			s, source, _ := elasticMonitorFixture(t)
			assertElasticPreparationReadinessChangeRequiresNewSnapshot(t, s, source, beforeReady)
		})
	}
}

func assertElasticPreparationReadinessChangeRequiresNewSnapshot(t *testing.T, s *SQLiteStore, source MonitorSource, beforeReady bool) {
	t.Helper()
	now := time.Now().UTC()
	connection, err := s.GetELKConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateElasticConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "unavailable", "", elastic.Capabilities{}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	run, err := s.GetTest("monitor-run")
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "QUEUED"
	run.MonitorBindings.Sources[0].Required = false
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.CreatePreparation(Preparation{ID: "readiness-cas", ProjectID: source.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("e", 64), IdempotencyKey: "readiness-cas", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !beforeReady {
		prepared, err = s.TransitionPreparation(prepared.ID, "preparing", "ready", prepared.Revision, "", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := s.UpdateElasticConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", elastic.Capabilities{DSL: true, Logs: true}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if beforeReady {
		_, err = s.TransitionPreparation(prepared.ID, "preparing", "ready", prepared.Revision, "", nil)
	} else {
		run.Status = "INITIALIZING"
		if err = s.SaveTest(run); err != nil {
			t.Fatal(err)
		}
		_, err = s.ConsumePreparation(prepared.ID, prepared.Revision, "", "starter", "Starter")
	}
	if !errors.Is(err, ErrPreparationConflict) {
		t.Fatalf("missing snapshot gained execution authority without a new preparation: %v", err)
	}
	stored, err := s.GetPreparation(prepared.ID)
	if err != nil || stored.Revision != prepared.Revision || stored.State != prepared.State || stored.Monitors.ExpandedElasticConnections[source.ID] != nil {
		t.Fatalf("rejected transition mutated immutable preparation: %+v %v", stored, err)
	}
}
func TestElasticCanonicalEvidenceIdentityCursorAndTerminalDrain(t *testing.T) {
	s, m, start := elasticMonitorFixture(t)
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job := jobs[0]
	end := start.Add(time.Minute)
	evidence := []analysis.ProviderEvidence{{ID: "same", Provider: "elasticsearch", Kind: "log", QueryID: "logs", Index: "logs-a", DocumentID: "same", TimestampUTC: start, Log: &analysis.ProviderLog{Message: "first"}}, {ID: "same", Provider: "elasticsearch", Kind: "log", QueryID: "logs", Index: "logs-b", DocumentID: "same", TimestampUTC: start, Log: &analysis.ProviderLog{Message: "second"}}}
	cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "private-pit", WindowFrom: start, WindowUntil: end}
	payload := ProviderMonitorBatch{Evidence: evidence, QueryFromUTC: start, QueryUntilUTC: end, Progress: &analysis.ProviderProgress{TotalQueries: 1, Continuation: cursor}}
	receipt := providerReceipt(t, job.ID, 0, payload, end)
	for range 2 {
		if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", receipt, payload, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	heads, err := s.ListProviderObservationHeads(m.ProjectID, "monitor-run", m.ID, "diagnostic", "", 10)
	if err != nil || len(heads) != 2 {
		t.Fatalf("index-scoped identities: %d %v", len(heads), err)
	}
	recovered, _ := s.GetRunMonitorJob(m.ProjectID, job.ID)
	if recovered.ElasticContinuation == nil || recovered.ElasticContinuation.PITID != "private-pit" || recovered.AcceptedArtifactBytes <= 0 {
		t.Fatal("transactional continuation lost")
	}
	payload.Progress.Complete = true
	payload.Progress.NextQuery = 1
	payload.Progress.Continuation = nil
	payload.QueryWindowEnd = &end
	payload.Coverage = map[string]analysis.ProviderCoverage{"logs": {Status: "complete", From: start, To: end}}
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 1, payload, end.Add(time.Second)), payload, "", "", 0); err != nil {
		t.Fatal(err)
	}
	run, _ := s.GetTest("monitor-run")
	run.Status = "COMPLETE"
	run.EndTime = &end
	if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	payload.Final = true
	for i := 0; i < 2; i++ {
		at := end.Add(time.Duration(65+15*i) * time.Second)
		if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, int64(2+i), payload, at), payload, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	recovered, _ = s.GetRunMonitorJob(m.ProjectID, job.ID)
	if recovered.State != "stopped" {
		t.Fatalf("did not settle: %+v", recovered)
	}
}

func TestElasticProviderTruncationReasonRemainsSticky(t *testing.T) {
	for _, reason := range []string{"result_limit", "response_bytes", "retention_quota", ""} {
		t.Run(reason, func(t *testing.T) {
			s, m, start := elasticMonitorFixture(t)
			jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			until := start.Add(time.Minute)
			batch := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, QueryWindowEnd: &until, Progress: &analysis.ProviderProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, Coverage: map[string]analysis.ProviderCoverage{"logs": {QueryID: "logs", Status: "truncated", Truncated: true, Reason: reason, From: start, To: until}}}
			if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, jobs[0].ID, 0, batch, until), batch, "", "", 0); err != nil {
				t.Fatal(err)
			}
			want := reason
			if want == "" {
				want = "retention_quota"
			}
			job, err := s.GetRunMonitorJob(m.ProjectID, jobs[0].ID)
			if err != nil || !job.TruncatedQueries["logs"] || job.Coverage["logs"].Reason != want {
				t.Fatalf("initial truncation %+v %v", job, err)
			}
			// A later provider pass skips the disabled query using its generic
			// retention status. Keep the actual first limiting reason instead.
			batch.Coverage["logs"] = analysis.ProviderCoverage{QueryID: "logs", Status: "truncated", Truncated: true, Reason: "retention_quota", From: start, To: until}
			if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 1, batch, until.Add(time.Second)), batch, "", "", 0); err != nil {
				t.Fatal(err)
			}
			job, err = s.GetRunMonitorJob(m.ProjectID, job.ID)
			if err != nil || job.Coverage["logs"].Reason != want {
				t.Fatalf("disabled-query pass replaced reason %+v %v", job.Coverage, err)
			}
			run, _ := s.GetTest(job.RunID)
			run.Status, run.EndTime = "COMPLETE", &until
			if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
				t.Fatal(err)
			}
			batch.Final = true
			for i := range 2 {
				at := until.Add(time.Duration(65+15*i) * time.Second)
				if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, int64(2+i), batch, at), batch, "", "", 0); err != nil {
					t.Fatal(err)
				}
			}
			job, err = s.GetRunMonitorJob(m.ProjectID, job.ID)
			if err != nil || job.State != "incomplete" || job.CoverageReason != want {
				t.Fatalf("terminal truncation reason %+v %v", job, err)
			}
		})
	}
}

func TestElasticFinalTruncationReasonPrefersGenuineRetentionQuota(t *testing.T) {
	coverage := map[string]analysis.ProviderCoverage{
		"logs":  {QueryID: "logs", Status: "truncated", Truncated: true, Reason: "result_limit"},
		"spans": {QueryID: "spans", Status: "truncated", Truncated: true, Reason: "retention_quota"},
	}
	if got := elasticFinalTruncationReason(coverage); got != "retention_quota" {
		t.Fatalf("mixed truncation reason %s", got)
	}
	delete(coverage, "spans")
	coverage["healthy"] = analysis.ProviderCoverage{Status: "complete", Reason: "retention_quota"}
	if got := elasticFinalTruncationReason(coverage); got != "result_limit" {
		t.Fatalf("healthy query displaced actual truncation %s", got)
	}
}

func TestElasticHealthOnlyFinalizationReusesSettledSnapshot(t *testing.T) {
	s, m, start := elasticMonitorFixture(t)
	m.Elastic.Logs, m.Elastic.ClusterHealth = false, true
	saved, err := s.SaveMonitorSource(m, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m = *saved
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	job := jobs[0]
	end := start.Add(time.Minute)
	run, _ := s.GetTest(job.RunID)
	run.Status, run.EndTime = "COMPLETE", &end
	if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	observed := end.Add(65 * time.Second)
	query := elastic.QueryDefinitions(*m.Elastic)[0].QueryID
	value := 1.0
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: m.ProjectID, RunID: job.RunID, Source: m.ID, MetricName: "elasticsearch.cluster.nodes", Unit: "count", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Value: &value, TimestampUTC: observed, Provider: &analysis.ProviderMeasurement{Name: "elasticsearch", QueryID: query, MetricID: query, WindowKind: "instant", BucketStart: observed, BucketEnd: observed, Statistic: "value", Settled: true}}
	coverage := analysis.ProviderCoverage{QueryID: query, Status: "complete", From: start, To: end, Points: 1, AsOf: &observed}
	batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{point}, QueryFromUTC: start, QueryUntilUTC: end, QueryWindowEnd: &end, Final: true, Progress: &analysis.ProviderProgress{TotalQueries: 1, NextQuery: 1, Complete: true}, Coverage: map[string]analysis.ProviderCoverage{query: coverage}}
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 0, batch, observed), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	first, err := s.GetRunMonitorJob(m.ProjectID, job.ID)
	if err != nil || first.State != "reconciling" || first.StablePasses != 1 {
		t.Fatalf("initial health snapshot %+v %v", first, err)
	}
	// The provider carries the successful post-settlement observation with
	// its original AsOf; stability never invents a second instant measurement.
	batch.Points = nil
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 1, batch, observed.Add(15*time.Second)), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	frozen, err := s.GetRunMonitorJob(m.ProjectID, job.ID)
	if err != nil || frozen.State != "stopped" || frozen.StablePasses < 2 || frozen.FinalDigest != first.FinalDigest || frozen.Coverage[query].AsOf == nil || !frozen.Coverage[query].AsOf.Equal(observed) {
		t.Fatalf("health-only final coverage %+v %v", frozen, err)
	}
	heads, err := s.ListProviderObservationHeads(m.ProjectID, job.RunID, m.ID, "point", "", 10)
	if err != nil || len(heads) != 1 || heads[0].Sequence != 0 {
		t.Fatalf("reused health coverage fabricated heads %+v %v", heads, err)
	}
}
func TestElasticInspectionLeaseOwnershipAndCancellation(t *testing.T) {
	s, m, _ := elasticMonitorFixture(t)
	now := time.Now().UTC()
	op := &ElasticInspection{ID: "inspect", ProjectID: m.ProjectID, ConnectionID: m.ConnectionID, ConnectionRevision: 1, Kind: "fields", PayloadJSON: "{}", CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if err := s.CreateElasticInspection(op); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"owner", "owner"} {
		if ok, err := s.ClaimElasticInspection(op.ID, owner, now, now.Add(time.Minute)); err != nil || !ok {
			t.Fatalf("claim %v %v", ok, err)
		}
	}
	if ok, err := s.CompleteElasticInspection(op.ID, "other", "{}", "", now); err != nil || ok {
		t.Fatal("stale inspection owner accepted")
	}
	if ok, err := s.CancelElasticInspection(op.ID, m.ProjectID, m.ConnectionID, now); err != nil || !ok {
		t.Fatal(err)
	}
	if ok, _ := s.CompleteElasticInspection(op.ID, "owner", "{}", "", now); ok {
		t.Fatal("cancelled inspection published")
	}
	consumers, err := s.ListElasticSecretConsumers(m.ProjectID, "api-key")
	if err != nil || len(consumers) == 0 || !strings.Contains(consumers[0].ResourceType, "elastic") {
		t.Fatalf("secret consumers %+v %v", consumers, err)
	}
}

func TestElasticSourceDefaultsAndDeletionFence(t *testing.T) {
	s, m, start := elasticMonitorFixture(t)
	m.ID = "default-source"
	m.Name = "defaults"
	m.Revision = 0
	m.IntervalSeconds = 0
	m.ExecutorMode = ""
	saved, err := s.SaveMonitorSource(m, 0)
	if err != nil || saved.IntervalSeconds != 10 || saved.ExecutorMode != "direct" {
		t.Fatalf("default source %+v %v", saved, err)
	}
	m.ID = "health-source"
	m.Elastic = &elastic.Selection{ProjectID: m.ProjectID, ClusterHealth: true}
	health, err := s.SaveMonitorSource(m, 0)
	if err != nil || health.IntervalSeconds != 30 {
		t.Fatalf("health source %+v %v", health, err)
	}
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	active := jobs[0].Source
	if err = s.DeleteMonitorSource(active.ProjectID, active.ID, active.Revision); err == nil {
		t.Fatal("active source deleted")
	}
	if _, err = s.DeleteELKConnection(active.ConnectionID, active.ProjectID); err == nil {
		t.Fatal("active connection deleted")
	}
	if err = s.StopRunMonitors(active.ProjectID, "monitor-run", start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteMonitorSource(active.ProjectID, active.ID, active.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalPreparationMonitoringWarningsAreDurable(t *testing.T) {
	s, m, _ := elasticMonitorFixture(t)
	m.Enabled = false
	if _, err := s.SaveMonitorSource(m, m.Revision); err != nil {
		t.Fatal(err)
	}
	run, _ := s.GetTest("monitor-run")
	run.MonitorBindings = &MonitorBindings{Sources: []MonitorBinding{{SourceID: m.ID}}}
	if err := s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshot, err := preparationMonitorSnapshotTx(tx, m.ProjectID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Warnings) != 1 || !strings.HasSuffix(snapshot.Warnings[0], "source_disabled") {
		t.Fatalf("optional warning absent %+v", snapshot.Warnings)
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var recovered PreparationMonitorSnapshot
	if json.Unmarshal(body, &recovered) != nil || len(recovered.Warnings) != 1 {
		t.Fatal("optional warning lost across preparation restart")
	}
}

func TestPreparationReadinessWarningsPreserveTransitionsAndConsumption(t *testing.T) {
	s, m, _ := elasticMonitorFixture(t)
	now := time.Now().UTC()
	run, _ := s.GetTest("monitor-run")
	run.Status = "QUEUED"
	run.MonitorBindings.Sources[0].Required = false
	if err := s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePreparation(Preparation{ID: "warning-preparation", ProjectID: m.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("c", 64), IdempotencyKey: "warnings", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	warning := "monitor " + m.ID + ": optional_connection_unavailable"
	if err = s.SetPreparationMonitorWarnings(m.ProjectID, run.ID, []string{warning, warning}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPreparation(p.ID)
	if err != nil || got.Revision != p.Revision || len(got.Monitors.Warnings) != 1 || got.Monitors.Warnings[0] != warning {
		t.Fatalf("durable bounded readiness warnings %+v %v", got, err)
	}
	ready, err := s.TransitionPreparation(p.ID, "preparing", "ready", p.Revision, "", nil)
	if err != nil || len(ready.Monitors.Warnings) != 1 {
		t.Fatalf("warnings blocked readiness %+v %v", ready, err)
	}
	second := "monitor " + m.ID + ": optional_schema_unavailable"
	if err = s.SetPreparationMonitorWarnings(m.ProjectID, run.ID, []string{second}); err != nil {
		t.Fatal(err)
	}
	run.Status = "INITIALIZING"
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	consumed, err := s.ConsumePreparation(p.ID, ready.Revision, "", "starter", "Starter")
	if err != nil || len(consumed.Monitors.Warnings) != 2 {
		t.Fatalf("warnings blocked consumption %+v %v", consumed, err)
	}
	if err = s.SetPreparationMonitorWarnings(m.ProjectID, run.ID, []string{"after consumption"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetPreparation(p.ID)
	if err != nil || len(got.Monitors.Warnings) != 2 {
		t.Fatalf("consumed warnings changed %+v %v", got, err)
	}
	for _, invalid := range [][]string{{""}, {"vendor\nbody"}, {strings.Repeat("x", 513)}, make([]string, MaxRunMonitorSources*3+1)} {
		if err = s.SetPreparationMonitorWarnings(m.ProjectID, run.ID, invalid); err == nil {
			t.Fatal("unbounded or unsafe warning accepted")
		}
	}
}

func TestElasticIngestionWatermarkAdvancesOnlyAfterDurableWholePass(t *testing.T) {
	s, m, start := elasticMonitorFixture(t)
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	until := start.Add(time.Minute)
	ingested := until.Add(time.Second)
	batch := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, Evidence: []analysis.ProviderEvidence{{ID: "doc", Provider: "elasticsearch", Kind: "log", QueryID: "logs", Index: "logs-a", DocumentID: "doc", TimestampUTC: start, IngestedAt: &ingested, Log: &analysis.ProviderLog{Message: "indexed late"}}}, Progress: &analysis.ProviderProgress{TotalQueries: 2, NextQuery: 1}}
	receipt := providerReceipt(t, jobs[0].ID, 0, batch, ingested.Add(time.Second))
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", receipt, batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	job, _ := s.GetRunMonitorJob(m.ProjectID, jobs[0].ID)
	if !job.ElasticIngestionWatermark.IsZero() || !job.ElasticPendingIngestionWatermark.Equal(ingested) {
		t.Fatalf("prefix advanced ingestion watermark %+v", job)
	}
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", receipt, batch, "", "", 0); err != nil {
		t.Fatal("identical checkpoint retry failed", err)
	}
	batch.Evidence = nil
	batch.Progress.NextQuery = 2
	batch.Progress.Complete = true
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 1, batch, ingested.Add(2*time.Second)), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	job, _ = s.GetRunMonitorJob(m.ProjectID, job.ID)
	if !job.ElasticIngestionWatermark.Equal(ingested) || !job.ElasticPendingIngestionWatermark.IsZero() {
		t.Fatalf("completed pass did not commit ingestion watermark %+v", job)
	}
	future := ingested.Add(time.Hour)
	batch.Evidence = []analysis.ProviderEvidence{{ID: "future", Provider: "elasticsearch", Kind: "log", QueryID: "logs", Index: "logs-a", DocumentID: "future", TimestampUTC: start, IngestedAt: &future, Log: &analysis.ProviderLog{Message: "invalid ingestion clock"}}}
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 2, batch, ingested.Add(3*time.Second)), batch, "", "", 0); err == nil {
		t.Fatal("future ingestion watermark accepted")
	}
	job, _ = s.GetRunMonitorJob(m.ProjectID, job.ID)
	if job.NextSequence != 2 || !job.ElasticIngestionWatermark.Equal(ingested) {
		t.Fatal("invalid batch changed durable ingestion progress")
	}
	batch.Evidence[0].IngestedAt = &ingested
	batch.Progress.NextQuery = 1
	batch.Progress.Complete = false
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 2, batch, ingested.Add(4*time.Second)), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	run, _ := s.GetTest("monitor-run")
	run.Status = "COMPLETE"
	run.EndTime = &until
	if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	job, _ = s.GetRunMonitorJob(m.ProjectID, job.ID)
	if !job.ElasticIngestionWatermark.Equal(ingested) || !job.ElasticPendingIngestionWatermark.IsZero() {
		t.Fatal("load end lost committed watermark or retained unfinished prefix")
	}
}

func TestElasticCoverageCountsAccumulatePagesAndResetReadPass(t *testing.T) {
	s, m, start := elasticMonitorFixture(t)
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	until := start.Add(time.Minute)
	coverage := analysis.ProviderCoverage{QueryID: "logs", Status: "partial", From: start, To: until, Evidence: 100}
	batch := ProviderMonitorBatch{QueryFromUTC: start, QueryUntilUTC: until, Coverage: map[string]analysis.ProviderCoverage{"logs": coverage}, Progress: &analysis.ProviderProgress{TotalQueries: 1}}
	for seq := int64(0); seq < 2; seq++ {
		batch.Evidence = make([]analysis.ProviderEvidence, 100)
		for i := range batch.Evidence {
			id := strconv.Itoa(int(seq)*100 + i)
			batch.Evidence[i] = analysis.ProviderEvidence{ID: id, DocumentID: id, QueryID: "logs", Index: "logs-a", Provider: "elasticsearch", Kind: "log", TimestampUTC: start, Log: &analysis.ProviderLog{Message: "matching document"}}
		}
		receipt := providerReceipt(t, jobs[0].ID, seq, batch, until.Add(time.Duration(seq)*time.Second))
		for range 2 {
			if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", receipt, batch, "", "", 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	coverage.Status, coverage.Evidence = "empty", 0
	batch.Evidence = nil
	batch.Coverage["logs"] = coverage
	batch.Progress.NextQuery, batch.Progress.Complete = 1, true
	batch.QueryWindowEnd = &until
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, jobs[0].ID, 2, batch, until.Add(2*time.Second)), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	job, _ := s.GetRunMonitorJob(m.ProjectID, jobs[0].ID)
	if got := job.Coverage["logs"]; got.Status != "complete" || got.Evidence != 200 {
		t.Fatalf("EOF dropped counts or retries inflated pass %+v", got)
	}
	// Reconciliation may re-read precisely the same window. Its count is a new
	// read horizon, so it must not add the previous pass a second time.
	coverage.Status, coverage.Evidence = "complete", 1
	batch.Evidence = []analysis.ProviderEvidence{{ID: "revised", DocumentID: "revised", QueryID: "logs", Index: "logs-a", Provider: "elasticsearch", Kind: "log", TimestampUTC: start, Log: &analysis.ProviderLog{Message: "matching document"}}}
	batch.Coverage["logs"] = coverage
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, jobs[0].ID, 3, batch, until.Add(3*time.Second)), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	job, _ = s.GetRunMonitorJob(m.ProjectID, jobs[0].ID)
	if job.Coverage["logs"].Evidence != 1 {
		t.Fatalf("new read pass accumulated old counts %+v", job.Coverage["logs"])
	}
}

func TestElasticConcreteScopeFreezesUntilPassCompletion(t *testing.T) {
	s, m, start := elasticMonitorFixture(t)
	jobs, err := s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	until := start.Add(time.Minute)
	scope := []string{".ds-logs-orders-2026.10.05-000001"}
	cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "private-scope-pit", WindowFrom: start, WindowUntil: until, ResolvedIndices: scope}
	batch := ProviderMonitorBatch{ResolvedIndices: scope, QueryFromUTC: start, QueryUntilUTC: until, Progress: &analysis.ProviderProgress{TotalQueries: 1, Continuation: cursor}}
	receipt := providerReceipt(t, jobs[0].ID, 0, batch, until)
	for range 2 {
		if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", receipt, batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	job, _ := s.GetRunMonitorJob(m.ProjectID, jobs[0].ID)
	if len(job.ElasticResolvedIndices) != 1 || job.ElasticResolvedIndices[0] != scope[0] {
		t.Fatal("concrete scope not durable")
	}
	for _, changed := range [][]string{{"logs-unapproved"}, {".security"}, {"logs-*"}} {
		bad := batch
		bad.ResolvedIndices = changed
		if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 1, bad, until.Add(time.Second)), bad, "", "", 0); err == nil {
			t.Fatal("changed or unsafe fixed-window scope accepted", changed)
		}
	}
	batch.Progress = &analysis.ProviderProgress{TotalQueries: 1, NextQuery: 1, Complete: true}
	batch.QueryWindowEnd = &until
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 1, batch, until.Add(time.Second)), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	job, _ = s.GetRunMonitorJob(m.ProjectID, job.ID)
	if len(job.ElasticResolvedIndices) != 0 || job.NextSequence != 2 {
		t.Fatal("completed pass retained private scope")
	}
	batch.QueryWindowEnd = nil
	newScope := []string{".ds-logs-orders-2026.10.05-000002"}
	cursorCopy := *cursor
	cursorCopy.ResolvedIndices = newScope
	batch.ResolvedIndices = nil // The owned continuation can supply its scope.
	batch.Progress = &analysis.ProviderProgress{TotalQueries: 1, Continuation: &cursorCopy}
	if err = s.AcceptProviderMonitorBatch(m.ProjectID, "", providerReceipt(t, job.ID, 2, batch, until.Add(2*time.Second)), batch, "", "", 0); err != nil {
		t.Fatal("new polling window could not resolve rollover", err)
	}
	job, _ = s.GetRunMonitorJob(m.ProjectID, job.ID)
	if len(job.ElasticResolvedIndices) != 1 || job.ElasticResolvedIndices[0] != newScope[0] {
		t.Fatal("new read horizon did not freeze rollover scope")
	}
	run, _ := s.GetTest(job.RunID)
	run.Status, run.EndTime = "COMPLETE", &until
	if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	job, _ = s.GetRunMonitorJob(m.ProjectID, job.ID)
	if len(job.ElasticResolvedIndices) != 0 || len(job.ElasticRetiredContinuations) != 1 || job.ElasticRetiredContinuations[0].PITID != cursor.PITID || len(job.ElasticRetiredContinuations[0].ResolvedIndices) != 0 {
		t.Fatal("load-end scope not reset or retired cleanup lost provenance")
	}
}
