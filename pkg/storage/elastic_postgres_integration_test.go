//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
)

func TestElasticPostgresVerifiedSourcePreparationAuthorization(t *testing.T) {
	for _, scenario := range []string{"source_verification", "consumed_missing_snapshot", "before_ready", "before_consumption", "cleanup_backpressure", "inspection_cleanup_claim", "owner_cancel_fence"} {
		t.Run(scenario, func(t *testing.T) {
			pg, _ := managementPostgresFixture(t)
			s, _, now := seedMonitorFixture(t, pg.SQLiteStore)
			s, source, now := seedElasticMonitorConnection(t, s, now)
			switch scenario {
			case "source_verification":
				assertElasticSourceRequiresVerifiedSavedConnectionRevision(t, s, source, now)
			case "consumed_missing_snapshot":
				assertElasticPreparedMissingVerificationCannotUseLaterConnectionState(t, s, source)
			case "cleanup_backpressure":
				assertElasticCleanupBackpressureAndExactAcknowledgementResume(t, s, source, now)
			case "inspection_cleanup_claim":
				assertElasticInspectionCleanupBlocksReexecutionUntilExactAcknowledgement(t, s, source)
			case "owner_cancel_fence":
				assertElasticInspectionOwnerCancellationCannotReplaceNewClaim(t, s, source)
			default:
				assertElasticPreparationReadinessChangeRequiresNewSnapshot(t, s, source, scenario == "before_ready")
			}
		})
	}
}

