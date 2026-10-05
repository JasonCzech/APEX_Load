//go:build postgres_integration

package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Use real pgx timestamp serialization and a separate store handle. SQLite
// cannot reveal the database/artifact receive-clock precision mismatch.
func TestPostgresManagementReceiptPrecisionAfterReload(t *testing.T) {
	server, store, dsn, jobs, key := postgresManagementAPIFixture(t)
	batch, grant := admittedManagementBatch(t, server, jobs[0], key, true)
	sourceTime := postgresManagementSourceTime(grant)
	setPostgresManagementSourceTime(t, &batch, jobs[0], sourceTime)
	poll, err := store.GetManagementPoll("proj-a", grant.PollID)
	if err != nil {
		t.Fatal(err)
	}

	// The literal fraction always contains submicrosecond precision, regardless
	// of the ambient clock. Keep it inside this admitted poll's lifetime.
	input := grant.IssuedAt.UTC().Truncate(time.Second).Add(time.Second + 123456789*time.Nanosecond).In(time.FixedZone("receipt-input", -4*3600))
	received := managementReceiptTime(input)
	want := input.UTC().Truncate(time.Microsecond)
	if received.Location() != time.UTC || !received.Equal(want) || received.Nanosecond() != 123456000 || input.Equal(received) {
		t.Fatalf("noncanonical receive clock: input=%s received=%s want=%s", input, received, want)
	}
	core := analysis.MonitorEvidenceEnvelope{Version: analysis.MonitorEvidenceVersion, Points: []analysis.MetricPoint{}, MessagingManagement: batch.MessagingManagement}
	coreBytes, err := analysis.MonitorEvidenceCoreBytes(core)
	if err != nil {
		t.Fatal(err)
	}
	coreHash := sha256.Sum256(coreBytes)
	receipts := make([]storage.MonitorBatchReceipt, 0, len(poll.Cohort))
	for _, member := range poll.Cohort {
		job := member.Job
		evidence := core.Clone()
		evidence.Attribution = &analysis.MonitorEvidenceAttribution{ProjectID: job.ProjectID, RunID: job.RunID, ScenarioID: job.ScenarioID, JobID: job.ID, SourceID: job.Source.ID, SourceRevision: job.Source.Revision, CollectorID: grant.CollectorID, ReceivedAt: received}
		body, err := analysis.EncodeMonitorEvidence(evidence)
		if err != nil {
			t.Fatal(err)
		}
		artifact, digest := monitorArtifactKey(job.ProjectID, job.RunID, body)
		if err := store.QueueMonitorObjectCleanup(job.ProjectID, job.RunID, artifact); err != nil {
			t.Fatal(err)
		}
		if err := writeMonitorArtifact(context.Background(), artifact, body); err != nil {
			t.Fatal(err)
		}
		version, snapshots, rows, generation := int64(analysis.MonitorEvidenceVersion), int64(1), int64(1), poll.Generation
		pollID := poll.ID
		receipts = append(receipts, storage.MonitorBatchReceipt{JobID: job.ID, Sequence: member.Sequence, Digest: digest, ArtifactKey: artifact, ReceivedAt: received, EvidenceVersion: &version, ManagementSnapshotCount: &snapshots, ManagementRowCount: &rows, CollectionGeneration: &generation, ManagementPollID: &pollID})
	}
	if err := store.AcceptManagementPoll("proj-a", grant.CollectorID, grant.CollectorInstanceID, poll.ID, hex.EncodeToString(coreHash[:]), receipts, input); err != nil {
		t.Fatal(err)
	}

	reader := postgresManagementReader(t, dsn)
	for _, job := range jobs {
		receipt := postgresManagementReceipt(t, reader, job)
		if !receipt.ReceivedAt.Equal(want) {
			t.Fatalf("PostgreSQL changed canonical receive time: got=%s want=%s", receipt.ReceivedAt, want)
		}
		evidence, err := readMonitorEvidence(context.Background(), "proj-a", job.RunID, receipt)
		if err != nil {
			t.Fatalf("canonical PostgreSQL receipt rejected after reload: %v", err)
		}
		assertPostgresManagementPrecision(t, evidence, receipt, sourceTime)

		// Keep exact attribution validation: normalize at publication, rather
		// than accepting a changed receipt merely because it is within 1 us.
		body, err := readMonitorArtifact(context.Background(), receipt.ArtifactKey, receipt.Digest)
		if err != nil {
			t.Fatal(err)
		}
		altered := receipt
		altered.ReceivedAt = receipt.ReceivedAt.Add(time.Nanosecond)
		if _, err := decodeMonitorEvidenceBody(body, "proj-a", job.RunID, altered); err == nil {
			t.Fatal("receipt timestamp mismatch was accepted")
		}
	}
}

