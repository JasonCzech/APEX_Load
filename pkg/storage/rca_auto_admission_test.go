package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRCAAutoAdmissionRecoveryAndConditionalAcknowledgement(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "admission.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	telemetryOutboxFixture(t, first, "retry-run")
	initial, err := first.EnsureRCAAutoAdmission(ctx, "retry-run")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	list, _, err := second.ListRCAAutoAdmissions(ctx, "", 4)
	if err != nil || len(list) != 1 || list[0].Raw != initial.Raw {
		t.Fatalf("recovery: %+v %v", list, err)
	}
	next, err := second.EnsureRCAAutoAdmission(ctx, "retry-run")
	if err != nil {
		t.Fatal(err)
	}
	if next.Raw == initial.Raw {
		t.Fatal("new attachment must replace acknowledgement token")
	}
	if err := second.FinishRCAAutoAdmission(ctx, *initial); err != nil {
		t.Fatal(err)
	}
	if current, err := second.RCAAutoAdmissionCurrent(ctx, *next); err != nil || !current {
		t.Fatal("stale acknowledgement removed refresh", current, err)
	}
	// Initial provenance is immutable through SaveTest. Inject an actual changed
	// incarnation while preserving the old intent to exercise its identity fence.
	if _, err := second.db.Exec(`UPDATE tests SET initial_provenance_json=? WHERE id=?`, `{"run_incarnation":"replacement"}`, "retry-run"); err != nil {
		t.Fatal(err)
	}
	var replaced string
	if err := second.db.QueryRow(`SELECT initial_provenance_json FROM tests WHERE id=?`, "retry-run").Scan(&replaced); err != nil {
		t.Fatal(err)
	}
	if incarnation, err := testRunIncarnationJSON(replaced); err != nil || incarnation != "replacement" {
		t.Fatal("replacement fixture did not change incarnation", incarnation, err)
	}
	if current, err := second.RCAAutoAdmissionCurrent(ctx, *next); err != nil || current {
		t.Fatal("replacement incarnation accepted", current, err)
	}
	if err := second.DeleteTest("retry-run"); err != nil {
		t.Fatal(err)
	}
	list, _, err = second.ListRCAAutoAdmissions(ctx, "", 4)
	if err != nil || len(list) != 0 {
		t.Fatal("deletion retained intent", list, err)
	}
	if _, err := second.EnsureRCAAutoAdmission(ctx, "retry-run"); err == nil {
		t.Fatal("deleted run admitted")
	}
}

func TestRCAAutoAdmissionKeysetBoundsAndCancellation(t *testing.T) {
	store := newHAFencingStore(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("retry-%02d", i)
		telemetryOutboxFixture(t, store, id)
		if _, err := store.EnsureRCAAutoAdmission(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	// Future versions are preserved, yet still advance the cursor.
	if err := store.SaveSetting(rcaAutoAdmissionKey("retry-00"), `{"version":2}`); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	count := 0
	for i := 0; i < 4; i++ {
		list, next, err := store.ListRCAAutoAdmissions(ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		count += len(list)
		if next == "" {
			break
		}
		if next <= cursor {
			t.Fatal("cursor did not progress")
		}
		cursor = next
	}
	if count != 5 {
		t.Fatal("keyset lost rows", count)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.EnsureRCAAutoAdmission(canceled, "retry-01"); err == nil {
		t.Fatal("canceled enqueue succeeded")
	}
	if _, _, err := store.ListRCAAutoAdmissions(ctx, "", 33); err == nil {
		t.Fatal("unbounded scan accepted")
	}
	if _, err := store.EnsureRCAAutoAdmission(ctx, "retry-00"); err == nil {
		t.Fatal("future version overwritten")
	}
}

func TestRCAAutoAdmissionPublicationFencesAndLegacyProject(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			store := newHAFencingStore(t)
			ctx := context.Background()
			telemetryOutboxFixture(t, store, "fenced-run")
			if legacy {
				if _, err := store.db.Exec(`UPDATE tests SET project_id=NULL WHERE id='fenced-run'`); err != nil {
					t.Fatal(err)
				}
			}
			intent, err := store.EnsureRCAAutoAdmission(ctx, "fenced-run")
			if err != nil || intent.ProjectID != DefaultProjectID {
				t.Fatal("legacy owner not normalized", intent, err)
			}
			evidence, err := store.AppendRCAEvidenceForAutoAdmission(ctx, *intent, []byte(`{"test":true}`), time.Now(), nil)
			if err != nil || evidence == nil {
				t.Fatal("fenced capture", err)
			}
			// The new attachment token fences both the evidence and analysis publication
			// paths, even though the underlying run incarnation itself has not changed.
			if _, err := store.EnsureRCAAutoAdmission(ctx, "fenced-run"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AppendRCAEvidenceForAutoAdmission(ctx, *intent, []byte(`{"test":false}`), time.Now(), nil); !errors.Is(err, ErrRCAAutoAdmissionSuperseded) {
				t.Fatal("stale evidence token accepted", err)
			}
			if _, _, err := store.CreateRCAAnalysisForAutoAdmission(ctx, *intent, strings.Repeat("a", 64), []byte(`{"test":true}`), time.Now()); !errors.Is(err, ErrRCAAutoAdmissionSuperseded) {
				t.Fatal("stale queue token accepted", err)
			}
			active, err := store.EnsureRCAAutoAdmission(ctx, "fenced-run")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`UPDATE tests SET initial_provenance_json=? WHERE id='fenced-run'`, `{"run_incarnation":"replacement"}`); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.CreateRCAAnalysisForAutoAdmission(ctx, *active, strings.Repeat("b", 64), []byte(`{"test":true}`), time.Now()); !errors.Is(err, ErrRCAAutoAdmissionSuperseded) {
				t.Fatal("replacement run accepted old queue admission", err)
			}
		})
	}
}
