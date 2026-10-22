package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func replayReceiptFixture(t *testing.T, s *SQLiteStore, session, owner, generation string) ReplayArtifactSpec {
	t.Helper()
	ctx := context.Background()
	got, err := s.TryAcquireReplaySessionLease(ctx, session, owner, DefaultProjectID, time.Hour, 10, 10)
	if err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatal(got, err)
	}
	if ok, err := s.RetainReplaySessionLease(ctx, session, owner, DefaultProjectID, time.Hour); err != nil || !ok {
		t.Fatal(ok, err)
	}
	return ReplayArtifactSpec{Version: 1, SessionID: session, ProjectID: DefaultProjectID, Generation: strings.Repeat(generation, 64), SourceSHA256: strings.Repeat("a", 64), HARSHA256: strings.Repeat("b", 64), HARBytes: 100, ReportSHA256: strings.Repeat("c", 64), ReportBytes: 50}
}
func replayReceiptLimits() ReplayArtifactLimits {
	return ReplayArtifactLimits{GlobalReceipts: 10, ProjectReceipts: 10, GlobalBytes: 10000, ProjectBytes: 10000}
}

func exerciseReplayArtifactReceipts(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	spec := replayReceiptFixture(t, first, "receipt", "owner", "d")
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "foreign", time.Hour, replayReceiptLimits()); !errors.Is(err, ErrReplayArtifactFence) {
		t.Fatal(err)
	}
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, replayReceiptLimits()); err != nil {
		t.Fatal(err)
	}
	if err := second.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, replayReceiptLimits()); !errors.Is(err, ErrReplayArtifactFence) {
		t.Fatal("unknown writer restarted", err)
	}
	if ok, err := first.PublishReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil || ok {
		t.Fatal("unjoined publication", ok, err)
	}
	if ok, err := first.JoinReplayArtifactWriter(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "foreign"); err != nil || ok {
		t.Fatal("foreign join", ok, err)
	}
	if ok, err := first.JoinReplayArtifactWriter(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := first.PublishReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := second.GetReplayArtifactReceipt(ctx, spec.SessionID, "foreign"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	got, err := second.GetReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID)
	if err != nil || got.Spec != spec || !got.WriterJoined || got.State != "ready" {
		t.Fatal(got, err)
	}
	if ok, err := first.ReleaseRetainedReplaySessionLease(ctx, spec.SessionID, "owner", spec.ProjectID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if result, err := second.TryAcquireReplaySessionLease(ctx, spec.SessionID, "successor", spec.ProjectID, time.Hour, 10, 10); err != nil || result != ReplaySessionLeaseConflict {
		t.Fatal("process prune released durable namespace", result, err)
	}
	if got, err := second.ClaimReplayArtifactCleanup(ctx, 10); err != nil || len(got) != 0 {
		t.Fatal("readable receipt reclaimed", got, err)
	}
	if _, err := first.db.Exec(`UPDATE replay_artifact_receipts SET expires_at=? WHERE session_id=?`, time.Now().Add(-time.Hour), spec.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := second.GetReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired receipt served", err)
	}
	rows, err := second.ClaimReplayArtifactCleanup(ctx, 10)
	if err != nil || len(rows) != 1 || rows[0].State != "cleanup" {
		t.Fatal(rows, err)
	}
	if ok, err := first.FinishReplayArtifactCleanup(ctx, spec.SessionID, spec.ProjectID, strings.Repeat("e", 64)); err != nil || ok {
		t.Fatal("foreign cleanup", ok, err)
	}
	if ok, err := first.FinishReplayArtifactCleanup(ctx, spec.SessionID, spec.ProjectID, spec.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if result, err := second.TryAcquireReplaySessionLease(ctx, spec.SessionID, "successor", spec.ProjectID, time.Hour, 10, 10); err != nil || result != ReplaySessionLeaseAcquired {
		t.Fatal(result, err)
	}
	if ok, err := first.FinishReplayArtifactCleanup(ctx, spec.SessionID, spec.ProjectID, spec.Generation); err != nil || ok {
		t.Fatal("late cleanup", ok, err)
	}
}
func TestReplayArtifactReceiptsSurviveNewStoreAndPruning(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	exerciseReplayArtifactReceipts(t, first, second)
}

func TestReplayArtifactReceiptUnknownWriterRetainsCapacity(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	ctx := context.Background()
	spec := replayReceiptFixture(t, first, "unknown", "owner", "d")
	limits := replayReceiptLimits()
	limits.GlobalReceipts = 1
	limits.ProjectReceipts = 1
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, limits); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(`UPDATE replay_artifact_receipts SET expires_at=?`, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ReleaseRetainedReplaySessionLease(ctx, spec.SessionID, "owner", spec.ProjectID); err != nil {
		t.Fatal(err)
	}
	if rows, err := second.ClaimReplayArtifactCleanup(ctx, 10); err != nil || len(rows) != 0 {
		t.Fatal("expiry invented writer proof", rows, err)
	}
	other := replayReceiptFixture(t, second, "other", "other-owner", "e")
	if err := second.BeginReplayArtifactReceipt(ctx, other, "other-owner", time.Hour, limits); !errors.Is(err, ErrReplayArtifactCapacity) {
		t.Fatal("unknown bytes released", err)
	}
	if ok, err := first.JoinReplayArtifactWriter(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := first.PublishReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil || ok {
		t.Fatal("late publication", ok, err)
	}
	rows, err := second.ClaimReplayArtifactCleanup(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if ok, err := second.FinishReplayArtifactCleanup(ctx, spec.SessionID, spec.ProjectID, spec.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := second.BeginReplayArtifactReceipt(ctx, other, "other-owner", time.Hour, limits); err != nil {
		t.Fatal(err)
	}
}

func TestReplayArtifactReceiptValidationAndByteAdmission(t *testing.T) {
	first, _ := openSharedReplaySessionLeaseStores(t)
	ctx := context.Background()
	spec := replayReceiptFixture(t, first, "bounded", "owner", "d")
	for name, change := range map[string]func(*ReplayArtifactSpec){
		"future version":     func(s *ReplayArtifactSpec) { s.Version = 2 },
		"invalid text":       func(s *ReplayArtifactSpec) { s.SessionID = string([]byte{255}) },
		"path generation":    func(s *ReplayArtifactSpec) { s.Generation = "../x" },
		"bad digest":         func(s *ReplayArtifactSpec) { s.HARSHA256 = strings.Repeat("z", 64) },
		"zero bytes":         func(s *ReplayArtifactSpec) { s.ReportBytes = 0 },
		"oversized report":   func(s *ReplayArtifactSpec) { s.ReportBytes = 16<<20 + 1 },
		"oversized har":      func(s *ReplayArtifactSpec) { s.HARBytes = 256<<20 + 1 },
		"oversized identity": func(s *ReplayArtifactSpec) { s.SessionID = strings.Repeat("x", 129) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := spec
			change(&bad)
			if err := first.BeginReplayArtifactReceipt(ctx, bad, "owner", time.Hour, replayReceiptLimits()); err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
	limits := replayReceiptLimits()
	limits.ProjectBytes = 149
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, limits); !errors.Is(err, ErrReplayArtifactCapacity) {
		t.Fatal(err)
	}
	limits.ProjectBytes = 150
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, limits); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ClaimReplayArtifactCleanup(ctx, 257); err == nil {
		t.Fatal("unbounded cleanup")
	}
}

func exerciseReplayArtifactProjectDeletion(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	project := &Project{ID: "receipt-project", Name: "Receipt project"}
	if err := first.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	spec := ReplayArtifactSpec{Version: 1, SessionID: "deleted-project", ProjectID: project.ID, Generation: strings.Repeat("d", 64), SourceSHA256: strings.Repeat("a", 64), HARSHA256: strings.Repeat("b", 64), HARBytes: 100, ReportSHA256: strings.Repeat("c", 64), ReportBytes: 50}
	if got, err := first.TryAcquireReplaySessionLease(ctx, spec.SessionID, "owner", project.ID, time.Hour, 10, 10); err != nil || got != ReplaySessionLeaseAcquired {
		t.Fatal(got, err)
	}
	if ok, err := first.RetainReplaySessionLease(ctx, spec.SessionID, "owner", project.ID, time.Hour); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, replayReceiptLimits()); err != nil {
		t.Fatal(err)
	}
	if err := first.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	if scope, err := second.LookupReplayArtifactScope(ctx, spec.SessionID); !errors.Is(err, sql.ErrNoRows) || scope != nil {
		t.Fatal("deleted project retained authorization scope", scope, err)
	}
	if rows, err := second.ClaimReplayArtifactCleanup(ctx, 10); err != nil || len(rows) != 0 {
		t.Fatal("deleted unknown writer became cleanable", rows, err)
	}
	if err := first.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	if ok, err := first.JoinReplayArtifactWriter(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := first.PublishReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil || ok {
		t.Fatal("deleted project resurrected publication", ok, err)
	}
	if _, err := second.GetReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	rows, err := second.ClaimReplayArtifactCleanup(ctx, 10)
	if err != nil || len(rows) != 1 || rows[0].Spec != spec {
		t.Fatal("lost exact cleanup identity", rows, err)
	}
	if ok, err := second.FinishReplayArtifactCleanup(ctx, spec.SessionID, spec.ProjectID, spec.Generation); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, replayReceiptLimits()); !errors.Is(err, ErrReplayArtifactFence) {
		t.Fatal("old lease reused after project recreation", err)
	}
}

func TestReplayArtifactReceiptRejectsCorruptIdentityBeforeCleanup(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	ctx := context.Background()
	spec := replayReceiptFixture(t, first, "corrupt", "owner", "d")
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, replayReceiptLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := first.JoinReplayArtifactWriter(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(`UPDATE replay_artifact_receipts SET generation=?,expires_at=?`, strings.Repeat("e", 64), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ClaimReplayArtifactCleanup(ctx, 10); err == nil {
		t.Fatal("corrupt object identity authorized cleanup")
	}
}

func exerciseReplayArtifactConcurrentAdmission(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	one := replayReceiptFixture(t, first, "quota-one", "owner-one", "e")
	two := replayReceiptFixture(t, second, "quota-two", "owner-two", "f")
	limits := replayReceiptLimits()
	limits.GlobalReceipts = 1
	limits.ProjectReceipts = 1
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() { <-start; done <- first.BeginReplayArtifactReceipt(ctx, one, "owner-one", time.Hour, limits) }()
	go func() { <-start; done <- second.BeginReplayArtifactReceipt(ctx, two, "owner-two", time.Hour, limits) }()
	close(start)
	success, capacity := 0, 0
	for range 2 {
		err := <-done
		if err == nil {
			success++
		} else if errors.Is(err, ErrReplayArtifactCapacity) {
			capacity++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || capacity != 1 {
		t.Fatal("quota was not serialized", success, capacity)
	}
}
func TestReplayArtifactReceiptConcurrentAdmission(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	exerciseReplayArtifactConcurrentAdmission(t, first, second)
}

func TestReplayArtifactReceiptUpgradeAndRestart(t *testing.T) {
	path := t.TempDir() + "/upgrade.db"
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DROP TABLE replay_artifact_receipts`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DELETE FROM schema_migrations WHERE version=74`); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	spec := replayReceiptFixture(t, store, "restart", "owner", "d")
	if err = store.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, replayReceiptLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.JoinReplayArtifactWriter(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil {
		t.Fatal(err)
	}
	if ok, e := store.PublishReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.GetReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID)
	if err != nil || got.Spec != spec {
		t.Fatal(got, err)
	}
	status, err := store.SchemaStatus()
	if err != nil || status.AppliedVersion != currentSchemaRelease().Version {
		t.Fatal(status, err)
	}
}

func TestReplayArtifactReceiptExpiredLeaseCannotPublishFreshReceipt(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	ctx := context.Background()
	spec := replayReceiptFixture(t, first, "expired-lease", "owner", "d")
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, replayReceiptLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := first.JoinReplayArtifactWriter(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(`UPDATE replay_session_leases SET expires_at=? WHERE session_id=?`, time.Now().Add(-time.Hour), spec.SessionID); err != nil {
		t.Fatal(err)
	}
	if ok, err := first.PublishReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); err != nil || ok {
		t.Fatal("expired owner published a fresh receipt", ok, err)
	}
	rows, err := second.ClaimReplayArtifactCleanup(ctx, 10)
	if err != nil || len(rows) != 1 || rows[0].State != "cleanup" {
		t.Fatal(rows, err)
	}
}

func TestReplayArtifactReceiptProjectDeletionPreservesCleanupAndRevokesOwner(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	exerciseReplayArtifactProjectDeletion(t, first, second)
}