func TestPostgresManagementSignedPublicationAfterReload(t *testing.T) {
	server, _, dsn, jobs, key := postgresManagementAPIFixture(t)
	batch, grant := admittedManagementBatch(t, server, jobs[0], key, true)
	sourceTime := postgresManagementSourceTime(grant)
	setPostgresManagementSourceTime(t, &batch, jobs[0], sourceTime)
	path := "/api/v1/monitoring/collectors/management-collector/jobs/" + jobs[0].ID + "/samples"
	if response := collectorRequest(t, server, http.MethodPost, path, "management-token", batch); response.Code != http.StatusOK {
		t.Fatalf("signed publication: HTTP %d: %s", response.Code, response.Body.String())
	}

	reader := postgresManagementReader(t, dsn)
	// An unopened HA coordinator suppresses unrelated startup reconciliation;
	// the viewer reads the database receipts and immutable artifacts directly.
	viewer := NewServer(ServerConfig{Store: reader, MaxConcurrent: 2, HAExecutionLeadership: true})
	enableTrustedHeaderAuth(t, viewer)
	for _, job := range jobs {
		receipt := postgresManagementReceipt(t, reader, job)
		evidence, err := readMonitorEvidence(context.Background(), "proj-a", job.RunID, receipt)
		if err != nil {
			t.Fatalf("signed PostgreSQL evidence rejected after reload: %v", err)
		}
		assertPostgresManagementPrecision(t, evidence, receipt, sourceTime)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+job.RunID+"/monitor-management?limit=1", nil)
		request.Header.Set("X-Auth-Request-User", "management-viewer")
		request.Header.Set("X-APEXLoad-Project-Roles", "proj-a="+RoleViewer)
		response := httptest.NewRecorder()
		viewer.mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("fresh PostgreSQL viewer: HTTP %d: %s", response.Code, response.Body.String())
		}
		var result MonitorManagementResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Records) != 1 || result.HasMore {
			t.Fatalf("viewer response: %s, error=%v", response.Body.String(), err)
		}
		if result.Records[0].JobID != job.ID || !result.Records[0].ReceivedAt.Equal(receipt.ReceivedAt) {
			t.Fatalf("viewer changed receipt identity: %+v", result.Records[0])
		}
		assertPostgresManagementPrecision(t, result.Records[0].Evidence, receipt, sourceTime)
	}

	// Historical evidence remains readable after collection is switched off.
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
	t.Setenv("APEXLOAD_MESSAGING_MANAGEMENT_PROJECTS", "")
	archiveRoot := filepath.Join(resolveArchiveRoot(), "completed")
	if err := os.MkdirAll(archiveRoot, 0700); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: jobs[0].RunID, ArchivePath: archiveRoot, ManifestPath: filepath.Join(archiveRoot, runArchiveManifestFileName)}
	manifest := &RunArchiveManifest{TestID: jobs[0].RunID, SchemaVersion: archiveSchemaVersion}
	budget, err := newArchiveAdjunctBudgetForRecord(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	if err := viewer.runner.persistRunMonitorArchive("proj-a", jobs[0].RunID, record, manifest, budget); err != nil {
		t.Fatalf("archive from PostgreSQL receipts: %v", err)
	}
	if manifest.MonitorManagementSnapshotCount != 1 || manifest.MonitorManagementRowCount != 1 || manifest.MonitorEvidenceCount != 0 || manifest.MonitorManagementVersion != 1 {
		t.Fatalf("archive manifest lost evidence: %+v", manifest)
	}
	body, err := os.ReadFile(filepath.Join(archiveRoot, manifest.MonitorManagementFile))
	if err != nil {
		t.Fatal(err)
	}
	var archived MonitorManagementRecord
	if err := json.Unmarshal(body, &archived); err != nil {
		t.Fatal(err)
	}
	receipt := postgresManagementReceipt(t, reader, jobs[0])
	if archived.JobID != jobs[0].ID || !archived.ReceivedAt.Equal(receipt.ReceivedAt) {
		t.Fatalf("archive changed receipt identity: %+v", archived)
	}
	assertPostgresManagementPrecision(t, archived.Evidence, receipt, sourceTime)
	coverageCount := len(manifest.MonitorCoverage)
	if err := viewer.runner.persistRunMonitorArchive("proj-a", jobs[0].RunID, record, manifest, budget); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := os.ReadFile(filepath.Join(archiveRoot, manifest.MonitorManagementFile))
	if err != nil || string(rebuilt) != string(body) || len(manifest.MonitorCoverage) != coverageCount {
		t.Fatalf("archive rebuild changed immutable evidence: error=%v manifest=%+v", err, manifest)
	}
}