func TestElasticPostgresDurableEvidenceMetadataAndRetry(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("elastic_durable_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	pg, err := NewPostgresStore(postgresIntegrationURLWithSearchPath(t, url, schema), StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	s, _, now := seedMonitorFixture(t, pg.SQLiteStore)
	connection := &ELKConnection{ID: "elastic", ProjectID: "monitor-p", Name: "Elastic", Enabled: true, Config: elastic.Config{Endpoint: "https://elastic.example", Product: "elasticsearch", AuthType: "api_key"}, APIKeySecretName: "API_KEY"}
	if err = s.CreateELKConnection(connection); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.UpdateElasticConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", elastic.Capabilities{Product: "elasticsearch", DSL: true, PIT: true, ShardDoc: true}, now); err != nil || !ok {
		t.Fatalf("verification %v %v", ok, err)
	}
	source, err := s.SaveMonitorSource(MonitorSource{ID: "elastic-source", ProjectID: connection.ProjectID, Name: "Elastic", Enabled: true, Pack: "elastic", ExecutorMode: "direct", ConnectionID: connection.ID, IntervalSeconds: 10, Elastic: &elastic.Selection{ProjectID: connection.ProjectID, Indices: []string{"logs-*"}, Logs: true}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.GetTest("monitor-run")
	run.MonitorBindings = &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID}}}
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.StartRunMonitors(source.ProjectID, run.ID, run.ScenarioID, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Minute)
	concrete := ".ds-logs-orders-2026.10.05-000001"
	batch := ProviderMonitorBatch{ResolvedIndices: []string{concrete}, QueryFromUTC: now, QueryUntilUTC: until, Evidence: []analysis.ProviderEvidence{{ID: "doc", DocumentID: "doc", Index: concrete, QueryID: "logs", Provider: "elasticsearch", Kind: "log", Service: "checkout", Severity: "error", TimestampUTC: now, Log: &analysis.ProviderLog{Message: "failed"}}}}
	receipt := providerReceipt(t, jobs[0].ID, 0, batch, until)
	for range 2 {
		if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", receipt, batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	heads, err := s.ListProviderEvidenceHeads(source.ProjectID, run.ID, source.ID, "", EvidenceHeadFilter{Service: "checkout", Severity: "error", Kind: "log"}, 10)
	if err != nil || len(heads) != 1 {
		t.Fatalf("canonical metadata %v %v", heads, err)
	}
	job, err := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if err != nil || job.NextSequence != 1 || job.ElasticConnectionSnapshot == nil || job.AcceptedArtifactBytes <= 0 || len(job.ElasticResolvedIndices) != 1 || job.ElasticResolvedIndices[0] != concrete {
		t.Fatalf("durable job %+v %v", job, err)
	}
	// A rollover cannot silently change an already accepted PIT window. Scope
	// rejection occurs before recording a receipt or charging immutable quota.
	changed := batch
	changed.ResolvedIndices = []string{".ds-logs-orders-2026.10.05-000002"}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 1, changed, until), changed, "", "", 0); err == nil {
		t.Fatal("PostgreSQL accepted a changed fixed-window concrete scope")
	}
	unchanged, err := s.GetRunMonitorJob(source.ProjectID, job.ID)
	if err != nil || unchanged.NextSequence != 1 || unchanged.AcceptedArtifactBytes != job.AcceptedArtifactBytes || len(unchanged.ElasticResolvedIndices) != 1 || unchanged.ElasticResolvedIndices[0] != concrete {
		t.Fatalf("rejected scope mutated job %+v %v", unchanged, err)
	}
	accepted, err := s.ListMonitorBatchReceipts(source.ProjectID, run.ID, source.ID, -1, 10)
	if err != nil || len(accepted) != 1 {
		t.Fatalf("rejected scope created receipt %+v %v", accepted, err)
	}
	// Retire a live PIT at the load-end boundary, then prove that only the
	// current execution owner can acknowledge its exact private cleanup handle.
	cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "owned-private-pit", WindowFrom: now, WindowUntil: until, ResolvedIndices: []string{concrete}}
	batch.Evidence = nil
	batch.Progress = &analysis.ProviderProgress{TotalQueries: 1, Continuation: cursor}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 1, batch, until), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	run.Status = "COMPLETE"
	run.EndTime = &until
	if _, err = s.SaveTestWithRunMonitors(run, "", "", true); err != nil {
		t.Fatal(err)
	}
	cleanup, _, err := s.ListElasticContinuationCleanupJobs(source.ProjectID, run.ID, "", 100)
	if err != nil || len(cleanup) != 1 || cleanup[0].State != "reconciling" || cleanup[0].ElasticContinuation.PITID != cursor.PITID || len(cleanup[0].ElasticContinuation.ResolvedIndices) != 0 || len(cleanup[0].ElasticResolvedIndices) != 0 {
		t.Fatalf("retired cleanup %+v %v", cleanup, err)
	}
	cursor = cleanup[0].ElasticContinuation
	acquired, oldTerm, err := s.TryAcquireSchedulerLeaseTerm("elastic-pg-cleanup", "old", now, time.Minute)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	if err = s.ReleaseSchedulerLease("elastic-pg-cleanup", "old"); err != nil {
		t.Fatal(err)
	}
	acquired, newTerm, err := s.TryAcquireSchedulerLeaseTerm("elastic-pg-cleanup", "new", time.Now().UTC(), time.Minute)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	if ok, err := s.ClearElasticContinuation(source.ProjectID, job.ID, cursor, "elastic-pg-cleanup", "old", oldTerm.Generation); err != nil || ok {
		t.Fatal("stale owner cleared cleanup", ok, err)
	}
	if err = s.CloseExpiredProviderMonitors(source.ProjectID, run.ID, until.Add(301*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertCleanupConsumer := func(want bool) {
		t.Helper()
		consumers, err := s.ListElasticSecretConsumers(source.ProjectID, "API_KEY")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, consumer := range consumers {
			found = found || consumer.ResourceType == "run_monitor_job" && consumer.ResourceID == job.ID
		}
		if found != want {
			t.Fatalf("terminal cleanup credential consumer=%v, want %v: %+v", found, want, consumers)
		}
	}
	assertCleanupConsumer(true)
	if ok, err := s.ClearElasticContinuation(source.ProjectID, job.ID, cursor, "elastic-pg-cleanup", "new", newTerm.Generation); err != nil || !ok {
		t.Fatal("current owner failed cleanup", ok, err)
	}
	assertCleanupConsumer(false)
	if cleanup, _, err = s.ListElasticContinuationCleanupJobs(source.ProjectID, run.ID, "", 100); err != nil || len(cleanup) != 0 {
		t.Fatalf("cleanup not acknowledged %+v %v", cleanup, err)
	}
	// Optional future-source readiness failures are saved in the immutable
	// preparation projection rather than turning into a traffic-start failure.
	source.Enabled = false
	if _, err = s.SaveMonitorSource(*source, source.Revision); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshot, err := preparationMonitorSnapshotTx(tx, source.ProjectID, run.ID)
	if err != nil || len(snapshot.Warnings) != 1 || !strings.HasSuffix(snapshot.Warnings[0], "source_disabled") {
		t.Fatalf("optional warnings %+v %v", snapshot, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	warningRun := &TestRecord{ID: "elastic-warning-run", ProjectID: source.ProjectID, Name: "warnings", Status: "QUEUED", StartTime: now, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID}}}}
	if err = s.SaveTest(warningRun); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.CreatePreparation(Preparation{ID: "elastic-warning-preparation", ProjectID: source.ProjectID, TestID: warningRun.ID, ExecutionRevision: strings.Repeat("d", 64), IdempotencyKey: "elastic-readiness", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	warning := "monitor " + source.ID + ": optional_connection_unavailable"
	if err = s.SetPreparationMonitorWarnings(source.ProjectID, warningRun.ID, []string{warning, warning}); err != nil {
		t.Fatal(err)
	}
	prepared, err = s.TransitionPreparation(prepared.ID, "preparing", "ready", prepared.Revision, "", nil)
	if err != nil || len(prepared.Monitors.Warnings) != 2 {
		t.Fatalf("readiness warning persistence %+v %v", prepared, err)
	}
}

func TestElasticInspectionPostgresDurableCleanupAfterCancellation(t *testing.T) {
	pg, dsn := managementPostgresFixture(t)
	s, _, now := seedMonitorFixture(t, pg.SQLiteStore)
	connection := &ELKConnection{ID: "elastic-inspection", ProjectID: "monitor-p", Name: "Elastic", Enabled: true, Config: elastic.Config{Endpoint: "https://elastic.example", Product: "elasticsearch", AuthType: "api_key"}, APIKeySecretName: "API_KEY"}
	if err := s.CreateELKConnection(connection); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		Connection *ExpandedElasticConnectionSnapshot `json:"connection"`
	}{connection.Snapshot()})
	if err != nil {
		t.Fatal(err)
	}
	op := &ElasticInspection{ID: "inspection", ProjectID: connection.ProjectID, ConnectionID: connection.ID, ConnectionRevision: connection.Revision, Kind: "preview", PayloadJSON: string(payload), CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if err = s.CreateElasticInspection(op); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimElasticInspection(op.ID, "owner", now, now.Add(time.Minute)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := s.CancelElasticInspection(op.ID, op.ProjectID, op.ConnectionID, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	cursor := &analysis.ProviderContinuation{QueryID: "logs", PITID: "private-pg-inspection", WindowFrom: now.Add(-time.Minute), WindowUntil: now, KeepAliveUntil: time.Now().UTC().Add(time.Hour)}
	if ok, err := s.RetainElasticInspectionCleanup(op.ID, "owner", []*analysis.ProviderContinuation{cursor}, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	peer, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	cleanup, err := peer.ListElasticInspectionCleanup(op.ProjectID, "", 100)
	if err != nil || len(cleanup) != 1 || cleanup[0].State != "cancelled" || len(cleanup[0].CleanupContinuations) != 1 || cleanup[0].CleanupContinuations[0].PITID != cursor.PITID {
		t.Fatalf("durable cleanup %+v %v", cleanup, err)
	}
	if ok, err := peer.RetainElasticInspectionCleanup(op.ID, "stale-owner", []*analysis.ProviderContinuation{cursor}, now); err != nil || ok {
		t.Fatal("stale owner retained private cleanup", ok, err)
	}
	if ok, err := peer.DeleteELKConnection(connection.ID, connection.ProjectID); err == nil || ok {
		t.Fatal("PostgreSQL deleted connection with outstanding inspection cleanup", ok, err)
	}
	wrong := *cursor
	wrong.PITID = "other"
	if ok, err := peer.ClearElasticInspectionCleanup(op.ID, []*analysis.ProviderContinuation{&wrong}); err != nil || ok {
		t.Fatal("different handle cleared cleanup", ok, err)
	}
	if ok, err := peer.ClearElasticInspectionCleanup(op.ID, []*analysis.ProviderContinuation{cursor}); err != nil || !ok {
		t.Fatal("matching acknowledgement failed", ok, err)
	}
	if cleanup, err = s.ListElasticInspectionCleanup(op.ProjectID, "", 100); err != nil || len(cleanup) != 0 {
		t.Fatal("cleanup remained after durable acknowledgement", cleanup, err)
	}
	if ok, err := s.DeleteELKConnection(connection.ID, connection.ProjectID); err != nil || !ok {
		t.Fatal("cleaned inspection still retains connection", ok, err)
	}
}
