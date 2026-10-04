//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

func TestPrometheusDurablePostgresSnapshotsSecretsCursorRevisionsAndMigration(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("prometheus_durable_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err = db.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	scopedURL := postgresIntegrationURLWithSearchPath(t, databaseURL, schema)
	store, err := NewPostgresStore(scopedURL, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	s, _, now := seedMonitorFixture(t, store.SQLiteStore)
	_, source, start := seedPrometheusMonitorFixture(t, s, now)
	source.Prometheus.Targets = true
	source.Prometheus.EvidenceSelector = `{instance="same"}`
	savedSource, err := s.SaveMonitorSource(source, source.Revision)
	if err != nil {
		t.Fatal(err)
	}
	source = *savedSource
	connection, err := s.GetPrometheusConnection(source.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	connection.Config.AuthType = "bearer"
	connection.TokenSecretName = "prom.old"
	if ok, err := s.UpdatePrometheusConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err = s.UpdatePrometheusConnectionHealth(connection.ID, source.ProjectID, connection.Revision, "connected", "", prometheus.Capabilities{Metrics: true, Targets: true}, now); err != nil {
		t.Fatal(err)
	}
	run, _ := s.GetTest("monitor-run")
	run.Status = "QUEUED"
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.CreatePreparation(Preparation{ID: "prom-prepared", ProjectID: source.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "prom-prepared", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if frozen := prepared.Monitors.PrometheusConnections[source.ID]; frozen == nil || frozen.TokenSecretName != "prom.old" || frozen.Revision != connection.Revision {
		t.Fatal("preparation omitted private connection snapshot")
	}
	run.Status = "INITIALIZING"
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	prepared, err = s.TransitionPreparation(prepared.ID, "preparing", "ready", prepared.Revision, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumePreparation(prepared.ID, prepared.Revision, "", "tester", "Tester"); err != nil {
		t.Fatal(err)
	}
	run.Status = "RUNNING"
	if err = s.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.StartRunMonitors(source.ProjectID, run.ID, run.ScenarioID, start, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	job := jobs[0]
	connection, _ = s.GetPrometheusConnection(source.ConnectionID)
	oldRevision := connection.Revision
	connection.TokenSecretName = "prom.current"
	if ok, err := s.UpdatePrometheusConnectionIfRevision(connection, oldRevision); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := s.UpdatePrometheusConnectionIfRevision(connection, oldRevision); err != nil || ok {
		t.Fatal("stale connection revision accepted", ok, err)
	}
	loaded, _ := s.GetRunMonitorJob(source.ProjectID, job.ID)
	if loaded.PrometheusConnectionSnapshot.TokenSecretName != "prom.old" || loaded.PrometheusConnectionSnapshot.Revision != oldRevision {
		t.Fatal("consumed run snapshot changed with connection edit")
	}
	consumers, err := s.ListAppDynamicsSecretConsumers(source.ProjectID, "prom.old")
	if err != nil || len(consumers) == 0 {
		t.Fatal("frozen job secret reference not protected", err)
	}
	public, _ := json.Marshal(connection)
	if strings.Contains(string(public), "prom.current") {
		t.Fatal("secret logical reference disclosed")
	}
	if _, err = s.UpdatePrometheusConnectionHealth(connection.ID, source.ProjectID, connection.Revision, "connected", "", prometheus.Capabilities{Metrics: true, Targets: true}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveTest(&TestRecord{ID: "pending-prom-run", Name: "pending", ProjectID: source.ProjectID, ScenarioID: run.ScenarioID, Status: "QUEUED", StartTime: now, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID, Required: true}}}}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.CreatePreparation(Preparation{ID: "pending-prom", ProjectID: source.ProjectID, TestID: "pending-prom-run", ExecutionRevision: strings.Repeat("b", 64), IdempotencyKey: "pending-prom", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	invalidated, err := s.InvalidateAppDynamicsSecretPreparations(source.ProjectID, "prom.current", now.Add(time.Second))
	if err != nil || len(invalidated) != 1 || invalidated[0].ID != pending.ID {
		t.Fatal("secret rotation did not invalidate pending preparation", err)
	}
	if _, err = s.ConsumePreparation(pending.ID, pending.Revision, "", "tester", "Tester"); !errors.Is(err, ErrPreparationConflict) {
		t.Fatal("invalidated preparation consumed", err)
	}
	end := start.Add(time.Minute)
	next := start.Add(30 * time.Second)
	diagnostic := monitorprovider.Diagnostic{ID: "pg-target", Kind: "target", TimestampUTC: start.Add(time.Second), EntityIDs: map[string]string{"instance": "same"}, Status: "up"}
	batch := ProviderMonitorBatch{Points: []analysis.MetricPoint{prometheusTestPoint(source, start, "one")}, Evidence: []monitorprovider.Diagnostic{diagnostic}, QueryFromUTC: start, QueryUntilUTC: end, Progress: &monitorprovider.QueryProgress{TotalQueries: 2, NextTime: &next}, Coverage: map[string]monitorprovider.Coverage{"cpu": {Status: "partial", From: start, To: end, Points: 2, ExpectedPoints: 2}}}
	receipt := providerReceipt(t, job.ID, 0, batch, end)
	for range 2 {
		if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", receipt, batch, "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewPostgresStore(scopedURL, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	s = store.SQLiteStore
	loaded, _ = s.GetRunMonitorJob(source.ProjectID, job.ID)
	if loaded.QueryTime == nil || !loaded.QueryTime.Equal(next) || loaded.NextSequence != 1 || loaded.PrometheusConnectionSnapshot.TokenSecretName != "prom.old" {
		t.Fatal("restart lost immutable snapshot or committed cursor")
	}
	point := prometheusTestPoint(source, start, "one")
	value := float64(9)
	point.Value = &value
	batch.Points = []analysis.MetricPoint{point}
	batch.Progress = &monitorprovider.QueryProgress{TotalQueries: 2, NextQuery: 2, Complete: true}
	batch.Evidence[0].TimestampUTC = start.Add(30 * time.Second)
	batch.Evidence[0].Status = "down"
	batch.QueryWindowEnd = &end
	batch.Coverage = map[string]monitorprovider.Coverage{"cpu": {Status: "complete", From: start, To: end, Points: 3, ExpectedPoints: 3}}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 1, batch, end), batch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	heads, err := s.ListProviderObservationHeads(source.ProjectID, run.ID, source.ID, "point", "", 1000)
	if err != nil || len(heads) != 1 || heads[0].Sequence != 1 {
		t.Fatal("late revision did not replace one canonical head", err)
	}
	loaded, _ = s.GetRunMonitorJob(source.ProjectID, job.ID)
	if loaded.Coverage["cpu"].Points != 5 || !loaded.QueryWatermark.Equal(end) {
		t.Fatal("restart lost continuation totals or watermark")
	}
	first, err := s.ListProviderFirstDiagnosticHeads(source.ProjectID, run.ID, []string{DiagnosticKeyForJob(job.ID, diagnostic)})
	if err != nil || len(first) != 1 || first[0].Sequence != 0 || first[0].Index != 0 {
		t.Fatal("restart lost first-observed diagnostic reference", first, err)
	}
	latest, err := s.ListProviderObservationHeads(source.ProjectID, run.ID, source.ID, "diagnostic", "", 10)
	if err != nil || len(latest) != 1 || latest[0].Sequence != 1 {
		t.Fatal("latest PostgreSQL diagnostic stopped advancing", latest, err)
	}
	// Source chronology must survive an older captured observation delivered
	// after both a newer observation and a controller restart.
	early := start.Add(500 * time.Millisecond)
	lateDiagnostic := diagnostic
	lateDiagnostic.TimestampUTC = early
	lateDiagnostic.Status = "unknown"
	lateBatch := ProviderMonitorBatch{Evidence: []monitorprovider.Diagnostic{lateDiagnostic}, QueryFromUTC: start, QueryUntilUTC: end}
	if err = s.AcceptProviderMonitorBatch(source.ProjectID, "", providerReceipt(t, job.ID, 2, lateBatch, end), lateBatch, "", "", 0); err != nil {
		t.Fatal(err)
	}
	first, err = s.ListProviderFirstDiagnosticHeads(source.ProjectID, run.ID, []string{DiagnosticKeyForJob(job.ID, diagnostic)})
	if err != nil || len(first) != 1 || first[0].Sequence != 2 {
		t.Fatal("late earliest PostgreSQL observation lost chronology", first, err)
	}
	latest, err = s.ListProviderObservationHeads(source.ProjectID, run.ID, source.ID, "diagnostic", "", 10)
	if err != nil || len(latest) != 1 || latest[0].Sequence != 1 {
		t.Fatal("late older PostgreSQL observation replaced latest state", latest, err)
	}
	// Simulate the prior published release's additive administrative schema;
	// canonical receipts and frozen jobs must survive convergence to the current
	// combined release, while published release30 retains its exact identity.
	op := &AppDynamicsInspection{ID: "old-appd-operation", ProjectID: source.ProjectID, ConnectionID: "appd", ConnectionRevision: 1, Kind: "test", PayloadJSON: `{}`, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err = s.CreateAppDynamicsInspection(op); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DROP TABLE prometheus_connections`, `ALTER TABLE appdynamics_inspections DROP COLUMN provider`, `DELETE FROM schema_migrations WHERE version>=30`} {
		if _, err = s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewPostgresStore(scopedURL, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	s = store.SQLiteStore
	old, err := s.GetAppDynamicsInspection(op.ID)
	if err != nil || old.Provider != "appdynamics" {
		t.Fatal("upgrade reassigned old inspection", err)
	}
	status, err := s.SchemaStatus()
	if err != nil || status.AppliedVersion != currentSchemaRelease().Version {
		t.Fatal(status, err)
	}
	assertPublishedPrometheusReleaseIdentity(t, s)
	heads, err = s.ListProviderObservationHeads(source.ProjectID, run.ID, source.ID, "point", "", 1000)
	if err != nil || len(heads) != 1 || heads[0].Sequence != 1 {
		t.Fatal("migration changed canonical evidence", err)
	}
	loaded, err = s.GetRunMonitorJob(source.ProjectID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	chronology := loaded.PrometheusDiagnosticTimes[DiagnosticKeyForJob(job.ID, diagnostic)]
	if !chronology.First.Equal(early) || !chronology.Last.Equal(start.Add(30*time.Second)) {
		t.Fatal("restart or additive migration lost diagnostic chronology", chronology)
	}
}
