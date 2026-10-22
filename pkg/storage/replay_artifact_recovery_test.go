package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func exerciseReplayArtifactScope(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	spec := replayReceiptFixture(t, first, "scope", "owner", "d")
	if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, replayReceiptLimits()); err != nil {
		t.Fatal(err)
	}
	scope, err := second.LookupReplayArtifactScope(ctx, spec.SessionID)
	if err != nil || scope == nil {
		t.Fatal(scope, err)
	}
	if scope.SessionID != spec.SessionID || scope.ProjectID != spec.ProjectID || scope.Generation != spec.Generation || scope.State != "staging" || scope.WriterJoined || scope.Expired {
		t.Fatal(scope)
	}
	if _, err = second.GetReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("scope granted byte authority", err)
	}
	if ok, e := first.JoinReplayArtifactWriter(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); e != nil || !ok {
		t.Fatal(ok, e)
	}
	if ok, e := first.PublishReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID, spec.Generation, "owner"); e != nil || !ok {
		t.Fatal(ok, e)
	}
	scope, err = second.LookupReplayArtifactScope(ctx, spec.SessionID)
	if err != nil || scope.State != "ready" || !scope.WriterJoined || scope.Expired {
		t.Fatal(scope, err)
	}
	if _, err = second.GetReplayArtifactReceipt(ctx, spec.SessionID, "foreign-project"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("scope bypassed project", err)
	}
	if _, err = first.db.Exec(`UPDATE replay_artifact_receipts SET expires_at=? WHERE session_id=?`, time.Now().Add(-time.Hour), spec.SessionID); err != nil {
		t.Fatal(err)
	}
	scope, err = second.LookupReplayArtifactScope(ctx, spec.SessionID)
	if err != nil || !scope.Expired {
		t.Fatal(scope, err)
	}
	if _, err = second.GetReplayArtifactReceipt(ctx, spec.SessionID, spec.ProjectID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired scope granted bytes", err)
	}
	if rows, e := second.ClaimReplayArtifactCleanupAfter(ctx, "", 1); e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	scope, err = second.LookupReplayArtifactScope(ctx, spec.SessionID)
	if err != nil || scope.State != "cleanup" {
		t.Fatal(scope, err)
	}
	for _, id := range []string{"", "missing", " scope", strings.Repeat("x", 129), string([]byte{255})} {
		if scope, e := second.LookupReplayArtifactScope(ctx, id); !errors.Is(e, sql.ErrNoRows) || scope != nil {
			t.Fatal(id, scope, e)
		}
	}
	if _, err = first.db.Exec(`UPDATE replay_artifact_receipts SET spec_json=? WHERE session_id=?`, `{}`, spec.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = second.LookupReplayArtifactScope(ctx, spec.SessionID); err == nil {
		t.Fatal("corrupt scope accepted")
	}
}

func exerciseReplayArtifactCleanupPages(t *testing.T, first, second *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	for i, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		spec := replayReceiptFixture(t, first, id, "owner", fmt.Sprintf("%x", i+1))
		if id == "d" || id == "e" {
			spec.SourceSHA256 = strings.Repeat("e", 64)
		}
		if err := first.BeginReplayArtifactReceipt(ctx, spec, "owner", time.Hour, replayReceiptLimits()); err != nil {
			t.Fatal(err)
		}
		if id != "f" {
			if ok, err := first.JoinReplayArtifactWriter(ctx, id, spec.ProjectID, spec.Generation, "owner"); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
		if id == "g" {
			if ok, err := first.PublishReplayArtifactReceipt(ctx, id, spec.ProjectID, spec.Generation, "owner"); err != nil || !ok {
				t.Fatal(ok, err)
			}
		} else {
			if ok, err := first.ReleaseRetainedReplaySessionLease(ctx, id, "owner", spec.ProjectID); err != nil || !ok {
				t.Fatal(ok, err)
			}
		}
	}
	cursor := ""
	var seen, current []string
	for page := 0; page < 4; page++ {
		rows, err := second.ClaimReplayArtifactCleanupAfter(ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		if len(rows) > 2 {
			t.Fatal("page exceeded bound")
		}
		for _, r := range rows {
			seen = append(seen, r.Spec.SessionID)
			if r.Spec.SourceSHA256 == strings.Repeat("e", 64) {
				current = append(current, r.Spec.SessionID)
			}
		}
		cursor = rows[len(rows)-1].Spec.SessionID
	}
	if !reflect.DeepEqual(seen, []string{"a", "b", "c", "d", "e"}) || !reflect.DeepEqual(current, []string{"d", "e"}) {
		t.Fatal("foreign source starved later work", seen, current)
	}
	rows, err := first.ClaimReplayArtifactCleanupAfter(ctx, "", 2)
	if err != nil || len(rows) != 2 || rows[0].Spec.SessionID != "a" {
		t.Fatal("restart lost pending cleanup", rows, err)
	}
	for _, id := range []string{"f", "g"} {
		scope, e := first.LookupReplayArtifactScope(ctx, id)
		if e != nil || scope.State == "cleanup" {
			t.Fatal("unknown writer or ready artifact claimed", scope, e)
		}
	}
	for _, limit := range []int{0, 257} {
		if _, err = first.ClaimReplayArtifactCleanupAfter(ctx, "", limit); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	for _, after := range []string{" a", strings.Repeat("x", 129), string([]byte{255})} {
		if _, err = first.ClaimReplayArtifactCleanupAfter(ctx, after, 2); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
}
func TestReplayArtifactScope(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	exerciseReplayArtifactScope(t, first, second)
}
func TestReplayArtifactCleanupPages(t *testing.T) {
	first, second := openSharedReplaySessionLeaseStores(t)
	exerciseReplayArtifactCleanupPages(t, first, second)
}