func postgresManagementAPIFixture(t *testing.T) (*Server, *storage.PostgresStore, string, []storage.RunMonitorJob, ed25519.PrivateKey) {
	t.Helper()
	databaseURL := os.Getenv("APEXLOAD_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("management_api_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("invalid PostgreSQL integration URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	dsn := parsed.String()
	store := postgresManagementReader(t, dsn)
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_MESSAGING_MANAGEMENT_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_MONITOR_SIGNING_SEED", base64.StdEncoding.EncodeToString(key.Seed()))
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "Project A", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2})
	for _, err := range []error{
		store.SaveScript(&dsl.Script{ID: "management-script", ProjectID: "proj-a", Name: "script", Protocol: "http", CreatedAt: now}),
		store.SaveScenario(&storage.Scenario{ID: "management-scenario", Name: "scenario", Duration: "1m", CreatedAt: now}, []storage.ScenarioScript{{ScriptID: "management-script"}}),
		store.SaveMonitoringCollector(&storage.MonitoringCollector{ID: "management-collector", ProjectID: "proj-a", Name: "collector", TokenHash: hashAPIKey("management-token"), Health: "healthy", LastSeenAt: &now, AllowedMetricPacks: []string{"kafka-offsets"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	source := storage.MonitorSource{ID: "management-source", ProjectID: "proj-a", Name: "Kafka offsets", CollectorID: "management-collector", Pack: "kafka-offsets", Destination: "management://broker-readonly", IntervalSeconds: 10, Enabled: true, ManagementScope: &analysis.ManagementScope{Version: 1, Kind: analysis.ManagementKindKafka, Provider: "broker-cluster", Isolation: "read_uncommitted", Entities: []analysis.ManagementEntity{{Kafka: &analysis.KafkaManagementEntity{Topic: "orders", Group: "workers", Partition: 0}}}}, ManagementIdentity: &analysis.ManagementIdentity{Mode: "anonymous", Principal: "approved-local-broker", PolicyRevision: "policy-v1"}}
	saved, err := store.SaveMonitorSource(source, 0)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := saved.ManagementScope.Hash()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := saved.ManagementIdentity.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCollectorManagementReadiness("proj-a", "management-collector", storage.CollectorManagementReadiness{InstanceID: "instance-1", Capabilities: []string{collectoragent.FeatureMessagingManagementEvidenceV1}, Scopes: []storage.ManagementReadinessScope{{ScopeHash: scope, IdentityHash: identity}}, Packs: []string{"kafka-offsets"}, Destinations: []string{saved.Destination}, SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveMonitorBindings(storage.MonitorBindings{ProjectID: "proj-a", ScenarioID: "management-scenario", Sources: []storage.MonitorBinding{{SourceID: saved.ID, Required: true}}}, 0); err != nil {
		t.Fatal(err)
	}
	jobs := []storage.RunMonitorJob{}
	for _, run := range []string{"management-run-a", "management-run-b"} {
		if err := store.SaveTest(&storage.TestRecord{ID: run, Name: run, ProjectID: "proj-a", ScenarioID: "management-scenario", Status: "RUNNING", StartedAt: &now, StartTime: now}); err != nil {
			t.Fatal(err)
		}
		bound, err := store.StartRunMonitors("proj-a", run, "management-scenario", now, now.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, bound...)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected two bound monitor jobs, got %d", len(jobs))
	}
	return server, store, dsn, jobs, key
}

func postgresManagementReader(t *testing.T, dsn string) *storage.PostgresStore {
	t.Helper()
	store, err := storage.NewPostgresStore(dsn, storage.StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func postgresManagementSourceTime(grant collectoragent.ManagementPollGrant) time.Time {
	return grant.IssuedAt.UTC().Truncate(time.Microsecond).Add(time.Microsecond + 789*time.Nanosecond)
}

func setPostgresManagementSourceTime(t *testing.T, batch *collectoragent.RunSampleBatch, job storage.RunMonitorJob, sourceTime time.Time) {
	t.Helper()
	snapshot := batch.MessagingManagement
	snapshot.StartedAt, snapshot.CompletedAt, snapshot.CapturedAt = sourceTime, sourceTime, sourceTime
	for i := range snapshot.Rows {
		observed := sourceTime
		snapshot.Rows[i].ObservedAt = &observed
	}
	if sourceTime.Nanosecond()%1000 != 789 {
		t.Fatal("source fixture lacks deterministic nanosecond precision")
	}
	if err := snapshot.ValidateFor(*job.Source.ManagementScope, *job.Source.ManagementIdentity); err != nil {
		t.Fatal(err)
	}
}

func postgresManagementReceipt(t *testing.T, store *storage.PostgresStore, job storage.RunMonitorJob) storage.MonitorBatchReceipt {
	t.Helper()
	receipts, err := store.ListMonitorBatchReceipts("proj-a", job.RunID, "", -1, 10)
	if err != nil || len(receipts) != 1 || receipts[0].JobID != job.ID || receipts[0].SampleCount != 0 {
		t.Fatalf("reloaded receipt: %+v error=%v", receipts, err)
	}
	return receipts[0]
}

func assertPostgresManagementPrecision(t *testing.T, evidence analysis.MonitorEvidenceEnvelope, receipt storage.MonitorBatchReceipt, sourceTime time.Time) {
	t.Helper()
	if evidence.Attribution == nil || !evidence.Attribution.ReceivedAt.Equal(receipt.ReceivedAt) || receipt.ReceivedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("database/artifact receipt precision differs: receipt=%+v attribution=%+v", receipt, evidence.Attribution)
	}
	snapshot := evidence.MessagingManagement
	if snapshot == nil || !snapshot.StartedAt.Equal(sourceTime) || !snapshot.CompletedAt.Equal(sourceTime) || !snapshot.CapturedAt.Equal(sourceTime) || len(snapshot.Rows) != 1 || snapshot.Rows[0].ObservedAt == nil || !snapshot.Rows[0].ObservedAt.Equal(sourceTime) {
		t.Fatalf("source timestamp precision was changed: %+v", snapshot)
	}
	offsets := snapshot.Rows[0].KafkaOffsets
	if offsets == nil || offsets.CommittedNext.State != analysis.ManagementStateObserved || offsets.CommittedNext.Value == nil || *offsets.CommittedNext.Value != "0" || offsets.HighWatermarkNext.Value == nil || *offsets.HighWatermarkNext.Value != "9007199254740993" {
		t.Fatalf("exact observed offsets were lost: %+v", offsets)
	}
}
