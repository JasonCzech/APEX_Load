//go:build postgres_integration

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestPostgresPartitionManifestReopenReconciliationAndRefusals(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal("cannot open owned PostgreSQL fixture")
	}
	defer admin.Close()
	schema := fmt.Sprintf("apexload_native_partitions_%d", time.Now().UnixNano())
	quoted := quotePostgresIdentifier(schema)
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal("cannot create private partition fixture schema")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("cannot remove exact owned partition fixture schema")
			return
		}
		var remaining int
		if err := admin.QueryRowContext(cleanup, `SELECT count(*) FROM pg_namespace WHERE nspname=$1`, schema).Scan(&remaining); err != nil || remaining != 0 {
			t.Error("owned partition fixture schema cleanup was not verified")
		}
	}()
	dsn := postgresIntegrationURLWithSearchPath(t, databaseURL, schema)
	first, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal("cannot initialize partition fixture store")
	}
	defer func() {
		if first != nil {
			first.Close()
		}
	}()
	seedMonitorFixture(t, first.SQLiteStore)
	mapped := nativeAttemptMappedFixture(t, first.SQLiteStore)
	in, a, err := first.ClaimAnalysisImportAttempt(ctx, mapped.ID, mapped.ProjectID, mapped.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if in.PartitionManifestVersion != 1 || a.PartitionManifestVersion != 1 || in.PartitionManifest != nil {
		t.Fatal("PostgreSQL claim did not fence fresh producer proof")
	}
	publication := nativeAttemptPublicationFixture(t, in, a)
	if err = first.ConfirmAnalysisAttemptWritersQuiesced(ctx, in.ID, in.ProjectID, a.ID); err != nil {
		t.Fatal(err)
	}
	// Discard the returned payload, then reopen independently. Only the actual
	// durable PostgreSQL result receipt can resolve this caller's missing result.
	if _, err = first.PublishAnalysisImportAttempt(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	first = nil
	reopened, err := NewPostgresStore(dsn, StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal("cannot reopen partition fixture store")
	}
	defer reopened.Close()
	loaded, err := reopened.GetAnalysisImport(in.ID)
	if err != nil || loaded.PartitionManifestVersion != 1 || !reflect.DeepEqual(loaded.PartitionManifest, publication.Import.PartitionManifest) {
		t.Fatal("PostgreSQL reopen changed producer proof", err)
	}
	resolution, err := reopened.ResolveAnalysisAttemptPublication(ctx, in.ID, in.ProjectID, a.ID, publication.Digest)
	if err != nil || !resolution.Published || resolution.Import == nil || !reflect.DeepEqual(resolution.Import.PartitionManifest, loaded.PartitionManifest) || resolution.PublicationDigest != publication.Digest {
		t.Fatal("durable exact result did not reconcile after reopen", err)
	}
	if _, err = reopened.ResolveAnalysisAttemptPublication(ctx, in.ID, in.ProjectID, a.ID, strings.Repeat("f", 64)); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("different result digest reconciled", err)
	}
	now := time.Now().UTC()
	if err = reopened.SaveProject(&Project{ID: "foreign-partition-project", Name: "Foreign", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = reopened.ClaimAnalysisImportAttempt(ctx, in.ID, "foreign-partition-project", loaded.Revision, time.Minute); err == nil {
		t.Fatal("foreign project claimed partition dataset")
	}
	if _, err = reopened.ResolveAnalysisAttemptPublication(ctx, in.ID, "foreign-partition-project", a.ID, publication.Digest); err == nil {
		t.Fatal("foreign project resolved another dataset receipt")
	}
	foreign := publication
	foreign.Import.ProjectID = "foreign-partition-project"
	if _, err = reopened.PublishAnalysisImportAttempt(ctx, foreign); err == nil {
		t.Fatal("foreign project reused exact publication")
	}
	changed := *loaded
	changed.PartitionManifest = loaded.PartitionManifest.Clone()
	changed.PartitionManifest = nil
	if _, err = reopened.SaveAnalysisImport(changed, changed.Revision); err == nil {
		t.Fatal("generic PostgreSQL save erased immutable proof")
	}
	validBody, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	badBody := strings.Replace(string(validBody), `"rows":3`, `"rows":null`, 1)
	if badBody == string(validBody) {
		t.Fatal("canonical proof fixture missing explicit row population")
	}
	if _, err = reopened.db.ExecContext(ctx, `UPDATE analysis_imports SET payload=? WHERE id=?`, badBody, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.GetAnalysisImport(in.ID); err == nil {
		t.Fatal("corrupt PostgreSQL proof became a zero/default row population")
	}
	if _, err = reopened.db.ExecContext(ctx, `UPDATE analysis_imports SET payload=? WHERE id=?`, string(validBody), in.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := reopened.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	receipt, err := analysisAttemptJournalLoad(ctx, tx, a.ID, in.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	parts := receipt.Published.PartitionManifest.Clone().Partitions
	later := parts[0].Stats.MaxTime.Add(time.Second)
	parts[0].Stats.MaxTime = &later
	receipt.Published.PartitionManifest, err = analysis.NewNormalizedPartitionManifest(in.ProjectID, in.ID, *in.Mapping, parts)
	if err != nil {
		t.Fatal(err)
	}
	receiptBody, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE analysis_attempt_journal SET payload=? WHERE id=?`, string(receiptBody), a.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.ResolveAnalysisAttemptPublication(ctx, in.ID, in.ProjectID, a.ID, publication.Digest); !errors.Is(err, ErrAnalysisPublicationUnknown) {
		t.Fatal("validly redigested changed proof trusted stale receipt digest", err)
	}
	if _, err = reopened.PublishAnalysisImportAttempt(ctx, publication); !errors.Is(err, ErrAnalysisAuthority) {
		t.Fatal("corrupt exact PostgreSQL receipt replay accepted", err)
	}
}
